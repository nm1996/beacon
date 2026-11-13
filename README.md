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
- [Rate limits](#rate-limits)
- [Reloading certificates and tokens](#reloading-certificates-and-tokens)
- [Running in Docker](#running-in-docker)
- [Go client](#go-client)
- [Metrics](#metrics)
- [Exposing Beacon to a network](#exposing-beacon-to-a-network)
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
| ✅ | Optional client certificates (mTLS), with the same permissions |
| ✅ | With authentication on, only the caller that registered an instance may change it |

## Quick start

You need [Go 1.26](https://go.dev/dl/) or newer, or [mise](https://mise.jdx.dev/) to install the pinned toolchain for you.

```bash
git clone https://github.com/nm1996/beacon.git
cd beacon

go run ./cmd/beacon
# {"time":"...","level":"INFO","msg":"beacon listening","addr":"127.0.0.1:8080","version":"..."}
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
4. **Expire.** An instance that has not sent a heartbeat for more than 30 seconds is no longer returned by lookups, and within 5 seconds Beacon removes it.
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
| `400` | `invalid_query` | A query parameter is invalid, such as `limit=0` or a `cursor` Beacon did not issue |
| `403` | `instance_not_owned` | [Authentication](#authentication) is on and another caller registered this instance; see [Instance ownership](#instance-ownership) |
| `429` | `quota_exceeded` | Beacon already holds `registry.max_instances` instances, or the caller already holds `registry.max_instances_per_owner`; retry after some unregister or expire |
| `429` | `rate_limited` | Too many registrations from this caller, or too many failed authentication attempts from this network; retry after the `Retry-After` header. See [Rate limits](#rate-limits) |
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
| `name` | Required. 1–63 (`registry.max_name_length`) lowercase letters, digits, `.`, `_` or `-`, starting and ending with a letter or digit, with no two of `.`, `_` and `-` in a row, such as `orders` or `payment-service.v2`. Uppercase, spaces and names like `orders..v2` are rejected, not changed |
| `address` | Required. An IPv4 or IPv6 address, or a host name such as `orders.internal`, up to 253 characters (`registry.max_address_length`). No scheme, port or path |
| `port` | Required, between `1` and `65535` |

| Status | Meaning |
|---|---|
| `201 Created` | Registered. The `Location` header points at the new instance |
| `400 Bad Request` | The body is invalid or a field breaks a rule above |
| `429 Too Many Requests` | `quota_exceeded`: Beacon is full, or this caller holds its maximum number of instances. `rate_limited`: this caller registered too often; see [Rate limits](#rate-limits) |

```json
{ "id": "8c0f3c56-5d7e-4a63-9a39-2b7c1e5e8f40", "ttl_seconds": 30 }
```

`ttl_seconds` is how long the instance may go without a heartbeat before Beacon removes it. Sending a heartbeat every third of that leaves room for a missed one.

### `GET /api/v1/services`

| Query parameter | Meaning |
|---|---|
| `name` | Optional. Only return instances with this name |
| `limit` | Optional. Return at most this many instances, from `1` to `1000`, and link to the next page |
| `cursor` | Only with `limit`. Where the next page starts; take it from the `Link` header, never build it |

Returns `200 OK` with a JSON array sorted by ID, which is empty when nothing matches. Instances whose last heartbeat is more than `registry.service_ttl` ago are left out, even before cleanup removes them:

```json
[
  { "id": "8c0f3c56-...", "name": "orders", "address": "10.0.0.12", "port": 8081, "last_heartbeat": "2026-11-10T16:00:00Z" },
  { "id": "1b9e47a2-...", "name": "orders", "address": "10.0.0.13", "port": 8081, "last_heartbeat": "2026-11-10T16:00:04Z" }
]
```

`last_heartbeat` is in UTC, in RFC 3339 format.

Without `limit`, every matching instance comes back in one response, as it always has; `registry.max_instances` bounds how large that can get. With `limit`, the response holds at most that many and, when more follow, a `Link` header points at the next page:

```http
Link: </api/v1/services?cursor=Y2FmZS0xMjM&limit=100&name=orders>; rel="next"
```

Request that URL to get the next page, until a response has no `Link` header. The response body stays a plain array either way, so clients that ignore paging keep working. Pages are taken in ID order, so an instance registered or removed while you page may or may not show up, but none is returned twice.

| Status | Meaning |
|---|---|
| `200 OK` | The instances, possibly with a `Link` header |
| `400 Bad Request` | `limit` or `cursor` is invalid; code `invalid_query` |

### `POST /api/v1/services/{id}/heartbeat`

No request body.

| Status | Meaning |
|---|---|
| `204 No Content` | Heartbeat recorded |
| `403 Forbidden` | With authentication on: another caller registered this instance (`instance_not_owned`), or the caller lacks `register` (`forbidden`) |
| `404 Not Found` | No instance with that ID, possibly because it already expired |

An instance that gets `404` should register again.

### `DELETE /api/v1/services/{id}`

No request body.

| Status | Meaning |
|---|---|
| `204 No Content` | Instance removed |
| `403 Forbidden` | With authentication on: another caller registered this instance (`instance_not_owned`), or the caller lacks `register` (`forbidden`) |
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
| `server.host` | `BEACON_SERVER_HOST` | `"127.0.0.1"` | Interface to listen on. The default accepts local connections only; see [Exposing Beacon to a network](#exposing-beacon-to-a-network). The container image sets `0.0.0.0` |
| `server.port` | `BEACON_SERVER_PORT` | `8080` | TCP port, `1`–`65535` |
| `server.read_timeout` | `BEACON_SERVER_READ_TIMEOUT` | `"5s"` | Time allowed to read a request, headers included |
| `server.write_timeout` | `BEACON_SERVER_WRITE_TIMEOUT` | `"10s"` | Time allowed to write a response |
| `server.idle_timeout` | `BEACON_SERVER_IDLE_TIMEOUT` | `"60s"` | Time an idle keep-alive connection stays open |
| `server.shutdown_timeout` | `BEACON_SERVER_SHUTDOWN_TIMEOUT` | `"10s"` | Time in-flight requests get to finish on shutdown |
| `server.max_request_body_bytes` | `BEACON_SERVER_MAX_REQUEST_BODY_BYTES` | `65536` | Largest accepted request body |
| `server.reload_interval` | `BEACON_SERVER_RELOAD_INTERVAL` | `"0s"` | How often to check the TLS files and tokens file for changes; `0s` is off. See [Reloading](#reloading-certificates-and-tokens) |
| `tls.cert_file` | `BEACON_TLS_CERT_FILE` | `""` | PEM certificate chain to serve HTTPS with; see [TLS](#tls) |
| `tls.key_file` | `BEACON_TLS_KEY_FILE` | `""` | PEM private key for `tls.cert_file` |
| `tls.client_ca_file` | `BEACON_TLS_CLIENT_CA_FILE` | `""` | PEM bundle of CAs whose client certificates Beacon accepts; see [Client certificates (mTLS)](#client-certificates-mtls) |
| `auth.enabled` | `BEACON_AUTH_ENABLED` | `false` | Require a bearer token on every endpoint except `/health`; see [Authentication](#authentication) |
| `auth.tokens_file` | `BEACON_AUTH_TOKENS_FILE` | `""` | File listing the accepted tokens. Required when `auth.enabled` is `true` |
| `registry.service_ttl` | `BEACON_REGISTRY_SERVICE_TTL` | `"30s"` | Time without a heartbeat before an instance is removed |
| `registry.cleanup_interval` | `BEACON_REGISTRY_CLEANUP_INTERVAL` | `"5s"` | How often stale instances are removed |
| `registry.max_name_length` | `BEACON_REGISTRY_MAX_NAME_LENGTH` | `63` | Longest accepted service name, 1–253. The default fits a DNS label |
| `registry.max_address_length` | `BEACON_REGISTRY_MAX_ADDRESS_LENGTH` | `253` | Longest accepted host name as an address, 1–253. IP addresses are not held to it |
| `registry.max_instances` | `BEACON_REGISTRY_MAX_INSTANCES` | `10000` | The most instances Beacon holds at once; further registrations get `429 quota_exceeded`. Bounds memory use and the size of discovery responses |
| `registry.max_instances_per_owner` | `BEACON_REGISTRY_MAX_INSTANCES_PER_OWNER` | `0` | The most instances one token or certificate may hold at once, so no single caller can take every place; `0` is no limit. Needs [authentication](#instance-ownership), and must not exceed `registry.max_instances` |
| `rate_limit.registrations_per_second` | `BEACON_RATE_LIMIT_REGISTRATIONS_PER_SECOND` | `20` | Registrations each caller may make per second on average; `0` is off. See [Rate limits](#rate-limits) |
| `rate_limit.registration_burst` | `BEACON_RATE_LIMIT_REGISTRATION_BURST` | `200` | Registrations a caller may make at once before the rate applies |
| `rate_limit.auth_failures_per_second` | `BEACON_RATE_LIMIT_AUTH_FAILURES_PER_SECOND` | `1` | Failed authentication attempts each client network may make per second on average; `0` is off |
| `rate_limit.auth_failure_burst` | `BEACON_RATE_LIMIT_AUTH_FAILURE_BURST` | `20` | Failed attempts a network may make at once before the rate applies |
| `storage.backend` | `BEACON_STORAGE_BACKEND` | `"memory"` | Where instances are kept. Only `memory` exists today |
| `metrics.enabled` | `BEACON_METRICS_ENABLED` | `true` | Serve Prometheus metrics at `GET /metrics` |
| `logging.level` | `BEACON_LOGGING_LEVEL` | `"info"` | `debug`, `info`, `warn` or `error` |
| `logging.format` | `BEACON_LOGGING_FORMAT` | `"json"` | `json` or `text` |

```bash
BEACON_SERVER_PORT=9090 BEACON_LOGGING_FORMAT=text beacon --config /etc/beacon/beacon.toml
```

Environment variables hold plain values without TOML quotes, for example `BEACON_REGISTRY_SERVICE_TTL=2m`. A variable that is set applies even when it is empty, so `BEACON_SERVER_HOST=` listens on all interfaces.

Durations use Go syntax such as `"500ms"`, `"5s"` or `"1m30s"`. All durations and sizes must be positive, `registry.service_ttl` must be a whole number of seconds and at least `1s`, because clients are told it as `ttl_seconds`, and `registry.cleanup_interval` must not be longer than `registry.service_ttl`.

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
- Renewed certificates are picked up without a restart; see [Reloading certificates and tokens](#reloading-certificates-and-tokens).
- To have clients prove who they are with certificates as well, see [Client certificates (mTLS)](#client-certificates-mtls).
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

or, over [TLS](#tls) with `tls.client_ca_file` set, a [client certificate](#client-certificates-mtls). Both kinds of caller are listed in the same file, `auth.tokens_file`, and get the same permissions.

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
# No permissions line: discover, register and metrics, but not manage.
```

```toml
# beacon.toml
[auth]
enabled = true
tokens_file = "/etc/beacon/tokens.toml"
```

| Permission | Allows |
|---|---|
| `register` | `POST /api/v1/services`, and `POST /api/v1/services/{id}/heartbeat` and `DELETE /api/v1/services/{id}` for the instances the same caller registered |
| `discover` | `GET /api/v1/services` |
| `metrics` | `GET /metrics` |
| `manage` | Together with `register`: heartbeats and unregistering for instances other callers registered. Never granted unless listed |

- `name` identifies the token in logs and error messages. It is not secret, and must be unique across tokens and certificates.
- `sha256` is the hex hash printed by `beacon --hash-token`, in either case. Each token must be unique.
- `permissions` is optional. Leave it out to allow `discover`, `register` and `metrics`. `manage` must always be listed, together with `register`, so a token written without a permission list can never touch another service's instances. An empty list is rejected, and so is `manage` without `register`.
- Beacon refuses to start if the file is missing, contains an unknown key or an invalid entry, or lists neither tokens nor certificates. Changes take effect on the next [reload](#reloading-certificates-and-tokens), without a restart.
- A file of hashes is not enough to call Beacon, but keep it readable only by Beacon anyway.

### Instance ownership

With authentication on, Beacon records which token or certificate registered each instance, by its `name`. Only that caller may send the instance's heartbeats or unregister it; anyone else with `register` gets `403` with the code `instance_not_owned`. Every caller with `discover` still sees every instance, since finding other services is the point of a registry.

- An operator who needs to remove a stuck instance of another service uses an entry with `permissions = ["register", "manage"]`, alongside `discover` if it should also list instances.
- The owner is the entry's `name`. Renaming an entry, or moving a service from a token to a certificate under another name, means its existing instances belong to the old name: their heartbeats get `403`, they expire after `registry.service_ttl`, and a [`Lease`](#go-client) then gets `404` and registers again under the new name.
- Owners appear in the `service registered` log line (`owner`), and whoever unregistered an instance in `service unregistered` (`by`). `GET /api/v1/services` does not show them.
- `registry.max_instances_per_owner` limits how many instances each owner may hold at once, so one service, or one leaked token, cannot fill Beacon and lock everyone else out. Registrations beyond it get `429 quota_exceeded`, logged as `registration refused: owner quota reached` with the `caller`. Other owners are not affected.
- Without authentication nothing is recorded or checked, and anyone may change any instance. The per-owner limit does not apply then; only `registry.max_instances` does.

### Client certificates (mTLS)

Services that already have certificates from your own CA, for example from cert-manager, Vault or a service mesh, can use them instead of tokens. Set `tls.client_ca_file` to the CA bundle, and list each certificate in the tokens file by one of its subject alternative names:

```toml
# beacon.toml
[tls]
cert_file = "/etc/beacon/tls/cert.pem"
key_file = "/etc/beacon/tls/key.pem"
client_ca_file = "/etc/beacon/tls/clients-ca.pem"

[auth]
enabled = true
tokens_file = "/etc/beacon/tokens.toml"
```

```toml
# /etc/beacon/tokens.toml, next to any [[tokens]]
[[certificates]]
name = "orders"
dns = "orders.internal"
permissions = ["register", "discover"]

[[certificates]]
name = "payments"
uri = "spiffe://example.org/ns/prod/sa/payments"
permissions = ["discover"]
```

- `tls.client_ca_file` needs TLS (`tls.cert_file` and `tls.key_file`) and `auth.enabled`. Beacon refuses to start without them, or when the bundle holds no PEM certificate, or when the tokens file has no `[[certificates]]`. Entries without `tls.client_ca_file` are only warned about, since they can never match.
- Each entry sets exactly one of `dns`, compared case-insensitively with the certificate's DNS names, or `uri`, compared exactly with its URI names, such as a SPIFFE ID. Wildcards are not matched, and the subject's common name is ignored. When several entries match one certificate, the first in the file wins.
- Clients are asked for a certificate but not required to send one, so `/health` and bearer tokens keep working on the same port. A certificate that does not verify against the bundle, or is not issued for client authentication, fails the TLS handshake. One that verifies but no entry names gets `401`, and the log names it so you can add it.
- A request with an `Authorization` header is judged by its token alone, even if it also presents a certificate.
- Beacon does not check revocation lists or OCSP. Issue short-lived client certificates, and remove an entry and [reload](#reloading-certificates-and-tokens) to lock one out.

To try it locally, make a CA, then a server and a client certificate signed by it. This writes everything to `certs/`, which `.gitignore` already excludes:

```bash
mkdir -p certs

# The CA
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -days 30 \
  -subj "/CN=beacon test CA" -keyout certs/ca-key.pem -out certs/ca.pem

# Beacon's server certificate
printf 'subjectAltName=DNS:localhost,IP:127.0.0.1\nextendedKeyUsage=serverAuth\n' > certs/server.ext
openssl req -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -subj "/CN=localhost" \
  -keyout certs/key.pem -out certs/server.csr
openssl x509 -req -in certs/server.csr -CA certs/ca.pem -CAkey certs/ca-key.pem -days 30 \
  -extfile certs/server.ext -out certs/cert.pem

# A client certificate for orders.internal
printf 'subjectAltName=DNS:orders.internal\nextendedKeyUsage=clientAuth\n' > certs/orders.ext
openssl req -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -subj "/CN=orders" \
  -keyout certs/orders-key.pem -out certs/orders.csr
openssl x509 -req -in certs/orders.csr -CA certs/ca.pem -CAkey certs/ca-key.pem -days 30 \
  -extfile certs/orders.ext -out certs/orders.pem

printf '[[certificates]]\nname = "orders"\ndns = "orders.internal"\n' > certs/tokens.toml

BEACON_TLS_CERT_FILE=certs/cert.pem BEACON_TLS_KEY_FILE=certs/key.pem BEACON_TLS_CLIENT_CA_FILE=certs/ca.pem \
  BEACON_AUTH_ENABLED=true BEACON_AUTH_TOKENS_FILE=certs/tokens.toml go run ./cmd/beacon

curl --cacert certs/ca.pem --cert certs/orders.pem --key certs/orders-key.pem https://localhost:8080/api/v1/services
```

On Windows, the `curl` that ships with Git and Windows uses Schannel, which rejects certificates from a private CA unless you pass `--ssl-no-revoke`, and only takes client certificates as `.p12` files. `openssl s_client -connect localhost:8080 -CAfile certs/ca.pem -cert certs/orders.pem -key certs/orders-key.pem` works with the PEM files as they are.

### Responses

A request without a token or verified client certificate, with a malformed `Authorization` header, with a token Beacon does not accept, or with a certificate no entry names gets `401` with the code `unauthorized` and a `WWW-Authenticate: Bearer realm="beacon"` header. A known caller without the needed permission gets `403` with the code `forbidden`. Unknown paths and wrong methods still answer `404` and `405`, credentials or not.

Refused requests are logged at `warn` level, with the caller's name when it is known (`caller`), or the certificate's names when no entry matched (`certificate_names`), and at `debug` level when no credentials were sent at all. Tokens themselves are never logged. Refusals are also counted in `beacon_auth_failures_total`; see [Metrics](#metrics).

## Rate limits

Beacon limits two things per client, each with a token bucket: a steady rate, plus a burst that may be spent at once. The limits live in each Beacon process; there is nothing to set up. They are not shared between processes, so if several Beacon nodes ever sit behind one load balancer, each enforces its own and a client spread over three nodes gets up to three times the limit; enforce the limit at the load balancer then.

| Limit | Counted per | Default | Beyond it |
|---|---|---|---|
| Registrations (`POST /api/v1/services`) | Caller: the token or certificate's `name` with [authentication](#authentication) on, the client network without it | 20 per second, bursts of 200 | That caller's registrations get `429 rate_limited` |
| Failed authentication: an unknown token or certificate, or a malformed `Authorization` header | Client network | 1 per second, bursts of 20 | Every request from that network gets `429 rate_limited`, valid credentials or not, until it is back under the limit |

- Refused requests carry a `Retry-After` header with the seconds to wait, are logged at `warn` level with the `limit`, and are counted in `beacon_rate_limited_total{limit}`. The [Go client](#go-client) reports them as `client.ErrRateLimited`, and a `Lease` keeps retrying with backoff.
- A client network is the IPv4 address of the connection, or the `/64` its IPv6 address is in, since one host often holds a whole `/64`.
- Requests without any credentials and callers lacking a permission do not count as failures: the first are probes or misconfiguration, and the second have already proved who they are.
- The failure limit is enforced before credentials are checked, so concurrent attempts cannot get past it together: each request holds one place from its network's burst while Beacon checks its credentials, and gives it back unless they turn out invalid. The check takes microseconds, but a network sending more than `rate_limit.auth_failure_burst` requests at the very same moment, such as many clients behind one NAT under heavy load, can see some valid ones refused with `429`; raise the burst if it does.
- Heartbeats, lookups and unregistering are not rate limited.
- Behind a reverse proxy or load balancer every client has the proxy's address, so the per-network limits apply to all of them together. Beacon does not read `X-Forwarded-For`, because a client could set it to anything, and one client could then use up the failure limit of others by naming their addresses. Raise the limits, or set them to `0`, when the proxy enforces its own.
- The defaults leave room for a large rollout that shares one token: a burst of 200 registrations at once, then 20 a second. `client.Announce` returns the error if its first registration is refused, so size the burst for the most instances that start together.
- Each limit tracks up to 65536 callers or networks. Callers and networks whose buckets have refilled are forgotten to make room; if every place is still in use, new ones are not limited until there is room again. The same bound applies to requests in authentication: once 65536 networks have one in progress, requests from further networks that have not failed before are checked without holding a place, though their failures still count. Networks that have failed are always held to their limit.

## Reloading certificates and tokens

Beacon reads the TLS certificate and key, the client CA bundle and the tokens file at startup, and can read them again while it runs, so you can renew a certificate or add and revoke tokens without a restart:

- **On a signal.** Send `SIGHUP` to reload at once, for example `kill -HUP <pid>`, `systemctl reload` with `ExecReload=/bin/kill -HUP $MAINPID`, or `docker kill --signal HUP <container>`. Windows has no `SIGHUP`; use the interval there.
- **On a change.** Set `server.reload_interval`, for example to `"30s"`, and Beacon checks the files that often and reloads when their contents differ. This suits files that something else rotates, such as cert-manager or a Kubernetes Secret mounted as a volume. Unchanged files are not reloaded.

```toml
[server]
reload_interval = "30s"
```

A reload replaces everything at once or nothing. If any file is missing, unreadable or invalid, for example a new certificate whose key has not been written yet, Beacon logs an error and keeps using the files it already has, so a rotation caught halfway never locks clients out. With the interval, the same broken contents are reported once, not on every check, and tried again as soon as they change.

After a successful reload, every new request is checked against the new tokens file, and every new TLS connection gets the new certificate and client CAs. Connections that are already open keep the certificate they were made with until they close. Each reload is published as one generation: a handshake and the request authenticated after it never mix the client CAs of one reload with the identities of another. A request on a connection opened before a reload is still authenticated against the identities in use now, so a revoked certificate or token stops working at once. The `credentials reloaded` log line reports the new `generation`. Only the contents of the files are reloaded: changing `tls.cert_file` or any other setting, or turning TLS or authentication on or off, still needs a restart.

Reloads are logged and counted in `beacon_credential_reloads_total`; see [Metrics](#metrics). Alert on its `result="error"` series to catch a rotation that failed.

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

The image is built on [distroless](https://github.com/GoogleContainerTools/distroless) `static`: it has no shell or package manager and runs as a non-root user. It reads `/etc/beacon/beacon.toml`, a copy of the example configuration, so every default from [Configuration](#configuration) applies, except that the image sets `BEACON_SERVER_HOST=0.0.0.0` so the published port can reach it. Environment variables win over the file, so a mounted file's `server.host` is ignored unless you also pass `-e BEACON_SERVER_HOST=...`. Change settings either way:

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

A `Lease` sends a heartbeat every third of the TTL Beacon reported, with a little jitter. If a heartbeat fails it retries after 1s, 2s, 4s and so on, never waiting longer than the normal interval, and keeps trying until `Close`. Each heartbeat or registration the lease sends gives up after a sixth of the TTL, so one stuck on a dead connection leaves time to retry on a new one before the instance expires, whatever timeout the `http.Client` has; with a TTL of 3s or less that cap is reached at once, so retries come at a steady third of the TTL. If Beacon no longer knows the instance, because it restarted or the instance expired, the lease registers again; the instance then has a new ID, which `lease.ID()` always returns. `Close` stops the background goroutine, waits for it and unregisters the instance. If its context ends first, or Beacon cannot be reached, `Close` returns the error and can simply be called again with a new context; the heartbeats stay stopped either way, and once the instance is unregistered further calls return `nil`. With `client.WithLogger`, retries and re-registrations are logged.

For full control, call the API directly:

```go
registered, err := beacon.Register(ctx, client.Registration{Name: "orders", Address: "10.0.0.12", Port: 8081})
err = beacon.Heartbeat(ctx, registered.ID)
err = beacon.Unregister(ctx, registered.ID)
```

Errors from Beacon are `*client.Error` values carrying the status and Beacon's error code, and they match `client.ErrNotFound` and `client.ErrInvalid` with `errors.Is`. `client.WithHTTPClient` sets a custom `http.Client`; by default every request times out after 10 seconds. `Register` is never retried, because repeating it would create a second instance. `Discover` fetches instances 500 at a time and follows the page links, so a large registry never has to fit in one response; it works against servers without paging too. The result is a live view rather than a snapshot, as described under [`GET /api/v1/services`](#get-apiv1services). If the paging breaks, for example a proxy drops the cursor from the next link or sends the client round in a loop, `Discover` returns an error rather than a partial list.

When Beacon has [authentication](#authentication) on, give the client its token. Read it from a file or secret store rather than writing it into the code:

```go
beacon, err := client.New("https://beacon:8080", client.WithToken(os.Getenv("BEACON_TOKEN")))
```

The client then sends `Authorization: Bearer <token>` on every request. Errors for refused requests match `client.ErrUnauthorized` (`401`) and `client.ErrForbidden` (`403`) with `errors.Is`, including a `401` or `403` from a proxy in front of Beacon. A heartbeat or unregister of an instance another caller registered also matches `client.ErrNotOwned`; see [Instance ownership](#instance-ownership). A registration refused because Beacon is full matches `client.ErrQuotaExceeded`; a `Lease` that has to register again keeps retrying with backoff until there is room. A request refused by a [rate limit](#rate-limits), or any `429` without Beacon's error format, such as a proxy's, matches `client.ErrRateLimited`. A `Lease` whose heartbeats are refused keeps retrying with backoff and logs each failure; it never registers again because of them, though once its instance expires it gets `404` and registers again as usual.

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

To authenticate with a [client certificate](#client-certificates-mtls) instead of a token, load it into the same `tls.Config`:

```go
certificate, err := tls.LoadX509KeyPair("/etc/orders/tls/cert.pem", "/etc/orders/tls/key.pem")
if err != nil {
	return err
}

beacon, err := client.New("https://beacon:8080", client.WithHTTPClient(&http.Client{
	Timeout: 10 * time.Second,
	Transport: &http.Transport{
		TLSClientConfig: &tls.Config{
			RootCAs:      roots,
			Certificates: []tls.Certificate{certificate},
			MinVersion:   tls.VersionTLS12,
		},
	},
}))
```

A certificate Beacon does not trust fails during the TLS handshake, so the error is a connection error rather than a `*client.Error`.

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
| `beacon_registry_operations_total` | counter | `operation`, `result` | Calls to the storage backend; `result` is `ok`, `not_found`, `not_owned`, `full` (refused by `registry.max_instances`), `owner_full` (refused by `registry.max_instances_per_owner`) or `error` |
| `beacon_registry_operation_duration_seconds` | histogram | `operation` | Time spent in the storage backend |
| `beacon_rate_limited_total` | counter | `limit` | Requests refused by a [rate limit](#rate-limits); `limit` is `registrations` or `auth_failures`. Only appears once a request has been refused |
| `beacon_auth_failures_total` | counter | `reason` | Requests refused by [authentication](#authentication); `reason` is `missing`, `invalid` or `forbidden`. Only appears once a request has been refused |
| `beacon_credential_reloads_total` | counter | `result` | Attempts to [reload](#reloading-certificates-and-tokens) the TLS files and tokens file while running; `result` is `ok` or `error`. Only appears after the first attempt |
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

With [authentication](#authentication) on, `/metrics` needs a token or client certificate with the `metrics` permission, like the API needs its own permissions. With [TLS](#tls) on, Prometheus must scrape it with `scheme: https`:

```yaml
scrape_configs:
  - job_name: beacon
    scheme: https
    authorization:
      credentials_file: /etc/prometheus/beacon-token
    static_configs:
      - targets: ["beacon:8080"]
```

To scrape with a [client certificate](#client-certificates-mtls) instead, replace `authorization` with `tls_config` holding `cert_file` and `key_file`.

Without authentication, `/metrics` is open like the rest of the API. Disable it with `BEACON_METRICS_ENABLED=false` if it must not be reachable.

## Exposing Beacon to a network

> [!WARNING]
> The defaults are for one machine: Beacon listens on `127.0.0.1`, without TLS or authentication. Before other machines can reach it, decide who may.

To accept connections from the network:

1. Turn on [TLS](#tls), so tokens and registrations are not sent in the clear.
2. Turn on [authentication](#authentication), so only callers with a token or certificate can register, discover or remove instances, and only an instance's owner can change it.
3. Set `server.host` to `"0.0.0.0"` (or `BEACON_SERVER_HOST=0.0.0.0`), or to the address of one interface.

Beacon starts either way, but logs a warning when it serves plain HTTP on an address other than loopback.

The [container image](#running-in-docker) already listens on `0.0.0.0`, because inside a container `127.0.0.1` is only reachable from the container itself; who can reach it is decided by the ports you publish. Publish them only on a trusted network, or with TLS and authentication on. To bind a published port to the host's loopback only, use `-p 127.0.0.1:8080:8080`.

## Limitations

Beacon is deliberately small. Before you depend on it, know that:

- **State lives in memory.** Restarting Beacon forgets every instance. Services find out on their next heartbeat (`404`) and should register again.
- **It runs as a single node.** There is no clustering or replication.
- **Authentication is off by default.** Until you turn on [authentication](#authentication), anyone who can reach the port can register, discover and remove instances. By default only the same machine can, since Beacon listens on `127.0.0.1`; see [Exposing Beacon to a network](#exposing-beacon-to-a-network). [TLS](#tls) encrypts the traffic but does not decide who may connect.
- **Ownership is by name, not by service.** Every instance registered with the same token or certificate belongs to the same caller, so two services sharing one token can change each other's instances. Give each service its own token or certificate.
- **The owner is the name in the tokens file.** Ownership and `registry.max_instances_per_owner` are keyed by the entry's `name`, which is also what logs show, so renaming an entry changes the owner; see [Instance ownership](#instance-ownership). A stable owner ID, kept apart from the name shown in logs, is planned.
- **Client certificates are not checked for revocation.** Use short-lived certificates.
- **A paused Beacon expires everything at once.** If the process is stopped for longer than `registry.service_ttl`, for example with `SIGSTOP` or a paused virtual machine, no heartbeats arrive meanwhile, and every instance expires when it resumes; their leases then register again. A suspended laptop does not count, since Beacon measures time with a clock that stops during suspend.

## Development

Beacon uses [mise](https://mise.jdx.dev/) to pin the Go and `golangci-lint` versions and to run common tasks.

```bash
mise install   # install the pinned tools
mise run check # check formatting, vet, lint and run tests with the race detector
```

| Task | What it does |
|---|---|
| `mise run fmt` | Format Go code with `gofmt` |
| `mise run fmt-check` | Fail if any file is not formatted |
| `mise run vet` | Run `go vet` |
| `mise run lint` | Run `golangci-lint` |
| `mise run test` | Run tests |
| `mise run test-race` | Run tests with the race detector |
| `mise run bench` | Run the benchmarks; see [Benchmarks](#benchmarks) |
| `mise run bench-parallel` | Run the mixed backend benchmark at 1, 2, 4 and 8 goroutines |
| `mise run vuln` | Scan for known vulnerabilities with `govulncheck` |
| `mise run build` | Build `bin/beacon` |
| `mise run docker-build` | Build the `beacon:dev` container image |
| `mise run dist` | Build release archives for every platform into `dist/` |
| `mise run run` | Start the server on `:8080` |
| `mise run check` | Check formatting, vet, lint, race tests and vulnerability scan; never changes files |

Run `mise tasks` to list them all. CI runs the same tasks on every pull request.

### Benchmarks

`mise run bench` measures the in-memory backend at 100 to 100000 instances, the business rules, and the HTTP handlers. One run on a laptop (AMD Ryzen 3 5300U, 8 threads, Go 1.27), at the default `registry.max_instances` of 10000:

| Operation | Time | Allocations |
|---|---|---|
| Heartbeat in the backend (`Touch`) | 0.1 µs | 0 |
| Register and remove in the backend | 0.85 µs | 4 |
| Cleanup scan with nothing expired (`DeleteStale`) | 0.25 ms | 0 |
| Look up one service of 100 (`List` by name) | 27 µs | 1 |
| A page of 500, or every instance (`List`) | 4.7 ms | 1 |
| `POST /api/v1/services` through the router, with validation and JSON | 14 µs | 42 |
| `GET /api/v1/services?limit=500` through the router | 5.4 ms | 48 |
| A valid token through the Guard, with the failed-authentication limit | 0.9 µs | 7 |

- Heartbeats cost the same at any size; they are what clients send most.
- A lookup by name visits only that name's instances, through an index the backend keeps by name, so it costs what it returns rather than what the registry holds. Keeping the index is why registering and removing cost about 0.4 µs more than without it, and removing every instance at once about a third more.
- Without a name, `List` scans, copies and sorts every instance on each call, so a page of 500 costs as much as the whole registry, and grows with it: 82 ms for one page at 100000. The default limit keeps it in the low milliseconds; an index by ID would make a page cost what it returns.
- The failed-authentication limit adds about 0.1 µs to a request with valid credentials: it only notes the attempt while the credentials are checked, and keeps no state for networks that have never failed.

Every operation shares one lock: lookups can run side by side, but heartbeats and registrations wait for every lookup in progress. Two benchmarks measure what that costs.

`mise run bench-parallel` runs `BenchmarkMixedParallel`, 90% heartbeats, 9% lookups of one service of 100, and 1% registrations, from 1 to 8 goroutines at once. Average time per operation, the median of three runs:

| Instances | 1 goroutine | 2 | 4 | 8 |
|---|---|---|---|---|
| 100 | 0.10 µs | 0.11 µs | 0.11 µs | 0.11 µs |
| 1000 | 0.43 µs | 0.31 µs | 0.32 µs | 0.32 µs |
| 10000 | 4.6 µs | 3.1 µs | 3.2 µs | 3.2 µs |
| 100000 | 60 µs | 28 µs | 24 µs | 22 µs |

`BenchmarkHeartbeatDuringLookups` times single heartbeats while other goroutines look up one service as fast as they can, the case where heartbeats wait longest. Mean and 99th percentile:

| Instances | No lookups | 1 looking up | 4 looking up |
|---|---|---|---|
| 10000 | 4.0 µs, p99 2.9 µs | 24 µs, p99 112 µs | 84 µs, p99 0.30 ms |
| 100000 | 4.1 µs, p99 3.3 µs | 10 µs, p99 0.18 ms | 127 µs, p99 1.2 ms |

- Before the index, and before lookups stopped reserving room for the whole registry, four goroutines looking up in a loop held 10000 instances to about 400 heartbeats a second, with a p99 of 7 ms: fewer than the 1000 a second those instances send at the default TTL, so live instances could have expired. They now leave room for over 10000.
- More goroutines help little beyond two, since heartbeats need the lock to themselves.
- On this machine reading the clock takes about 2.4 µs, because the kernel uses the HPET clock source, which shows up in `BenchmarkServiceHeartbeat`, `BenchmarkHeartbeatDuringLookups` and every HTTP benchmark. Most servers read the TSC in tens of nanoseconds.

## Project layout

```text
.
├── client/                  # Go client for the HTTP API
├── cmd/beacon/              # Entry point: flags, config, logging, signals, cleanup worker and shutdown
├── internal/
│   ├── api/                 # HTTP transport: routes, request decoding, JSON errors, token and client certificate checks
│   ├── auth/                # Tokens file: token hashes, client certificate names and permissions
│   ├── discovery/           # Business rules: validation, IDs, TTL, cleanup; the Registry interface
│   ├── metrics/             # Prometheus metrics around the HTTP handler and any Registry backend
│   ├── ratelimit/           # Token buckets per caller or client network, bounded in size
│   ├── registry/
│   │   ├── memory/          # In-memory Registry backend
│   │   └── registrytest/    # Contract tests every Registry backend must pass
│   ├── tlstest/             # Test certificates: self-signed, and a small CA for mTLS
│   ├── credentials/         # Loads and reloads the TLS files and tokens file, swapping them in all at once
│   ├── bootstrap/           # Builds the backend, discovery layer, credentials and HTTP server and connects them
│   └── config/              # Loads and validates the TOML configuration and environment overrides
├── deployments/
│   ├── docker/              # Dockerfile
│   └── compose/             # Compose example
├── configs/                 # Example configuration
├── test/integration/        # A real server on a real port, driven by the Go client
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

**v0.2.0**

- [x] TLS for the HTTP API
- [x] Authentication with bearer tokens and per-token permissions
- [x] Client certificates (mTLS) as another way to authenticate
- [x] Reload certificates and tokens without a restart
- [x] Only the caller that registered an instance may change it
- [x] Limits on instances, per owner, and on how fast clients register or fail to authenticate
- [x] Paging for lookups
- [x] Lookups by name that cost what they return

**Next**

- [ ] gRPC API alongside HTTP
- [ ] Keep an instance's ID when it registers again, instead of issuing a new one
- [ ] Client-side caching and load balancing in the Go client
- [ ] Cache and database
- [ ] Scale, multiple nodes
- [ ] Rate limits shared between nodes, or enforced at the load balancer
- [ ] Trusted proxies, so rate limits can use the client address a known proxy forwards
- [ ] A stable owner ID for instance ownership and quotas, separate from the name shown in logs
- [ ] Idempotency

## Versioning

Beacon follows [Semantic Versioning](https://semver.org/). Releases are git tags such as `v0.1.0` on the `prod` branch, and every change is listed in [CHANGELOG.md](CHANGELOG.md).

| What | How it is versioned | What counts as breaking |
|---|---|---|
| Releases (binaries, tags) | `vMAJOR.MINOR.PATCH` | Before `v1.0.0` a breaking change bumps the minor version, anything else the patch version. From `v1.0.0` on, standard SemVer applies. |
| HTTP API | `/api/v1` in the path | Removing or renaming an endpoint or field, or changing its meaning. Adding endpoints or fields is not breaking and stays in `v1`. A breaking change moves to `/api/v2`, and `/api/v1` keeps working for at least one more minor release. |
| Configuration | Same as the release | Removing or renaming a key or `BEACON_*` variable, or changing a default in a way that changes behavior. Adding a key with a default is not breaking. |

Commit messages follow Conventional Commits, which map onto these rules: `feat!` or `BREAKING CHANGE` is breaking, `feat` adds functionality, and `fix` is a patch.

### Branches and releases

Changes move through three long-lived branches:

| Branch | Takes pull requests from | On every push |
|---|---|---|
| `main` (default) | Feature branches such as `feat/...` and `fix/...`, merged by the maintainer once reviewed | CI |
| `stage` | `main` only | CI, then the next release candidate is tagged and published |
| `prod` | `stage` only | CI, then the release is tagged and published |

A required `Promotion source` check refuses pull requests to `stage` or `prod` from any other branch. Both take merge commits only, so each holds exactly the commits of the branch before it and the next promotion merges cleanly.

To release:

1. On a branch from `main`, move the **Unreleased** entries in `CHANGELOG.md` under a new heading such as `## [0.2.0] - 2026-11-20`, and merge it.
2. Run `mise run promote-stage`. It opens the pull request from `main` to `stage`, named after the candidate it will produce, and stops if the version has already been released, which means step 1 was missed. Once it is merged, the [stage workflow](.github/workflows/stage.yml) tags the next candidate, such as `v0.2.0-rc.1`, and publishes it as a pre-release.
3. Try the candidate. To fix something, merge the fix into `main` and promote again; each merge into `stage` tags the next candidate, `v0.2.0-rc.2` and so on.
4. Run `mise run promote-prod`. It opens the pull request from `stage` to `prod`, after checking that `stage` is exactly the newest candidate. Once it is merged, the [production release workflow](.github/workflows/prod.yml) checks the same again, tags `v0.2.0`, and publishes the release.

Both commands print the open pull request instead if there already is one. They run the GitHub CLI, which mise installs for them, as you, so pull requests they open run CI like any other; log in once with `gh auth login`.

Publishing is done by the [release workflow](.github/workflows/release.yml): it runs the checks, builds archives for Linux, macOS and Windows on amd64 and arm64, publishes a GitHub release with checksums, and pushes the container image. Candidates become pre-releases and never move the image's `latest` tag. Pushing a `v*` tag by hand still runs it, for emergencies.

Tags are never moved or deleted once pushed.

## Contributing

Suggestions, bug reports and pull requests are welcome. Please read [CONTRIBUTING.md](CONTRIBUTING.md) first; it covers the development workflow, commit message format and what a good pull request includes.

## License

Beacon is released under the [MIT License](LICENSE).
