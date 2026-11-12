// Package bootstrap constructs Beacon's components and connects them.
//
// It only builds the dependency graph. It does not start servers, run
// background work, or handle shutdown; those belong to the caller.
package bootstrap

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/nm1996/beacon/internal/api"
	"github.com/nm1996/beacon/internal/auth"
	"github.com/nm1996/beacon/internal/config"
	"github.com/nm1996/beacon/internal/discovery"
	"github.com/nm1996/beacon/internal/metrics"
	"github.com/nm1996/beacon/internal/registry/memory"
)

// Components are the connected parts of a Beacon server.
type Components struct {
	// Discovery applies Beacon's rules to the configured registry. It is
	// exposed so the caller can run its cleanup worker against the same
	// state the server uses.
	Discovery *discovery.Service
	// Server serves the HTTP API. It is built but not started. Its TLSConfig
	// is set, with the certificate already loaded, when settings.TLS is
	// enabled, and nil otherwise.
	Server *http.Server
}

// New builds the registry backend chosen by settings.Storage, with metrics
// around it and the API when settings.Metrics is enabled, the discovery layer on top of it and the
// HTTP server that serves it, configured by settings. When settings.TLS is
// enabled it loads the certificate, key and any client CA bundle, and when
// settings.Auth is enabled it loads the tokens file, so unusable files fail
// here, before anything listens. With auth on, every route but /health,
// /metrics included, needs a token or verified client certificate with the
// matching permission. Both the API and the
// server's own internal errors are reported to logger, and version is what
// GET /health reports.
func New(settings config.Config, logger *slog.Logger, version string) (*Components, error) {
	tlsConfig, err := newTLSConfig(settings.TLS)
	if err != nil {
		return nil, err
	}

	var identities *auth.Identities
	if settings.Auth.Enabled {
		identities, err = loadIdentities(settings, logger)
		if err != nil {
			return nil, err
		}
	}

	registry, err := newRegistry(settings.Storage)
	if err != nil {
		return nil, err
	}

	var observer *metrics.Metrics
	if settings.Metrics.Enabled {
		observer = metrics.New(version)
		// Count instances straight from the backend, so scrapes do not show
		// up in the registry operation metrics they report.
		observer.WatchInstances(registry.List)
		registry = observer.InstrumentRegistry(registry)
	}

	service, err := discovery.New(registry, discovery.Config{
		TTL:             settings.Registry.ServiceTTL,
		CleanupInterval: settings.Registry.CleanupInterval,
	}, logger)
	if err != nil {
		return nil, fmt.Errorf("build discovery: %w", err)
	}

	var guard *api.Guard
	if identities != nil {
		var failed func(reason string)
		if observer != nil {
			failed = observer.AuthFailed
		}

		guard = api.NewGuard(identities, logger, failed)
	}

	handler := api.NewRouter(service, logger, api.Config{
		MaxRequestBodyBytes: settings.Server.MaxRequestBodyBytes,
		Version:             version,
		Guard:               guard,
	})

	if observer != nil {
		handler = withMetrics(handler, observer, guard)
	}

	return &Components{
		Discovery: service,
		Server: &http.Server{
			Addr:              settings.Server.Addr(),
			Handler:           handler,
			TLSConfig:         tlsConfig,
			ReadHeaderTimeout: settings.Server.ReadTimeout,
			ReadTimeout:       settings.Server.ReadTimeout,
			WriteTimeout:      settings.Server.WriteTimeout,
			IdleTimeout:       settings.Server.IdleTimeout,
			ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
		},
	}, nil
}

// newTLSConfig loads the certificate and key named by settings, or returns nil
// when TLS is not enabled. With a client CA bundle it also asks clients for
// a certificate and verifies any they send against it. Clients that send
// none can still connect, so /health stays reachable and bearer tokens keep
// working; the API's guard decides per route whether a caller is known.
func newTLSConfig(settings config.TLSConfig) (*tls.Config, error) {
	if !settings.Enabled() {
		return nil, nil
	}

	certificate, err := tls.LoadX509KeyPair(settings.CertFile, settings.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("load TLS certificate %s and key %s: %w", settings.CertFile, settings.KeyFile, err)
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{certificate},
		MinVersion:   tls.VersionTLS12,
	}

	if settings.ClientCAFile != "" {
		pool, err := loadCertificatePool(settings.ClientCAFile)
		if err != nil {
			return nil, err
		}

		tlsConfig.ClientCAs = pool
		tlsConfig.ClientAuth = tls.VerifyClientCertIfGiven
	}

	return tlsConfig, nil
}

// loadCertificatePool reads a PEM bundle of CA certificates.
func loadCertificatePool(path string) (*x509.CertPool, error) {
	contents, err := os.ReadFile(path) //nolint:gosec // The path is the operator's own tls.client_ca_file setting.
	if err != nil {
		return nil, fmt.Errorf("load TLS client CA file: %w", err)
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(contents) {
		return nil, fmt.Errorf("load TLS client CA file %s: no PEM certificates found", path)
	}

	return pool, nil
}

// loadIdentities loads the tokens file named by settings.Auth. A client CA
// without any [[certificates]] entries is refused, since every certificate
// would then be turned away. Entries without a client CA are only warned
// about: they never match, but may be prepared ahead of turning mTLS on.
func loadIdentities(settings config.Config, logger *slog.Logger) (*auth.Identities, error) {
	identities, err := auth.Load(settings.Auth.TokensFile)
	if err != nil {
		return nil, err
	}

	switch {
	case settings.TLS.ClientCAFile != "" && !identities.HasCertificates():
		return nil, fmt.Errorf("tls.client_ca_file is set but tokens file %s has no [[certificates]] entries, so no client certificate could be used",
			settings.Auth.TokensFile)
	case settings.TLS.ClientCAFile == "" && identities.HasCertificates():
		logger.Warn("tokens file lists [[certificates]] but tls.client_ca_file is not set, so client certificates are not accepted",
			slog.String("tokens_file", settings.Auth.TokensFile))
	}

	return identities, nil
}

// newRegistry builds the registry backend named by settings.Backend. Adding a
// backend means adding a case here and its name in internal/config.
func newRegistry(settings config.StorageConfig) (discovery.Registry, error) {
	switch settings.Backend {
	case config.StorageMemory:
		return memory.New(), nil
	default:
		return nil, fmt.Errorf("unknown storage backend %q", settings.Backend)
	}
}

// withMetrics serves GET /metrics from observer and everything else from
// routes, which it counts and times. /metrics itself is left out of the HTTP
// metrics so scrapes do not inflate them, and needs the metrics permission
// when guard is not nil.
func withMetrics(routes http.Handler, observer *metrics.Metrics, guard *api.Guard) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", guard.Require(auth.PermissionMetrics, observer.Handler()))
	mux.Handle("/", observer.InstrumentHTTP(routes))

	return mux
}
