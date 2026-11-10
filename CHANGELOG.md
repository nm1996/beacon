# Changelog

All notable changes to Beacon are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and Beacon uses
[Semantic Versioning](https://semver.org/) as described in the README's
[Versioning](README.md#versioning) section.

Add an entry under **Unreleased** in the same pull request as the change.

## [Unreleased]

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
- Server timeouts, a request body size limit, structured JSON or text logs,
  and graceful shutdown on `SIGINT` / `SIGTERM`.
- `beacon --version`, and release archives for Linux, macOS and Windows on
  amd64 and arm64.

[Unreleased]: https://github.com/nm1996/beacon/commits/main
