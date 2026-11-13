// Package memory is the in-memory discovery.Registry: a map guarded by one
// sync.RWMutex. It keeps nothing across restarts, which Beacon's clients
// handle by registering again when a heartbeat gets 404.
package memory

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nm1996/beacon/internal/discovery"
)

// MemoryRegistry stores instances in memory. Use New to create one; the
// zero value is not ready to use. It is safe for concurrent use.
//
// The name repeats the package on purpose: next to other backends it reads
// as what it is wherever the type is mentioned alone.
type MemoryRegistry struct { //nolint:revive // stutter accepted for clarity, see above
	mu        sync.RWMutex
	instances map[string]discovery.Instance
	// owned counts the instances of each non-empty owner, so Create checks
	// the per-owner limit without a scan. Every insert and removal updates
	// it under mu.
	owned map[string]int
	// byName holds the IDs of each name's instances, so a lookup by name
	// visits only those rather than the whole registry, and holds the lock
	// that heartbeats wait for only that long. Every insert and removal
	// updates it under mu.
	byName map[string]map[string]struct{}
}

var _ discovery.Registry = (*MemoryRegistry)(nil)

// New returns an empty registry.
func New() *MemoryRegistry {
	return &MemoryRegistry{
		instances: make(map[string]discovery.Instance),
		owned:     make(map[string]int),
		byName:    make(map[string]map[string]struct{}),
	}
}

// Create stores instance under its ID unless that would break limits, and
// fails if the ID is taken. The counts are checked under the same lock as
// the insert.
func (registry *MemoryRegistry) Create(_ context.Context, instance discovery.Instance, limits discovery.Limits) error {
	registry.mu.Lock()
	defer registry.mu.Unlock()

	if _, exists := registry.instances[instance.ID]; exists {
		return fmt.Errorf("instance %s already exists", instance.ID)
	}

	if len(registry.instances) >= limits.Total {
		return discovery.ErrRegistryFull
	}

	if instance.Owner != "" && limits.PerOwner > 0 && registry.owned[instance.Owner] >= limits.PerOwner {
		return discovery.ErrOwnerQuotaExceeded
	}

	registry.instances[instance.ID] = instance

	ids, named := registry.byName[instance.Name]
	if !named {
		ids = make(map[string]struct{})
		registry.byName[instance.Name] = ids
	}

	ids[instance.ID] = struct{}{}

	if instance.Owner != "" {
		registry.owned[instance.Owner]++
	}

	return nil
}

// remove deletes the instance with id, its entry in the name index and its
// share of the owner counts. The caller holds mu for writing.
func (registry *MemoryRegistry) remove(id string, instance discovery.Instance) {
	delete(registry.instances, id)

	if ids := registry.byName[instance.Name]; len(ids) <= 1 {
		delete(registry.byName, instance.Name)
	} else {
		delete(ids, id)
	}

	if instance.Owner == "" {
		return
	}

	if registry.owned[instance.Owner] <= 1 {
		delete(registry.owned, instance.Owner)
		return
	}

	registry.owned[instance.Owner]--
}

// Touch sets the LastHeartbeat of the instance with id, if check allows it.
// The owner is checked under the same lock as the change.
func (registry *MemoryRegistry) Touch(_ context.Context, id string, at time.Time, check discovery.OwnerCheck) error {
	registry.mu.Lock()
	defer registry.mu.Unlock()

	instance, exists := registry.instances[id]
	if !exists {
		return discovery.ErrInstanceNotFound
	}

	if !check.Allows(instance.Owner) {
		return discovery.ErrInstanceNotOwned
	}

	instance.LastHeartbeat = at
	registry.instances[id] = instance

	return nil
}

// List returns copies of the instances query selects, sorted by ID. A
// lookup by name visits only that name's instances; without a name it scans
// every instance. Either way it copies and sorts every match before
// applying Limit, so a page costs as much as all its matches.
func (registry *MemoryRegistry) List(_ context.Context, query discovery.ListQuery) ([]discovery.Instance, error) {
	registry.mu.RLock()

	var result []discovery.Instance

	if query.Name != "" {
		ids := registry.byName[query.Name]
		result = make([]discovery.Instance, 0, len(ids))

		for id := range ids {
			if instance := registry.instances[id]; id > query.After && !instance.LastHeartbeat.Before(query.HeartbeatSince) {
				result = append(result, instance)
			}
		}
	} else {
		// Room for every instance only when every instance may match.
		if query.After == "" {
			result = make([]discovery.Instance, 0, len(registry.instances))
		}

		for _, instance := range registry.instances {
			if instance.ID > query.After && !instance.LastHeartbeat.Before(query.HeartbeatSince) {
				result = append(result, instance)
			}
		}
	}

	registry.mu.RUnlock()

	slices.SortFunc(result, func(left, right discovery.Instance) int {
		return strings.Compare(left.ID, right.ID)
	})

	if query.Limit > 0 && len(result) > query.Limit {
		result = result[:query.Limit]
	}

	return result, nil
}

// Get returns a copy of the instance with id.
func (registry *MemoryRegistry) Get(_ context.Context, id string) (discovery.Instance, error) {
	registry.mu.RLock()
	defer registry.mu.RUnlock()

	instance, exists := registry.instances[id]
	if !exists {
		return discovery.Instance{}, discovery.ErrInstanceNotFound
	}

	return instance, nil
}

// Delete removes the instance with id, if check allows it. The owner is
// checked under the same lock as the removal.
func (registry *MemoryRegistry) Delete(_ context.Context, id string, check discovery.OwnerCheck) error {
	registry.mu.Lock()
	defer registry.mu.Unlock()

	instance, exists := registry.instances[id]
	if !exists {
		return discovery.ErrInstanceNotFound
	}

	if !check.Allows(instance.Owner) {
		return discovery.ErrInstanceNotOwned
	}

	registry.remove(id, instance)

	return nil
}

// DeleteStale removes every instance whose last heartbeat is before cutoff.
func (registry *MemoryRegistry) DeleteStale(_ context.Context, cutoff time.Time) (int, error) {
	registry.mu.Lock()
	defer registry.mu.Unlock()

	removed := 0

	for id, instance := range registry.instances {
		if instance.LastHeartbeat.Before(cutoff) {
			registry.remove(id, instance)
			removed++
		}
	}

	return removed, nil
}
