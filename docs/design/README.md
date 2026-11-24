# Design records

Each file here records one architectural decision for Beacon: the problem, the options weighed, the choice and why, and what it costs. They are written before the code, so review can disagree with the idea before it is built. Once accepted, a record is not rewritten. A later record supersedes it instead.

| # | Decision | Status |
|---|---|---|
| [0001](0001-trusted-proxies.md) | Client address behind trusted proxies | Proposed |
| [0002](0002-owner-id.md) | A stable owner ID, separate from the display name | Proposed |
| [0003](0003-stable-instance-ids.md) | Instances keep their ID when they register again | Proposed |
| [0004](0004-idempotency.md) | Idempotent registration with `Idempotency-Key` | Proposed |
| [0005](0005-client-cache-and-load-balancing.md) | Client-side caching and load balancing | Proposed |
| [0006](0006-postgres-and-cache.md) | PostgreSQL storage, with a read cache | Proposed |
| [0007](0007-multiple-nodes.md) | Several Beacon nodes over one database | Proposed |
| [0008](0008-cluster-rate-limits.md) | Rate limits across nodes | Proposed |
| [0009](0009-grpc-api.md) | gRPC API alongside HTTP | Proposed |

The records build on each other in this order, and so do the pull requests that implement them.

## How the pieces fit

```text
             clients: Go client (cache + picker), HTTP, gRPC
                                 │
                          load balancer ── trusted proxy (0001)
                     ┌───────────┴───────────┐
               Beacon node              Beacon node        stateless (0007)
            HTTP + gRPC (0009)       HTTP + gRPC (0009)
            rate limit share (0008)  rate limit share (0008)
            read cache (0006)        read cache (0006)
                     └───────────┬───────────┘
                           PostgreSQL (0006)
                 instances · owners (0002) · nodes (0007)
```

A single node with the in-memory backend stays the default and keeps working exactly as before. Every feature above is opt-in through configuration.

## Template

```markdown
# NNNN. Title

Status: Proposed | Accepted | Superseded by NNNN

## Context
What is wrong or missing today, with the constraints that matter.

## Decision
What Beacon will do, precisely enough to review the code against it.

## Alternatives considered
Each serious option, and why it lost.

## Consequences
What gets better, what gets worse, and what operators must now know.

## Open questions
What is not settled, and the default taken meanwhile.
```
