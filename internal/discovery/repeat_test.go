package discovery_test

import (
	"errors"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nm1996/beacon/internal/discovery"
)

// ownerA and ownerB are two authenticated callers.
var (
	ownerA = discovery.Caller{ID: "a", Name: "a"}
	ownerB = discovery.Caller{ID: "b", Name: "b"}
)

func TestRegisterWithAnIDKeepsIt(t *testing.T) {
	service := newService(t)
	wanted := registration("orders")
	wanted.ID = "orders-7f3c9a"

	instance, created, err := service.Register(t.Context(), wanted, ownerA)
	if err != nil || !created || instance.ID != "orders-7f3c9a" {
		t.Fatalf("expected a new instance with the given ID, got %+v, %v, %v", instance, created, err)
	}
}

func TestRepeatingARegistrationRefreshesTheInstance(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		service := newService(t)
		wanted := registration("orders")
		wanted.ID = "orders-1"

		first, _, err := service.Register(t.Context(), wanted, ownerA)
		if err != nil {
			t.Fatal(err)
		}

		time.Sleep(testConfig.TTL - time.Second)

		again, created, err := service.Register(t.Context(), wanted, ownerA)
		if err != nil || created {
			t.Fatalf("expected the repeat to refresh, got %v, %v", created, err)
		}

		if !again.RegisteredAt.Equal(first.RegisteredAt) || !again.LastHeartbeat.After(first.LastHeartbeat) {
			t.Errorf("expected the original instance with a new heartbeat, got %+v", again)
		}

		// The refresh counts as a heartbeat: the instance outlives its first TTL.
		time.Sleep(2 * time.Second)

		instances, err := service.Discover(t.Context(), "orders")
		if err != nil || len(instances) != 1 {
			t.Errorf("expected the refreshed instance to be live, got %d, %v", len(instances), err)
		}
	})
}

func TestRegisteringAnotherInstancesIDIsAConflict(t *testing.T) {
	service := newService(t)
	wanted := registration("orders")
	wanted.ID = "orders-1"

	if _, _, err := service.Register(t.Context(), wanted, ownerA); err != nil {
		t.Fatal(err)
	}

	moved := wanted
	moved.Port = 9999

	for name, attempt := range map[string]struct {
		registration discovery.Registration
		caller       discovery.Caller
	}{
		"another owner": {registration: wanted, caller: ownerB},
		"another port":  {registration: moved, caller: ownerA},
		"another name":  {registration: discovery.Registration{ID: "orders-1", Name: "payments", Address: "localhost", Port: 8080}, caller: ownerA},
	} {
		if _, _, err := service.Register(t.Context(), attempt.registration, attempt.caller); !errors.Is(err, discovery.ErrInstanceConflict) {
			t.Errorf("%s: expected ErrInstanceConflict, got %v", name, err)
		}
	}

	instances, _ := service.Discover(t.Context(), "orders")
	if len(instances) != 1 || instances[0].Port != wanted.Port || instances[0].Owner != "a" {
		t.Errorf("expected the original instance untouched, got %+v", instances)
	}
}

func TestIdempotencyKeysFindTheFirstInstance(t *testing.T) {
	service := newService(t)
	wanted := registration("orders")
	wanted.IdempotencyKey = "deploy-42/attempt"

	first, created, err := service.Register(t.Context(), wanted, ownerA)
	if err != nil || !created {
		t.Fatalf("expected the first attempt to create, got %v, %v", created, err)
	}

	again, created, err := service.Register(t.Context(), wanted, ownerA)
	if err != nil || created || again.ID != first.ID {
		t.Fatalf("expected the repeat to find %s, got %+v, %v, %v", first.ID, again, created, err)
	}

	other, created, err := service.Register(t.Context(), wanted, ownerB)
	if err != nil || !created || other.ID == first.ID {
		t.Errorf("expected another owner's same key to be a separate instance, got %+v, %v, %v", other, created, err)
	}

	changed := wanted
	changed.Port = 9999

	if _, _, err := service.Register(t.Context(), changed, ownerA); !errors.Is(err, discovery.ErrIdempotencyKeyReused) {
		t.Errorf("expected a key reused with another body to fail with ErrIdempotencyKeyReused, got %v", err)
	}

	instances, _ := service.Discover(t.Context(), "orders")
	if len(instances) != 2 {
		t.Errorf("expected one instance per owner, got %d", len(instances))
	}
}

func TestRegistrationIDsAndKeysAreValidated(t *testing.T) {
	service := newService(t)

	for name, wanted := range map[string]discovery.Registration{
		"id with a slash":    {ID: "a/b", Name: "orders", Address: "localhost", Port: 8080},
		"id starting with -": {ID: "-a", Name: "orders", Address: "localhost", Port: 8080},
		"id too long":        {ID: strings.Repeat("a", 129), Name: "orders", Address: "localhost", Port: 8080},
		"key with a newline": {IdempotencyKey: "a\nb", Name: "orders", Address: "localhost", Port: 8080},
		"id and key":         {ID: "a", IdempotencyKey: "k", Name: "orders", Address: "localhost", Port: 8080},
	} {
		if _, _, err := service.Register(t.Context(), wanted, ownerA); !errors.Is(err, discovery.ErrInvalidRegistration) {
			t.Errorf("%s: expected ErrInvalidRegistration, got %v", name, err)
		}
	}
}

func TestARepeatAfterExpiryCreatesTheInstanceAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		service := newService(t)
		wanted := registration("orders")
		wanted.ID = "orders-1"

		if _, _, err := service.Register(t.Context(), wanted, ownerA); err != nil {
			t.Fatal(err)
		}

		if err := service.Unregister(t.Context(), "orders-1", ownerA); err != nil {
			t.Fatal(err)
		}

		instance, created, err := service.Register(t.Context(), wanted, ownerA)
		if err != nil || !created || instance.ID != "orders-1" {
			t.Errorf("expected the ID to be created again once free, got %+v, %v, %v", instance, created, err)
		}
	})
}
