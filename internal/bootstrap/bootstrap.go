// Package bootstrap constructs Beacon's components and connects them.
//
// It only builds the dependency graph. It does not start servers, run
// background work, or handle shutdown; those belong to the caller.
package bootstrap

import (
	"net/http"

	httpapi "github.com/nm1996/beacon/internal/http"
	"github.com/nm1996/beacon/internal/registry"
)

// Settings holds the values needed to build Beacon's components.
type Settings struct {
	// Addr is the TCP address the HTTP server listens on, such as ":8080".
	Addr string
}

// Components are the connected parts of a Beacon server.
type Components struct {
	Registry *registry.Registry
	Server   *http.Server
}

// New builds the registry and the HTTP server that serves it.
func New(settings Settings) *Components {
	serviceRegistry := registry.NewRegistry()

	return &Components{
		Registry: serviceRegistry,
		Server: &http.Server{
			Addr:    settings.Addr,
			Handler: httpapi.NewRouter(serviceRegistry),
		},
	}
}
