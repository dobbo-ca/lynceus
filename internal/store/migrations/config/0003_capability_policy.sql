-- internal/store/migrations/config/0003_capability_policy.sql
--
-- ly-xnk.2 — Per-server / per-database capability policy.
--
-- Operators enable or disable each Lynceus capability for a server, or
-- override the server-wide default for one database within that server.
--   database_name IS NULL  -> server-wide default
--   database_name = '<db>'  -> override for that database only
--
-- Every toggle is written through the Go writer, which records a
-- tamper-evident audit entry (ly-8b0.3) and stores its id in
-- audit_chain_id.
--
-- Vanilla PostgreSQL — no extensions (must run on RDS / Aurora).
-- NULLS NOT DISTINCT is core PostgreSQL 15+; it makes the unique index
-- treat NULL database_name rows as equal, so a server can hold at most
-- one server-wide default per capability.

CREATE TABLE capability_policy (
    server_id      TEXT        NOT NULL REFERENCES servers (id) ON DELETE CASCADE,
    database_name  TEXT,
    capability     TEXT        NOT NULL,
    enabled        BOOLEAN     NOT NULL,
    set_by         TEXT        NOT NULL,
    set_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    reason         TEXT        NOT NULL DEFAULT '',
    audit_chain_id BIGINT      REFERENCES audit_log (id),
    CONSTRAINT capability_policy_uniq
        UNIQUE NULLS NOT DISTINCT (server_id, database_name, capability)
);

-- Effective-policy resolution reads all rows for one server at a time
-- (DB override falls back to the server-wide default), so index by server.
CREATE INDEX capability_policy_server_idx
    ON capability_policy (server_id, capability);
