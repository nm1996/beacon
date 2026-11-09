package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nm1996/beacon/internal/registry"
)

func TestRouter(t *testing.T) {
	tests := []struct {
		name           string
		method         string
		path           string
		expectedStatus int
	}{
		{
			name:           "register route",
			method:         http.MethodPost,
			path:           "/register",
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "heartbeat route",
			method:         http.MethodPost,
			path:           "/heartbeat",
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "get services route",
			method:         http.MethodGet,
			path:           "/services",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "unregister route",
			method:         http.MethodDelete,
			path:           "/unregister",
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "unknown route",
			method:         http.MethodGet,
			path:           "/unknown",
			expectedStatus: http.StatusNotFound,
		},
		{
			name:           "register with wrong method",
			method:         http.MethodGet,
			path:           "/register",
			expectedStatus: http.StatusMethodNotAllowed,
		},
		{
			name:           "heartbeat with wrong method",
			method:         http.MethodGet,
			path:           "/heartbeat",
			expectedStatus: http.StatusMethodNotAllowed,
		},
		{
			name:           "services with wrong method",
			method:         http.MethodPost,
			path:           "/services",
			expectedStatus: http.StatusMethodNotAllowed,
		},
		{
			name:           "unregister with wrong method",
			method:         http.MethodGet,
			path:           "/unregister",
			expectedStatus: http.StatusMethodNotAllowed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			serviceRegistry := registry.New()
			router := NewRouter(serviceRegistry)

			req := httptest.NewRequest(tt.method, tt.path, nil)
			rec := httptest.NewRecorder()

			router.ServeHTTP(rec, req)

			if rec.Code != tt.expectedStatus {
				t.Errorf(
					"expected status %d, got %d",
					tt.expectedStatus,
					rec.Code,
				)
			}
		})
	}
}
