<div align="center">

# Beacon

**Where services find each other.**

A lightweight, self-hosted service registry written in Go.<br>
Services register themselves, send heartbeats, and discover each other over a small HTTP API.

[![CI](https://github.com/nm1996/beacon/actions/workflows/ci.yml/badge.svg)](https://github.com/nm1996/beacon/actions/workflows/ci.yml)
[![Go version](https://img.shields.io/badge/go-1.27-00ADD8?logo=go&logoColor=white)](go.mod)
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
| 🔜 | TOML configuration file and environment overrides |
| 🔜 | Versioned API (`/api/v1`) with consistent JSON errors |
| 🔜 | Native Go client with automatic heartbeats |
| 🔜 | Docker image and Compose example |
| 🔜 | Prometheus metrics |

## Quick start

You need [Go 1.27](https://go.dev/dl/) or newer, or [mise](https://mise.jdx.dev/) to install the pinned toolchain for you.

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
# Register an instance and save its ID
curl -s -X POST localhost:8080/register \
  -d '{"name": "orders", "address": "10.0.0.12", "port": 8081}'
# {"id":"8c0f3c56-5d7e-4a63-9a39-2b7c1e5e8f40"}

# Find every instance of "orders"
curl -s 'localhost:8080/services?name=orders'
# [{"name":"orders","address":"10.0.0.12","port":8081}]

# Tell Beacon the instance is still alive (repeat well within 30 seconds)
curl -s -X POST localhost:8080/heartbeat -d '{"id": "8c0f3c56-5d7e-4a63-9a39-2b7c1e5e8f40"}'

# Remove the instance when it shuts down
curl -s -X DELETE localhost:8080/unregister -d '{"id": "8c0f3c56-5d7e-4a63-9a39-2b7c1e5e8f40"}'
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

All request and response bodies are JSON. Error responses are plain text for now.

| Method | Path | Purpose |
|---|---|---|
| `POST` | [`/register`](#post-register) | Register a new instance |
| `POST` | [`/heartbeat`](#post-heartbeat) | Keep an instance alive |
| `GET` | [`/services`](#get-services) | List instances, optionally by name |
| `DELETE` | [`/unregister`](#delete-unregister) | Remove an instance |
| `GET` | [`/health`](#get-health) | Check that Beacon is running |

Unknown paths return `404`, and a known path called with the wrong method returns `405`. Request bodies must be a single JSON object with only the fields documented below; unknown fields, a second JSON value or any other trailing data are rejected with `400`. Bodies larger than 64 KiB are rejected with `413`.

### `POST /register`

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
| `201 Created` | Registered. Body: `{"id": "<instance id>"}` |
| `400 Bad Request` | Malformed JSON or a field breaks a rule above |

### `POST /heartbeat`

```json
{ "id": "8c0f3c56-5d7e-4a63-9a39-2b7c1e5e8f40" }
```

| Status | Meaning |
|---|---|
| `204 No Content` | Heartbeat recorded |
| `400 Bad Request` | Malformed JSON or missing `id` |
| `404 Not Found` | No instance with that ID, possibly because it already expired |

An instance that gets `404` should register again.

### `GET /services`

| Query parameter | Meaning |
|---|---|
| `name` | Optional. Only return instances with this name |

Always returns `200 OK` with a JSON array, which is empty when nothing matches:

```json
[
  { "name": "orders", "address": "10.0.0.12", "port": 8081 },
  { "name": "orders", "address": "10.0.0.13", "port": 8081 }
]
```

The order of instances is not defined.

### `DELETE /unregister`

```json
{ "id": "8c0f3c56-5d7e-4a63-9a39-2b7c1e5e8f40" }
```

| Status | Meaning |
|---|---|
| `204 No Content` | Instance removed |
| `400 Bad Request` | Malformed JSON or missing `id` |
| `404 Not Found` | No instance with that ID |

### `GET /health`

Always returns `200 OK` while the process is serving requests. Use it for container and load balancer health checks.

```json
{ "status": "ok" }
```

## Configuration

Beacon does not read a configuration file yet. It currently runs with these fixed values:

| Setting | Value |
|---|---|
| Listen address | `:8080` |
| Instance TTL (time without a heartbeat before removal) | `30s` |
| Cleanup interval | `5s` |
| Read timeout (headers and body) | `5s` |
| Write timeout | `10s` |
| Idle connection timeout | `60s` |
| Maximum request body | `64 KiB` |
| Shutdown grace period | `10s` |

Logs are written to standard output as JSON. Run `beacon --version` to print the build version.

[`configs/beacon.example.toml`](configs/beacon.example.toml) shows the planned configuration format. It is not loaded by the server yet.

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
├── cmd/beacon/          # Entry point: logging, signals, cleanup worker and shutdown
├── internal/
│   ├── api/             # HTTP routes and handlers
│   ├── bootstrap/       # Builds the registry and HTTP server and connects them
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

**Next**

- [ ] TOML configuration with environment overrides
- [ ] Versioned REST API with consistent JSON errors
- [ ] Go client library
- [ ] Docker image and Compose example
- [ ] Prometheus metrics
- [ ] First release, `v0.1.0`

## Contributing

Suggestions, bug reports and pull requests are welcome. Please read [CONTRIBUTING.md](CONTRIBUTING.md) first; it covers the development workflow, commit message format and what a good pull request includes.

## License

Beacon is released under the [MIT License](LICENSE).
