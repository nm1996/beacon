package client_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nm1996/beacon/client"
)

// lookupCounter forwards to next and counts lookups and the 304s among
// them.
type lookupCounter struct {
	next        http.Handler
	lookups     atomic.Int32
	notModified atomic.Int32
}

// ServeHTTP forwards request and counts it if it is a lookup.
func (handler *lookupCounter) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	recorder := httptest.NewRecorder()
	handler.next.ServeHTTP(recorder, request)

	if request.Method == http.MethodGet {
		handler.lookups.Add(1)

		if recorder.Code == http.StatusNotModified {
			handler.notModified.Add(1)
		}
	}

	for key, values := range recorder.Header() {
		writer.Header()[key] = values
	}

	writer.WriteHeader(recorder.Code)
	_, _ = writer.Write(recorder.Body.Bytes())
}

// resolverFixture is a fake Beacon behind a counting handler, and a client
// for it.
func resolverFixture(t *testing.T) (*fakeBeacon, *lookupCounter, *client.Client) {
	t.Helper()

	fake := newFakeBeacon(t)
	counter := &lookupCounter{next: fake}
	beacon := mustNew(t, "http://beacon.test", client.WithHTTPClient(&http.Client{Transport: handlerTransport{handler: counter}}))

	return fake, counter, beacon
}

// registerPorts registers one orders instance per port, with IDs that sort
// in port order.
func registerPorts(t *testing.T, beacon *client.Client, ports ...int) {
	t.Helper()

	for _, port := range ports {
		id := "orders-" + string(rune('a'+port%26))
		if _, err := beacon.Register(t.Context(), client.Registration{ID: id, Name: "orders", Address: "127.0.0.1", Port: port}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestResolverPicksRoundRobinWithoutAskingBeacon(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		_, counter, beacon := resolverFixture(t)
		registerPorts(t, beacon, 1, 2, 3)

		resolver, err := beacon.NewResolver(t.Context(), "orders")
		if err != nil {
			t.Fatal(err)
		}
		defer resolver.Close()

		lookups := counter.lookups.Load()

		var ports []int

		for range 6 {
			instance, err := resolver.Pick()
			if err != nil {
				t.Fatal(err)
			}

			ports = append(ports, instance.Port)
		}

		if got := ports; got[0] != 1 || got[1] != 2 || got[2] != 3 || got[3] != 1 || got[5] != 3 {
			t.Errorf("expected the instances in turn, got %v", got)
		}

		if counter.lookups.Load() != lookups {
			t.Error("expected Pick to make no request")
		}
	})
}

func TestResolverRefreshesWithConditionalRequests(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		_, counter, beacon := resolverFixture(t)
		registerPorts(t, beacon, 1)

		resolver, err := beacon.NewResolver(t.Context(), "orders", client.WithRefreshInterval(5*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		defer resolver.Close()

		// Nothing changes for a while: refreshes get 304.
		time.Sleep(16 * time.Second)
		synctest.Wait()

		if counter.notModified.Load() < 2 {
			t.Errorf("expected unchanged refreshes to get 304, got %d of %d", counter.notModified.Load(), counter.lookups.Load())
		}

		registerPorts(t, beacon, 2)

		time.Sleep(6 * time.Second)
		synctest.Wait()

		if got := len(resolver.Instances()); got != 2 {
			t.Errorf("expected the new instance within a refresh interval, got %d instances", got)
		}
	})
}

func TestResolverServesStaleInstancesForAWhile(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fake, _, beacon := resolverFixture(t)
		registerPorts(t, beacon, 1)

		resolver, err := beacon.NewResolver(t.Context(), "orders", client.WithMaxStale(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		defer resolver.Close()

		fake.down.Store(true)

		time.Sleep(50 * time.Second)
		synctest.Wait()

		if _, err := resolver.Pick(); err != nil {
			t.Fatalf("expected the last instances while Beacon is down, got %v", err)
		}

		time.Sleep(20 * time.Second)
		synctest.Wait()

		if _, err := resolver.Pick(); !errors.Is(err, client.ErrStale) {
			t.Fatalf("expected ErrStale after a minute without Beacon, got %v", err)
		}

		// Beacon is back. The instance expired meanwhile, since nothing
		// could reach Beacon, so it registers again.
		fake.down.Store(false)
		registerPorts(t, beacon, 1)

		time.Sleep(6 * time.Second)
		synctest.Wait()

		if _, err := resolver.Pick(); err != nil {
			t.Errorf("expected Pick to recover once Beacon is back, got %v", err)
		}
	})
}

func TestResolverWithoutInstances(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		_, _, beacon := resolverFixture(t)

		resolver, err := beacon.NewResolver(t.Context(), "orders")
		if err != nil {
			t.Fatalf("expected an empty service not to be an error, got %v", err)
		}
		defer resolver.Close()

		if _, err := resolver.Pick(); !errors.Is(err, client.ErrNoInstances) {
			t.Errorf("expected ErrNoInstances, got %v", err)
		}
	})
}

func TestNewResolverFailsWhenBeaconIsUnreachable(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fake, _, beacon := resolverFixture(t)
		fake.down.Store(true)

		if _, err := beacon.NewResolver(t.Context(), "orders"); err == nil {
			t.Fatal("expected an error")
		}

		// No goroutine may be left behind; synctest fails the test if one is.
	})
}

func TestResolverCloseIsIdempotentAndStopsRefreshing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		_, counter, beacon := resolverFixture(t)
		registerPorts(t, beacon, 1)

		resolver, err := beacon.NewResolver(context.Background(), "orders")
		if err != nil {
			t.Fatal(err)
		}

		resolver.Close()
		resolver.Close()

		lookups := counter.lookups.Load()

		time.Sleep(time.Minute)
		synctest.Wait()

		if counter.lookups.Load() != lookups {
			t.Error("expected no refreshes after Close")
		}

		if _, err := resolver.Pick(); err != nil {
			t.Errorf("expected Pick to keep answering after Close, got %v", err)
		}
	})
}

func TestRandomPickerStaysWithinTheInstances(t *testing.T) {
	instances := []client.Instance{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	seen := map[string]bool{}

	for range 300 {
		seen[client.Random().Pick(instances).ID] = true
	}

	if len(seen) != 3 {
		t.Errorf("expected every instance to be picked eventually, got %v", seen)
	}
}

// pickedPorts picks count times and returns the ports seen.
func pickedPorts(t *testing.T, resolver *client.Resolver, count int) map[int]int {
	t.Helper()

	seen := map[int]int{}

	for range count {
		instance, err := resolver.Pick()
		if err != nil {
			t.Fatal(err)
		}

		seen[instance.Port]++
	}

	return seen
}

// instanceOnPort returns the resolver's instance listening on port.
func instanceOnPort(t *testing.T, resolver *client.Resolver, port int) client.Instance {
	t.Helper()

	for _, instance := range resolver.Instances() {
		if instance.Port == port {
			return instance
		}
	}

	t.Fatalf("no instance on port %d", port)

	return client.Instance{}
}

func TestOutlierDetectionIsOffByDefault(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		_, _, beacon := resolverFixture(t)
		registerPorts(t, beacon, 1, 2)

		resolver, err := beacon.NewResolver(t.Context(), "orders")
		if err != nil {
			t.Fatal(err)
		}
		defer resolver.Close()

		for range 100 {
			resolver.Report(instanceOnPort(t, resolver, 1), errors.New("refused"))
		}

		if seen := pickedPorts(t, resolver, 10); seen[1] == 0 {
			t.Errorf("expected reports to change nothing without outlier detection, got %v", seen)
		}
	})
}

func TestFailingInstancesAreEjectedForAWhile(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		_, _, beacon := resolverFixture(t)
		registerPorts(t, beacon, 1, 2, 3)

		resolver, err := beacon.NewResolver(t.Context(), "orders", client.WithOutlierDetection(client.OutlierDetection{
			ConsecutiveFailures: 3,
			EjectionTime:        10 * time.Second,
		}))
		if err != nil {
			t.Fatal(err)
		}
		defer resolver.Close()

		bad := instanceOnPort(t, resolver, 1)

		// Two failures, a success, two more: never three in a row.
		for _, failed := range []bool{true, true, false, true, true} {
			var err error
			if failed {
				err = errors.New("refused")
			}

			resolver.Report(bad, err)
		}

		if seen := pickedPorts(t, resolver, 9); seen[1] == 0 {
			t.Fatalf("expected a success to reset the count, got %v", seen)
		}

		resolver.Report(bad, errors.New("refused"))

		if seen := pickedPorts(t, resolver, 30); seen[1] != 0 || seen[2] == 0 || seen[3] == 0 {
			t.Fatalf("expected the instance ejected and the others picked, got %v", seen)
		}

		time.Sleep(10 * time.Second)

		if seen := pickedPorts(t, resolver, 9); seen[1] == 0 {
			t.Fatalf("expected the instance back after its ejection, got %v", seen)
		}

		// A second ejection lasts twice as long.
		for range 3 {
			resolver.Report(bad, errors.New("refused"))
		}

		time.Sleep(15 * time.Second)

		if seen := pickedPorts(t, resolver, 9); seen[1] != 0 {
			t.Errorf("expected the second ejection to last 20s, got %v after 15s", seen)
		}

		time.Sleep(5 * time.Second)

		if seen := pickedPorts(t, resolver, 9); seen[1] == 0 {
			t.Errorf("expected the instance back after 20s, got %v", seen)
		}
	})
}

func TestEjectionsAreCapped(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		_, _, beacon := resolverFixture(t)
		registerPorts(t, beacon, 1, 2)

		resolver, err := beacon.NewResolver(t.Context(), "orders", client.WithOutlierDetection(client.OutlierDetection{ConsecutiveFailures: 1}))
		if err != nil {
			t.Fatal(err)
		}
		defer resolver.Close()

		// The caller's own network fails, so every instance looks bad: at
		// most half of them may be ejected.
		resolver.Report(instanceOnPort(t, resolver, 1), errors.New("unreachable"))
		resolver.Report(instanceOnPort(t, resolver, 2), errors.New("unreachable"))

		if seen := pickedPorts(t, resolver, 10); len(seen) != 1 {
			t.Errorf("expected exactly one of two instances ejected, got %v", seen)
		}
	})
}

func TestTheLastInstanceIsNeverEjected(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		_, _, beacon := resolverFixture(t)
		registerPorts(t, beacon, 1)

		resolver, err := beacon.NewResolver(t.Context(), "orders", client.WithOutlierDetection(client.OutlierDetection{
			ConsecutiveFailures: 1,
			MaxEjectedPercent:   100,
		}))
		if err != nil {
			t.Fatal(err)
		}
		defer resolver.Close()

		resolver.Report(instanceOnPort(t, resolver, 1), errors.New("refused"))

		if _, err := resolver.Pick(); err != nil {
			t.Errorf("expected the only instance to stay pickable, got %v", err)
		}
	})
}

func TestOneInstanceAlwaysStaysEvenAtAHundredPercent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		_, _, beacon := resolverFixture(t)
		registerPorts(t, beacon, 1, 2)

		resolver, err := beacon.NewResolver(t.Context(), "orders", client.WithOutlierDetection(client.OutlierDetection{
			ConsecutiveFailures: 1,
			MaxEjectedPercent:   100,
		}))
		if err != nil {
			t.Fatal(err)
		}
		defer resolver.Close()

		resolver.Report(instanceOnPort(t, resolver, 1), errors.New("refused"))
		resolver.Report(instanceOnPort(t, resolver, 2), errors.New("refused"))

		if seen := pickedPorts(t, resolver, 10); len(seen) != 1 || seen[2] != 10 {
			t.Errorf("expected only the second instance to stay, got %v", seen)
		}
	})
}
