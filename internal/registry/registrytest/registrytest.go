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
	"math"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nm1996/beacon/internal/discovery"
)

// anyOwner lets a Touch or Delete change any instance.
var anyOwner = discovery.OwnerCheck{Any: true}

// noLimit is a Create limit no case reaches.
var noLimit = discovery.Limits{Total: math.MaxInt}

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
		{name: "ListByNameFollowsRemovals", run: listByNameFollowsRemovals},
		{name: "ListOnEmptyRegistry", run: listOnEmptyRegistry},
		{name: "ListReturnsCopies", run: listReturnsCopies},
		{name: "GetReturnsStoredInstance", run: getReturnsStoredInstance},
		{name: "GetUnknownID", run: getUnknownID},
		{name: "GetReturnsCopy", run: getReturnsCopy},
		{name: "TouchUpdatesLastHeartbeat", run: touchUpdatesLastHeartbeat},
		{name: "TouchUnknownID", run: touchUnknownID},
		{name: "DeleteRemovesInstance", run: deleteRemovesInstance},
		{name: "DeleteUnknownID", run: deleteUnknownID},
		{name: "DeleteStaleUsesCutoff", run: deleteStaleUsesCutoff},
		{name: "ConcurrentUse", run: concurrentUse},
		{name: "CreateRespectsLimit", run: createRespectsLimit},
		{name: "TouchChecksOwner", run: touchChecksOwner},
		{name: "ListIsSortedByID", run: listIsSortedByID},
		{name: "ListPagesWithAfterAndLimit", run: listPagesWithAfterAndLimit},
		{name: "ListPagesWithinAName", run: listPagesWithinAName},
		{name: "DeleteChecksOwner", run: deleteChecksOwner},
		{name: "ConcurrentCreateNeverExceedsLimit", run: concurrentCreateNeverExceedsLimit},
		{name: "CreateRespectsOwnerLimit", run: createRespectsOwnerLimit},
		{name: "OwnerLimitSkipsUnownedInstances", run: ownerLimitSkipsUnownedInstances},
		{name: "RemovalFreesOwnerPlaces", run: removalFreesOwnerPlaces},
		{name: "TotalLimitComesFirst", run: totalLimitComesFirst},
		{name: "ConcurrentCreateNeverExceedsOwnerLimit", run: concurrentCreateNeverExceedsOwnerLimit},
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
		if err := registry.Create(test.Context(), item, noLimit); err != nil {
			test.Fatalf("create %s: %v", item.ID, err)
		}
	}
}

// mustList lists instances named name and fails the test on error.
func mustList(test *testing.T, registry discovery.Registry, name string) []discovery.Instance {
	test.Helper()

	instances, err := registry.List(test.Context(), discovery.ListQuery{Name: name})
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

	if err := registry.Create(test.Context(), instance("a", "payments", base), noLimit); err == nil {
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

func listByNameFollowsRemovals(test *testing.T, registry discovery.Registry) {
	mustCreate(test, registry,
		instance("a", "orders", base),
		instance("b", "orders", base.Add(-time.Hour)),
		instance("c", "payments", base),
	)

	if err := registry.Delete(test.Context(), "a", anyOwner); err != nil {
		test.Fatalf("delete: %v", err)
	}

	if removed, err := registry.DeleteStale(test.Context(), base); err != nil || removed != 1 {
		test.Fatalf("expected 1 stale instance removed, got %d, %v", removed, err)
	}

	if got := mustList(test, registry, "orders"); len(got) != 0 {
		test.Fatalf("expected no orders once both were removed, got %v", ids(got))
	}

	// The name works again once something registers under it.
	mustCreate(test, registry, instance("d", "orders", base))

	if got := mustList(test, registry, "orders"); !sameIDs(got, "d") {
		test.Errorf("expected only the new orders instance, got %v", ids(got))
	}

	if got := mustList(test, registry, "payments"); !sameIDs(got, "c") {
		test.Errorf("expected payments untouched, got %v", ids(got))
	}

	if got := page(test, registry, discovery.ListQuery{Name: "orders", After: "d"}); len(got) != 0 {
		test.Errorf("expected nothing after the last orders ID, got %v", ids(got))
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
	if err := registry.Touch(test.Context(), "a", later, anyOwner); err != nil {
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

func getReturnsStoredInstance(test *testing.T, registry discovery.Registry) {
	stored := instance("a", "orders", base)
	stored.Owner = "orders-token"
	mustCreate(test, registry, stored, instance("b", "payments", base))

	got, err := registry.Get(test.Context(), "a")
	if err != nil {
		test.Fatalf("get: %v", err)
	}

	if got != stored {
		test.Errorf("expected every field, owner included, to round-trip:\nwant %+v\ngot  %+v", stored, got)
	}
}

func getUnknownID(test *testing.T, registry discovery.Registry) {
	_, err := registry.Get(test.Context(), "missing")

	if !errors.Is(err, discovery.ErrInstanceNotFound) {
		test.Errorf("expected ErrInstanceNotFound, got %v", err)
	}
}

func getReturnsCopy(test *testing.T, registry discovery.Registry) {
	mustCreate(test, registry, instance("a", "orders", base))

	got, err := registry.Get(test.Context(), "a")
	if err != nil {
		test.Fatalf("get: %v", err)
	}

	got.Owner = "changed"
	got.Port = 1

	again, err := registry.Get(test.Context(), "a")
	if err != nil {
		test.Fatalf("get again: %v", err)
	}

	if again.Owner != "" || again.Port != 8081 {
		test.Errorf("expected changing a returned instance not to change the stored one, got %+v", again)
	}
}

func touchUnknownID(test *testing.T, registry discovery.Registry) {
	err := registry.Touch(test.Context(), "missing", base, anyOwner)

	if !errors.Is(err, discovery.ErrInstanceNotFound) {
		test.Errorf("expected ErrInstanceNotFound, got %v", err)
	}
}

func deleteRemovesInstance(test *testing.T, registry discovery.Registry) {
	mustCreate(test, registry, instance("a", "orders", base), instance("b", "orders", base))

	if err := registry.Delete(test.Context(), "a", anyOwner); err != nil {
		test.Fatalf("delete: %v", err)
	}

	if got := mustList(test, registry, ""); !sameIDs(got, "b") {
		test.Errorf("expected only b to remain, got %v", ids(got))
	}

	if err := registry.Delete(test.Context(), "a", anyOwner); !errors.Is(err, discovery.ErrInstanceNotFound) {
		test.Errorf("expected deleting twice to give ErrInstanceNotFound, got %v", err)
	}
}

func deleteUnknownID(test *testing.T, registry discovery.Registry) {
	err := registry.Delete(test.Context(), "missing", anyOwner)

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
				if err := registry.Create(ctx, instance(id, fmt.Sprintf("service-%d", worker), base), noLimit); err != nil {
					test.Errorf("create %s: %v", id, err)
					return
				}

				if err := registry.Touch(ctx, id, base.Add(time.Second), anyOwner); err != nil {
					test.Errorf("touch %s: %v", id, err)
				}

				if err := registry.Create(ctx, instance(id+"-stale", "stale", stale), noLimit); err != nil {
					test.Errorf("create %s-stale: %v", id, err)
				}

				if _, err := registry.DeleteStale(ctx, base); err != nil {
					test.Errorf("delete stale: %v", err)
				}

				if _, err := registry.List(ctx, discovery.ListQuery{}); err != nil {
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

func createRespectsLimit(test *testing.T, registry discovery.Registry) {
	ctx := test.Context()

	for _, id := range []string{"a", "b"} {
		if err := registry.Create(ctx, instance(id, "orders", base), discovery.Limits{Total: 2}); err != nil {
			test.Fatalf("create %s within the limit: %v", id, err)
		}
	}

	if err := registry.Create(ctx, instance("c", "orders", base), discovery.Limits{Total: 2}); !errors.Is(err, discovery.ErrRegistryFull) {
		test.Fatalf("expected ErrRegistryFull at the limit, got %v", err)
	}

	if got := mustList(test, registry, ""); !sameIDs(got, "a", "b") {
		test.Errorf("expected the refused instance not to be stored, got %v", ids(got))
	}

	// Leaving frees a place.
	if err := registry.Delete(ctx, "a", anyOwner); err != nil {
		test.Fatalf("delete: %v", err)
	}

	if err := registry.Create(ctx, instance("c", "orders", base), discovery.Limits{Total: 2}); err != nil {
		test.Errorf("expected a place after one instance left, got %v", err)
	}
}

// concurrentCreateNeverExceedsLimit races more creates than the limit
// allows. Exactly limit of them may succeed; the rest must get
// ErrRegistryFull, never a silent overshoot.
func concurrentCreateNeverExceedsLimit(test *testing.T, registry discovery.Registry) {
	const (
		limit     = 50
		workers   = 16
		perWorker = 10
	)

	var (
		group   sync.WaitGroup
		created atomic.Int64
		refused atomic.Int64
		start   = make(chan struct{})
		ctx     = context.Background()
	)

	for worker := range workers {
		group.Go(func() {
			<-start

			for op := range perWorker {
				err := registry.Create(ctx, instance(fmt.Sprintf("w%d-%d", worker, op), "orders", base), discovery.Limits{Total: limit})

				switch {
				case err == nil:
					created.Add(1)
				case errors.Is(err, discovery.ErrRegistryFull):
					refused.Add(1)
				default:
					test.Errorf("create: %v", err)
				}
			}
		})
	}

	close(start)
	group.Wait()

	if created.Load() != limit || refused.Load() != workers*perWorker-limit {
		test.Errorf("expected %d created and %d refused, got %d and %d", limit, workers*perWorker-limit, created.Load(), refused.Load())
	}

	if got := len(mustList(test, registry, "")); got != limit {
		test.Errorf("expected exactly %d stored instances, got %d", limit, got)
	}
}

// owned returns instance id, named "orders", registered by owner.
func owned(id, owner string) discovery.Instance {
	item := instance(id, "orders", base)
	item.Owner = owner

	return item
}

func touchChecksOwner(test *testing.T, registry discovery.Registry) {
	ctx := test.Context()
	mustCreate(test, registry, owned("alice-1", "alice"), owned("unowned", ""))

	later := base.Add(time.Minute)
	alice := discovery.OwnerCheck{Owner: "alice"}
	bob := discovery.OwnerCheck{Owner: "bob"}

	if err := registry.Touch(ctx, "alice-1", later, bob); !errors.Is(err, discovery.ErrInstanceNotOwned) {
		test.Fatalf("expected ErrInstanceNotOwned for another caller, got %v", err)
	}

	if got, err := registry.Get(ctx, "alice-1"); err != nil || !got.LastHeartbeat.Equal(base) {
		test.Errorf("a refused touch must not change the instance, got %+v, %v", got, err)
	}

	for name, check := range map[string]discovery.OwnerCheck{"owner": alice, "any": anyOwner} {
		if err := registry.Touch(ctx, "alice-1", later, check); err != nil {
			test.Errorf("%s: expected the touch to be allowed, got %v", name, err)
		}
	}

	if err := registry.Touch(ctx, "unowned", later, bob); err != nil {
		test.Errorf("expected anyone to touch an instance without an owner, got %v", err)
	}

	// An unknown ID is not found, whatever the check says.
	if err := registry.Touch(ctx, "missing", later, bob); !errors.Is(err, discovery.ErrInstanceNotFound) {
		test.Errorf("expected ErrInstanceNotFound for an unknown ID, got %v", err)
	}
}

func deleteChecksOwner(test *testing.T, registry discovery.Registry) {
	ctx := test.Context()
	mustCreate(test, registry, owned("alice-1", "alice"), owned("alice-2", "alice"), owned("unowned", ""))

	bob := discovery.OwnerCheck{Owner: "bob"}

	if err := registry.Delete(ctx, "alice-1", bob); !errors.Is(err, discovery.ErrInstanceNotOwned) {
		test.Fatalf("expected ErrInstanceNotOwned for another caller, got %v", err)
	}

	if got := mustList(test, registry, ""); !sameIDs(got, "alice-1", "alice-2", "unowned") {
		test.Errorf("a refused delete must not remove anything, got %v", ids(got))
	}

	if err := registry.Delete(ctx, "alice-1", discovery.OwnerCheck{Owner: "alice"}); err != nil {
		test.Errorf("expected the owner to delete, got %v", err)
	}

	if err := registry.Delete(ctx, "alice-2", anyOwner); err != nil {
		test.Errorf("expected any-owner to delete, got %v", err)
	}

	if err := registry.Delete(ctx, "unowned", bob); err != nil {
		test.Errorf("expected anyone to delete an instance without an owner, got %v", err)
	}

	if err := registry.Delete(ctx, "missing", bob); !errors.Is(err, discovery.ErrInstanceNotFound) {
		test.Errorf("expected ErrInstanceNotFound for an unknown ID, got %v", err)
	}
}

// page lists with query and fails the test on error.
func page(test *testing.T, registry discovery.Registry, query discovery.ListQuery) []discovery.Instance {
	test.Helper()

	instances, err := registry.List(test.Context(), query)
	if err != nil {
		test.Fatalf("list %+v: %v", query, err)
	}

	return instances
}

// orderedIDs returns the IDs of instances in the order they came.
func orderedIDs(instances []discovery.Instance) string {
	result := make([]string, 0, len(instances))
	for _, item := range instances {
		result = append(result, item.ID)
	}

	return fmt.Sprint(result)
}

func listIsSortedByID(test *testing.T, registry discovery.Registry) {
	mustCreate(test, registry,
		instance("c", "orders", base),
		instance("a", "orders", base),
		instance("d", "payments", base),
		instance("b", "orders", base),
	)

	if got := orderedIDs(page(test, registry, discovery.ListQuery{})); got != "[a b c d]" {
		test.Errorf("expected every instance sorted by ID, got %s", got)
	}

	if got := orderedIDs(page(test, registry, discovery.ListQuery{Name: "orders"})); got != "[a b c]" {
		test.Errorf("expected orders sorted by ID, got %s", got)
	}
}

func listPagesWithAfterAndLimit(test *testing.T, registry discovery.Registry) {
	for _, id := range []string{"e", "a", "d", "b", "c"} {
		mustCreate(test, registry, instance(id, "orders", base))
	}

	// Walk the pages the way a client following cursors does.
	var (
		after string
		pages []string
	)

	for range 10 {
		items := page(test, registry, discovery.ListQuery{After: after, Limit: 2})
		if len(items) == 0 {
			break
		}

		pages = append(pages, orderedIDs(items))
		after = items[len(items)-1].ID
	}

	if got := fmt.Sprint(pages); got != "[[a b] [c d] [e]]" {
		test.Errorf("expected pages [a b] [c d] [e], got %s", got)
	}

	if got := orderedIDs(page(test, registry, discovery.ListQuery{After: "e", Limit: 2})); got != "[]" {
		test.Errorf("expected nothing after the last ID, got %s", got)
	}

	// After need not be a stored ID, so a page still continues correctly
	// when the instance it ended on has been removed meanwhile.
	if got := orderedIDs(page(test, registry, discovery.ListQuery{After: "bb", Limit: 2})); got != "[c d]" {
		test.Errorf("expected the page after a removed ID to continue at c, got %s", got)
	}

	if got := orderedIDs(page(test, registry, discovery.ListQuery{Limit: 0})); got != "[a b c d e]" {
		test.Errorf("expected Limit 0 to mean no limit, got %s", got)
	}
}

func listPagesWithinAName(test *testing.T, registry discovery.Registry) {
	mustCreate(test, registry,
		instance("a", "orders", base),
		instance("b", "payments", base),
		instance("c", "orders", base),
		instance("d", "payments", base),
		instance("e", "orders", base),
	)

	first := page(test, registry, discovery.ListQuery{Name: "orders", Limit: 2})
	if got := orderedIDs(first); got != "[a c]" {
		test.Fatalf("expected the first orders page [a c], got %s", got)
	}

	second := page(test, registry, discovery.ListQuery{Name: "orders", After: first[len(first)-1].ID, Limit: 2})
	if got := orderedIDs(second); got != "[e]" {
		test.Errorf("expected the second orders page [e], got %s", got)
	}
}

// ownerLimit allows two instances per owner and plenty in total.
var ownerLimit = discovery.Limits{Total: 100, PerOwner: 2}

func createRespectsOwnerLimit(test *testing.T, registry discovery.Registry) {
	ctx := test.Context()

	for _, id := range []string{"a1", "a2"} {
		if err := registry.Create(ctx, owned(id, "alice"), ownerLimit); err != nil {
			test.Fatalf("create %s within the owner limit: %v", id, err)
		}
	}

	if err := registry.Create(ctx, owned("a3", "alice"), ownerLimit); !errors.Is(err, discovery.ErrOwnerQuotaExceeded) {
		test.Fatalf("expected ErrOwnerQuotaExceeded at the owner limit, got %v", err)
	}

	// Another owner still has its own places.
	for _, id := range []string{"b1", "b2"} {
		if err := registry.Create(ctx, owned(id, "bob"), ownerLimit); err != nil {
			test.Errorf("expected bob to register %s while alice is at her limit, got %v", id, err)
		}
	}

	if got := mustList(test, registry, ""); !sameIDs(got, "a1", "a2", "b1", "b2") {
		test.Errorf("expected the refused instance not to be stored, got %v", ids(got))
	}
}

func ownerLimitSkipsUnownedInstances(test *testing.T, registry discovery.Registry) {
	ctx := test.Context()

	// Without authentication every instance has an empty owner; the owner
	// limit must not turn them into one owner sharing two places.
	for index := range 5 {
		if err := registry.Create(ctx, instance(fmt.Sprintf("u%d", index), "orders", base), ownerLimit); err != nil {
			test.Fatalf("create unowned instance %d: %v", index, err)
		}
	}

	// Nor may they use up an owner's places.
	for _, id := range []string{"a1", "a2"} {
		if err := registry.Create(ctx, owned(id, "alice"), ownerLimit); err != nil {
			test.Errorf("create %s: %v", id, err)
		}
	}
}

func removalFreesOwnerPlaces(test *testing.T, registry discovery.Registry) {
	ctx := test.Context()

	fresh := owned("fresh", "alice")
	stale := owned("stale", "alice")
	stale.LastHeartbeat = base.Add(-time.Hour)

	for _, item := range []discovery.Instance{fresh, stale} {
		if err := registry.Create(ctx, item, ownerLimit); err != nil {
			test.Fatalf("create %s: %v", item.ID, err)
		}
	}

	// Expiry frees a place.
	if removed, err := registry.DeleteStale(ctx, base.Add(-time.Minute)); err != nil || removed != 1 {
		test.Fatalf("expected 1 stale instance removed, got %d, %v", removed, err)
	}

	if err := registry.Create(ctx, owned("after-expiry", "alice"), ownerLimit); err != nil {
		test.Fatalf("expected a place after an instance expired, got %v", err)
	}

	// So does unregistering.
	if err := registry.Delete(ctx, "fresh", anyOwner); err != nil {
		test.Fatalf("delete: %v", err)
	}

	if err := registry.Create(ctx, owned("after-delete", "alice"), ownerLimit); err != nil {
		test.Fatalf("expected a place after an instance was deleted, got %v", err)
	}

	if err := registry.Create(ctx, owned("over", "alice"), ownerLimit); !errors.Is(err, discovery.ErrOwnerQuotaExceeded) {
		test.Errorf("expected the limit to hold again, got %v", err)
	}
}

func totalLimitComesFirst(test *testing.T, registry discovery.Registry) {
	limits := discovery.Limits{Total: 1, PerOwner: 1}

	if err := registry.Create(test.Context(), owned("a", "alice"), limits); err != nil {
		test.Fatalf("create: %v", err)
	}

	// Both limits are reached; the registry being full is the reason that
	// does not depend on who asks.
	if err := registry.Create(test.Context(), owned("b", "alice"), limits); !errors.Is(err, discovery.ErrRegistryFull) {
		test.Errorf("expected ErrRegistryFull when both limits are reached, got %v", err)
	}
}

// concurrentCreateNeverExceedsOwnerLimit races two owners, each sending more
// creates than its limit allows. Each must end with exactly its limit.
func concurrentCreateNeverExceedsOwnerLimit(test *testing.T, registry discovery.Registry) {
	const (
		perOwner  = 20
		workers   = 8
		perWorker = 10
	)

	limits := discovery.Limits{Total: math.MaxInt, PerOwner: perOwner}
	owners := []string{"alice", "bob"}

	var (
		group sync.WaitGroup
		start = make(chan struct{})
		ctx   = context.Background()
	)

	for worker := range workers {
		owner := owners[worker%len(owners)]

		group.Go(func() {
			<-start

			for op := range perWorker {
				err := registry.Create(ctx, owned(fmt.Sprintf("%s-%d-%d", owner, worker, op), owner), limits)
				if err != nil && !errors.Is(err, discovery.ErrOwnerQuotaExceeded) {
					test.Errorf("create: %v", err)
				}
			}
		})
	}

	close(start)
	group.Wait()

	counts := make(map[string]int)
	for _, item := range mustList(test, registry, "") {
		counts[item.Owner]++
	}

	for _, owner := range owners {
		if counts[owner] != perOwner {
			test.Errorf("expected %s to hold exactly %d instances, got %d", owner, perOwner, counts[owner])
		}
	}
}
