# Changelog

All notable changes to Beacon are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and Beacon uses
[Semantic Versioning](https://semver.org/) as described in the README's
[Versioning](README.md#versioning) section.

Add an entry under **Unreleased** in the same pull request as the change.

## [Unreleased]

## [0.3.0] - 2026-11-24

Upgrading from 0.2.0 needs no changes: the configuration and the HTTP API
stay compatible, and PostgreSQL, several nodes, the gRPC API, trusted proxies
and the lookup cache are all off until configured. The Go client's
`Register` and `Lease` behave differently, as described under Changed.

### Added

- `server.trusted_proxies` (`BEACON_SERVER_TRUSTED_PROXIES`): addresses or
  CIDR ranges of proxies whose `X-Forwarded-For` header gives the client
  address that rate limits are keyed by. Beacon takes the rightmost
  address that is not a listed proxy, so entries a client adds are never
  believed. Empty by default, which keeps ignoring the header. See
  [design record 0001](docs/design/0001-trusted-proxies.md).
- An optional `id` for entries in the tokens file. It owns the instances the
  entry registers and is what `registry.max_instances_per_owner` and the
  registration rate limit count, so an entry can be renamed, or its token
  rotated under a new entry, without its instances changing owner. Entries
  that share an `id` share ownership. Without it the ID is the `name`, as
  before. Refused-request logs add `caller_id`. See
  [design record 0002](docs/design/0002-owner-id.md).
- Registrations may name their instance `id`. Registering the same `id`
  again with the same name, address and port answers `200 OK` with the same
  instance, refreshed as by a heartbeat, so the ID survives Beacon restarts
  and retries. Another caller's ID, or the same ID with a different name,
  address or port, gets `409 instance_exists`. See
  [design record 0003](docs/design/0003-stable-instance-ids.md).
- The `Idempotency-Key` header makes repeating a registration safe: Beacon
  derives the instance ID from the key and the caller, so a retry finds the
  instance the first attempt created. The same key with a different body
  gets `422 idempotency_key_reused`. See
  [design record 0004](docs/design/0004-idempotency.md).
- `beacon_registry_operations_total` has the result `exists`.
- `GET /api/v1/services` sends a weak `ETag` over the instances' IDs, names,
  addresses and ports, and answers `304 Not Modified` without a body when
  `If-None-Match` lists it.
- `client.Resolver`, a client-side cache and load balancer for one service:
  `NewResolver` fetches once, a background goroutine refreshes with
  conditional requests, `Pick` chooses an instance without a request
  (`client.RoundRobin`, the default, `client.Random`, or any
  `client.Picker`), and while Beacon is unreachable it keeps serving the
  last instances for up to `client.WithMaxStale`, then returns
  `client.ErrStale`. With `client.WithOutlierDetection`, off by default,
  instances that `Resolver.Report` shows failing several times in a row are
  skipped for a while, never more than half of them nor the last one. See
  [design record 0005](docs/design/0005-client-cache-and-load-balancing.md).
- PostgreSQL storage: `storage.backend = "postgres"` with
  `storage.postgres.url` (`BEACON_STORAGE_POSTGRES_URL`) keeps instances
  across restarts, shared by every Beacon process using the database.
  Beacon creates and migrates its tables itself, and the instance limits
  stay exact across processes. Built on `github.com/jackc/pgx/v5`, a new
  direct dependency. See
  [design record 0006](docs/design/0006-postgres-and-cache.md).
- With PostgreSQL, heartbeats are batched: each process checks an
  instance's first heartbeat with the database, buffers the following ones,
  and writes them every `storage.postgres.heartbeat_flush_interval`
  (`BEACON_STORAGE_POSTGRES_HEARTBEAT_FLUSH_INTERVAL`, 1s by default) in one
  statement per thousand instances. When no write has succeeded for two
  intervals, a process stops buffering and sends each heartbeat to the
  database, so it answers with an error instead of acknowledging heartbeats
  it cannot store. `beacon_heartbeat_flushes_total{result}` counts the
  writes.
- `registry.cache_ttl` (`BEACON_REGISTRY_CACHE_TTL`) caches lookup results
  in each process for up to a third of the service TTL; off by default.
- Several nodes over one PostgreSQL database: each node records itself in
  `beacon_nodes` every 5 seconds and leaves on shutdown, every node runs
  cleanup, and `GET /api/v1/nodes` (with `manage`) and the
  `beacon_cluster_nodes` gauge list the live ones. `server.node_id`
  (`BEACON_SERVER_NODE_ID`) names a node. See
  [design record 0007](docs/design/0007-multiple-nodes.md).
- Rate limits are divided by the number of live nodes, so behind a
  round-robin load balancer the cluster enforces about the configured
  limit. See [design record 0008](docs/design/0008-cluster-rate-limits.md).
- A gRPC API on `grpc.port` (`BEACON_GRPC_PORT`), off by default:
  `beacon.v1.RegistryService` with `Register`, `Heartbeat`, `Unregister`
  and `ListInstances`, defined in `api/beacon/v1/registry.proto` with the
  generated Go code next to it. It uses the HTTP API's TLS settings,
  authentication, permissions and rate limiters, and reports errors with
  standard codes and a `google.rpc.ErrorInfo` whose reason is the HTTP
  error code. The health service and reflection are included, as are
  `beacon_grpc_requests_total` and `beacon_grpc_request_duration_seconds`.
  Adds `google.golang.org/grpc` and `google.golang.org/protobuf`. See
  [design record 0009](docs/design/0009-grpc-api.md).
- `client.Registration.ID` chooses the instance ID, and `client.ErrConflict`
  matches `instance_exists` and `idempotency_key_reused`.

### Changed

- The README is now a short introduction. The API, configuration, security,
  operations, client and development guides moved to `docs/guides/`, which
  release archives include, and the new
  [Guarantees and failure behavior](docs/guides/guarantees.md) guide states
  what is consistent across nodes, what may lag, and what happens when
  PostgreSQL, a node or shutdown fails.
- `client.Register` sends an `Idempotency-Key` and retries, up to three
  attempts in all, when Beacon cannot be reached or a proxy answers `502`,
  `503` or `504`. It used to fail on the first lost response, so `Announce`
  could fail on one dropped packet.
- A `Lease` registers again under the ID it had, so the instance keeps its
  ID across Beacon restarts. Only if another instance took the ID meanwhile,
  or another caller owns it (`403 instance_not_owned`), does it register
  under a new one, with one warning in the log.

## [0.2.0] - 2026-11-13

Upgrading from 0.1.0 needs attention in three places, each described below:
Beacon now listens on `127.0.0.1` unless `server.host` says otherwise
(containers are unaffected), registrations must follow the new name and
address rules, and `beacon_instances` no longer has a `service` label.

### Added

- HTTPS: setting `tls.cert_file` and `tls.key_file` (`BEACON_TLS_CERT_FILE`,
  `BEACON_TLS_KEY_FILE`) serves the API, `/health` and `/metrics` over TLS
  1.2 or newer, with HTTP/2. Beacon refuses to start if only one of the two is
  set or the files cannot be loaded. Plain HTTP stays the default.
- `beacon --health-check` uses HTTPS when TLS is configured.
- A startup warning when Beacon serves plain HTTP on a non-loopback address.
- Bearer-token authentication: with `auth.enabled` and `auth.tokens_file`
  (`BEACON_AUTH_ENABLED`, `BEACON_AUTH_TOKENS_FILE`), every endpoint except
  `GET /health` needs `Authorization: Bearer <token>`. The tokens file lists
  each token's name, SHA-256 hash and optional permissions (`register`,
  `discover`, `metrics`, `manage`); a token without a permission list gets
  `register`, `discover` and `metrics`. Refused requests get
  `401 unauthorized` or `403 forbidden`. Authentication is off by default.
- `beacon --hash-token`, which reads a token from standard input and prints
  the hash for the tokens file.
- `beacon_auth_failures_total{reason}` metric counting refused requests.
- `client.WithToken`, and `client.ErrUnauthorized` and `client.ErrForbidden`
  for refused requests.
- Client certificates (mTLS): `tls.client_ca_file`
  (`BEACON_TLS_CLIENT_CA_FILE`) asks clients for a certificate and verifies
  any they send against that CA bundle. `[[certificates]]` entries in the
  tokens file name a certificate by a DNS or URI subject alternative name,
  such as a SPIFFE ID, and give it the same permissions tokens get. Clients
  without a certificate can still connect, use `/health` and send a bearer
  token; a request with an `Authorization` header is judged by its token.
  Requires TLS and `auth.enabled`.
- Refused-request logs name the caller under `caller`, and an unlisted
  certificate's names under `certificate_names`.
- Reloading the TLS certificate and key, the client CA bundle and the tokens
  file without a restart: on `SIGHUP`, and every `server.reload_interval`
  (`BEACON_SERVER_RELOAD_INTERVAL`, off by default) when their contents
  change. A reload replaces all of them or none, so a missing or invalid file
  keeps the credentials already in use. New connections and every request
  after a reload use the new files.
- `beacon_credential_reloads_total{result}` metric counting reload attempts.
- Instance ownership: with authentication on, Beacon records which token or
  certificate registered each instance, and only that caller may send its
  heartbeats or unregister it. Others get `403 instance_not_owned`. The new
  `manage` permission, which must be listed together with `register` and is
  never granted by default, lifts that limit, for example for an operator
  removing a stuck instance. Owners are logged, not shown by the API.
  Without authentication nothing changes.
- `client.ErrNotOwned`, which matches `instance_not_owned` errors.
- Paging for `GET /api/v1/services`: `limit` (1 to 1000) returns at most that
  many instances and, when more follow, a `Link: <...>; rel="next"` header
  with an opaque `cursor` for the next page. The body stays a JSON array and
  requests without `limit` return everything as before, so existing clients
  keep working. Invalid parameters get `400 invalid_query`.
- `client.Discover` fetches 500 instances at a time and follows the page
  links, so it no longer fails when a registry outgrows one response. It
  returns an error instead of a partial or endless result when a server or
  proxy breaks the paging: a next link without a cursor, a cursor seen
  before, or more than 10000 pages.
- A limit on registered instances: `registry.max_instances`
  (`BEACON_REGISTRY_MAX_INSTANCES`, default 10000). Registrations beyond it
  get `429 quota_exceeded` until instances unregister or expire, which bounds
  memory use and the size of discovery responses. The limit is checked in the
  same step as the insert, so concurrent registrations cannot overshoot it.
  Refused creates are counted as `result="full"` in
  `beacon_registry_operations_total`. `client.ErrQuotaExceeded` matches the
  error.
- A per-owner limit: `registry.max_instances_per_owner`
  (`BEACON_REGISTRY_MAX_INSTANCES_PER_OWNER`, default 0, no limit). With
  authentication on, each token or certificate may hold at most that many
  instances, so one caller cannot take every place `registry.max_instances`
  allows. Registrations beyond it get `429 quota_exceeded` and are counted as
  `result="owner_full"`. Like the total, it is checked in the same step as the
  insert. Without authentication it does not apply.
- Rate limits, each a token bucket per client:
  - Registrations per caller (the token or certificate with authentication
    on, the client network without it): `rate_limit.registrations_per_second`
    and `rate_limit.registration_burst`, default 20 per second in bursts of
    200.
  - Failed authentication per client network: `rate_limit.auth_failures_per_second`
    and `rate_limit.auth_failure_burst`, default 1 per second in bursts of
    20. Beyond it every request from that network is refused until it is
    back under the limit. Each request reserves a place before its
    credentials are checked, so concurrent attempts cannot overshoot the
    burst; the place is used up only if the credentials are invalid. Like
    the buckets, places held by networks that have not failed before are
    bounded, so a flood from many networks cannot grow memory.

  Refused requests get `429 rate_limited` with a `Retry-After` header and
  are counted in `beacon_rate_limited_total{limit}`. A client network is an
  IPv4 address or an IPv6 `/64`. A rate of `0` turns a limit off. Each
  setting has a `BEACON_RATE_LIMIT_*` environment variable. Built on
  `golang.org/x/time/rate`, a new direct dependency.
- `client.ErrRateLimited`, which matches `rate_limited` errors and any `429`
  without Beacon's error format.

### Changed

- Beacon listens on `127.0.0.1` by default instead of `0.0.0.0`, so a fresh
  install without TLS or authentication is not reachable from the network.
  Set `server.host` to `"0.0.0.0"` to accept remote connections. The
  container image sets `BEACON_SERVER_HOST=0.0.0.0`, so containers behave as
  before. The README has a new section on exposing Beacon safely.
- Stricter registration rules. A service name must be 1–63 lowercase letters,
  digits, `.`, `_` or `-`, starting and ending with a letter or digit, with no
  two of `.`, `_` and `-` in a row, so `orders`, `Orders` and `orders ` can no
  longer become three different services. An address must be an IP address or
  a valid host name of at most 253 characters, without a scheme, port or path.
  Registrations that break either rule get `400 invalid_service`; names are
  not changed to fit. Both lengths can be changed, between 1 and 253, with
  `registry.max_name_length` and `registry.max_address_length`
  (`BEACON_REGISTRY_MAX_NAME_LENGTH`, `BEACON_REGISTRY_MAX_ADDRESS_LENGTH`);
  IP addresses are not held to the address limit.

- `GET /api/v1/services` returns instances sorted by ID instead of in no
  particular order.
- Heartbeats and unregistrations refused because another caller owns the
  instance are counted as `result="not_owned"` in
  `beacon_registry_operations_total`.

- `registry.service_ttl` must now be a whole number of seconds. Clients are
  told it as `ttl_seconds`, so a TTL such as `1.5s` used to reach them as
  `1`.
- `Lease.Close` can be retried. A call whose context ended, or that could not
  reach Beacon, used to be final; now the heartbeats stop on the first call
  and a later call with a new context finishes unregistering. Once
  unregistered, further calls return `nil`.
- `BenchmarkMixedParallel` runs at 100 to 100000 instances, and
  `mise run bench-parallel` runs it at 1, 2, 4 and 8 goroutines. Filling the
  registry no longer counts towards its time. The README gives the results.

### Fixed

- Lookups returned instances whose TTL had passed until the next cleanup
  removed them, up to `registry.cleanup_interval` later. They are now left
  out as soon as their TTL passes; a heartbeat before cleanup still brings
  an instance back.
- A `Lease` heartbeat or registration that never got an answer, as on a
  connection that died without being closed, held up the lease for the
  `http.Client`'s whole timeout, or for good with a client without one, so
  the instance could expire although Beacon was reachable. Each request
  the lease sends now gives up after a sixth of the TTL.
- Looking a service up by name scanned the whole registry and reserved
  room for every instance, holding the lock heartbeats need. With 10000
  instances, four clients looking up in a loop held heartbeats to about 400
  a second, fewer than those instances send, so live instances could
  expire. The in-memory backend now keeps an index by name, and lookups
  by name cost what they return: 27 µs rather than 0.94 ms at 10000, and
  heartbeats during such lookups 84 µs rather than 2.5 ms on average.
  Registering and removing cost about 0.4 µs more.
- `beacon_instances` carried a `service` label taken from client-chosen
  service names, so a client registering many names could create any number
  of time series. It is now one series counting every instance, and the new
  `beacon_services` gauge counts distinct names. Per-service counts come from
  the API. The README no longer claims series stay bounded when they did not.
- The `method` label of the HTTP metrics took any method a client sent; methods
  outside the standard set are now counted as `other`.
- `mise run check` no longer rewrites unformatted files; it fails instead.
  The release workflow runs it, so a release is now always built from the
  tagged source.
- A credential reload published the new TLS configuration and the new
  identities in two steps, so a request could briefly pair one with the
  other. Both are now published as one snapshot, and the reload log line
  reports its `generation`.
- `client.WithHTTPClient(nil)` and `client.WithLogger(nil)` no longer cause a
  panic; they keep the defaults.
- A sentence in the README's Go client section that rendered after a code
  block's closing fence.

## [0.1.0] - 2026-11-11

First release. Beacon is a single-node, in-memory service registry with an
HTTP API.

### Added

- Service registration, discovery by name, heartbeats and unregistration
  under `/api/v1/services`.
- Automatic removal of instances that stop sending heartbeats, with a
  configurable TTL and cleanup interval.
- Registration responses that report `ttl_seconds`, and listings that include
  each instance's `id` and `last_heartbeat`.
- Request validation and a single JSON error format with stable error codes.
- `GET /health` reporting status and the running version.
- TOML configuration (`--config`) with `BEACON_*` environment variable
  overrides and strict validation.
- `storage.backend` setting (`BEACON_STORAGE_BACKEND`) that selects where
  instances are kept. `memory` is the only backend so far.
- Server timeouts, a request body size limit, structured JSON or text logs,
  and graceful shutdown on `SIGINT` / `SIGTERM`.
- `beacon --version`, and release archives for Linux, macOS and Windows on
  amd64 and arm64.
- Container image on GHCR for `linux/amd64` and `linux/arm64`, based on
  distroless and running as non-root, with a Compose example.
- `beacon --health-check`, which the image uses as its health check.
- Go client in `github.com/nm1996/beacon/client` for every v1 endpoint,
  with typed errors matching `ErrNotFound` and `ErrInvalid`.
- `client.Announce`, which registers an instance and keeps it alive with
  heartbeats, retries with backoff and registers again after Beacon
  restarts, until `Lease.Close` unregisters it.
- Prometheus metrics at `GET /metrics` for HTTP requests, storage backend
  operations, registered and expired instances and the running build,
  controlled by `metrics.enabled` (`BEACON_METRICS_ENABLED`).

[Unreleased]: https://github.com/nm1996/beacon/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/nm1996/beacon/releases/tag/v0.3.0
[0.2.0]: https://github.com/nm1996/beacon/releases/tag/v0.2.0
[0.1.0]: https://github.com/nm1996/beacon/releases/tag/v0.1.0
