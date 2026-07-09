-- settings: curated pg_settings tuning GUCs (numeric/bool/enum values only —
-- the collector ships a fixed allowlist by name, never SELECT *). Feeds the
-- Settings checks (ly-u4t.24) + Config advisor (ly-u4t.18). Vanilla Postgres,
-- RDS-safe (no extensions). value is a bounded config string; the redaction
-- boundary is the collector allowlist. T1 (data_tier defaults to 1).
CREATE TABLE settings (
    server_id       TEXT        NOT NULL,
    collected_at    TIMESTAMPTZ NOT NULL,
    name            TEXT        NOT NULL,
    value           TEXT        NOT NULL,
    unit            TEXT        NOT NULL,
    source          TEXT        NOT NULL,
    pending_restart BOOLEAN     NOT NULL DEFAULT false,
    data_tier       SMALLINT    NOT NULL DEFAULT 1
) PARTITION BY RANGE (collected_at);

CREATE INDEX settings_brin_time ON settings USING brin (collected_at);
CREATE INDEX settings_srv_name  ON settings (server_id, name, collected_at);
