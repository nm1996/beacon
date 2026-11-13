package ratelimit

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestAllowSpendsTheBurstThenRefills(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		limiter := New(2, 3, 0)

		for attempt := range 3 {
			if ok, _ := limiter.Allow("orders"); !ok {
				t.Fatalf("expected attempt %d within the burst to be allowed", attempt+1)
			}
		}

		ok, wait := limiter.Allow("orders")
		if ok {
			t.Fatal("expected the attempt after the burst to be refused")
		}

		if wait != 500*time.Millisecond {
			t.Errorf("expected to wait 500ms for the next token at 2 per second, got %v", wait)
		}

		time.Sleep(wait)

		if ok, _ := limiter.Allow("orders"); !ok {
			t.Error("expected a token after waiting the reported time")
		}

		if ok, _ := limiter.Allow("orders"); ok {
			t.Error("expected only one token to have refilled")
		}
	})
}

func TestARefusedAttemptCostsNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		limiter := New(1, 1, 0)
		limiter.Allow("orders")

		// Hammering an empty bucket must not push the next token further
		// away.
		for range 100 {
			limiter.Allow("orders")
		}

		time.Sleep(time.Second)

		if ok, _ := limiter.Allow("orders"); !ok {
			t.Error("expected a token one second after the last allowed attempt")
		}
	})
}

func TestKeysHaveTheirOwnBuckets(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		limiter := New(1, 1, 0)

		if ok, _ := limiter.Allow("orders"); !ok {
			t.Fatal("expected orders to be allowed")
		}

		if ok, _ := limiter.Allow("orders"); ok {
			t.Fatal("expected orders to be refused after its burst")
		}

		if ok, _ := limiter.Allow("payments"); !ok {
			t.Error("expected payments not to be affected by orders")
		}
	})
}

func TestExhaustedAndTake(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		limiter := New(1, 2, 0)

		if exhausted, _ := limiter.Exhausted("10.0.0.1"); exhausted {
			t.Fatal("expected an unknown key not to be exhausted")
		}

		if limiter.Keys() != 0 {
			t.Fatal("expected Exhausted not to start tracking a key")
		}

		limiter.Take("10.0.0.1")

		if exhausted, _ := limiter.Exhausted("10.0.0.1"); exhausted {
			t.Fatal("expected one token to remain")
		}

		limiter.Take("10.0.0.1")

		exhausted, wait := limiter.Exhausted("10.0.0.1")
		if !exhausted || wait != time.Second {
			t.Fatalf("expected the key to be exhausted for 1s, got %v, %v", exhausted, wait)
		}

		time.Sleep(wait)

		if exhausted, _ := limiter.Exhausted("10.0.0.1"); exhausted {
			t.Error("expected the key to recover after the reported wait")
		}
	})
}

func TestZeroRateTurnsLimitingOff(t *testing.T) {
	limiter := New(0, 1, 0)
	if limiter != nil {
		t.Fatal("expected a nil limiter for a zero rate")
	}

	for range 1000 {
		if ok, _ := limiter.Allow("orders"); !ok {
			t.Fatal("expected a nil limiter to allow everything")
		}
	}

	if exhausted, _ := limiter.Exhausted("orders"); exhausted {
		t.Error("expected a nil limiter never to be exhausted")
	}

	limiter.Take("orders")
}

func TestKeysStayBounded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		limiter := New(1, 1, 10)

		// Ten clients use their only token, filling every place.
		for index := range 10 {
			limiter.Allow(fmt.Sprintf("busy-%d", index))
		}

		// More clients than places: they are allowed, but not tracked.
		for index := range 1000 {
			if ok, _ := limiter.Allow(fmt.Sprintf("new-%d", index)); !ok {
				t.Fatalf("expected an untracked client to be allowed")
			}
		}

		if got := limiter.Keys(); got != 10 {
			t.Fatalf("expected the limiter to track at most 10 keys, got %d", got)
		}

		// The busy clients are still limited.
		if ok, _ := limiter.Allow("busy-0"); ok {
			t.Error("expected a tracked client to stay limited while the limiter is full")
		}

		// Once their buckets refill they are dropped to make room.
		time.Sleep(time.Second)

		limiter.Allow("later")

		if ok, _ := limiter.Allow("later"); ok {
			t.Error("expected the new client to be tracked, and limited, once full buckets were dropped")
		}

		if got := limiter.Keys(); got != 1 {
			t.Errorf("expected only the new client to be tracked after the sweep, got %d", got)
		}
	})
}

func TestConcurrentAllowNeverExceedsTheBurst(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		limiter := New(1, 50, 0)

		var (
			group   sync.WaitGroup
			allowed atomic.Int64
		)

		for range 16 {
			group.Go(func() {
				for range 20 {
					if ok, _ := limiter.Allow("orders"); ok {
						allowed.Add(1)
					}
				}
			})
		}

		group.Wait()

		if got := allowed.Load(); got != 50 {
			t.Errorf("expected exactly the burst of 50 to be allowed at once, got %d", got)
		}
	})
}
