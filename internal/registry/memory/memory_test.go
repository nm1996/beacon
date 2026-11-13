package memory

import (
	"fmt"
	"testing"
	"time"

	"github.com/nm1996/beacon/internal/discovery"
	"github.com/nm1996/beacon/internal/registry/registrytest"
)

func TestRegistryContract(t *testing.T) {
	registrytest.Run(t, func(*testing.T) discovery.Registry {
		return New()
	})
}

func TestNameIndexForgetsEmptyNames(t *testing.T) {
	registry := New()
	ctx := t.Context()
	anyOwner := discovery.OwnerCheck{Any: true}
	now := time.Now()

	for index := range 100 {
		instance := discovery.Instance{
			ID:            fmt.Sprintf("id-%d", index),
			Name:          fmt.Sprintf("name-%d", index%10),
			LastHeartbeat: now,
		}

		if err := registry.Create(ctx, instance, discovery.Limits{Total: 1000}); err != nil {
			t.Fatal(err)
		}
	}

	for index := range 50 {
		if err := registry.Delete(ctx, fmt.Sprintf("id-%d", index), anyOwner); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := registry.DeleteStale(ctx, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}

	if len(registry.byName) != 0 {
		t.Errorf("expected no names left once every instance is gone, got %d", len(registry.byName))
	}
}
