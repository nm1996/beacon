package client_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nm1996/beacon/client"
	"github.com/nm1996/beacon/internal/bootstrap"
	"github.com/nm1996/beacon/internal/config"
)

// fakeBeacon is a real Beacon handler the test can restart, take down and
// count heartbeats on. It is reached through handlerTransport, without a
// network, so the tests can run in a synctest bubble.
type fakeBeacon struct {
	t   *testing.T
	ttl time.Duration

	mu      sync.Mutex
	handler http.Handler

	down       atomic.Bool
	heartbeats atomic.Int32
	succeeded  atomic.Int32
}

// newFakeBeacon returns a running fake with an empty registry and the
// default 30s TTL.
func newFakeBeacon(t *testing.T) *fakeBeacon {
	t.Helper()

	return newFakeBeaconWithTTL(t, config.Default().Registry.ServiceTTL)
}

// newFakeBeaconWithTTL returns a running fake that reports ttl.
func newFakeBeaconWithTTL(t *testing.T, ttl time.Duration) *fakeBeacon {
	t.Helper()

	beacon := &fakeBeacon{t: t, ttl: ttl}
	beacon.restart()

	return beacon
}

// restart replaces the server with a fresh one that remembers nothing, as a
// real Beacon does after a restart.
func (beacon *fakeBeacon) restart() {
	beacon.t.Helper()

	settings := config.Default()
	settings.Registry.ServiceTTL = beacon.ttl
	settings.Registry.CleanupInterval = min(settings.Registry.CleanupInterval, beacon.ttl)

	components, err := bootstrap.New(settings, slog.New(slog.DiscardHandler), "test")
	if err != nil {
		beacon.t.Fatalf("build server: %v", err)
	}

	beacon.mu.Lock()
	beacon.handler = components.Server.Handler
	beacon.mu.Unlock()
}

// ServeHTTP answers 503 while the fake is down and otherwise forwards to
// the current server, counting heartbeats on the way.
func (beacon *fakeBeacon) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	isHeartbeat := strings.HasSuffix(request.URL.Path, "/heartbeat")
	if isHeartbeat {
		beacon.heartbeats.Add(1)
	}

	if beacon.down.Load() {
		http.Error(writer, "unavailable", http.StatusServiceUnavailable)
		return
	}

	beacon.mu.Lock()
	handler := beacon.handler
	beacon.mu.Unlock()

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if isHeartbeat && recorder.Code == http.StatusNoContent {
		beacon.succeeded.Add(1)
	}

	for key, values := range recorder.Header() {
		writer.Header()[key] = values
	}

	writer.WriteHeader(recorder.Code)
	_, _ = writer.Write(recorder.Body.Bytes())
}

// handlerTransport is an http.RoundTripper that serves requests with a
// handler in the same goroutine.
type handlerTransport struct {
	handler http.Handler
}

// RoundTrip serves request with the handler.
func (transport handlerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if err := request.Context().Err(); err != nil {
		return nil, err
	}

	recorder := httptest.NewRecorder()
	transport.handler.ServeHTTP(recorder, request)

	return recorder.Result(), nil
}

// clientFor returns a client that talks to beacon in memory.
func clientFor(t *testing.T, beacon *fakeBeacon) *client.Client {
	t.Helper()

	return mustNew(t, "http://beacon.test", client.WithHTTPClient(&http.Client{Transport: handlerTransport{handler: beacon}}))
}

// instanceIDs lists the IDs Beacon currently holds.
func instanceIDs(t *testing.T, beacon *client.Client) []string {
	t.Helper()

	instances, err := beacon.Discover(t.Context(), "")
	if err != nil {
		t.Fatalf("discover: %v", err)
	}

	ids := make([]string, 0, len(instances))
	for _, instance := range instances {
		ids = append(ids, instance.ID)
	}

	return ids
}

// mustAnnounce announces orders and fails the test if that fails.
func mustAnnounce(t *testing.T, beacon *client.Client) *client.Lease {
	t.Helper()

	lease, err := beacon.Announce(t.Context(), orders)
	if err != nil {
		t.Fatalf("announce: %v", err)
	}

	return lease
}

func TestAnnounceRegistersAndCloseUnregisters(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fake := newFakeBeacon(t)
		beacon := clientFor(t, fake)

		lease := mustAnnounce(t, beacon)

		if ids := instanceIDs(t, beacon); len(ids) != 1 || ids[0] != lease.ID() {
			t.Fatalf("expected the lease's instance to be registered, got %v", ids)
		}

		// A deadline turns a Close that never returns into a clear failure.
		ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
		defer cancel()

		if err := lease.Close(ctx); err != nil {
			t.Fatalf("close: %v", err)
		}

		if ids := instanceIDs(t, beacon); len(ids) != 0 {
			t.Errorf("expected Close to unregister, %v remain", ids)
		}

		// Nothing may be sent once Close has returned.
		sent := fake.heartbeats.Load()
		time.Sleep(5 * time.Minute)

		if fake.heartbeats.Load() != sent {
			t.Error("expected no heartbeats after Close")
		}
	})
}

func TestLeaseSendsHeartbeatsAtAThirdOfTheTTL(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fake := newFakeBeacon(t)
		lease := mustAnnounce(t, clientFor(t, fake))
		defer lease.Close(context.Background())

		// The default TTL is 30s, so heartbeats come every 10s ±10%: the
		// third lands between 27s and 33s, the fourth no earlier than 36s.
		time.Sleep(35 * time.Second)
		synctest.Wait()

		if got := fake.succeeded.Load(); got != 3 {
			t.Errorf("expected 3 heartbeats in 35s, got %d", got)
		}
	})
}

func TestLeaseRegistersAgainAfterBeaconRestarts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fake := newFakeBeacon(t)
		beacon := clientFor(t, fake)
		lease := mustAnnounce(t, beacon)
		defer lease.Close(context.Background())

		before := lease.ID()
		fake.restart()

		// The next heartbeat gets 404 and the lease registers again.
		time.Sleep(12 * time.Second)
		synctest.Wait()

		after := lease.ID()
		if after == before {
			t.Fatal("expected a new ID after Beacon forgot the instance")
		}

		if ids := instanceIDs(t, beacon); len(ids) != 1 || ids[0] != after {
			t.Errorf("expected the restarted Beacon to hold the new ID %s, got %v", after, ids)
		}

		// And heartbeats continue for the new ID.
		succeeded := fake.succeeded.Load()
		time.Sleep(12 * time.Second)
		synctest.Wait()

		if fake.succeeded.Load() <= succeeded {
			t.Error("expected heartbeats to continue after registering again")
		}
	})
}

func TestLeaseRetriesWhileBeaconIsDown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fake := newFakeBeacon(t)
		lease := mustAnnounce(t, clientFor(t, fake))
		defer lease.Close(context.Background())

		id := lease.ID()

		// Take Beacon down just before the first heartbeat is due.
		time.Sleep(8 * time.Second)
		fake.down.Store(true)

		time.Sleep(25 * time.Second)
		synctest.Wait()

		// Retries come after about 1s, 2s, 4s and 8s, then every 10s: far
		// more often than the normal pace of one per 10s.
		if attempts := fake.heartbeats.Load(); attempts < 5 {
			t.Errorf("expected at least 5 attempts while Beacon was down, got %d", attempts)
		}

		fake.down.Store(false)
		time.Sleep(12 * time.Second)
		synctest.Wait()

		if fake.succeeded.Load() == 0 {
			t.Error("expected heartbeats to succeed once Beacon was back")
		}

		if lease.ID() != id {
			t.Error("expected the same ID, since Beacon never forgot the instance")
		}
	})
}

func TestCloseCanBeRetriedAfterBeaconWasUnreachable(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fake := newFakeBeacon(t)
		beacon := clientFor(t, fake)
		lease := mustAnnounce(t, beacon)

		fake.down.Store(true)

		if err := lease.Close(t.Context()); err == nil {
			t.Fatal("expected Close to report that unregistering failed")
		}

		if err := lease.Close(t.Context()); err == nil {
			t.Fatal("expected a retry while Beacon is still down to fail too")
		}

		// The heartbeats stopped with the first Close and stay stopped.
		sent := fake.heartbeats.Load()
		time.Sleep(5 * time.Minute)

		if fake.heartbeats.Load() != sent {
			t.Error("expected no heartbeats after the first Close")
		}

		fake.down.Store(false)

		if err := lease.Close(t.Context()); err != nil {
			t.Fatalf("expected Close to succeed once Beacon was back, got %v", err)
		}

		if ids := instanceIDs(t, beacon); len(ids) != 0 {
			t.Errorf("expected the instance to be unregistered, %v remain", ids)
		}

		// Done: further calls send nothing.
		fake.down.Store(true)

		if err := lease.Close(t.Context()); err != nil {
			t.Errorf("expected Close after unregistering to return nil, got %v", err)
		}
	})
}

func TestCloseCanBeRetriedAfterItsContextEnded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fake := newFakeBeacon(t)
		beacon := clientFor(t, fake)
		lease := mustAnnounce(t, beacon)

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		if err := lease.Close(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}

		if err := lease.Close(t.Context()); err != nil {
			t.Fatalf("expected a retry with a live context to succeed, got %v", err)
		}

		if ids := instanceIDs(t, beacon); len(ids) != 0 {
			t.Errorf("expected the retry to unregister, %v remain", ids)
		}
	})
}

func TestConcurrentCloseUnregistersOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fake := newFakeBeacon(t)
		beacon := clientFor(t, fake)
		lease := mustAnnounce(t, beacon)

		var closers sync.WaitGroup
		for range 8 {
			closers.Go(func() {
				if err := lease.Close(t.Context()); err != nil {
					t.Errorf("close: %v", err)
				}
			})
		}

		closers.Wait()

		if ids := instanceIDs(t, beacon); len(ids) != 0 {
			t.Errorf("expected the instance to be unregistered, %v remain", ids)
		}
	})
}

func TestCloseWhileHeartbeatsAreFailing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fake := newFakeBeacon(t)
		lease := mustAnnounce(t, clientFor(t, fake))

		fake.down.Store(true)
		time.Sleep(20 * time.Second) // several failed heartbeats and retries
		fake.down.Store(false)

		ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
		defer cancel()

		if err := lease.Close(ctx); err != nil {
			t.Fatalf("expected Close to stop the retry loop and unregister, got %v", err)
		}
	})
}

func TestAnnounceFailsWithoutStartingAnything(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fake := newFakeBeacon(t)

		_, err := clientFor(t, fake).Announce(t.Context(), client.Registration{Name: "orders", Address: "10.0.0.12"})

		if !errors.Is(err, client.ErrInvalid) {
			t.Fatalf("expected ErrInvalid, got %v", err)
		}

		// No goroutine may be left behind; synctest fails the test if one is.
		time.Sleep(time.Minute)

		if fake.heartbeats.Load() != 0 {
			t.Error("expected no heartbeats from a failed Announce")
		}
	})
}

func TestLeaseIDIsSafeToReadWhileRenewing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fake := newFakeBeacon(t)
		lease := mustAnnounce(t, clientFor(t, fake))
		defer lease.Close(context.Background())

		var readers sync.WaitGroup
		for range 4 {
			readers.Go(func() {
				for range 50 {
					_ = lease.ID()
					time.Sleep(time.Second)
				}
			})
		}

		for range 3 {
			fake.restart()
			time.Sleep(12 * time.Second)
		}

		readers.Wait()
	})
}

// attemptsWhileDown takes fake down right after Announce, waits outage, and
// returns how many heartbeats the lease tried meanwhile.
func attemptsWhileDown(t *testing.T, ttl, outage time.Duration) int32 {
	t.Helper()

	fake := newFakeBeaconWithTTL(t, ttl)
	lease := mustAnnounce(t, clientFor(t, fake))
	defer lease.Close(context.Background())

	fake.down.Store(true)
	time.Sleep(outage)
	synctest.Wait()

	return fake.heartbeats.Load()
}

func TestRetryPaceDependsOnTTL(t *testing.T) {
	// Retries start after 1s and double, but never wait longer than TTL/3,
	// the normal heartbeat interval, so an instance is never left to expire
	// just because the client backed off. With a short TTL that cap is below
	// 1s and retries come at a steady TTL/3.
	tests := []struct {
		name     string
		ttl      time.Duration
		outage   time.Duration
		min, max int32
	}{
		// Every ~333ms: about 30 in 10s.
		{name: "1s TTL", ttl: time.Second, outage: 10 * time.Second, min: 25, max: 36},
		// Every ~1s: the first retry delay already equals TTL/3.
		{name: "3s TTL", ttl: 3 * time.Second, outage: 10 * time.Second, min: 8, max: 12},
		// First attempt at ~10s, then retries after ~1s, 2s, 4s, 8s and
		// every ~10s after that: about 8 in 60s.
		{name: "30s TTL", ttl: 30 * time.Second, outage: time.Minute, min: 6, max: 10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				if got := attemptsWhileDown(t, tt.ttl, tt.outage); got < tt.min || got > tt.max {
					t.Errorf("expected %d to %d attempts in %s, got %d", tt.min, tt.max, tt.outage, got)
				}
			})
		})
	}
}

func TestLeaseRecoversAfterALongOutage(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fake := newFakeBeacon(t)
		beacon := clientFor(t, fake)
		lease := mustAnnounce(t, beacon)
		defer lease.Close(context.Background())

		before := lease.ID()

		// Beacon is down for an hour and comes back restarted, having
		// forgotten every instance.
		fake.down.Store(true)
		time.Sleep(time.Hour)
		fake.restart()
		fake.down.Store(false)

		// Retries are capped at TTL/3, so the lease notices within ~11s.
		time.Sleep(12 * time.Second)
		synctest.Wait()

		if lease.ID() == before {
			t.Fatal("expected the lease to register again after the outage")
		}

		if ids := instanceIDs(t, beacon); len(ids) != 1 || ids[0] != lease.ID() {
			t.Errorf("expected Beacon to hold the new ID, got %v", ids)
		}
	})
}
