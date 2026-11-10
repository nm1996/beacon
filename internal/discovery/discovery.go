// Package discovery holds Beacon's business rules: what a valid registration
// is, how instances are identified and timestamped, and when they expire.
//
// It keeps instances through the Registry interface, so the storage backend
// can change without touching these rules or the transports built on top of
// them. Backends live under internal/registry and must pass the contract
// tests in internal/registry/registrytest.
package discovery

import (
	"context"
	"errors"
	"time"
)

var (
	// ErrInvalidRegistration reports that a registration failed validation.
	// The wrapping error names the field that is wrong.
	ErrInvalidRegistration = errors.New("invalid registration")

	// ErrInstanceNotFound reports that no instance has the given ID. Registry
	// implementations return it, unwrapped or wrapped, for unknown IDs.
	ErrInstanceNotFound = errors.New("instance not found")
)

// Registration is what a service sends to announce one of its instances.
type Registration struct {
	// Name groups instances of the same service, such as "orders".
	Name string
	// Address is the host name or IP address where the instance is reachable.
	Address string
	// Port is the TCP port the instance listens on.
	Port int
}

// Instance is one registered, running copy of a service. It holds only
// plain values, so copying an Instance never shares state with the copy it
// came from.
type Instance struct {
	// ID identifies the instance. It is generated when the instance registers.
	ID string
	// Name groups instances of the same service, such as "orders".
	Name string
	// Address is the host name or IP address where the instance is reachable.
	Address string
	// Port is the TCP port the instance listens on.
	Port int
	// RegisteredAt is when the instance registered.
	RegisteredAt time.Time
	// LastHeartbeat is when the instance last proved it was alive.
	LastHeartbeat time.Time
}

// Registry stores instances. It only stores: validation, IDs, timestamps
// and expiry policy are decided by Service, so every backend behaves the
// same. Implementations must be safe for concurrent use.
type Registry interface {
	// Create stores instance under instance.ID. It fails if an instance with
	// that ID already exists.
	Create(ctx context.Context, instance Instance) error
	// Touch sets the LastHeartbeat of the instance with id to at. It returns
	// ErrInstanceNotFound if there is none.
	Touch(ctx context.Context, id string, at time.Time) error
	// List returns the instances named name, or every instance when name is
	// empty, in no particular order.
	List(ctx context.Context, name string) ([]Instance, error)
	// Delete removes the instance with id. It returns ErrInstanceNotFound if
	// there is none.
	Delete(ctx context.Context, id string) error
	// DeleteStale removes every instance whose LastHeartbeat is before cutoff
	// and reports how many it removed. An instance exactly at cutoff stays.
	DeleteStale(ctx context.Context, cutoff time.Time) (int, error)
}
