# 0003. Instances keep their ID when they register again

Status: Proposed

## Context

Beacon issues a new random ID on every registration. A `Lease` that has to register again, after Beacon restarted or the instance expired, gets a different ID for the same process. Anything keyed by instance ID then sees one instance vanish and another appear: client caches (0005), dashboards, logs that follow an ID, and, with several nodes, two IDs for one process during a failover.

Consul and Eureka let the client name its instance for this reason. The ID belongs to the process, not to the registration.

## Decision

`POST /api/v1/services` accepts an optional `id`:

```json
{ "id": "orders-7f3c9a", "name": "orders", "address": "10.0.0.12", "port": 8081 }
```

- **Format:** 1–128 characters of letters, digits, `.`, `_`, `-`, starting with a letter or digit. UUIDs, host names and pod names all fit. Without `id`, Beacon generates a UUID as today.
- **Free ID:** it is created with that ID and owned by the caller. The response is `201 Created`.
- **ID already in use by the same owner, with the same name, address and port:** the registration counts as a heartbeat, and the response is `200 OK` with the same body. This makes registering again a safe retry.
- **ID in use by another owner,** or with a different name, address or port: `409 Conflict`, code `instance_exists`, and nothing changes. Moving an instance means unregistering it first. Never silently taking over an ID keeps ownership meaningful.
- **Without authentication,** owners are empty. Registering an existing ID again succeeds only if the contents match.
- **Store:** `Registry.Create` already fails on a duplicate ID. The service adds an "existing instance matches" path, `Registry.Get`, then `Touch` with the owner check, so no backend has to learn a new operation. If a race leads `Create` to fail on a duplicate, the service retries the match path once.
- **Client:** `client.Registration` gains `ID`. A `Lease` registers again with its current ID, so its ID survives Beacon restarts. `Announce` without an ID still gets one from Beacon and keeps it afterwards.

## Alternatives considered

- **`PUT /api/v1/services/{id}` as an upsert.** It is RESTful, but a second write endpoint doubles the surface, and an upsert that also updates the address invites one process silently moving another's instance. A conditional create on the existing endpoint is smaller and safer.
- **Server-generated ID with a client "previous ID" hint.** This is the same mechanism with a worse name. It also suggests that the server may refuse the hint, which it would not.
- **Deterministic IDs derived from name, address and port.** Two processes on one address would collide, and a restarted process on a new port would get a new ID anyway.

## Consequences

- IDs are stable across Beacon restarts and re-registrations. Caches and logs keyed by ID stay valid.
- A client can choose a guessable ID. That is harmless with authentication, since others cannot change it. Without authentication, anyone can already change any instance.
- One more read per registration that names an existing ID. Registrations without `id` cost the same as before.

## Open questions

- Should a changed address or port with the same ID update the instance instead of answering `409`? Default: no. Rescheduled pods get new IDs from their pod names anyway.
