# HTTP API

Every endpoint of Beacon's HTTP API, with its requests, responses and errors.

All service endpoints live under `/api/v1`; `/health` is not versioned. Response bodies are JSON, including errors.

| Method | Path | Purpose |
|---|---|---|
| `POST` | [`/api/v1/services`](#post-apiv1services) | Register a new instance |
| `GET` | [`/api/v1/services`](#get-apiv1services) | List instances, optionally by name |
| `POST` | [`/api/v1/services/{id}/heartbeat`](#post-apiv1servicesidheartbeat) | Keep an instance alive |
| `DELETE` | [`/api/v1/services/{id}`](#delete-apiv1servicesid) | Remove an instance |
| `GET` | [`/health`](#get-health) | Check that Beacon is running |

Unknown paths return `404`, and a known path called with the wrong method returns `405`. Only registration reads a request body. It must be a single JSON object of at most 64 KiB (configurable) containing only the fields documented below. Anything else (unknown fields, a second JSON value, trailing text, an oversized body) is rejected with `400` and the code `invalid_request_body`.

## Errors

Every error response has the same shape:

```json
{ "error": { "code": "service_not_found", "message": "No service is registered with this ID" } }
```

`code` is stable and meant for programs; `message` is meant for people and may change.

| Status | Code | When |
|---|---|---|
| `400` | `invalid_request_body` | The body is not a single JSON object within the size limit with only the documented fields |
| `400` | `invalid_service` | A registration field breaks a rule, for example a port outside `1`–`65535` |
| `400` | `invalid_query` | A query parameter is invalid, such as `limit=0` or a `cursor` Beacon did not issue |
| `403` | `instance_not_owned` | [Authentication](security.md#authentication) is on and another caller registered this instance; see [Instance ownership](security.md#instance-ownership) |
| `429` | `quota_exceeded` | Beacon already holds `registry.max_instances` instances, or the caller already holds `registry.max_instances_per_owner`; retry after some unregister or expire |
| `429` | `rate_limited` | Too many registrations from this caller, or too many failed authentication attempts from this network; retry after the `Retry-After` header. See [Rate limits](security.md#rate-limits) |
| `409` | `instance_exists` | A registration names the `id` of an instance that another caller registered, or that has a different name, address or port |
| `422` | `idempotency_key_reused` | A registration repeats an `Idempotency-Key` with a different name, address or port |
| `404` | `service_not_found` | No instance is registered with the given ID |
| `404` | `not_found` | No endpoint exists at the path |
| `401` | `unauthorized` | [Authentication](security.md#authentication) is on and the request has no bearer token, a malformed `Authorization` header or a token Beacon does not accept |
| `403` | `forbidden` | The token is valid but lacks the permission this endpoint needs |
| `405` | `method_not_allowed` | The endpoint exists but not for this method; the `Allow` header lists the supported one |
| `500` | `internal_error` | Something unexpected went wrong on the server |

## `POST /api/v1/services`

```json
{ "name": "orders", "address": "10.0.0.12", "port": 8081 }
```

| Field | Rule |
|---|---|
| `id` | Optional. The instance ID to use instead of a generated one: 1–128 letters, digits, `.`, `_` or `-`, starting with a letter or digit, such as a pod name. Registering the same `id` again keeps it; see below |
| `name` | Required. 1–63 (`registry.max_name_length`) lowercase letters, digits, `.`, `_` or `-`, starting and ending with a letter or digit, with no two of `.`, `_` and `-` in a row, such as `orders` or `payment-service.v2`. Uppercase, spaces and names like `orders..v2` are rejected, not changed |
| `address` | Required. An IPv4 or IPv6 address, or a host name such as `orders.internal`, up to 253 characters (`registry.max_address_length`). No scheme, port or path |
| `port` | Required, between `1` and `65535` |

| Status | Meaning |
|---|---|
| `201 Created` | Registered. The `Location` header points at the new instance |
| `200 OK` | A repeat of an earlier registration, by `id` or `Idempotency-Key`: the same instance, refreshed as by a heartbeat, with the same body |
| `400 Bad Request` | The body is invalid, a field breaks a rule above, or both `id` and `Idempotency-Key` are given |
| `409 Conflict` | `instance_exists`: the `id` belongs to another caller's instance, or to one with a different name, address or port |
| `422 Unprocessable Content` | `idempotency_key_reused`: the key was used for a registration with a different name, address or port |
| `429 Too Many Requests` | `quota_exceeded`: Beacon is full, or this caller holds its maximum number of instances. `rate_limited`: this caller registered too often; see [Rate limits](security.md#rate-limits) |

```json
{ "id": "8c0f3c56-5d7e-4a63-9a39-2b7c1e5e8f40", "ttl_seconds": 30 }
```

`ttl_seconds` is how long the instance may go without a heartbeat before Beacon removes it. Sending a heartbeat every third of that leaves room for a missed one.

Registering is not otherwise safe to repeat: if a response is lost, a retry creates a second instance. Two ways make it safe:

- **`id`**: an instance that names its own ID, such as its pod or host name, gets the same instance back when it registers again with the same name, address and port, whether after a lost response or after Beacon restarted. Moving to another address means unregistering first; Beacon never hands an ID to another caller or another address.
- **`Idempotency-Key` header**: any unique string up to 255 printable ASCII characters, sent again on every retry of one registration. Beacon derives the instance ID from the key and the caller, so a retry finds the instance the first attempt created. A key lives as long as its instance: once that is unregistered or expired, the same key creates a new one. The [Go client](client.md) sends one with every registration.

## `GET /api/v1/services`

| Query parameter | Meaning |
|---|---|
| `name` | Optional. Only return instances with this name |
| `limit` | Optional. Return at most this many instances, from `1` to `1000`, and link to the next page |
| `cursor` | Only with `limit`. Where the next page starts; take it from the `Link` header, never build it |

Returns `200 OK` with a JSON array sorted by ID, which is empty when nothing matches. Instances whose last heartbeat is more than `registry.service_ttl` ago are left out, even before cleanup removes them:

```json
[
  { "id": "8c0f3c56-...", "name": "orders", "address": "10.0.0.12", "port": 8081, "last_heartbeat": "2026-11-10T16:00:00Z" },
  { "id": "1b9e47a2-...", "name": "orders", "address": "10.0.0.13", "port": 8081, "last_heartbeat": "2026-11-10T16:00:04Z" }
]
```

`last_heartbeat` is in UTC, in RFC 3339 format.

Without `limit`, every matching instance comes back in one response, as it always has; `registry.max_instances` bounds how large that can get. With `limit`, the response holds at most that many and, when more follow, a `Link` header points at the next page:

```http
Link: </api/v1/services?cursor=Y2FmZS0xMjM&limit=100&name=orders>; rel="next"
```

Request that URL to get the next page, until a response has no `Link` header. The response body stays a plain array either way, so clients that ignore paging keep working. Pages are taken in ID order, so an instance registered or removed while you page may or may not show up, but none is returned twice.

| Status | Meaning |
|---|---|
| `200 OK` | The instances, possibly with a `Link` header |
| `304 Not Modified` | The `If-None-Match` header lists the current `ETag`: the instances are the same as before. No body |
| `400 Bad Request` | `limit` or `cursor` is invalid; code `invalid_query` |

Every response carries a weak `ETag`, such as `W/"5f2c..."`, over the instances' IDs, names, addresses and ports, and the next page link. It ignores `last_heartbeat`, which changes with every heartbeat without changing where to connect. A client that polls sends it back in `If-None-Match` and gets `304` with no body until an instance is added, removed or moved.

## `POST /api/v1/services/{id}/heartbeat`

No request body.

| Status | Meaning |
|---|---|
| `204 No Content` | Heartbeat recorded |
| `403 Forbidden` | With authentication on: another caller registered this instance (`instance_not_owned`), or the caller lacks `register` (`forbidden`) |
| `404 Not Found` | No instance with that ID, possibly because it already expired |

An instance that gets `404` should register again.

## `DELETE /api/v1/services/{id}`

No request body.

| Status | Meaning |
|---|---|
| `204 No Content` | Instance removed |
| `403 Forbidden` | With authentication on: another caller registered this instance (`instance_not_owned`), or the caller lacks `register` (`forbidden`) |
| `404 Not Found` | No instance with that ID |

## `GET /api/v1/nodes`

Lists the live Beacon nodes sharing this node's storage, sorted by ID; with the memory backend, only this one. With [authentication](security.md#authentication) on it needs the `manage` permission.

```json
[{ "id": "beacon-0", "address": "0.0.0.0:8080", "version": "v0.3.0", "started_at": "2026-11-23T09:00:00Z", "last_seen": "2026-11-23T10:00:05Z" }]
```

## `GET /health`

Always returns `200 OK` while the process is serving requests, with the version of the running build. Use it for container and load balancer health checks.

```json
{ "status": "ok", "version": "v0.1.0" }
```
