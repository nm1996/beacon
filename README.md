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

- **Register, discover, heartbeat** over a small HTTP API, or gRPC, with instances removed when they stop sending heartbeats.
- **A Go client** that keeps an instance registered, retries safely, and caches and load-balances lookups.
- **Memory or PostgreSQL storage**, and several nodes over one database, without consensus.
- **Security when you turn it on:** TLS, tokens or client certificates, ownership of instances, and rate limits.
- **Operable:** one binary or container image, metrics, structured logs, and [written-down guarantees](docs/guides/guarantees.md) checked by an end-to-end [simulation](simulation/README.md).

The full list is in [Features](docs/guides/features.md), and what comes next in the [roadmap](ROADMAP.md).

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
| [Simulation](simulation/README.md) | Two nodes, a load balancer and application services run locally, with failures and their [reports](simulation/reports/README.md) |
| [Developing Beacon](docs/guides/development.md) | Building, testing, benchmarks, project layout |
| [Branches and releases](docs/guides/releasing.md) | How a change reaches a release |
| [Features](docs/guides/features.md) and [roadmap](ROADMAP.md) | Everything Beacon does, and what comes next |
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

## Versioning

Beacon follows [Semantic Versioning](https://semver.org/). Releases are git tags such as `v0.1.0` on the `prod` branch, and every change is listed in [CHANGELOG.md](CHANGELOG.md).

| What | How it is versioned | What counts as breaking |
|---|---|---|
| Releases (binaries, tags) | `vMAJOR.MINOR.PATCH` | Before `v1.0.0` a breaking change bumps the minor version, anything else the patch version. From `v1.0.0` on, standard SemVer applies. |
| HTTP API | `/api/v1` in the path | Removing or renaming an endpoint or field, or changing its meaning. Adding endpoints or fields is not breaking and stays in `v1`. A breaking change moves to `/api/v2`, and `/api/v1` keeps working for at least one more minor release. |
| Configuration | Same as the release | Removing or renaming a key or `BEACON_*` variable, or changing a default in a way that changes behavior. Adding a key with a default is not breaking. |

Commit messages follow Conventional Commits, which map onto these rules: `feat!` or `BREAKING CHANGE` is breaking, `feat` adds functionality, and `fix` is a patch.

How changes move from `main` through `stage` to a release on `prod` is described in [Branches and releases](docs/guides/releasing.md).

## Contributing

Suggestions, bug reports and pull requests are welcome. Please read [CONTRIBUTING.md](CONTRIBUTING.md) first; it covers the development workflow, commit message format and what a good pull request includes.

## License

Beacon is released under the [MIT License](LICENSE).
