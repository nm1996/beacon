package client_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nm1996/beacon/client"
	"github.com/nm1996/beacon/internal/bootstrap"
	"github.com/nm1996/beacon/internal/config"
)

// beaconHandler returns a real Beacon handler with default settings.
func beaconHandler(t *testing.T) http.Handler {
	t.Helper()

	components, err := bootstrap.New(config.Default(), slog.New(slog.DiscardHandler), "test")
	if err != nil {
		t.Fatalf("build server: %v", err)
	}

	return components.Server.Handler
}

func TestRegisterSurvivesALostResponse(t *testing.T) {
	beacon := beaconHandler(t)

	var (
		attempts atomic.Int32
		keys     []string
	)

	// The first registration reaches Beacon, but its response is lost and
	// the client sees 503 instead.
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost {
			keys = append(keys, request.Header.Get("Idempotency-Key"))

			if attempts.Add(1) == 1 {
				beacon.ServeHTTP(httptest.NewRecorder(), request)
				http.Error(writer, "upstream timed out", http.StatusServiceUnavailable)

				return
			}
		}

		beacon.ServeHTTP(writer, request)
	}))
	defer server.Close()

	beaconClient := mustNew(t, server.URL)

	registered, err := beaconClient.Register(t.Context(), client.Registration{Name: "orders", Address: "127.0.0.1", Port: 8081})
	if err != nil {
		t.Fatalf("expected the retry to succeed, got %v", err)
	}

	if len(keys) != 2 || keys[0] == "" || keys[0] != keys[1] {
		t.Errorf("expected two attempts with the same Idempotency-Key, got %q", keys)
	}

	instances, err := beaconClient.Discover(t.Context(), "orders")
	if err != nil || len(instances) != 1 || instances[0].ID != registered.ID {
		t.Errorf("expected exactly the one instance %s, got %+v, %v", registered.ID, instances, err)
	}
}

func TestRegisterDoesNotRetryDefiniteAnswers(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusTooManyRequests, http.StatusInternalServerError} {
		var attempts atomic.Int32

		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			attempts.Add(1)
			writer.WriteHeader(status)
		}))

		_, err := mustNew(t, server.URL).Register(t.Context(), client.Registration{Name: "orders", Address: "127.0.0.1", Port: 8081})
		server.Close()

		if err == nil || attempts.Load() != 1 {
			t.Errorf("%d: expected one attempt and an error, got %d, %v", status, attempts.Load(), err)
		}
	}
}

func TestRegisterGivesUpAfterThreeAttempts(t *testing.T) {
	var attempts atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		writer.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()

	_, err := mustNew(t, server.URL).Register(t.Context(), client.Registration{Name: "orders", Address: "127.0.0.1", Port: 8081})
	if err == nil || attempts.Load() != 3 {
		t.Errorf("expected three attempts and an error, got %d, %v", attempts.Load(), err)
	}
}

func TestAnnounceWithAnIDKeepsIt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fake := newFakeBeacon(t)
		beacon := clientFor(t, fake)

		lease, err := beacon.Announce(t.Context(), client.Registration{ID: "orders-pod-1", Name: "orders", Address: "127.0.0.1", Port: 8081})
		if err != nil {
			t.Fatal(err)
		}
		defer lease.Close(context.Background())

		if lease.ID() != "orders-pod-1" {
			t.Errorf("expected the chosen ID, got %s", lease.ID())
		}

		// Registering the same instance again, as a restarted process would,
		// is no conflict.
		if _, err := beacon.Register(t.Context(), client.Registration{ID: "orders-pod-1", Name: "orders", Address: "127.0.0.1", Port: 8081}); err != nil {
			t.Errorf("expected registering the same instance again to succeed, got %v", err)
		}

		if _, err := beacon.Register(t.Context(), client.Registration{ID: "orders-pod-1", Name: "orders", Address: "127.0.0.1", Port: 9999}); !errors.Is(err, client.ErrConflict) {
			t.Errorf("expected another port under the ID to match ErrConflict, got %v", err)
		}
	})
}

func TestLeaseTakesANewIDWhenItsOwnIsTaken(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fake := newFakeBeacon(t)
		beacon := clientFor(t, fake)
		lease := mustAnnounce(t, beacon)
		defer lease.Close(context.Background())

		id := lease.ID()

		// Beacon restarts. The lease's next heartbeat gets 404, and before
		// it registers again something else takes its ID.
		fake.onNotFound = func() {
			if _, err := beacon.Register(context.Background(), client.Registration{ID: id, Name: "intruder", Address: "127.0.0.2", Port: 1}); err != nil {
				t.Errorf("register the intruder: %v", err)
			}
		}
		fake.restart()

		time.Sleep(12 * time.Second)
		synctest.Wait()

		if lease.ID() == id || lease.ID() == "" {
			t.Fatalf("expected the lease to register under a new ID, got %s", lease.ID())
		}

		if ids := instanceIDs(t, beacon); len(ids) != 2 {
			t.Errorf("expected the intruder and the lease's new instance, got %v", ids)
		}
	})
}

func TestLeaseTakesANewIDWhenAnotherCallerOwnsIt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var notOwned atomic.Int32

		// Heartbeats for the lease's first ID are refused as another
		// caller's, as after a token mix-up; everything else is served.
		fake := newFakeBeacon(t)
		handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/first/heartbeat") {
				notOwned.Add(1)
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(http.StatusForbidden)
				_, _ = writer.Write([]byte(`{"error":{"code":"instance_not_owned","message":"not yours"}}`))

				return
			}

			fake.ServeHTTP(writer, request)
		})

		beacon := mustNew(t, "http://beacon.test", client.WithHTTPClient(&http.Client{Transport: handlerTransport{handler: handler}}))

		lease, err := beacon.Announce(t.Context(), client.Registration{ID: "first", Name: "orders", Address: "127.0.0.1", Port: 8081})
		if err != nil {
			t.Fatal(err)
		}
		defer lease.Close(context.Background())

		time.Sleep(25 * time.Second)
		synctest.Wait()

		if lease.ID() == "first" || notOwned.Load() != 1 {
			t.Errorf("expected one refused heartbeat and then a new ID, got %s after %d refusals", lease.ID(), notOwned.Load())
		}

		if fake.succeeded.Load() == 0 {
			t.Error("expected heartbeats under the new ID to succeed")
		}
	})
}
