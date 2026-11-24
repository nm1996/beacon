# Guarantees and failure behavior

What Beacon promises, what it does not, and what happens when something
fails. Everything here describes the code as it is; the
[design records](../design/README.md) explain why.

Defaults are used for the numbers below: `registry.service_ttl` 30s,
`registry.cleanup_interval` 5s, `storage.postgres.heartbeat_flush_interval`
1s, `registry.cache_ttl` off and `server.shutdown_timeout` 10s.

## What is shared and what each process keeps

With the memory backend nothing is shared: each process is a registry of
its own. With PostgreSQL:

| State | Where it lives |
|---|---|
| Instances, their owners and last stored heartbeats | PostgreSQL, shared |
| Live nodes (`beacon_nodes`) | PostgreSQL, shared |
| Registration limits (`max_instances`, `max_instances_per_owner`) | Enforced in PostgreSQL, exact across processes |
| Heartbeats received since the last write | The process that received them, for up to one flush interval |
| Owners of instances heartbeating through a process | That process, checked with the database again at least once a minute |
| Lookup cache (`registry.cache_ttl`) | Each process |
| Rate limiter counters | Each process; the limits are divided by the number of live nodes |
| TLS files and tokens | Each process reads its own files |

## Lookups

- **Through one process, writes are visible at once.** A registration or
  removal through a process clears that process's lookup cache, and a lookup
  that started before the change does not store what it read.
- **Through other processes, within `registry.cache_ttl`.** With the cache
  off, every lookup reads the database, so a committed change is visible to
  every process at once. With it on, a change made through another process
  may take up to `cache_ttl` to show. Beacon does not offer read-your-writes
  across processes with the cache on.
- **`last_heartbeat` may lag.** It can be as old as the cache, plus the wait
  for the next heartbeat write, plus the time between heartbeats.
  Configuration keeps `cache_ttl + heartbeat_flush_interval` within a third
  of the TTL, so this lag alone never makes a live instance look expired.
- **Expired instances are not returned.** A lookup leaves out every
  instance whose last heartbeat is older than the TTL, even before cleanup
  removes it, which happens within `cleanup_interval` after that.
- **Lookups are not a snapshot across pages.** Pages are sorted by ID and
  continue after the last ID returned, so an instance registered or removed
  while a client pages may or may not appear; none appears twice.

## Registrations

- **Limits hold across processes.** Checking the limits and inserting is one
  transaction, under a lock that serializes registrations across every
  process. Heartbeats and lookups do not take that lock.
- **A chosen ID is kept.** Registering the same `id` again with the same name,
  address and port answers with the same instance. Another caller's ID, or
  the same ID with different details, gets `409 instance_exists`.
- **`Idempotency-Key` makes a retry safe.** A repeated registration with the
  same key and caller finds the instance the first attempt created.

## Heartbeats

- **A success means the heartbeat was stored or will be within one flush
  interval.** With batching, a process answers from memory only while one of
  its writes has succeeded within the last two intervals. Otherwise each
  heartbeat goes to the database and gets its answer, an error when the
  database cannot be reached.
- **A process that crashes loses up to one interval of heartbeats.** The
  instances' next heartbeats make up for them, since the interval is at most
  a third of the TTL.
- **Ownership is checked on every heartbeat.** A buffered heartbeat is
  checked against the owner the process learned from the database within the
  last minute, and checked again by the database when it is written. An ID
  taken over through another process is noticed at the next write, after
  which the old owner's heartbeats are refused.
- **An instance removed through another process** is noticed at the next
  write; its heartbeats then get `404 instance_not_found`.

## When something fails

| What fails | What Beacon does |
|---|---|
| PostgreSQL unreachable at startup | Beacon tries once to connect and migrate, for up to 30 seconds, then exits with an error. It does not start half initialized; let the supervisor or orchestrator restart it. |
| PostgreSQL unreachable while running | Requests that need it fail with `500 internal_error` (gRPC: `INTERNAL`). With `cache_ttl` on, lookups still answer from the cache until it expires. Heartbeats are accepted for at most two more flush intervals, then fail too. Cleanup and node records log the error and try again on their next run. |
| PostgreSQL restarts | Requests fail while it is down, as above. pgx's connection pool replaces broken connections, so requests succeed again once it is back, without restarting Beacon. |
| One process crashes | The others keep serving every request. Its buffered heartbeats are lost, and after 15 seconds it no longer counts as a live node. |
| A process is stopped for longer than the TTL | If it is the only process, every instance expires when it resumes, and their leases register again. With PostgreSQL and other processes running, the instances keep sending heartbeats through those and stay registered. |
| Clocks drift apart | Timestamps come from each process's clock, so skew between processes shifts when instances expire. Keep them synchronized with NTP, to well within the TTL. |
| Shutdown (`SIGINT`, `SIGTERM`) | Beacon stops accepting connections, lets in-flight HTTP and gRPC requests finish for up to `shutdown_timeout`, writes the buffered heartbeats within one flush interval plus 5 seconds, leaves the node list and closes the database. A request still running after `shutdown_timeout` may have its heartbeat lost. |
| The HTTP or gRPC port is taken | Beacon exits with an error at startup. |

`GET /health` reports that the process is serving. It does not check
PostgreSQL. Watch `beacon_heartbeat_flushes_total{result="error"}`,
`beacon_registry_operations_total{result="error"}` and the logs to see
database trouble.

## What Beacon does not provide

- **No consensus or leader election.** Processes coordinate only through
  PostgreSQL. Every process runs cleanup, which is safe because removing an
  expired instance twice removes it once.
- **PostgreSQL is the single point that must stay available.** Beacon adds no
  replication of its own; use PostgreSQL's.
- **No strong consistency across processes with the cache on.** See
  [Lookups](#lookups).
- **No replication across regions or between separate databases.**
