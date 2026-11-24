# Security

TLS, authentication and instance ownership, rate limits, reloading credentials, and exposing Beacon to a network.

## TLS

Beacon serves plain HTTP unless you give it a certificate. Set both `tls.cert_file` and `tls.key_file` to serve HTTPS instead:

```toml
[tls]
cert_file = "/etc/beacon/tls/cert.pem"
key_file = "/etc/beacon/tls/key.pem"
```

- Both files are PEM. `cert_file` holds the server certificate first, followed by any intermediate certificates. `key_file` holds its private key.
- Set both or neither. Beacon refuses to start if only one is set, or if the files are missing, unreadable or do not belong together.
- With TLS on, the API, `/health` and `/metrics` are served over HTTPS only, on the same port. HTTP/2 is offered. TLS 1.2 is the minimum version.
- Renewed certificates are picked up without a restart; see [Reloading certificates and tokens](#reloading-certificates-and-tokens).
- To have clients prove who they are with certificates as well, see [Client certificates (mTLS)](#client-certificates-mtls).
- Without TLS, Beacon logs a warning at startup if it listens on anything other than a loopback address.

For local testing, make a self-signed certificate valid for `localhost`:

```bash
mkdir -p certs
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -days 30 \
  -subj "/CN=localhost" -addext "subjectAltName=DNS:localhost,IP:127.0.0.1" \
  -keyout certs/key.pem -out certs/cert.pem

BEACON_TLS_CERT_FILE=certs/cert.pem BEACON_TLS_KEY_FILE=certs/key.pem go run ./cmd/beacon
curl --cacert certs/cert.pem https://localhost:8080/health
```

In production, use a certificate from your own CA or a public one, issued for the name clients use to reach Beacon.

## Authentication

Beacon accepts every request unless you turn authentication on. With `auth.enabled`, every endpoint except `GET /health` needs a bearer token:

```http
Authorization: Bearer <token>
```

or, over [TLS](#tls) with `tls.client_ca_file` set, a [client certificate](#client-certificates-mtls). Both kinds of caller are listed in the same file, `auth.tokens_file`, and get the same permissions.

Turn on [TLS](#tls) as well, unless a TLS proxy sits in front of Beacon: tokens sent over plain HTTP can be read by anyone on the network. Beacon logs a warning at startup when tokens would cross the network unencrypted.

### Creating tokens

A token is any string of at least 16 printable ASCII characters without spaces. Generate a random one, then let Beacon hash it:

```bash
TOKEN=$(openssl rand -hex 32)
printf '%s' "$TOKEN" | beacon --hash-token
# 3f5c...e1a9
```

`beacon --hash-token` reads the token from standard input, so it stays out of your shell history and the process list, and prints its SHA-256 hash. Give the token to the service that will use it, and put only the hash in the tokens file.

### The tokens file

```toml
# /etc/beacon/tokens.toml
[[tokens]]
id = "orders"                # optional; owns instances and quotas, defaults to name
name = "orders (2026 token)"
sha256 = "3f5c...e1a9"
permissions = ["register", "discover"]

[[tokens]]
name = "prometheus"
sha256 = "9b20...47d0"
permissions = ["metrics"]

[[tokens]]
name = "admin"
sha256 = "c71e...0b3f"
# No permissions line: discover, register and metrics, but not manage.
```

```toml
# beacon.toml
[auth]
enabled = true
tokens_file = "/etc/beacon/tokens.toml"
```

| Permission | Allows |
|---|---|
| `register` | `POST /api/v1/services`, and `POST /api/v1/services/{id}/heartbeat` and `DELETE /api/v1/services/{id}` for the instances the same caller registered |
| `discover` | `GET /api/v1/services` |
| `metrics` | `GET /metrics` |
| `manage` | Together with `register`: heartbeats and unregistering for instances other callers registered. Never granted unless listed |

- `name` identifies the token in logs and error messages. It is not secret, and must be unique across tokens and certificates.
- `id` is optional. It is who owns the instances the entry registers, and who `registry.max_instances_per_owner` and the registration rate limit count. Without it, the ID is the `name`. Give entries an `id` so they can be renamed, or their token rotated under a new entry, without their instances changing owner. Entries that share an `id` share ownership and quota, for example a token and a certificate for one service. At most 255 printable ASCII characters without spaces, so a SPIFFE ID fits.
- `sha256` is the hex hash printed by `beacon --hash-token`, in either case. Each token must be unique.
- `permissions` is optional. Leave it out to allow `discover`, `register` and `metrics`. `manage` must always be listed, together with `register`, so a token written without a permission list can never touch another service's instances. An empty list is rejected, and so is `manage` without `register`.
- Beacon refuses to start if the file is missing, contains an unknown key or an invalid entry, or lists neither tokens nor certificates. Changes take effect on the next [reload](#reloading-certificates-and-tokens), without a restart.
- A file of hashes is not enough to call Beacon, but keep it readable only by Beacon anyway.

### Instance ownership

With authentication on, Beacon records which token or certificate registered each instance, by its `name`. Only that caller may send the instance's heartbeats or unregister it; anyone else with `register` gets `403` with the code `instance_not_owned`. Every caller with `discover` still sees every instance, since finding other services is the point of a registry.

- An operator who needs to remove a stuck instance of another service uses an entry with `permissions = ["register", "manage"]`, alongside `discover` if it should also list instances.
- The owner is the entry's `id`, or its `name` if it has none. Renaming an entry that has an `id`, or rotating its token under a new entry with the same `id`, changes nothing. Changing the `id`, or the `name` of an entry without one, means its existing instances belong to the old owner: their heartbeats get `403`, they expire after `registry.service_ttl`, and a [`Lease`](client.md) then gets `404` and registers again under the new owner. Refused-request logs show both the `caller` name and the `caller_id`.
- Owners appear in the `service registered` log line (`owner`), and whoever unregistered an instance in `service unregistered` (`by`). `GET /api/v1/services` does not show them.
- `registry.max_instances_per_owner` limits how many instances each owner may hold at once, so one service, or one leaked token, cannot fill Beacon and lock everyone else out. Registrations beyond it get `429 quota_exceeded`, logged as `registration refused: owner quota reached` with the `caller`. Other owners are not affected.
- Without authentication nothing is recorded or checked, and anyone may change any instance. The per-owner limit does not apply then; only `registry.max_instances` does.

### Client certificates (mTLS)

Services that already have certificates from your own CA, for example from cert-manager, Vault or a service mesh, can use them instead of tokens. Set `tls.client_ca_file` to the CA bundle, and list each certificate in the tokens file by one of its subject alternative names:

```toml
# beacon.toml
[tls]
cert_file = "/etc/beacon/tls/cert.pem"
key_file = "/etc/beacon/tls/key.pem"
client_ca_file = "/etc/beacon/tls/clients-ca.pem"

[auth]
enabled = true
tokens_file = "/etc/beacon/tokens.toml"
```

```toml
# /etc/beacon/tokens.toml, next to any [[tokens]]
[[certificates]]
name = "orders"
dns = "orders.internal"
permissions = ["register", "discover"]

[[certificates]]
name = "payments"
uri = "spiffe://example.org/ns/prod/sa/payments"
permissions = ["discover"]
```

- `tls.client_ca_file` needs TLS (`tls.cert_file` and `tls.key_file`) and `auth.enabled`. Beacon refuses to start without them, or when the bundle holds no PEM certificate, or when the tokens file has no `[[certificates]]`. Entries without `tls.client_ca_file` are only warned about, since they can never match.
- Each entry sets exactly one of `dns`, compared case-insensitively with the certificate's DNS names, or `uri`, compared exactly with its URI names, such as a SPIFFE ID. Wildcards are not matched, and the subject's common name is ignored. When several entries match one certificate, the first in the file wins.
- Clients are asked for a certificate but not required to send one, so `/health` and bearer tokens keep working on the same port. A certificate that does not verify against the bundle, or is not issued for client authentication, fails the TLS handshake. One that verifies but no entry names gets `401`, and the log names it so you can add it.
- A request with an `Authorization` header is judged by its token alone, even if it also presents a certificate.
- Beacon does not check revocation lists or OCSP. Issue short-lived client certificates, and remove an entry and [reload](#reloading-certificates-and-tokens) to lock one out.

To try it locally, make a CA, then a server and a client certificate signed by it. This writes everything to `certs/`, which `.gitignore` already excludes:

```bash
mkdir -p certs

# The CA
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -days 30 \
  -subj "/CN=beacon test CA" -keyout certs/ca-key.pem -out certs/ca.pem

# Beacon's server certificate
printf 'subjectAltName=DNS:localhost,IP:127.0.0.1\nextendedKeyUsage=serverAuth\n' > certs/server.ext
openssl req -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -subj "/CN=localhost" \
  -keyout certs/key.pem -out certs/server.csr
openssl x509 -req -in certs/server.csr -CA certs/ca.pem -CAkey certs/ca-key.pem -days 30 \
  -extfile certs/server.ext -out certs/cert.pem

# A client certificate for orders.internal
printf 'subjectAltName=DNS:orders.internal\nextendedKeyUsage=clientAuth\n' > certs/orders.ext
openssl req -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -subj "/CN=orders" \
  -keyout certs/orders-key.pem -out certs/orders.csr
openssl x509 -req -in certs/orders.csr -CA certs/ca.pem -CAkey certs/ca-key.pem -days 30 \
  -extfile certs/orders.ext -out certs/orders.pem

printf '[[certificates]]\nname = "orders"\ndns = "orders.internal"\n' > certs/tokens.toml

BEACON_TLS_CERT_FILE=certs/cert.pem BEACON_TLS_KEY_FILE=certs/key.pem BEACON_TLS_CLIENT_CA_FILE=certs/ca.pem \
  BEACON_AUTH_ENABLED=true BEACON_AUTH_TOKENS_FILE=certs/tokens.toml go run ./cmd/beacon

curl --cacert certs/ca.pem --cert certs/orders.pem --key certs/orders-key.pem https://localhost:8080/api/v1/services
```

On Windows, the `curl` that ships with Git and Windows uses Schannel, which rejects certificates from a private CA unless you pass `--ssl-no-revoke`, and only takes client certificates as `.p12` files. `openssl s_client -connect localhost:8080 -CAfile certs/ca.pem -cert certs/orders.pem -key certs/orders-key.pem` works with the PEM files as they are.

### Responses

A request without a token or verified client certificate, with a malformed `Authorization` header, with a token Beacon does not accept, or with a certificate no entry names gets `401` with the code `unauthorized` and a `WWW-Authenticate: Bearer realm="beacon"` header. A known caller without the needed permission gets `403` with the code `forbidden`. Unknown paths and wrong methods still answer `404` and `405`, credentials or not.

Refused requests are logged at `warn` level, with the caller's name when it is known (`caller`), or the certificate's names when no entry matched (`certificate_names`), and at `debug` level when no credentials were sent at all. Tokens themselves are never logged. Refusals are also counted in `beacon_auth_failures_total`; see [Metrics](operations.md#metrics).

## Rate limits

Beacon limits two things per client, each with a token bucket: a steady rate, plus a burst that may be spent at once. The limits live in each Beacon process; there is nothing to set up. With [several nodes](operations.md#running-several-nodes), each node enforces its share: the rate and burst divided by the number of live nodes (the burst never below 1), so behind a round-robin load balancer, which gives each node about the same share of every client's requests, the cluster as a whole enforces about the configured limit. Behind a sticky load balancer a client meets one node and gets that node's share, which is stricter. For large deployments, enforce limits at the load balancer as well.

| Limit | Counted per | Default | Beyond it |
|---|---|---|---|
| Registrations (`POST /api/v1/services`) | Caller: the token or certificate's `name` with [authentication](#authentication) on, the client network without it | 20 per second, bursts of 200 | That caller's registrations get `429 rate_limited` |
| Failed authentication: an unknown token or certificate, or a malformed `Authorization` header | Client network | 1 per second, bursts of 20 | Every request from that network gets `429 rate_limited`, valid credentials or not, until it is back under the limit |

- Refused requests carry a `Retry-After` header with the seconds to wait, are logged at `warn` level with the `limit`, and are counted in `beacon_rate_limited_total{limit}`. The [Go client](client.md) reports them as `client.ErrRateLimited`, and a `Lease` keeps retrying with backoff.
- A client network is the IPv4 address of the connection, or the `/64` its IPv6 address is in, since one host often holds a whole `/64`.
- Requests without any credentials and callers lacking a permission do not count as failures: the first are probes or misconfiguration, and the second have already proved who they are.
- The failure limit is enforced before credentials are checked, so concurrent attempts cannot get past it together: each request holds one place from its network's burst while Beacon checks its credentials, and gives it back unless they turn out invalid. The check takes microseconds, but a network sending more than `rate_limit.auth_failure_burst` requests at the very same moment, such as many clients behind one NAT under heavy load, can see some valid ones refused with `429`; raise the burst if it does.
- Heartbeats, lookups and unregistering are not rate limited.
- Behind a reverse proxy or load balancer every client has the proxy's address, so the per-network limits apply to all of them together, unless the proxy is listed in `server.trusted_proxies`. For a connection from a listed address, Beacon reads `X-Forwarded-For` from right to left and takes the first address that is not itself a listed proxy, so entries a client added in front are never believed. List only your own proxies: a range that also covers clients, such as `0.0.0.0/0`, lets them choose their address, and one could then use up the failure limit of others by naming theirs. Without the setting Beacon ignores the header. Beacon logs the ranges at startup.
- The defaults leave room for a large rollout that shares one token: a burst of 200 registrations at once, then 20 a second. `client.Announce` returns the error if its first registration is refused, so size the burst for the most instances that start together.
- Each limit tracks up to 65536 callers or networks. Callers and networks whose buckets have refilled are forgotten to make room; if every place is still in use, new ones are not limited until there is room again. The same bound applies to requests in authentication: once 65536 networks have one in progress, requests from further networks that have not failed before are checked without holding a place, though their failures still count. Networks that have failed are always held to their limit.

## Reloading certificates and tokens

Beacon reads the TLS certificate and key, the client CA bundle and the tokens file at startup, and can read them again while it runs, so you can renew a certificate or add and revoke tokens without a restart:

- **On a signal.** Send `SIGHUP` to reload at once, for example `kill -HUP <pid>`, `systemctl reload` with `ExecReload=/bin/kill -HUP $MAINPID`, or `docker kill --signal HUP <container>`. Windows has no `SIGHUP`; use the interval there.
- **On a change.** Set `server.reload_interval`, for example to `"30s"`, and Beacon checks the files that often and reloads when their contents differ. This suits files that something else rotates, such as cert-manager or a Kubernetes Secret mounted as a volume. Unchanged files are not reloaded.

```toml
[server]
reload_interval = "30s"
```

A reload replaces everything at once or nothing. If any file is missing, unreadable or invalid, for example a new certificate whose key has not been written yet, Beacon logs an error and keeps using the files it already has, so a rotation caught halfway never locks clients out. With the interval, the same broken contents are reported once, not on every check, and tried again as soon as they change.

After a successful reload, every new request is checked against the new tokens file, and every new TLS connection gets the new certificate and client CAs. Connections that are already open keep the certificate they were made with until they close. Each reload is published as one generation: a handshake and the request authenticated after it never mix the client CAs of one reload with the identities of another. A request on a connection opened before a reload is still authenticated against the identities in use now, so a revoked certificate or token stops working at once. The `credentials reloaded` log line reports the new `generation`. Only the contents of the files are reloaded: changing `tls.cert_file` or any other setting, or turning TLS or authentication on or off, still needs a restart.

Reloads are logged and counted in `beacon_credential_reloads_total`; see [Metrics](operations.md#metrics). Alert on its `result="error"` series to catch a rotation that failed.

## Exposing Beacon to a network

> [!WARNING]
> The defaults are for one machine: Beacon listens on `127.0.0.1`, without TLS or authentication. Before other machines can reach it, decide who may.

To accept connections from the network:

1. Turn on [TLS](#tls), so tokens and registrations are not sent in the clear.
2. Turn on [authentication](#authentication), so only callers with a token or certificate can register, discover or remove instances, and only an instance's owner can change it.
3. Set `server.host` to `"0.0.0.0"` (or `BEACON_SERVER_HOST=0.0.0.0`), or to the address of one interface.

Beacon starts either way, but logs a warning when it serves plain HTTP on an address other than loopback.

The [container image](operations.md#running-in-docker) already listens on `0.0.0.0`, because inside a container `127.0.0.1` is only reachable from the container itself; who can reach it is decided by the ports you publish. Publish them only on a trusted network, or with TLS and authentication on. To bind a published port to the host's loopback only, use `-p 127.0.0.1:8080:8080`.
