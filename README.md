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
- [Status](#status)
- [Features](#features)
- [Quick start](#quick-start)
- [How it works](#how-it-works)
- [Documentation](#documentation)
- [Limitations](#limitations)
- [Roadmap](#roadmap)
- [Versioning](#versioning)
- [Contributing](#contributing)
- [License](#license)

## Why Beacon

In a distributed system, services need to know where their peers are running. Full-featured tools like Consul, etcd or ZooKeeper solve this, but they bring clustering, consensus and operational weight that many small deployments do not need.

Beacon covers the core of the problem and nothing more: one small binary, no external dependencies at runtime unless you choose PostgreSQL, and an API you can call with `curl`.

## Status

- **Version:** before 1.0. The HTTP API under `/api/v1` stays compatible within v1; the Go client, the gRPC API and the configuration may still change in a minor release, and every such change is in the [CHANGELOG](CHANGELOG.md).
- **Storage:** in memory (the default, one process) or PostgreSQL (kept across restarts, shared by several processes).
- **Several nodes:** supported over one PostgreSQL database, without consensus or leader election. What that does and does not guarantee is in [Guarantees and failure behavior](docs/guides/guarantees.md).
- **Not a goal:** a key-value store, configuration distribution, a service mesh, or replication across regions.

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
| ✅ | Limits on instances, per owner, and on how fast clients register or fail to authenticate |
| ✅ | Paging for lookups, and conditional lookups with `ETag` |
| ✅ | Stable instance IDs and safe retries with `Idempotency-Key` |
| ✅ | Optional PostgreSQL storage, with batched heartbeats and a lookup cache |
| ✅ | Several nodes over one PostgreSQL database |
| ✅ | Optional gRPC API alongside HTTP |
| ✅ | Client-side caching and load balancing in the Go client |

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

Instances of the same service share a name. Registering again creates a new instance with a new ID, unless the registration names the ID it had; see [`POST /api/v1/services`](docs/guides/http-api.md#post-apiv1services).

## Documentation

| Guide | What it covers |
|---|---|
| [HTTP API](docs/guides/http-api.md) | Every endpoint, request, response and error code |
| [gRPC API](docs/guides/grpc-api.md) | The gRPC service that runs alongside HTTP |
| [Go client](docs/guides/client.md) | Registering, keeping a lease, discovering, and the caching resolver |
| [Configuration](docs/guides/configuration.md) | Every setting, its environment variable and its default |
| [Security](docs/guides/security.md) | TLS, tokens, client certificates, ownership, rate limits, reloading credentials, exposing Beacon |
| [Operations](docs/guides/operations.md) | PostgreSQL, several nodes, Docker, metrics |
| [Guarantees and failure behavior](docs/guides/guarantees.md) | What is consistent, what may lag, and what happens when something fails |
| [Developing Beacon](docs/guides/development.md) | Building, testing, benchmarks, project layout |
| [Design records](docs/design/README.md) | Why the larger decisions were made |

## Limitations

Beacon is deliberately small. Before you depend on it, know that:

- **By default, state lives in memory.** Restarting Beacon forgets every instance. Services find out on their next heartbeat (`404`) and register again, under the same ID with a `Lease`. Use [PostgreSQL storage](docs/guides/operations.md#postgresql-storage) to keep instances across restarts.
- **Several nodes need PostgreSQL.** With the memory backend every node is a registry of its own; see [Running several nodes](docs/guides/operations.md#running-several-nodes). PostgreSQL itself is then what must stay available, and `GET /health` does not check it.
- **Nodes do not agree on everything at once.** With `registry.cache_ttl` on, a change through one node may take that long to show on another. See [Guarantees and failure behavior](docs/guides/guarantees.md).
- **Authentication is off by default.** Until you turn on [authentication](docs/guides/security.md#authentication), anyone who can reach the port can register, discover and remove instances. By default only the same machine can, since Beacon listens on `127.0.0.1`; see [Exposing Beacon to a network](docs/guides/security.md#exposing-beacon-to-a-network). [TLS](docs/guides/security.md#tls) encrypts the traffic but does not decide who may connect.
- **Ownership is by name, not by service.** Every instance registered with the same token or certificate belongs to the same caller, so two services sharing one token can change each other's instances. Give each service its own token or certificate.
- **Client certificates are not checked for revocation.** Use short-lived certificates.
- **A paused Beacon expires everything at once.** If the process is stopped for longer than `registry.service_ttl`, for example with `SIGSTOP` or a paused virtual machine, no heartbeats arrive meanwhile, and every instance expires when it resumes; their leases then register again. A suspended laptop does not count, since Beacon measures time with a clock that stops during suspend.

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

**v0.3.0** (see the [design records](docs/design/README.md))

- [x] gRPC API alongside HTTP
- [x] Keep an instance's ID when it registers again, instead of issuing a new one
- [x] Client-side caching and load balancing in the Go client, with optional outlier detection
- [x] PostgreSQL storage, with a lookup cache and batched heartbeats
- [x] Several nodes over one database
- [x] Rate limits divided between nodes
- [x] Trusted proxies, so rate limits can use the client address a known proxy forwards
- [x] A stable owner ID for instance ownership and quotas, separate from the name shown in logs
- [x] Idempotent registration with `Idempotency-Key`
- [x] Conditional lookups with `ETag` and `If-None-Match`
- [x] Heartbeats refused, not accepted, while a node cannot write them to PostgreSQL, with a metric for the writes
- [x] [Guarantees and failure behavior](docs/guides/guarantees.md) written down, and the README split into [guides](#documentation)
- [x] Integration tests with two nodes over a real PostgreSQL database

**Next**

- [ ] A `Watch` RPC that pushes changes instead of polling
- [ ] One authentication core shared by the HTTP and gRPC transports
- [ ] An advertised address for each node, for `GET /api/v1/nodes` behind NAT

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

1. On a branch from `main`, move the **Unreleased** entries in `CHANGELOG.md` under a new heading `## [X.Y.Z] - YYYY-MM-DD`, for the version being released, and merge it.
2. Run `mise run promote-stage`. It opens the pull request from `main` to `stage`, named after the candidate it will produce, and stops if the version has already been released, which means step 1 was missed. Once it is merged, the [stage workflow](.github/workflows/stage.yml) tags the next candidate, `vX.Y.Z-rc.1` for the first, and publishes it as a pre-release.
3. Try the candidate. To fix something, merge the fix into `main` and promote again; each merge into `stage` tags the next candidate, `vX.Y.Z-rc.2` and so on.
4. Run `mise run promote-prod`. It opens the pull request from `stage` to `prod`, after checking that `stage` is exactly the newest candidate. Once it is merged, the [production release workflow](.github/workflows/prod.yml) checks the same again, tags `vX.Y.Z`, and publishes the release.

Both commands print the open pull request instead if there already is one. They run the GitHub CLI, which mise installs for them, as you, so pull requests they open run CI like any other; log in once with `gh auth login`.

Publishing is done by the [release workflow](.github/workflows/release.yml): it runs the checks, builds archives for Linux, macOS and Windows on amd64 and arm64, publishes a GitHub release with checksums, and pushes the container image. Candidates become pre-releases and never move the image's `latest` tag. Pushing a `v*` tag by hand still runs it, for emergencies.

Tags are never moved or deleted once pushed.

## Contributing

Suggestions, bug reports and pull requests are welcome. Please read [CONTRIBUTING.md](CONTRIBUTING.md) first; it covers the development workflow, commit message format and what a good pull request includes.

## License

Beacon is released under the [MIT License](LICENSE).
