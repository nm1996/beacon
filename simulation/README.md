# Local simulation: test plan

> [!NOTE]
> This is the test plan, and the simulation is being built from it one
> phase at a time; see [Phases](#phases) for what exists. It records what we
> want to learn, how each test is set up, what Beacon's documented behavior
> says should happen, and what would count as a bug.

## Running it

Needs Docker with Compose v2. From the repository root:

```sh
docker compose -f simulation/compose.yaml up --build --wait
curl -s localhost:18080/api/v1/nodes    # through the load balancer
curl -s localhost:18081/health          # beacon-a
curl -s localhost:18082/health          # beacon-b
docker compose -f simulation/compose.yaml down --volumes
```

Each application logs its registration, heartbeat failures and
unregistration as JSON:

```sh
docker compose -f simulation/compose.yaml logs orders-1
docker compose -f simulation/compose.yaml stop orders-1   # logs "unregistered"
```

Then run a scenario. The runner prints each check and writes a JSON report
to `simulation/reports/`, and exits non-zero if a check failed:

```sh
RUNNER_UID=$(id -u) RUNNER_GID=$(id -g) \
  docker compose -f simulation/compose.yaml run --rm runner -scenario normal
```

The scenarios that stop, kill and start containers run from the host, with
`run.sh`. It starts the project, runs scenario A before each of them, and
leaves the project running:

```sh
simulation/run.sh                    # A, then B and D in their variants
simulation/run.sh instance-killed    # one scenario
```

| `run.sh` scenario | Plan | Runner check |
|---|---|---|
| `normal` | A | `-scenario normal` |
| `instance-killed` | B, killed | `-scenario gone -within 9s` |
| `instance-stopped` | B, stopped | `-scenario gone -within 1s` |
| `instance-back` | D | `gone`, then `back -old-id <its ID>` |
| `instance-back-same-id` | D with `Registration.ID` | `back -want-id orders-2` |
| `node-stopped`, `node-killed` | C | `node-down -within 1s` or `15s`, then `normal` |
| `node-stopped-retry-post`, `node-killed-retry-post` | C, nginx with `non_idempotent` | the same |
| `storage-down` | E | `storage-down -outage 20s -recover 15s -within 4s` |
| `ownership` | F | `ownership -service orders -attacker-token-file /auth/payments.token`, before and right after `beacon-b` restarts |

For each step, `run.sh` starts the runner first with `-since` 10 seconds
ahead, then stops, kills or starts the container at that moment, so the
runner is already watching and its timings start at the step itself. A
runner that was not ready by then fails with "the runner is ready before
-since". The containers share the host's clock. For `storage-down` the
runner also sends heartbeats through both nodes before PostgreSQL stops.

Authentication is off unless `BEACON_SIM_AUTH=true` is set for every
Compose command; leave it unset, not `false`, to keep it off. `ownership`
does that itself: it writes a random token per caller to
`simulation/auth/`, which Git ignores, with the tokens file holding their
hashes, runs with authentication on, then turns it off again.

`--wait` returns once every health check passes. The ports are published on
loopback only. `down --volumes` also deletes the database, so the next run
starts clean.

## Why

Unit, contract and integration tests check Beacon's pieces, and the
PostgreSQL integration tests run two nodes in one process. None of them runs
Beacon the way it is meant to be deployed: separate containers, a real
network, a load balancer, application services that come and go, and
processes that get killed. This simulation does that, to answer two
questions:

1. **Does Beacon work?** Do services find each other, stay registered while
   alive, and disappear when they die, within the bounds in
   [Guarantees and failure behavior](../docs/guides/guarantees.md)?
2. **Can it be operated?** When a node, an application instance or PostgreSQL
   fails, does everything recover without a person stepping in, and can an
   operator see what happened from logs and metrics?

Every expected outcome below comes from `guarantees.md` or the code. If the
simulation shows something else, either Beacon has a bug or the guide is
wrong; both are findings.

## What inspection of the repository found

These shape the design and are not guesses:

| What | Value |
|---|---|
| Image | Built from `deployments/docker/Dockerfile` with the repository root as context. Distroless, runs as `nonroot`, sets `BEACON_SERVER_HOST=0.0.0.0` |
| Ports | HTTP `8080`. gRPC off by default (`BEACON_GRPC_PORT=0`) |
| Health | `GET /health`, and `beacon --health-check` for the container health check. It does not check PostgreSQL |
| Two nodes | `BEACON_STORAGE_BACKEND=postgres`, `BEACON_STORAGE_POSTGRES_URL` shared, `BEACON_SERVER_NODE_ID` per node |
| Faster lifecycle | `BEACON_REGISTRY_SERVICE_TTL`, `BEACON_REGISTRY_CLEANUP_INTERVAL`, `BEACON_STORAGE_POSTGRES_HEARTBEAT_FLUSH_INTERVAL` |
| Client | `client.New(baseURL)`, `Announce` returns a `Lease`, `Lease.Close`, `Discover`, `NewResolver` with `Pick` and `Report` |
| Lease timing | A heartbeat every third of the TTL, ±10% jitter. Each attempt times out after a sixth of the TTL. Failures retry from 1s, doubling, capped at a third of the TTL. Registration sends an `Idempotency-Key` and makes up to three attempts |
| Metrics | `beacon_registry_operations_total`, `beacon_heartbeat_flushes_total`, `beacon_expired_instances_total`, `beacon_cluster_nodes`, `beacon_http_requests_total` |

Three findings affect how the simulation must be built:

1. **The Go client takes one Beacon URL and has no failover.** An
   application pointed at node A cannot send heartbeats once A is gone, and
   its instance expires. Several nodes are meant to sit behind a load
   balancer (see [Running several nodes](../docs/guides/operations.md#running-several-nodes)),
   so the simulation puts one in front of both nodes. Client-side failover
   across endpoints would be a feature request, not part of this work.
2. **Registered addresses only resolve inside the Compose network**, such as
   `orders-1:9000`. A runner started on the host could not call the
   instances, so the runner runs as a Compose service.
3. **Nothing in the simulation gets the Docker socket.** Stopping and
   killing containers is done by a small script on the host, which then runs
   the runner to observe and measure.

One small documentation bug turned up as well: the `Lease` comment in
`client/lease.go` still says it registers again "under a new ID", which has
not been true since 0.3.0. That is a separate fix.

## Setup

```text
orders-1, orders-2 ─┐         ┌──► beacon-a ──┐
payments-1         ─┼──► lb ──┤               ├──► postgres
inventory-1        ─┘         └──► beacon-b ──┘

runner ──► beacon-a and beacon-b directly: discovery, /api/v1/nodes, /metrics
runner ──► each instance's /work, picked through client.Resolver
```

- **postgres**: one container with a named volume, only for this Compose project.
- **beacon-a, beacon-b**: built from the local checkout, so every run tests
  the current code, not a published image. Node IDs `beacon-a` and
  `beacon-b`. Both expose `/metrics`.
- **lb**: a pinned nginx image, round robin across both nodes. Applications
  reach Beacon only through it. When one node is down, connection errors must
  go to the other one; see the failover tests below for the retry setting
  that needs. It has a fixed address, `172.29.53.10`, which both nodes trust
  as a proxy, so rate limits see each application's own address instead of
  the load balancer's. It looks node names up again every 5s, since a
  restarted container can come back with a new address.
- **Application services**: one small Go binary run as `orders`, `payments`
  or `inventory`. It serves `GET /health` and `GET /work`, the latter
  returning its service name, instance ID and a timestamp. It registers
  through `client.Announce` with its Compose service name as address, and
  calls `Lease.Close` on `SIGTERM`. Each instance is its own Compose service
  (`orders-1`, `orders-2`, …) instead of `--scale`, so every instance has a
  stable DNS name to register.
- **runner**: a Go command, run with `docker compose run --rm runner -scenario <name>`.
  It writes a JSON report to `simulation/reports/`, which is mounted into
  it and ignored by Git, prints a summary, and exits non-zero when a
  check fails.
- **run.sh**: drives the failure steps from the host with `docker compose
  stop`, `kill` and `start`, and calls the runner before and after each
  step.

### Settings for the simulation

Short enough to see expiry in seconds, valid under Beacon's own config rules:

| Setting | Value | Why |
|---|---|---|
| `BEACON_REGISTRY_SERVICE_TTL` | `9s` | Heartbeats every ~3s, expiry within seconds |
| `BEACON_REGISTRY_CLEANUP_INTERVAL` | `1s` | Removal follows expiry closely |
| `BEACON_STORAGE_POSTGRES_HEARTBEAT_FLUSH_INTERVAL` | `1s` (default) | At most a third of the TTL, as config requires |
| `BEACON_REGISTRY_CACHE_TTL` | `0s` (default) | Cross-node changes visible at once; a later run can turn it on to measure staleness |
| `BEACON_RATE_LIMIT_REGISTRATIONS_PER_SECOND` | default | Keep it, so the load profiles show whether it gets in the way |
| Authentication | off at first | Then one run with tokens on, to check ownership across nodes |

### Timing bounds that follow from these settings

With a TTL of 9s, a Lease sends a heartbeat every 2.7s to 3.3s. So:

- An instance **killed** (no `Lease.Close`) last sent a heartbeat at most
  3.3s before. Lookups stop returning it when that heartbeat is older than
  the TTL: **between about 5.7s and 9s after the kill**, and never later than
  9s. Cleanup deletes it within 1s after that (`beacon_expired_instances_total`
  goes up).
- An instance **stopped gracefully** unregisters in `Lease.Close`: it is
  gone from lookups **at once**, through either node, since the cache is off.
- A registration through one node is visible through the other **at once**.
- A node stopped with `SIGTERM` leaves `/api/v1/nodes` **at once**. A node
  killed with `SIGKILL` leaves it **within 15s**.

## Scenarios

Each scenario lists its steps, what should happen, and what counts as a
failure. Every number in a report is measured; nothing is assumed.

### A. Normal operation

1. Start everything and wait for the health checks, with a bounded timeout.
2. `/api/v1/nodes` on both nodes lists `beacon-a` and `beacon-b`.
3. Discover `orders`, `payments` and `inventory` through each node
   separately.
4. Call `/work` on every discovered instance.

**Expect:** both nodes return the same set of instances, 4 in all, with
valid IDs and addresses and no duplicates. Every `/work` call succeeds and
reports the instance ID that Beacon returned for it.
**Fail if:** an instance is missing on either node, appears twice, or does
not answer. Record discovery and request latency, p50, p95 and p99.

### B. An application instance dies

1. With A passing, `docker compose kill orders-2`.
2. Poll discovery through both nodes every 250ms and record when `orders-2`
   disappears.
3. Keep calling `orders` through the resolver throughout.

**Expect:** `orders-2` disappears from both nodes between about 5.7s and 9s
after the kill, and `orders-1` stays and answers throughout. With
`Resolver.Report`, failed calls to `orders-2` before it expires are skipped.
**Fail if:** `orders-2` is still returned more than 9s after the kill,
`orders-1` drops out at any point, or the two nodes disagree for longer than
one poll.

Then repeat with `docker compose stop orders-2` (graceful): it should
disappear at once.

### C. A Beacon node fails

1. With A passing, `docker compose stop beacon-a`, the graceful variant.
2. Discover through `beacon-b`, and call every instance.
3. Watch every instance for the next 30s, more than three TTLs. None may
   expire, since their heartbeats now reach `beacon-b` through the load
   balancer.
4. `docker compose start beacon-a`, wait for its health check, and discover
   through it.
5. Repeat with `docker compose kill beacon-a`.

**Expect:** no instance leaves discovery at any point. `beacon-b` lists only
itself at once after the graceful stop, and within 15s after the kill.
After the restart, `beacon-a` returns the same instances as `beacon-b` at
once, and `/api/v1/nodes` lists both again.
**Fail if:** any instance expires, which would mean heartbeats did not fail
over. If that happens, check whether the load balancer retried, before
calling it a Beacon bug. Record how many heartbeats and registrations
failed during the failover, from the application logs and from
`beacon_http_requests_total` on `beacon-b`.

This does not make Beacon highly available: PostgreSQL is still a single
point of failure.

**Load balancer detail to test:** nginx does not pass a `POST` on to the
next server once it has sent the request, unless `proxy_next_upstream`
includes `non_idempotent`. Heartbeats are `POST`s and are safe to repeat.
Registrations are safe too, because the client sends an `Idempotency-Key`.
Run C once without `non_idempotent` and once with it, and record the
difference in failed requests. The Lease retries either way.

### D. An application instance comes back

1. Kill `orders-2` and wait until it disappears, as in B.
2. `docker compose start orders-2`.
3. Record the time until it is discoverable again, and call it.

**Expect:** the new process registers under a new ID, because the
simulation registers without a fixed ID, and is discoverable through both
nodes within a second of its registration. The old ID never returns.
**Fail if:** the old ID appears again, or the new one takes longer than its
first successful registration plus one poll.

A variant with a fixed `Registration.ID` (`orders-2`) should get the same
ID back. If the old instance has not expired yet, it is the same instance,
answered `200 OK`.

### E. PostgreSQL goes away (after A–D pass)

1. With A passing, `docker compose stop postgres` for 20s, more than two
   TTLs, then `docker compose start postgres`.
2. Throughout, poll discovery through both nodes, call instances, and
   scrape `/metrics`.

**Expect, from `guarantees.md` and a manual check with a real binary:**

- Lookups fail with `500 internal_error` while it is down.
- Heartbeats are accepted for at most two flush intervals (2s), then fail
  with `500`. `beacon_heartbeat_flushes_total{result="error"}` rises, and
  every Lease retries with backoff.
- `/health` stays `200` on both nodes, which is documented.
- After PostgreSQL is back, both nodes serve again without a restart.
  Instances whose last stored heartbeat is older than the TTL are missing
  from lookups until their next heartbeat, at most about 3.3s later. If
  cleanup ran first and removed them, their next heartbeat gets `404` and
  the Lease registers them again.

**Fail if:** a node needs a restart to recover, any heartbeat is answered
`204` more than 2s into the outage, or an instance whose application is
still running is missing from discovery more than one heartbeat interval
after recovery (about 3.3s).

### F. Ownership across nodes (with authentication on)

1. Turn on authentication, with one token per service.
2. `orders` registers through the load balancer, which picks a node.
3. Using the `payments` token, try heartbeats and `DELETE` for an `orders`
   instance through both nodes.

**Expect:** `403 instance_not_owned` from both nodes, every time.
**Fail if:** either node lets it through, even once. This matters most
right after a node restart, while the node has not learned the owners yet.

## Load

After A–E pass, the same setup with more instances of each service, still
on a laptop:

| Profile | Instances | Runner load |
|---|---|---|
| small | 10 | 5 lookups/s, 20 `/work` calls/s |
| medium | 50 | 20 lookups/s, 100 `/work` calls/s |
| large | 100 | 50 lookups/s, 200 `/work` calls/s |

Each profile runs for a configurable time, 5 minutes by default, and
records:

- registration, heartbeat, lookup and request outcomes;
- latency at p50, p95 and p99;
- `beacon_registry_operations_total` and `beacon_heartbeat_flushes_total`;
- `docker stats` for CPU and memory, sampled by `run.sh`.

The question is not how fast Beacon is, but whether any instance expires
while it is alive under load, which would be a bug, and how much CPU and
memory two nodes need for 100 instances.

## Reports

One JSON file per run, such as `simulation/reports/2026-11-24T180000Z-normal.json`,
with the scenario, start and end time, the endpoints used, the instances
expected and seen, every step with its result and timing, request and error
totals, latency percentiles, recovery times, metric snapshots from both
nodes before and after, and the overall result. No tokens.

## What counts as done

- Scenarios A–D pass from a clean checkout on a second machine, by
  following this README's commands only.
- Deliberately breaking a scenario, for example a TTL shorter than the
  heartbeat interval, makes the runner exit non-zero.
- E and F have run at least once, with their reports kept in the pull
  request that adds them.
- Every finding is either fixed in its own pull request or recorded here
  as a known limitation.

## Phases

Each phase is one pull request, and each one is run before the next starts.

1. Compose with PostgreSQL, both nodes and the load balancer; `/api/v1/nodes` lists both.
2. The application service; registration, heartbeats and graceful shutdown seen in the logs.
3. The runner and scenario A, with the first report.
4. Scenarios B and D, with `run.sh`.
5. Scenario C, both load balancer settings.
6. Scenarios E and F.
7. Load profiles.

## Open questions

- Should a client-side list of Beacon URLs, as failover without a load
  balancer, become a roadmap item? The simulation will show how much the
  load balancer alone covers.
- Is `GET /health` not checking PostgreSQL acceptable for orchestrators that
  restart unhealthy containers? Scenario E will show what an operator sees.

## Findings

What runs have shown so far, and what became of it.

- **A load balancer that neither retries POSTs nor stops sending to a
  dead node loses instances when a node goes away.** Seen while building
  scenario C, with the nodes and applications run as local processes and a
  stand-in load balancer with neither behavior: one instance expired 7.2s
  after `beacon-a` stopped. Every heartbeat it sent had an even chance of
  reaching the dead node, and the Lease's retries, after 1s, 2s, then
  every 3s, did not land on the live one within the TTL. With the stand-in
  retrying POSTs on the other node, nothing expired, after a stop or a
  kill. The nginx configuration here does both: it stops sending to a node
  for 2s after one failure, and the `-retry-post` scenarios also retry
  POSTs. The Compose runs will show whether the first alone is enough.
  This is what the open question on client-side failover is about.
- **Scenarios E and F behave as documented**, in the same local setup with
  a real PostgreSQL stopped for 20s: lookups failed with `500` 47ms after
  the stop, heartbeats were refused within the 2s allowance, `/health`
  stayed `200`, both nodes served again 0.3s after PostgreSQL started, and
  every instance was back 2.6s later. `payments`' token got `403
  instance_not_owned` on every heartbeat and unregister of `orders`'
  instances through both nodes, also right after `beacon-b` restarted. A
  token with `manage`, as a check that the scenario can fail, removed them.
