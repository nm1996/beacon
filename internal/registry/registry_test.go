package registry

import (
	"testing"
	"time"
)

func TestRegister(t *testing.T) {
	r := NewRegistry()

	input := &Service{
		Name:    "payment-service",
		Address: "localhost",
		Port:    8080,
	}

	id := r.Register(input)

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
	r := NewRegistry()

	input := &Service{
		Name: "payment-service",
		Port: 8080,
	}

	r.Register(input)

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
	r := NewRegistry()

	id := r.Register(nil)

	if id != "" {
		t.Errorf("expected empty ID for nil service, got %s", id)
	}
}

func TestHeartbeat(t *testing.T) {
	r := NewRegistry()

	id := r.Register(&Service{
		Name: "payment-service",
	})

	before := time.Now()

	success := r.Heartbeat(id)

	if !success {
		t.Fatal("expected heartbeat to succeed")
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
	r := NewRegistry()

	success := r.Heartbeat("unknown-id")

	if success {
		t.Error("expected heartbeat to fail for unknown service")
	}
}

func TestGetServices(t *testing.T) {
	r := NewRegistry()

	r.Register(&Service{Name: "payment-service"})
	r.Register(&Service{Name: "payment-service"})
	r.Register(&Service{Name: "user-service"})

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
	r := NewRegistry()

	r.Register(&Service{
		Name: "payment-service",
		Port: 8080,
	})

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
	r := NewRegistry()

	id := r.Register(&Service{
		Name: "payment-service",
	})

	success := r.Unregister(id)

	if !success {
		t.Fatal("expected unregister to succeed")
	}

	services := r.GetServices("payment-service")

	if len(services) != 0 {
		t.Errorf("expected 0 services, got %d", len(services))
	}
}

func TestUnregisterUnknownService(t *testing.T) {
	r := NewRegistry()

	success := r.Unregister("unknown-id")

	if success {
		t.Error("expected unregister to fail for unknown service")
	}
}

func TestCleanup(t *testing.T) {
	r := NewRegistry()

	staleID := r.Register(&Service{
		Name: "stale-service",
	})

	r.Register(&Service{
		Name: "active-service",
	})

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
	r := NewRegistry()

	r.Register(&Service{
		Name: "active-service",
	})

	removed := r.Cleanup(time.Hour)

	if removed != 0 {
		t.Errorf("expected 0 removed services, got %d", removed)
	}
}
