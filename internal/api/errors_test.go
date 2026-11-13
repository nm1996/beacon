package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nm1996/beacon/internal/discovery"
	"github.com/nm1996/beacon/internal/registry/memory"
)

// decodeError checks that rec holds a JSON error response and returns its
// detail.
func decodeError(t *testing.T, rec *httptest.ResponseRecorder) ErrorDetail {
	t.Helper()

	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", got)
	}

	var response ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("expected a JSON error body, got %q: %v", rec.Body.String(), err)
	}

	if response.Error.Code == "" || response.Error.Message == "" {
		t.Errorf("expected both code and message, got %+v", response.Error)
	}

	return response.Error
}

func TestErrorResponses(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		path       string
		body       string
		wantStatus int
		wantCode   string
	}{
		{name: "malformed body", method: http.MethodPost, path: "/api/v1/services", body: `{"name":`, wantStatus: http.StatusBadRequest, wantCode: codeInvalidRequestBody},
		{name: "invalid service", method: http.MethodPost, path: "/api/v1/services", body: `{"name":"orders","address":"10.0.0.1","port":0}`, wantStatus: http.StatusBadRequest, wantCode: codeInvalidService},
		{name: "heartbeat unknown id", method: http.MethodPost, path: "/api/v1/services/unknown/heartbeat", wantStatus: http.StatusNotFound, wantCode: codeServiceNotFound},
		{name: "unregister unknown id", method: http.MethodDelete, path: "/api/v1/services/unknown", wantStatus: http.StatusNotFound, wantCode: codeServiceNotFound},
		{name: "unknown path", method: http.MethodGet, path: "/nope", wantStatus: http.StatusNotFound, wantCode: codeNotFound},
		{name: "unversioned path from before v1", method: http.MethodPost, path: "/register", wantStatus: http.StatusNotFound, wantCode: codeNotFound},
		{name: "wrong method on collection", method: http.MethodPut, path: "/api/v1/services", wantStatus: http.StatusMethodNotAllowed, wantCode: codeMethodNotAllowed},
		{name: "wrong method on instance", method: http.MethodGet, path: "/api/v1/services/some-id", wantStatus: http.StatusMethodNotAllowed, wantCode: codeMethodNotAllowed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router, _ := setupRouter(t)

			rec := serve(router, tt.method, tt.path, tt.body)

			if rec.Code != tt.wantStatus {
				t.Fatalf("expected status %d, got %d", tt.wantStatus, rec.Code)
			}

			if detail := decodeError(t, rec); detail.Code != tt.wantCode {
				t.Errorf("expected code %q, got %q (message %q)", tt.wantCode, detail.Code, detail.Message)
			}
		})
	}
}

func TestInvalidServiceMessageNamesTheProblem(t *testing.T) {
	handler, _ := setupHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/services", strings.NewReader(`{"name":"orders","address":"10.0.0.1","port":70000}`))
	rec := httptest.NewRecorder()

	handler.Register(rec, req)

	if detail := decodeError(t, rec); !strings.Contains(detail.Message, "port") {
		t.Errorf("expected message to mention the port, got %q", detail.Message)
	}
}

func TestMethodNotAllowedKeepsAllowHeader(t *testing.T) {
	router, _ := setupRouter(t)

	tests := []struct {
		path      string
		wantAllow string
	}{
		{path: "/api/v1/services/some-id", wantAllow: http.MethodDelete},
		{path: "/api/v1/services/some-id/heartbeat", wantAllow: http.MethodPost},
	}

	for _, tt := range tests {
		rec := serve(router, http.MethodGet, tt.path, "")

		if got := rec.Header().Get("Allow"); got != tt.wantAllow {
			t.Errorf("%s: expected Allow %q, got %q", tt.path, tt.wantAllow, got)
		}
	}
}

func TestInternalErrorUsesJSONFormat(t *testing.T) {
	router := NewRouter(failingDiscovery{err: errors.New("disk on fire")}, discard, testConfig)

	rec := serve(router, http.MethodPost, "/api/v1/services/x/heartbeat", "")

	detail := decodeError(t, rec)

	if detail.Code != codeInternalError {
		t.Errorf("expected code %q, got %q", codeInternalError, detail.Code)
	}

	if strings.Contains(detail.Message, "disk on fire") {
		t.Error("expected internal error details not to leak to the client")
	}
}

func TestFullRegistryAnswersQuotaExceeded(t *testing.T) {
	service, err := discovery.New(memory.New(), discovery.Config{TTL: testTTL, CleanupInterval: time.Second, MaxInstances: 1}, discard)
	if err != nil {
		t.Fatalf("new discovery: %v", err)
	}

	router := NewRouter(service, discard, testConfig)
	body := `{"name":"orders","address":"10.0.0.1","port":8081}`

	if rec := serve(router, http.MethodPost, "/api/v1/services", body); rec.Code != http.StatusCreated {
		t.Fatalf("expected the first registration to succeed, got %d", rec.Code)
	}

	rec := serve(router, http.MethodPost, "/api/v1/services", body)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected status %d, got %d", http.StatusTooManyRequests, rec.Code)
	}

	if detail := decodeError(t, rec); detail.Code != codeQuotaExceeded {
		t.Errorf("expected code %q, got %q", codeQuotaExceeded, detail.Code)
	}
}
