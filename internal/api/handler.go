package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/nm1996/beacon/internal/auth"
	"github.com/nm1996/beacon/internal/discovery"
)

// Discovery is the business layer the API needs. It is declared here, where
// it is used, so handlers depend only on these methods and tests can swap
// in a fake. *discovery.Service satisfies it.
type Discovery interface {
	Register(ctx context.Context, registration discovery.Registration, caller discovery.Caller) (discovery.Instance, error)
	Heartbeat(ctx context.Context, id string, caller discovery.Caller) error
	Discover(ctx context.Context, name string) ([]discovery.Instance, error)
	Unregister(ctx context.Context, id string, caller discovery.Caller) error
	TTL() time.Duration
}

// Config holds the settings the API needs from Beacon's configuration.
type Config struct {
	// MaxRequestBodyBytes is the largest request body accepted.
	MaxRequestBodyBytes int64
	// Version is the running build's version, reported by GET /health.
	Version string
	// Guard protects every route except GET /health. Nil turns
	// authentication off.
	Guard *Guard
}

// Handler serves the registry API endpoints. Every method is an
// http.HandlerFunc; NewRouter decides which path and method reach each one.
// Handlers only translate between HTTP and the Discovery calls; the rules
// live in the discovery package.
type Handler struct {
	discovery Discovery
	logger    *slog.Logger
	config    Config
}

// NewHandler returns a handler backed by discovery that reports unexpected
// failures to logger and follows config.
func NewHandler(discovery Discovery, logger *slog.Logger, config Config) *Handler {
	return &Handler{
		discovery: discovery,
		logger:    logger,
		config:    config,
	}
}

// Register adds a new service instance and responds 201 with its ID and the
// TTL within which it must send heartbeats. The Location header points at
// the new instance. Malformed bodies or invalid fields get 400.
func (handler *Handler) Register(writer http.ResponseWriter, request *http.Request) {
	var input RegisterRequest

	if err := mapRequest(writer, request, &input, handler.config.MaxRequestBodyBytes); err != nil {
		handler.writeInvalidBody(writer)
		return
	}

	instance, err := handler.discovery.Register(request.Context(), mapRegisterRequest(input), handler.caller(request))
	if err != nil {
		handler.writeDiscoveryError(writer, request, err)
		return
	}

	writer.Header().Set("Location", servicesPath+"/"+url.PathEscape(instance.ID))
	handler.writeJSON(writer, http.StatusCreated, RegisterResponse{
		ID:         instance.ID,
		TTLSeconds: int64(handler.discovery.TTL() / time.Second),
	})
}

// Heartbeat marks the instance named in the path as still alive and
// responds 204. An unknown ID, including one that already expired, gets 404.
func (handler *Handler) Heartbeat(writer http.ResponseWriter, request *http.Request) {
	if err := handler.discovery.Heartbeat(request.Context(), request.PathValue("id"), handler.caller(request)); err != nil {
		handler.writeDiscoveryError(writer, request, err)
		return
	}

	writeStatus(writer, http.StatusNoContent)
}

// GetServices lists registered instances as a JSON array, filtered by the
// optional "name" query parameter. An empty registry gives an empty array.
func (handler *Handler) GetServices(writer http.ResponseWriter, request *http.Request) {
	instances, err := handler.discovery.Discover(request.Context(), request.URL.Query().Get("name"))
	if err != nil {
		handler.writeDiscoveryError(writer, request, err)
		return
	}

	result := make([]Service, 0, len(instances))
	for _, instance := range instances {
		result = append(result, mapInstanceResponse(instance))
	}

	handler.writeJSON(writer, http.StatusOK, result)
}

// Unregister removes the instance named in the path and responds 204. An
// unknown ID gets 404.
func (handler *Handler) Unregister(writer http.ResponseWriter, request *http.Request) {
	if err := handler.discovery.Unregister(request.Context(), request.PathValue("id"), handler.caller(request)); err != nil {
		handler.writeDiscoveryError(writer, request, err)
		return
	}

	writeStatus(writer, http.StatusNoContent)
}

// caller describes who sent request to the discovery layer. Without
// authentication everyone may change every instance, as before ownership
// existed. With it, the caller is the identity the guard found, and may
// change instances other callers registered only with the manage
// permission. A request that somehow reached a handler without passing the
// guard gets a nameless caller, which may only change unowned instances.
func (handler *Handler) caller(request *http.Request) discovery.Caller {
	if handler.config.Guard == nil {
		return discovery.Caller{AnyInstance: true}
	}

	identity, ok := identityFrom(request.Context())
	if !ok {
		return discovery.Caller{}
	}

	return discovery.Caller{Name: identity.Name, AnyInstance: identity.Allows(auth.PermissionManage)}
}

// Health reports that the process is up and serving requests. It always
// responds 200 with {"status":"ok","version":"..."} and does not touch the
// registry.
func (handler *Handler) Health(writer http.ResponseWriter, _ *http.Request) {
	handler.writeJSON(writer, http.StatusOK, HealthResponse{Status: "ok", Version: handler.config.Version})
}

// writeJSON sends data as a JSON body with the given status code.
func (handler *Handler) writeJSON(writer http.ResponseWriter, status int, data any) {
	writeJSON(handler.logger, writer, status, data)
}

// writeJSON sends data as a JSON body with the given status code, logging
// to logger if encoding fails.
func writeJSON(logger *slog.Logger, writer http.ResponseWriter, status int, data any) {
	writer.Header().Set("Content-Type", "application/json")
	writeStatus(writer, status)

	// The status line is already sent, so an encoding failure can only be logged.
	if err := json.NewEncoder(writer).Encode(data); err != nil {
		logger.Error("write JSON response", slog.Any("error", err))
	}
}

// writeStatus sends a response with no body.
func writeStatus(writer http.ResponseWriter, status int) {
	writer.WriteHeader(status)
}
