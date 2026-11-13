package bootstrap

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nm1996/beacon/internal/auth"
	"github.com/nm1996/beacon/internal/config"
)

const (
	serviceToken = "service-token-0123456789"
	scrapeToken  = "scrape-token-0123456789"
)

// authSettings returns default settings with auth enabled and a tokens file
// holding serviceToken, allowed everything, and scrapeToken, allowed only
// metrics.
func authSettings(test *testing.T) config.Config {
	test.Helper()

	serviceHash, err := auth.HashToken(serviceToken)
	if err != nil {
		test.Fatalf("hash token: %v", err)
	}

	scrapeHash, err := auth.HashToken(scrapeToken)
	if err != nil {
		test.Fatalf("hash token: %v", err)
	}

	content := "[[tokens]]\nname = \"service\"\nsha256 = \"" + serviceHash + "\"\n\n" +
		"[[tokens]]\nname = \"prometheus\"\nsha256 = \"" + scrapeHash + "\"\npermissions = [\"metrics\"]\n"

	path := filepath.Join(test.TempDir(), "tokens.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		test.Fatalf("write tokens file: %v", err)
	}

	settings := config.Default()
	settings.Auth = config.AuthConfig{Enabled: true, TokensFile: path}

	return settings
}

// getWithToken sends a GET for path to handler with token as bearer token,
// or with no Authorization header when token is empty.
func getWithToken(handler http.Handler, path, token string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	return recorder
}

func TestNewRequiresTokensWhenAuthIsEnabled(test *testing.T) {
	handler := mustNew(test, authSettings(test), "v0.0.0-test").Server.Handler

	cases := []struct {
		name       string
		path       string
		token      string
		wantStatus int
	}{
		{name: "API without token", path: "/api/v1/services", wantStatus: http.StatusUnauthorized},
		{name: "API with service token", path: "/api/v1/services", token: serviceToken, wantStatus: http.StatusOK},
		{name: "API with metrics-only token", path: "/api/v1/services", token: scrapeToken, wantStatus: http.StatusForbidden},
		{name: "metrics without token", path: "/metrics", wantStatus: http.StatusUnauthorized},
		{name: "metrics with scrape token", path: "/metrics", token: scrapeToken, wantStatus: http.StatusOK},
		{name: "metrics with service token", path: "/metrics", token: serviceToken, wantStatus: http.StatusOK},
		{name: "health without token", path: "/health", wantStatus: http.StatusOK},
	}

	for _, testCase := range cases {
		test.Run(testCase.name, func(test *testing.T) {
			if recorder := getWithToken(handler, testCase.path, testCase.token); recorder.Code != testCase.wantStatus {
				test.Errorf("expected status %d, got %d: %s", testCase.wantStatus, recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestNewCountsAuthFailures(test *testing.T) {
	handler := mustNew(test, authSettings(test), "v0.0.0-test").Server.Handler

	getWithToken(handler, "/api/v1/services", "")
	getWithToken(handler, "/api/v1/services", "wrong-token-0123456789")
	getWithToken(handler, "/api/v1/services", scrapeToken)
	getWithToken(handler, "/metrics", "")

	body := getWithToken(handler, "/metrics", scrapeToken).Body.String()

	for _, want := range []string{
		`beacon_auth_failures_total{reason="missing"} 2`,
		`beacon_auth_failures_total{reason="invalid"} 1`,
		`beacon_auth_failures_total{reason="forbidden"} 1`,
		`beacon_http_requests_total{code="401",method="GET",route="GET /api/v1/services"} 2`,
	} {
		if !strings.Contains(body, want) {
			test.Errorf("expected /metrics to contain %q", want)
		}
	}
}

func TestNewAuthWorksWithoutMetrics(test *testing.T) {
	settings := authSettings(test)
	settings.Metrics.Enabled = false

	handler := mustNew(test, settings, "v0.0.0-test").Server.Handler

	if recorder := getWithToken(handler, "/api/v1/services", ""); recorder.Code != http.StatusUnauthorized {
		test.Errorf("expected 401 without a token, got %d", recorder.Code)
	}

	if recorder := getWithToken(handler, "/api/v1/services", serviceToken); recorder.Code != http.StatusOK {
		test.Errorf("expected 200 with a token, got %d", recorder.Code)
	}
}

func TestNewIgnoresTokensFileWhenAuthIsDisabled(test *testing.T) {
	settings := config.Default()
	settings.Auth.TokensFile = filepath.Join(test.TempDir(), "missing.toml")

	handler := mustNew(test, settings, "v0.0.0-test").Server.Handler

	if recorder := getWithToken(handler, "/api/v1/services", ""); recorder.Code != http.StatusOK {
		test.Errorf("expected the API to stay open with auth disabled, got %d", recorder.Code)
	}
}

func TestNewRejectsUnusableTokensFile(test *testing.T) {
	settings := authSettings(test)
	settings.Auth.TokensFile = filepath.Join(test.TempDir(), "missing.toml")

	_, err := New(settings, discard, "v0.0.0-test")

	if err == nil || !strings.Contains(err.Error(), "read tokens file") {
		test.Errorf("expected a tokens file error, got %v", err)
	}
}

func TestReloadedTokensApplyToTheRunningAPI(test *testing.T) {
	settings := authSettings(test)
	components := mustNew(test, settings, "v0.0.0-test")
	handler := components.Server.Handler

	if recorder := getWithToken(handler, "/api/v1/services", serviceToken); recorder.Code != http.StatusOK {
		test.Fatalf("expected the service token before reloading, got %d", recorder.Code)
	}

	// Keep only the metrics token: the service token is revoked.
	scrapeHash, err := auth.HashToken(scrapeToken)
	if err != nil {
		test.Fatalf("hash token: %v", err)
	}

	content := "[[tokens]]\nname = \"prometheus\"\nsha256 = \"" + scrapeHash + "\"\npermissions = [\"metrics\"]\n"
	if err := os.WriteFile(settings.Auth.TokensFile, []byte(content), 0o600); err != nil {
		test.Fatalf("rewrite tokens file: %v", err)
	}

	if err := components.Credentials.Reload(); err != nil {
		test.Fatalf("reload: %v", err)
	}

	if recorder := getWithToken(handler, "/api/v1/services", serviceToken); recorder.Code != http.StatusUnauthorized {
		test.Errorf("expected the revoked token to get 401, got %d", recorder.Code)
	}

	body := getWithToken(handler, "/metrics", scrapeToken).Body.String()
	if !strings.Contains(body, `beacon_credential_reloads_total{result="ok"} 1`) {
		test.Error("expected the reload to be counted in /metrics")
	}
}

func TestNewAppliesMaxInstancesPerOwner(test *testing.T) {
	settings := authSettings(test)
	settings.Registry.MaxInstancesPerOwner = 1

	handler := mustNew(test, settings, "v0.0.0-test").Server.Handler

	want := []int{http.StatusCreated, http.StatusTooManyRequests}
	for attempt, status := range want {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/services", strings.NewReader(`{"name":"orders","address":"10.0.0.1","port":8081}`))
		request.Header.Set("Authorization", "Bearer "+serviceToken)

		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)

		if recorder.Code != status {
			test.Errorf("registration %d: expected status %d, got %d: %s", attempt+1, status, recorder.Code, recorder.Body)
		}
	}
}
