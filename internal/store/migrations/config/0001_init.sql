-- Config / metadata schema.
-- Vanilla PostgreSQL only — no extensions (must run on RDS / Aurora).

CREATE TABLE servers (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    t2_enabled  BOOLEAN NOT NULL DEFAULT false,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE audit_log (
    id         BIGSERIAL PRIMARY KEY,
    actor      TEXT NOT NULL,
    action     TEXT NOT NULL,
    server_id  TEXT,
    data_tier  SMALLINT,
    detail     JSONB,
    at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX audit_log_at_brin ON audit_log USING brin (at);
