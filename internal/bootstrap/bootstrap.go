// Package bootstrap constructs Beacon's components and connects them.
//
// It only builds the dependency graph. It does not start servers, run
// background work, or handle shutdown; those belong to the caller.
package bootstrap

import (
	"log/slog"
	"net/http"

	"github.com/nm1996/beacon/internal/api"
	"github.com/nm1996/beacon/internal/registry"
)

// Settings holds the values needed to build Beacon's components.
type Settings struct {
	// Addr is the TCP address the HTTP server listens on, such as ":8080".
	Addr string
}

// Components are the connected parts of a Beacon server.
type Components struct {
	// Registry holds the registered services. It is exposed so callers can
	// run maintenance such as cleanup against the same state the server uses.
	Registry *registry.Registry
	// Server serves the HTTP API. It is built but not started.
	Server *http.Server
}

// New builds the registry and the HTTP server that serves it. Both the API
// and the server's own internal errors are reported to logger.
func New(settings Settings, logger *slog.Logger) *Components {
	serviceRegistry := registry.New()

	return &Components{
		Registry: serviceRegistry,
		Server: &http.Server{
			Addr:     settings.Addr,
			Handler:  api.NewRouter(serviceRegistry, logger),
			ErrorLog: slog.NewLogLogger(logger.Handler(), slog.LevelError),
		},
	}
}
