# Features

Everything Beacon does, by area. The [roadmap](../../ROADMAP.md) shows
when each arrived and what comes next; the other guides explain each in
detail.

## Registry

| Feature | Guide |
|---|---|
| Register, discover and unregister service instances over HTTP | [HTTP API](http-api.md) |
| A unique ID for every instance, kept when it registers again with the same `id` | [HTTP API](http-api.md#post-apiv1services) |
| Safe retries of a registration with `Idempotency-Key` | [HTTP API](http-api.md#post-apiv1services) |
| Heartbeats keep an instance alive; instances without one are no longer returned, then removed | [Guarantees](guarantees.md) |
| Paging for lookups, and conditional lookups with `ETag` | [HTTP API](http-api.md) |
| Input validation with clear `400` responses, and JSON errors with stable codes | [HTTP API](http-api.md) |
| A versioned API under `/api/v1` | [Versioning](../../README.md#versioning) |
| An optional gRPC API alongside HTTP | [gRPC API](grpc-api.md) |

## Go client

| Feature | Guide |
|---|---|
| Automatic heartbeats and registering again with a `Lease` | [Go client](client.md) |
| Retries of a lost registration, without creating a second instance | [Go client](client.md) |
| Client-side caching and load balancing with a `Resolver`, and optional outlier detection | [Go client](client.md) |

## Storage and scale

| Feature | Guide |
|---|---|
| In-memory storage by default, for one process | [Operations](operations.md) |
| Optional PostgreSQL storage, kept across restarts, with batched heartbeats and a lookup cache | [Operations](operations.md#postgresql-storage) |
| Several nodes over one PostgreSQL database, without consensus or leader election | [Operations](operations.md#running-several-nodes) |
| Rate limits divided between the live nodes | [Security](security.md#rate-limits) |
| Written-down guarantees for every failure, tested end to end by the [simulation](../../simulation/README.md) | [Guarantees](guarantees.md) |

## Security

| Feature | Guide |
|---|---|
| Optional HTTPS (TLS 1.2+) | [Security](security.md#tls) |
| Optional bearer tokens with per-token permissions, and client certificates (mTLS) with the same permissions | [Security](security.md#authentication) |
| Only the caller that registered an instance may change it, by a stable owner ID | [Security](security.md#instance-ownership) |
| Limits on instances, per owner, and on how fast clients register or fail to authenticate | [Security](security.md#rate-limits) |
| Client addresses from trusted proxies | [Security](security.md#rate-limits) |
| Certificates and tokens reloaded without a restart | [Security](security.md#reloading-certificates-and-tokens) |

## Operations

| Feature | Guide |
|---|---|
| A TOML configuration file with validation, and environment variable overrides | [Configuration](configuration.md) |
| Structured JSON logs | [Operations](operations.md) |
| Prometheus metrics at `/metrics` | [Operations](operations.md) |
| `GET /health` for liveness checks | [HTTP API](http-api.md) |
| Graceful shutdown on `SIGINT` and `SIGTERM`, with server timeouts and request size limits | [Configuration](configuration.md) |
| A container image (amd64, arm64) with a built-in health check, and a Compose example | [Operations](operations.md) |
