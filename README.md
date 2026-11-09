# Beacon

**Where services find each other.**

A lightweight, self-hosted service discovery and registry solution written in Go.

Beacon aims to provide a simple and reliable way for applications to register, discover, and monitor service instances without introducing unnecessary infrastructure complexity.

> 🚧 **Work in Progress**
>
> Beacon is currently under active development. Features, APIs, and architecture are subject to change.

---

## Overview

In distributed applications, services need a reliable way to find and communicate with each other.

Beacon aims to solve this problem by providing a centralized service registry where applications can register their instances, report their availability, and discover other services dynamically.

Designed with simplicity and minimal infrastructure requirements in mind, Beacon will be available as a standalone service with Docker support and a Go client library.

## Goals

- **Simplicity** – Easy to deploy, configure, and integrate.
- **Lightweight** – Minimal resource consumption and infrastructure overhead.
- **Developer-friendly** – Straightforward APIs and a native Go client.
- **Self-hosted** – Full control over your service discovery infrastructure.
- **Extensible** – Designed to support additional capabilities as the project evolves.

## Planned Features

- Service registration and unregistration
- Service discovery by name
- Unique service instance identification
- Heartbeat-based health monitoring
- Automatic removal of stale service instances
- HTTP REST API
- Native Go client SDK
- Docker and Docker Compose support
- Health and readiness endpoints
- Structured logging
- Prometheus metrics
- Configuration through environment variables

## Architecture

Beacon is being designed as a lightweight, standalone service registry.

Its initial architecture will focus on a single-node deployment with in-memory service registration and HTTP-based communication.

More architectural details will be documented as implementation progresses.

## Deployment

Beacon is intended to support multiple deployment options:

- Standalone executable
- Docker container
- Docker Compose

Deployment instructions will be provided once the initial release is available.

## Development

Beacon uses [mise](https://mise.jdx.dev/) to pin tool versions (Go and `golangci-lint`) and to run project tasks.

```bash
mise install          # install the pinned tools
mise run fmt          # format Go code with gofmt
mise run vet          # run go vet
mise run lint         # run golangci-lint
mise run test         # run tests
mise run test-race    # run tests with the race detector
mise run build        # build bin/beacon
mise run run          # start the server on :8080
mise run check        # fmt, vet, lint, and race-enabled tests
```

Run `mise tasks` to list every available task.

## Go Client

A native Go client library is planned to make integration with Go applications straightforward.

The client will provide a convenient interface for registering service instances, sending heartbeats, and discovering available services.

Documentation and usage examples will follow as the SDK develops.

## Roadmap

### Core

- [x] Initial service registry implementation
- [x] Service registration
- [x] Service unregistration
- [x] Heartbeat handling
- [x] Service discovery
- [x] Initial HTTP handlers and tests
- [x] Automatic cleanup scheduler
- [x] Graceful shutdown
- [ ] Core architecture refinement

### API

- [ ] Versioned REST API
- [ ] Request validation
- [ ] Standardized error responses
- [ ] API documentation
- [ ] Authentication and authorization

### Client SDK

- [ ] Go client library
- [ ] Automatic heartbeat management
- [ ] Registration lifecycle management
- [ ] Service discovery helpers
- [ ] Integration examples

### Deployment

- [ ] Docker image
- [ ] Docker Compose configuration
- [ ] Environment-based configuration
- [ ] Health checks
- [ ] GitHub Container Registry releases

### Observability

- [ ] Structured logging
- [ ] Prometheus metrics
- [ ] Operational health endpoints

### Project

- [ ] CI/CD pipeline
- [ ] Integration tests
- [ ] Contribution guidelines
- [ ] First stable release

## Contributing

Beacon is an open-source project and contributions will be welcome.

Contribution guidelines, development instructions, and issue templates will be introduced as the project matures.

Suggestions, bug reports, and ideas for improvement are always appreciated.

## License

MIT License.

See [LICENSE](LICENSE) for details.

---

**Beacon** — Discover services. Keep systems connected.
