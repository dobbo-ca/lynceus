-- internal/store/migrations/config/0004_discovered_capability.sql
--
-- ly-xnk.4 — Discovered capability inventory.
--
-- Persists the result of caps.Discover for each (server, database,
-- capability): whether the capability is available on the monitored
-- Postgres and a short, package-authored reason. The capability matrix
-- API (GET /api/servers/{id}/capabilities) joins this "discovered" axis
-- against capability_policy (operator intent) to compute final-enabled.
--
--   database_name IS NULL  -> the server-level / connection-database probe
--   database_name = '<db>'  -> a per-database probe result
--
-- reason is always a bounded, package-authored string (caps.Status.Reason,
-- caps.go:54-64) — never a literal from the monitored database.
--
-- Vanilla PostgreSQL — no extensions (must run on RDS / Aurora).
-- NULLS NOT DISTINCT (core PostgreSQL 15+) makes the unique index treat
-- NULL database_name rows as equal, so each (server, capability) holds at
-- most one server-level discovered row.

CREATE TABLE discovered_capability (
    server_id     TEXT        NOT NULL REFERENCES servers (id) ON DELETE CASCADE,
    database_name TEXT,
    capability    TEXT        NOT NULL,
    available     BOOLEAN     NOT NULL,
    reason        TEXT        NOT NULL DEFAULT '',
    observed_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT discovered_capability_uniq
        UNIQUE NULLS NOT DISTINCT (server_id, database_name, capability)
);

-- The matrix GET reads all rows for one server at a time.
CREATE INDEX discovered_capability_server_idx
    ON discovered_capability (server_id);
