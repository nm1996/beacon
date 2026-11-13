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

func TestSpendTakesTheReservedToken(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		limiter := New(1, 2, 0)

		for attempt := range 2 {
			reservation, ok, _ := limiter.Reserve("10.0.0.1")
			if !ok {
				t.Fatalf("attempt %d: expected a reservation within the burst", attempt+1)
			}

			reservation.Spend()
		}

		_, ok, wait := limiter.Reserve("10.0.0.1")
		if ok || wait != time.Second {
			t.Fatalf("expected no reservation for 1s once both tokens were spent, got %v, %v", ok, wait)
		}

		time.Sleep(wait)

		reservation, ok, _ := limiter.Reserve("10.0.0.1")
		if !ok {
			t.Fatal("expected a reservation after the reported wait")
		}

		reservation.Release()
	})
}

func TestReleaseGivesTheTokenBack(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		limiter := New(1, 2, 0)

		// Far more attempts than tokens, one after another: none counts.
		for attempt := range 100 {
			reservation, ok, _ := limiter.Reserve("10.0.0.1")
			if !ok {
				t.Fatalf("attempt %d: expected released reservations to cost nothing", attempt+1)
			}

			reservation.Release()
		}

		if ok, _ := limiter.Allow("10.0.0.1"); !ok {
			t.Fatal("expected the first token to be left")
		}

		if ok, _ := limiter.Allow("10.0.0.1"); !ok {
			t.Fatal("expected the whole burst to be left after releases")
		}
	})
}

func TestReservationsInProgressHoldTheirTokens(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		limiter := New(1, 3, 0)

		held := make([]Reservation, 0, 3)

		for range 3 {
			reservation, ok, _ := limiter.Reserve("10.0.0.1")
			if !ok {
				t.Fatal("expected a reservation within the burst")
			}

			held = append(held, reservation)
		}

		// Nothing is spent yet, but every token is promised.
		if _, ok, wait := limiter.Reserve("10.0.0.1"); ok || wait != time.Second {
			t.Fatalf("expected no reservation while the burst is in progress, got %v, %v", ok, wait)
		}

		if ok, _ := limiter.Allow("10.0.0.1"); ok {
			t.Fatal("expected Allow not to take a reserved token")
		}

		held[0].Release()

		reservation, ok, _ := limiter.Reserve("10.0.0.1")
		if !ok {
			t.Fatal("expected a released token to be reservable again")
		}

		reservation.Release()

		for _, reservation := range held[1:] {
			reservation.Release()
		}
	})
}

func TestConcurrentReservationsNeverExceedTheBurst(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		limiter := New(1, 20, 0)
		gate := make(chan struct{})

		var (
			group    sync.WaitGroup
			reserved atomic.Int64
		)

		for range 100 {
			group.Go(func() {
				reservation, ok, _ := limiter.Reserve("10.0.0.1")
				if !ok {
					return
				}

				reserved.Add(1)
				<-gate
				reservation.Spend()
			})
		}

		// Every attempt has either reserved and is waiting, or was refused.
		synctest.Wait()

		if got := reserved.Load(); got != 20 {
			t.Errorf("expected exactly the burst of 20 attempts at once, got %d", got)
		}

		close(gate)
		group.Wait()

		if _, ok, _ := limiter.Reserve("10.0.0.1"); ok {
			t.Error("expected the spent burst to leave nothing to reserve")
		}
	})
}

func TestBucketsWithReservationsAreNotDropped(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		limiter := New(1, 2, 1)

		// The attacker has failed once, so it is tracked, and has an
		// attempt in progress.
		failed, _, _ := limiter.Reserve("attacker")
		failed.Spend()

		inProgress, ok, _ := limiter.Reserve("attacker")
		if !ok {
			t.Fatal("expected a reservation")
		}

		// The bucket refills while the attempt runs, then a new key makes the
		// full limiter sweep. The attempt in progress must keep the bucket.
		time.Sleep(time.Second)
		limiter.Allow("someone-else")

		inProgress.Spend()

		if reservation, ok, _ := limiter.Reserve("attacker"); !ok {
			t.Fatal("expected one token left")
		} else {
			defer reservation.Release()
		}

		if _, ok, _ := limiter.Reserve("attacker"); ok {
			t.Error("expected the spent token to still count")
		}
	})
}

func TestReleasedKeysAreNotTracked(t *testing.T) {
	limiter := New(1, 1, 0)

	for range 100 {
		reservation, _, _ := limiter.Reserve("10.0.0.1")
		reservation.Release()
	}

	if got := limiter.Keys(); got != 0 {
		t.Errorf("expected keys that never spend not to be tracked, got %d", got)
	}

	if got := len(limiter.pending); got != 0 {
		t.Errorf("expected nothing left in progress, got %d keys", got)
	}
}

// holdMany reserves for count distinct keys at once and returns the
// reservations, failing the test if any is refused.
func holdMany(t *testing.T, limiter *Limiter, prefix string, count int) []Reservation {
	t.Helper()

	held := make([]Reservation, 0, count)

	for index := range count {
		reservation, ok, _ := limiter.Reserve(fmt.Sprintf("%s-%d", prefix, index))
		if !ok {
			t.Fatalf("expected key %d to be allowed", index)
		}

		held = append(held, reservation)
	}

	return held
}

func TestKeysInProgressStayBounded(t *testing.T) {
	limiter := New(1, 1, 10)

	// A flood of distinct clients, all in the middle of authenticating.
	held := holdMany(t, limiter, "flood", 1000)

	if got := len(limiter.pending); got != 10 {
		t.Fatalf("expected at most 10 keys counted in progress, got %d", got)
	}

	// Ending an attempt that was not counted leaves the counted ones alone.
	held[999].Release()

	if got := len(limiter.pending); got != 10 {
		t.Fatalf("expected an uncounted release not to touch the counts, got %d", got)
	}

	for _, reservation := range held[:999] {
		reservation.Release()
	}

	if got := len(limiter.pending); got != 0 {
		t.Errorf("expected nothing in progress after every release, got %d", got)
	}
}

func TestTrackedKeysStayProtectedWhileKeysInProgressAreFull(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		limiter := New(1, 3, 10)

		// The attacker has failed once, so it is tracked, with 2 tokens left.
		failed, _, _ := limiter.Reserve("attacker")
		failed.Spend()

		flood := holdMany(t, limiter, "flood", 1000)

		first, ok, _ := limiter.Reserve("attacker")
		if !ok {
			t.Fatal("expected the attacker's first attempt to be allowed")
		}

		second, ok, _ := limiter.Reserve("attacker")
		if !ok {
			t.Fatal("expected the attacker's second attempt to be allowed")
		}

		if _, ok, _ := limiter.Reserve("attacker"); ok {
			t.Fatal("expected a tracked key to stay limited however many other keys are in progress")
		}

		first.Spend()
		second.Spend()

		if _, ok, _ := limiter.Reserve("attacker"); ok {
			t.Error("expected the spent burst to leave nothing to reserve")
		}

		for _, reservation := range flood {
			reservation.Release()
		}
	})
}

func TestUncountedAttemptsStillCountFailures(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		limiter := New(1, 1, 10)
		flood := holdMany(t, limiter, "flood", 10)

		uncounted, ok, _ := limiter.Reserve("newcomer")
		if !ok {
			t.Fatal("expected an untracked key to be allowed when no more can be counted")
		}

		uncounted.Spend()

		for _, reservation := range flood {
			reservation.Release()
		}

		if _, ok, _ := limiter.Reserve("newcomer"); ok {
			t.Error("expected the failure to be counted although its attempt was not")
		}
	})
}

func TestUncountedAttemptsLeaveLaterCountsAlone(t *testing.T) {
	limiter := New(1, 2, 10)
	flood := holdMany(t, limiter, "flood", 10)

	uncounted, _, _ := limiter.Reserve("newcomer")

	// Room frees up, and the same key's next attempt is counted.
	flood[0].Release()

	counted, ok, _ := limiter.Reserve("newcomer")
	if !ok {
		t.Fatal("expected the second attempt to be allowed")
	}

	// The first attempt ends; it must not end the second one's count.
	uncounted.Release()

	third, ok, _ := limiter.Reserve("newcomer")
	if !ok {
		t.Fatal("expected one of the two tokens to be free")
	}

	if _, ok, _ := limiter.Reserve("newcomer"); ok {
		t.Error("expected the counted attempts to hold both tokens")
	}

	counted.Release()
	third.Release()

	for _, reservation := range flood[1:] {
		reservation.Release()
	}
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

	for range 1000 {
		reservation, ok, _ := limiter.Reserve("orders")
		if !ok {
			t.Fatal("expected a nil limiter to reserve everything")
		}

		reservation.Spend()
	}
}

func TestUntrackedKeysReserveNothing(t *testing.T) {
	limiter := New(1, 1, 1)
	limiter.Allow("busy")

	for range 1000 {
		reservation, ok, _ := limiter.Reserve("new")
		if !ok {
			t.Fatal("expected an untracked key to be allowed")
		}

		reservation.Spend()
	}

	if got := limiter.Keys(); got != 1 {
		t.Errorf("expected the full limiter to track only the first key, got %d", got)
	}
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
