package client

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"
)

const (
	// firstRetryDelay is how long a Lease waits before retrying a failed
	// heartbeat. Each further failure doubles it, up to the normal interval.
	firstRetryDelay = time.Second

	// maxBackoffDoublings caps the doubling so the shift cannot overflow.
	maxBackoffDoublings = 16
)

// Lease keeps one instance registered with Beacon. Announce creates it; it
// then sends heartbeats in the background every third of the TTL and, if
// Beacon no longer knows the instance, for example after Beacon restarted,
// registers it again under a new ID. Failed heartbeats are retried with
// backoff until Close is called.
//
// A Lease runs one goroutine, which Close stops. It is safe for concurrent
// use.
type Lease struct {
	client       *Client
	registration Registration

	mu  sync.Mutex
	id  string
	ttl time.Duration

	stop context.CancelFunc
	done chan struct{}

	// closeMu serializes the unregister step of concurrent Close calls.
	closeMu sync.Mutex
	// unregistered is set once Close has removed the instance, or found it
	// already gone, so later calls do not send another request.
	unregistered bool
}

// Announce registers registration and returns a Lease that keeps it alive
// until Close. It returns an error, and starts nothing, if the first
// registration fails.
//
// ctx only bounds that first registration. The background heartbeats keep
// ctx's values but not its cancellation; they stop when Close is called.
func (client *Client) Announce(ctx context.Context, registration Registration) (*Lease, error) {
	registered, err := client.Register(ctx, registration)
	if err != nil {
		return nil, err
	}

	loopCtx, stop := context.WithCancel(context.WithoutCancel(ctx))

	lease := &Lease{
		client:       client,
		registration: registration,
		id:           registered.ID,
		ttl:          registered.TTL,
		stop:         stop,
		done:         make(chan struct{}),
	}

	go lease.run(loopCtx)

	return lease, nil
}

// ID returns the instance's current ID. It changes when the Lease has to
// register again.
func (lease *Lease) ID() string {
	lease.mu.Lock()
	defer lease.mu.Unlock()

	return lease.id
}

// Close stops the heartbeats, waits for the background goroutine to exit
// and unregisters the instance. The first call stops the heartbeats for
// good; once the goroutine has exited, nothing is sent in the background
// any more.
//
// Close returns ctx's error if ctx ends before the goroutine has exited,
// and the unregister error if Beacon cannot be reached. Either way Close
// may be called again, with a new ctx, to finish the job; if it never is,
// the instance expires after its TTL. Once the instance is unregistered,
// further calls return nil without sending anything.
func (lease *Lease) Close(ctx context.Context) error {
	// Cancelling is idempotent, so every call may do it.
	lease.stop()

	select {
	case <-lease.done:
	case <-ctx.Done():
		return ctx.Err()
	}

	lease.closeMu.Lock()
	defer lease.closeMu.Unlock()

	if lease.unregistered {
		return nil
	}

	err := lease.client.Unregister(ctx, lease.ID())
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}

	lease.unregistered = true

	return nil
}

// run sends heartbeats until ctx is cancelled.
func (lease *Lease) run(ctx context.Context) {
	defer close(lease.done)

	failures := 0
	timer := time.NewTimer(lease.nextDelay(failures))
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}

		if lease.renew(ctx) {
			failures = 0
		} else {
			failures++
		}

		timer.Reset(lease.nextDelay(failures))
	}
}

// renew sends a heartbeat, or registers again if Beacon no longer knows the
// instance. It reports whether the instance is known to Beacon afterwards.
// Each request gets at most attemptTimeout.
func (lease *Lease) renew(ctx context.Context) bool {
	logger := lease.client.logger
	id := lease.ID()

	attemptCtx, cancel := lease.attempt(ctx)
	err := lease.client.Heartbeat(attemptCtx, id)
	cancel()

	if err == nil {
		return true
	}

	if ctx.Err() != nil {
		return false
	}

	if !errors.Is(err, ErrNotFound) {
		logger.WarnContext(ctx, "beacon heartbeat failed", slog.String("id", id), slog.Any("error", err))
		return false
	}

	attemptCtx, cancel = lease.attempt(ctx)
	registered, err := lease.client.Register(attemptCtx, lease.registration)
	cancel()

	if err != nil {
		if ctx.Err() == nil {
			logger.WarnContext(ctx, "beacon registration failed", slog.String("name", lease.registration.Name), slog.Any("error", err))
		}

		return false
	}

	lease.mu.Lock()
	lease.id = registered.ID
	lease.ttl = registered.TTL
	lease.mu.Unlock()

	logger.InfoContext(ctx, "beacon registered the instance again",
		slog.String("name", lease.registration.Name),
		slog.String("previous_id", id),
		slog.String("id", registered.ID),
	)

	return true
}

// attempt returns a context for one request of the background loop, which
// ends after a sixth of the TTL, half the normal heartbeat interval. A
// request stuck on a dead connection then fails in time for retries on a
// new one before the instance expires, even with an http.Client that has
// no timeout of its own.
func (lease *Lease) attempt(ctx context.Context) (context.Context, context.CancelFunc) {
	lease.mu.Lock()
	timeout := lease.ttl / 6
	lease.mu.Unlock()

	return context.WithTimeout(ctx, timeout)
}

// nextDelay is how long to wait before the next heartbeat: a third of the
// TTL normally, or a doubling retry delay capped at that after failures. It
// adds up to 10% jitter either way, so many instances started together do
// not send heartbeats in lockstep.
func (lease *Lease) nextDelay(failures int) time.Duration {
	lease.mu.Lock()
	interval := lease.ttl / 3
	lease.mu.Unlock()

	if failures > 0 {
		retry := firstRetryDelay << min(failures-1, maxBackoffDoublings)
		interval = min(interval, retry)
	}

	jitter := 0.9 + 0.2*rand.Float64() //nolint:gosec // jitter needs no cryptographic randomness

	return time.Duration(float64(interval) * jitter)
}
