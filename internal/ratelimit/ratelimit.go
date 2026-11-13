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
type Limiter struct {
	limit   rate.Limit
	burst   int
	maxKeys int

	mu      sync.Mutex
	buckets map[string]*rate.Limiter
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
	}
}

// Allow takes one token from key's bucket. If the bucket is empty it takes
// nothing and returns false with how long until a token is available.
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

	reservation := bucket.ReserveN(now, 1)
	if !reservation.OK() {
		return false, math.MaxInt64
	}

	if wait := reservation.DelayFrom(now); wait > 0 {
		reservation.CancelAt(now)
		return false, wait
	}

	return true, 0
}

// Exhausted reports whether key's bucket is empty, without taking a token,
// and if so how long until it is not. Use it with Take to refuse a key only
// after it has spent its tokens on something else, such as failed attempts.
func (limiter *Limiter) Exhausted(key string) (bool, time.Duration) {
	if limiter == nil {
		return false, 0
	}

	limiter.mu.Lock()
	defer limiter.mu.Unlock()

	bucket, tracked := limiter.buckets[key]
	if !tracked {
		return false, 0
	}

	now := time.Now()
	if tokens := bucket.TokensAt(now); tokens < 1 {
		return true, time.Duration((1 - tokens) / float64(limiter.limit) * float64(time.Second))
	}

	return false, 0
}

// Take takes one token from key's bucket, if it has one.
func (limiter *Limiter) Take(key string) {
	limiter.Allow(key)
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

// dropFull forgets every bucket that has refilled completely; a new bucket
// for the same key would be identical. The caller holds mu.
func (limiter *Limiter) dropFull(now time.Time) {
	for key, bucket := range limiter.buckets {
		if bucket.TokensAt(now) >= float64(limiter.burst) {
			delete(limiter.buckets, key)
		}
	}
}
