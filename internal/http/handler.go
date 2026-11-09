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
func (handler *ServiceDiscoveryHandler) Register(writer http.ResponseWriter, request *http.Request) {
	var input RegisterRequest

	err := mapRequest(&input, request)
	if err != nil {
		http.Error(writer, "Invalid JSON", http.StatusBadRequest)
		return
	}

	service := MapRegisterRequestToService(input)

	serviceID, err := handler.registry.Register(service)
	if err != nil {
		writeRegistryError(writer, err)
		return
	}

	writeJSON(writer, http.StatusCreated, RegisterResponse{
		ID: serviceID,
	})
}

// Heartbeat marks an instance as still alive and responds 204.
// A missing ID gets 400 and an unknown ID gets 404.
func (handler *ServiceDiscoveryHandler) Heartbeat(writer http.ResponseWriter, request *http.Request) {
	var input HeartbeatRequest

	err := mapRequest(&input, request)
	if err != nil {
		http.Error(writer, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if input.ID == "" {
		http.Error(writer, "id is required", http.StatusBadRequest)
		return
	}

	if err := handler.registry.Heartbeat(input.ID); err != nil {
		writeRegistryError(writer, err)
		return
	}

	writeStatus(writer, http.StatusNoContent)
}

// GetServices lists registered instances as a JSON array, filtered by the
// optional "name" query parameter. An empty registry gives an empty array.
func (handler *ServiceDiscoveryHandler) GetServices(writer http.ResponseWriter, request *http.Request) {
	name := request.URL.Query().Get("name")

	services := handler.registry.GetServices(name)
	result := make([]Service, 0, len(services))

	for _, service := range services {
		result = append(result, MapServiceToResponse(service))
	}

	writeJSON(writer, http.StatusOK, result)
}

// Unregister removes an instance and responds 204.
// A missing ID gets 400 and an unknown ID gets 404.
func (handler *ServiceDiscoveryHandler) Unregister(writer http.ResponseWriter, request *http.Request) {
	var input UnregisterRequest

	err := mapRequest(&input, request)
	if err != nil {
		http.Error(writer, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if input.ID == "" {
		http.Error(writer, "id is required", http.StatusBadRequest)
		return
	}

	if err := handler.registry.Unregister(input.ID); err != nil {
		writeRegistryError(writer, err)
		return
	}

	writeStatus(writer, http.StatusNoContent)
}

// writeRegistryError maps a registry error to an HTTP error response.
// Unexpected errors are logged and reported without internal details.
func writeRegistryError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, registry.ErrInvalidService):
		http.Error(writer, err.Error(), http.StatusBadRequest)
	case errors.Is(err, registry.ErrServiceNotFound):
		http.Error(writer, "Service not found", http.StatusNotFound)
	default:
		log.Printf("registry operation failed: %v", err)
		http.Error(writer, "Internal server error", http.StatusInternalServerError)
	}
}

// writeJSON sends data as a JSON body with the given status code.
func writeJSON(writer http.ResponseWriter, status int, data any) {
	writer.Header().Set("Content-Type", "application/json")
	writeStatus(writer, status)

	// The status line is already sent, so an encoding failure can only be logged.
	if err := json.NewEncoder(writer).Encode(data); err != nil {
		log.Printf("write JSON response: %v", err)
	}
}

// writeStatus sends a response with no body.
func writeStatus(writer http.ResponseWriter, status int) {
	writer.WriteHeader(status)
}

// mapRequest decodes the JSON request body into dto.
func mapRequest[T any](dto *T, request *http.Request) error {
	return json.NewDecoder(request.Body).Decode(dto)
}
