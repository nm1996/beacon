// Package cache wraps a discovery.Registry so lookups reach it at most once
// per TTL for each name, which matters when the registry is a database
// shared by several nodes.
package cache

import (
	"context"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/nm1996/beacon/internal/discovery"
)

// Registry is a discovery.Registry that caches List results. Use New to
// create one. It is safe for concurrent use if the registry it wraps is.
//
// It caches every instance of a name, or of the whole registry, and applies
// After, Limit and HeartbeatSince to that in memory, so pages and their
// links stay exact. Creates and deletes through it clear the cache at once;
// changes through other nodes show within the TTL.
//
// Heartbeats do not clear it, or a busy registry would never hit. A cached
// LastHeartbeat may therefore be up to the TTL old, which is why the TTL
// must stay well below the time between heartbeats; config enforces at most
// a third of the service TTL.
type Registry struct {
	discovery.Registry

	ttl time.Duration

	mu      sync.Mutex
	entries map[string]entry
}

// entry is every instance of one name, sorted by ID, as fetched at
// fetchedAt.
type entry struct {
	instances []discovery.Instance
	fetchedAt time.Time
}

var _ discovery.Registry = (*Registry)(nil)

// New returns registry with its List results cached for ttl. A ttl of zero
// or less returns registry itself.
func New(registry discovery.Registry, ttl time.Duration) discovery.Registry {
	if ttl <= 0 {
		return registry
	}

	return &Registry{Registry: registry, ttl: ttl, entries: make(map[string]entry)}
}

// List answers from the cached instances of query.Name, fetching them if
// they are missing or older than the TTL.
func (registry *Registry) List(ctx context.Context, query discovery.ListQuery) ([]discovery.Instance, error) {
	all, err := registry.instances(ctx, query.Name)
	if err != nil {
		return nil, err
	}

	// Sorted by ID, so the page starts at the first ID after query.After.
	start := sort.Search(len(all), func(index int) bool { return all[index].ID > query.After })

	result := make([]discovery.Instance, 0, min(len(all)-start, max(query.Limit, 0)))

	for _, instance := range all[start:] {
		if query.Limit > 0 && len(result) == query.Limit {
			break
		}

		if !instance.LastHeartbeat.Before(query.HeartbeatSince) {
			result = append(result, instance)
		}
	}

	return result, nil
}

// instances returns every instance of name, from the cache if it is
// fresh. Callers must not change the slice.
func (registry *Registry) instances(ctx context.Context, name string) ([]discovery.Instance, error) {
	now := time.Now()

	registry.mu.Lock()
	cached, ok := registry.entries[name]
	registry.mu.Unlock()

	if ok && now.Sub(cached.fetchedAt) < registry.ttl {
		return cached.instances, nil
	}

	fetched, err := registry.Registry.List(ctx, discovery.ListQuery{Name: name})
	if err != nil {
		return nil, err
	}

	fetched = slices.Clip(fetched)

	registry.mu.Lock()
	registry.entries[name] = entry{instances: fetched, fetchedAt: now}
	registry.mu.Unlock()

	return fetched, nil
}

// clear forgets every cached lookup.
func (registry *Registry) clear() {
	registry.mu.Lock()
	clear(registry.entries)
	registry.mu.Unlock()
}

// Create stores instance and clears the cache.
func (registry *Registry) Create(ctx context.Context, instance discovery.Instance, limits discovery.Limits) error {
	err := registry.Registry.Create(ctx, instance, limits)
	if err == nil {
		registry.clear()
	}

	return err
}

// Delete removes the instance and clears the cache.
func (registry *Registry) Delete(ctx context.Context, id string, check discovery.OwnerCheck) error {
	err := registry.Registry.Delete(ctx, id, check)
	if err == nil {
		registry.clear()
	}

	return err
}

// DeleteStale removes stale instances and clears the cache if it removed
// any.
func (registry *Registry) DeleteStale(ctx context.Context, cutoff time.Time) (int, error) {
	removed, err := registry.Registry.DeleteStale(ctx, cutoff)
	if removed > 0 {
		registry.clear()
	}

	return removed, err
}
