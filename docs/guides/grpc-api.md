# gRPC API

The gRPC API that runs alongside HTTP.

Setting `grpc.port` serves the same registry over gRPC, for generated clients in any language. The contract is [`api/beacon/v1/registry.proto`](../../api/beacon/v1/registry.proto), and the generated Go code is in the same directory, importable as `github.com/nm1996/beacon/api/beacon/v1`.

```toml
[grpc]
port = 9090
```

```go
conn, err := grpc.NewClient("beacon:9090", grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{})))
registry := beaconv1.NewRegistryServiceClient(conn)

ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
registered, err := registry.Register(ctx, &beaconv1.RegisterRequest{Name: "orders", Address: "10.0.0.12", Port: 8081})
```

- `Register`, `Heartbeat`, `Unregister` and `ListInstances` behave as the HTTP endpoints do, including `id`, `idempotency_key` (the HTTP API's `Idempotency-Key`) and paging, here with `page_size` and `page_token`.
- The listener uses the same TLS settings, and the same authentication: an `authorization: Bearer <token>` metadata entry, or a client certificate. Permissions, ownership and quotas apply the same way.
- The rate limits are the HTTP API's own limiters, so a client has one budget across both transports. A gRPC peer is keyed by its connection's address; `server.trusted_proxies` does not apply.
- Errors use the standard status codes and carry a `google.rpc.ErrorInfo` with domain `beacon` and, as `reason`, the HTTP API's error code, such as `instance_not_owned`:

  | Code | Situation |
  |---|---|
  | `InvalidArgument` | Invalid input, or a reused idempotency key |
  | `NotFound` | Unknown instance |
  | `PermissionDenied` | Another caller's instance, or a missing permission |
  | `AlreadyExists` | The `id` is another instance's |
  | `ResourceExhausted` | Quotas, and rate limits, which also carry `google.rpc.RetryInfo` |
  | `Unauthenticated` | Missing or unknown credentials |

- The standard `grpc.health.v1` health service and server reflection are served without credentials, so `grpc_health_probe` and `grpcurl` work.
- `mise run proto` regenerates the Go code after changing the `.proto`, and CI fails if the committed code is out of date.
