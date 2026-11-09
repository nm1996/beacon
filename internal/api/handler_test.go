package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nm1996/beacon/internal/registry"
)

// setupHandler returns a handler and the empty registry behind it, so tests
// can seed or inspect state directly.
func setupHandler() (*Handler, *registry.Registry) {
	r := registry.New()
	handler := NewHandler(r, slog.New(slog.DiscardHandler))

	return handler, r
}

// mustRegister adds a valid service and fails the test if that is rejected.
func mustRegister(t *testing.T, r *registry.Registry, name string, port int) string {
	t.Helper()

	id, err := r.Register(&registry.Service{
		Name:    name,
		Address: "localhost",
		Port:    port,
	})
	if err != nil {
		t.Fatalf("register %q: %v", name, err)
	}

	return id
}

func TestRegister(t *testing.T) {
	handler, r := setupHandler()

	body := `{
		"name": "payment-service",
		"address": "localhost",
		"port": 8080
	}`

	req := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(body))
	rec := httptest.NewRecorder()

	handler.Register(rec, req)

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

	services := r.GetServices("payment-service")

	if len(services) != 1 {
		t.Fatalf("expected 1 registered service, got %d", len(services))
	}
}

func TestRegisterInvalidJSON(t *testing.T) {
	handler, _ := setupHandler()

	req := httptest.NewRequest(
		http.MethodPost,
		"/register",
		strings.NewReader(`{"name":`),
	)

	rec := httptest.NewRecorder()

	handler.Register(rec, req)

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
			handler, r := setupHandler()

			req := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(tt.body))
			rec := httptest.NewRecorder()

			handler.Register(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Errorf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
			}

			if len(r.GetServices("")) != 0 {
				t.Error("expected invalid service not to be registered")
			}
		})
	}
}

func TestHeartbeat(t *testing.T) {
	handler, r := setupHandler()

	id := mustRegister(t, r, "payment-service", 8080)

	body := `{"id":"` + id + `"}`

	req := httptest.NewRequest(
		http.MethodPost,
		"/heartbeat",
		strings.NewReader(body),
	)

	rec := httptest.NewRecorder()

	handler.Heartbeat(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("expected status %d, got %d", http.StatusNoContent, rec.Code)
	}
}

func TestHeartbeatUnknownService(t *testing.T) {
	handler, _ := setupHandler()

	req := httptest.NewRequest(
		http.MethodPost,
		"/heartbeat",
		strings.NewReader(`{"id":"unknown-id"}`),
	)

	rec := httptest.NewRecorder()

	handler.Heartbeat(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected status %d, got %d", http.StatusNotFound, rec.Code)
	}
}

func TestHeartbeatMissingID(t *testing.T) {
	handler, _ := setupHandler()

	req := httptest.NewRequest(http.MethodPost, "/heartbeat", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()

	handler.Heartbeat(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}
}

func TestHeartbeatInvalidJSON(t *testing.T) {
	handler, _ := setupHandler()

	req := httptest.NewRequest(
		http.MethodPost,
		"/heartbeat",
		strings.NewReader(`{"id":`),
	)

	rec := httptest.NewRecorder()

	handler.Heartbeat(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}
}

func TestGetServices(t *testing.T) {
	handler, r := setupHandler()

	mustRegister(t, r, "payment-service", 8080)
	mustRegister(t, r, "user-service", 8081)

	req := httptest.NewRequest(
		http.MethodGet,
		"/services?name=payment-service",
		nil,
	)

	rec := httptest.NewRecorder()

	handler.GetServices(rec, req)

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

	if response[0].Name != "payment-service" {
		t.Errorf("expected payment-service, got %s", response[0].Name)
	}
}

func TestGetServicesEmpty(t *testing.T) {
	handler, _ := setupHandler()

	req := httptest.NewRequest(http.MethodGet, "/services", nil)
	rec := httptest.NewRecorder()

	handler.GetServices(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	var response []Service

	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(response) != 0 {
		t.Errorf("expected empty response, got %d services", len(response))
	}
}

func TestUnregister(t *testing.T) {
	handler, r := setupHandler()

	id := mustRegister(t, r, "payment-service", 8080)

	body := `{"id":"` + id + `"}`

	req := httptest.NewRequest(
		http.MethodDelete,
		"/unregister",
		strings.NewReader(body),
	)

	rec := httptest.NewRecorder()

	handler.Unregister(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("expected status %d, got %d", http.StatusNoContent, rec.Code)
	}

	if len(r.GetServices("")) != 0 {
		t.Error("expected service to be removed")
	}
}

func TestUnregisterUnknownService(t *testing.T) {
	handler, _ := setupHandler()

	req := httptest.NewRequest(
		http.MethodDelete,
		"/unregister",
		strings.NewReader(`{"id":"unknown-id"}`),
	)

	rec := httptest.NewRecorder()

	handler.Unregister(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected status %d, got %d", http.StatusNotFound, rec.Code)
	}
}

func TestUnregisterMissingID(t *testing.T) {
	handler, _ := setupHandler()

	req := httptest.NewRequest(http.MethodDelete, "/unregister", strings.NewReader(`{"id":""}`))
	rec := httptest.NewRecorder()

	handler.Unregister(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}
}

func TestUnregisterInvalidJSON(t *testing.T) {
	handler, _ := setupHandler()

	req := httptest.NewRequest(
		http.MethodDelete,
		"/unregister",
		strings.NewReader(`{"id":`),
	)

	rec := httptest.NewRecorder()

	handler.Unregister(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}
}

// failingStore is a ServiceStore whose every operation fails with err.
type failingStore struct {
	err error
}

// Register always fails.
func (store failingStore) Register(*registry.Service) (string, error) { return "", store.err }

// Heartbeat always fails.
func (store failingStore) Heartbeat(string) error { return store.err }

// GetServices always returns no services.
func (store failingStore) GetServices(string) []*registry.Service { return nil }

// Unregister always fails.
func (store failingStore) Unregister(string) error { return store.err }

func TestUnexpectedStoreErrorReturnsInternalServerError(t *testing.T) {
	handler := NewHandler(failingStore{err: errors.New("disk on fire")}, slog.New(slog.DiscardHandler))

	tests := []struct {
		name   string
		serve  http.HandlerFunc
		method string
		body   string
	}{
		{name: "register", serve: handler.Register, method: http.MethodPost, body: `{"name":"a","address":"b","port":1}`},
		{name: "heartbeat", serve: handler.Heartbeat, method: http.MethodPost, body: `{"id":"x"}`},
		{name: "unregister", serve: handler.Unregister, method: http.MethodDelete, body: `{"id":"x"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "/", strings.NewReader(tt.body))
			rec := httptest.NewRecorder()

			tt.serve(rec, req)

			if rec.Code != http.StatusInternalServerError {
				t.Errorf("expected status %d, got %d", http.StatusInternalServerError, rec.Code)
			}

			if strings.Contains(rec.Body.String(), "disk on fire") {
				t.Error("expected internal error details not to leak to the client")
			}
		})
	}
}
