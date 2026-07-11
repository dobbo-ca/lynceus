-- internal/store/migrations/config/0006_saved_scripts.sql
--
-- ly-ae6.9 — Saved Scripts (global / team / personal).
--
-- User-authored SQL scripts saved for reuse. sql_text is *user-authored
-- org metadata* (like a saved report), NOT monitored-database data, so it
-- carries no T1/T2 stats-store classification. Visibility follows scope:
--   GLOBAL   -> everyone in the org
--   TEAM     -> members of owner_group (e.g. dba-oncall)
--   PERSONAL -> the owner only
-- Only the owner (or an admin) may change scope or delete; every such
-- change is recorded through the Go writer as a tamper-evident audit entry
-- (ly-8b0.3). audit_chain_id binds a live row to the audit entry for its
-- LAST scope change (mirrors capability_policy.audit_chain_id). A delete
-- leaves a standalone audit entry — the row is gone, so there is nothing
-- left to bind, which is why DeleteScript records the entry then removes
-- the row and does not carry the id anywhere.
--
-- Vanilla PostgreSQL — no extensions (must run on RDS / Aurora).

CREATE TABLE saved_scripts (
    id             BIGSERIAL   PRIMARY KEY,
    name           TEXT        NOT NULL,
    description    TEXT        NOT NULL DEFAULT '',
    sql_text       TEXT        NOT NULL,
    scope          TEXT        NOT NULL CHECK (scope IN ('GLOBAL','TEAM','PERSONAL')),
    owner          TEXT        NOT NULL,
    owner_group    TEXT        NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    audit_chain_id BIGINT      REFERENCES audit_log (id)
);

-- Visibility resolution scans by scope; ownership checks scan by owner.
CREATE INDEX saved_scripts_scope_idx ON saved_scripts (scope);
CREATE INDEX saved_scripts_owner_idx ON saved_scripts (owner);
