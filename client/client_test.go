package client_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nm1996/beacon/client"
	"github.com/nm1996/beacon/internal/bootstrap"
	"github.com/nm1996/beacon/internal/config"
)

// newServer starts a real Beacon server with default settings and returns
// a client for it.
func newServer(t *testing.T) *client.Client {
	t.Helper()

	components, err := bootstrap.New(config.Default(), slog.New(slog.DiscardHandler), "v9.9.9")
	if err != nil {
		t.Fatalf("build server: %v", err)
	}

	server := httptest.NewServer(components.Server.Handler)
	t.Cleanup(server.Close)

	return mustNew(t, server.URL)
}

// mustNew returns a client for baseURL and fails the test if that fails.
func mustNew(t *testing.T, baseURL string, options ...client.Option) *client.Client {
	t.Helper()

	beacon, err := client.New(baseURL, options...)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	return beacon
}

// orders is a valid registration.
var orders = client.Registration{Name: "orders", Address: "10.0.0.12", Port: 8081}

func TestRegisterAndDiscover(t *testing.T) {
	beacon := newServer(t)
	before := time.Now().Add(-time.Second)

	registered, err := beacon.Register(t.Context(), orders)
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	if registered.ID == "" || registered.TTL != 30*time.Second {
		t.Errorf("expected an ID and a 30s TTL, got %+v", registered)
	}

	instances, err := beacon.Discover(t.Context(), "orders")
	if err != nil {
		t.Fatalf("discover: %v", err)
	}

	if len(instances) != 1 {
		t.Fatalf("expected 1 instance, got %d", len(instances))
	}

	got := instances[0]
	if got.ID != registered.ID || got.Name != "orders" || got.Address != "10.0.0.12" || got.Port != 8081 {
		t.Errorf("unexpected instance %+v", got)
	}

	if got.LastHeartbeat.Before(before) {
		t.Errorf("expected a recent LastHeartbeat, got %v", got.LastHeartbeat)
	}
}

func TestDiscoverFiltersAndListsAll(t *testing.T) {
	beacon := newServer(t)

	for _, registration := range []client.Registration{orders, orders, {Name: "payments", Address: "10.0.0.20", Port: 9000}} {
		if _, err := beacon.Register(t.Context(), registration); err != nil {
			t.Fatalf("register: %v", err)
		}
	}

	for name, want := range map[string]int{"orders": 2, "payments": 1, "": 3, "missing": 0} {
		instances, err := beacon.Discover(t.Context(), name)
		if err != nil {
			t.Fatalf("discover %q: %v", name, err)
		}

		if len(instances) != want {
			t.Errorf("discover %q: expected %d instances, got %d", name, want, len(instances))
		}
	}
}

func TestHeartbeatAndUnregister(t *testing.T) {
	beacon := newServer(t)

	registered, err := beacon.Register(t.Context(), orders)
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	if err := beacon.Heartbeat(t.Context(), registered.ID); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}

	if err := beacon.Unregister(t.Context(), registered.ID); err != nil {
		t.Fatalf("unregister: %v", err)
	}

	if err := beacon.Heartbeat(t.Context(), registered.ID); !errors.Is(err, client.ErrNotFound) {
		t.Errorf("expected ErrNotFound after unregistering, got %v", err)
	}

	if err := beacon.Unregister(t.Context(), registered.ID); !errors.Is(err, client.ErrNotFound) {
		t.Errorf("expected ErrNotFound on a second unregister, got %v", err)
	}
}

func TestErrorsCarryBeaconCodes(t *testing.T) {
	beacon := newServer(t)

	_, err := beacon.Register(t.Context(), client.Registration{Name: "orders", Address: "10.0.0.12", Port: 70000})

	if !errors.Is(err, client.ErrInvalid) {
		t.Fatalf("expected ErrInvalid, got %v", err)
	}

	var apiError *client.Error
	if !errors.As(err, &apiError) {
		t.Fatalf("expected an *client.Error, got %T", err)
	}

	if apiError.StatusCode != http.StatusBadRequest || apiError.Code != "invalid_service" || !strings.Contains(apiError.Message, "port") {
		t.Errorf("unexpected error details %+v", apiError)
	}

	if errors.Is(err, client.ErrNotFound) {
		t.Error("an invalid request must not match ErrNotFound")
	}
}

func TestEmptyIDIsRejectedWithoutARequest(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests++ }))
	defer server.Close()

	beacon := mustNew(t, server.URL)

	if err := beacon.Heartbeat(t.Context(), ""); !errors.Is(err, client.ErrInvalid) {
		t.Errorf("heartbeat: expected ErrInvalid, got %v", err)
	}

	if err := beacon.Unregister(t.Context(), ""); !errors.Is(err, client.ErrInvalid) {
		t.Errorf("unregister: expected ErrInvalid, got %v", err)
	}

	if requests != 0 {
		t.Errorf("expected no requests, got %d", requests)
	}
}

func TestHealth(t *testing.T) {
	health, err := newServer(t).Health(t.Context())
	if err != nil {
		t.Fatalf("health: %v", err)
	}

	if health.Status != "ok" || health.Version != "v9.9.9" {
		t.Errorf("unexpected health %+v", health)
	}
}

func TestNonJSONErrorKeepsStatus(t *testing.T) {
	// A proxy in front of Beacon may answer with HTML.
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/html")
		writer.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(writer, "<html>bad gateway</html>")
	}))
	defer server.Close()

	_, err := mustNew(t, server.URL).Discover(t.Context(), "")

	var apiError *client.Error
	if !errors.As(err, &apiError) || apiError.StatusCode != http.StatusBadGateway || apiError.Code != "" {
		t.Fatalf("expected a 502 *client.Error without a code, got %#v", err)
	}

	if errors.Is(err, client.ErrNotFound) || errors.Is(err, client.ErrInvalid) {
		t.Error("a proxy error must not match Beacon's sentinel errors")
	}
}

func TestBaseURLWithPathPrefix(t *testing.T) {
	components, err := bootstrap.New(config.Default(), slog.New(slog.DiscardHandler), "v9.9.9")
	if err != nil {
		t.Fatalf("build server: %v", err)
	}

	// Beacon served under /beacon behind a proxy.
	mux := http.NewServeMux()
	mux.Handle("/beacon/", http.StripPrefix("/beacon", components.Server.Handler))

	server := httptest.NewServer(mux)
	defer server.Close()

	for _, base := range []string{server.URL + "/beacon", server.URL + "/beacon/"} {
		beacon := mustNew(t, base)

		if _, err := beacon.Register(t.Context(), orders); err != nil {
			t.Errorf("%s: register: %v", base, err)
		}
	}
}

func TestNewValidatesBaseURL(t *testing.T) {
	for _, base := range []string{"", "beacon:8080", "ftp://beacon", "http://", "://bad"} {
		if _, err := client.New(base); err == nil {
			t.Errorf("expected %q to be rejected", base)
		}
	}
}

func TestRequestsHonorContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		<-request.Context().Done()
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	_, err := mustNew(t, server.URL).Discover(ctx, "")

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected the context deadline to stop the request, got %v", err)
	}
}

func TestWithHTTPClientIsUsed(t *testing.T) {
	var sawAgent string

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		sawAgent = request.Header.Get("User-Agent")
		_, _ = io.WriteString(writer, `{"status":"ok","version":"x"}`)
	}))
	defer server.Close()

	custom := &http.Client{Timeout: time.Second, Transport: http.DefaultTransport}

	if _, err := mustNew(t, server.URL, client.WithHTTPClient(custom)).Health(t.Context()); err != nil {
		t.Fatalf("health: %v", err)
	}

	if sawAgent != "beacon-go-client" {
		t.Errorf("expected the client's User-Agent, got %q", sawAgent)
	}
}

func TestNilOptionsKeepDefaults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(writer, `{"status":"ok","version":"x"}`)
	}))
	defer server.Close()

	beacon := mustNew(t, server.URL, client.WithHTTPClient(nil), client.WithLogger(nil))

	// A nil http.Client would panic here.
	if _, err := beacon.Health(t.Context()); err != nil {
		t.Fatalf("health: %v", err)
	}
}

func TestRegisterOnFullRegistryMatchesErrQuotaExceeded(t *testing.T) {
	settings := config.Default()
	settings.Registry.MaxInstances = 1

	components, err := bootstrap.New(settings, slog.New(slog.DiscardHandler), "v9.9.9")
	if err != nil {
		t.Fatalf("build server: %v", err)
	}

	server := httptest.NewServer(components.Server.Handler)
	defer server.Close()

	beacon := mustNew(t, server.URL)

	if _, err := beacon.Register(t.Context(), orders); err != nil {
		t.Fatalf("first register: %v", err)
	}

	_, err = beacon.Register(t.Context(), orders)

	if !errors.Is(err, client.ErrQuotaExceeded) {
		t.Fatalf("expected ErrQuotaExceeded, got %v", err)
	}

	if errors.Is(err, client.ErrInvalid) || errors.Is(err, client.ErrNotFound) {
		t.Error("a full registry must not look like a bad request or a missing instance")
	}
}

// countingHandler counts the requests it forwards to next.
type countingHandler struct {
	next     http.Handler
	requests atomic.Int32
}

// ServeHTTP counts and forwards.
func (handler *countingHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	handler.requests.Add(1)
	handler.next.ServeHTTP(writer, request)
}

func TestDiscoverFollowsPages(t *testing.T) {
	// Registering 1203 instances from one address at once is what the
	// registration rate limit exists to stop.
	settings := config.Default()
	settings.RateLimit.RegistrationsPerSecond = 0

	components, err := bootstrap.New(settings, slog.New(slog.DiscardHandler), "v9.9.9")
	if err != nil {
		t.Fatalf("build server: %v", err)
	}

	counter := &countingHandler{next: components.Server.Handler}

	// Behind a proxy prefix, to prove only the cursor is taken from the
	// server's Link header, not its path.
	mux := http.NewServeMux()
	mux.Handle("/beacon/", http.StripPrefix("/beacon", counter))

	server := httptest.NewServer(mux)
	defer server.Close()

	beacon := mustNew(t, server.URL+"/beacon")

	const total = 1203 // three pages of 500
	for range total {
		if _, err := beacon.Register(t.Context(), orders); err != nil {
			t.Fatalf("register: %v", err)
		}
	}

	counter.requests.Store(0)

	instances, err := beacon.Discover(t.Context(), "orders")
	if err != nil {
		t.Fatalf("discover: %v", err)
	}

	if got := counter.requests.Load(); got != 3 {
		t.Errorf("expected 3 page requests, got %d", got)
	}

	if len(instances) != total {
		t.Fatalf("expected %d instances, got %d", total, len(instances))
	}

	seen := make(map[string]bool, total)
	for index, instance := range instances {
		if seen[instance.ID] {
			t.Fatalf("instance %s returned twice", instance.ID)
		}

		seen[instance.ID] = true

		if index > 0 && instances[index-1].ID >= instance.ID {
			t.Fatalf("expected instances sorted by ID, %s came before %s", instances[index-1].ID, instance.ID)
		}
	}
}

func TestDiscoverWorksWithServersWithoutPaging(t *testing.T) {
	// Beacon v0.1.0 ignores limit and returns everything, without a Link.
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(writer, `[{"id":"a","name":"orders","address":"10.0.0.1","port":1},{"id":"b","name":"orders","address":"10.0.0.2","port":1}]`)
	}))
	defer server.Close()

	instances, err := mustNew(t, server.URL).Discover(t.Context(), "orders")
	if err != nil || len(instances) != 2 {
		t.Errorf("expected both instances from a server without paging, got %d, %v", len(instances), err)
	}
}

func TestDiscoverStopsOnARepeatedCursor(t *testing.T) {
	// A broken server that always links to the same page must not make the
	// client loop forever.
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Link", `</api/v1/services?limit=500&cursor=c2FtZQ>; rel="next"`)
		_, _ = io.WriteString(writer, `[{"id":"a","name":"orders","address":"10.0.0.1","port":1}]`)
	}))
	defer server.Close()

	if _, err := mustNew(t, server.URL).Discover(t.Context(), "orders"); err == nil {
		t.Error("expected an error for a repeated cursor")
	}
}

func TestDiscoverEmptyIsAnEmptySlice(t *testing.T) {
	instances, err := newServer(t).Discover(t.Context(), "nothing")
	if err != nil || instances == nil || len(instances) != 0 {
		t.Errorf("expected an empty, non-nil slice, got %#v, %v", instances, err)
	}
}

// pagingServer answers every discovery request with one instance and the
// Link header that link returns for the request's cursor.
func pagingServer(t *testing.T, link func(cursor string) string) *client.Client {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if value := link(request.URL.Query().Get("cursor")); value != "" {
			writer.Header().Set("Link", value)
		}

		_, _ = io.WriteString(writer, `[{"id":"a","name":"orders","address":"10.0.0.1","port":1}]`)
	}))
	t.Cleanup(server.Close)

	return mustNew(t, server.URL)
}

func TestDiscoverStopsOnACursorCycle(t *testing.T) {
	// "" -> A -> B -> A -> B ... never repeats the cursor it was just sent.
	next := map[string]string{"": "QQ", "QQ": "Qg", "Qg": "QQ"}

	beacon := pagingServer(t, func(cursor string) string {
		return `</api/v1/services?cursor=` + next[cursor] + `>; rel="next"`
	})

	if _, err := beacon.Discover(t.Context(), "orders"); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Errorf("expected an error for a cursor cycle, got %v", err)
	}
}

func TestDiscoverFailsOnANextLinkWithoutACursor(t *testing.T) {
	for _, link := range []string{
		`</api/v1/services?limit=500>; rel="next"`,
		`</api/v1/services?cursor=>; rel="next"`,
		`<http://[::1>; rel="next"`,
	} {
		t.Run(link, func(t *testing.T) {
			beacon := pagingServer(t, func(string) string { return link })

			// Stopping quietly would return a partial list as if it were all.
			if instances, err := beacon.Discover(t.Context(), "orders"); err == nil {
				t.Errorf("expected an error, got %d instances", len(instances))
			}
		})
	}
}

func TestDiscoverIgnoresOtherLinks(t *testing.T) {
	beacon := pagingServer(t, func(string) string { return `</docs>; rel="help"` })

	if instances, err := beacon.Discover(t.Context(), "orders"); err != nil || len(instances) != 1 {
		t.Errorf("expected one page and no error, got %d, %v", len(instances), err)
	}
}

// roundTripFunc is an http.RoundTripper made from a function.
type roundTripFunc func(*http.Request) (*http.Response, error)

// RoundTrip calls the function.
func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestDiscoverGivesUpAfterTooManyPages(t *testing.T) {
	// A server that hands out a new cursor on every page, forever. A
	// RoundTripper instead of a real server keeps ten thousand pages fast.
	var requests atomic.Int64

	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		page := requests.Add(1)

		header := http.Header{}
		header.Set("Link", fmt.Sprintf(`</api/v1/services?cursor=c%d>; rel="next"`, page))

		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     header,
			Body:       io.NopCloser(strings.NewReader(`[]`)),
			Request:    request,
		}, nil
	})

	beacon := mustNew(t, "http://beacon.test", client.WithHTTPClient(&http.Client{Transport: transport}))

	_, err := beacon.Discover(t.Context(), "orders")
	if err == nil || !strings.Contains(err.Error(), "pages") {
		t.Fatalf("expected Discover to give up, got %v", err)
	}

	if got := requests.Load(); got != 10_000 {
		t.Errorf("expected exactly 10000 page requests, got %d", got)
	}
}

func TestRegisterTooOftenMatchesErrRateLimited(t *testing.T) {
	settings := config.Default()
	settings.RateLimit.RegistrationsPerSecond = 0.001
	settings.RateLimit.RegistrationBurst = 1

	components, err := bootstrap.New(settings, slog.New(slog.DiscardHandler), "v9.9.9")
	if err != nil {
		t.Fatalf("build server: %v", err)
	}

	server := httptest.NewServer(components.Server.Handler)
	defer server.Close()

	beacon := mustNew(t, server.URL)

	if _, err := beacon.Register(t.Context(), orders); err != nil {
		t.Fatalf("first registration: %v", err)
	}

	_, err = beacon.Register(t.Context(), orders)

	if !errors.Is(err, client.ErrRateLimited) {
		t.Fatalf("expected ErrRateLimited, got %v", err)
	}

	if errors.Is(err, client.ErrQuotaExceeded) {
		t.Error("a rate limit must not look like a full registry")
	}
}

func TestProxyTooManyRequestsMatchesErrRateLimited(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Error(writer, "slow down", http.StatusTooManyRequests)
	}))
	defer server.Close()

	_, err := mustNew(t, server.URL).Register(t.Context(), orders)
	if !errors.Is(err, client.ErrRateLimited) {
		t.Errorf("expected a plain 429 to match ErrRateLimited, got %v", err)
	}

	if errors.Is(err, client.ErrQuotaExceeded) {
		t.Error("a plain 429 must not look like a full registry")
	}
}
