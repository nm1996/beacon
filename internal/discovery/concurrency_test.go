package discovery_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/nm1996/beacon/internal/discovery"
	"github.com/nm1996/beacon/internal/registry/memory"
)

// These tests drive one Service from many goroutines at once, together with
// the cleanup worker, to give the race detector overlapping work. Run them
// with go test -race. The registry's own concurrency is covered by the
// registrytest contract.

const (
	workers      = 32
	opsPerWorker = 100
)

// runConcurrently starts count goroutines that each call work with their
// index, releases them at the same moment and waits for all of them.
func runConcurrently(count int, work func(worker int)) {
	var group sync.WaitGroup
	start := make(chan struct{})

	for worker := range count {
		group.Go(func() {
			<-start
			work(worker)
		})
	}

	close(start)
	group.Wait()
}

func TestConcurrentRegisterGivesUniqueIDs(t *testing.T) {
	service := newService(t)
	ids := make([][]string, workers)

	runConcurrently(workers, func(worker int) {
		for op := range opsPerWorker {
			instance, err := service.Register(t.Context(), registration(fmt.Sprintf("service-%d", op%8)), anyone)
			if err != nil {
				t.Errorf("worker %d: register: %v", worker, err)
				return
			}

			ids[worker] = append(ids[worker], instance.ID)
		}
	})

	seen := make(map[string]bool, workers*opsPerWorker)
	for _, workerIDs := range ids {
		for _, id := range workerIDs {
			if seen[id] {
				t.Fatalf("ID %s was handed out twice", id)
			}

			seen[id] = true
		}
	}

	if got := len(mustDiscover(t, service, "")); got != workers*opsPerWorker {
		t.Errorf("expected %d instances, got %d", workers*opsPerWorker, got)
	}
}

func TestServiceUnderLoadWithCleanupRunning(t *testing.T) {
	service, err := discovery.New(memory.New(), discovery.Config{TTL: time.Hour, CleanupInterval: time.Millisecond, MaxInstances: 1 << 20}, discard)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	ctx := t.Context()
	cleanupCtx, stopCleanup := context.WithCancel(ctx)
	defer stopCleanup()
	done := startCleanup(cleanupCtx, service)

	runConcurrently(workers, func(worker int) {
		name := fmt.Sprintf("worker-%d", worker)
		own := mustRegister(t, service, name)

		for range opsPerWorker {
			if err := service.Heartbeat(ctx, own.ID, anyone); err != nil {
				t.Errorf("heartbeat: %v", err)
			}

			if got := mustDiscover(t, service, name); len(got) != 1 || got[0].ID != own.ID {
				t.Errorf("%s lost its own instance: %+v", name, got)
			}

			// Churn: instances that come and go while cleanup sweeps.
			churn := mustRegister(t, service, "churn")
			if err := service.Unregister(ctx, churn.ID, anyone); err != nil {
				t.Errorf("unregister churn: %v", err)
			}
		}
	})

	stopCleanup()

	if err := <-done; err != nil {
		t.Errorf("expected cleanup to stop cleanly, got %v", err)
	}

	if got := len(mustDiscover(t, service, "")); got != workers {
		t.Errorf("expected one instance per worker, got %d", got)
	}
}
