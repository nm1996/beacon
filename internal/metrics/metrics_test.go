package metrics

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/nm1996/beacon/internal/discovery"
	"github.com/nm1996/beacon/internal/registry/memory"
)

// scrape returns what GET /metrics serves, and its status code.
func scrape(t *testing.T, m *Metrics) (int, string) {
	t.Helper()

	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	body, err := io.ReadAll(rec.Body)
	if err != nil {
		t.Fatalf("read scrape: %v", err)
	}

	return rec.Code, string(body)
}

func TestHandlerExposesBuildInfoAndRuntimeMetrics(t *testing.T) {
	code, body := scrape(t, New("v1.2.3"))

	if code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", code)
	}

	for _, want := range []string{`beacon_build_info{goversion="go`, `version="v1.2.3"} 1`, "go_goroutines", "process_start_time_seconds"} {
		if !strings.Contains(body, want) {
			t.Errorf("expected the scrape to contain %q", want)
		}
	}
}

func TestInstrumentHTTPLabelsByRoutePattern(t *testing.T) {
	m := New("test")

	inner := http.NewServeMux()
	inner.HandleFunc("GET /items/{id}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })

	// Mirror bootstrap: an outer mux in front of the instrumented one, whose
	// own "/" pattern must not leak into the labels.
	outer := http.NewServeMux()
	outer.Handle("/", m.InstrumentHTTP(inner))

	for _, path := range []string{"/items/a", "/items/b", "/items/c", "/nope", "/also-nope"} {
		outer.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}

	if got := testutil.ToFloat64(m.httpRequests.WithLabelValues("GET", "GET /items/{id}", "204")); got != 3 {
		t.Errorf("expected 3 requests on the item route, got %v", got)
	}

	if got := testutil.ToFloat64(m.httpRequests.WithLabelValues("GET", unmatchedRoute, "404")); got != 2 {
		t.Errorf("expected 2 unmatched requests, got %v", got)
	}

	// Two series in total: instance IDs and unknown paths never become labels.
	if got := testutil.CollectAndCount(m.httpRequests); got != 2 {
		t.Errorf("expected 2 request series, got %d", got)
	}

	if got := testutil.CollectAndCount(m.httpDuration); got != 2 {
		t.Errorf("expected 2 duration series, got %d", got)
	}
}

func TestInstrumentHTTPRecordsImplicitStatus(t *testing.T) {
	m := New("test")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /plain", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") })

	m.InstrumentHTTP(mux).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/plain", nil))

	if got := testutil.ToFloat64(m.httpRequests.WithLabelValues("GET", "GET /plain", "200")); got != 1 {
		t.Errorf("expected a handler that never calls WriteHeader to count as 200, got %v", got)
	}
}

func TestInstrumentRegistryCountsOperationsAndResults(t *testing.T) {
	m := New("test")
	registry := m.InstrumentRegistry(memory.New())
	ctx := t.Context()
	old := time.Now().Add(-time.Hour)

	for _, id := range []string{"a", "b", "c"} {
		if err := registry.Create(ctx, discovery.Instance{ID: id, Name: "orders", LastHeartbeat: old}, 100); err != nil {
			t.Fatalf("create: %v", err)
		}
	}

	_ = registry.Touch(ctx, "a", time.Now(), discovery.OwnerCheck{Any: true})
	_ = registry.Touch(ctx, "missing", time.Now(), discovery.OwnerCheck{Any: true})
	_, _ = registry.List(ctx, "")
	_ = registry.Delete(ctx, "missing", discovery.OwnerCheck{Any: true})

	removed, err := registry.DeleteStale(ctx, time.Now().Add(-time.Minute))
	if err != nil || removed != 2 {
		t.Fatalf("expected 2 stale instances removed, got %d, %v", removed, err)
	}

	for _, tt := range []struct {
		operation string
		result    string
		want      float64
	}{
		{operation: "create", result: "ok", want: 3},
		{operation: "touch", result: "ok", want: 1},
		{operation: "touch", result: "not_found", want: 1},
		{operation: "list", result: "ok", want: 1},
		{operation: "delete", result: "not_found", want: 1},
		{operation: "delete_stale", result: "ok", want: 1},
	} {
		if got := testutil.ToFloat64(m.registryOperations.WithLabelValues(tt.operation, tt.result)); got != tt.want {
			t.Errorf("%s/%s: expected %v, got %v", tt.operation, tt.result, tt.want, got)
		}
	}

	if err := registry.Create(ctx, discovery.Instance{ID: "over", Name: "orders"}, 1); !errors.Is(err, discovery.ErrRegistryFull) {
		t.Fatalf("expected ErrRegistryFull, got %v", err)
	}

	if got := testutil.ToFloat64(m.registryOperations.WithLabelValues("create", "full")); got != 1 {
		t.Errorf("expected 1 refused create counted as full, got %v", got)
	}

	if got := testutil.ToFloat64(m.expiredInstances); got != 2 {
		t.Errorf("expected 2 expired instances, got %v", got)
	}
}

// brokenRegistry is a discovery.Registry whose List always fails.
type brokenRegistry struct {
	discovery.Registry
}

// List always fails.
func (brokenRegistry) List(context.Context, string) ([]discovery.Instance, error) {
	return nil, errors.New("backend unavailable")
}

func TestInstrumentRegistryCountsBackendErrors(t *testing.T) {
	m := New("test")
	registry := m.InstrumentRegistry(brokenRegistry{Registry: memory.New()})

	if _, err := registry.List(t.Context(), ""); err == nil {
		t.Fatal("expected the backend error to pass through")
	}

	if got := testutil.ToFloat64(m.registryOperations.WithLabelValues("list", "error")); got != 1 {
		t.Errorf("expected 1 failed list, got %v", got)
	}
}

func TestWatchInstancesCountsByService(t *testing.T) {
	m := New("test")
	registry := memory.New()
	m.WatchInstances(registry.List)

	for id, name := range map[string]string{"a": "orders", "b": "orders", "c": "payments"} {
		if err := registry.Create(t.Context(), discovery.Instance{ID: id, Name: name}, 1000); err != nil {
			t.Fatalf("create: %v", err)
		}
	}

	_, body := scrape(t, m)

	for _, want := range []string{"\nbeacon_instances 3\n", "\nbeacon_services 2\n"} {
		if !strings.Contains(body, want) {
			t.Errorf("expected the scrape to contain %q", strings.TrimSpace(want))
		}
	}
}

func TestInstanceMetricsDoNotGrowWithServiceNames(t *testing.T) {
	m := New("test")
	registry := memory.New()
	m.WatchInstances(registry.List)

	// Every registration uses a new name, as a careless or hostile client
	// might.
	for index := range 500 {
		id := fmt.Sprintf("id-%d", index)
		if err := registry.Create(t.Context(), discovery.Instance{ID: id, Name: fmt.Sprintf("service-%06d", index)}, 1000); err != nil {
			t.Fatalf("create: %v", err)
		}
	}

	_, body := scrape(t, m)

	if strings.Contains(body, `service="`) {
		t.Error("expected no metric to carry a service label")
	}

	series := 0
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "beacon_instances") || strings.HasPrefix(line, "beacon_services") {
			series++
		}
	}

	if series != 2 {
		t.Errorf("expected exactly 2 instance series for 500 service names, got %d", series)
	}
}

func TestWatchInstancesReportsBackendFailureAsScrapeError(t *testing.T) {
	m := New("test")
	m.WatchInstances(brokenRegistry{}.List)

	// A failing count must not be reported as zero instances.
	if code, body := scrape(t, m); code != http.StatusInternalServerError || strings.Contains(body, "\nbeacon_instances ") || strings.Contains(body, "\nbeacon_services ") {
		t.Errorf("expected a 500 scrape without an instances value, got %d:\n%s", code, body)
	}
}

func TestInstrumentHTTPGroupsUnknownMethods(t *testing.T) {
	m := New("test")
	handler := m.InstrumentHTTP(http.NewServeMux())

	for index := range 50 {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(fmt.Sprintf("METHOD%d", index), "/", nil))
	}

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	if got := testutil.ToFloat64(m.httpRequests.WithLabelValues(otherMethod, unmatchedRoute, "404")); got != 50 {
		t.Errorf("expected 50 requests under method=%q, got %v", otherMethod, got)
	}

	// One series for the made-up methods and one for GET.
	if got := testutil.CollectAndCount(m.httpRequests); got != 2 {
		t.Errorf("expected 2 request series, got %d", got)
	}
}
