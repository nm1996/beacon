package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nm1996/beacon/internal/registry"
)

func setupHandler() (*ServiceDiscoveryHandler, *registry.Registry) {
	r := registry.NewRegistry()
	handler := NewServiceDiscoveryHandler(r)

	return handler, r
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

func TestHeartbeat(t *testing.T) {
	handler, r := setupHandler()

	id := r.Register(&registry.Service{
		Name: "payment-service",
	})

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

	r.Register(&registry.Service{
		Name:    "payment-service",
		Address: "localhost",
		Port:    8080,
	})

	r.Register(&registry.Service{
		Name:    "user-service",
		Address: "localhost",
		Port:    8081,
	})

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

	id := r.Register(&registry.Service{
		Name: "payment-service",
	})

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
