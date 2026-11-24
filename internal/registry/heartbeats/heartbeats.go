// Package heartbeats wraps a discovery.Registry so heartbeats reach it in
// batches: each node keeps the latest heartbeat of every instance in
// memory and writes them all at once on a short interval. With a database
// shared by several nodes, that turns one write per heartbeat into one
// statement per node and interval.
//
// A heartbeat still gets a definite answer. The first one for an instance,
// and one after the instance is forgotten, goes to the registry at once,
// which checks that it exists and who owns it; the owner is remembered, so
// later heartbeats are checked here and only buffered. When a write finds
// an instance gone, because it expired or another node removed it, the
// instance is forgotten, and its next heartbeat gets ErrInstanceNotFound.
//
// Buffering needs flushes that work. When none has succeeded for two
// intervals, heartbeats go to the registry again and get its answer, an
// error if it cannot be reached, so a node cut off from the database does
// not keep answering for instances that other nodes have meanwhile expired.
// Once a flush succeeds again, the node forgets every instance, so each is
// checked with the registry at its next heartbeat: while the node could
// not write, other nodes may have removed it.
//
// Heartbeats buffered when a node dies are lost, as an instance whose node
// died needs to register again anyway; an interval well below the TTL
// keeps that from expiring any instance.
package heartbeats

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/nm1996/beacon/internal/discovery"
)

// chunkSize is the most heartbeats written in one statement.
const chunkSize = 1000

// The results a flush that had heartbeats to write reports.
const (
	FlushOK    = "ok"
	FlushError = "error"
)

// recheckAfter is how long a checked instance is trusted before its next
// heartbeat is checked with the registry again, so an instance removed and
// registered anew under the same ID by another owner is noticed.
const recheckAfter = time.Minute

// Heartbeat is one buffered heartbeat: the instance, its time, and the
// owner check of the caller that sent it.
type Heartbeat struct {
	ID    string
	At    time.Time
	Check discovery.OwnerCheck
}

// Writer writes many heartbeats at once. The PostgreSQL backend is one.
type Writer interface {
	// TouchMany sets the LastHeartbeat of every instance in heartbeats to
	// its time, unless it already holds a later one, applying each owner
	// check, and returns the IDs it did not change: instances that no
	// longer exist, or whose owner the check refuses.
	TouchMany(ctx context.Context, heartbeats []Heartbeat) ([]string, error)
}

// known is an instance a heartbeat has been checked against.
type known struct {
	owner     string
	checkedAt time.Time
}

// Registry is a discovery.Registry whose heartbeats are batched. Use New
// to create one, and run Run until shutdown. It is safe for concurrent
// use.
type Registry struct {
	discovery.Registry

	writer   Writer
	interval time.Duration
	logger   *slog.Logger
	flushed  func(result string)

	mu      sync.Mutex
	known   map[string]known
	pending map[string]Heartbeat
	// flushedAt is when the last flush that did not fail started. While it
	// is more than two intervals old, heartbeats are not buffered.
	flushedAt time.Time
	// forgets counts the instances forgotten, so a check that started
	// before one does not remember an instance that is gone.
	forgets uint64
}

var _ discovery.Registry = (*Registry)(nil)

// New returns registry with its heartbeats written through writer every
// interval. flushed, if not nil, is called with FlushOK or FlushError after
// every flush that had heartbeats to write.
func New(registry discovery.Registry, writer Writer, interval time.Duration, logger *slog.Logger, flushed func(result string)) *Registry {
	if flushed == nil {
		flushed = func(string) {}
	}

	return &Registry{
		Registry: registry,
		writer:   writer,
		interval: interval,
		logger:   logger,
		flushed:  flushed,
		known:    make(map[string]known),
		pending:  make(map[string]Heartbeat),
		// Counted from the start, so the first heartbeats can be buffered.
		flushedAt: time.Now(),
	}
}

// Touch buffers the heartbeat of a known instance, or checks an unknown one
// with the registry, which records it at once. While flushes fail, every
// heartbeat goes to the registry, so none is acknowledged without being
// written.
func (registry *Registry) Touch(ctx context.Context, id string, at time.Time, check discovery.OwnerCheck) error {
	registry.mu.Lock()

	instance, isKnown := registry.known[id]
	writing := time.Since(registry.flushedAt) <= 2*registry.interval

	if isKnown && writing && time.Since(instance.checkedAt) < recheckAfter {
		defer registry.mu.Unlock()

		if !check.Allows(instance.owner) {
			return discovery.ErrInstanceNotOwned
		}

		if at.After(registry.pending[id].At) {
			registry.pending[id] = Heartbeat{ID: id, At: at, Check: check}
		}

		return nil
	}

	forgets := registry.forgets
	registry.mu.Unlock()

	if err := registry.Registry.Touch(ctx, id, at, check); err != nil {
		return err
	}

	stored, err := registry.Get(ctx, id)
	if err != nil {
		// The heartbeat was recorded; the instance is just not remembered.
		return nil //nolint:nilerr // the next heartbeat checks again
	}

	registry.mu.Lock()
	defer registry.mu.Unlock()

	// An instance forgotten meanwhile may be the one just read, now gone.
	if registry.forgets == forgets {
		registry.known[id] = known{owner: stored.Owner, checkedAt: time.Now()}
	}

	return nil
}

// Delete removes the instance and forgets it.
func (registry *Registry) Delete(ctx context.Context, id string, check discovery.OwnerCheck) error {
	err := registry.Registry.Delete(ctx, id, check)
	if err == nil {
		registry.forget(id)
	}

	return err
}

// DeleteStale writes the buffered heartbeats first, so instances whose
// heartbeats this node holds are not taken for stale, then removes the
// stale ones.
func (registry *Registry) DeleteStale(ctx context.Context, cutoff time.Time) (int, error) {
	if err := registry.Flush(ctx); err != nil {
		return 0, err
	}

	return registry.Registry.DeleteStale(ctx, cutoff)
}

// forget drops what the node knows about id.
func (registry *Registry) forget(id string) {
	registry.mu.Lock()
	defer registry.mu.Unlock()

	delete(registry.known, id)
	delete(registry.pending, id)
	registry.forgets++
}

// Flush writes every buffered heartbeat, chunkSize at a time, and forgets
// the instances the writes did not change, gone or owned by someone else,
// so their next heartbeat is checked with the registry. Heartbeats of a
// chunk that fails are kept for the next flush, unless a newer one arrived
// meanwhile. It also forgets instances checked too long ago, which keeps
// the memory bound to the instances heartbeating through this node.
func (registry *Registry) Flush(ctx context.Context) error {
	started := time.Now()

	registry.mu.Lock()
	pending := registry.pending
	registry.pending = make(map[string]Heartbeat, len(pending))

	for id, instance := range registry.known {
		if time.Since(instance.checkedAt) >= recheckAfter {
			delete(registry.known, id)
		}
	}
	registry.mu.Unlock()

	chunk := make([]Heartbeat, 0, min(len(pending), chunkSize))

	var firstErr error

	write := func() {
		missing, err := registry.writer.TouchMany(ctx, chunk)
		if err != nil {
			registry.requeue(chunk)

			if firstErr == nil {
				firstErr = fmt.Errorf("write heartbeats: %w", err)
			}
		}

		for _, id := range missing {
			registry.forget(id)
		}

		chunk = make([]Heartbeat, 0, chunkSize)
	}

	for _, heartbeat := range pending {
		chunk = append(chunk, heartbeat)
		if len(chunk) == chunkSize {
			write()
		}
	}

	if len(chunk) > 0 {
		write()
	}

	if len(pending) > 0 {
		if firstErr == nil {
			registry.flushed(FlushOK)
		} else {
			registry.flushed(FlushError)
		}
	}

	if firstErr == nil {
		registry.mu.Lock()
		// A slower flush that started earlier must not move it back.
		if started.After(registry.flushedAt) {
			// After a time without writing, the instances this node knows
			// may have been removed by other nodes, such as by cleanup once
			// the database was back. Checking each again makes its next
			// heartbeat get ErrInstanceNotFound at once, instead of being
			// acknowledged here once per node behind a load balancer.
			if started.Sub(registry.flushedAt) > 2*registry.interval {
				clear(registry.known)
				registry.forgets++
			}

			registry.flushedAt = started
		}
		registry.mu.Unlock()
	}

	return firstErr
}

// requeue puts heartbeats back to be written next time, keeping any newer
// one that arrived meanwhile.
func (registry *Registry) requeue(heartbeats []Heartbeat) {
	registry.mu.Lock()
	defer registry.mu.Unlock()

	for _, heartbeat := range heartbeats {
		if heartbeat.At.After(registry.pending[heartbeat.ID].At) {
			registry.pending[heartbeat.ID] = heartbeat
		}
	}
}

// Run flushes every interval until ctx is cancelled, then once more, so a
// node shutting down cleanly writes what it holds. A failed flush is
// logged and retried with the next one.
func (registry *Registry) Run(ctx context.Context) {
	ticker := time.NewTicker(registry.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			finalCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), registry.interval+5*time.Second)
			defer cancel()

			if err := registry.Flush(finalCtx); err != nil {
				registry.logger.ErrorContext(finalCtx, "write the last heartbeats", slog.Any("error", err))
			}

			return
		case <-ticker.C:
			if err := registry.Flush(ctx); err != nil {
				registry.logger.WarnContext(ctx, "write heartbeats", slog.Any("error", err))
			}
		}
	}
}
