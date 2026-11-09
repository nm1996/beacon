package bootstrap

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nm1996/beacon/internal/registry"
)

func TestNewUsesConfiguredAddress(t *testing.T) {
	components := New(Settings{Addr: ":9090"}, slog.New(slog.DiscardHandler))

	if components.Server.Addr != ":9090" {
		t.Fatalf("expected server address %q, got %q", ":9090", components.Server.Addr)
	}
}

func TestNewAppliesServerTimeouts(t *testing.T) {
	components := New(Settings{
		Addr:         ":0",
		ReadTimeout:  time.Second,
		WriteTimeout: 2 * time.Second,
		IdleTimeout:  3 * time.Second,
	}, slog.New(slog.DiscardHandler))

	server := components.Server

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

func TestNewConnectsServerToRegistry(t *testing.T) {
	components := New(Settings{Addr: ":0"}, slog.New(slog.DiscardHandler))

	_, err := components.Registry.Register(&registry.Service{
		Name:    "orders",
		Address: "10.0.0.1",
		Port:    8081,
	})
	if err != nil {
		t.Fatalf("register service: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/services?name=orders", nil)
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
