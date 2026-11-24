# 0007. Several Beacon nodes over one database

Status: Proposed

## Context

A single node is a single point of failure. While Beacon is down, nothing can register, and with today's client nothing can be discovered. Deploys and host failures take it down. There are three families of designs for a replicated registry:

1. **Consensus (Raft):** etcd, Consul servers and ZooKeeper. Each node holds the state, and a leader orders writes. It is strongly consistent and needs no external storage, but it is the hardest thing in this list to build and operate correctly: membership changes, snapshots, split brain, and quorum loss during maintenance.
2. **Peer replication, AP:** Netflix Eureka. Nodes accept writes and copy them to peers, preferring availability over consistency. It is simpler than Raft but has weak semantics, for example quotas cannot be exact, and it needs its own replication protocol.
3. **Shared storage:** stateless nodes over one database, as most web services do. Correctness lives in a system built for it. Nodes come and go freely, and a load balancer spreads clients.

## Decision

Option 3, on the PostgreSQL backend (0006).

- **Nodes are stateless,** apart from the read cache (0006) and rate limit buckets (0008). Any node serves any request, so the load balancer needs no stickiness.
- **Membership:** each node upserts a row in `beacon_nodes (id, address, version, started_at, last_seen)` every few seconds and deletes it on shutdown. A node is alive if `last_seen` is within three intervals. Membership drives the rate limit share (0008) and is reported at `GET /api/v1/nodes`, which needs the `manage` permission.
- **Cleanup runs on every node.** `DELETE ... WHERE last_heartbeat < $cutoff` is idempotent, so several nodes running it at once remove each instance once. A leader election would save a few cheap deletes per interval, at the price of a distributed protocol and of nobody cleaning up when the leader is stuck. Each node's interval has jitter, so their scans spread out.
- **Node ID:** a random ID per process, or `server.node_id` when set, for example to a pod name.
- **The memory backend stays single-node.** Two memory nodes behind a load balancer would be two registries. Beacon cannot detect that, so the README says it plainly.

## Alternatives considered

- **Raft with `hashicorp/raft`.** It is the most self-contained option, and the right one if Beacon ever has to run with no other infrastructure. It is postponed: it is a large, risky change, and shared storage gives HA now. The `Registry` interface keeps the door open, since a Raft-backed registry would be one more backend.
- **Leader election for cleanup** through a PostgreSQL advisory lock or lease. It is not needed while cleanup is idempotent, as explained above.

## Consequences

- Beacon survives losing any node. Capacity for lookups scales with nodes, and with the cache. Capacity for writes is PostgreSQL's.
- PostgreSQL becomes the availability limit. Its HA (a managed service, Patroni) is the operator's choice.
- Cross-node visibility of a change is immediate for writes and within `cache_ttl` for lookups.

## Open questions

- Heartbeats are batched per node, decided in review; see 0006. An accepted heartbeat is durable within one flush interval, which is enough for liveness.
