// Package bootstrap constructs Beacon's components and connects them.
//
// It only builds the dependency graph. It does not start servers, run
// background work, or handle shutdown; those belong to the caller.
package bootstrap

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/nm1996/beacon/internal/api"
	"github.com/nm1996/beacon/internal/auth"
	"github.com/nm1996/beacon/internal/cluster"
	"github.com/nm1996/beacon/internal/config"
	"github.com/nm1996/beacon/internal/credentials"
	"github.com/nm1996/beacon/internal/discovery"
	"github.com/nm1996/beacon/internal/metrics"
	"github.com/nm1996/beacon/internal/ratelimit"
	"github.com/nm1996/beacon/internal/registry/cache"
	"github.com/nm1996/beacon/internal/registry/heartbeats"
	"github.com/nm1996/beacon/internal/registry/memory"
	"github.com/nm1996/beacon/internal/registry/postgres"
)

// Components are the connected parts of a Beacon server.
type Components struct {
	// Discovery applies Beacon's rules to the configured registry. It is
	// exposed so the caller can run its cleanup worker against the same
	// state the server uses.
	Discovery *discovery.Service
	// Credentials holds the TLS files and tokens file in use, so the caller
	// can run its reload loop. It is nil when neither TLS nor
	// authentication is enabled.
	Credentials *credentials.Store
	// Server serves the HTTP API. It is built but not started. Its TLSConfig
	// is set when settings.TLS is enabled, and nil otherwise; every
	// handshake takes its certificate from Credentials.
	Server *http.Server

	// Heartbeats batches heartbeats for the storage backend, when it is
	// PostgreSQL. The caller runs it until shutdown; it is nil otherwise.
	Heartbeats *heartbeats.Registry
	// Membership records this node among those sharing the storage backend
	// and divides the rate limits by their number. The caller joins it
	// before serving and runs it until shutdown.
	Membership *cluster.Membership

	// closers release what New opened, such as database connections.
	closers []func()
}

// Close releases what New opened, such as database connections. Call it
// once the server and workers have stopped.
func (components *Components) Close() {
	for _, closer := range slices.Backward(components.closers) {
		closer()
	}
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
	registry, nodes, closeRegistry, err := newRegistry(settings.Storage)
	if err != nil {
		return nil, err
	}

	// Close the registry if building anything after it fails.
	built := false
	defer func() {
		if !built {
			closeRegistry()
		}
	}()

	// A backend that can write many heartbeats at once gets them batched.
	var batched *heartbeats.Registry
	if writer, ok := registry.(heartbeats.Writer); ok && settings.Storage.Postgres.HeartbeatFlushInterval > 0 {
		batched = heartbeats.New(registry, writer, settings.Storage.Postgres.HeartbeatFlushInterval, logger)
		registry = batched
	}

	var observer *metrics.Metrics
	if settings.Metrics.Enabled {
		observer = metrics.New(version)
		// Count instances straight from the backend, so scrapes do not show
		// up in the registry operation metrics they report.
		observer.WatchInstances(registry.List)
		registry = observer.InstrumentRegistry(registry)
	}

	// Outside the metrics, so they count the lookups that reach the backend.
	registry = cache.New(registry, settings.Registry.CacheTTL)

	var reloaded func(result string)
	if observer != nil {
		reloaded = observer.CredentialsReloaded
	}

	store, err := credentials.New(settings.TLS, settings.Auth, logger, reloaded)
	if err != nil {
		return nil, err
	}

	service, err := discovery.New(registry, discovery.Config{
		TTL:                  settings.Registry.ServiceTTL,
		CleanupInterval:      settings.Registry.CleanupInterval,
		MaxNameLength:        settings.Registry.MaxNameLength,
		MaxAddressLength:     settings.Registry.MaxAddressLength,
		MaxInstances:         settings.Registry.MaxInstances,
		MaxInstancesPerOwner: settings.Registry.MaxInstancesPerOwner,
	}, logger)
	if err != nil {
		return nil, fmt.Errorf("build discovery: %w", err)
	}

	var (
		failed      func(reason string)
		rateLimited func(limit string)
	)

	if observer != nil {
		failed = observer.AuthFailed
		rateLimited = observer.RateLimited
	}

	limits := settings.RateLimit

	// Load has validated the ranges already.
	ranges, err := settings.Server.TrustedProxyRanges()
	if err != nil {
		return nil, fmt.Errorf("trusted proxies: %w", err)
	}

	proxies := api.TrustedProxies(ranges)

	registrationLimit := ratelimit.New(limits.RegistrationsPerSecond, limits.RegistrationBurst, 0)
	failureLimit := ratelimit.New(limits.AuthFailuresPerSecond, limits.AuthFailureBurst, 0)

	membership := cluster.New(nodes, cluster.Node{
		ID:        nodeID(settings.Server.NodeID),
		Address:   settings.Server.Addr(),
		Version:   version,
		StartedAt: time.Now(),
	}, logger, func(live int) {
		// Each node sees about 1/live of every client's requests.
		registrationLimit.SetShare(live)
		failureLimit.SetShare(live)

		if observer != nil {
			observer.ClusterNodes(live)
		}
	})

	var guard *api.Guard
	if settings.Auth.Enabled {
		guard = api.NewGuard(store, logger, failed, api.WithTrustedProxies(proxies), api.WithFailureLimit(
			failureLimit,
			func() {
				if rateLimited != nil {
					rateLimited(api.LimitAuthFailures)
				}
			},
		))
	}

	handler := api.NewRouter(service, logger, api.Config{
		MaxRequestBodyBytes: settings.Server.MaxRequestBodyBytes,
		Version:             version,
		Guard:               guard,
		RegistrationLimit:   registrationLimit,
		RateLimited:         rateLimited,
		TrustedProxies:      proxies,
		Nodes:               nodeLister(membership),
	})

	if observer != nil {
		handler = withMetrics(handler, observer, guard)
	}

	server := &http.Server{
		Addr:              settings.Server.Addr(),
		Handler:           handler,
		ReadHeaderTimeout: settings.Server.ReadTimeout,
		ReadTimeout:       settings.Server.ReadTimeout,
		WriteTimeout:      settings.Server.WriteTimeout,
		IdleTimeout:       settings.Server.IdleTimeout,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}

	if store != nil {
		server.TLSConfig = store.ServerTLSConfig()
	}

	built = true

	return &Components{
		Discovery:   service,
		Credentials: store,
		Server:      server,
		Heartbeats:  batched,
		Membership:  membership,
		closers:     []func(){closeRegistry},
	}, nil
}

// postgresOpenTimeout bounds connecting to PostgreSQL and migrating it at
// startup.
const postgresOpenTimeout = 30 * time.Second

// newRegistry builds the registry backend named by settings.Backend, the
// store where nodes sharing it record themselves, and a function that
// releases both. Adding a backend means adding a case here and its name in
// internal/config.
func newRegistry(settings config.StorageConfig) (discovery.Registry, cluster.Store, func(), error) {
	switch settings.Backend {
	case config.StorageMemory:
		// Nothing is shared, so this node is alone.
		return memory.New(), cluster.NewLocal(), func() {}, nil
	case config.StoragePostgres:
		ctx, cancel := context.WithTimeout(context.Background(), postgresOpenTimeout)
		defer cancel()

		registry, err := postgres.Open(ctx, settings.Postgres.URL)
		if err != nil {
			return nil, nil, nil, err
		}

		return registry, registry, registry.Close, nil
	default:
		return nil, nil, nil, fmt.Errorf("unknown storage backend %q", settings.Backend)
	}
}

// nodeID returns configured, or a random ID when it is empty.
func nodeID(configured string) string {
	if configured != "" {
		return configured
	}

	return "node-" + strings.ToLower(rand.Text()[:12])
}

// nodeLister lists membership's nodes for the API.
func nodeLister(membership *cluster.Membership) func(ctx context.Context) ([]api.Node, error) {
	return func(ctx context.Context) ([]api.Node, error) {
		nodes, err := membership.Nodes(ctx)
		if err != nil {
			return nil, err
		}

		result := make([]api.Node, 0, len(nodes))
		for _, node := range nodes {
			node.StartedAt = node.StartedAt.UTC()
			node.LastSeen = node.LastSeen.UTC()
			result = append(result, api.Node(node))
		}

		return result, nil
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
