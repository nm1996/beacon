# Configuration

Every setting, its environment variable and its default.

Beacon reads its settings from a TOML file passed with `--config` and from environment variables:

```bash
beacon --config /etc/beacon/beacon.toml
```

Each setting is taken from the first of these that provides it:

1. An environment variable such as `BEACON_SERVER_PORT`
2. The TOML file given with `--config`
3. The built-in default

A file only needs the keys it changes, and no file is needed at all. [`configs/beacon.example.toml`](../../configs/beacon.example.toml) lists every key with its default value.

| Key | Environment variable | Default | Meaning |
|---|---|---|---|
| `server.host` | `BEACON_SERVER_HOST` | `"127.0.0.1"` | Interface to listen on. The default accepts local connections only; see [Exposing Beacon to a network](security.md#exposing-beacon-to-a-network). The container image sets `0.0.0.0` |
| `server.port` | `BEACON_SERVER_PORT` | `8080` | TCP port, `1`–`65535` |
| `server.read_timeout` | `BEACON_SERVER_READ_TIMEOUT` | `"5s"` | Time allowed to read a request, headers included |
| `server.write_timeout` | `BEACON_SERVER_WRITE_TIMEOUT` | `"10s"` | Time allowed to write a response |
| `server.idle_timeout` | `BEACON_SERVER_IDLE_TIMEOUT` | `"60s"` | Time an idle keep-alive connection stays open |
| `server.shutdown_timeout` | `BEACON_SERVER_SHUTDOWN_TIMEOUT` | `"10s"` | Time in-flight requests get to finish on shutdown |
| `server.max_request_body_bytes` | `BEACON_SERVER_MAX_REQUEST_BODY_BYTES` | `65536` | Largest accepted request body |
| `server.reload_interval` | `BEACON_SERVER_RELOAD_INTERVAL` | `"0s"` | How often to check the TLS files and tokens file for changes; `0s` is off. See [Reloading](security.md#reloading-certificates-and-tokens) |
| `server.trusted_proxies` | `BEACON_SERVER_TRUSTED_PROXIES` (comma-separated) | `[]` | Addresses or CIDR ranges of proxies whose `X-Forwarded-For` gives the client address for [rate limits](security.md#rate-limits) |
| `server.node_id` | `BEACON_SERVER_NODE_ID` | `""` | Names this node among those sharing PostgreSQL, such as its pod name; empty picks a random ID at every start |
| `grpc.port` | `BEACON_GRPC_PORT` | `0` | Port of the [gRPC API](grpc-api.md), on `server.host`; `0` is off |
| `tls.cert_file` | `BEACON_TLS_CERT_FILE` | `""` | PEM certificate chain to serve HTTPS with; see [TLS](security.md#tls) |
| `tls.key_file` | `BEACON_TLS_KEY_FILE` | `""` | PEM private key for `tls.cert_file` |
| `tls.client_ca_file` | `BEACON_TLS_CLIENT_CA_FILE` | `""` | PEM bundle of CAs whose client certificates Beacon accepts; see [Client certificates (mTLS)](security.md#client-certificates-mtls) |
| `auth.enabled` | `BEACON_AUTH_ENABLED` | `false` | Require a bearer token on every endpoint except `/health`; see [Authentication](security.md#authentication) |
| `auth.tokens_file` | `BEACON_AUTH_TOKENS_FILE` | `""` | File listing the accepted tokens. Required when `auth.enabled` is `true` |
| `registry.service_ttl` | `BEACON_REGISTRY_SERVICE_TTL` | `"30s"` | Time without a heartbeat before an instance is removed |
| `registry.cleanup_interval` | `BEACON_REGISTRY_CLEANUP_INTERVAL` | `"5s"` | How often stale instances are removed |
| `registry.max_name_length` | `BEACON_REGISTRY_MAX_NAME_LENGTH` | `63` | Longest accepted service name, 1–253. The default fits a DNS label |
| `registry.max_address_length` | `BEACON_REGISTRY_MAX_ADDRESS_LENGTH` | `253` | Longest accepted host name as an address, 1–253. IP addresses are not held to it |
| `registry.max_instances` | `BEACON_REGISTRY_MAX_INSTANCES` | `10000` | The most instances Beacon holds at once; further registrations get `429 quota_exceeded`. Bounds memory use and the size of discovery responses |
| `registry.max_instances_per_owner` | `BEACON_REGISTRY_MAX_INSTANCES_PER_OWNER` | `0` | The most instances one token or certificate may hold at once, so no single caller can take every place; `0` is no limit. Needs [authentication](security.md#instance-ownership), and must not exceed `registry.max_instances` |
| `registry.cache_ttl` | `BEACON_REGISTRY_CACHE_TTL` | `"0s"` | How long each node keeps lookup results before asking storage again, at most a third of `registry.service_ttl`; `0s` is off. See [PostgreSQL storage](operations.md#postgresql-storage) |
| `rate_limit.registrations_per_second` | `BEACON_RATE_LIMIT_REGISTRATIONS_PER_SECOND` | `20` | Registrations each caller may make per second on average; `0` is off. See [Rate limits](security.md#rate-limits) |
| `rate_limit.registration_burst` | `BEACON_RATE_LIMIT_REGISTRATION_BURST` | `200` | Registrations a caller may make at once before the rate applies |
| `rate_limit.auth_failures_per_second` | `BEACON_RATE_LIMIT_AUTH_FAILURES_PER_SECOND` | `1` | Failed authentication attempts each client network may make per second on average; `0` is off |
| `rate_limit.auth_failure_burst` | `BEACON_RATE_LIMIT_AUTH_FAILURE_BURST` | `20` | Failed attempts a network may make at once before the rate applies |
| `storage.backend` | `BEACON_STORAGE_BACKEND` | `"memory"` | Where instances are kept: `memory`, or `postgres` to keep them across restarts. See [PostgreSQL storage](operations.md#postgresql-storage) |
| `storage.postgres.url` | `BEACON_STORAGE_POSTGRES_URL` | `""` | Connection string for `postgres`, such as `postgres://beacon:secret@db:5432/beacon?sslmode=verify-full`. Never logged |
| `storage.postgres.heartbeat_flush_interval` | `BEACON_STORAGE_POSTGRES_HEARTBEAT_FLUSH_INTERVAL` | `"1s"` | How often each process writes the heartbeats it received, in batches, at most a third of `registry.service_ttl`; `0s` writes each at once. See [PostgreSQL storage](operations.md#postgresql-storage) |
| `metrics.enabled` | `BEACON_METRICS_ENABLED` | `true` | Serve Prometheus metrics at `GET /metrics` |
| `logging.level` | `BEACON_LOGGING_LEVEL` | `"info"` | `debug`, `info`, `warn` or `error` |
| `logging.format` | `BEACON_LOGGING_FORMAT` | `"json"` | `json` or `text` |

```bash
BEACON_SERVER_PORT=9090 BEACON_LOGGING_FORMAT=text beacon --config /etc/beacon/beacon.toml
```

Environment variables hold plain values without TOML quotes, for example `BEACON_REGISTRY_SERVICE_TTL=2m`. A variable that is set applies even when it is empty, so `BEACON_SERVER_HOST=` listens on all interfaces.

Durations use Go syntax such as `"500ms"`, `"5s"` or `"1m30s"`. All durations and sizes must be positive, `registry.service_ttl` must be a whole number of seconds and at least `1s`, because clients are told it as `ttl_seconds`, and `registry.cleanup_interval` must not be longer than `registry.service_ttl`.

Beacon refuses to start, and lists every problem it found, if the file is missing, is not valid TOML or contains an unknown key, if an environment variable cannot be parsed, or if a setting ends up out of range. Range errors name the TOML key, for example `server.port`, whichever source set it.

Logs go to standard output. Run `beacon --version` to print the build version.
