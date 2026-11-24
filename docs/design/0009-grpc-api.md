# 0009. gRPC API alongside HTTP

Status: Proposed

## Context

Many service fleets speak gRPC internally and want typed, generated clients in several languages, deadlines that propagate, and HTTP/2 multiplexing. Beacon's HTTP API serves curl and simple clients well. gRPC serves generated clients, and it is where a streaming watch (0005) would naturally live later. The discovery layer was kept transport-neutral for this reason.

## Decision

- **Contract:** `api/beacon/v1/registry.proto`, package `beacon.v1`, following Google's API design guide and `buf lint`'s defaults:
  ```proto
  service RegistryService {
    rpc Register(RegisterRequest) returns (RegisterResponse);
    rpc Heartbeat(HeartbeatRequest) returns (HeartbeatResponse);
    rpc Unregister(UnregisterRequest) returns (UnregisterResponse);
    rpc ListInstances(ListInstancesRequest) returns (ListInstancesResponse);  // page_size / page_token
  }
  ```
  The fields mirror the HTTP API, including `id` (0003) and an `idempotency_key` field (0004). Paging uses `page_size` and `page_token`, which carry the same cursor as HTTP.
- **Generated Go code is committed** next to the proto, in `api/beacon/v1`, so `go get` users need no protoc. `mise run proto` regenerates it with pinned `protoc`, `protoc-gen-go` and `protoc-gen-go-grpc`, and CI fails if the committed code differs.
- **Server:** in `internal/grpcapi`, on its own port, `grpc.port`, default `0` = off. It has the same TLS and client certificate settings and uses `google.golang.org/grpc`.
- **Same rules:**
  - Authentication reads `authorization: Bearer <token>` metadata or the verified peer certificate, through the same `auth` identities, permissions and reloads as HTTP.
  - The gRPC interceptor applies the same rules as the HTTP Guard, with the same identities, failed-authentication limiter and failure reasons. (Implementation note: the identify-and-reserve steps are mirrored in `internal/grpcapi` rather than extracted into a shared function; extracting them is a follow-up once a third transport would need them.)
  - Registration rate limits are the same limiter instances, so a client cannot double its budget by switching transports.
- **Errors:** canonical status codes:

  | Situation | Status code |
  |---|---|
  | Invalid input | `InvalidArgument` |
  | Unknown instance | `NotFound` |
  | Instance owned by another caller | `PermissionDenied` |
  | Quotas and rate limits | `ResourceExhausted` |
  | Bad credentials | `Unauthenticated` |
  | Instance exists (0003) | `AlreadyExists` |

  The HTTP error code travels as `google.rpc.ErrorInfo.reason`, so both transports report the same machine-readable reason.
- **Health and discovery of the API:** the standard `grpc.health.v1` health service is registered, and so is server reflection, so `grpcurl` works.
- **Observability:** a unary interceptor records `beacon_grpc_requests_total{method,code}` and a duration histogram, the counterparts of the HTTP metrics.

## Alternatives considered

- **One port for both**, by serving gRPC through `net/http`'s `ServeHTTP` or with cmux. It saves a port, but `ServeHTTP` gives up grpc-go's own transport, which is slower and lacks some features, and cmux is a protocol-sniffing hack. Separate ports are the common practice.
- **Connect-go.** It is attractive, since one handler serves gRPC, gRPC-Web and JSON. But it is less common in ops tooling, and grpc-go is the reference implementation that users' generated clients expect.
- **No Go client over gRPC.** The existing Go client stays HTTP. Users who want gRPC from Go import the generated `RegistryServiceClient`. Two Go clients would double the maintenance.

## Consequences

- Typed clients in any language from one proto, and deadlines and cancellation end to end.
- New dependencies: `google.golang.org/grpc` and `google.golang.org/protobuf`, plus generated code to keep in step, which CI guards.
- One more listener to secure. It is off by default, and it reuses every setting the HTTP side has.

## Open questions

- A `Watch` streaming RPC for push updates. Default: next phase, once backends expose a change feed.
