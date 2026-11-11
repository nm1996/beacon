// Package bootstrap constructs Beacon's components and connects them.
//
// It only builds the dependency graph. It does not start servers, run
// background work, or handle shutdown; those belong to the caller.
package bootstrap

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/nm1996/beacon/internal/api"
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
	// Server serves the HTTP API. It is built but not started.
	Server *http.Server
}

// New builds the registry backend chosen by settings.Storage, with metrics
// around it and the API when settings.Metrics is enabled, the discovery layer on top of it and the
// HTTP server that serves it, configured by settings. Both the API and the
// server's own internal errors are reported to logger, and version is what
// GET /health reports.
func New(settings config.Config, logger *slog.Logger, version string) (*Components, error) {
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

	handler := api.NewRouter(service, logger, api.Config{
		MaxRequestBodyBytes: settings.Server.MaxRequestBodyBytes,
		Version:             version,
	})

	if observer != nil {
		handler = withMetrics(handler, observer)
	}

	return &Components{
		Discovery: service,
		Server: &http.Server{
			Addr:              settings.Server.Addr(),
			Handler:           handler,
			ReadHeaderTimeout: settings.Server.ReadTimeout,
			ReadTimeout:       settings.Server.ReadTimeout,
			WriteTimeout:      settings.Server.WriteTimeout,
			IdleTimeout:       settings.Server.IdleTimeout,
			ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
		},
	}, nil
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
// api, which it counts and times. /metrics itself is left out of the HTTP
// metrics so scrapes do not inflate them.
func withMetrics(api http.Handler, observer *metrics.Metrics) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", observer.Handler())
	mux.Handle("/", observer.InstrumentHTTP(api))

	return mux
}
