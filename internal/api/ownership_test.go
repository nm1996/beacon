package api

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nm1996/beacon/internal/auth"
)

// ownershipTokens are callers that register instances, or manage them.
var ownershipTokens = fakeTokens{
	"orders-token":   auth.NewIdentity("orders", auth.PermissionRegister, auth.PermissionDiscover),
	"payments-token": auth.NewIdentity("payments", auth.PermissionRegister, auth.PermissionDiscover),
	"operator-token": auth.NewIdentity("operator", auth.PermissionRegister, auth.PermissionManage),
}

// ownershipRouter returns a router protected by ownershipTokens, and its
// log output.
func ownershipRouter(test *testing.T) (http.Handler, *bytes.Buffer) {
	test.Helper()

	var logs bytes.Buffer

	logger := slog.New(slog.NewTextHandler(&logs, nil))
	config := testConfig
	config.Guard = NewGuard(ownershipTokens, logger, nil)

	return NewRouter(newDiscovery(test, testTTL), logger, config), &logs
}

// send sends one request with token as bearer token through router.
func send(router http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+token)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	return recorder
}

// registerAs registers an instance with token and returns its ID.
func registerAs(test *testing.T, router http.Handler, token string) string {
	test.Helper()

	recorder := send(router, http.MethodPost, servicesPath, token, `{"name":"orders","address":"10.0.0.1","port":8081}`)
	if recorder.Code != http.StatusCreated {
		test.Fatalf("register: expected 201, got %d: %s", recorder.Code, recorder.Body.String())
	}

	var response RegisterResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		test.Fatalf("decode register response: %v", err)
	}

	return response.ID
}

func TestOnlyTheOwnerOrAManagerMayChangeInstances(test *testing.T) {
	cases := []struct {
		name       string
		token      string
		wantStatus int
	}{
		{name: "owner", token: "orders-token", wantStatus: http.StatusNoContent},
		{name: "another service", token: "payments-token", wantStatus: http.StatusForbidden},
		{name: "operator with manage", token: "operator-token", wantStatus: http.StatusNoContent},
	}

	for _, testCase := range cases {
		test.Run(testCase.name, func(test *testing.T) {
			router, _ := ownershipRouter(test)
			id := registerAs(test, router, "orders-token")

			for _, request := range []struct{ method, path string }{
				{method: http.MethodPost, path: servicesPath + "/" + id + "/heartbeat"},
				{method: http.MethodDelete, path: servicesPath + "/" + id},
			} {
				recorder := send(router, request.method, request.path, testCase.token, "")

				if recorder.Code != testCase.wantStatus {
					test.Fatalf("%s: expected %d, got %d: %s", request.method, testCase.wantStatus, recorder.Code, recorder.Body.String())
				}

				if testCase.wantStatus == http.StatusForbidden {
					if detail := decodeError(test, recorder); detail.Code != codeInstanceNotOwned {
						test.Errorf("%s: expected code %q, got %q", request.method, codeInstanceNotOwned, detail.Code)
					}
				}
			}
		})
	}
}

func TestNotOwnedIsLoggedWithTheCaller(test *testing.T) {
	router, logs := ownershipRouter(test)
	id := registerAs(test, router, "orders-token")

	send(router, http.MethodDelete, servicesPath+"/"+id, "payments-token", "")

	for _, want := range []string{"level=WARN", "caller=payments", "reason=not_owner"} {
		if !strings.Contains(logs.String(), want) {
			test.Errorf("expected the log to contain %q, logs:\n%s", want, logs.String())
		}
	}
}

func TestUnknownInstanceStaysNotFoundWithOwnership(test *testing.T) {
	router, _ := ownershipRouter(test)

	recorder := send(router, http.MethodPost, servicesPath+"/unknown-id/heartbeat", "payments-token", "")

	if recorder.Code != http.StatusNotFound {
		test.Errorf("expected 404 for an unknown instance, so a Lease registers again, got %d", recorder.Code)
	}
}

func TestListingDoesNotShowOwners(test *testing.T) {
	router, _ := ownershipRouter(test)
	registerAs(test, router, "orders-token")

	body := send(router, http.MethodGet, servicesPath, "orders-token", "").Body.String()

	if strings.Contains(body, "owner") || strings.Contains(body, `"orders-token"`) {
		test.Errorf("expected the listing to leave owners out, got %s", body)
	}
}
