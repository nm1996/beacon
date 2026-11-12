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
