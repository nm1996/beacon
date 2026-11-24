// Package cluster keeps track of the Beacon nodes that share one storage
// backend, so each can tell how many are live: every node records itself
// on a short interval, and a node not seen for a few intervals counts as
// gone. The count divides the rate limits between nodes.
package cluster

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"
)

// Interval is how often a node records itself. A node whose last record
// is more than LiveFor old counts as gone.
const (
	Interval = 5 * time.Second
	LiveFor  = 3 * Interval
)

// Node is one Beacon process.
type Node struct {
	// ID names the node: server.node_id, or a random ID per process.
	ID string
	// Address is where the node listens.
	Address string
	// Version is the Beacon version the node runs.
	Version string
	// StartedAt is when the node started.
	StartedAt time.Time
	// LastSeen is when the node last recorded itself.
	LastSeen time.Time
}

// Store records nodes where every node can see them, such as the database
// they share.
type Store interface {
	// Beat records node as seen at node.LastSeen, adding it if it is new.
	Beat(ctx context.Context, node Node) error
	// Leave removes the node with id.
	Leave(ctx context.Context, id string) error
	// Live returns the nodes seen at or after since, sorted by ID.
	Live(ctx context.Context, since time.Time) ([]Node, error)
}

// Membership records one node in a Store and follows how many are live.
type Membership struct {
	store   Store
	node    Node
	logger  *slog.Logger
	changed func(live int)

	mu   sync.Mutex
	live int
}

// New returns a Membership that records node in store. changed, if not
// nil, is called with the number of live nodes whenever it changes, and
// once with the first count.
func New(store Store, node Node, logger *slog.Logger, changed func(live int)) *Membership {
	if changed == nil {
		changed = func(int) {}
	}

	return &Membership{store: store, node: node, logger: logger, changed: changed}
}

// Join records the node once and reports the first count, so the rate
// limits are divided before the server takes requests.
func (membership *Membership) Join(ctx context.Context) error {
	return membership.beat(ctx)
}

// Run records the node every Interval until ctx is cancelled, then removes
// it, so the other nodes take over its share at once instead of after
// LiveFor. A failed record is logged and retried on the next tick.
func (membership *Membership) Run(ctx context.Context) {
	ticker := time.NewTicker(Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			leaveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), Interval)
			defer cancel()

			if err := membership.store.Leave(leaveCtx, membership.node.ID); err != nil {
				membership.logger.WarnContext(leaveCtx, "leave the cluster", slog.String("node", membership.node.ID), slog.Any("error", err))
			}

			return
		case <-ticker.C:
			if err := membership.beat(ctx); err != nil && ctx.Err() == nil {
				membership.logger.WarnContext(ctx, "record cluster membership", slog.String("node", membership.node.ID), slog.Any("error", err))
			}
		}
	}
}

// Nodes returns the live nodes, sorted by ID.
func (membership *Membership) Nodes(ctx context.Context) ([]Node, error) {
	nodes, err := membership.store.Live(ctx, time.Now().Add(-LiveFor))
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}

	return nodes, nil
}

// beat records the node and reports a change in the number of live nodes.
func (membership *Membership) beat(ctx context.Context) error {
	now := time.Now()
	node := membership.node
	node.LastSeen = now

	if err := membership.store.Beat(ctx, node); err != nil {
		return fmt.Errorf("record node: %w", err)
	}

	nodes, err := membership.store.Live(ctx, now.Add(-LiveFor))
	if err != nil {
		return fmt.Errorf("list nodes: %w", err)
	}

	// The node itself counts even if the store's clock disagrees.
	live := max(len(nodes), 1)

	membership.mu.Lock()
	previous := membership.live
	membership.live = live
	membership.mu.Unlock()

	if live != previous {
		if previous != 0 {
			membership.logger.InfoContext(ctx, "cluster membership changed", slog.Int("nodes", live), slog.Int("previous", previous))
		}

		membership.changed(live)
	}

	return nil
}

// Local is a Store for a node that shares its storage with no other, as
// with the memory backend. It is safe for concurrent use.
type Local struct {
	mu    sync.Mutex
	nodes map[string]Node
}

// NewLocal returns an empty Local store.
func NewLocal() *Local {
	return &Local{nodes: make(map[string]Node)}
}

// Beat records node.
func (store *Local) Beat(_ context.Context, node Node) error {
	store.mu.Lock()
	defer store.mu.Unlock()

	store.nodes[node.ID] = node

	return nil
}

// Leave removes the node with id.
func (store *Local) Leave(_ context.Context, id string) error {
	store.mu.Lock()
	defer store.mu.Unlock()

	delete(store.nodes, id)

	return nil
}

// Live returns the nodes seen at or after since, sorted by ID.
func (store *Local) Live(_ context.Context, since time.Time) ([]Node, error) {
	store.mu.Lock()
	defer store.mu.Unlock()

	nodes := make([]Node, 0, len(store.nodes))

	for _, node := range store.nodes {
		if !node.LastSeen.Before(since) {
			nodes = append(nodes, node)
		}
	}

	slices.SortFunc(nodes, func(left, right Node) int { return strings.Compare(left.ID, right.ID) })

	return nodes, nil
}
