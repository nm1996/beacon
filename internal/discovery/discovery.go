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

	// ErrOwnerQuotaExceeded reports that a registration was refused because
	// its owner already holds the configured maximum number of instances.
	// Registry implementations return it, unwrapped or wrapped, from Create.
	ErrOwnerQuotaExceeded = errors.New("owner holds its maximum number of instances")
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

// check returns the OwnerCheck that limits caller's changes.
func (caller Caller) check() OwnerCheck {
	return OwnerCheck{Owner: caller.Name, Any: caller.AnyInstance}
}

// Limits bounds what Registry.Create may store.
type Limits struct {
	// Total is the most instances the registry may hold.
	Total int
	// PerOwner is the most instances that share one non-empty Owner. Zero
	// means no limit per owner. Instances without an owner, registered
	// while authentication is off, never count towards it.
	PerOwner int
}

// ListQuery selects instances for Registry.List.
type ListQuery struct {
	// Name keeps only instances with this name. Empty keeps every name.
	Name string
	// After keeps only instances whose ID sorts after it, for fetching the
	// page that follows the one whose last ID it is. Empty starts at the
	// beginning.
	After string
	// Limit is the most instances to return. Zero means no limit.
	Limit int
}

// OwnerCheck limits Touch and Delete to instances a caller may change.
// Registry backends apply it in the same operation as the change, so no
// other request can come between the check and the change.
type OwnerCheck struct {
	// Owner is the caller's name. Instances registered by someone else are
	// refused with ErrInstanceNotOwned.
	Owner string
	// Any allows every instance, whoever registered it.
	Any bool
}

// Allows reports whether an instance registered by owner may be changed.
// Instances without an owner, registered while authentication was off,
// may be changed by anyone.
func (check OwnerCheck) Allows(owner string) bool {
	return check.Any || owner == "" || owner == check.Owner
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
	// Create stores instance under instance.ID unless that would break
	// limits: it returns ErrRegistryFull if the registry already holds
	// limits.Total instances, and ErrOwnerQuotaExceeded if instance.Owner
	// already holds limits.PerOwner. The checks and the insert are one
	// operation, so concurrent calls can never go past either limit. It
	// fails if an instance with that ID already exists.
	Create(ctx context.Context, instance Instance, limits Limits) error
	// Get returns a copy of the instance with id. It returns
	// ErrInstanceNotFound if there is none.
	Get(ctx context.Context, id string) (Instance, error)
	// Touch sets the LastHeartbeat of the instance with id to at, if check
	// allows changing it. It returns ErrInstanceNotFound if there is no such
	// instance and ErrInstanceNotOwned if check refuses it; checking and
	// changing are one operation.
	Touch(ctx context.Context, id string, at time.Time, check OwnerCheck) error
	// List returns the instances query selects, sorted by ID, so a caller
	// can page through them with query.After.
	List(ctx context.Context, query ListQuery) ([]Instance, error)
	// Delete removes the instance with id, if check allows changing it. It
	// returns ErrInstanceNotFound if there is no such instance and
	// ErrInstanceNotOwned if check refuses it; checking and removing are one
	// operation.
	Delete(ctx context.Context, id string, check OwnerCheck) error
	// DeleteStale removes every instance whose LastHeartbeat is before cutoff
	// and reports how many it removed. An instance exactly at cutoff stays.
	DeleteStale(ctx context.Context, cutoff time.Time) (int, error)
}
