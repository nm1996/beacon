// Package ratelimit keeps one token bucket per key, such as a caller's name
// or a client's network, so one noisy client cannot use up what everyone
// else is allowed.
//
// It is local to one Beacon process, which is all a single-node registry
// needs.
package ratelimit

import (
	"math"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const (
	// DefaultMaxKeys is how many keys a Limiter tracks at most when New is
	// given no other number. Each takes well under a kilobyte.
	DefaultMaxKeys = 65536

	// sweepInterval is how often, at most, a full Limiter looks for buckets
	// to drop. Looking visits every bucket, so a stream of new keys must not
	// trigger it on every call.
	sweepInterval = time.Second
)

// Limiter is a set of token buckets, one per key, all with the same rate
// and burst. A nil *Limiter allows everything, which is how a limit is
// turned off. It is safe for concurrent use.
//
// A bucket that has refilled completely behaves exactly like a new one, so
// Limiter drops full buckets when it reaches its key limit instead of
// growing. If every bucket is still in use, keys it cannot track are
// allowed: the limit then stops protecting against more distinct clients
// than that, rather than turning legitimate new clients away.
//
// The same holds for attempts in progress (see Reserve): an untracked key
// only has its attempts counted while fewer than maxKeys keys have attempts
// in progress, so a flood of requests from distinct clients cannot grow the
// Limiter either. Keys it already tracks are always counted, and so stay
// protected; at most 2 * maxKeys keys have attempts in progress.
type Limiter struct {
	limit   rate.Limit
	burst   int
	maxKeys int

	mu      sync.Mutex
	buckets map[string]*rate.Limiter
	pending map[string]int
	sweptAt time.Time
}

// New returns a Limiter that allows each key perSecond events on average and
// up to burst at once, tracking at most maxKeys keys. It returns nil, which
// allows everything, when perSecond is zero. maxKeys of zero means
// DefaultMaxKeys.
func New(perSecond float64, burst, maxKeys int) *Limiter {
	if perSecond == 0 {
		return nil
	}

	if maxKeys == 0 {
		maxKeys = DefaultMaxKeys
	}

	return &Limiter{
		limit:   rate.Limit(perSecond),
		burst:   burst,
		maxKeys: maxKeys,
		buckets: make(map[string]*rate.Limiter),
		pending: make(map[string]int),
	}
}

// Allow takes one token from key's bucket. If the bucket is empty, or every
// token left is reserved, it takes nothing and returns false with how long
// until a token is available.
func (limiter *Limiter) Allow(key string) (bool, time.Duration) {
	if limiter == nil {
		return true, 0
	}

	limiter.mu.Lock()
	defer limiter.mu.Unlock()

	bucket := limiter.bucket(key)
	if bucket == nil {
		return true, 0
	}

	now := time.Now()
	if available := bucket.TokensAt(now) - float64(limiter.pending[key]); available < 1 {
		return false, limiter.waitFor(available)
	}

	bucket.AllowN(now, 1)

	return true, 0
}

// Reservation is a token Reserve has set aside for an attempt in progress.
// Once the attempt is over, call exactly one of Spend, if it counts against
// the limit, or Release, if it does not. The zero Reservation, which Reserve
// returns when nothing is limited, does nothing.
type Reservation struct {
	limiter *Limiter
	key     string
	// held is whether the attempt is counted as in progress. It is not when
	// too many untracked keys already have attempts in progress.
	held bool
}

// Reserve sets aside one of key's tokens for an attempt whose outcome
// decides whether it counts, such as an authentication that may fail. If
// none is left it returns false with how long until one is, and the attempt
// should not be made.
//
// Checking and reserving are one step, so however many attempts run at
// once, no more of them start than there are tokens. Attempts in progress
// hold their tokens until they are spent or released. A key is only tracked
// once a token is spent, so keys whose attempts are always released cost
// nothing between attempts.
//
// When maxKeys keys already have attempts in progress, an attempt for an
// untracked key that has none is allowed without being counted, as keys the
// Limiter has no room for are. Spending it still counts the failure.
func (limiter *Limiter) Reserve(key string) (Reservation, bool, time.Duration) {
	if limiter == nil {
		return Reservation{}, true, 0
	}

	limiter.mu.Lock()
	defer limiter.mu.Unlock()

	available := float64(limiter.burst)

	bucket, tracked := limiter.buckets[key]
	if tracked {
		available = bucket.TokensAt(time.Now())
	}

	inProgress, counted := limiter.pending[key]

	available -= float64(inProgress)
	if available < 1 {
		return Reservation{}, false, limiter.waitFor(available)
	}

	if !counted && !tracked && len(limiter.pending) >= limiter.maxKeys {
		return Reservation{limiter: limiter, key: key}, true, 0
	}

	limiter.pending[key]++

	return Reservation{limiter: limiter, key: key, held: true}, true, 0
}

// Spend takes the reserved token: the attempt counts against the limit.
func (reservation Reservation) Spend() {
	reservation.settle(true)
}

// Release gives the reserved token back: the attempt does not count.
func (reservation Reservation) Release() {
	reservation.settle(false)
}

// settle ends the reservation, taking its token if spend is true.
func (reservation Reservation) settle(spend bool) {
	limiter := reservation.limiter
	if limiter == nil {
		return
	}

	limiter.mu.Lock()
	defer limiter.mu.Unlock()

	if !reservation.held {
		if spend {
			limiter.spend(reservation.key)
		}

		return
	}

	if limiter.pending[reservation.key]--; limiter.pending[reservation.key] <= 0 {
		delete(limiter.pending, reservation.key)
	}

	if spend {
		limiter.spend(reservation.key)
	}
}

// spend takes one token from key's bucket, tracking key if there is room.
// The caller holds mu.
func (limiter *Limiter) spend(key string) {
	if bucket := limiter.bucket(key); bucket != nil {
		bucket.AllowN(time.Now(), 1)
	}
}

// waitFor returns how long until a key with available unreserved tokens
// has one, if no reservation is released meanwhile.
func (limiter *Limiter) waitFor(available float64) time.Duration {
	if limiter.burst < 1 {
		return math.MaxInt64
	}

	return time.Duration((1 - available) / float64(limiter.limit) * float64(time.Second))
}

// Keys reports how many keys the limiter tracks.
func (limiter *Limiter) Keys() int {
	if limiter == nil {
		return 0
	}

	limiter.mu.Lock()
	defer limiter.mu.Unlock()

	return len(limiter.buckets)
}

// bucket returns key's bucket, creating it if there is room, or nil if
// there is none. The caller holds mu.
func (limiter *Limiter) bucket(key string) *rate.Limiter {
	if bucket, tracked := limiter.buckets[key]; tracked {
		return bucket
	}

	if len(limiter.buckets) >= limiter.maxKeys {
		now := time.Now()
		if now.Sub(limiter.sweptAt) < sweepInterval {
			return nil
		}

		limiter.sweptAt = now
		limiter.dropFull(now)

		if len(limiter.buckets) >= limiter.maxKeys {
			return nil
		}
	}

	bucket := rate.NewLimiter(limiter.limit, limiter.burst)
	limiter.buckets[key] = bucket

	return bucket
}

// dropFull forgets every bucket that has refilled completely and has no
// attempt in progress; a new bucket for the same key would be identical. The
// caller holds mu.
func (limiter *Limiter) dropFull(now time.Time) {
	for key, bucket := range limiter.buckets {
		if limiter.pending[key] == 0 && bucket.TokensAt(now) >= float64(limiter.burst) {
			delete(limiter.buckets, key)
		}
	}
}
