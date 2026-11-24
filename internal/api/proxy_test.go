package api

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/nm1996/beacon/internal/ratelimit"
)

// testProxies trusts a private IPv4 range and one IPv6 range.
var testProxies = TrustedProxies{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("fd00::/8")}

func TestClientAddressBehindTrustedProxies(t *testing.T) {
	tests := []struct {
		name      string
		remote    string
		forwarded []string
		want      string
	}{
		{name: "untrusted peer ignores the header", remote: "203.0.113.9:1", forwarded: []string{"198.51.100.1"}, want: "203.0.113.9"},
		{name: "trusted peer without a header", remote: "10.0.0.1:1", want: "10.0.0.1"},
		{name: "one proxy", remote: "10.0.0.1:1", forwarded: []string{"198.51.100.1"}, want: "198.51.100.1"},
		{name: "spoofed entries left of the client", remote: "10.0.0.1:1", forwarded: []string{"1.2.3.4, 198.51.100.1"}, want: "198.51.100.1"},
		{name: "chain of trusted proxies", remote: "10.0.0.1:1", forwarded: []string{"198.51.100.1, 10.1.1.1, 10.2.2.2"}, want: "198.51.100.1"},
		{name: "several headers are one list", remote: "10.0.0.1:1", forwarded: []string{"1.2.3.4", "198.51.100.1, 10.1.1.1"}, want: "198.51.100.1"},
		{name: "every hop trusted", remote: "10.0.0.1:1", forwarded: []string{"10.9.9.9, 10.1.1.1"}, want: "10.9.9.9"},
		{name: "addresses with ports", remote: "10.0.0.1:1", forwarded: []string{"198.51.100.1:4711, [fd00::1]:80"}, want: "198.51.100.1"},
		{name: "IPv6 client", remote: "[fd00::5]:1", forwarded: []string{"2001:db8::7"}, want: "2001:db8::7"},
		{name: "IPv4-mapped peer", remote: "[::ffff:10.0.0.1]:1", forwarded: []string{"198.51.100.1"}, want: "198.51.100.1"},
		{name: "garbage before the client", remote: "10.0.0.1:1", forwarded: []string{"198.51.100.1, not-an-address"}, want: "10.0.0.1"},
		{name: "garbage left of the client is never read", remote: "10.0.0.1:1", forwarded: []string{"not-an-address, 198.51.100.1"}, want: "198.51.100.1"},
		{name: "empty entries", remote: "10.0.0.1:1", forwarded: []string{" , 198.51.100.1 ,"}, want: "198.51.100.1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			request.RemoteAddr = tt.remote

			for _, value := range tt.forwarded {
				request.Header.Add("X-Forwarded-For", value)
			}

			got, ok := testProxies.clientAddress(request)
			if !ok || got.String() != tt.want {
				t.Errorf("expected %s, got %v (%v)", tt.want, got, ok)
			}
		})
	}
}

func TestNoTrustedProxiesIgnoresTheHeader(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "10.0.0.1:1"
	request.Header.Set("X-Forwarded-For", "198.51.100.1")

	if got := clientNetwork(request, nil); got != "10.0.0.1" {
		t.Errorf("expected the connection's address, got %s", got)
	}
}

// listBehindProxy lists services through the proxy at 10.0.0.1 for the
// client forwarded, with authorization as the Authorization header.
func listBehindProxy(router http.Handler, forwarded, authorization string) int {
	request := httptest.NewRequest(http.MethodGet, servicesPath, nil)
	request.RemoteAddr = "10.0.0.1:1000"
	request.Header.Set("X-Forwarded-For", forwarded)
	request.Header.Set("Authorization", authorization)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	return recorder.Code
}

func TestFailureLimitSeparatesClientsBehindATrustedProxy(t *testing.T) {
	config := testConfig
	config.Guard = NewGuard(failureTokens, discard, nil,
		WithFailureLimit(ratelimit.New(1, 2, 0), nil),
		WithTrustedProxies(testProxies))
	router := NewRouter(newDiscovery(t, testTTL), discard, config)

	for range 2 {
		listBehindProxy(router, "198.51.100.1", "Bearer wrong")
	}

	if code := listBehindProxy(router, "198.51.100.1", "Bearer good-token"); code != http.StatusTooManyRequests {
		t.Errorf("expected the failing client to be limited, got %d", code)
	}

	if code := listBehindProxy(router, "198.51.100.2", "Bearer good-token"); code != http.StatusOK {
		t.Errorf("expected another client behind the same proxy not to be affected, got %d", code)
	}

	// The failing client cannot escape by claiming another address left
	// of the one the proxy wrote.
	if code := listBehindProxy(router, "192.0.2.77, 198.51.100.1", "Bearer good-token"); code != http.StatusTooManyRequests {
		t.Errorf("expected a spoofed entry not to help, got %d", code)
	}
}

func TestRegistrationLimitUsesTheForwardedClient(t *testing.T) {
	config := testConfig
	config.RegistrationLimit = ratelimit.New(1, 1, 0)
	config.TrustedProxies = testProxies
	router := NewRouter(newDiscovery(t, testTTL), discard, config)

	register := func(forwarded string) int {
		request := httptest.NewRequest(http.MethodPost, servicesPath, strings.NewReader(`{"name":"orders","address":"10.0.0.1","port":8081}`))
		request.RemoteAddr = "10.0.0.1:1000"
		request.Header.Set("X-Forwarded-For", forwarded)

		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)

		return recorder.Code
	}

	if code := register("198.51.100.1"); code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", code)
	}

	if code := register("198.51.100.1"); code != http.StatusTooManyRequests {
		t.Errorf("expected the same client to be limited, got %d", code)
	}

	if code := register("198.51.100.2"); code != http.StatusCreated {
		t.Errorf("expected another client behind the proxy not to be affected, got %d", code)
	}
}
