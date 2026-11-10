package api

import (
	"net/http"
	"testing"
)

func TestRouter(t *testing.T) {
	tests := []struct {
		name           string
		method         string
		path           string
		expectedStatus int
	}{
		// Routes that exist. Register and the instance routes fail on the
		// empty body or unknown ID, which proves the route was reached.
		{name: "register", method: http.MethodPost, path: "/api/v1/services", expectedStatus: http.StatusBadRequest},
		{name: "list", method: http.MethodGet, path: "/api/v1/services", expectedStatus: http.StatusOK},
		{name: "list by name", method: http.MethodGet, path: "/api/v1/services?name=orders", expectedStatus: http.StatusOK},
		{name: "heartbeat", method: http.MethodPost, path: "/api/v1/services/some-id/heartbeat", expectedStatus: http.StatusNotFound},
		{name: "unregister", method: http.MethodDelete, path: "/api/v1/services/some-id", expectedStatus: http.StatusNotFound},
		{name: "health", method: http.MethodGet, path: "/health", expectedStatus: http.StatusOK},

		// Wrong methods.
		{name: "delete the collection", method: http.MethodDelete, path: "/api/v1/services", expectedStatus: http.StatusMethodNotAllowed},
		{name: "get an instance", method: http.MethodGet, path: "/api/v1/services/some-id", expectedStatus: http.StatusMethodNotAllowed},
		{name: "get a heartbeat", method: http.MethodGet, path: "/api/v1/services/some-id/heartbeat", expectedStatus: http.StatusMethodNotAllowed},
		{name: "post health", method: http.MethodPost, path: "/health", expectedStatus: http.StatusMethodNotAllowed},

		// Paths that do not exist, including the routes from before v1.
		{name: "unknown path", method: http.MethodGet, path: "/unknown", expectedStatus: http.StatusNotFound},
		{name: "unknown version", method: http.MethodGet, path: "/api/v2/services", expectedStatus: http.StatusNotFound},
		{name: "old register", method: http.MethodPost, path: "/register", expectedStatus: http.StatusNotFound},
		{name: "old heartbeat", method: http.MethodPost, path: "/heartbeat", expectedStatus: http.StatusNotFound},
		{name: "old services", method: http.MethodGet, path: "/services", expectedStatus: http.StatusNotFound},
		{name: "old unregister", method: http.MethodDelete, path: "/unregister", expectedStatus: http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router, _ := setupRouter(t)

			rec := serve(router, tt.method, tt.path, "")

			if rec.Code != tt.expectedStatus {
				t.Errorf("expected status %d, got %d", tt.expectedStatus, rec.Code)
			}
		})
	}
}
