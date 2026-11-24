# 0002. A stable owner ID, separate from the display name

Status: Proposed

## Context

With authentication on, an instance's owner is the `name` of the token or certificate entry that registered it, and `registry.max_instances_per_owner` counts per name. The name is also what logs show, so it is the thing people want to edit. Renaming an entry silently changes the owner: existing instances become someone else's, their heartbeats get `403`, and the per-owner count restarts. Two entries with the same name, for example a token and a certificate for one service, already share ownership on purpose, but this is implicit.

## Decision

Entries in the tokens file gain an optional `id`:

```toml
[[tokens]]
id     = "svc-orders"        # stable; ownership and quotas use it
name   = "orders (prod)"     # free text for logs; may change
sha256 = "..."
```

- `auth.Identity` gains `ID`. Without an `id`, the ID is the name, so every existing tokens file keeps its meaning.
- `discovery.Caller` gains `ID`, and `Instance.Owner` stores it. The owner check and the per-owner count use the ID.
- `name` becomes a label, and the format rules for it relax: free text up to 128 characters. `id` follows the service-name rules (lowercase letters, digits, `.`, `_`, `-`, at most 63 characters), because it is stored and compared.
- Entries may share an `id` on purpose, such as a token and a certificate for the same service. They then share ownership and quota. A reload that changes an entry's `id` changes its owner; that is now an explicit act.
- Logs show both: `caller` is the name, `caller_id` is the ID.

## Alternatives considered

- **Hash the token as the owner.** That would be stable until the token is rotated, which is exactly when ownership must survive. Rotation is more common than renaming.
- **Generate IDs and store them in Beacon.** That needs persistent state for something the operator already controls in a file.
- **Use the certificate's subject (SPIFFE ID) as the ID.** That works for certificates and is a good value to put in `id`, but tokens have no such identity. One field for both keeps a single rule.

## Consequences

- Renaming is safe. Rotating a token is safe if the new entry keeps the `id`.
- One more field, which is optional. Old files behave exactly as before.
- The owner stored with instances in PostgreSQL (0006) is the ID, so it is stable across nodes and restarts.

## Open questions

- Should Beacon refuse two entries with the same `id` but different permissions? Default: allowed. Permissions belong to the credential, ownership to the ID.
