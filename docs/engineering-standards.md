# Engineering Standards

This document describes Beacon's engineering direction and the standards used to make technical decisions.

## Project Goal

Beacon is a lightweight, self-hosted service discovery and registry solution written in Go.

Its intended responsibilities are:

- Accept service instance registrations.
- Allow clients or services to discover registered instances.
- Track instance liveness through heartbeats.
- Expire stale registrations.
- Provide a straightforward HTTP API.
- Offer a Go client/SDK for application integration.

These are project goals, not a claim that every capability is already implemented. The README and API documentation must distinguish implemented functionality from planned work.

## Initial Product Direction

The initial design should favor:

- A single Beacon server instance.
- In-memory registry state.
- A simple HTTP API.
- Heartbeat-based liveness tracking.
- Time-to-live (TTL) expiration.
- A Go client.
- Container-based deployment when the server behavior is stable.

The initial design does not promise persistence across restarts, high availability, distributed consensus, or multi-node clustering. Registrations are expected to be recreated by clients after a Beacon restart.

Do not add persistence, Redis, a message broker, clustering, or consensus as an assumed requirement. Such capabilities require a separate design decision.

## Architectural Principles

### Keep the Registry Independent

The registry is the core domain component. It owns service registration state and operations such as registering, looking up, heartbeating, unregistering, and expiring instances.

It should not depend on HTTP request or response types.

### Keep Transport Logic at the Boundary

HTTP handlers should:

- Parse and validate incoming requests.
- Call the appropriate application or registry operation.
- Map results and errors to HTTP responses.
- Avoid implementing registry business rules themselves.

Transport-specific concerns should not leak into the registry.

### Keep the Public Client Separate

The public Go client belongs in a package that external Go applications can import. It should provide a clear and small API for the supported server capabilities.

The client should not import Beacon's private `internal/` packages.

### Make Lifecycle Explicit

Long-running processes and goroutines need clear ownership and shutdown behavior. Server shutdown, periodic cleanup, and client heartbeat loops must be cancellable and testable.

### Avoid Artificial Layers

Create packages when they have a meaningful responsibility, not merely to make the directory tree look comprehensive. A small codebase is preferable to a large number of thin wrappers.

## Proposed API Direction

The following paths are examples of a possible versioned HTTP API. They are not a finalized contract until implemented and documented:

| Method | Example path | Purpose |
|---|---|---|
| `POST` | `/api/v1/services` | Register a service instance |
| `POST` | `/api/v1/services/{id}/heartbeat` | Refresh instance liveness |
| `GET` | `/api/v1/services?name={name}` | Discover service instances |
| `DELETE` | `/api/v1/services/{id}` | Unregister an instance |
| `GET` | `/health` | Liveness check |
| `GET` | `/ready` | Readiness check |

Before finalizing the API, define request and response formats, validation rules, status codes, error representation, and compatibility expectations.

## Proposed Repository Structure

This is a target structure to evolve toward. Do not create empty directories or files simply to match this tree.

```text
beacon/
├── cmd/
│   └── beacon/
│       └── main.go
├── internal/
│   ├── app/
│   ├── bootstrap/
│   ├── registry/
│   ├── api/
│   │   └── http/
│   │       ├── handlers/
│   │       └── middleware/
│   ├── config/
│   └── health/
├── client/
├── api/
│   └── openapi.yaml
├── deployments/
│   ├── docker/
│   └── compose/
├── configs/
├── docs/
├── examples/
├── test/
│   └── integration/
├── scripts/
├── .github/
│   └── workflows/
├── AGENTS.md
├── CLAUDE.md
├── CONTRIBUTING.md
├── README.md
├── LICENSE
├── Makefile
├── .mise.toml
└── go.mod
```

### Directory Responsibilities

| Path | Responsibility |
|---|---|
| `cmd/beacon/` | Main executable entry point and dependency wiring |
| `internal/app/` | Application startup, orchestration, and lifecycle |
| `internal/bootstrap/` | Constructing components and wiring their dependencies together; no lifecycle or runtime behavior |
| `internal/registry/` | Core in-memory registry behavior |
| `internal/api/http/` | HTTP routing, handlers, and transport concerns |
| `internal/config/` | Configuration loading and validation |
| `internal/health/` | Liveness and readiness behavior |
| `client/` | Public Go SDK for external applications |
| `api/` | API contracts and OpenAPI specification |
| `deployments/` | Docker and Compose deployment assets |
| `configs/` | Example configuration files |
| `docs/` | Architecture, API, configuration, and development documentation |
| `examples/` | Small, runnable usage examples |
| `test/integration/` | Tests that exercise multiple components together |
| `scripts/` | Development and maintenance scripts |
| `.github/workflows/` | Automated CI and release workflows |

The exact package names and boundaries may change as the implementation develops. Keep the structure aligned with real responsibilities.

## Testing Strategy

Use the smallest test scope that gives confidence:

- Unit tests for registry behavior and validation.
- HTTP tests for request handling, status codes, and response formats.
- Integration tests for behavior across components.
- Concurrency tests for shared registry state and lifecycle behavior.
- Race detector runs for code that uses goroutines or shared mutable state.

Tests should verify observable behavior. Avoid tests that merely mirror implementation details.

## Dependencies

Prefer the standard library. Every additional dependency should have a clear benefit that justifies its maintenance, security, and compatibility cost.

Avoid adding a framework when a small standard-library implementation is sufficient.

## Compatibility and Versioning

Use Semantic Versioning when releases begin:

- `MAJOR`: incompatible public API changes.
- `MINOR`: backward-compatible functionality.
- `PATCH`: backward-compatible fixes.

Before `v1.0.0`, public APIs may evolve, but breaking changes should still be deliberate and documented.

## Operational Direction

As the project matures, consider:

- Structured logging.
- Graceful shutdown.
- Health and readiness endpoints.
- Container image and Compose support.
- CI checks and release automation.
- Metrics and operational documentation.

Implement these when the core behavior justifies them. Do not add observability or deployment scaffolding that the application cannot yet use.
