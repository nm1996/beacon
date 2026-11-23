# 0008. Rate limits across nodes

Status: Proposed

## Context

Rate limit buckets live in each process. With N nodes behind a round-robin load balancer, a client gets N times the configured limit. Exact global limiting needs every decision to consult shared state. Typically that is Redis with a Lua token bucket, as Envoy's global rate limit service does, at the cost of a network round trip per request and a new dependency.

## Decision

**Local buckets with a dynamic share.** When membership (0007) is available, each node applies `rate / N` and `max(1, burst / N)`, where N is the number of live nodes, and updates the share when membership changes. The limiter gains `SetRate`, which retunes every bucket in place through `rate.Limiter.SetLimit` and `SetBurst`.

- With a round-robin load balancer, a client's requests spread evenly, so each node sees about 1/N of them and the cluster enforces about the configured limit.
- With a sticky load balancer, a client hits one node and gets 1/N of the limit. That is stricter than configured, which is the safe direction for an abuse limit.
- Without membership, on the memory backend, N is 1 and nothing changes.
- The README recommends enforcing limits at the load balancer as well for large deployments. Beacon's limits are a second line, not the only one.

## Alternatives considered

- **Redis-backed global buckets.** They are exact, but they add a new required service and a round trip to every registration and failed authentication. Exactness matters little for limits whose purpose is to stop floods.
- **Buckets in PostgreSQL.** They are exact without new infrastructure, but turn every failed authentication into a database write, which is exactly the traffic an attacker controls.
- **Leaving it to the load balancer only.** This is documented as the complement, but Beacon should not become unprotected just because it is scaled out.

## Consequences

- Limits mean roughly the same thing at any node count, without new infrastructure.
- Short skews while membership changes, for up to three membership intervals.
- A per-key burst smaller than N is rounded up to 1 per node. With many nodes, a burst is then up to N per cluster.

## Open questions

- Is a pluggable global limiter, such as Redis, worth an interface now? Default: no, until someone needs exactness.
