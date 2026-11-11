package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nm1996/beacon/internal/config"
)

func TestHealthURL(t *testing.T) {
	tests := []struct {
		host string
		want string
	}{
		{host: "0.0.0.0", want: "http://127.0.0.1:8080/health"},
		{host: "", want: "http://127.0.0.1:8080/health"},
		{host: "::", want: "http://127.0.0.1:8080/health"},
		{host: "10.0.0.5", want: "http://10.0.0.5:8080/health"},
		{host: "::1", want: "http://[::1]:8080/health"},
	}

	for _, tt := range tests {
		if got := healthURL(config.ServerConfig{Host: tt.host, Port: 8080}); got != tt.want {
			t.Errorf("host %q: expected %q, got %q", tt.host, tt.want, got)
		}
	}
}

func TestCheckHealth(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		wantErr string
	}{
		{name: "healthy", handler: func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }},
		{name: "unhealthy status", handler: func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }, wantErr: "503"},
		{
			name: "too slow",
			handler: func(w http.ResponseWriter, r *http.Request) {
				select {
				case <-r.Context().Done():
				case <-time.After(2 * healthCheckTimeout):
				}
			},
			wantErr: "health check",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(tt.handler)
			defer server.Close()

			err := checkHealth(t.Context(), server.URL+"/health")

			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("expected healthy, got %v", err)
				}

				return
			}

			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("expected an error containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestCheckHealthWithNothingListening(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	url := server.URL + "/health"
	server.Close()

	if err := checkHealth(t.Context(), url); err == nil {
		t.Error("expected an error when nothing is listening")
	}
}
