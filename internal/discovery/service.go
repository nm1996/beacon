package discovery

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

// Config holds the expiry settings Service enforces.
type Config struct {
	// TTL is how long an instance may go without a heartbeat before it is
	// considered stale.
	TTL time.Duration
	// CleanupInterval is how often RunCleanup removes stale instances.
	CleanupInterval time.Duration
}

// validate reports the first setting that is not positive.
func (config Config) validate() error {
	if config.TTL <= 0 {
		return errors.New("TTL must be positive")
	}

	if config.CleanupInterval <= 0 {
		return errors.New("cleanup interval must be positive")
	}

	return nil
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

// Register validates registration and stores it as a new instance with a
// generated ID. It returns an error wrapping ErrInvalidRegistration when
// validation fails.
func (service *Service) Register(ctx context.Context, registration Registration) (Instance, error) {
	if err := registration.validate(); err != nil {
		return Instance{}, err
	}

	now := time.Now()
	instance := Instance{
		ID:            uuid.New().String(),
		Name:          registration.Name,
		Address:       registration.Address,
		Port:          registration.Port,
		RegisteredAt:  now,
		LastHeartbeat: now,
	}

	if err := service.registry.Create(ctx, instance); err != nil {
		return Instance{}, fmt.Errorf("store instance: %w", err)
	}

	service.logger.InfoContext(ctx, "service registered",
		slog.String("id", instance.ID),
		slog.String("name", instance.Name),
		slog.String("address", instance.Address),
		slog.Int("port", instance.Port),
	)

	return instance, nil
}

// Heartbeat records that the instance with id is still alive. It returns
// ErrInstanceNotFound if there is none, for example because it expired.
func (service *Service) Heartbeat(ctx context.Context, id string) error {
	if err := service.registry.Touch(ctx, id, time.Now()); err != nil {
		return fmt.Errorf("heartbeat %s: %w", id, err)
	}

	return nil
}

// Discover returns the instances named name, or every instance when name is
// empty, in no particular order.
func (service *Service) Discover(ctx context.Context, name string) ([]Instance, error) {
	instances, err := service.registry.List(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("list instances: %w", err)
	}

	return instances, nil
}

// Unregister removes the instance with id. It returns ErrInstanceNotFound if
// there is none.
func (service *Service) Unregister(ctx context.Context, id string) error {
	if err := service.registry.Delete(ctx, id); err != nil {
		return fmt.Errorf("unregister %s: %w", id, err)
	}

	service.logger.InfoContext(ctx, "service unregistered", slog.String("id", id))

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
