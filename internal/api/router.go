// Package api exposes the service registry over a JSON HTTP API.
//
// Handlers only decode requests, call the registry and encode responses;
// the rules about what is valid live in the registry package.
package api

import (
	"log/slog"
	"net/http"
)

// servicesPath is the collection every versioned service route lives under.
const servicesPath = "/api/v1/services"

// NewRouter returns a handler that serves the registry API backed by
// discovery:
//
//	POST   /api/v1/services                 register an instance
//	GET    /api/v1/services?name=orders     list instances
//	POST   /api/v1/services/{id}/heartbeat  keep an instance alive
//	DELETE /api/v1/services/{id}            unregister an instance
//	GET    /health                          liveness check, not versioned
//
// Requests with an unsupported method get 405 and unknown paths get 404,
// both in the JSON error format.
func NewRouter(discovery Discovery, logger *slog.Logger, config Config) http.Handler {
	handler := NewHandler(discovery, logger, config)

	mux := http.NewServeMux()

	mux.HandleFunc("POST "+servicesPath, handler.Register)
	mux.HandleFunc("GET "+servicesPath, handler.GetServices)
	mux.HandleFunc("POST "+servicesPath+"/{id}/heartbeat", handler.Heartbeat)
	mux.HandleFunc("DELETE "+servicesPath+"/{id}", handler.Unregister)
	mux.HandleFunc("GET /health", handler.Health)

	return handler.withJSONErrors(mux)
}
