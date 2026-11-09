package http

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/nm1996/beacon/internal/registry"
)

type ServiceDiscoveryHandler struct {
	registry *registry.Registry
}

func NewServiceDiscoveryHandler(registry *registry.Registry) *ServiceDiscoveryHandler {
	return &ServiceDiscoveryHandler{
		registry: registry,
	}
}

func (h *ServiceDiscoveryHandler) Register(w http.ResponseWriter, r *http.Request) {
	var input RegisterRequest

	err := mapRequest(&input, r)
	if err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	service := MapRegisterRequestToService(input)

	serviceID := h.registry.Register(service)

	writeJSON(w, http.StatusCreated, RegisterResponse{
		ID: serviceID,
	})
}

func (h *ServiceDiscoveryHandler) Heartbeat(w http.ResponseWriter, r *http.Request) {
	var input HeartbeatRequest

	err := mapRequest(&input, r)
	if err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if !h.registry.Heartbeat(input.ID) {
		http.Error(w, "Service not found", http.StatusNotFound)
		return
	}

	writeStatus(w, http.StatusNoContent)
}

func (h *ServiceDiscoveryHandler) GetServices(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")

	services := h.registry.GetServices(name)
	result := make([]Service, 0, len(services))

	for _, service := range services {
		result = append(result, MapServiceToResponse(service))
	}

	writeJSON(w, http.StatusOK, result)
}

func (h *ServiceDiscoveryHandler) Unregister(w http.ResponseWriter, r *http.Request) {
	var input UnregisterRequest

	err := mapRequest(&input, r)
	if err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if !h.registry.Unregister(input.ID) {
		http.Error(w, "Service not found", http.StatusNotFound)
		return
	}

	writeStatus(w, http.StatusNoContent)

}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	writeStatus(w, status)

	// The status line is already sent, so an encoding failure can only be logged.
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("write JSON response: %v", err)
	}
}

func writeStatus(w http.ResponseWriter, status int) {
	w.WriteHeader(status)
}

func mapRequest[T any](dto *T, r *http.Request) error {
	return json.NewDecoder(r.Body).Decode(dto)
}
