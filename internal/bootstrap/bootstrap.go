// Package bootstrap constructs Beacon's components and connects them.
//
// It only builds the dependency graph. It does not start servers, run
// background work, or handle shutdown; those belong to the caller.
package bootstrap

import (
	"log/slog"
	"net/http"

	"github.com/nm1996/beacon/internal/api"
	"github.com/nm1996/beacon/internal/config"
	"github.com/nm1996/beacon/internal/registry"
)

// Components are the connected parts of a Beacon server.
type Components struct {
	// Registry holds the registered services. It is exposed so callers can
	// run maintenance such as cleanup against the same state the server uses.
	Registry *registry.Registry
	// Server serves the HTTP API. It is built but not started.
	Server *http.Server
}

// New builds the registry and the HTTP server that serves it, configured by
// settings. Both the API and the server's own internal errors are reported
// to logger, and version is what GET /health reports.
func New(settings config.Config, logger *slog.Logger, version string) *Components {
	serviceRegistry := registry.New()

	return &Components{
		Registry: serviceRegistry,
		Server: &http.Server{
			Addr: settings.Server.Addr(),
			Handler: api.NewRouter(serviceRegistry, logger, api.Config{
				MaxRequestBodyBytes: settings.Server.MaxRequestBodyBytes,
				ServiceTTL:          settings.Registry.ServiceTTL,
				Version:             version,
			}),
			ReadHeaderTimeout: settings.Server.ReadTimeout,
			ReadTimeout:       settings.Server.ReadTimeout,
			WriteTimeout:      settings.Server.WriteTimeout,
			IdleTimeout:       settings.Server.IdleTimeout,
			ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
		},
	}
}
