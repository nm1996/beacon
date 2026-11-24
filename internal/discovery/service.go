package discovery

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

// Config holds the settings Service enforces.
type Config struct {
	// TTL is how long an instance may go without a heartbeat before it is
	// considered stale.
	TTL time.Duration
	// CleanupInterval is how often RunCleanup removes stale instances.
	CleanupInterval time.Duration
	// MaxNameLength is the longest service name Register accepts, at most
	// MaxLengthLimit. Zero means DefaultMaxNameLength.
	MaxNameLength int
	// MaxAddressLength is the longest address Register accepts, at most
	// MaxLengthLimit. Zero means DefaultMaxAddressLength.
	MaxAddressLength int
	// MaxInstances is the most instances the registry may hold at once.
	// Registrations beyond it get ErrRegistryFull. It bounds memory use,
	// response sizes and metric work however clients behave.
	MaxInstances int
	// MaxInstancesPerOwner is the most instances one caller may hold at
	// once, so a single identity cannot take every place MaxInstances
	// allows. Registrations beyond it get ErrOwnerQuotaExceeded. Zero means
	// no limit per owner. It applies only to callers with an ID, that is
	// when the transport authenticates.
	MaxInstancesPerOwner int
}

// validate reports the first setting that is out of range.
func (config Config) validate() error {
	if config.TTL <= 0 {
		return errors.New("TTL must be positive")
	}

	if config.CleanupInterval <= 0 {
		return errors.New("cleanup interval must be positive")
	}

	if config.MaxNameLength < 0 || config.MaxNameLength > MaxLengthLimit {
		return fmt.Errorf("max name length must be between 1 and %d, or 0 for the default", MaxLengthLimit)
	}

	if config.MaxAddressLength < 0 || config.MaxAddressLength > MaxLengthLimit {
		return fmt.Errorf("max address length must be between 1 and %d, or 0 for the default", MaxLengthLimit)
	}

	if config.MaxInstances <= 0 {
		return errors.New("maximum number of instances must be positive")
	}

	if config.MaxInstancesPerOwner < 0 || config.MaxInstancesPerOwner > config.MaxInstances {
		return errors.New("maximum number of instances per owner must be between 0 (no limit) and the maximum number of instances")
	}

	return nil
}

// maxNameLength is MaxNameLength, or its default when unset.
func (config Config) maxNameLength() int {
	if config.MaxNameLength == 0 {
		return DefaultMaxNameLength
	}

	return config.MaxNameLength
}

// maxAddressLength is MaxAddressLength, or its default when unset.
func (config Config) maxAddressLength() int {
	if config.MaxAddressLength == 0 {
		return DefaultMaxAddressLength
	}

	return config.MaxAddressLength
}

// Service applies Beacon's rules on top of a Registry. Every transport, the
// HTTP API today and others later, goes through it rather than the Registry.
// It is safe for concurrent use as long as the Registry is.
type Service struct {
	registry Registry
	config   Config
	logger   *slog.Logger
}

// New returns a Service that stores instances in registry, expires them
// according to config and reports registrations and cleanups to logger.
func New(registry Registry, config Config, logger *slog.Logger) (*Service, error) {
	if err := config.validate(); err != nil {
		return nil, fmt.Errorf("discovery config: %w", err)
	}

	return &Service{registry: registry, config: config, logger: logger}, nil
}

// TTL is how long an instance may go without a heartbeat. Transports report
// it to clients so they can pace their heartbeats.
func (service *Service) TTL() time.Duration {
	return service.config.TTL
}

// idempotencyNamespace is the UUID namespace of instance IDs derived from
// idempotency keys. It must never change, or repeated registrations would
// stop finding the instances their first attempt created.
var idempotencyNamespace = uuid.MustParse("1f0b6a52-3c8e-4d6b-9a57-0c3f6e2d8b41")

// Register validates registration and stores it as an instance owned by
// caller, with the ID the registration gives, one derived from its
// idempotency key and the caller, or a new random one. It reports whether
// it created the instance.
//
// If an instance with that ID exists, belongs to the caller and has the
// same name, address and port, the registration is a repeat: it counts as
// a heartbeat and returns that instance, created false. Otherwise it fails
// with ErrInstanceConflict, or ErrIdempotencyKeyReused when the ID came
// from a key. Validation failures wrap ErrInvalidRegistration.
func (service *Service) Register(ctx context.Context, registration Registration, caller Caller) (Instance, bool, error) {
	if err := registration.validate(service.config); err != nil {
		return Instance{}, false, err
	}

	id := registration.ID

	switch {
	case registration.IdempotencyKey != "":
		// The owner is part of the name, so callers cannot reach each
		// other's instances by using the same key.
		id = uuid.NewSHA1(idempotencyNamespace, []byte(caller.ID+"\x00"+registration.IdempotencyKey)).String()
	case id == "":
		id = uuid.New().String()
	}

	chosen := registration.ID != "" || registration.IdempotencyKey != ""

	// Twice at most: an instance found to exist may be removed before it
	// can be refreshed, and is then created again.
	for range 2 {
		now := time.Now()
		instance := Instance{
			ID:            id,
			Name:          registration.Name,
			Address:       registration.Address,
			Port:          registration.Port,
			Owner:         caller.ID,
			RegisteredAt:  now,
			LastHeartbeat: now,
		}

		err := service.registry.Create(ctx, instance, Limits{
			Total:    service.config.MaxInstances,
			PerOwner: service.config.MaxInstancesPerOwner,
		})
		if err == nil {
			service.logRegistered(ctx, "service registered", instance)
			return instance, true, nil
		}

		if !chosen || !errors.Is(err, ErrInstanceExists) {
			return Instance{}, false, fmt.Errorf("store instance: %w", err)
		}

		existing, refreshed, err := service.repeat(ctx, instance, registration.IdempotencyKey != "", caller)
		if err != nil {
			return Instance{}, false, err
		}

		if refreshed {
			service.logRegistered(ctx, "service registration repeated", existing)
			return existing, false, nil
		}
	}

	return Instance{}, false, fmt.Errorf("store instance %s: %w", id, ErrInstanceExists)
}

// repeat handles a registration whose ID already exists: if the stored
// instance is the same one, it refreshes and returns it. It reports false,
// without an error, if the instance disappeared meanwhile.
func (service *Service) repeat(ctx context.Context, wanted Instance, fromKey bool, caller Caller) (Instance, bool, error) {
	existing, err := service.registry.Get(ctx, wanted.ID)
	if errors.Is(err, ErrInstanceNotFound) {
		return Instance{}, false, nil
	}

	if err != nil {
		return Instance{}, false, fmt.Errorf("look up instance %s: %w", wanted.ID, err)
	}

	sameInstance := existing.Name == wanted.Name && existing.Address == wanted.Address && existing.Port == wanted.Port

	switch {
	case !caller.check().Allows(existing.Owner):
		return Instance{}, false, fmt.Errorf("register %s: %w", wanted.ID, ErrInstanceConflict)
	case !sameInstance && fromKey:
		return Instance{}, false, fmt.Errorf("register %s: %w", wanted.ID, ErrIdempotencyKeyReused)
	case !sameInstance:
		return Instance{}, false, fmt.Errorf("register %s: %w", wanted.ID, ErrInstanceConflict)
	}

	err = service.registry.Touch(ctx, wanted.ID, wanted.LastHeartbeat, caller.check())
	if errors.Is(err, ErrInstanceNotFound) {
		return Instance{}, false, nil
	}

	if err != nil {
		return Instance{}, false, fmt.Errorf("refresh instance %s: %w", wanted.ID, err)
	}

	existing.LastHeartbeat = wanted.LastHeartbeat

	return existing, true, nil
}

// logRegistered logs message about instance with its ID, name, address,
// port and owner.
func (service *Service) logRegistered(ctx context.Context, message string, instance Instance) {
	attributes := []slog.Attr{
		slog.String("id", instance.ID),
		slog.String("name", instance.Name),
		slog.String("address", instance.Address),
		slog.Int("port", instance.Port),
	}
	if instance.Owner != "" {
		attributes = append(attributes, slog.String("owner", instance.Owner))
	}

	service.logger.LogAttrs(ctx, slog.LevelInfo, message, attributes...)
}

// Heartbeat records that the instance with id is still alive. It returns
// ErrInstanceNotFound if there is none, for example because it expired, and
// ErrInstanceNotOwned if caller may not change it.
func (service *Service) Heartbeat(ctx context.Context, id string, caller Caller) error {
	if err := service.registry.Touch(ctx, id, time.Now(), caller.check()); err != nil {
		return fmt.Errorf("heartbeat %s: %w", id, err)
	}

	return nil
}

// Discover returns the live instances named name, or every live instance
// when name is empty, sorted by ID.
func (service *Service) Discover(ctx context.Context, name string) ([]Instance, error) {
	return service.List(ctx, ListQuery{Name: name})
}

// List returns the instances query selects, sorted by ID. Transports use
// it to serve one page at a time. It leaves out instances whose last
// heartbeat is more than TTL ago, which cleanup has yet to remove; clients
// should not be sent to instances Beacon already considers gone.
func (service *Service) List(ctx context.Context, query ListQuery) ([]Instance, error) {
	query.HeartbeatSince = time.Now().Add(-service.config.TTL)

	instances, err := service.registry.List(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list instances: %w", err)
	}

	return instances, nil
}

// Unregister removes the instance with id. It returns ErrInstanceNotFound if
// there is none, and ErrInstanceNotOwned if caller may not change it.
func (service *Service) Unregister(ctx context.Context, id string, caller Caller) error {
	if err := service.registry.Delete(ctx, id, caller.check()); err != nil {
		return fmt.Errorf("unregister %s: %w", id, err)
	}

	attributes := []slog.Attr{slog.String("id", id)}
	if caller.ID != "" {
		attributes = append(attributes, slog.String("by", caller.ID))
	}

	service.logger.LogAttrs(ctx, slog.LevelInfo, "service unregistered", attributes...)

	return nil
}

// RunCleanup removes stale instances every CleanupInterval until ctx is
// cancelled. A failed sweep is logged and retried on the next tick, so a
// backend that is briefly unreachable does not stop cleanup for good.
//
// It blocks, so callers run it in their own goroutine and stop it by
// cancelling ctx. It always returns nil once ctx is cancelled.
func (service *Service) RunCleanup(ctx context.Context) error {
	ticker := time.NewTicker(service.config.CleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			service.cleanup(ctx)
		}
	}
}

// cleanup removes every instance whose last heartbeat is more than TTL ago.
func (service *Service) cleanup(ctx context.Context) {
	removed, err := service.registry.DeleteStale(ctx, time.Now().Add(-service.config.TTL))
	if err != nil {
		service.logger.ErrorContext(ctx, "remove stale service instances", slog.Any("error", err))
		return
	}

	if removed > 0 {
		service.logger.InfoContext(ctx, "removed stale service instances", slog.Int("count", removed))
	}
}
