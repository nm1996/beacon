package http

import (
	"net/http"

	"github.com/nm1996/beacon/internal/registry"
)

func NewRouter(registry *registry.Registry) http.Handler {
	handler := NewServiceDiscoveryHandler(registry)

	mux := http.NewServeMux()

	mux.HandleFunc("POST /register", handler.Register)
	mux.HandleFunc("POST /heartbeat", handler.Heartbeat)
	mux.HandleFunc("GET /services", handler.GetServices)
	mux.HandleFunc("DELETE /unregister", handler.Unregister)

	return mux
}
