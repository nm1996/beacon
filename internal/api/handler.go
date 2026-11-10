package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"time"

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

// Config holds the settings the API needs from Beacon's configuration.
type Config struct {
	// MaxRequestBodyBytes is the largest request body accepted.
	MaxRequestBodyBytes int64
	// ServiceTTL is how long an instance may go without a heartbeat. It is
	// reported to clients when they register so they can pace heartbeats.
	ServiceTTL time.Duration
	// Version is the running build's version, reported by GET /health.
	Version string
}

// Handler serves the registry API endpoints. Every method is an
// http.HandlerFunc; NewRouter decides which path and method reach each one.
type Handler struct {
	store  ServiceStore
	logger *slog.Logger
	config Config
}

// NewHandler returns a handler that keeps services in store, reports
// registrations and unexpected failures to logger, and follows config.
func NewHandler(store ServiceStore, logger *slog.Logger, config Config) *Handler {
	return &Handler{
		store:  store,
		logger: logger,
		config: config,
	}
}

// Register adds a new service instance and responds 201 with its ID and the
// TTL within which it must send heartbeats. The Location header points at
// the new instance. Malformed bodies or invalid service fields get 400.
func (handler *Handler) Register(writer http.ResponseWriter, request *http.Request) {
	var input RegisterRequest

	if err := mapRequest(writer, request, &input, handler.config.MaxRequestBodyBytes); err != nil {
		handler.writeInvalidBody(writer)
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

	writer.Header().Set("Location", servicesPath+"/"+url.PathEscape(serviceID))
	handler.writeJSON(writer, http.StatusCreated, RegisterResponse{
		ID:         serviceID,
		TTLSeconds: int64(handler.config.ServiceTTL / time.Second),
	})
}

// Heartbeat marks the instance named in the path as still alive and
// responds 204. An unknown ID, including one that already expired, gets 404.
func (handler *Handler) Heartbeat(writer http.ResponseWriter, request *http.Request) {
	if err := handler.store.Heartbeat(request.PathValue("id")); err != nil {
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

// Unregister removes the instance named in the path and responds 204. An
// unknown ID gets 404.
func (handler *Handler) Unregister(writer http.ResponseWriter, request *http.Request) {
	serviceID := request.PathValue("id")

	if err := handler.store.Unregister(serviceID); err != nil {
		handler.writeRegistryError(writer, err)
		return
	}

	handler.logger.InfoContext(request.Context(), "service unregistered", slog.String("id", serviceID))

	writeStatus(writer, http.StatusNoContent)
}

// Health reports that the process is up and serving requests. It always
// responds 200 with {"status":"ok","version":"..."} and does not touch the
// registry.
func (handler *Handler) Health(writer http.ResponseWriter, _ *http.Request) {
	handler.writeJSON(writer, http.StatusOK, HealthResponse{Status: "ok", Version: handler.config.Version})
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
