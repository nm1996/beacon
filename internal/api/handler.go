package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/nm1996/beacon/internal/registry"
)

// ServiceStore is the part of the registry the API needs. It is declared
// here, where it is used, so the handlers depend only on these four methods
// and tests can swap in a fake. *registry.Registry satisfies it.
type ServiceStore interface {
	Register(service *registry.Service) (string, error)
	Heartbeat(serviceID string) error
	GetServices(name string) []*registry.Service
	Unregister(serviceID string) error
}

// Handler serves the registry API endpoints. Every method is an
// http.HandlerFunc; NewRouter decides which path and method reach each one.
type Handler struct {
	store  ServiceStore
	logger *slog.Logger
}

// NewHandler returns a handler that keeps services in store and reports
// registrations and unexpected failures to logger.
func NewHandler(store ServiceStore, logger *slog.Logger) *Handler {
	return &Handler{
		store:  store,
		logger: logger,
	}
}

// Register adds a new service instance and responds 201 with its ID.
// Malformed JSON or invalid service fields get 400.
func (handler *Handler) Register(writer http.ResponseWriter, request *http.Request) {
	var input RegisterRequest

	if !decodeRequest(writer, request, &input) {
		return
	}

	service := mapRegisterRequest(input)

	serviceID, err := handler.store.Register(service)
	if err != nil {
		handler.writeRegistryError(writer, err)
		return
	}

	handler.logger.InfoContext(request.Context(), "service registered",
		slog.String("id", serviceID),
		slog.String("name", service.Name),
		slog.String("address", service.Address),
		slog.Int("port", service.Port),
	)

	handler.writeJSON(writer, http.StatusCreated, RegisterResponse{
		ID: serviceID,
	})
}

// Heartbeat marks an instance as still alive and responds 204.
// A missing ID gets 400 and an unknown ID gets 404.
func (handler *Handler) Heartbeat(writer http.ResponseWriter, request *http.Request) {
	var input HeartbeatRequest

	if !decodeRequest(writer, request, &input) {
		return
	}

	if input.ID == "" {
		http.Error(writer, "id is required", http.StatusBadRequest)
		return
	}

	if err := handler.store.Heartbeat(input.ID); err != nil {
		handler.writeRegistryError(writer, err)
		return
	}

	writeStatus(writer, http.StatusNoContent)
}

// GetServices lists registered instances as a JSON array, filtered by the
// optional "name" query parameter. An empty registry gives an empty array.
func (handler *Handler) GetServices(writer http.ResponseWriter, request *http.Request) {
	name := request.URL.Query().Get("name")

	services := handler.store.GetServices(name)
	result := make([]Service, 0, len(services))

	for _, service := range services {
		result = append(result, mapServiceResponse(service))
	}

	handler.writeJSON(writer, http.StatusOK, result)
}

// Unregister removes an instance and responds 204.
// A missing ID gets 400 and an unknown ID gets 404.
func (handler *Handler) Unregister(writer http.ResponseWriter, request *http.Request) {
	var input UnregisterRequest

	if !decodeRequest(writer, request, &input) {
		return
	}

	if input.ID == "" {
		http.Error(writer, "id is required", http.StatusBadRequest)
		return
	}

	if err := handler.store.Unregister(input.ID); err != nil {
		handler.writeRegistryError(writer, err)
		return
	}

	handler.logger.InfoContext(request.Context(), "service unregistered", slog.String("id", input.ID))

	writeStatus(writer, http.StatusNoContent)
}

// Health reports that the process is up and serving requests. It always
// responds 200 with {"status":"ok"} and does not touch the registry.
func (handler *Handler) Health(writer http.ResponseWriter, _ *http.Request) {
	handler.writeJSON(writer, http.StatusOK, HealthResponse{Status: "ok"})
}

// writeRegistryError maps a registry error to an HTTP error response.
// Unexpected errors are logged and reported without internal details.
func (handler *Handler) writeRegistryError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, registry.ErrInvalidService):
		http.Error(writer, err.Error(), http.StatusBadRequest)
	case errors.Is(err, registry.ErrServiceNotFound):
		http.Error(writer, "Service not found", http.StatusNotFound)
	default:
		handler.logger.Error("registry operation failed", slog.Any("error", err))
		http.Error(writer, "Internal server error", http.StatusInternalServerError)
	}
}

// writeJSON sends data as a JSON body with the given status code.
func (handler *Handler) writeJSON(writer http.ResponseWriter, status int, data any) {
	writer.Header().Set("Content-Type", "application/json")
	writeStatus(writer, status)

	// The status line is already sent, so an encoding failure can only be logged.
	if err := json.NewEncoder(writer).Encode(data); err != nil {
		handler.logger.Error("write JSON response", slog.Any("error", err))
	}
}

// writeStatus sends a response with no body.
func writeStatus(writer http.ResponseWriter, status int) {
	writer.WriteHeader(status)
}
