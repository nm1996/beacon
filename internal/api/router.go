// Package api exposes the service registry over a JSON HTTP API.
//
// Handlers only decode requests, call the registry and encode responses;
// the rules about what is valid live in the registry package.
package api

import (
	"log/slog"
	"net/http"

	"github.com/nm1996/beacon/internal/auth"
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
// both in the JSON error format. With config.Guard set, listing needs the
// discover permission, the other service routes need register, and
// /health stays open so liveness checks need no token. With
// config.RegistrationLimit set, registrations beyond it get 429.
func NewRouter(discovery Discovery, logger *slog.Logger, config Config) http.Handler {
	handler := NewHandler(discovery, logger, config)
	guard := config.Guard

	mux := http.NewServeMux()

	mux.Handle("POST "+servicesPath, guard.Require(auth.PermissionRegister, handler.limitRegistrations(http.HandlerFunc(handler.Register))))
	mux.Handle("GET "+servicesPath, guard.Require(auth.PermissionDiscover, http.HandlerFunc(handler.GetServices)))
	mux.Handle("POST "+servicesPath+"/{id}/heartbeat", guard.Require(auth.PermissionRegister, http.HandlerFunc(handler.Heartbeat)))
	mux.Handle("DELETE "+servicesPath+"/{id}", guard.Require(auth.PermissionRegister, http.HandlerFunc(handler.Unregister)))
	mux.HandleFunc("GET /health", handler.Health)

	return handler.withJSONErrors(mux)
}
