# Developing Beacon

Building, testing and benchmarking Beacon, and how the code is laid out.

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
├── api/beacon/v1/           # gRPC contract (.proto) and its generated Go code
├── client/                  # Go client for the HTTP API
├── cmd/beacon/              # Entry point: flags, config, logging, signals, cleanup worker and shutdown
├── internal/
│   ├── api/                 # HTTP transport: routes, request decoding, JSON errors, token and client certificate checks
│   ├── grpcapi/             # gRPC transport: the same rules, credentials, limits and error reasons
│   ├── auth/                # Tokens file: token hashes, client certificate names and permissions
│   ├── discovery/           # Business rules: validation, IDs, TTL, cleanup; the Registry interface
│   ├── metrics/             # Prometheus metrics around the HTTP handler and any Registry backend
│   ├── ratelimit/           # Token buckets per caller or client network, bounded in size
│   ├── cluster/             # Which Beacon nodes share the storage backend, for dividing rate limits
│   ├── registry/
│   │   ├── memory/          # In-memory Registry backend
│   │   ├── postgres/        # PostgreSQL Registry backend, with its embedded migrations
│   │   ├── cache/           # Caches lookups in front of any backend
│   │   ├── heartbeats/      # Batches heartbeats for backends that can write many at once
│   │   └── registrytest/    # Contract tests every Registry backend must pass
│   ├── tlstest/             # Test certificates: self-signed, and a small CA for mTLS
│   ├── credentials/         # Loads and reloads the TLS files and tokens file, swapping them in all at once
│   ├── bootstrap/           # Builds the backend, discovery layer, credentials and HTTP server and connects them
│   └── config/              # Loads and validates the TOML configuration and environment overrides
├── deployments/
│   ├── docker/              # Dockerfile
│   └── compose/             # Compose example
├── configs/                 # Example configuration
├── docs/design/             # Design records: each architectural decision, its alternatives and trade-offs
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
