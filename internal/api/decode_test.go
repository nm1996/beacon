package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMapRequestAcceptsSingleObject(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "compact object", body: `{"name":"orders","address":"10.0.0.1","port":8081}`},
		{name: "surrounding whitespace", body: "\n  {\"name\":\"orders\",\"address\":\"10.0.0.1\",\"port\":8081}\r\n\t "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tt.body))

			var dto RegisterRequest
			if err := mapRequest(httptest.NewRecorder(), req, &dto, testMaxBodyBytes); err != nil {
				t.Fatalf("expected body to be accepted, got %v", err)
			}

			want := RegisterRequest{Name: "orders", Address: "10.0.0.1", Port: 8081}
			if dto != want {
				t.Errorf("expected %+v, got %+v", want, dto)
			}
		})
	}
}

func TestMapRequestRejectsInvalidBodies(t *testing.T) {
	const valid = `{"name":"orders","address":"10.0.0.1","port":8081}`

	tests := []struct {
		name string
		body string
	}{
		// Size limit.
		{name: "body over the limit", body: `{"name":"` + strings.Repeat("a", testMaxBodyBytes) + `"}`},
		{name: "valid object padded past the limit", body: valid + strings.Repeat(" ", testMaxBodyBytes)},

		// Not a single valid JSON object.
		{name: "empty body", body: ""},
		{name: "only whitespace", body: " \n\t"},
		{name: "truncated object", body: `{"name":"orders"`},
		{name: "syntax error", body: `{name: "orders"}`},
		{name: "null", body: `null`},
		{name: "array", body: `[` + valid + `]`},
		{name: "string", body: `"orders"`},
		{name: "number", body: `8081`},

		// Unknown fields and wrong types.
		{name: "unknown field", body: `{"name":"orders","address":"10.0.0.1","port":8081,"weight":5}`},
		{name: "wrongly named field", body: `{"name":"orders","addr":"10.0.0.1","port":8081}`},
		{name: "wrong field type", body: `{"name":"orders","address":"10.0.0.1","port":"8081"}`},

		// Trailing content.
		{name: "second object", body: valid + valid},
		{name: "second object after newline", body: valid + "\n" + valid},
		{name: "trailing array", body: valid + `[]`},
		{name: "trailing text", body: valid + ` extra`},
		{name: "trailing comma", body: valid + `,`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tt.body))

			var dto RegisterRequest
			if err := mapRequest(httptest.NewRecorder(), req, &dto, testMaxBodyBytes); err == nil {
				t.Errorf("expected body %.60q to be rejected, decoded %+v", tt.body, dto)
			}
		})
	}
}

func TestEndpointsRejectInvalidBodiesWithBadRequest(t *testing.T) {
	bodies := map[string]string{
		"too large":     `{"id":"` + strings.Repeat("a", testMaxBodyBytes) + `"}`,
		"unknown field": `{"id":"some-id","force":true}`,
		"two objects":   `{"id":"a"}{"id":"b"}`,
		"trailing text": `{"id":"a"} extra`,
		"malformed":     `{"id":`,
	}

	for bodyName, body := range bodies {
		for _, endpoint := range []struct {
			method string
			path   string
		}{
			{method: http.MethodPost, path: "/register"},
			{method: http.MethodPost, path: "/heartbeat"},
			{method: http.MethodDelete, path: "/unregister"},
		} {
			t.Run(endpoint.path+"/"+bodyName, func(t *testing.T) {
				handler, r := setupHandler()
				id := mustRegister(t, r, "orders", 8081)

				req := httptest.NewRequest(endpoint.method, endpoint.path, strings.NewReader(body))
				rec := httptest.NewRecorder()

				NewRouter(r, handler.logger, testMaxBodyBytes).ServeHTTP(rec, req)

				if rec.Code != http.StatusBadRequest {
					t.Errorf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
				}

				if detail := decodeError(t, rec); detail.Code != codeInvalidRequestBody {
					t.Errorf("expected code %q, got %q", codeInvalidRequestBody, detail.Code)
				}

				// The rejected request must not have changed the registry.
				if got := r.GetServices(""); len(got) != 1 || got[0].ID != id {
					t.Errorf("expected only the seeded service to remain, got %+v", got)
				}
			})
		}
	}
}

func TestRegisterBodyTooLarge(t *testing.T) {
	handler, r := setupHandler()

	body := `{"name":"` + strings.Repeat("a", testMaxBodyBytes) + `","address":"localhost","port":8080}`

	req := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(body))
	rec := httptest.NewRecorder()

	handler.Register(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}

	if len(r.GetServices("")) != 0 {
		t.Error("expected oversized request not to register a service")
	}
}
