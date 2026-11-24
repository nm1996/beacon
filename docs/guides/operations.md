# Operations

PostgreSQL storage, running several nodes, Docker, and metrics. What Beacon guarantees, and how it behaves when something fails, is in [Guarantees and failure behavior](guarantees.md).

## PostgreSQL storage

By default Beacon keeps instances in memory. With PostgreSQL they survive restarts, and several Beacon processes can share them:

```toml
[storage]
backend = "postgres"

[storage.postgres]
# Better set with BEACON_STORAGE_POSTGRES_URL, since it may hold a password.
url = "postgres://beacon:secret@db:5432/beacon?sslmode=verify-full&pool_max_conns=20"

[registry]
cache_ttl = "2s"
```

- Beacon creates its tables on startup, `beacon_instances` and `beacon_schema_migrations`, and applies later schema changes itself. Several processes starting together apply each change once. The database user needs to create tables in its schema.
- The limits stay exact with several processes: a registration checks `registry.max_instances` and `registry.max_instances_per_owner` and inserts in one transaction, under a lock that serializes registrations across every process. Heartbeats and lookups do not take it.
- Heartbeats are batched. Each process checks an instance's first heartbeat with the database, then keeps the following ones in memory and writes them all every `storage.postgres.heartbeat_flush_interval` (1 second by default), in one `UPDATE` per thousand instances. 10000 instances at the default TTL send about 1000 heartbeats a second, which become about one statement a second per process instead of 1000. A heartbeat is still answered at once: owners are checked from what the process learned, an instance another process removed is noticed at the next write and gets `404` after it, and every instance is checked with the database again at least once a minute. When no write has succeeded for two intervals, the process stops batching and sends each heartbeat to the database, so a process cut off from it answers with an error instead of accepting heartbeats it cannot store; `beacon_heartbeat_flushes_total` counts the writes. A process that crashes loses up to one interval of heartbeats, which its instances' next heartbeats make up for; `0s` writes each heartbeat at once.
- `registry.cache_ttl` keeps each process's lookup results for that long, so lookups reach the database at most once per name per interval. A registration or removal through a process clears its cache. Changes through other processes show within the interval, and so may `last_heartbeat`, which heartbeats do not refresh in the cache. That is why it may be at most a third of `registry.service_ttl`, and together with `storage.postgres.heartbeat_flush_interval` too: a lookup can see a heartbeat as old as the cache, plus its wait to be written, plus the time between heartbeats.
- Timestamps come from each process's clock. Keep the clocks of every Beacon process synchronized, with NTP, to well within the TTL.
- The connection string's parameters include pgx's pool settings, such as `pool_max_conns` (default: the greater of 4 and the number of CPUs).

## Running several nodes

With [PostgreSQL storage](#postgresql-storage), any number of Beacon processes can share one database behind a load balancer. Every node serves every request, so the load balancer needs no stickiness, and losing a node loses nothing.

```text
clients ──► load balancer ──► beacon-0 ─┐
                          ──► beacon-1 ─┼──► PostgreSQL
                          ──► beacon-2 ─┘
```

- Each node records itself in `beacon_nodes` every 5 seconds and removes itself on shutdown; a node not seen for 15 seconds no longer counts. `GET /api/v1/nodes` and `beacon_cluster_nodes` show who is live.
- `server.node_id` names a node, for example after its pod; without it each start picks a random ID.
- Every node runs cleanup. Removing expired instances is idempotent, so two nodes doing it at once remove each instance once, and no node has to be elected to do it.
- Registrations stay within `registry.max_instances` and `registry.max_instances_per_owner` across all nodes; see [PostgreSQL storage](#postgresql-storage).
- [Rate limits](security.md#rate-limits) are divided by the number of live nodes.
- With the memory backend, nodes behind one load balancer would be separate registries, each knowing only the instances that happened to reach it. Use one node, or PostgreSQL.

## Running in Docker

Release images are published to the GitHub Container Registry for `linux/amd64` and `linux/arm64`:

```bash
docker run --rm -p 8080:8080 ghcr.io/nm1996/beacon:0.1
```

| Tag | Points to |
|---|---|
| `0.1.0` | That exact release |
| `0.1` | The newest `0.1.x` release |
| `latest` | The newest release; pre-releases such as `0.1.0-rc.1` never move it |

The image is built on [distroless](https://github.com/GoogleContainerTools/distroless) `static`: it has no shell or package manager and runs as a non-root user. It reads `/etc/beacon/beacon.toml`, a copy of the example configuration, so every default from [Configuration](configuration.md) applies, except that the image sets `BEACON_SERVER_HOST=0.0.0.0` so the published port can reach it. Environment variables win over the file, so a mounted file's `server.host` is ignored unless you also pass `-e BEACON_SERVER_HOST=...`. Change settings either way:

```bash
# Environment variables
docker run --rm -p 9090:9090 -e BEACON_SERVER_PORT=9090 ghcr.io/nm1996/beacon:0.1

# Your own file, mounted over the built-in one
docker run --rm -p 8080:8080 -v "$PWD/beacon.toml:/etc/beacon/beacon.toml:ro" ghcr.io/nm1996/beacon:0.1
```

The image's health check runs `beacon --health-check`, which asks the running server for `GET /health` using the same configuration and environment. With TLS on it uses HTTPS and does not verify the certificate, because it only connects to the server's own address, sends nothing secret and reads nothing but the status. If you start the container with a different `--config` path, mount your file at `/etc/beacon/beacon.toml` instead, so the health check reads it too.

To serve HTTPS from the container, mount the certificate and key and point Beacon at them. The image runs as the `nonroot` user (UID 65532), which must be able to read both files:

```bash
docker run --rm -p 8080:8080 \
  -v "$PWD/certs:/etc/beacon/tls:ro" \
  -e BEACON_TLS_CERT_FILE=/etc/beacon/tls/cert.pem \
  -e BEACON_TLS_KEY_FILE=/etc/beacon/tls/key.pem \
  ghcr.io/nm1996/beacon:0.1
```

To turn on [authentication](security.md#authentication), mount the tokens file the same way:

```bash
docker run --rm -p 8080:8080 \
  -v "$PWD/tokens.toml:/etc/beacon/tokens.toml:ro" \
  -e BEACON_AUTH_ENABLED=true \
  -e BEACON_AUTH_TOKENS_FILE=/etc/beacon/tokens.toml \
  ghcr.io/nm1996/beacon:0.1
```

The image's health check keeps working, since `/health` needs no token.

To build and run locally with Compose, which also drops all capabilities and uses a read-only filesystem:

```bash
docker compose -f deployments/compose/compose.yaml up --build
```

## Metrics

With `metrics.enabled` (the default), Beacon serves Prometheus metrics at `GET /metrics` on the API port. Point Prometheus at it:

```yaml
scrape_configs:
  - job_name: beacon
    static_configs:
      - targets: ["beacon:8080"]
```

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `beacon_http_requests_total` | counter | `method`, `route`, `code` | Requests served. `route` is the route pattern, such as `POST /api/v1/services/{id}/heartbeat`, or `unmatched`; `method` is a standard HTTP method or `other` |
| `beacon_http_request_duration_seconds` | histogram | `method`, `route` | Time spent serving requests |
| `beacon_instances` | gauge | | Registered instances |
| `beacon_services` | gauge | | Distinct service names with at least one registered instance |
| `beacon_expired_instances_total` | counter | | Instances removed because they stopped sending heartbeats |
| `beacon_registry_operations_total` | counter | `operation`, `result` | Calls to the storage backend; `result` is `ok`, `not_found`, `not_owned`, `full` (refused by `registry.max_instances`), `owner_full` (refused by `registry.max_instances_per_owner`), `exists` (a create for an ID already taken, as when a registration is repeated) or `error` |
| `beacon_registry_operation_duration_seconds` | histogram | `operation` | Time spent in the storage backend |
| `beacon_rate_limited_total` | counter | `limit` | Requests refused by a [rate limit](security.md#rate-limits); `limit` is `registrations` or `auth_failures`. Only appears once a request has been refused |
| `beacon_cluster_nodes` | gauge | | Live Beacon nodes sharing this node's storage, this one included; see [Running several nodes](#running-several-nodes) |
| `beacon_grpc_requests_total` | counter | `method`, `code` | gRPC calls, by full method name and status code |
| `beacon_grpc_request_duration_seconds` | histogram | `method` | Time spent serving gRPC calls |
| `beacon_auth_failures_total` | counter | `reason` | Requests refused by [authentication](security.md#authentication); `reason` is `missing`, `invalid` or `forbidden`. Only appears once a request has been refused |
| `beacon_credential_reloads_total` | counter | `result` | Attempts to [reload](security.md#reloading-certificates-and-tokens) the TLS files and tokens file while running; `result` is `ok` or `error`. Only appears after the first attempt |
| `beacon_heartbeat_flushes_total` | counter | `result` | Writes of [batched heartbeats](#postgresql-storage) to PostgreSQL that had heartbeats to write; `result` is `ok` or `error`. While writes fail, heartbeats go to the database one by one and get its error. Only appears with PostgreSQL and batching on, after the first write |
| `beacon_build_info` | gauge | `version`, `goversion` | Always `1`; describes the running build |

The standard `go_*` and `process_*` metrics are included too. Labels only take values Beacon defines, such as route patterns, operations and results, never values clients choose, such as instance IDs, service names, owners or raw paths. The number of series therefore does not grow with registrations, however many instances or names clients send. For counts per service, use `GET /api/v1/services`. Requests to `/metrics` itself are not counted.

A few useful queries:

```promql
# Registered instances, and how close Beacon is to registry.max_instances
beacon_instances

# Distinct services
beacon_services

# Instances expiring per minute, a sign of crashing or unreachable services
rate(beacon_expired_instances_total[5m]) * 60

# Share of heartbeats for unknown instances, which then register again
sum(rate(beacon_http_requests_total{route="POST /api/v1/services/{id}/heartbeat",code="404"}[5m]))
  / sum(rate(beacon_http_requests_total{route="POST /api/v1/services/{id}/heartbeat"}[5m]))
```

With [authentication](security.md#authentication) on, `/metrics` needs a token or client certificate with the `metrics` permission, like the API needs its own permissions. With [TLS](security.md#tls) on, Prometheus must scrape it with `scheme: https`:

```yaml
scrape_configs:
  - job_name: beacon
    scheme: https
    authorization:
      credentials_file: /etc/prometheus/beacon-token
    static_configs:
      - targets: ["beacon:8080"]
```

To scrape with a [client certificate](security.md#client-certificates-mtls) instead, replace `authorization` with `tls_config` holding `cert_file` and `key_file`.

Without authentication, `/metrics` is open like the rest of the API. Disable it with `BEACON_METRICS_ENABLED=false` if it must not be reachable.
