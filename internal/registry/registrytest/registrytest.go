// Package registrytest is the contract every discovery.Registry backend must
// satisfy. A backend's tests call Run with a constructor; passing Run means
// the backend can replace any other without discovery.Service noticing.
//
// Run the suite with -race: the concurrency cases rely on the race detector.
package registrytest

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/nm1996/beacon/internal/discovery"
)

// base is a fixed point in time the cases build their timestamps from.
var base = time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC)

// Run runs the contract against registries built by newRegistry. Each case
// gets a fresh, empty registry.
func Run(test *testing.T, newRegistry func(test *testing.T) discovery.Registry) {
	cases := []struct {
		name string
		run  func(test *testing.T, registry discovery.Registry)
	}{
		{name: "CreateThenList", run: createThenList},
		{name: "CreateRejectsDuplicateID", run: createRejectsDuplicateID},
		{name: "ListFiltersByName", run: listFiltersByName},
		{name: "ListOnEmptyRegistry", run: listOnEmptyRegistry},
		{name: "ListReturnsCopies", run: listReturnsCopies},
		{name: "TouchUpdatesLastHeartbeat", run: touchUpdatesLastHeartbeat},
		{name: "TouchUnknownID", run: touchUnknownID},
		{name: "DeleteRemovesInstance", run: deleteRemovesInstance},
		{name: "DeleteUnknownID", run: deleteUnknownID},
		{name: "DeleteStaleUsesCutoff", run: deleteStaleUsesCutoff},
		{name: "ConcurrentUse", run: concurrentUse},
	}

	for _, tc := range cases {
		test.Run(tc.name, func(test *testing.T) {
			tc.run(test, newRegistry(test))
		})
	}
}

// instance returns a valid instance with the given ID and name whose last
// heartbeat is at.
func instance(id, name string, at time.Time) discovery.Instance {
	return discovery.Instance{
		ID:            id,
		Name:          name,
		Address:       "10.0.0.1",
		Port:          8081,
		RegisteredAt:  at,
		LastHeartbeat: at,
	}
}

// mustCreate stores each instance and fails the test if any is rejected.
func mustCreate(test *testing.T, registry discovery.Registry, instances ...discovery.Instance) {
	test.Helper()

	for _, item := range instances {
		if err := registry.Create(test.Context(), item); err != nil {
			test.Fatalf("create %s: %v", item.ID, err)
		}
	}
}

// mustList lists instances named name and fails the test on error.
func mustList(test *testing.T, registry discovery.Registry, name string) []discovery.Instance {
	test.Helper()

	instances, err := registry.List(test.Context(), name)
	if err != nil {
		test.Fatalf("list %q: %v", name, err)
	}

	return instances
}

// ids returns the sorted IDs of instances.
func ids(instances []discovery.Instance) []string {
	result := make([]string, 0, len(instances))
	for _, item := range instances {
		result = append(result, item.ID)
	}

	sort.Strings(result)

	return result
}

// sameIDs reports whether got holds exactly the IDs in want.
func sameIDs(got []discovery.Instance, want ...string) bool {
	sort.Strings(want)

	return fmt.Sprint(ids(got)) == fmt.Sprint(want)
}

func createThenList(test *testing.T, registry discovery.Registry) {
	want := instance("a", "orders", base)
	mustCreate(test, registry, want)

	got := mustList(test, registry, "")
	if len(got) != 1 {
		test.Fatalf("expected 1 instance, got %d", len(got))
	}

	// Backends may store times at a different precision or zone, so compare
	// instants rather than the whole struct.
	stored := got[0]
	if stored.ID != want.ID || stored.Name != want.Name || stored.Address != want.Address || stored.Port != want.Port ||
		!stored.RegisteredAt.Equal(want.RegisteredAt) || !stored.LastHeartbeat.Equal(want.LastHeartbeat) {
		test.Errorf("expected %+v, got %+v", want, stored)
	}
}

func createRejectsDuplicateID(test *testing.T, registry discovery.Registry) {
	mustCreate(test, registry, instance("a", "orders", base))

	if err := registry.Create(test.Context(), instance("a", "payments", base)); err == nil {
		test.Fatal("expected creating a second instance with the same ID to fail")
	}

	if got := mustList(test, registry, "orders"); !sameIDs(got, "a") {
		test.Errorf("expected the original instance to be kept, got %+v", got)
	}
}

func listFiltersByName(test *testing.T, registry discovery.Registry) {
	mustCreate(test, registry,
		instance("a", "orders", base),
		instance("b", "orders", base),
		instance("c", "payments", base),
	)

	if got := mustList(test, registry, "orders"); !sameIDs(got, "a", "b") {
		test.Errorf("expected orders a and b, got %v", ids(got))
	}

	if got := mustList(test, registry, "payments"); !sameIDs(got, "c") {
		test.Errorf("expected payments c, got %v", ids(got))
	}

	if got := mustList(test, registry, "missing"); len(got) != 0 {
		test.Errorf("expected no instances for an unknown name, got %v", ids(got))
	}

	if got := mustList(test, registry, ""); !sameIDs(got, "a", "b", "c") {
		test.Errorf("expected every instance for an empty name, got %v", ids(got))
	}
}

func listOnEmptyRegistry(test *testing.T, registry discovery.Registry) {
	if got := mustList(test, registry, ""); len(got) != 0 {
		test.Errorf("expected an empty registry, got %v", ids(got))
	}
}

func listReturnsCopies(test *testing.T, registry discovery.Registry) {
	mustCreate(test, registry, instance("a", "orders", base))

	listed := mustList(test, registry, "orders")
	listed[0].Name = "changed"
	listed[0].Port = -1

	got := mustList(test, registry, "orders")
	if len(got) != 1 || got[0].Port != 8081 {
		test.Errorf("changing a listed instance changed the registry: %+v", got)
	}
}

func touchUpdatesLastHeartbeat(test *testing.T, registry discovery.Registry) {
	mustCreate(test, registry, instance("a", "orders", base))

	later := base.Add(time.Minute)
	if err := registry.Touch(test.Context(), "a", later); err != nil {
		test.Fatalf("touch: %v", err)
	}

	got := mustList(test, registry, "orders")
	if len(got) != 1 || !got[0].LastHeartbeat.Equal(later) {
		test.Fatalf("expected LastHeartbeat %v, got %+v", later, got)
	}

	if !got[0].RegisteredAt.Equal(base) {
		test.Errorf("expected RegisteredAt to stay %v, got %v", base, got[0].RegisteredAt)
	}
}

func touchUnknownID(test *testing.T, registry discovery.Registry) {
	err := registry.Touch(test.Context(), "missing", base)

	if !errors.Is(err, discovery.ErrInstanceNotFound) {
		test.Errorf("expected ErrInstanceNotFound, got %v", err)
	}
}

func deleteRemovesInstance(test *testing.T, registry discovery.Registry) {
	mustCreate(test, registry, instance("a", "orders", base), instance("b", "orders", base))

	if err := registry.Delete(test.Context(), "a"); err != nil {
		test.Fatalf("delete: %v", err)
	}

	if got := mustList(test, registry, ""); !sameIDs(got, "b") {
		test.Errorf("expected only b to remain, got %v", ids(got))
	}

	if err := registry.Delete(test.Context(), "a"); !errors.Is(err, discovery.ErrInstanceNotFound) {
		test.Errorf("expected deleting twice to give ErrInstanceNotFound, got %v", err)
	}
}

func deleteUnknownID(test *testing.T, registry discovery.Registry) {
	err := registry.Delete(test.Context(), "missing")

	if !errors.Is(err, discovery.ErrInstanceNotFound) {
		test.Errorf("expected ErrInstanceNotFound, got %v", err)
	}
}

func deleteStaleUsesCutoff(test *testing.T, registry discovery.Registry) {
	cutoff := base

	mustCreate(test, registry,
		instance("old", "orders", cutoff.Add(-time.Hour)),
		instance("just-before", "orders", cutoff.Add(-time.Millisecond)),
		instance("at-cutoff", "orders", cutoff),
		instance("after", "orders", cutoff.Add(time.Second)),
	)

	removed, err := registry.DeleteStale(test.Context(), cutoff)
	if err != nil {
		test.Fatalf("delete stale: %v", err)
	}

	if removed != 2 {
		test.Errorf("expected 2 instances removed, got %d", removed)
	}

	if got := mustList(test, registry, ""); !sameIDs(got, "at-cutoff", "after") {
		test.Errorf("expected at-cutoff and after to remain, got %v", ids(got))
	}

	if removed, err := registry.DeleteStale(test.Context(), cutoff); err != nil || removed != 0 {
		test.Errorf("expected a second sweep to remove nothing, got %d, %v", removed, err)
	}
}

// concurrentUse runs every operation from many goroutines at once, released
// together so they overlap, and then checks the registry is consistent.
func concurrentUse(test *testing.T, registry discovery.Registry) {
	const workers = 16
	const opsPerWorker = 100

	ctx := context.Background()
	stale := base.Add(-time.Hour)

	var group sync.WaitGroup
	start := make(chan struct{})

	for worker := range workers {
		group.Go(func() {
			<-start

			for op := range opsPerWorker {
				id := fmt.Sprintf("w%d-%d", worker, op)

				// Every worker leaves one fresh instance per op and creates
				// and deletes a stale one, while sweeps and reads run.
				if err := registry.Create(ctx, instance(id, fmt.Sprintf("service-%d", worker), base)); err != nil {
					test.Errorf("create %s: %v", id, err)
					return
				}

				if err := registry.Touch(ctx, id, base.Add(time.Second)); err != nil {
					test.Errorf("touch %s: %v", id, err)
				}

				if err := registry.Create(ctx, instance(id+"-stale", "stale", stale)); err != nil {
					test.Errorf("create %s-stale: %v", id, err)
				}

				if _, err := registry.DeleteStale(ctx, base); err != nil {
					test.Errorf("delete stale: %v", err)
				}

				if _, err := registry.List(ctx, ""); err != nil {
					test.Errorf("list: %v", err)
				}
			}
		})
	}

	close(start)
	group.Wait()

	if _, err := registry.DeleteStale(ctx, base); err != nil {
		test.Fatalf("final delete stale: %v", err)
	}

	if got := mustList(test, registry, ""); len(got) != workers*opsPerWorker {
		test.Errorf("expected %d fresh instances, got %d", workers*opsPerWorker, len(got))
	}

	if got := mustList(test, registry, "stale"); len(got) != 0 {
		test.Errorf("expected every stale instance to be swept, %d remain", len(got))
	}
}
