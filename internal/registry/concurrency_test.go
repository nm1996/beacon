package registry

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"
	"sync"
	"testing"
	"time"
)

// These tests hammer one registry from many goroutines at once. They check
// the results, but their main job is to give the race detector overlapping
// reads and writes to inspect, so run them with go test -race.

const (
	workers          = 32
	opsPerWorker     = 200
	servicesPerGroup = 8
)

// runConcurrently starts count goroutines that each call work with their
// index, releases them at the same moment so their operations overlap, and
// waits for all of them to finish.
func runConcurrently(count int, work func(worker int)) {
	var wg sync.WaitGroup
	start := make(chan struct{})

	for worker := range count {
		wg.Go(func() {
			<-start
			work(worker)
		})
	}

	close(start)
	wg.Wait()
}

func TestConcurrentRegister(t *testing.T) {
	r := New()

	ids := make([][]string, workers)

	runConcurrently(workers, func(worker int) {
		for op := range opsPerWorker {
			id, err := r.Register(newService(fmt.Sprintf("service-%d", op%servicesPerGroup)))
			if err != nil {
				t.Errorf("worker %d: register: %v", worker, err)
				return
			}

			ids[worker] = append(ids[worker], id)
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

	if got := len(r.GetServices("")); got != workers*opsPerWorker {
		t.Errorf("expected %d services, got %d", workers*opsPerWorker, got)
	}

	if got := len(r.GetServices("service-0")); got != workers*opsPerWorker/servicesPerGroup {
		t.Errorf("expected %d instances of service-0, got %d", workers*opsPerWorker/servicesPerGroup, got)
	}
}

func TestConcurrentHeartbeatAndLookup(t *testing.T) {
	r := New()

	ids := make([]string, servicesPerGroup)
	for index := range ids {
		ids[index] = mustRegister(t, r, newService("orders"))
	}

	runConcurrently(workers, func(worker int) {
		for op := range opsPerWorker {
			// Half the workers send heartbeats, the other half look services up.
			if worker%2 == 0 {
				if err := r.Heartbeat(ids[op%len(ids)]); err != nil {
					t.Errorf("heartbeat: %v", err)
				}

				continue
			}

			services := r.GetServices("orders")
			if len(services) != len(ids) {
				t.Errorf("expected %d services, got %d", len(ids), len(services))
				continue
			}

			for _, service := range services {
				if service.Name != "orders" || service.LastHeartbeat.Before(service.RegisteredAt) {
					t.Errorf("lookup returned an inconsistent service: %+v", service)
				}
			}
		}
	})
}

func TestConcurrentCleanupAndLookup(t *testing.T) {
	r := New()

	activeIDs := make([]string, servicesPerGroup)
	for index := range activeIDs {
		activeIDs[index] = mustRegister(t, r, newService("active"))
	}

	staleIDs := make([]string, servicesPerGroup)
	for index := range staleIDs {
		staleIDs[index] = mustRegister(t, r, newService("stale"))
	}

	r.mu.Lock()
	for _, id := range staleIDs {
		r.services[id].LastHeartbeat = time.Now().Add(-2 * time.Hour)
	}
	r.mu.Unlock()

	runConcurrently(workers, func(worker int) {
		for op := range opsPerWorker {
			switch worker % 4 {
			case 0:
				r.Cleanup(time.Hour)
			case 1:
				for _, service := range r.GetServices("") {
					if service.Name == "" || service.ID == "" {
						t.Errorf("lookup returned a half-written service: %+v", service)
					}
				}
			case 2:
				if err := r.Heartbeat(activeIDs[op%len(activeIDs)]); err != nil {
					t.Errorf("heartbeat on active service: %v", err)
				}
			case 3:
				// Churn: services that come and go while cleanup runs.
				id := mustRegister(t, r, newService("churn"))
				if err := r.Unregister(id); err != nil {
					t.Errorf("unregister churn service: %v", err)
				}
			}
		}
	})

	if got := len(r.GetServices("stale")); got != 0 {
		t.Errorf("expected stale services to be removed, %d remain", got)
	}

	if got := len(r.GetServices("active")); got != len(activeIDs) {
		t.Errorf("expected %d active services to survive cleanup, got %d", len(activeIDs), got)
	}

	if got := len(r.GetServices("churn")); got != 0 {
		t.Errorf("expected every churn service to be unregistered, %d remain", got)
	}
}

func TestRunCleanupAlongsideTraffic(t *testing.T) {
	r := New()

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)

	// A 1ms interval makes the background worker tick many times while the
	// traffic below runs. The TTL is long, so it never removes anything here.
	go func() {
		done <- r.RunCleanup(ctx, CleanupConfig{Interval: time.Millisecond, TTL: time.Hour}, slog.New(slog.DiscardHandler))
	}()

	runConcurrently(workers, func(worker int) {
		id := mustRegister(t, r, newService(fmt.Sprintf("worker-%d", worker)))

		for range opsPerWorker {
			if err := r.Heartbeat(id); err != nil {
				t.Errorf("heartbeat: %v", err)
			}

			if len(r.GetServices(fmt.Sprintf("worker-%d", worker))) != 1 {
				t.Errorf("worker %d lost its own service", worker)
			}
		}
	})

	cancel()

	if err := <-done; err != nil {
		t.Errorf("expected cleanup to stop cleanly, got %v", err)
	}

	if got := len(r.GetServices("")); got != workers {
		t.Errorf("expected %d services, got %d", workers, got)
	}
}

func TestReturnedServicesCanBeChangedConcurrently(t *testing.T) {
	r := New()
	id := mustRegister(t, r, newService("orders"))

	// Callers scribble over the services they get back while others keep
	// reading and heartbeating. If GetServices handed out the registry's own
	// pointers, the race detector would report it and the checks below
	// would see the scribbles.
	runConcurrently(workers, func(worker int) {
		for range opsPerWorker {
			for _, service := range r.GetServices("orders") {
				service.Name = fmt.Sprintf("changed-by-%d", worker)
				service.Port = -1
				service.LastHeartbeat = time.Time{}
			}

			if err := r.Heartbeat(id); err != nil {
				t.Errorf("heartbeat: %v", err)
			}
		}
	})

	services := r.GetServices("orders")
	if len(services) != 1 {
		t.Fatalf("expected the service under its original name, got %d services", len(services))
	}

	if services[0].Port != 8080 || services[0].LastHeartbeat.IsZero() {
		t.Errorf("registry state was changed through a returned service: %+v", services[0])
	}
}

func TestServiceHasNoSharedReferenceFields(t *testing.T) {
	// GetServices and Register protect the registry with a shallow copy
	// (copy := *service). That is only enough while Service holds plain
	// values. A slice, map or pointer field would be shared between the copy
	// and the registry, so adding one must come with a deep copy and an
	// update to this test.
	serviceType := reflect.TypeFor[Service]()

	for field := range serviceType.Fields() {
		switch field.Type.Kind() {
		case reflect.Slice, reflect.Map, reflect.Pointer, reflect.Chan, reflect.Func, reflect.Interface:
			t.Errorf("Service.%s is a %s; shallow copies would share it with the registry", field.Name, field.Type.Kind())
		}
	}
}
