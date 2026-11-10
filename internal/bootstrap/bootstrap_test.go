package bootstrap

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nm1996/beacon/internal/config"
	"github.com/nm1996/beacon/internal/discovery"
)

// discard is a logger that drops everything.
var discard = slog.New(slog.DiscardHandler)

// mustNew builds the components and fails the test if that is rejected.
func mustNew(t *testing.T, settings config.Config, version string) *Components {
	t.Helper()

	components, err := New(settings, discard, version)
	if err != nil {
		t.Fatalf("new components: %v", err)
	}

	return components
}

func TestNewUsesConfiguredAddress(t *testing.T) {
	settings := config.Default()
	settings.Server.Host = "127.0.0.1"
	settings.Server.Port = 9090

	components := mustNew(t, settings, "v0.0.0-test")

	if components.Server.Addr != "127.0.0.1:9090" {
		t.Fatalf("expected server address %q, got %q", "127.0.0.1:9090", components.Server.Addr)
	}
}

func TestNewAppliesServerTimeouts(t *testing.T) {
	settings := config.Default()
	settings.Server.ReadTimeout = time.Second
	settings.Server.WriteTimeout = 2 * time.Second
	settings.Server.IdleTimeout = 3 * time.Second

	server := mustNew(t, settings, "v0.0.0-test").Server

	if server.ReadHeaderTimeout != time.Second || server.ReadTimeout != time.Second {
		t.Errorf("expected read timeouts of 1s, got header %v, read %v", server.ReadHeaderTimeout, server.ReadTimeout)
	}

	if server.WriteTimeout != 2*time.Second {
		t.Errorf("expected write timeout 2s, got %v", server.WriteTimeout)
	}

	if server.IdleTimeout != 3*time.Second {
		t.Errorf("expected idle timeout 3s, got %v", server.IdleTimeout)
	}
}

func TestNewAppliesRequestBodyLimit(t *testing.T) {
	settings := config.Default()
	settings.Server.MaxRequestBodyBytes = 40

	handler := mustNew(t, settings, "v0.0.0-test").Server.Handler

	tests := []struct {
		name       string
		body       string
		wantStatus int
	}{
		{name: "within limit", body: `{"name":"a","address":"b","port":1}`, wantStatus: http.StatusCreated},
		{name: "over limit", body: `{"name":"orders","address":"10.0.0.1","port":8081}`, wantStatus: http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/services", strings.NewReader(tt.body))
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("expected status %d, got %d", tt.wantStatus, rec.Code)
			}
		})
	}
}

func TestNewConnectsServerToDiscovery(t *testing.T) {
	components := mustNew(t, config.Default(), "v0.0.0-test")

	_, err := components.Discovery.Register(t.Context(), discovery.Registration{
		Name:    "orders",
		Address: "10.0.0.1",
		Port:    8081,
	})
	if err != nil {
		t.Fatalf("register service: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/services?name=orders", nil)
	rec := httptest.NewRecorder()

	components.Server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	var services []map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&services); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if len(services) != 1 {
		t.Fatalf("expected 1 service from the wired registry, got %d", len(services))
	}
}

func TestNewReportsConfiguredTTLToClients(t *testing.T) {
	settings := config.Default()
	settings.Registry.ServiceTTL = 45 * time.Second

	handler := mustNew(t, settings, "v0.0.0-test").Server.Handler

	req := httptest.NewRequest(http.MethodPost, "/api/v1/services", strings.NewReader(`{"name":"orders","address":"10.0.0.1","port":8081}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	var response struct {
		TTLSeconds int64 `json:"ttl_seconds"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if response.TTLSeconds != 45 {
		t.Errorf("expected ttl_seconds 45, got %d", response.TTLSeconds)
	}
}

func TestNewReportsVersionOnHealth(t *testing.T) {
	handler := mustNew(t, config.Default(), "v1.2.3").Server.Handler

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	var response struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if response.Version != "v1.2.3" {
		t.Errorf("expected version %q, got %q", "v1.2.3", response.Version)
	}
}

func TestNewRejectsInvalidDiscoverySettings(t *testing.T) {
	settings := config.Default()
	settings.Registry.ServiceTTL = 0

	if _, err := New(settings, discard, "v0.0.0-test"); err == nil {
		t.Error("expected an error for a zero TTL")
	}
}

func TestNewRejectsUnknownStorageBackend(t *testing.T) {
	// Config validation normally stops this; bootstrap must still refuse
	// rather than fall back to some default.
	settings := config.Default()
	settings.Storage.Backend = "redis"

	_, err := New(settings, discard, "v0.0.0-test")

	if err == nil || !strings.Contains(err.Error(), `unknown storage backend "redis"`) {
		t.Errorf("expected an unknown backend error, got %v", err)
	}
}
