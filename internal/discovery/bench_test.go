package discovery_test

import (
	"testing"
	"time"

	"github.com/nm1996/beacon/internal/discovery"
	"github.com/nm1996/beacon/internal/registry/memory"
)

// BenchmarkServiceRegister is a registration through the business rules:
// validation, a new UUID, the store and the log line, with the logger
// discarding.
func BenchmarkServiceRegister(b *testing.B) {
	config := discovery.Config{TTL: time.Minute, CleanupInterval: time.Second, MaxInstances: 1 << 30}

	service, err := discovery.New(memory.New(), config, discard)
	if err != nil {
		b.Fatal(err)
	}

	registration := discovery.Registration{Name: "payment-service.v2", Address: "orders.default.svc.cluster.local", Port: 8080}
	caller := discovery.Caller{Name: "orders"}

	b.ReportAllocs()

	for b.Loop() {
		if _, err := service.Register(b.Context(), registration, caller); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkServiceHeartbeat is a heartbeat through the business rules,
// including the owner check.
func BenchmarkServiceHeartbeat(b *testing.B) {
	service, err := discovery.New(memory.New(), testConfig, discard)
	if err != nil {
		b.Fatal(err)
	}

	caller := discovery.Caller{Name: "orders"}

	instance, err := service.Register(b.Context(), registration("orders"), caller)
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()

	for b.Loop() {
		if err := service.Heartbeat(b.Context(), instance.ID, caller); err != nil {
			b.Fatal(err)
		}
	}
}
