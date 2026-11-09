package http

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/nm1996/beacon/internal/registry"
)

// ServiceDiscoveryHandler serves the registry API endpoints. Every method is
// an http.HandlerFunc; NewRouter decides which path and method reach each one.
type ServiceDiscoveryHandler struct {
	registry *registry.Registry
}

// NewServiceDiscoveryHandler returns a handler backed by the given registry.
func NewServiceDiscoveryHandler(registry *registry.Registry) *ServiceDiscoveryHandler {
	return &ServiceDiscoveryHandler{
		registry: registry,
	}
}

// Register adds a new service instance and responds 201 with its ID.
// Malformed JSON or invalid service fields get 400.
func (h *ServiceDiscoveryHandler) Register(w http.ResponseWriter, r *http.Request) {
	var input RegisterRequest

	err := mapRequest(&input, r)
	if err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	service := MapRegisterRequestToService(input)

	serviceID, err := h.registry.Register(service)
	if err != nil {
		writeRegistryError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, RegisterResponse{
		ID: serviceID,
	})
}

// Heartbeat marks an instance as still alive and responds 204.
// A missing ID gets 400 and an unknown ID gets 404.
func (h *ServiceDiscoveryHandler) Heartbeat(w http.ResponseWriter, r *http.Request) {
	var input HeartbeatRequest

	err := mapRequest(&input, r)
	if err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if input.ID == "" {
		http.Error(w, "id is required", http.StatusBadRequest)
		return
	}

	if err := h.registry.Heartbeat(input.ID); err != nil {
		writeRegistryError(w, err)
		return
	}

	writeStatus(w, http.StatusNoContent)
}

// GetServices lists registered instances as a JSON array, filtered by the
// optional "name" query parameter. An empty registry gives an empty array.
func (h *ServiceDiscoveryHandler) GetServices(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")

	services := h.registry.GetServices(name)
	result := make([]Service, 0, len(services))

	for _, service := range services {
		result = append(result, MapServiceToResponse(service))
	}

	writeJSON(w, http.StatusOK, result)
}

// Unregister removes an instance and responds 204.
// A missing ID gets 400 and an unknown ID gets 404.
func (h *ServiceDiscoveryHandler) Unregister(w http.ResponseWriter, r *http.Request) {
	var input UnregisterRequest

	err := mapRequest(&input, r)
	if err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if input.ID == "" {
		http.Error(w, "id is required", http.StatusBadRequest)
		return
	}

	if err := h.registry.Unregister(input.ID); err != nil {
		writeRegistryError(w, err)
		return
	}

	writeStatus(w, http.StatusNoContent)
}

// writeRegistryError maps a registry error to an HTTP error response.
// Unexpected errors are logged and reported without internal details.
func writeRegistryError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, registry.ErrInvalidService):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, registry.ErrServiceNotFound):
		http.Error(w, "Service not found", http.StatusNotFound)
	default:
		log.Printf("registry operation failed: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

// writeJSON sends data as a JSON body with the given status code.
func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	writeStatus(w, status)

	// The status line is already sent, so an encoding failure can only be logged.
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("write JSON response: %v", err)
	}
}

// writeStatus sends a response with no body.
func writeStatus(w http.ResponseWriter, status int) {
	w.WriteHeader(status)
}

// mapRequest decodes the JSON request body into dto.
func mapRequest[T any](dto *T, r *http.Request) error {
	return json.NewDecoder(r.Body).Decode(dto)
}
