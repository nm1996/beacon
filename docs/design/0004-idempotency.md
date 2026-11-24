# 0004. Idempotent registration with `Idempotency-Key`

Status: Proposed

## Context

`POST /api/v1/services` is the only operation that is not safe to repeat. If the response is lost, the client cannot know whether the instance exists, and retrying creates a second instance. The Go client therefore never retries `Register`, and so a single dropped packet fails `Announce`. Heartbeats are idempotent by nature. Unregistering is too, since the client treats `404` as done.

## Decision

Beacon accepts the `Idempotency-Key` request header on registration, as specified in the IETF draft `draft-ietf-httpapi-idempotency-key-header` and as used by Stripe and others.

- **The key becomes the instance ID.** The ID is a UUIDv5 of `(owner, key)` in a Beacon namespace, so repeating the request lands on the ID decision of 0003: the first request creates the instance, and a repeat with the same body answers `200` with the same instance.
- **Same key, different body:** `422 Unprocessable Content`, code `idempotency_key_reused`, as the draft prescribes. A body with its own `id` and a key together is `400`, since each would decide the ID.
- **The key lives as long as the instance.** After the instance expires or is unregistered, the same key creates it again. That is the correct outcome, since the earlier instance is gone.
- **Key format:** 1–255 printable ASCII characters. Owners are mixed into the hash, so two callers using the same key cannot collide.
- **Client:** `Register` sends a random key and retries up to three times, with backoff, on connection errors and on `502`, `503` and `504`. `Announce` therefore survives a lost response. `ErrInvalid` and other definite answers are never retried.

## Alternatives considered

- **A key store**, mapping keys to stored responses with a retention period, as payment APIs do. That is a new table, a new expiry job and a new consistency problem across nodes, all to remember something the instance already embodies. A store is needed when an operation has no natural identity. Registration has one: the instance ID.
- **Making the client generate IDs (0003) and stopping there.** That covers our client. The header gives every HTTP client, such as curl in a script or another language's SDK, the same safety without having to invent IDs.

## Consequences

- `Register` becomes safely retryable, so the Go client can retry it.
- No storage, no cleanup job, and the same behavior on one node or many.
- A key cannot be replayed to fetch someone else's instance, since the owner is part of the derived ID.

## Open questions

- Should a key be refused once the instance it made was unregistered? Default: no. Re-creating is what a retry after a clean shutdown should do.
