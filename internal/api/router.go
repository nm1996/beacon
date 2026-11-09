// Package api exposes the service registry over a JSON HTTP API.
//
// Handlers only decode requests, call the registry and encode responses;
// the rules about what is valid live in the registry package.
package api

import (
	"log/slog"
	"net/http"
)

// NewRouter returns a handler that serves the registry API routes backed by
// store. Requests with an unsupported method get 405 and unknown paths get 404.
func NewRouter(store ServiceStore, logger *slog.Logger) http.Handler {
	handler := NewHandler(store, logger)

	mux := http.NewServeMux()

	mux.HandleFunc("POST /register", handler.Register)
	mux.HandleFunc("POST /heartbeat", handler.Heartbeat)
	mux.HandleFunc("GET /services", handler.GetServices)
	mux.HandleFunc("DELETE /unregister", handler.Unregister)
	mux.HandleFunc("GET /health", handler.Health)

	return mux
}
