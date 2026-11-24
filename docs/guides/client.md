# Go client

The Go client in `github.com/nm1996/beacon/client`.

The [`client`](../../client) package is a Go client for the HTTP API. It uses only the standard library, so it adds nothing else to your build.

```bash
go get github.com/nm1996/beacon/client
```

Most services need two calls: announce themselves when they start, and look up the services they call.

```go
beacon, err := client.New("http://beacon:8080", client.WithLogger(logger))
if err != nil {
	return err
}

// Register and keep the registration alive until shutdown.
lease, err := beacon.Announce(ctx, client.Registration{Name: "orders", Address: "10.0.0.12", Port: 8081})
if err != nil {
	return err
}
defer lease.Close(context.Background())

// Call another service: keep its instances in memory and pick one per call.
payments, err := beacon.NewResolver(ctx, "payments")
if err != nil {
	return err
}
defer payments.Close()

instance, err := payments.Pick() // no request to Beacon
```

A `Resolver` is the client-side cache and load balancer most services want instead of calling `Discover` for every request:

- **Startup:** `NewResolver` fetches the instances once and fails if that fails, so a wrong address or name shows at startup.
- **Refresh:** a background goroutine refreshes every 5 seconds (`client.WithRefreshInterval`), with jitter. It sends the last `ETag`, so while nothing changes Beacon answers `304` with no body.
- **Picking:** `Pick` chooses an instance without a request, in turn by default. `client.WithPicker(client.Random())` picks at random, and any type with a `Pick([]client.Instance) client.Instance` method plugs in, such as consistent hashing.
- **While Beacon is down:** the `Resolver` keeps serving the instances it last saw and retries with backoff. After 5 minutes (`client.WithMaxStale`), `Pick` returns `client.ErrStale`, since routing on a picture that old is worse than failing.
- **No instances:** `Pick` returns `client.ErrNoInstances`, so callers can tell an empty service from an unreachable Beacon.
- **Failing instances:** with `client.WithOutlierDetection(client.OutlierDetection{ConsecutiveFailures: 5})`, callers report each call with `resolver.Report(instance, err)`, and an instance that fails that many times in a row is skipped for 30 seconds (`EjectionTime`), longer each time it happens again, up to ten times as long. At most half the instances (`MaxEjectedPercent`) are skipped at once, and never the last one, so a failure on the caller's own side cannot empty the service. It is off by default.
- **Close:** stops the refreshes. A picked instance may still have just gone away, so handle connection errors by picking again.

`Discover` fetches the current instances on every call, for tools and one-off lookups.

A `Lease` sends a heartbeat every third of the TTL Beacon reported, with a little jitter. If a heartbeat fails it retries after 1s, 2s, 4s and so on, never waiting longer than the normal interval, and keeps trying until `Close`. Each heartbeat or registration the lease sends gives up after a sixth of the TTL, so one stuck on a dead connection leaves time to retry on a new one before the instance expires, whatever timeout the `http.Client` has; with a TTL of 3s or less that cap is reached at once, so retries come at a steady third of the TTL. If Beacon no longer knows the instance, because it restarted or the instance expired, the lease registers it again under the same ID, so callers caching instances by ID see no change. Set `Registration.ID`, for example to the pod or host name, to choose the ID; otherwise the ID Beacon issued first is kept. Only if that ID has meanwhile been taken by another instance, or its heartbeats get `403` because another caller owns it, does the lease register under a new one, logging a warning once, and `lease.ID()` then returns the new ID. `Close` stops the background goroutine, waits for it and unregisters the instance. If its context ends first, or Beacon cannot be reached, `Close` returns the error and can simply be called again with a new context; the heartbeats stay stopped either way, and once the instance is unregistered further calls return `nil`. With `client.WithLogger`, retries and re-registrations are logged.

For full control, call the API directly:

```go
registered, err := beacon.Register(ctx, client.Registration{Name: "orders", Address: "10.0.0.12", Port: 8081})
err = beacon.Heartbeat(ctx, registered.ID)
err = beacon.Unregister(ctx, registered.ID)
```

Errors from Beacon are `*client.Error` values carrying the status and Beacon's error code, and they match `client.ErrNotFound` and `client.ErrInvalid` with `errors.Is`. `client.WithHTTPClient` sets a custom `http.Client`; by default every request times out after 10 seconds. `Register` sends an `Idempotency-Key` and retries, twice at most, when Beacon cannot be reached or a proxy answers `502`, `503` or `504`; the key lets Beacon find the instance a lost response created, so a retry never adds a second one. A registration whose `ID` belongs to another instance matches `client.ErrConflict`. `Discover` fetches instances 500 at a time and follows the page links, so a large registry never has to fit in one response; it works against servers without paging too. The result is a live view rather than a snapshot, as described under [`GET /api/v1/services`](http-api.md#get-apiv1services). If the paging breaks, for example a proxy drops the cursor from the next link or sends the client round in a loop, `Discover` returns an error rather than a partial list.

When Beacon has [authentication](security.md#authentication) on, give the client its token. Read it from a file or secret store rather than writing it into the code:

```go
beacon, err := client.New("https://beacon:8080", client.WithToken(os.Getenv("BEACON_TOKEN")))
```

The client then sends `Authorization: Bearer <token>` on every request. Errors for refused requests match `client.ErrUnauthorized` (`401`) and `client.ErrForbidden` (`403`) with `errors.Is`, including a `401` or `403` from a proxy in front of Beacon. A heartbeat or unregister of an instance another caller registered also matches `client.ErrNotOwned`; see [Instance ownership](security.md#instance-ownership). A registration refused because Beacon is full matches `client.ErrQuotaExceeded`; a `Lease` that has to register again keeps retrying with backoff until there is room. A request refused by a [rate limit](security.md#rate-limits), or any `429` without Beacon's error format, such as a proxy's, matches `client.ErrRateLimited`. A `Lease` whose heartbeats are refused keeps retrying with backoff and logs each failure; it never registers again because of them, though once its instance expires it gets `404` and registers again as usual.

For a Beacon served over [TLS](security.md#tls), use an `https://` URL. If its certificate comes from a private CA, trust that CA through `WithHTTPClient`:

```go
caPEM, err := os.ReadFile("/etc/beacon/ca.pem")
if err != nil {
	return err
}

roots := x509.NewCertPool()
if !roots.AppendCertsFromPEM(caPEM) {
	return errors.New("no certificates in ca.pem")
}

beacon, err := client.New("https://beacon:8080", client.WithHTTPClient(&http.Client{
	Timeout: 10 * time.Second,
	Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
	},
}))
```

`WithToken` and `WithHTTPClient` can be combined.

To authenticate with a [client certificate](security.md#client-certificates-mtls) instead of a token, load it into the same `tls.Config`:

```go
certificate, err := tls.LoadX509KeyPair("/etc/orders/tls/cert.pem", "/etc/orders/tls/key.pem")
if err != nil {
	return err
}

beacon, err := client.New("https://beacon:8080", client.WithHTTPClient(&http.Client{
	Timeout: 10 * time.Second,
	Transport: &http.Transport{
		TLSClientConfig: &tls.Config{
			RootCAs:      roots,
			Certificates: []tls.Certificate{certificate},
			MinVersion:   tls.VersionTLS12,
		},
	},
}))
```

A certificate Beacon does not trust fails during the TLS handshake, so the error is a connection error rather than a `*client.Error`.
