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

// New builds the registry backend, the discovery layer on top of it and the
// HTTP server that serves it, configured by settings. Both the API and the
// server's own internal errors are reported to logger, and version is what
// GET /health reports.
func New(settings config.Config, logger *slog.Logger, version string) (*Components, error) {
	service, err := discovery.New(memory.New(), discovery.Config{
		TTL:             settings.Registry.ServiceTTL,
		CleanupInterval: settings.Registry.CleanupInterval,
	}, logger)
	if err != nil {
		return nil, fmt.Errorf("build discovery: %w", err)
	}

	return &Components{
		Discovery: service,
		Server: &http.Server{
			Addr: settings.Server.Addr(),
			Handler: api.NewRouter(service, logger, api.Config{
				MaxRequestBodyBytes: settings.Server.MaxRequestBodyBytes,
				Version:             version,
			}),
			ReadHeaderTimeout: settings.Server.ReadTimeout,
			ReadTimeout:       settings.Server.ReadTimeout,
			WriteTimeout:      settings.Server.WriteTimeout,
			IdleTimeout:       settings.Server.IdleTimeout,
			ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
		},
	}, nil
}
