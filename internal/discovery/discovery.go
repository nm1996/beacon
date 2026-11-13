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

	// ErrInstanceNotOwned reports that the caller may not change an instance
	// another caller registered.
	ErrInstanceNotOwned = errors.New("instance registered by another caller")

	// ErrRegistryFull reports that a registration was refused because the
	// registry already holds the configured maximum number of instances.
	// Registry implementations return it, unwrapped or wrapped, from Create.
	ErrRegistryFull = errors.New("registry is full")
)

// Caller is who asks to change an instance. Transports build it from
// whatever identity they authenticated; discovery only compares names.
type Caller struct {
	// Name identifies the caller, and becomes the Owner of instances it
	// registers. It is empty when the transport does not authenticate.
	Name string
	// AnyInstance lets the caller change instances it did not register. A
	// transport without authentication sets it, so everyone may change
	// everything, as before ownership existed.
	AnyInstance bool
}

// mayChange reports whether caller may send heartbeats for, or unregister,
// instance. Instances without an owner, registered while authentication was
// off, may be changed by anyone.
func (caller Caller) mayChange(instance Instance) bool {
	return caller.AnyInstance || instance.Owner == "" || instance.Owner == caller.Name
}

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
	// Owner names the caller that registered the instance, or is empty if it
	// registered without authentication. Only the owner, or a caller allowed
	// to change any instance, may send its heartbeats or unregister it.
	Owner string
	// RegisteredAt is when the instance registered.
	RegisteredAt time.Time
	// LastHeartbeat is when the instance last proved it was alive.
	LastHeartbeat time.Time
}

// Registry stores instances. It only stores: validation, IDs, timestamps
// and expiry policy are decided by Service, so every backend behaves the
// same. Implementations must be safe for concurrent use.
type Registry interface {
	// Create stores instance under instance.ID unless the registry already
	// holds limit instances, in which case it returns ErrRegistryFull. The
	// check and the insert are one operation, so concurrent calls can never
	// take the registry past limit. It fails if an instance with that ID
	// already exists.
	Create(ctx context.Context, instance Instance, limit int) error
	// Get returns a copy of the instance with id. It returns
	// ErrInstanceNotFound if there is none.
	Get(ctx context.Context, id string) (Instance, error)
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
