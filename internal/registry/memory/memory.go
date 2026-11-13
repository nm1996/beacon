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
}

var _ discovery.Registry = (*MemoryRegistry)(nil)

// New returns an empty registry.
func New() *MemoryRegistry {
	return &MemoryRegistry{
		instances: make(map[string]discovery.Instance),
		owned:     make(map[string]int),
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

	if instance.Owner != "" {
		registry.owned[instance.Owner]++
	}

	return nil
}

// remove deletes the instance with id and its share of the owner counts.
// The caller holds mu for writing.
func (registry *MemoryRegistry) remove(id string, instance discovery.Instance) {
	delete(registry.instances, id)

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

// List returns copies of the instances query selects, sorted by ID. It
// scans and sorts every match on each call, which is fine for the sizes
// registry.max_instances allows; a backend with an index can do better.
func (registry *MemoryRegistry) List(_ context.Context, query discovery.ListQuery) ([]discovery.Instance, error) {
	registry.mu.RLock()
	result := make([]discovery.Instance, 0, len(registry.instances))

	for _, instance := range registry.instances {
		if (query.Name == "" || instance.Name == query.Name) && instance.ID > query.After {
			result = append(result, instance)
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
