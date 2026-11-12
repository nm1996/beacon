<div align="center">

# Beacon

**Where services find each other.**

A lightweight, self-hosted service registry written in Go.<br>
Services register themselves, send heartbeats, and discover each other over a small HTTP API.

[![CI](https://github.com/nm1996/beacon/actions/workflows/ci.yml/badge.svg)](https://github.com/nm1996/beacon/actions/workflows/ci.yml)
[![Go version](https://img.shields.io/badge/go-1.26%2B-00ADD8?logo=go&logoColor=white)](go.mod)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Latest release](https://img.shields.io/github/v/release/nm1996/beacon?sort=semver)](https://github.com/nm1996/beacon/releases/latest)

</div>

> [!NOTE]
> Beacon is before 1.0. The HTTP API is versioned under `/api/v1` and stays compatible within v1. The Go client and the configuration may still change in a minor release before 1.0; every such change is listed in the [CHANGELOG](CHANGELOG.md).

## Contents

- [Why Beacon](#why-beacon)
- [Features](#features)
- [Quick start](#quick-start)
- [How it works](#how-it-works)
- [HTTP API](#http-api)
- [Configuration](#configuration)
- [TLS](#tls)
- [Authentication](#authentication)
- [Running in Docker](#running-in-docker)
- [Go client](#go-client)
- [Metrics](#metrics)
- [Limitations](#limitations)
- [Development](#development)
- [Project layout](#project-layout)
- [Roadmap](#roadmap)
- [Versioning](#versioning)
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
| ✅ | Go client with automatic heartbeats and re-registration |
| ✅ | Container image (amd64, arm64) with a built-in health check, and a Compose example |
| ✅ | Prometheus metrics at `/metrics` |
| ✅ | Optional HTTPS (TLS 1.2+) |
| ✅ | Optional bearer-token authentication with per-token permissions |

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
# {"status":"ok","version":"..."}
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
| `401` | `unauthorized` | [Authentication](#authentication) is on and the request has no bearer token, a malformed `Authorization` header or a token Beacon does not accept |
| `403` | `forbidden` | The token is valid but lacks the permission this endpoint needs |
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

Always returns `200 OK` while the process is serving requests, with the version of the running build. Use it for container and load balancer health checks.

```json
{ "status": "ok", "version": "v0.1.0" }
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
| `tls.cert_file` | `BEACON_TLS_CERT_FILE` | `""` | PEM certificate chain to serve HTTPS with; see [TLS](#tls) |
| `tls.key_file` | `BEACON_TLS_KEY_FILE` | `""` | PEM private key for `tls.cert_file` |
| `auth.enabled` | `BEACON_AUTH_ENABLED` | `false` | Require a bearer token on every endpoint except `/health`; see [Authentication](#authentication) |
| `auth.tokens_file` | `BEACON_AUTH_TOKENS_FILE` | `""` | File listing the accepted tokens. Required when `auth.enabled` is `true` |
| `registry.service_ttl` | `BEACON_REGISTRY_SERVICE_TTL` | `"30s"` | Time without a heartbeat before an instance is removed |
| `registry.cleanup_interval` | `BEACON_REGISTRY_CLEANUP_INTERVAL` | `"5s"` | How often stale instances are removed |
| `storage.backend` | `BEACON_STORAGE_BACKEND` | `"memory"` | Where instances are kept. Only `memory` exists today |
| `metrics.enabled` | `BEACON_METRICS_ENABLED` | `true` | Serve Prometheus metrics at `GET /metrics` |
| `logging.level` | `BEACON_LOGGING_LEVEL` | `"info"` | `debug`, `info`, `warn` or `error` |
| `logging.format` | `BEACON_LOGGING_FORMAT` | `"json"` | `json` or `text` |

```bash
BEACON_SERVER_PORT=9090 BEACON_LOGGING_FORMAT=text beacon --config /etc/beacon/beacon.toml
```

Environment variables hold plain values without TOML quotes, for example `BEACON_REGISTRY_SERVICE_TTL=2m`. A variable that is set applies even when it is empty, so `BEACON_SERVER_HOST=` listens on all interfaces.

Durations use Go syntax such as `"500ms"`, `"5s"` or `"1m30s"`. All durations and sizes must be positive, `registry.service_ttl` must be at least `1s`, and `registry.cleanup_interval` must not be longer than `registry.service_ttl`.

Beacon refuses to start, and lists every problem it found, if the file is missing, is not valid TOML or contains an unknown key, if an environment variable cannot be parsed, or if a setting ends up out of range. Range errors name the TOML key, for example `server.port`, whichever source set it.

Logs go to standard output. Run `beacon --version` to print the build version.

## TLS

Beacon serves plain HTTP unless you give it a certificate. Set both `tls.cert_file` and `tls.key_file` to serve HTTPS instead:

```toml
[tls]
cert_file = "/etc/beacon/tls/cert.pem"
key_file = "/etc/beacon/tls/key.pem"
```

- Both files are PEM. `cert_file` holds the server certificate first, followed by any intermediate certificates. `key_file` holds its private key.
- Set both or neither. Beacon refuses to start if only one is set, or if the files are missing, unreadable or do not belong together.
- With TLS on, the API, `/health` and `/metrics` are served over HTTPS only, on the same port. HTTP/2 is offered. TLS 1.2 is the minimum version.
- The files are read once at startup. Restart Beacon to pick up a renewed certificate.
- Without TLS, Beacon logs a warning at startup if it listens on anything other than a loopback address.

For local testing, make a self-signed certificate valid for `localhost`:

```bash
mkdir -p certs
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -days 30 \
  -subj "/CN=localhost" -addext "subjectAltName=DNS:localhost,IP:127.0.0.1" \
  -keyout certs/key.pem -out certs/cert.pem

BEACON_TLS_CERT_FILE=certs/cert.pem BEACON_TLS_KEY_FILE=certs/key.pem go run ./cmd/beacon
curl --cacert certs/cert.pem https://localhost:8080/health
```

In production, use a certificate from your own CA or a public one, issued for the name clients use to reach Beacon.

## Authentication

Beacon accepts every request unless you turn authentication on. With `auth.enabled`, every endpoint except `GET /health` needs a bearer token:

```http
Authorization: Bearer <token>
```

Turn on [TLS](#tls) as well, unless a TLS proxy sits in front of Beacon: tokens sent over plain HTTP can be read by anyone on the network. Beacon logs a warning at startup when tokens would cross the network unencrypted.

### Creating tokens

A token is any string of at least 16 printable ASCII characters without spaces. Generate a random one, then let Beacon hash it:

```bash
TOKEN=$(openssl rand -hex 32)
printf '%s' "$TOKEN" | beacon --hash-token
# 3f5c...e1a9
```

`beacon --hash-token` reads the token from standard input, so it stays out of your shell history and the process list, and prints its SHA-256 hash. Give the token to the service that will use it, and put only the hash in the tokens file.

### The tokens file

```toml
# /etc/beacon/tokens.toml
[[tokens]]
name = "orders"
sha256 = "3f5c...e1a9"
permissions = ["register", "discover"]

[[tokens]]
name = "prometheus"
sha256 = "9b20...47d0"
permissions = ["metrics"]

[[tokens]]
name = "admin"
sha256 = "c71e...0b3f"
# No permissions line: this token may do everything.
```

```toml
# beacon.toml
[auth]
enabled = true
tokens_file = "/etc/beacon/tokens.toml"
```

| Permission | Allows |
|---|---|
| `register` | `POST /api/v1/services`, `POST /api/v1/services/{id}/heartbeat`, `DELETE /api/v1/services/{id}` |
| `discover` | `GET /api/v1/services` |
| `metrics` | `GET /metrics` |

- `name` identifies the token in logs and error messages. It is not secret, and must be unique.
- `sha256` is the hex hash printed by `beacon --hash-token`, in either case. Each token must be unique.
- `permissions` is optional. Leave it out to allow everything; an empty list is rejected.
- Beacon reads the file once at startup and refuses to start if it is missing, contains an unknown key or an invalid entry, or lists no tokens. Restart Beacon after changing it.
- A file of hashes is not enough to call Beacon, but keep it readable only by Beacon anyway.

### Responses

A request without a token, with a malformed `Authorization` header or with a token Beacon does not accept gets `401` with the code `unauthorized` and a `WWW-Authenticate: Bearer realm="beacon"` header. A valid token without the needed permission gets `403` with the code `forbidden`. Unknown paths and wrong methods still answer `404` and `405`, token or not.

Refused requests are logged at `warn` level, with the token's name when it is known, and at `debug` level when no token was sent at all. Tokens themselves are never logged. They are also counted in `beacon_auth_failures_total`; see [Metrics](#metrics).

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

The image is built on [distroless](https://github.com/GoogleContainerTools/distroless) `static`: it has no shell or package manager and runs as a non-root user. It reads `/etc/beacon/beacon.toml`, a copy of the example configuration, so every default from [Configuration](#configuration) applies. Change settings either way:

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

To turn on [authentication](#authentication), mount the tokens file the same way:

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

## Go client

The [`client`](client) package is a Go client for the HTTP API. It uses only the standard library, so it adds nothing else to your build.

```bash
go get github.com/nm1996/beacon/client
```

Most services need two calls: announce themselves when they start, and look up the services they call.

```go
beacon, err := client.New("http://beacon:8080", client.WithLogger(logger))
if err != nil {
	return err
}

// Register and keep the registration alive until shutdown.
lease, err := beacon.Announce(ctx, client.Registration{Name: "orders", Address: "10.0.0.12", Port: 8081})
if err != nil {
	return err
}
defer lease.Close(context.Background())

// Find the instances of another service.
instances, err := beacon.Discover(ctx, "payments")
```

A `Lease` sends a heartbeat every third of the TTL Beacon reported, with a little jitter. If a heartbeat fails it retries after 1s, 2s, 4s and so on, never waiting longer than the normal interval, and keeps trying until `Close`. If Beacon no longer knows the instance, because it restarted or the instance expired, the lease registers again; the instance then has a new ID, which `lease.ID()` always returns. `Close` stops the background goroutine, waits for it and unregisters the instance. With `client.WithLogger`, retries and re-registrations are logged.

For full control, call the API directly:

```go
registered, err := beacon.Register(ctx, client.Registration{Name: "orders", Address: "10.0.0.12", Port: 8081})
err = beacon.Heartbeat(ctx, registered.ID)
err = beacon.Unregister(ctx, registered.ID)
```

Errors from Beacon are `*client.Error` values carrying the status and Beacon's error code, and they match `client.ErrNotFound` and `client.ErrInvalid` with `errors.Is`. `client.WithHTTPClient` sets a custom `http.Client`; by default every request times out after 10 seconds. `Register` is never retried, because repeating it would create a second instance.

When Beacon has [authentication](#authentication) on, give the client its token. Read it from a file or secret store rather than writing it into the code:

```go
beacon, err := client.New("https://beacon:8080", client.WithToken(os.Getenv("BEACON_TOKEN")))
```

The client then sends `Authorization: Bearer <token>` on every request. Errors for refused requests match `client.ErrUnauthorized` (`401`) and `client.ErrForbidden` (`403`) with `errors.Is`, including a `401` or `403` from a proxy in front of Beacon. A `Lease` whose heartbeats are refused keeps retrying with backoff and logs each failure; it never registers again because of them.

For a Beacon served over [TLS](#tls), use an `https://` URL. If its certificate comes from a private CA, trust that CA through `WithHTTPClient`:

```go
caPEM, err := os.ReadFile("/etc/beacon/ca.pem")
if err != nil {
	return err
}

roots := x509.NewCertPool()
if !roots.AppendCertsFromPEM(caPEM) {
	return errors.New("no certificates in ca.pem")
}

beacon, err := client.New("https://beacon:8080", client.WithHTTPClient(&http.Client{
	Timeout: 10 * time.Second,
	Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
	},
}))
```

`WithToken` and `WithHTTPClient` can be combined.

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
| `beacon_http_requests_total` | counter | `method`, `route`, `code` | Requests served. `route` is the route pattern, such as `POST /api/v1/services/{id}/heartbeat`, or `unmatched` |
| `beacon_http_request_duration_seconds` | histogram | `method`, `route` | Time spent serving requests |
| `beacon_instances` | gauge | `service` | Registered instances per service name |
| `beacon_expired_instances_total` | counter | | Instances removed because they stopped sending heartbeats |
| `beacon_registry_operations_total` | counter | `operation`, `result` | Calls to the storage backend; `result` is `ok`, `not_found` or `error` |
| `beacon_registry_operation_duration_seconds` | histogram | `operation` | Time spent in the storage backend |
| `beacon_auth_failures_total` | counter | `reason` | Requests refused by [authentication](#authentication); `reason` is `missing`, `invalid` or `forbidden`. Only appears once a request has been refused |
| `beacon_build_info` | gauge | `version`, `goversion` | Always `1`; describes the running build |

The standard `go_*` and `process_*` metrics are included too. Labels never contain instance IDs or raw paths, so the number of series stays small however many instances register. Requests to `/metrics` itself are not counted.

A few useful queries:

```promql
# Instances per service
sum by (service) (beacon_instances)

# Instances expiring per minute, a sign of crashing or unreachable services
rate(beacon_expired_instances_total[5m]) * 60

# Share of heartbeats for unknown instances, which then register again
sum(rate(beacon_http_requests_total{route="POST /api/v1/services/{id}/heartbeat",code="404"}[5m]))
  / sum(rate(beacon_http_requests_total{route="POST /api/v1/services/{id}/heartbeat"}[5m]))
```

With [authentication](#authentication) on, `/metrics` needs a token with the `metrics` permission, like the API needs its own permissions. With [TLS](#tls) on, Prometheus must scrape it with `scheme: https`:

```yaml
scrape_configs:
  - job_name: beacon
    scheme: https
    authorization:
      credentials_file: /etc/prometheus/beacon-token
    static_configs:
      - targets: ["beacon:8080"]
```

Without authentication, `/metrics` is open like the rest of the API. Disable it with `BEACON_METRICS_ENABLED=false` if it must not be reachable.

## Limitations

Beacon is deliberately small. Before you depend on it, know that:

- **State lives in memory.** Restarting Beacon forgets every instance. Services find out on their next heartbeat (`404`) and should register again.
- **It runs as a single node.** There is no clustering or replication.
- **Authentication is off by default.** Until you turn on [authentication](#authentication), anyone who can reach the port can register, discover and remove instances. [TLS](#tls) encrypts the traffic but does not decide who may connect.
- **A token with `register` can affect any instance.** Beacon does not record which token registered an instance, so any such token can send heartbeats for, or unregister, instances that other services registered.

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
| `mise run docker-build` | Build the `beacon:dev` container image |
| `mise run dist` | Build release archives for every platform into `dist/` |
| `mise run run` | Start the server on `:8080` |
| `mise run check` | Format, vet, lint, race tests and vulnerability scan |

Run `mise tasks` to list them all. CI runs the same tasks on every pull request.

## Project layout

```text
.
├── client/                  # Go client for the HTTP API
├── cmd/beacon/              # Entry point: flags, config, logging, signals, cleanup worker and shutdown
├── internal/
│   ├── api/                 # HTTP transport: routes, request decoding, JSON errors, bearer-token checks
│   ├── auth/                # Tokens file, token hashes and permissions
│   ├── discovery/           # Business rules: validation, IDs, TTL, cleanup; the Registry interface
│   ├── metrics/             # Prometheus metrics around the HTTP handler and any Registry backend
│   ├── registry/
│   │   ├── memory/          # In-memory Registry backend
│   │   └── registrytest/    # Contract tests every Registry backend must pass
│   ├── tlstest/             # Self-signed certificates for tests
│   ├── bootstrap/           # Builds the backend, discovery layer and HTTP server and connects them, and loads the TLS certificate
│   └── config/              # Loads and validates the TOML configuration and environment overrides
├── deployments/
│   ├── docker/              # Dockerfile
│   └── compose/             # Compose example
├── configs/                 # Example configuration
├── scripts/                 # Release build script
├── .github/workflows/       # CI and releases
├── .golangci.yml            # Linter configuration
└── .mise.toml               # Pinned tools and development tasks
```

Requests flow in one direction:

```text
api (HTTP)  ──►  discovery.Service  ──►  discovery.Registry  ◄──  registry/memory
transport        business rules          storage interface        storage backend
```

Transports only translate requests and errors; every rule lives in `discovery`, so a future gRPC transport behaves exactly like HTTP. Storage backends only store, so they are interchangeable. A new backend goes under `internal/registry/` and is done when it passes `registrytest.Run`.

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
- [x] Docker image and Compose example
- [x] Prometheus metrics
- [x] Go client library
- [x] First release, `v0.1.0`

**After v0.1.0**

- [x] TLS for the HTTP API
- [x] Authentication with bearer tokens and per-token permissions
- [ ] Client certificates (mTLS) as another way to authenticate
- [ ] Reload certificates and tokens without a restart
- [ ] gRPC API alongside HTTP
- [ ] Keep an instance's ID when it registers again, instead of issuing a new one
- [ ] Client-side caching and load balancing in the Go client
- [ ] Cache and database
- [ ] Scale, multiple nodes
- [ ] Idempotency

## Versioning

Beacon follows [Semantic Versioning](https://semver.org/). Releases are git tags such as `v0.1.0` on `main`, and every change is listed in [CHANGELOG.md](CHANGELOG.md).

| What | How it is versioned | What counts as breaking |
|---|---|---|
| Releases (binaries, tags) | `vMAJOR.MINOR.PATCH` | Before `v1.0.0` a breaking change bumps the minor version, anything else the patch version. From `v1.0.0` on, standard SemVer applies. |
| HTTP API | `/api/v1` in the path | Removing or renaming an endpoint or field, or changing its meaning. Adding endpoints or fields is not breaking and stays in `v1`. A breaking change moves to `/api/v2`, and `/api/v1` keeps working for at least one more minor release. |
| Configuration | Same as the release | Removing or renaming a key or `BEACON_*` variable, or changing a default in a way that changes behavior. Adding a key with a default is not breaking. |

Commit messages follow Conventional Commits, which map onto these rules: `feat!` or `BREAKING CHANGE` is breaking, `feat` adds functionality, and `fix` is a patch.

### Releasing

1. Move the **Unreleased** entries in `CHANGELOG.md` under a new version heading and merge that change.
2. Tag the merge commit on `main` and push the tag:

   ```bash
   git tag -a v0.1.0 -m "v0.1.0"
   git push origin v0.1.0
   ```

3. The [release workflow](.github/workflows/release.yml) runs the checks, builds archives for Linux, macOS and Windows on amd64 and arm64, and publishes a GitHub release with checksums. Tags with a suffix, such as `v0.1.0-rc.1`, become pre-releases.

Tags are never moved or deleted once pushed.

## Contributing

Suggestions, bug reports and pull requests are welcome. Please read [CONTRIBUTING.md](CONTRIBUTING.md) first; it covers the development workflow, commit message format and what a good pull request includes.

## License

Beacon is released under the [MIT License](LICENSE).
