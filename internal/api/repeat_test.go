package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// registerWith sends a registration with body, and with key as the
// Idempotency-Key header when it is not empty.
func registerWith(router http.Handler, body, key string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, servicesPath, strings.NewReader(body))
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	return recorder
}

// registeredID decodes the ID from a registration response.
func registeredID(t *testing.T, recorder *httptest.ResponseRecorder) string {
	t.Helper()

	var response RegisterResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode %q: %v", recorder.Body.String(), err)
	}

	return response.ID
}

func TestRegisteringAChosenIDTwice(t *testing.T) {
	router := NewRouter(newDiscovery(t, testTTL), discard, testConfig)
	body := `{"id":"orders-1","name":"orders","address":"10.0.0.1","port":8081}`

	first := registerWith(router, body, "")
	if first.Code != http.StatusCreated || registeredID(t, first) != "orders-1" {
		t.Fatalf("expected 201 with the chosen ID, got %d: %s", first.Code, first.Body.String())
	}

	again := registerWith(router, body, "")
	if again.Code != http.StatusOK || again.Body.String() != first.Body.String() {
		t.Errorf("expected 200 with the same body, got %d: %s", again.Code, again.Body.String())
	}

	if location := again.Header().Get("Location"); location != servicesPath+"/orders-1" {
		t.Errorf("expected the Location of the instance, got %q", location)
	}

	moved := registerWith(router, `{"id":"orders-1","name":"orders","address":"10.0.0.2","port":8081}`, "")
	if moved.Code != http.StatusConflict {
		t.Fatalf("expected 409 for the ID with another address, got %d", moved.Code)
	}

	if detail := decodeError(t, moved); detail.Code != codeInstanceExists {
		t.Errorf("expected code %q, got %q", codeInstanceExists, detail.Code)
	}
}

func TestIdempotencyKeyHeader(t *testing.T) {
	router := NewRouter(newDiscovery(t, testTTL), discard, testConfig)
	body := `{"name":"orders","address":"10.0.0.1","port":8081}`

	first := registerWith(router, body, "retry-me")
	if first.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", first.Code)
	}

	again := registerWith(router, body, "retry-me")
	if again.Code != http.StatusOK || registeredID(t, again) != registeredID(t, first) {
		t.Errorf("expected the retry to find the same instance, got %d: %s", again.Code, again.Body.String())
	}

	reused := registerWith(router, `{"name":"orders","address":"10.0.0.1","port":9999}`, "retry-me")
	if reused.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for a reused key, got %d", reused.Code)
	}

	if detail := decodeError(t, reused); detail.Code != codeIdempotencyReused {
		t.Errorf("expected code %q, got %q", codeIdempotencyReused, detail.Code)
	}

	both := registerWith(router, `{"id":"x","name":"orders","address":"10.0.0.1","port":8081}`, "retry-me")
	if both.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for an id and a key together, got %d", both.Code)
	}

	if recorder := serve(router, http.MethodGet, servicesPath, ""); strings.Count(recorder.Body.String(), `"id"`) != 1 {
		t.Errorf("expected exactly one instance, got %s", recorder.Body.String())
	}
}
