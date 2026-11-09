package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRegisterDecoding(t *testing.T) {
	const valid = `{"name":"orders","address":"10.0.0.1","port":8081}`

	tests := []struct {
		name        string
		body        string
		wantStatus  int
		wantMessage string
	}{
		{name: "valid object", body: valid, wantStatus: http.StatusCreated},
		{name: "trailing whitespace", body: valid + "\n\t ", wantStatus: http.StatusCreated},
		{name: "empty body", body: "", wantStatus: http.StatusBadRequest, wantMessage: "Request body is empty"},
		{name: "truncated JSON", body: `{"name":`, wantStatus: http.StatusBadRequest, wantMessage: "not valid JSON"},
		{name: "syntax error", body: `{name: "orders"}`, wantStatus: http.StatusBadRequest, wantMessage: "not valid JSON"},
		{
			name:        "unknown field",
			body:        `{"name":"orders","address":"10.0.0.1","port":8081,"weight":5}`,
			wantStatus:  http.StatusBadRequest,
			wantMessage: `Unknown field "weight"`,
		},
		{
			name:        "abbreviated field name",
			body:        `{"name":"orders","addr":"10.0.0.1","port":8081}`,
			wantStatus:  http.StatusBadRequest,
			wantMessage: `Unknown field "addr"`,
		},
		{
			name:        "wrong field type",
			body:        `{"name":"orders","address":"10.0.0.1","port":"8081"}`,
			wantStatus:  http.StatusBadRequest,
			wantMessage: `Field "port" has the wrong type`,
		},
		{name: "array instead of object", body: `[]`, wantStatus: http.StatusBadRequest, wantMessage: "must be a JSON object"},
		{name: "two objects", body: valid + valid, wantStatus: http.StatusBadRequest, wantMessage: "single JSON object"},
		{name: "object then text", body: valid + " extra", wantStatus: http.StatusBadRequest, wantMessage: "single JSON object"},
		{
			name:       "valid object then padding past the limit",
			body:       valid + strings.Repeat(" ", maxRequestBodyBytes),
			wantStatus: http.StatusRequestEntityTooLarge,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, r := setupHandler()

			req := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(tt.body))
			rec := httptest.NewRecorder()

			handler.Register(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("expected status %d, got %d (body %q)", tt.wantStatus, rec.Code, rec.Body.String())
			}

			if !strings.Contains(rec.Body.String(), tt.wantMessage) {
				t.Errorf("expected message containing %q, got %q", tt.wantMessage, rec.Body.String())
			}

			wantRegistered := 0
			if tt.wantStatus == http.StatusCreated {
				wantRegistered = 1
			}

			if got := len(r.GetServices("")); got != wantRegistered {
				t.Errorf("expected %d registered services, got %d", wantRegistered, got)
			}
		})
	}
}

func TestHeartbeatAndUnregisterRejectUnknownFields(t *testing.T) {
	handler, r := setupHandler()
	id := mustRegister(t, r, "orders", 8081)

	body := `{"id":"` + id + `","force":true}`

	tests := []struct {
		name  string
		serve http.HandlerFunc
	}{
		{name: "heartbeat", serve: handler.Heartbeat},
		{name: "unregister", serve: handler.Unregister},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
			rec := httptest.NewRecorder()

			tt.serve(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Errorf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
			}
		})
	}

	if len(r.GetServices("")) != 1 {
		t.Error("expected the service to stay registered")
	}
}
