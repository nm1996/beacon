package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
		{name: "malformed body", method: http.MethodPost, path: "/register", body: `{"name":`, wantStatus: http.StatusBadRequest, wantCode: codeInvalidRequestBody},
		{name: "invalid service", method: http.MethodPost, path: "/register", body: `{"name":"orders","address":"10.0.0.1","port":0}`, wantStatus: http.StatusBadRequest, wantCode: codeInvalidService},
		{name: "heartbeat without id", method: http.MethodPost, path: "/heartbeat", body: `{}`, wantStatus: http.StatusBadRequest, wantCode: codeMissingID},
		{name: "unregister without id", method: http.MethodDelete, path: "/unregister", body: `{"id":""}`, wantStatus: http.StatusBadRequest, wantCode: codeMissingID},
		{name: "heartbeat unknown id", method: http.MethodPost, path: "/heartbeat", body: `{"id":"unknown"}`, wantStatus: http.StatusNotFound, wantCode: codeServiceNotFound},
		{name: "unregister unknown id", method: http.MethodDelete, path: "/unregister", body: `{"id":"unknown"}`, wantStatus: http.StatusNotFound, wantCode: codeServiceNotFound},
		{name: "unknown path", method: http.MethodGet, path: "/nope", wantStatus: http.StatusNotFound, wantCode: codeNotFound},
		{name: "wrong method", method: http.MethodGet, path: "/register", wantStatus: http.StatusMethodNotAllowed, wantCode: codeMethodNotAllowed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, r := setupHandler()
			router := NewRouter(r, handler.logger, testMaxBodyBytes)

			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			rec := httptest.NewRecorder()

			router.ServeHTTP(rec, req)

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
	handler, _ := setupHandler()

	req := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(`{"name":"orders","address":"10.0.0.1","port":70000}`))
	rec := httptest.NewRecorder()

	handler.Register(rec, req)

	if detail := decodeError(t, rec); !strings.Contains(detail.Message, "port") {
		t.Errorf("expected message to mention the port, got %q", detail.Message)
	}
}

func TestMethodNotAllowedKeepsAllowHeader(t *testing.T) {
	handler, r := setupHandler()

	req := httptest.NewRequest(http.MethodGet, "/unregister", nil)
	rec := httptest.NewRecorder()

	NewRouter(r, handler.logger, testMaxBodyBytes).ServeHTTP(rec, req)

	if got := rec.Header().Get("Allow"); got != http.MethodDelete {
		t.Errorf("expected Allow %q, got %q", http.MethodDelete, got)
	}
}

func TestInternalErrorUsesJSONFormat(t *testing.T) {
	handler := NewHandler(failingStore{err: errors.New("disk on fire")}, slog.New(slog.DiscardHandler), testMaxBodyBytes)

	req := httptest.NewRequest(http.MethodPost, "/heartbeat", strings.NewReader(`{"id":"x"}`))
	rec := httptest.NewRecorder()

	handler.Heartbeat(rec, req)

	detail := decodeError(t, rec)

	if detail.Code != codeInternalError {
		t.Errorf("expected code %q, got %q", codeInternalError, detail.Code)
	}

	if strings.Contains(detail.Message, "disk on fire") {
		t.Error("expected internal error details not to leak to the client")
	}
}
