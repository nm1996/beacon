package api

import (
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nm1996/beacon/internal/auth"
	"github.com/nm1996/beacon/internal/ratelimit"
)

func TestClientNetwork(t *testing.T) {
	tests := []struct {
		remote string
		want   string
	}{
		{remote: "10.0.0.1:5000", want: "10.0.0.1"},
		{remote: "[::ffff:10.0.0.1]:5000", want: "10.0.0.1"},
		{remote: "[2001:db8:1:2:aaaa::1]:5000", want: "2001:db8:1:2::/64"},
		{remote: "[2001:db8:1:2:bbbb::9]:5000", want: "2001:db8:1:2::/64"},
		{remote: "[2001:db8:1:3::1]:5000", want: "2001:db8:1:3::/64"},
		{remote: "[fe80::1%eth0]:5000", want: "fe80::/64"},
		{remote: "@", want: "@"},
	}

	for _, tt := range tests {
		t.Run(tt.remote, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			request.RemoteAddr = tt.remote

			if got := clientNetwork(request); got != tt.want {
				t.Errorf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

func TestRetryAfterSeconds(t *testing.T) {
	for _, tt := range []struct {
		wait time.Duration
		want int64
	}{
		{wait: 0, want: 1},
		{wait: time.Millisecond, want: 1},
		{wait: time.Second, want: 1},
		{wait: 1500 * time.Millisecond, want: 2},
		{wait: time.Duration(math.MaxInt64), want: math.MaxInt32},
	} {
		if got := retryAfterSeconds(tt.wait); got != tt.want {
			t.Errorf("%v: expected %d, got %d", tt.wait, tt.want, got)
		}
	}
}

// registerFrom sends a registration from remote, with token as bearer token
// when it is not empty.
func registerFrom(router http.Handler, remote, token string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, servicesPath, strings.NewReader(`{"name":"orders","address":"10.0.0.1","port":8081}`))
	request.RemoteAddr = remote

	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	return recorder
}

// countLimits returns a RateLimited callback and the counts it collects.
func countLimits() (func(string), map[string]int) {
	counts := make(map[string]int)

	return func(limit string) { counts[limit]++ }, counts
}

func TestRegistrationsAreLimitedPerNetworkWithoutAuthentication(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		limited, counts := countLimits()

		config := testConfig
		config.RegistrationLimit = ratelimit.New(1, 2, 0)
		config.RateLimited = limited
		router := NewRouter(newDiscovery(t, testTTL), discard, config)

		for attempt := range 2 {
			if recorder := registerFrom(router, "10.0.0.1:1000", ""); recorder.Code != http.StatusCreated {
				t.Fatalf("attempt %d: expected 201 within the burst, got %d", attempt+1, recorder.Code)
			}
		}

		// Another port of the same address is the same client.
		recorder := registerFrom(router, "10.0.0.1:2000", "")
		if recorder.Code != http.StatusTooManyRequests {
			t.Fatalf("expected 429 beyond the burst, got %d", recorder.Code)
		}

		if detail := decodeError(t, recorder); detail.Code != codeRateLimited {
			t.Errorf("expected code %q, got %q", codeRateLimited, detail.Code)
		}

		if got := recorder.Header().Get("Retry-After"); got != "1" {
			t.Errorf("expected Retry-After 1, got %q", got)
		}

		if counts[LimitRegistrations] != 1 {
			t.Errorf("expected 1 refusal reported, got %v", counts)
		}

		if recorder := registerFrom(router, "10.0.0.2:1000", ""); recorder.Code != http.StatusCreated {
			t.Errorf("expected another client not to be affected, got %d", recorder.Code)
		}

		time.Sleep(time.Second)

		if recorder := registerFrom(router, "10.0.0.1:1000", ""); recorder.Code != http.StatusCreated {
			t.Errorf("expected a registration after Retry-After, got %d", recorder.Code)
		}
	})
}

func TestRegistrationsAreLimitedPerCallerWithAuthentication(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		config := testConfig
		config.Guard = NewGuard(ownershipTokens, discard, nil)
		config.RegistrationLimit = ratelimit.New(1, 1, 0)
		router := NewRouter(newDiscovery(t, testTTL), discard, config)

		if recorder := registerFrom(router, "10.0.0.1:1000", "orders-token"); recorder.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d", recorder.Code)
		}

		// The same caller from another address is still the same caller.
		if recorder := registerFrom(router, "10.0.0.2:1000", "orders-token"); recorder.Code != http.StatusTooManyRequests {
			t.Errorf("expected the caller to be limited wherever it connects from, got %d", recorder.Code)
		}

		// Another caller from the first address is not.
		if recorder := registerFrom(router, "10.0.0.1:1000", "payments-token"); recorder.Code != http.StatusCreated {
			t.Errorf("expected another caller not to be affected, got %d", recorder.Code)
		}
	})
}

func TestOnlyRegistrationsAreLimited(t *testing.T) {
	config := testConfig
	config.RegistrationLimit = ratelimit.New(1, 1, 0)
	router := NewRouter(newDiscovery(t, testTTL), discard, config)

	recorder := registerFrom(router, "192.0.2.1:1000", "")
	if recorder.Code != http.StatusCreated {
		t.Fatalf("register: expected 201, got %d", recorder.Code)
	}

	location := recorder.Header().Get("Location")

	for range 10 {
		if recorder := serve(router, http.MethodPost, location+"/heartbeat", ""); recorder.Code != http.StatusNoContent {
			t.Fatalf("expected heartbeats not to be limited, got %d", recorder.Code)
		}

		if recorder := serve(router, http.MethodGet, servicesPath, ""); recorder.Code != http.StatusOK {
			t.Fatalf("expected listing not to be limited, got %d", recorder.Code)
		}
	}
}

// failureTokens holds one valid token, for a caller that may only discover.
var failureTokens = fakeTokens{
	"good-token": auth.NewIdentity("orders", auth.PermissionDiscover),
}

// listFrom lists services from remote with the Authorization header set to
// authorization, if not empty.
func listFrom(router http.Handler, remote, authorization string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, servicesPath, nil)
	request.RemoteAddr = remote

	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	return recorder
}

func TestFailedAuthenticationIsLimitedPerNetwork(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		limited := 0

		var logs strings.Builder

		logger := slog.New(slog.NewTextHandler(&logs, nil))
		config := testConfig
		config.Guard = NewGuard(failureTokens, logger, nil,
			WithFailureLimit(ratelimit.New(0.5, 2, 0), func() { limited++ }))
		router := NewRouter(newDiscovery(t, testTTL), logger, config)

		attacker := "[2001:db8:1:2::1]:1000"

		for attempt := range 2 {
			if recorder := listFrom(router, attacker, "Bearer wrong"); recorder.Code != http.StatusUnauthorized {
				t.Fatalf("attempt %d: expected 401 within the burst, got %d", attempt+1, recorder.Code)
			}
		}

		// Once the network is over its limit, even a valid token from it is
		// refused before its credentials are looked at, and so is another
		// address in the same /64.
		for _, remote := range []string{attacker, "[2001:db8:1:2::99]:1000"} {
			recorder := listFrom(router, remote, "Bearer good-token")
			if recorder.Code != http.StatusTooManyRequests {
				t.Fatalf("%s: expected 429, got %d", remote, recorder.Code)
			}

			if got := recorder.Header().Get("Retry-After"); got != "2" {
				t.Errorf("expected Retry-After 2 at half a failure per second, got %q", got)
			}

			if detail := decodeError(t, recorder); detail.Code != codeRateLimited {
				t.Errorf("expected code %q, got %q", codeRateLimited, detail.Code)
			}
		}

		if limited != 2 {
			t.Errorf("expected 2 refusals reported, got %d", limited)
		}

		for _, want := range []string{"reason=rate_limited", "limit=auth_failures", "network=2001:db8:1:2::/64"} {
			if !strings.Contains(logs.String(), want) {
				t.Errorf("expected the log to contain %q, logs:\n%s", want, logs.String())
			}
		}

		if recorder := listFrom(router, "10.0.0.9:1000", "Bearer good-token"); recorder.Code != http.StatusOK {
			t.Errorf("expected another network not to be affected, got %d", recorder.Code)
		}

		time.Sleep(2 * time.Second)

		if recorder := listFrom(router, attacker, "Bearer good-token"); recorder.Code != http.StatusOK {
			t.Errorf("expected the network to recover after Retry-After, got %d", recorder.Code)
		}
	})
}

func TestOnlyInvalidCredentialsCountAsFailures(t *testing.T) {
	config := testConfig
	config.Guard = NewGuard(failureTokens, discard, nil, WithFailureLimit(ratelimit.New(1, 1, 0), nil))
	router := NewRouter(newDiscovery(t, testTTL), discard, config)

	remote := "10.0.0.1:1000"

	for range 5 {
		// No credentials at all: a probe or a misconfigured client.
		if recorder := listFrom(router, remote, ""); recorder.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 without credentials, got %d", recorder.Code)
		}

		// A known caller without the permission has proved who it is.
		if recorder := registerFrom(router, remote, "good-token"); recorder.Code != http.StatusForbidden {
			t.Fatalf("expected 403 for a missing permission, got %d", recorder.Code)
		}
	}

	if recorder := listFrom(router, remote, "Bearer good-token"); recorder.Code != http.StatusOK {
		t.Errorf("expected missing credentials and permissions not to use up the limit, got %d", recorder.Code)
	}
}

// gatedTokens is failureTokens behind a gate: every Authenticate call waits
// until the gate opens, so a test can hold many checks in progress at once.
type gatedTokens struct {
	fakeTokens
	gate  chan struct{}
	calls atomic.Int64
}

func (tokens *gatedTokens) Authenticate(token string) (auth.Identity, bool) {
	tokens.calls.Add(1)
	<-tokens.gate

	return tokens.fakeTokens.Authenticate(token)
}

func TestConcurrentFailedAuthenticationNeverExceedsTheBurst(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tokens := &gatedTokens{fakeTokens: failureTokens, gate: make(chan struct{})}
		config := testConfig
		config.Guard = NewGuard(tokens, discard, nil, WithFailureLimit(ratelimit.New(1, 20, 0), nil))
		router := NewRouter(newDiscovery(t, testTTL), discard, config)

		var (
			group sync.WaitGroup
			mu    sync.Mutex
			codes = make(map[int]int)
		)

		for range 100 {
			group.Go(func() {
				recorder := listFrom(router, "10.0.0.1:1000", "Bearer wrong")

				mu.Lock()
				codes[recorder.Code]++
				mu.Unlock()
			})
		}

		// Every request is either waiting in Authenticate or already refused.
		synctest.Wait()

		if got := tokens.calls.Load(); got != 20 {
			t.Errorf("expected only the burst of 20 to reach authentication at once, got %d", got)
		}

		close(tokens.gate)
		group.Wait()

		if codes[http.StatusUnauthorized] != 20 || codes[http.StatusTooManyRequests] != 80 {
			t.Errorf("expected 20 answered 401 and 80 answered 429, got %v", codes)
		}

		if recorder := listFrom(router, "10.0.0.1:1000", "Bearer good-token"); recorder.Code != http.StatusTooManyRequests {
			t.Errorf("expected the spent burst to refuse even a valid token, got %d", recorder.Code)
		}
	})
}

func TestValidCredentialsGiveTheirReservationBack(t *testing.T) {
	config := testConfig
	config.Guard = NewGuard(failureTokens, discard, nil, WithFailureLimit(ratelimit.New(1, 3, 0), nil))
	router := NewRouter(newDiscovery(t, testTTL), discard, config)

	remote := "10.0.0.1:1000"

	for range 100 {
		if recorder := listFrom(router, remote, "Bearer good-token"); recorder.Code != http.StatusOK {
			t.Fatalf("expected a valid token to be served, got %d", recorder.Code)
		}
	}

	for attempt := range 3 {
		if recorder := listFrom(router, remote, "Bearer wrong"); recorder.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: expected valid requests to leave the whole burst, got %d", attempt+1, recorder.Code)
		}
	}

	if recorder := listFrom(router, remote, "Bearer wrong"); recorder.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429 once the burst is spent, got %d", recorder.Code)
	}
}

// panickingTokens panics on every token, as a broken authenticator might.
type panickingTokens struct{ fakeTokens }

func (panickingTokens) Authenticate(string) (auth.Identity, bool) {
	panic("broken authenticator")
}

func TestPanickingAuthenticationGivesItsReservationBack(t *testing.T) {
	config := testConfig
	config.Guard = NewGuard(panickingTokens{}, discard, nil, WithFailureLimit(ratelimit.New(1, 1, 0), nil))
	router := NewRouter(newDiscovery(t, testTTL), discard, config)

	for range 3 {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatal("expected the authenticator's panic to reach the caller")
				}
			}()

			listFrom(router, "10.0.0.1:1000", "Bearer anything")
		}()
	}
}
