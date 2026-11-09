package registry

import (
	"errors"
	"testing"
	"time"
)

// newService returns a service that passes validation.
func newService(name string) *Service {
	return &Service{
		Name:    name,
		Address: "localhost",
		Port:    8080,
	}
}

// mustRegister adds service and fails the test if that is rejected.
func mustRegister(t *testing.T, r *Registry, service *Service) string {
	t.Helper()

	id, err := r.Register(service)
	if err != nil {
		t.Fatalf("register %q: %v", service.Name, err)
	}

	return id
}

func TestRegister(t *testing.T) {
	r := New()

	input := newService("payment-service")

	id := mustRegister(t, r, input)

	if id == "" {
		t.Fatal("expected generated service ID")
	}

	services := r.GetServices("payment-service")

	if len(services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(services))
	}

	service := services[0]

	if service.ID != id {
		t.Errorf("expected ID %s, got %s", id, service.ID)
	}

	if service.RegisteredAt.IsZero() {
		t.Error("expected RegisteredAt to be set")
	}

	if service.LastHeartbeat.IsZero() {
		t.Error("expected LastHeartbeat to be set")
	}

	if service.Name != input.Name {
		t.Errorf("expected name %s, got %s", input.Name, service.Name)
	}
}

func TestRegisterCopiesInput(t *testing.T) {
	r := New()

	input := newService("payment-service")

	mustRegister(t, r, input)

	// Change original input after registration.
	input.Name = "modified-service"
	input.Port = 9090

	services := r.GetServices("payment-service")

	if len(services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(services))
	}

	if services[0].Name != "payment-service" {
		t.Errorf("expected original name, got %s", services[0].Name)
	}

	if services[0].Port != 8080 {
		t.Errorf("expected original port 8080, got %d", services[0].Port)
	}
}

func TestRegisterNil(t *testing.T) {
	r := New()

	id, err := r.Register(nil)

	if !errors.Is(err, ErrInvalidService) {
		t.Errorf("expected ErrInvalidService, got %v", err)
	}

	if id != "" {
		t.Errorf("expected empty ID for nil service, got %s", id)
	}
}

func TestRegisterRejectsInvalidService(t *testing.T) {
	tests := []struct {
		name    string
		service Service
	}{
		{name: "empty name", service: Service{Address: "localhost", Port: 8080}},
		{name: "blank name", service: Service{Name: "  ", Address: "localhost", Port: 8080}},
		{name: "empty address", service: Service{Name: "orders", Port: 8080}},
		{name: "blank address", service: Service{Name: "orders", Address: "\t", Port: 8080}},
		{name: "zero port", service: Service{Name: "orders", Address: "localhost"}},
		{name: "negative port", service: Service{Name: "orders", Address: "localhost", Port: -1}},
		{name: "port above range", service: Service{Name: "orders", Address: "localhost", Port: 65536}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := New()

			id, err := r.Register(&tt.service)

			if !errors.Is(err, ErrInvalidService) {
				t.Fatalf("expected ErrInvalidService, got %v", err)
			}

			if id != "" {
				t.Errorf("expected empty ID, got %s", id)
			}

			if len(r.GetServices("")) != 0 {
				t.Error("expected invalid service not to be stored")
			}
		})
	}
}

func TestRegisterAcceptsPortBoundaries(t *testing.T) {
	for _, port := range []int{1, 65535} {
		r := New()

		service := newService("orders")
		service.Port = port

		if _, err := r.Register(service); err != nil {
			t.Errorf("expected port %d to be accepted, got %v", port, err)
		}
	}
}

func TestHeartbeat(t *testing.T) {
	r := New()

	id := mustRegister(t, r, newService("payment-service"))

	before := time.Now()

	if err := r.Heartbeat(id); err != nil {
		t.Fatalf("expected heartbeat to succeed, got %v", err)
	}

	services := r.GetServices("payment-service")

	if len(services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(services))
	}

	if services[0].LastHeartbeat.Before(before) {
		t.Error("expected LastHeartbeat to be updated")
	}
}

func TestHeartbeatUnknownService(t *testing.T) {
	r := New()

	err := r.Heartbeat("unknown-id")

	if !errors.Is(err, ErrServiceNotFound) {
		t.Errorf("expected ErrServiceNotFound, got %v", err)
	}
}

func TestGetServices(t *testing.T) {
	r := New()

	mustRegister(t, r, newService("payment-service"))
	mustRegister(t, r, newService("payment-service"))
	mustRegister(t, r, newService("user-service"))

	paymentServices := r.GetServices("payment-service")

	if len(paymentServices) != 2 {
		t.Errorf("expected 2 payment services, got %d", len(paymentServices))
	}

	allServices := r.GetServices("")

	if len(allServices) != 3 {
		t.Errorf("expected 3 services, got %d", len(allServices))
	}
}

func TestGetServicesReturnsCopies(t *testing.T) {
	r := New()

	mustRegister(t, r, newService("payment-service"))

	services := r.GetServices("payment-service")

	// Modify returned object.
	services[0].Name = "modified-service"
	services[0].Port = 9090

	// Registry should remain unchanged.
	original := r.GetServices("payment-service")

	if len(original) != 1 {
		t.Fatalf("expected 1 service, got %d", len(original))
	}

	if original[0].Name != "payment-service" {
		t.Errorf("registry was modified: name = %s", original[0].Name)
	}

	if original[0].Port != 8080 {
		t.Errorf("registry was modified: port = %d", original[0].Port)
	}
}

func TestUnregister(t *testing.T) {
	r := New()

	id := mustRegister(t, r, newService("payment-service"))

	if err := r.Unregister(id); err != nil {
		t.Fatalf("expected unregister to succeed, got %v", err)
	}

	services := r.GetServices("payment-service")

	if len(services) != 0 {
		t.Errorf("expected 0 services, got %d", len(services))
	}
}

func TestUnregisterUnknownService(t *testing.T) {
	r := New()

	err := r.Unregister("unknown-id")

	if !errors.Is(err, ErrServiceNotFound) {
		t.Errorf("expected ErrServiceNotFound, got %v", err)
	}
}

func TestCleanup(t *testing.T) {
	r := New()

	staleID := mustRegister(t, r, newService("stale-service"))
	mustRegister(t, r, newService("active-service"))

	// Simulate an old heartbeat.
	r.mu.Lock()
	r.services[staleID].LastHeartbeat = time.Now().Add(-2 * time.Hour)
	r.mu.Unlock()

	removed := r.Cleanup(time.Hour)

	if removed != 1 {
		t.Errorf("expected 1 removed service, got %d", removed)
	}

	if len(r.GetServices("")) != 1 {
		t.Errorf("expected 1 remaining service, got %d", len(r.GetServices("")))
	}
}

func TestCleanupDoesNotRemoveActiveServices(t *testing.T) {
	r := New()

	mustRegister(t, r, newService("active-service"))

	removed := r.Cleanup(time.Hour)

	if removed != 0 {
		t.Errorf("expected 0 removed services, got %d", removed)
	}
}
