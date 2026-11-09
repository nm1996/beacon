// Package http exposes the service registry over a JSON HTTP API.
//
// Handlers only decode requests, call the registry and encode responses;
// the rules about what is valid live in the registry package.
package http

import (
	"net/http"

	"github.com/nm1996/beacon/internal/registry"
)

// NewRouter returns a handler that serves the registry API routes.
// Requests with an unsupported method get 405 and unknown paths get 404.
func NewRouter(registry *registry.Registry) http.Handler {
	handler := NewServiceDiscoveryHandler(registry)

	mux := http.NewServeMux()

	mux.HandleFunc("POST /register", handler.Register)
	mux.HandleFunc("POST /heartbeat", handler.Heartbeat)
	mux.HandleFunc("GET /services", handler.GetServices)
	mux.HandleFunc("DELETE /unregister", handler.Unregister)

	return mux
}
