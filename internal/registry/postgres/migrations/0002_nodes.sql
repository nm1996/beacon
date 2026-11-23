-- Beacon nodes sharing this database, each recorded every few seconds.
CREATE TABLE beacon_nodes (
    id         text COLLATE "C" PRIMARY KEY,
    address    text NOT NULL,
    version    text NOT NULL,
    started_at timestamptz NOT NULL,
    last_seen  timestamptz NOT NULL
);
