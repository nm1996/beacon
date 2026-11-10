<div align="center">

# Beacon

**Where services find each other.**

A lightweight, self-hosted service registry written in Go.<br>
Services register themselves, send heartbeats, and discover each other over a small HTTP API.

[![CI](https://github.com/nm1996/beacon/actions/workflows/ci.yml/badge.svg)](https://github.com/nm1996/beacon/actions/workflows/ci.yml)
[![Go version](https://img.shields.io/badge/go-1.26%2B-00ADD8?logo=go&logoColor=white)](go.mod)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
![Status: work in progress](https://img.shields.io/badge/status-work%20in%20progress-orange)

</div>

> [!NOTE]
> Beacon is in early development. The HTTP API is not versioned yet and may change before the first release.

## Contents

- [Why Beacon](#why-beacon)
- [Features](#features)
- [Quick start](#quick-start)
- [How it works](#how-it-works)
- [HTTP API](#http-api)
- [Configuration](#configuration)
- [Limitations](#limitations)
- [Development](#development)
- [Project layout](#project-layout)
- [Roadmap](#roadmap)
- [Contributing](#contributing)
- [License](#license)

## Why Beacon

In a distributed system, services need to know where their peers are running. Full-featured tools like Consul, etcd or ZooKeeper solve this, but they bring clustering, consensus and operational weight that many small deployments do not need.

Beacon covers the core of the problem and nothing more: one small binary, no external dependencies at runtime, and an API you can call with `curl`.

## Features

| | Feature |
|:-:|---|
| ✅ | Register, discover and unregister service instances over HTTP |
| ✅ | Unique ID for every registered instance |
| ✅ | Heartbeats keep an instance alive |
| ✅ | Instances without a recent heartbeat are removed automatically |
| ✅ | Input validation with clear `400` responses |
| ✅ | Graceful shutdown on `SIGINT` / `SIGTERM` |
| ✅ | `GET /health` endpoint for liveness checks |
| ✅ | Server timeouts and request size limits |
| ✅ | Structured JSON logs |
| ✅ | TOML configuration file with validation |
| ✅ | Environment variable overrides |
| ✅ | Consistent JSON error responses with stable codes |
| ✅ | Versioned API under `/api/v1` |
| 🔜 | Native Go client with automatic heartbeats |
| 🔜 | Docker image and Compose example |
| 🔜 | Prometheus metrics |

## Quick start

You need [Go 1.26](https://go.dev/dl/) or newer, or [mise](https://mise.jdx.dev/) to install the pinned toolchain for you.

```bash
git clone https://github.com/nm1996/beacon.git
cd beacon

go run ./cmd/beacon
# {"time":"...","level":"INFO","msg":"beacon listening","addr":":8080","version":"..."}
```

Check that it is up:

```bash
curl -s localhost:8080/health
# {"status":"ok"}
```

Register an instance, look it up, and keep it alive:

```bash
# Register an instance; the response holds its ID and how long it may go without a heartbeat
curl -s -X POST localhost:8080/api/v1/services \
  -d '{"name": "orders", "address": "10.0.0.12", "port": 8081}'
# {"id":"8c0f3c56-5d7e-4a63-9a39-2b7c1e5e8f40","ttl_seconds":30}

ID=8c0f3c56-5d7e-4a63-9a39-2b7c1e5e8f40

# Find every instance of "orders"
curl -s 'localhost:8080/api/v1/services?name=orders'
# [{"id":"8c0f3c56-...","name":"orders","address":"10.0.0.12","port":8081,"last_heartbeat":"2026-11-10T16:00:00Z"}]

# Tell Beacon the instance is still alive (repeat well within ttl_seconds)
curl -s -X POST localhost:8080/api/v1/services/$ID/heartbeat

# Remove the instance when it shuts down
curl -s -X DELETE localhost:8080/api/v1/services/$ID
```

## How it works

Each running copy of a service is an **instance**. Its life in Beacon looks like this:

```text
  register ──► alive ──► heartbeat ──► alive ──► ...
                 │                       │
                 │  no heartbeat         │  unregister
                 │  for 30 seconds       │
                 ▼                       ▼
              removed                 removed
```

1. **Register.** The instance sends its name, address and port, and gets back a unique ID.
2. **Heartbeat.** The instance sends its ID regularly to show it is still running.
3. **Discover.** Other services ask for instances by name and connect to one of them.
4. **Expire.** Every 5 seconds Beacon removes instances that have not sent a heartbeat for more than 30 seconds.
5. **Unregister.** An instance that shuts down cleanly removes itself straight away.

Instances of the same service share a name. Registering again creates a new instance with a new ID; it does not update an existing one.

## HTTP API

All service endpoints live under `/api/v1`; `/health` is not versioned. Response bodies are JSON, including errors.

| Method | Path | Purpose |
|---|---|---|
| `POST` | [`/api/v1/services`](#post-apiv1services) | Register a new instance |
| `GET` | [`/api/v1/services`](#get-apiv1services) | List instances, optionally by name |
| `POST` | [`/api/v1/services/{id}/heartbeat`](#post-apiv1servicesidheartbeat) | Keep an instance alive |
| `DELETE` | [`/api/v1/services/{id}`](#delete-apiv1servicesid) | Remove an instance |
| `GET` | [`/health`](#get-health) | Check that Beacon is running |

Unknown paths return `404`, and a known path called with the wrong method returns `405`. Only registration reads a request body. It must be a single JSON object of at most 64 KiB (configurable) containing only the fields documented below. Anything else (unknown fields, a second JSON value, trailing text, an oversized body) is rejected with `400` and the code `invalid_request_body`.

### Errors

Every error response has the same shape:

```json
{ "error": { "code": "service_not_found", "message": "No service is registered with this ID" } }
```

`code` is stable and meant for programs; `message` is meant for people and may change.

| Status | Code | When |
|---|---|---|
| `400` | `invalid_request_body` | The body is not a single JSON object within the size limit with only the documented fields |
| `400` | `invalid_service` | A registration field breaks a rule, for example a port outside `1`–`65535` |
| `404` | `service_not_found` | No instance is registered with the given ID |
| `404` | `not_found` | No endpoint exists at the path |
| `405` | `method_not_allowed` | The endpoint exists but not for this method; the `Allow` header lists the supported one |
| `500` | `internal_error` | Something unexpected went wrong on the server |

### `POST /api/v1/services`

```json
{ "name": "orders", "address": "10.0.0.12", "port": 8081 }
```

| Field | Rule |
|---|---|
| `name` | Required, not blank |
| `address` | Required, not blank. Host name or IP address |
| `port` | Required, between `1` and `65535` |

| Status | Meaning |
|---|---|
| `201 Created` | Registered. The `Location` header points at the new instance |
| `400 Bad Request` | The body is invalid or a field breaks a rule above |

```json
{ "id": "8c0f3c56-5d7e-4a63-9a39-2b7c1e5e8f40", "ttl_seconds": 30 }
```

`ttl_seconds` is how long the instance may go without a heartbeat before Beacon removes it. Sending a heartbeat every third of that leaves room for a missed one.

### `GET /api/v1/services`

| Query parameter | Meaning |
|---|---|
| `name` | Optional. Only return instances with this name |

Always returns `200 OK` with a JSON array, which is empty when nothing matches:

```json
[
  { "id": "8c0f3c56-...", "name": "orders", "address": "10.0.0.12", "port": 8081, "last_heartbeat": "2026-11-10T16:00:00Z" },
  { "id": "1b9e47a2-...", "name": "orders", "address": "10.0.0.13", "port": 8081, "last_heartbeat": "2026-11-10T16:00:04Z" }
]
```

`last_heartbeat` is in UTC, in RFC 3339 format. The order of instances is not defined.

### `POST /api/v1/services/{id}/heartbeat`

No request body.

| Status | Meaning |
|---|---|
| `204 No Content` | Heartbeat recorded |
| `404 Not Found` | No instance with that ID, possibly because it already expired |

An instance that gets `404` should register again.

### `DELETE /api/v1/services/{id}`

No request body.

| Status | Meaning |
|---|---|
| `204 No Content` | Instance removed |
| `404 Not Found` | No instance with that ID |

### `GET /health`

Always returns `200 OK` while the process is serving requests. Use it for container and load balancer health checks.

```json
{ "status": "ok" }
```

## Configuration

Beacon reads its settings from a TOML file passed with `--config` and from environment variables:

```bash
beacon --config /etc/beacon/beacon.toml
```

Each setting is taken from the first of these that provides it:

1. An environment variable such as `BEACON_SERVER_PORT`
2. The TOML file given with `--config`
3. The built-in default

A file only needs the keys it changes, and no file is needed at all. [`configs/beacon.example.toml`](configs/beacon.example.toml) lists every key with its default value.

| Key | Environment variable | Default | Meaning |
|---|---|---|---|
| `server.host` | `BEACON_SERVER_HOST` | `"0.0.0.0"` | Interface to listen on |
| `server.port` | `BEACON_SERVER_PORT` | `8080` | TCP port, `1`–`65535` |
| `server.read_timeout` | `BEACON_SERVER_READ_TIMEOUT` | `"5s"` | Time allowed to read a request, headers included |
| `server.write_timeout` | `BEACON_SERVER_WRITE_TIMEOUT` | `"10s"` | Time allowed to write a response |
| `server.idle_timeout` | `BEACON_SERVER_IDLE_TIMEOUT` | `"60s"` | Time an idle keep-alive connection stays open |
| `server.shutdown_timeout` | `BEACON_SERVER_SHUTDOWN_TIMEOUT` | `"10s"` | Time in-flight requests get to finish on shutdown |
| `server.max_request_body_bytes` | `BEACON_SERVER_MAX_REQUEST_BODY_BYTES` | `65536` | Largest accepted request body |
| `registry.service_ttl` | `BEACON_REGISTRY_SERVICE_TTL` | `"30s"` | Time without a heartbeat before an instance is removed |
| `registry.cleanup_interval` | `BEACON_REGISTRY_CLEANUP_INTERVAL` | `"5s"` | How often stale instances are removed |
| `logging.level` | `BEACON_LOGGING_LEVEL` | `"info"` | `debug`, `info`, `warn` or `error` |
| `logging.format` | `BEACON_LOGGING_FORMAT` | `"json"` | `json` or `text` |

```bash
BEACON_SERVER_PORT=9090 BEACON_LOGGING_FORMAT=text beacon --config /etc/beacon/beacon.toml
```

Environment variables hold plain values without TOML quotes, for example `BEACON_REGISTRY_SERVICE_TTL=2m`. A variable that is set applies even when it is empty, so `BEACON_SERVER_HOST=` listens on all interfaces.

Durations use Go syntax such as `"500ms"`, `"5s"` or `"1m30s"`. All durations and sizes must be positive, `registry.service_ttl` must be at least `1s`, and `registry.cleanup_interval` must not be longer than `registry.service_ttl`.

Beacon refuses to start, and lists every problem it found, if the file is missing, is not valid TOML or contains an unknown key, if an environment variable cannot be parsed, or if a setting ends up out of range. Range errors name the TOML key, for example `server.port`, whichever source set it.

Logs go to standard output. Run `beacon --version` to print the build version.

## Limitations

Beacon is deliberately small. Before you depend on it, know that:

- **State lives in memory.** Restarting Beacon forgets every instance. Services find out on their next heartbeat (`404`) and should register again.
- **It runs as a single node.** There is no clustering or replication.
- **There is no authentication.** Run it on a trusted network only.

## Development

Beacon uses [mise](https://mise.jdx.dev/) to pin the Go and `golangci-lint` versions and to run common tasks.

```bash
mise install   # install the pinned tools
mise run check # format, vet, lint and run tests with the race detector
```

| Task | What it does |
|---|---|
| `mise run fmt` | Format Go code with `gofmt` |
| `mise run fmt-check` | Fail if any file is not formatted |
| `mise run vet` | Run `go vet` |
| `mise run lint` | Run `golangci-lint` |
| `mise run test` | Run tests |
| `mise run test-race` | Run tests with the race detector |
| `mise run vuln` | Scan for known vulnerabilities with `govulncheck` |
| `mise run build` | Build `bin/beacon` |
| `mise run run` | Start the server on `:8080` |
| `mise run check` | Format, vet, lint, race tests and vulnerability scan |

Run `mise tasks` to list them all. CI runs the same tasks on every pull request.

## Project layout

```text
.
├── cmd/beacon/          # Entry point: flags, config, logging, signals, cleanup worker and shutdown
├── internal/
│   ├── api/             # HTTP routes and handlers
│   ├── bootstrap/       # Builds the registry and HTTP server and connects them
│   ├── config/          # Loads and validates the TOML configuration
│   └── registry/        # In-memory registry, validation and stale-instance cleanup
├── configs/             # Example configuration
├── .github/workflows/   # CI
├── .golangci.yml        # Linter configuration
└── .mise.toml           # Pinned tools and development tasks
```

## Roadmap

**Done**

- [x] In-memory registry with register, heartbeat, discovery and unregister
- [x] Request validation
- [x] Automatic removal of stale instances
- [x] Graceful shutdown
- [x] Server timeouts and request size limits
- [x] Health endpoint
- [x] Structured logging
- [x] CI pipeline
- [x] TOML configuration
- [x] Consistent JSON error responses
- [x] Versioned REST API
- [x] Environment variable overrides for configuration

**Next**

- [ ] Go client library
- [ ] Grpc support
- [ ] Docker image and Compose example
- [ ] Prometheus metrics
- [ ] First release, `v0.1.0`

**After v0.1.0**

- [ ] Cache and database
- [ ] Scale, multiple nodes
- [ ] Idempotency

## Contributing

Suggestions, bug reports and pull requests are welcome. Please read [CONTRIBUTING.md](CONTRIBUTING.md) first; it covers the development workflow, commit message format and what a good pull request includes.

## License

Beacon is released under the [MIT License](LICENSE).
