package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nm1996/beacon/internal/auth"
	"github.com/nm1996/beacon/internal/discovery"
	"github.com/nm1996/beacon/internal/ratelimit"
	"github.com/nm1996/beacon/internal/registry/memory"
)

// benchmarkRouter returns a router over a registry holding size instances
// of one service, without authentication or rate limits.
func benchmarkRouter(b *testing.B, size int) http.Handler {
	b.Helper()

	service, err := discovery.New(memory.New(), discovery.Config{TTL: time.Hour, CleanupInterval: time.Second, MaxInstances: 1 << 30}, discard)
	if err != nil {
		b.Fatal(err)
	}

	for index := range size {
		registration := discovery.Registration{Name: "orders", Address: "10.0.0.1", Port: 1 + index%65535}
		if _, err := service.Register(b.Context(), registration, discovery.Caller{AnyInstance: true}); err != nil {
			b.Fatal(err)
		}
	}

	return NewRouter(service, discard, testConfig)
}

// BenchmarkHTTPRegister is POST /api/v1/services through the router:
// decoding, validation, the store and the JSON response.
func BenchmarkHTTPRegister(b *testing.B) {
	router := benchmarkRouter(b, 0)
	body := `{"name":"orders","address":"10.0.0.1","port":8081}`

	b.ReportAllocs()

	for b.Loop() {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, servicesPath, strings.NewReader(body)))

		if recorder.Code != http.StatusCreated {
			b.Fatalf("expected 201, got %d", recorder.Code)
		}
	}
}

// BenchmarkHTTPListPage is one page of GET /api/v1/services, as the Go
// client asks for it, from registries of several sizes: the cost of
// encoding 500 instances plus the store's scan.
func BenchmarkHTTPListPage(b *testing.B) {
	for _, size := range []int{1_000, 10_000} {
		b.Run(fmt.Sprintf("size=%d", size), func(b *testing.B) {
			router := benchmarkRouter(b, size)

			b.ReportAllocs()

			for b.Loop() {
				recorder := httptest.NewRecorder()
				router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, servicesPath+"?name=orders&limit=500", nil))

				if recorder.Code != http.StatusOK {
					b.Fatalf("expected 200, got %d", recorder.Code)
				}
			}
		})
	}
}

// BenchmarkGuard is the Guard alone, in front of a handler that does
// nothing, for a valid token: with and without the failed-authentication
// limit, whose reservation is taken and given back on every request.
func BenchmarkGuard(b *testing.B) {
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})

	for _, test := range []struct {
		name    string
		options []GuardOption
	}{
		{name: "no-limit"},
		{name: "failure-limit", options: []GuardOption{WithFailureLimit(ratelimit.New(1, 20, 0), nil)}},
	} {
		b.Run(test.name, func(b *testing.B) {
			handler := NewGuard(failureTokens, discard, nil, test.options...).Require(auth.PermissionDiscover, next)
			request := httptest.NewRequest(http.MethodGet, servicesPath, nil)
			request.Header.Set("Authorization", "Bearer good-token")

			b.ReportAllocs()

			for b.Loop() {
				handler.ServeHTTP(httptest.NewRecorder(), request)
			}
		})
	}
}
