# Beacon — Development Roadmap

> **Project:** Beacon  
> **Tagline:** Where services find each other.  
> **Status:** Work in Progress  
> **Purpose:** A practical, self-hosted service discovery and registry solution written in Go.

This document is the implementation checklist for Beacon. Work through it in order, keep the checkboxes up to date, and avoid implementing later-stage features before the foundations are stable.

The roadmap describes the intended product. A checked item means the work is implemented, tested, and reflected in the documentation—not merely started.

---

## 0. Project Principles

These principles apply to every phase:

- [ ] Keep the first release focused on a reliable **single-node service registry**.
- [ ] Keep the registry in memory for the initial release. Be explicit that registrations are lost when Beacon restarts.
- [ ] Use heartbeats and TTL-based expiration to remove stale service instances.
- [ ] Keep business logic independent from HTTP transport.
- [ ] Prefer the Go standard library. Add dependencies only when they provide clear value.
- [ ] Keep the design small and understandable. Do not introduce abstractions without a current use case.
- [ ] Make lifecycle and shutdown behavior explicit, especially for goroutines.
- [ ] Preserve backward compatibility once an API is published, unless a breaking change is intentional and documented.
- [ ] Keep ChatGPT and Claude aligned through `AGENTS.md`, `CLAUDE.md`, and `docs/code-style.md`.
- [ ] Do not mark a feature complete until its tests and documentation are updated.

## 1. Repository Baseline and Developer Tooling

**Goal:** Make the repository predictable to work on from any supported development machine.

### 1.1 Repository structure

- [ ] Review the existing Service Discovery POC before moving or rewriting code.
- [ ] Establish the Go module path once the GitHub repository owner is confirmed.
- [x] Move the executable entry point to `cmd/beacon/`.
- [ ] Keep application implementation under `internal/`.
- [ ] Keep the future public Go SDK under `client/`.
- [ ] Keep deployment assets under `deployments/`.
- [ ] Keep project documentation under `docs/`.
- [ ] Avoid creating empty packages or directories for hypothetical future features.
- [ ] Add or review `.gitignore`, `.dockerignore`, and `.editorconfig`.

### 1.2 mise as the project tool manager

Use **mise** to pin project tool versions and expose repeatable development tasks. Do not rely on globally installed tools or undocumented commands.

- [x] Add a root `.mise.toml`.
- [x] Pin the Go version used by the project.
- [x] Add `golangci-lint` as a mise-managed tool and pin its version.
- [x] Add mise tasks for `fmt`, `vet`, `lint`, `test`, `test-race`, and `build`.
- [x] Make the task names and behavior clear in the README or `docs/development.md`.
- [ ] Verify that a fresh checkout can install the declared tools with `mise install`.
- [x] Verify that all project checks can be run through `mise run ...`.
- [ ] Do not add multiple formatters or linters that duplicate the same checks.

**Expected developer commands** (exact task names can be finalized in `.mise.toml`):

```bash
mise install
mise run fmt
mise run vet
mise run lint
mise run test
mise run test-race
mise run build
```

Formatting should use `gofmt` (and optionally `goimports` only if the project explicitly adopts it). `go vet` should be a separate check. `golangci-lint` should run the selected linter set. Tests should use the Go toolchain.

### 1.3 Git workflow

- [ ] Use Conventional Commits: `<type>(<scope>): <description>`.
- [ ] Keep commits focused on one logical change.
- [ ] Use short-lived branches such as `feat/automatic-cleanup` and `fix/heartbeat-timeout`.
- [ ] Merge changes into `main` through pull requests.
- [ ] Require formatting, linting, and tests to pass before merging.

Examples:

```text
feat(registry): add automatic stale service cleanup
fix(api): return not found for unknown service IDs
test(registry): cover concurrent registration
docs(config): document TOML configuration
chore(build): add mise development tasks
```

## 2. Import and Stabilize the Existing POC

**Goal:** Preserve the working prototype and establish a clean baseline before adding features.

The current POC already contains the core concepts: an in-memory map protected by `sync.RWMutex`, service registration, UUIDs, heartbeats, lookup, unregister, cleanup logic, HTTP handlers, routing, and tests. Verify the actual repository state before assuming every item is present.

- [ ] Bring the existing POC into the Beacon repository.
- [ ] Run the existing test suite and record the baseline.
- [ ] Run `go test -race ./...` and resolve any race reports.
- [ ] Review service and registry naming, exported APIs, and error behavior.
- [ ] Review validation for service name, address, and port.
- [ ] Ensure callers cannot accidentally mutate registry-owned state through returned pointers.
- [ ] Ensure all registry map access is protected by the appropriate mutex.
- [ ] Keep registry operations independent of HTTP request and response types.
- [ ] Remove prototype-only code only after confirming it is no longer needed.
- [ ] Update tests to match the intended behavior.

**Definition of done:** The POC is in the Beacon repository, the package boundaries are understandable, and all tests pass—including the race detector.

## 3. Registry Core and Automatic Cleanup

**Goal:** Make the in-memory registry safe and reliable as the central component.

### 3.1 Registry behavior

- [ ] Define the service model and its required fields.
- [ ] Define clear registry errors (for example, invalid service data and unknown service ID).
- [ ] Implement registration and assign a unique instance ID.
- [ ] Record registration and last-heartbeat timestamps consistently.
- [ ] Implement heartbeat updates.
- [ ] Implement lookup by service name.
- [ ] Implement unregister.
- [ ] Implement stale-instance cleanup based on a configurable TTL.
- [ ] Define behavior for duplicate service instances and document it.
- [ ] Add tests for invalid input, unknown IDs, duplicate cases, and time boundaries.

### 3.2 Cleanup lifecycle

- [ ] Implement a cleanup worker that runs periodically.
- [ ] Make the cleanup interval configurable.
- [ ] Use `context.Context` to stop the worker.
- [ ] Ensure the worker exits cleanly during application shutdown.
- [ ] Ensure cleanup does not race with registration, heartbeat, lookup, or unregister.
- [ ] Test cancellation and shutdown behavior.
- [ ] Test that expired instances are removed and active instances remain.

Do not start an unmanaged goroutine from a package-level function. The application lifecycle should own the worker and be responsible for stopping it.

**Definition of done:** Registry operations are covered by unit tests, concurrent access is race-safe, and cleanup can be started and stopped predictably.

## 4. TOML Configuration

**Goal:** Make server and registry settings configurable without scattering constants throughout the code.

Use a TOML file for application configuration. Keep the example configuration in `configs/beacon.example.toml`. The application should have sensible defaults so it can start without requiring every setting to be present.

### 4.1 Configuration design

- [ ] Define a typed configuration structure in `internal/config/`.
- [ ] Decide and document the configuration loading order.
- [ ] Support an explicit config file path (for example, a `--config` command-line flag).
- [ ] Load and parse TOML into typed Go structures.
- [ ] Apply defaults for omitted optional settings.
- [ ] Validate required values and ranges after parsing.
- [ ] Return actionable errors that identify the invalid setting.
- [ ] Keep configuration loading separate from business logic.
- [ ] Avoid reading environment variables throughout the application; centralize overrides in the config package.
- [ ] Document which settings can be overridden by environment variables.
- [ ] Do not commit secrets or production credentials in example files.

Recommended precedence:

1. Built-in defaults.
2. TOML configuration file.
3. Environment variable overrides.
4. Explicit command-line overrides, where applicable.

The precedence must be deterministic and documented. If a higher-priority source does not provide a value, retain the value from the lower-priority source.

### 4.2 Initial configuration sections

Start with only settings that the first server actually needs:

- `server`: HTTP listen address and port, read/write/idle timeouts.
- `registry`: service TTL and cleanup interval.
- `logging`: log level and format.
- Future sections such as TLS, metrics, or clustering should be added only when those features are implemented.

An initial example is provided in `configs/beacon.example.toml`. Treat it as a starting point; keep it synchronized with the actual Go configuration structure.

### 4.3 Configuration tests

- [ ] Test loading a valid TOML file.
- [ ] Test missing optional values and defaults.
- [ ] Test invalid TOML syntax.
- [ ] Test invalid values and validation errors.
- [ ] Test environment variable overrides.
- [ ] Test behavior when the requested config file does not exist.
- [ ] Test that the example configuration can be parsed by the application.

**Definition of done:** Configuration is loaded once during startup, validated, and passed explicitly to the components that need it.

## 5. HTTP Server and API

**Goal:** Expose a stable, versioned HTTP API without putting business logic in handlers.

### 5.1 HTTP server foundation

- [ ] Create the HTTP server in the application/server layer.
- [ ] Keep route registration separate from handler implementation.
- [ ] Use the standard library HTTP server unless a concrete need justifies another router.
- [ ] Configure read, write, and idle timeouts.
- [ ] Add graceful shutdown using the application context.
- [ ] Ensure server shutdown also stops background workers.

### 5.2 Versioned API

Proposed initial endpoints (confirm final request/response contracts before implementation):

| Method | Endpoint | Purpose |
|---|---|---|
| `POST` | `/api/v1/services` | Register a service instance |
| `POST` | `/api/v1/services/{id}/heartbeat` | Refresh its heartbeat |
| `GET` | `/api/v1/services?name={name}` | Discover instances by service name |
| `DELETE` | `/api/v1/services/{id}` | Unregister an instance |
| `GET` | `/health` | Basic process health |
| `GET` | `/ready` | Readiness to accept traffic |

- [ ] Define request and response structures.
- [ ] Define consistent JSON field names.
- [ ] Define status codes for success, invalid input, missing instances, and internal errors.
- [ ] Return consistent error responses.
- [ ] Validate request bodies and path/query parameters.
- [ ] Set appropriate content types.
- [ ] Add handler tests using `httptest`.
- [ ] Add API documentation and an OpenAPI specification after contracts stabilize.

Do not treat the proposed endpoints as implemented until they exist and are tested.

## 6. Application Lifecycle and Server Integration

**Goal:** Wire components together in one predictable startup and shutdown flow.

- [x] Add the executable entry point under `cmd/beacon/`.
- [ ] Load and validate configuration before starting services.
- [ ] Initialize logging.
- [x] Construct the registry and HTTP handlers (`internal/bootstrap`).
- [ ] Start the cleanup worker under the application lifecycle.
- [ ] Start the HTTP server.
- [ ] Handle operating-system termination signals.
- [ ] Shut down the HTTP server gracefully.
- [ ] Stop background workers and wait for them to exit.
- [ ] Return startup and shutdown errors to the caller.
- [ ] Add tests for important lifecycle behavior where practical.

**Definition of done:** Beacon starts from its CLI entry point and shuts down without leaving active goroutines behind.

## 7. Public Go Client SDK

**Goal:** Make it straightforward for another Go service to register itself and discover other services.

- [ ] Define the public client API before implementing it.
- [ ] Create the package under `client/`.
- [ ] Provide client construction with a Beacon server URL and options.
- [ ] Implement service registration.
- [ ] Implement heartbeat support.
- [ ] Implement service discovery.
- [ ] Implement unregister and a clear `Close`/shutdown behavior if the client owns background work.
- [ ] Use contexts for request cancellation and deadlines.
- [ ] Configure HTTP timeouts.
- [ ] Define retry behavior carefully; avoid retrying non-idempotent operations blindly.
- [ ] Make heartbeat interval configurable.
- [ ] Add unit tests with `httptest` and focused integration tests.
- [ ] Add a small usage example.

Keep the SDK independent from Beacon's `internal/` packages so external Go applications can import it.

## 8. Docker and Local Development Deployment

**Goal:** Make Beacon easy to run locally and in a container.

- [ ] Add a multi-stage Dockerfile.
- [ ] Build a small runtime image.
- [ ] Run the process as a non-root user.
- [ ] Expose only the required application port.
- [ ] Add a container health check using the health endpoint.
- [ ] Support mounting a TOML configuration file.
- [ ] Add a Docker Compose example for local development.
- [ ] Document ports, volumes, environment overrides, and startup commands.
- [ ] Verify the container starts, becomes healthy, and shuts down correctly.

The initial deployment is a single Beacon instance. Do not imply that the in-memory registry is highly available or persistent.

## 9. Observability and Operational Readiness

**Goal:** Make the service understandable and diagnosable in a real environment.

- [ ] Use structured logs.
- [ ] Include useful context in logs without exposing secrets.
- [ ] Log startup configuration safely (never log credentials).
- [ ] Log registration, removal, and cleanup events at appropriate levels.
- [ ] Add Prometheus metrics only when the metric names and operational use cases are clear.
- [ ] Consider metrics for registered instances, cleanup removals, request counts, and request duration.
- [ ] Document health and readiness semantics.
- [ ] Document current limitations: in-memory state, single-node operation, and restart behavior.

## 10. CI, Quality Gates, and Releases

**Goal:** Ensure every change is checked consistently and releases are reproducible.

### 10.1 Continuous integration

- [ ] Add a GitHub Actions workflow for formatting checks.
- [ ] Run `go vet ./...`.
- [ ] Run unit tests.
- [ ] Run race-enabled tests.
- [ ] Run `golangci-lint`.
- [ ] Build the application.
- [ ] Use the Go version pinned by mise.
- [ ] Make required checks visible on pull requests.

### 10.2 Release process

- [ ] Choose and document a release versioning policy based on Semantic Versioning.
- [ ] Prepare release notes.
- [ ] Build release binaries for the target platforms.
- [ ] Publish the container image to GHCR when the image workflow is ready.
- [ ] Tag releases consistently (for example, `v0.1.0`).
- [ ] Verify installation and startup instructions from a clean environment.

## 11. Documentation and Examples

**Goal:** Make the project usable by someone who has never seen the source code.

- [ ] Keep the README accurate and avoid documenting unimplemented features as available.
- [ ] Document architecture and component responsibilities.
- [ ] Document configuration options and precedence.
- [ ] Document API endpoints and example requests/responses.
- [ ] Document local development setup using mise.
- [ ] Document Docker usage.
- [ ] Add a basic registration example.
- [ ] Add a service discovery example.
- [ ] Add troubleshooting guidance.
- [ ] Document limitations and non-goals for the first release.

## 12. First Public Release

**Goal:** Publish a small, honest, useful first version.

- [ ] Confirm the first-release scope.
- [ ] Ensure the README matches the actual implementation.
- [ ] Ensure configuration examples are valid.
- [ ] Ensure all CI checks pass.
- [ ] Run tests with the race detector.
- [ ] Verify Docker build and runtime behavior.
- [ ] Review license and contribution instructions.
- [ ] Tag `v0.1.0`.
- [ ] Publish release notes describing capabilities and limitations.

---

## Suggested Package Layout

This is a direction, not a requirement to create every directory immediately.

```text
beacon/
├── cmd/
│   └── beacon/
│       └── main.go
├── internal/
│   ├── app/
│   ├── bootstrap/
│   ├── config/
│   ├── registry/
│   ├── api/
│   │   └── http/
│   └── health/
├── client/                 # Public Go SDK
├── configs/
│   └── beacon.example.toml
├── deployments/
│   ├── docker/
│   └── compose/
├── docs/
├── examples/
├── test/
│   └── integration/
├── .github/
│   └── workflows/
├── .mise.toml
├── AGENTS.md
├── CLAUDE.md
├── CONTRIBUTING.md
├── README.md
├── go.mod
└── LICENSE
```

Add packages such as `observability/` or a separate `discovery/` package only when their responsibilities are real and distinct. Do not split the registry into artificial layers.

## Working Agreement for AI Coding Assistants

Before each task, ChatGPT and Claude must:

1. Read `AGENTS.md` and `docs/code-style.md`.
2. Inspect the current code before proposing edits.
3. Identify the smallest set of files needed for the task.
4. Explain significant design decisions and trade-offs.
5. Avoid unrelated refactoring.
6. Add or update tests for changed behavior.
7. Run the relevant mise tasks (or state clearly why they could not be run).
8. Review the final diff for accidental changes.
9. Report what changed, what was tested, and any remaining concerns.
10. Never create a commit, push changes, or change project architecture unless explicitly asked.

## Current Focus

Work on the earliest incomplete phase only. The recommended immediate sequence is:

1. Import and stabilize the existing POC.
2. Add the project `.mise.toml` and verify the development tasks.
3. Implement and test the cleanup worker lifecycle.
4. Add typed TOML configuration and its tests.
5. Integrate configuration, registry, cleanup, and HTTP server through the application lifecycle.

Do not begin the SDK, Docker publishing, metrics, or clustering until the core server is stable.
