package cluster

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// counts records the counts a Membership reports.
type counts struct {
	mu     sync.Mutex
	values []int
}

// record appends live.
func (counts *counts) record(live int) {
	counts.mu.Lock()
	defer counts.mu.Unlock()

	counts.values = append(counts.values, live)
}

// last returns the latest count, or 0.
func (counts *counts) last() int {
	counts.mu.Lock()
	defer counts.mu.Unlock()

	if len(counts.values) == 0 {
		return 0
	}

	return counts.values[len(counts.values)-1]
}

// start joins a node called id to store and runs its membership until the
// returned function is called.
func start(t *testing.T, store Store, id string, seen *counts) (*Membership, context.CancelFunc) {
	t.Helper()

	membership := New(store, Node{ID: id, StartedAt: time.Now()}, slog.New(slog.DiscardHandler), seen.record)
	if err := membership.Join(t.Context()); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		defer close(done)
		membership.Run(ctx)
	}()

	return membership, func() {
		cancel()
		<-done
	}
}

func TestMembershipFollowsNodesJoiningAndLeaving(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := NewLocal()

		var first, second counts

		_, stopFirst := start(t, store, "a", &first)
		defer stopFirst()

		if first.last() != 1 {
			t.Fatalf("expected the first node to count itself, got %v", first.values)
		}

		membership, stopSecond := start(t, store, "b", &second)

		if second.last() != 2 {
			t.Errorf("expected the second node to see both at once, got %v", second.values)
		}

		time.Sleep(Interval)
		synctest.Wait()

		if first.last() != 2 {
			t.Errorf("expected the first node to see the second within an interval, got %v", first.values)
		}

		nodes, err := membership.Nodes(t.Context())
		if err != nil || len(nodes) != 2 || nodes[0].ID != "a" || nodes[1].ID != "b" {
			t.Errorf("expected nodes a and b, got %+v, %v", nodes, err)
		}

		// A node that shuts down leaves at once.
		stopSecond()

		time.Sleep(Interval)
		synctest.Wait()

		if first.last() != 1 {
			t.Errorf("expected the first node alone again within an interval, got %v", first.values)
		}
	})
}

func TestACrashedNodeIsForgottenAfterLiveFor(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := NewLocal()

		var seen counts

		_, stop := start(t, store, "a", &seen)
		defer stop()

		// A node that recorded itself once and then died without leaving.
		if err := store.Beat(t.Context(), Node{ID: "crashed", LastSeen: time.Now()}); err != nil {
			t.Fatal(err)
		}

		time.Sleep(Interval)
		synctest.Wait()

		if seen.last() != 2 {
			t.Fatalf("expected the crashed node to count while it is recent, got %v", seen.values)
		}

		time.Sleep(LiveFor)
		synctest.Wait()

		if seen.last() != 1 {
			t.Errorf("expected the crashed node to stop counting after %s, got %v", LiveFor, seen.values)
		}
	})
}
