-- Instances, one row each. IDs compare byte by byte (COLLATE "C"), as they
-- do in Go, so pages in ID order match the memory backend's.
CREATE TABLE beacon_instances (
    id             text COLLATE "C" PRIMARY KEY,
    name           text NOT NULL,
    address        text NOT NULL,
    port           integer NOT NULL,
    owner          text NOT NULL DEFAULT '',
    registered_at  timestamptz NOT NULL,
    last_heartbeat timestamptz NOT NULL
);

-- Lookups by name, paged by ID.
CREATE INDEX beacon_instances_name_id ON beacon_instances (name, id);

-- Cleanup and the expiry filter of lookups.
CREATE INDEX beacon_instances_last_heartbeat ON beacon_instances (last_heartbeat);

-- The per-owner count of Create.
CREATE INDEX beacon_instances_owner ON beacon_instances (owner) WHERE owner <> '';
