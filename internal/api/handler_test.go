package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nm1996/beacon/internal/discovery"
	"github.com/nm1996/beacon/internal/registry/memory"
)

// testMaxBodyBytes is the request body limit handlers get in tests.
const testMaxBodyBytes = 64 << 10

// testTTL is the TTL the discovery layer reports in tests.
const testTTL = 30 * time.Second

// testConfig is the API configuration used in tests.
var testConfig = Config{MaxRequestBodyBytes: testMaxBodyBytes, Version: "v0.0.0-test"}

// discard is a logger that drops everything.
var discard = slog.New(slog.DiscardHandler)

// newDiscovery returns a discovery layer over an empty in-memory registry.
func newDiscovery(t *testing.T, ttl time.Duration) *discovery.Service {
	t.Helper()

	service, err := discovery.New(memory.New(), discovery.Config{TTL: ttl, CleanupInterval: time.Second}, discard)
	if err != nil {
		t.Fatalf("new discovery: %v", err)
	}

	return service
}

// setupHandler returns a handler and the empty discovery layer behind it, so
// tests can seed or inspect state directly.
func setupHandler(t *testing.T) (*Handler, *discovery.Service) {
	t.Helper()

	service := newDiscovery(t, testTTL)

	return NewHandler(service, discard, testConfig), service
}

// setupRouter returns a router and the empty discovery layer behind it.
func setupRouter(t *testing.T) (http.Handler, *discovery.Service) {
	t.Helper()

	service := newDiscovery(t, testTTL)

	return NewRouter(service, discard, testConfig), service
}

// serve sends one request through router and returns the recorded response.
func serve(router http.Handler, method, path, body string) *httptest.ResponseRecorder {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}

	req := httptest.NewRequest(method, path, reader)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	return rec
}

// mustRegister adds a valid instance and fails the test if that is rejected.
func mustRegister(t *testing.T, service *discovery.Service, name string, port int) string {
	t.Helper()

	instance, err := service.Register(t.Context(), discovery.Registration{
		Name:    name,
		Address: "localhost",
		Port:    port,
	})
	if err != nil {
		t.Fatalf("register %q: %v", name, err)
	}

	return instance.ID
}

// listAll returns the instances named name, or all of them when name is
// empty, failing the test on error.
func listAll(t *testing.T, service *discovery.Service, name string) []discovery.Instance {
	t.Helper()

	instances, err := service.Discover(t.Context(), name)
	if err != nil {
		t.Fatalf("discover %q: %v", name, err)
	}

	return instances
}

func TestRegister(t *testing.T) {
	router, r := setupRouter(t)

	rec := serve(router, http.MethodPost, "/api/v1/services", `{
		"name": "payment-service",
		"address": "localhost",
		"port": 8080
	}`)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d", http.StatusCreated, rec.Code)
	}

	var response RegisterResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if response.ID == "" {
		t.Fatal("expected generated service ID")
	}

	if response.TTLSeconds != 30 {
		t.Errorf("expected ttl_seconds 30, got %d", response.TTLSeconds)
	}

	if got, want := rec.Header().Get("Location"), "/api/v1/services/"+response.ID; got != want {
		t.Errorf("expected Location %q, got %q", want, got)
	}

	services := listAll(t, r, "payment-service")
	if len(services) != 1 || services[0].ID != response.ID {
		t.Fatalf("expected the returned ID to be registered, got %+v", services)
	}
}

func TestRegisterReportsTTLInWholeSeconds(t *testing.T) {
	router := NewRouter(newDiscovery(t, 90*time.Second+900*time.Millisecond), discard, testConfig)

	rec := serve(router, http.MethodPost, "/api/v1/services", `{"name":"orders","address":"10.0.0.1","port":8081}`)

	var response RegisterResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Rounding down keeps clients on the safe side of the real TTL.
	if response.TTLSeconds != 90 {
		t.Errorf("expected ttl_seconds 90, got %d", response.TTLSeconds)
	}
}

func TestRegisterInvalidJSON(t *testing.T) {
	router, _ := setupRouter(t)

	rec := serve(router, http.MethodPost, "/api/v1/services", `{"name":`)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}
}

func TestRegisterInvalidService(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "missing name", body: `{"address":"localhost","port":8080}`},
		{name: "missing address", body: `{"name":"payment-service","port":8080}`},
		{name: "missing port", body: `{"name":"payment-service","address":"localhost"}`},
		{name: "port out of range", body: `{"name":"payment-service","address":"localhost","port":70000}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router, r := setupRouter(t)

			rec := serve(router, http.MethodPost, "/api/v1/services", tt.body)

			if rec.Code != http.StatusBadRequest {
				t.Errorf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
			}

			if len(listAll(t, r, "")) != 0 {
				t.Error("expected invalid service not to be registered")
			}
		})
	}
}

func TestHeartbeat(t *testing.T) {
	router, r := setupRouter(t)

	id := mustRegister(t, r, "payment-service", 8080)
	before := listAll(t, r, "")[0].LastHeartbeat

	rec := serve(router, http.MethodPost, "/api/v1/services/"+id+"/heartbeat", "")

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected status %d, got %d", http.StatusNoContent, rec.Code)
	}

	if after := listAll(t, r, "")[0].LastHeartbeat; after.Before(before) {
		t.Errorf("expected LastHeartbeat to move forward, went from %v to %v", before, after)
	}
}

func TestHeartbeatUnknownService(t *testing.T) {
	router, _ := setupRouter(t)

	rec := serve(router, http.MethodPost, "/api/v1/services/unknown-id/heartbeat", "")

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected status %d, got %d", http.StatusNotFound, rec.Code)
	}
}

func TestGetServices(t *testing.T) {
	router, r := setupRouter(t)

	id := mustRegister(t, r, "payment-service", 8080)
	mustRegister(t, r, "user-service", 8081)

	rec := serve(router, http.MethodGet, "/api/v1/services?name=payment-service", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	var response []Service
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(response) != 1 {
		t.Fatalf("expected 1 service, got %d", len(response))
	}

	service := response[0]

	if service.ID != id || service.Name != "payment-service" || service.Address != "localhost" || service.Port != 8080 {
		t.Errorf("unexpected service %+v", service)
	}

	if service.LastHeartbeat.IsZero() || service.LastHeartbeat.Location() != time.UTC {
		t.Errorf("expected a UTC last_heartbeat, got %v", service.LastHeartbeat)
	}
}

func TestGetServicesEmpty(t *testing.T) {
	router, _ := setupRouter(t)

	rec := serve(router, http.MethodGet, "/api/v1/services", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	if body := strings.TrimSpace(rec.Body.String()); body != "[]" {
		t.Errorf("expected an empty JSON array, got %q", body)
	}
}

func TestUnregister(t *testing.T) {
	router, r := setupRouter(t)

	id := mustRegister(t, r, "payment-service", 8080)

	rec := serve(router, http.MethodDelete, "/api/v1/services/"+id, "")

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected status %d, got %d", http.StatusNoContent, rec.Code)
	}

	if len(listAll(t, r, "")) != 0 {
		t.Error("expected service to be removed")
	}

	if rec := serve(router, http.MethodDelete, "/api/v1/services/"+id, ""); rec.Code != http.StatusNotFound {
		t.Errorf("expected a second unregister to get %d, got %d", http.StatusNotFound, rec.Code)
	}
}

func TestUnregisterUnknownService(t *testing.T) {
	router, _ := setupRouter(t)

	rec := serve(router, http.MethodDelete, "/api/v1/services/unknown-id", "")

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected status %d, got %d", http.StatusNotFound, rec.Code)
	}
}

// failingDiscovery is a Discovery whose every operation fails with err, as
// when the storage backend is unreachable.
type failingDiscovery struct {
	err error
}

// Register always fails.
func (fake failingDiscovery) Register(context.Context, discovery.Registration) (discovery.Instance, error) {
	return discovery.Instance{}, fake.err
}

// Heartbeat always fails.
func (fake failingDiscovery) Heartbeat(context.Context, string) error { return fake.err }

// Discover always fails.
func (fake failingDiscovery) Discover(context.Context, string) ([]discovery.Instance, error) {
	return nil, fake.err
}

// Unregister always fails.
func (fake failingDiscovery) Unregister(context.Context, string) error { return fake.err }

// TTL reports the test TTL.
func (fake failingDiscovery) TTL() time.Duration { return testTTL }

func TestUnexpectedDiscoveryErrorReturnsInternalServerError(t *testing.T) {
	router := NewRouter(failingDiscovery{err: errors.New("disk on fire")}, discard, testConfig)

	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "register", method: http.MethodPost, path: "/api/v1/services", body: `{"name":"a","address":"b","port":1}`},
		{name: "heartbeat", method: http.MethodPost, path: "/api/v1/services/x/heartbeat"},
		{name: "list", method: http.MethodGet, path: "/api/v1/services"},
		{name: "unregister", method: http.MethodDelete, path: "/api/v1/services/x"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := serve(router, tt.method, tt.path, tt.body)

			if rec.Code != http.StatusInternalServerError {
				t.Errorf("expected status %d, got %d", http.StatusInternalServerError, rec.Code)
			}

			if strings.Contains(rec.Body.String(), "disk on fire") {
				t.Error("expected internal error details not to leak to the client")
			}
		})
	}
}

func TestHealth(t *testing.T) {
	router, _ := setupRouter(t)

	rec := serve(router, http.MethodGet, "/health", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	var response HealthResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if response.Status != "ok" {
		t.Errorf("expected status %q, got %q", "ok", response.Status)
	}

	if response.Version != testConfig.Version {
		t.Errorf("expected version %q, got %q", testConfig.Version, response.Version)
	}
}
