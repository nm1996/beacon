# 0006. PostgreSQL storage, with a read cache

Status: Proposed

## Context

All state is in one process's memory. A restart forgets every instance, and a second Beacon process cannot share the first one's state. That rules out both durability and more than one node (0007). The storage interface (`discovery.Registry`) and its contract suite (`registrytest`) were built so a second backend could be added without touching the rules.

## Decision

### Backend

A `postgres` storage backend, selected with `storage.backend = "postgres"` and `storage.postgres.url`, implements `discovery.Registry` and passes the same contract suite as the memory backend.

- **Driver:** `github.com/jackc/pgx/v5` with its `pgxpool`. It is the de facto Go PostgreSQL driver: maintained, fast, native protocol, context everywhere, and no cgo.
- **Schema:**
  ```sql
  CREATE TABLE beacon_instances (
      id             text PRIMARY KEY,
      name           text NOT NULL,
      address        text NOT NULL,
      port           integer NOT NULL,
      owner          text NOT NULL DEFAULT '',
      registered_at  timestamptz NOT NULL,
      last_heartbeat timestamptz NOT NULL
  );
  CREATE INDEX ON beacon_instances (name, id);   -- lookups by name, paged by ID
  CREATE INDEX ON beacon_instances (last_heartbeat);  -- cleanup
  CREATE INDEX ON beacon_instances (owner) WHERE owner <> '';  -- per-owner count
  ```
- **Limits stay atomic.** `Create` runs in a transaction that first takes `pg_advisory_xact_lock` on a Beacon-wide key, then counts and inserts. Registrations are rare next to heartbeats, so serializing them costs little, and the count can never overshoot. That is the same guarantee the memory backend gives with its mutex.
- **Owner checks are one statement.** `Touch` and `Delete` use `UPDATE`/`DELETE ... WHERE id = $1 AND (owner = $2 OR $3)`. Only when no row is affected, a `SELECT` tells "not found" from "not owned".
- **Migrations** are embedded SQL files, applied at startup under an advisory lock and recorded in `beacon_schema_migrations`. That avoids a migration framework dependency for a handful of statements, and several nodes starting together apply each migration once.
- **Time:** timestamps come from the node, as with the memory backend. Several nodes need clocks synchronized well within a TTL, which NTP gives.

### Read cache

A `registry.cache_ttl` setting, default `0` (off), wraps any backend in a decorator that caches `List` results for that long. Lookups then reach the database at most once per TTL per query and node. A write through the same node clears its cache, and writes through other nodes become visible within the TTL. That staleness is below the client's own refresh interval (0005), so it is invisible in practice. It is a decorator in `internal/registry/cache`, so the memory backend does not need it and the PostgreSQL backend does not have to know about it.

### Batched heartbeats

Decided in review: one write per heartbeat means every heartbeat interval costs N writes for N instances. `internal/registry/heartbeats` wraps a backend that can write many heartbeats at once (`TouchMany`, one `UPDATE ... FROM unnest(...)` keeping the later time, per thousand instances).

- The first heartbeat of an instance goes to the database, which checks that it exists and who owns it.
- Later heartbeats are checked against the remembered owner and buffered, then written every `storage.postgres.heartbeat_flush_interval` (1s by default, at most a third of the TTL).
- A write that finds an instance gone makes the node forget it, so its next heartbeat gets `404`. Every instance is checked again at least once a minute, in case its ID was registered anew by another owner.
- Cleanup on a node writes that node's buffer first.
- A crashed node loses at most one interval of heartbeats. That is harmless, because its instances heartbeat again within a third of the TTL; an instance whose node died must register again anyway.

## Alternatives considered

- **SQLite.** It is embedded and gives durability, but one file on one host does nothing for several nodes, which is the main reason for a database here.
- **etcd or Consul as storage.** Using another registry as Beacon's storage gives consistency, but is an odd dependency for a registry, and their transaction models make atomic quotas awkward.
- **Redis.** It is fast and good at TTLs, but its durability and transactional guarantees are weaker. It would also likely be added next to a database anyway.
- **An ORM or sqlc.** The queries are few and fixed. Hand-written SQL with pgx is easier to review than generated code.

## Consequences

- Instances survive Beacon restarts when PostgreSQL is used, and several nodes can share them.
- Heartbeats are batched per node (decided in review, see below), so the database sees about one statement per node and second instead of one write per heartbeat.
- A new direct dependency (pgx) and a new operational dependency, but only for operators who choose it.
- Contract tests run against PostgreSQL when `BEACON_TEST_POSTGRES_URL` is set. CI provides one as a service container.

## Open questions

- Cleanup across nodes: see 0007.
- Should the cache also cover `Get`? Default: no. `Get` is only used on the registration path.
