package metrics

import (
	"context"
	"errors"
	"time"

	"github.com/nm1996/beacon/internal/discovery"
)

// InstrumentRegistry wraps registry so every call is counted and timed, and
// instances removed by DeleteStale are counted as expired. It works the same
// for every backend, because it only sees the discovery.Registry interface.
func (metrics *Metrics) InstrumentRegistry(registry discovery.Registry) discovery.Registry {
	return &instrumentedRegistry{next: registry, metrics: metrics}
}

// instrumentedRegistry is a discovery.Registry that records metrics around
// another one.
type instrumentedRegistry struct {
	next    discovery.Registry
	metrics *Metrics
}

// observe records one call of operation that started at started and ended
// with err.
func (registry *instrumentedRegistry) observe(operation string, started time.Time, err error) {
	result := "ok"

	switch {
	case errors.Is(err, discovery.ErrInstanceNotFound):
		result = "not_found"
	case errors.Is(err, discovery.ErrRegistryFull):
		result = "full"
	case errors.Is(err, discovery.ErrInstanceNotOwned):
		result = "not_owned"
	case err != nil:
		result = "error"
	}

	registry.metrics.registryOperations.WithLabelValues(operation, result).Inc()
	registry.metrics.registryDuration.WithLabelValues(operation).Observe(time.Since(started).Seconds())
}

// Create records and forwards the call.
func (registry *instrumentedRegistry) Create(ctx context.Context, instance discovery.Instance, limit int) error {
	started := time.Now()
	err := registry.next.Create(ctx, instance, limit)
	registry.observe("create", started, err)

	return err
}

// Get records and forwards the call.
func (registry *instrumentedRegistry) Get(ctx context.Context, id string) (discovery.Instance, error) {
	started := time.Now()
	instance, err := registry.next.Get(ctx, id)
	registry.observe("get", started, err)

	return instance, err
}

// Touch records and forwards the call.
func (registry *instrumentedRegistry) Touch(ctx context.Context, id string, at time.Time, check discovery.OwnerCheck) error {
	started := time.Now()
	err := registry.next.Touch(ctx, id, at, check)
	registry.observe("touch", started, err)

	return err
}

// List records and forwards the call.
func (registry *instrumentedRegistry) List(ctx context.Context, query discovery.ListQuery) ([]discovery.Instance, error) {
	started := time.Now()
	instances, err := registry.next.List(ctx, query)
	registry.observe("list", started, err)

	return instances, err
}

// Delete records and forwards the call.
func (registry *instrumentedRegistry) Delete(ctx context.Context, id string, check discovery.OwnerCheck) error {
	started := time.Now()
	err := registry.next.Delete(ctx, id, check)
	registry.observe("delete", started, err)

	return err
}

// DeleteStale records and forwards the call, and counts removed instances
// as expired.
func (registry *instrumentedRegistry) DeleteStale(ctx context.Context, cutoff time.Time) (int, error) {
	started := time.Now()
	removed, err := registry.next.DeleteStale(ctx, cutoff)
	registry.observe("delete_stale", started, err)

	if removed > 0 {
		registry.metrics.expiredInstances.Add(float64(removed))
	}

	return removed, err
}
