package heartbeats

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nm1996/beacon/internal/discovery"
	"github.com/nm1996/beacon/internal/registry/memory"
)

// memoryWriter writes heartbeats into a memory registry, one Touch each,
// and records every batch it gets.
type memoryWriter struct {
	registry *memory.MemoryRegistry

	mu      sync.Mutex
	batches []int
	fail    error
}

// TouchMany touches each instance and reports those not found.
func (writer *memoryWriter) TouchMany(ctx context.Context, heartbeats map[string]time.Time) ([]string, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()

	if writer.fail != nil {
		return nil, writer.fail
	}

	writer.batches = append(writer.batches, len(heartbeats))

	var missing []string

	for id, at := range heartbeats {
		if err := writer.registry.Touch(ctx, id, at, discovery.OwnerCheck{Any: true}); errors.Is(err, discovery.ErrInstanceNotFound) {
			missing = append(missing, id)
		}
	}

	return missing, nil
}

// fixture returns a batching registry over a memory registry holding one
// instance "a", owned by "alice", and the writer.
func fixture(t *testing.T) (*Registry, *memory.MemoryRegistry, *memoryWriter) {
	t.Helper()

	backend := memory.New()
	writer := &memoryWriter{registry: backend}
	batched := New(backend, writer, time.Second, slog.New(slog.DiscardHandler))

	create(t, backend, "a", "alice")

	return batched, backend, writer
}

// create stores an instance with id and owner, last seen at the start.
func create(t *testing.T, registry discovery.Registry, id, owner string) {
	t.Helper()

	start := time.Now()
	instance := discovery.Instance{ID: id, Name: "orders", Address: "10.0.0.1", Port: 8081, Owner: owner, RegisteredAt: start, LastHeartbeat: start}

	if err := registry.Create(t.Context(), instance, discovery.Limits{Total: math.MaxInt}); err != nil {
		t.Fatal(err)
	}
}

// lastHeartbeat returns what backend holds for id.
func lastHeartbeat(t *testing.T, backend discovery.Registry, id string) time.Time {
	t.Helper()

	instance, err := backend.Get(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}

	return instance.LastHeartbeat
}

// alice may change her own instances.
var alice = discovery.OwnerCheck{Owner: "alice"}

func TestHeartbeatsAreBufferedAfterTheFirst(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		batched, backend, writer := fixture(t)
		ctx := t.Context()

		first := time.Now().Add(time.Second)
		if err := batched.Touch(ctx, "a", first, alice); err != nil {
			t.Fatal(err)
		}

		if !lastHeartbeat(t, backend, "a").Equal(first) {
			t.Fatal("expected the first heartbeat to reach the backend at once")
		}

		later := first.Add(10 * time.Second)
		if err := batched.Touch(ctx, "a", later, alice); err != nil {
			t.Fatal(err)
		}

		if !lastHeartbeat(t, backend, "a").Equal(first) {
			t.Error("expected later heartbeats to wait for a flush")
		}

		if err := batched.Flush(ctx); err != nil {
			t.Fatal(err)
		}

		if !lastHeartbeat(t, backend, "a").Equal(later) || len(writer.batches) != 1 {
			t.Errorf("expected one batch to write the latest heartbeat, got %v", writer.batches)
		}
	})
}

func TestBufferedHeartbeatsStillCheckTheOwner(t *testing.T) {
	batched, _, _ := fixture(t)
	ctx := t.Context()

	if err := batched.Touch(ctx, "a", time.Now(), alice); err != nil {
		t.Fatal(err)
	}

	if err := batched.Touch(ctx, "a", time.Now(), discovery.OwnerCheck{Owner: "mallory"}); !errors.Is(err, discovery.ErrInstanceNotOwned) {
		t.Errorf("expected another owner to be refused from the buffer, got %v", err)
	}

	if err := batched.Touch(ctx, "a", time.Now(), discovery.OwnerCheck{Owner: "mallory", Any: true}); err != nil {
		t.Errorf("expected a manager to pass, got %v", err)
	}
}

func TestUnknownInstancesAreAnsweredAtOnce(t *testing.T) {
	batched, _, _ := fixture(t)

	if err := batched.Touch(t.Context(), "missing", time.Now(), alice); !errors.Is(err, discovery.ErrInstanceNotFound) {
		t.Errorf("expected ErrInstanceNotFound, got %v", err)
	}
}

func TestInstancesRemovedElsewhereAreForgottenOnFlush(t *testing.T) {
	batched, backend, _ := fixture(t)
	ctx := t.Context()

	if err := batched.Touch(ctx, "a", time.Now(), alice); err != nil {
		t.Fatal(err)
	}

	// Another node removes it; this one still buffers its heartbeats.
	if err := backend.Delete(ctx, "a", discovery.OwnerCheck{Any: true}); err != nil {
		t.Fatal(err)
	}

	if err := batched.Touch(ctx, "a", time.Now(), alice); err != nil {
		t.Fatalf("expected the buffer to accept it until the next flush, got %v", err)
	}

	if err := batched.Flush(ctx); err != nil {
		t.Fatal(err)
	}

	if err := batched.Touch(ctx, "a", time.Now(), alice); !errors.Is(err, discovery.ErrInstanceNotFound) {
		t.Errorf("expected ErrInstanceNotFound once the flush found it gone, got %v", err)
	}
}

func TestDeleteThroughTheNodeForgetsAtOnce(t *testing.T) {
	batched, _, _ := fixture(t)
	ctx := t.Context()

	if err := batched.Touch(ctx, "a", time.Now(), alice); err != nil {
		t.Fatal(err)
	}

	if err := batched.Delete(ctx, "a", alice); err != nil {
		t.Fatal(err)
	}

	if err := batched.Touch(ctx, "a", time.Now(), alice); !errors.Is(err, discovery.ErrInstanceNotFound) {
		t.Errorf("expected ErrInstanceNotFound right after a delete, got %v", err)
	}
}

func TestAFailedFlushKeepsTheHeartbeats(t *testing.T) {
	batched, backend, writer := fixture(t)
	ctx := t.Context()

	if err := batched.Touch(ctx, "a", time.Now(), alice); err != nil {
		t.Fatal(err)
	}

	later := time.Now().Add(time.Minute)
	if err := batched.Touch(ctx, "a", later, alice); err != nil {
		t.Fatal(err)
	}

	writer.fail = errors.New("database down")

	if err := batched.Flush(ctx); err == nil {
		t.Fatal("expected the flush to fail")
	}

	writer.fail = nil

	if err := batched.Flush(ctx); err != nil {
		t.Fatal(err)
	}

	if !lastHeartbeat(t, backend, "a").Equal(later) {
		t.Error("expected the heartbeat kept from the failed flush to be written by the next")
	}
}

func TestCleanupSeesTheBufferedHeartbeats(t *testing.T) {
	batched, _, _ := fixture(t)
	ctx := t.Context()

	if err := batched.Touch(ctx, "a", time.Now(), alice); err != nil {
		t.Fatal(err)
	}

	fresh := time.Now().Add(time.Hour)
	if err := batched.Touch(ctx, "a", fresh, alice); err != nil {
		t.Fatal(err)
	}

	removed, err := batched.DeleteStale(ctx, fresh.Add(-time.Minute))
	if err != nil || removed != 0 {
		t.Errorf("expected the buffered heartbeat to keep the instance, got %d removed, %v", removed, err)
	}
}

func TestLargeFlushesAreChunked(t *testing.T) {
	batched, backend, writer := fixture(t)
	ctx := t.Context()

	for index := range 2500 {
		id := "bulk-" + string(rune('a'+index%26)) + "-" + time.Duration(index).String()
		create(t, backend, id, "alice")

		if err := batched.Touch(ctx, id, time.Now(), alice); err != nil {
			t.Fatal(err)
		}

		if err := batched.Touch(ctx, id, time.Now(), alice); err != nil {
			t.Fatal(err)
		}
	}

	if err := batched.Flush(ctx); err != nil {
		t.Fatal(err)
	}

	total := 0
	for _, size := range writer.batches {
		if size > chunkSize {
			t.Errorf("expected chunks of at most %d, got %d", chunkSize, size)
		}

		total += size
	}

	if len(writer.batches) != 3 || total != 2500 {
		t.Errorf("expected 2500 heartbeats in 3 chunks, got %v", writer.batches)
	}
}

func TestKnownInstancesAreCheckedAgainAfterAWhile(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		batched, backend, _ := fixture(t)
		ctx := t.Context()

		if err := batched.Touch(ctx, "a", time.Now(), alice); err != nil {
			t.Fatal(err)
		}

		// Removed and registered anew under the same ID by someone else,
		// without the buffer seeing a flush find it missing.
		if err := backend.Delete(ctx, "a", discovery.OwnerCheck{Any: true}); err != nil {
			t.Fatal(err)
		}

		create(t, backend, "a", "bob")

		time.Sleep(recheckAfter)

		if err := batched.Touch(ctx, "a", time.Now(), alice); !errors.Is(err, discovery.ErrInstanceNotOwned) {
			t.Errorf("expected the new owner to be noticed after %s, got %v", recheckAfter, err)
		}
	})
}

func TestRunFlushesOnAnIntervalAndOnStop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		batched, backend, writer := fixture(t)
		ctx, cancel := context.WithCancel(t.Context())

		done := make(chan struct{})

		go func() {
			defer close(done)
			batched.Run(ctx)
		}()

		if err := batched.Touch(t.Context(), "a", time.Now(), alice); err != nil {
			t.Fatal(err)
		}

		buffered := time.Now().Add(time.Minute)
		if err := batched.Touch(t.Context(), "a", buffered, alice); err != nil {
			t.Fatal(err)
		}

		time.Sleep(time.Second)
		synctest.Wait()

		if !lastHeartbeat(t, backend, "a").Equal(buffered) {
			t.Error("expected a flush within the interval")
		}

		last := buffered.Add(time.Minute)
		if err := batched.Touch(t.Context(), "a", last, alice); err != nil {
			t.Fatal(err)
		}

		cancel()
		<-done

		if !lastHeartbeat(t, backend, "a").Equal(last) {
			t.Errorf("expected stopping to write what was buffered, batches %v", writer.batches)
		}
	})
}
