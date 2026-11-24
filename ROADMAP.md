# Roadmap

What Beacon does so far, release by release, and what comes next. Every
change is described in the [CHANGELOG](CHANGELOG.md), and the larger
decisions in the [design records](docs/design/README.md).

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
- [x] [Guarantees and failure behavior](docs/guides/guarantees.md) written down, and the README split into [guides](README.md#documentation)
- [x] Integration tests with two nodes over a real PostgreSQL database

**Unreleased**

- [x] A local end-to-end [simulation](simulation/README.md): two nodes over PostgreSQL behind a load balancer, application services, and scenarios that kill instances, nodes and the database, with reports in Git
- [x] Instances come back within one heartbeat interval after a PostgreSQL outage, instead of one interval per node

**Next**

- [ ] A `Watch` RPC that pushes changes instead of polling
- [ ] One authentication core shared by the HTTP and gRPC transports
- [ ] An advertised address for each node, for `GET /api/v1/nodes` behind NAT
- [ ] A list of Beacon URLs in the Go client, as failover without a load balancer (an open question of the [simulation](simulation/README.md#open-questions))
