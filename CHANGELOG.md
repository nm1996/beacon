# Changelog

All notable changes to Beacon are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and Beacon uses
[Semantic Versioning](https://semver.org/) as described in the README's
[Versioning](README.md#versioning) section.

Add an entry under **Unreleased** in the same pull request as the change.

## [Unreleased]

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
- A limit on registered instances: `registry.max_instances`
  (`BEACON_REGISTRY_MAX_INSTANCES`, default 10000). Registrations beyond it
  get `429 quota_exceeded` until instances unregister or expire, which bounds
  memory use and the size of discovery responses. The limit is checked in the
  same step as the insert, so concurrent registrations cannot overshoot it.
  Refused creates are counted as `result="full"` in
  `beacon_registry_operations_total`. `client.ErrQuotaExceeded` matches the
  error.

### Changed

- Stricter registration rules. A service name must be 1–63 lowercase
  letters, digits, `.`, `_` or `-`, starting and ending with a letter or
  digit, so `orders`, `Orders` and `orders ` can no longer become three
  different services. An address must be an IP address or a valid host name
  of at most 253 characters, without a scheme, port or path. Registrations
  that break either rule get `400 invalid_service`; names are not changed to
  fit. Both lengths can be changed, between 1 and 253, with
  `registry.max_name_length` and `registry.max_address_length`
  (`BEACON_REGISTRY_MAX_NAME_LENGTH`, `BEACON_REGISTRY_MAX_ADDRESS_LENGTH`);
  IP addresses are not held to the address limit.

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

### Fixed

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

[Unreleased]: https://github.com/nm1996/beacon/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/nm1996/beacon/releases/tag/v0.1.0
