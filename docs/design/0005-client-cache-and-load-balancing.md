# 0005. Client-side caching and load balancing

Status: Proposed

## Context

Today a service calls `client.Discover` and then picks an instance itself. Done naively, that is one Beacon request per outgoing call. Beacon then becomes a dependency of every request: when it is slow, every caller is slow, and when it is down, nobody can reach anybody, even though every instance is still running. Measurements in 0.2.0 also showed that lookups are what contend with heartbeats.

Production service meshes avoid this the same way. gRPC's xDS client, Netflix Ribbon and Finagle keep a local copy of each service's endpoints, refresh it in the background, keep serving the last good copy while the registry is unreachable, and spread calls across endpoints locally.

## Decision

### `client.Resolver`

```go
resolver, err := beacon.NewResolver(ctx, "orders",
    client.WithRefreshInterval(5*time.Second),   // default 5s
    client.WithMaxStale(5*time.Minute),           // default 5m
    client.WithPicker(client.RoundRobin()),       // default
)
defer resolver.Close()

instance, err := resolver.Pick()     // no network call
all := resolver.Instances()          // the cached view
```

- **Startup:** `NewResolver` fetches once and fails if that fails, so a misconfigured name is reported at startup, not on the first call.
- **Refresh:** a background goroutine, with jitter, refreshes every interval. On failure it keeps the last good view and retries with backoff. The view is served until it is `MaxStale` old. After that, `Pick` returns `ErrStale`, because sending traffic on a picture that is minutes old is worse than failing fast.
- **No instances:** `Pick` returns `ErrNoInstances`, distinct from `ErrStale`, so callers can tell "nothing is registered" from "Beacon is unreachable".
- **Pickers:** `RoundRobin()` uses an atomic counter and is the default. `Random()` is the other option. A `Picker` interface lets users plug in their own, such as consistent hashing by key.
- **Updates:** the view is replaced atomically (`atomic.Pointer`), so `Pick` takes no lock.

### Conditional requests on the server

`GET /api/v1/services` answers with a weak `ETag` that covers what discovery cares about, the set of `(id, name, address, port)`, and not `last_heartbeat`, which changes on every heartbeat. A request with a matching `If-None-Match` gets `304 Not Modified` and no body. RFC 9110 allows weak validators exactly for "semantically equivalent" representations. A resolver refresh with nothing changed then costs one lookup and a hash on the server, and a few hundred bytes on the wire.

## Alternatives considered

- **Watch or long polling.** Consul blocking queries and etcd watches push changes instead of polling. They react faster but need a change feed in every backend: a condition variable in memory, `LISTEN/NOTIFY` in PostgreSQL, fan-out across nodes. Polling with `304` gets most of the benefit at a fraction of the complexity. A watch fits naturally on the gRPC API (0009) later.
- **Load-aware pickers** (least requests, power of two choices). They need per-instance load signals the resolver does not have. The `Picker` interface leaves room for them.
- **Caching inside `Discover`.** That makes a plain function call surprisingly stateful. A separate type with a lifecycle (`Close`) is explicit.

## Consequences

- Callers make no Beacon request per call, and survive Beacon outages for `MaxStale`.
- New instances are seen within the refresh interval, and gone ones within the interval after their TTL. Callers should still handle connection errors to a picked instance, for example by picking again.
- One goroutine per resolved name.

## Open questions

- Should `Pick` skip instances that recently failed (outlier detection)? Default: not in this round. Callers can report failures to their own picker.
