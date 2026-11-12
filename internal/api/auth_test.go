package api

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nm1996/beacon/internal/auth"
)

// fakeTokens accepts the tokens it maps to identities.
type fakeTokens map[string]auth.Identity

func (tokens fakeTokens) Authenticate(token string) (auth.Identity, bool) {
	identity, ok := tokens[token]
	return identity, ok
}

var testTokens = fakeTokens{
	"admin-token":   auth.NewIdentity("admin", auth.PermissionDiscover, auth.PermissionRegister, auth.PermissionMetrics),
	"reader-token":  auth.NewIdentity("reader", auth.PermissionDiscover),
	"scraper-token": auth.NewIdentity("scraper", auth.PermissionMetrics),
}

// guardedRouter returns a router protected by testTokens, the failure
// reasons it reports, and its log output.
func guardedRouter(test *testing.T) (http.Handler, *[]string, *bytes.Buffer) {
	test.Helper()

	var (
		reasons []string
		logs    bytes.Buffer
	)

	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	guard := NewGuard(testTokens, logger, func(reason string) { reasons = append(reasons, reason) })

	config := testConfig
	config.Guard = guard

	return NewRouter(newDiscovery(test, testTTL), logger, config), &reasons, &logs
}

// serveWithAuth sends one request with the given Authorization header, if
// any, through router.
func serveWithAuth(router http.Handler, method, path, authorization string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, nil)
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	return recorder
}

func TestGuardChecksPermissionsPerRoute(test *testing.T) {
	cases := []struct {
		name          string
		method        string
		path          string
		authorization string
		wantStatus    int
		wantCode      string
		wantReason    string
	}{
		// Allowed requests reach the handler, which then answers as usual.
		{name: "admin lists", method: http.MethodGet, path: servicesPath, authorization: "Bearer admin-token", wantStatus: http.StatusOK},
		{name: "admin registers", method: http.MethodPost, path: servicesPath, authorization: "Bearer admin-token", wantStatus: http.StatusBadRequest},
		{name: "admin heartbeats", method: http.MethodPost, path: servicesPath + "/some-id/heartbeat", authorization: "Bearer admin-token", wantStatus: http.StatusNotFound},
		{name: "admin unregisters", method: http.MethodDelete, path: servicesPath + "/some-id", authorization: "Bearer admin-token", wantStatus: http.StatusNotFound},
		{name: "reader lists", method: http.MethodGet, path: servicesPath, authorization: "Bearer reader-token", wantStatus: http.StatusOK},
		{name: "scheme is case-insensitive", method: http.MethodGet, path: servicesPath, authorization: "bearer reader-token", wantStatus: http.StatusOK},

		// Health needs no token.
		{name: "health without token", method: http.MethodGet, path: "/health", wantStatus: http.StatusOK},
		{name: "health with bad token", method: http.MethodGet, path: "/health", authorization: "Bearer nope", wantStatus: http.StatusOK},

		// Refused requests.
		{name: "no token", method: http.MethodGet, path: servicesPath, wantStatus: http.StatusUnauthorized, wantCode: codeUnauthorized, wantReason: AuthFailureMissing},
		{name: "unknown token", method: http.MethodGet, path: servicesPath, authorization: "Bearer nope", wantStatus: http.StatusUnauthorized, wantCode: codeUnauthorized, wantReason: AuthFailureInvalid},
		// Any non-Bearer scheme is refused; the credentials are a
		// placeholder, not encoded user:password, so secret scanners stay quiet.
		{name: "basic auth", method: http.MethodGet, path: servicesPath, authorization: "Basic placeholder", wantStatus: http.StatusUnauthorized, wantCode: codeUnauthorized, wantReason: AuthFailureInvalid},
		{name: "scheme only", method: http.MethodGet, path: servicesPath, authorization: "Bearer", wantStatus: http.StatusUnauthorized, wantCode: codeUnauthorized, wantReason: AuthFailureInvalid},
		{name: "empty token", method: http.MethodGet, path: servicesPath, authorization: "Bearer   ", wantStatus: http.StatusUnauthorized, wantCode: codeUnauthorized, wantReason: AuthFailureInvalid},
		{name: "reader registers", method: http.MethodPost, path: servicesPath, authorization: "Bearer reader-token", wantStatus: http.StatusForbidden, wantCode: codeForbidden, wantReason: AuthFailureForbidden},
		{name: "reader heartbeats", method: http.MethodPost, path: servicesPath + "/some-id/heartbeat", authorization: "Bearer reader-token", wantStatus: http.StatusForbidden, wantCode: codeForbidden, wantReason: AuthFailureForbidden},
		{name: "reader unregisters", method: http.MethodDelete, path: servicesPath + "/some-id", authorization: "Bearer reader-token", wantStatus: http.StatusForbidden, wantCode: codeForbidden, wantReason: AuthFailureForbidden},
		{name: "scraper lists", method: http.MethodGet, path: servicesPath, authorization: "Bearer scraper-token", wantStatus: http.StatusForbidden, wantCode: codeForbidden, wantReason: AuthFailureForbidden},

		// Unknown paths and wrong methods answer as before, without asking
		// for a token.
		{name: "unknown path", method: http.MethodGet, path: "/unknown", wantStatus: http.StatusNotFound, wantCode: codeNotFound},
		{name: "wrong method", method: http.MethodPut, path: servicesPath, wantStatus: http.StatusMethodNotAllowed, wantCode: codeMethodNotAllowed},
	}

	for _, testCase := range cases {
		test.Run(testCase.name, func(test *testing.T) {
			router, reasons, _ := guardedRouter(test)

			recorder := serveWithAuth(router, testCase.method, testCase.path, testCase.authorization)

			if recorder.Code != testCase.wantStatus {
				test.Fatalf("expected status %d, got %d: %s", testCase.wantStatus, recorder.Code, recorder.Body.String())
			}

			if testCase.wantCode != "" {
				if detail := decodeError(test, recorder); detail.Code != testCase.wantCode {
					test.Errorf("expected code %q, got %q", testCase.wantCode, detail.Code)
				}
			}

			challenge := recorder.Header().Get("WWW-Authenticate")
			if refused := testCase.wantReason != ""; refused != strings.HasPrefix(challenge, `Bearer realm="beacon"`) {
				test.Errorf("expected a challenge only on refused requests, got %q", challenge)
			}

			var wantReasons []string
			if testCase.wantReason != "" {
				wantReasons = []string{testCase.wantReason}
			}

			if strings.Join(*reasons, ",") != strings.Join(wantReasons, ",") {
				test.Errorf("expected failure reasons %v, got %v", wantReasons, *reasons)
			}
		})
	}
}

func TestGuardForbiddenNamesTokenAndPermission(test *testing.T) {
	router, _, logs := guardedRouter(test)

	recorder := serveWithAuth(router, http.MethodPost, servicesPath, "Bearer reader-token")

	detail := decodeError(test, recorder)
	if !strings.Contains(detail.Message, `"reader"`) || !strings.Contains(detail.Message, "register") {
		test.Errorf("expected the message to name the token and permission, got %q", detail.Message)
	}

	if got := recorder.Header().Get("WWW-Authenticate"); !strings.Contains(got, `error="insufficient_scope", scope="register"`) {
		test.Errorf("expected an insufficient_scope challenge, got %q", got)
	}

	if !strings.Contains(logs.String(), "level=WARN") || !strings.Contains(logs.String(), "token=reader") {
		test.Errorf("expected a warning naming the token, logs:\n%s", logs.String())
	}
}

func TestGuardNeverLogsTokens(test *testing.T) {
	router, _, logs := guardedRouter(test)

	serveWithAuth(router, http.MethodGet, servicesPath, "Bearer secret-guess-123")
	serveWithAuth(router, http.MethodPost, servicesPath, "Bearer reader-token")

	for _, secret := range []string{"secret-guess-123", "reader-token"} {
		if strings.Contains(logs.String(), secret) {
			test.Errorf("expected %q to stay out of the logs, logs:\n%s", secret, logs.String())
		}
	}
}

func TestNilGuardLetsEverythingThrough(test *testing.T) {
	var guard *Guard

	called := false
	handler := guard.Require(auth.PermissionRegister, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, servicesPath, nil))

	if !called {
		test.Error("expected a nil guard to pass the request on")
	}
}
