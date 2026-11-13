// Package memory is the in-memory discovery.Registry: a map guarded by one
// sync.RWMutex. It keeps nothing across restarts, which Beacon's clients
// handle by registering again when a heartbeat gets 404.
package memory

import (
	"context"
	"fmt"
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
}

var _ discovery.Registry = (*MemoryRegistry)(nil)

// New returns an empty registry.
func New() *MemoryRegistry {
	return &MemoryRegistry{instances: make(map[string]discovery.Instance)}
}

// Create stores instance under its ID unless limit instances are already
// stored, and fails if the ID is taken. The count is checked under the same
// lock as the insert.
func (registry *MemoryRegistry) Create(_ context.Context, instance discovery.Instance, limit int) error {
	registry.mu.Lock()
	defer registry.mu.Unlock()

	if _, exists := registry.instances[instance.ID]; exists {
		return fmt.Errorf("instance %s already exists", instance.ID)
	}

	if len(registry.instances) >= limit {
		return discovery.ErrRegistryFull
	}

	registry.instances[instance.ID] = instance

	return nil
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

// List returns copies of the instances named name, or of all instances when
// name is empty.
func (registry *MemoryRegistry) List(_ context.Context, name string) ([]discovery.Instance, error) {
	registry.mu.RLock()
	defer registry.mu.RUnlock()

	result := make([]discovery.Instance, 0, len(registry.instances))

	for _, instance := range registry.instances {
		if name == "" || instance.Name == name {
			result = append(result, instance)
		}
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

	delete(registry.instances, id)

	return nil
}

// DeleteStale removes every instance whose last heartbeat is before cutoff.
func (registry *MemoryRegistry) DeleteStale(_ context.Context, cutoff time.Time) (int, error) {
	registry.mu.Lock()
	defer registry.mu.Unlock()

	removed := 0

	for id, instance := range registry.instances {
		if instance.LastHeartbeat.Before(cutoff) {
			delete(registry.instances, id)
			removed++
		}
	}

	return removed, nil
}
