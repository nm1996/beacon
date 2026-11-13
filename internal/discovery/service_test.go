package discovery_test

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/nm1996/beacon/internal/discovery"
	"github.com/nm1996/beacon/internal/registry/memory"
)

// testConfig is a valid configuration with a TTL long enough that nothing
// expires unless a test moves time on.
var testConfig = discovery.Config{TTL: 30 * time.Second, CleanupInterval: 5 * time.Second, MaxInstances: 1 << 20}

// discard is a logger that drops everything.
var discard = slog.New(slog.DiscardHandler)

// newService returns a Service over an empty in-memory registry.
func newService(t *testing.T) *discovery.Service {
	t.Helper()

	service, err := discovery.New(memory.New(), testConfig, discard)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	return service
}

// registration returns a registration that passes validation.
func registration(name string) discovery.Registration {
	return discovery.Registration{Name: name, Address: "localhost", Port: 8080}
}

// mustRegister registers a valid instance named name and returns it.
func mustRegister(t *testing.T, service *discovery.Service, name string) discovery.Instance {
	t.Helper()

	instance, err := service.Register(t.Context(), registration(name), anyone)
	if err != nil {
		t.Fatalf("register %q: %v", name, err)
	}

	return instance
}

// mustDiscover lists instances named name and fails the test on error.
func mustDiscover(t *testing.T, service *discovery.Service, name string) []discovery.Instance {
	t.Helper()

	instances, err := service.Discover(t.Context(), name)
	if err != nil {
		t.Fatalf("discover %q: %v", name, err)
	}

	return instances
}

func TestNewRejectsInvalidConfig(t *testing.T) {
	tests := []struct {
		name   string
		config discovery.Config
	}{
		{name: "zero TTL", config: discovery.Config{CleanupInterval: time.Second}},
		{name: "negative TTL", config: discovery.Config{TTL: -time.Second, CleanupInterval: time.Second}},
		{name: "zero interval", config: discovery.Config{TTL: time.Second}},
		{name: "negative interval", config: discovery.Config{TTL: time.Second, CleanupInterval: -time.Second, MaxInstances: 1}},
		{name: "zero max instances", config: discovery.Config{TTL: time.Second, CleanupInterval: time.Second}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := discovery.New(memory.New(), tt.config, discard); err == nil {
				t.Error("expected an error for invalid config")
			}
		})
	}
}

func TestTTL(t *testing.T) {
	if got := newService(t).TTL(); got != testConfig.TTL {
		t.Errorf("expected TTL %v, got %v", testConfig.TTL, got)
	}
}

func TestRegister(t *testing.T) {
	service := newService(t)
	before := time.Now()

	instance := mustRegister(t, service, "payment-service")

	if instance.ID == "" {
		t.Fatal("expected a generated ID")
	}

	if instance.Name != "payment-service" || instance.Address != "localhost" || instance.Port != 8080 {
		t.Errorf("expected the registration's fields, got %+v", instance)
	}

	if instance.RegisteredAt.Before(before) || !instance.LastHeartbeat.Equal(instance.RegisteredAt) {
		t.Errorf("expected RegisteredAt and LastHeartbeat to be set to now, got %+v", instance)
	}

	stored := mustDiscover(t, service, "payment-service")
	if len(stored) != 1 || stored[0].ID != instance.ID {
		t.Errorf("expected the returned instance to be stored, got %+v", stored)
	}
}

func TestRegisterGeneratesUniqueIDs(t *testing.T) {
	service := newService(t)

	first := mustRegister(t, service, "orders")
	second := mustRegister(t, service, "orders")

	if first.ID == second.ID {
		t.Errorf("expected two registrations to get different IDs, both got %s", first.ID)
	}

	if got := len(mustDiscover(t, service, "orders")); got != 2 {
		t.Errorf("expected registering twice to create two instances, got %d", got)
	}
}

func TestRegisterRejectsInvalidRegistration(t *testing.T) {
	tests := []struct {
		name         string
		registration discovery.Registration
	}{
		{name: "empty name", registration: discovery.Registration{Address: "localhost", Port: 8080}},
		{name: "blank name", registration: discovery.Registration{Name: "  ", Address: "localhost", Port: 8080}},
		{name: "empty address", registration: discovery.Registration{Name: "orders", Port: 8080}},
		{name: "blank address", registration: discovery.Registration{Name: "orders", Address: "\t", Port: 8080}},
		{name: "zero port", registration: discovery.Registration{Name: "orders", Address: "localhost"}},
		{name: "negative port", registration: discovery.Registration{Name: "orders", Address: "localhost", Port: -1}},
		{name: "port above range", registration: discovery.Registration{Name: "orders", Address: "localhost", Port: 65536}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := newService(t)

			instance, err := service.Register(t.Context(), tt.registration, anyone)

			if !errors.Is(err, discovery.ErrInvalidRegistration) {
				t.Fatalf("expected ErrInvalidRegistration, got %v", err)
			}

			if instance != (discovery.Instance{}) {
				t.Errorf("expected no instance, got %+v", instance)
			}

			if got := len(mustDiscover(t, service, "")); got != 0 {
				t.Errorf("expected nothing to be stored, got %d instances", got)
			}
		})
	}
}

func TestRegisterAcceptsPortBoundaries(t *testing.T) {
	for _, port := range []int{1, 65535} {
		reg := registration("orders")
		reg.Port = port

		if _, err := newService(t).Register(t.Context(), reg, anyone); err != nil {
			t.Errorf("expected port %d to be accepted, got %v", port, err)
		}
	}
}

func TestHeartbeat(t *testing.T) {
	service := newService(t)
	instance := mustRegister(t, service, "orders")

	if err := service.Heartbeat(t.Context(), instance.ID, anyone); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}

	got := mustDiscover(t, service, "orders")[0]
	if got.LastHeartbeat.Before(instance.LastHeartbeat) {
		t.Errorf("expected LastHeartbeat to move forward, went from %v to %v", instance.LastHeartbeat, got.LastHeartbeat)
	}
}

func TestHeartbeatUnknownInstance(t *testing.T) {
	err := newService(t).Heartbeat(t.Context(), "unknown-id", anyone)

	if !errors.Is(err, discovery.ErrInstanceNotFound) {
		t.Errorf("expected ErrInstanceNotFound, got %v", err)
	}
}

func TestDiscoverFiltersByName(t *testing.T) {
	service := newService(t)

	mustRegister(t, service, "payment-service")
	mustRegister(t, service, "payment-service")
	mustRegister(t, service, "user-service")

	if got := len(mustDiscover(t, service, "payment-service")); got != 2 {
		t.Errorf("expected 2 payment instances, got %d", got)
	}

	if got := len(mustDiscover(t, service, "")); got != 3 {
		t.Errorf("expected 3 instances in total, got %d", got)
	}
}

func TestUnregister(t *testing.T) {
	service := newService(t)
	instance := mustRegister(t, service, "orders")

	if err := service.Unregister(t.Context(), instance.ID, anyone); err != nil {
		t.Fatalf("unregister: %v", err)
	}

	if got := len(mustDiscover(t, service, "")); got != 0 {
		t.Errorf("expected the instance to be removed, %d remain", got)
	}

	if err := service.Unregister(t.Context(), instance.ID, anyone); !errors.Is(err, discovery.ErrInstanceNotFound) {
		t.Errorf("expected a second unregister to give ErrInstanceNotFound, got %v", err)
	}
}

// failingRegistry is a Registry whose every operation fails with err.
type failingRegistry struct {
	err error
}

// Create always fails.
func (registry failingRegistry) Create(context.Context, discovery.Instance, int) error {
	return registry.err
}

// Get always fails.
func (registry failingRegistry) Get(context.Context, string) (discovery.Instance, error) {
	return discovery.Instance{}, registry.err
}

// Touch always fails.
func (registry failingRegistry) Touch(context.Context, string, time.Time) error {
	return registry.err
}

// List always fails.
func (registry failingRegistry) List(context.Context, string) ([]discovery.Instance, error) {
	return nil, registry.err
}

// Delete always fails.
func (registry failingRegistry) Delete(context.Context, string) error {
	return registry.err
}

// DeleteStale always fails.
func (registry failingRegistry) DeleteStale(context.Context, time.Time) (int, error) {
	return 0, registry.err
}

func TestRegistryErrorsArePassedOn(t *testing.T) {
	unavailable := errors.New("backend unavailable")

	service, err := discovery.New(failingRegistry{err: unavailable}, testConfig, discard)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	ctx := t.Context()
	_, registerErr := service.Register(ctx, registration("orders"), anyone)
	_, discoverErr := service.Discover(ctx, "orders")

	for name, err := range map[string]error{
		"register":   registerErr,
		"heartbeat":  service.Heartbeat(ctx, "x", anyone),
		"discover":   discoverErr,
		"unregister": service.Unregister(ctx, "x", anyone),
		// A named caller makes the service look up the owner first.
		"heartbeat, owner lookup":  service.Heartbeat(ctx, "x", orders),
		"unregister, owner lookup": service.Unregister(ctx, "x", orders),
	} {
		if !errors.Is(err, unavailable) {
			t.Errorf("%s: expected the registry error to be wrapped, got %v", name, err)
		}

		if errors.Is(err, discovery.ErrInstanceNotFound) || errors.Is(err, discovery.ErrInvalidRegistration) {
			t.Errorf("%s: a backend failure must not look like a client error, got %v", name, err)
		}
	}
}

func TestInstanceHasNoSharedReferenceFields(t *testing.T) {
	// Registries hand out copies of Instance, which only protects their state
	// while Instance holds plain values. A slice, map or pointer field would
	// be shared between copies, so adding one needs a deep copy in every
	// backend and an update to this test.
	for field := range reflect.TypeFor[discovery.Instance]().Fields() {
		switch field.Type.Kind() {
		case reflect.Slice, reflect.Map, reflect.Pointer, reflect.Chan, reflect.Func, reflect.Interface:
			t.Errorf("Instance.%s is a %s; copies would share it", field.Name, field.Type.Kind())
		}
	}
}

func TestRegisterRefusesBeyondMaxInstances(t *testing.T) {
	config := testConfig
	config.MaxInstances = 2

	service, err := discovery.New(memory.New(), config, discard)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	first := mustRegister(t, service, "orders")
	mustRegister(t, service, "orders")

	if _, err := service.Register(t.Context(), registration("orders"), anyone); !errors.Is(err, discovery.ErrRegistryFull) {
		t.Fatalf("expected ErrRegistryFull beyond the limit, got %v", err)
	}

	if err := service.Unregister(t.Context(), first.ID, anyone); err != nil {
		t.Fatalf("unregister: %v", err)
	}

	if _, err := service.Register(t.Context(), registration("orders"), anyone); err != nil {
		t.Errorf("expected a place once an instance left, got %v", err)
	}
}
