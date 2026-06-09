-- Classified Postgres log events, normalized at the collector.
-- One row per parsed log line. Every column is either a fixed-vocabulary
-- string (event_type, severity, sql_state), a Postgres catalog identifier
-- (database_name, user_name), a hashed client IP, or a coarse numeric
-- counter — NEVER the statement text, bind params, error detail, or the raw
-- message itself (see the LogEvent privacy contract test). The literal-bearing
-- source line stays in the collector-local T2 LogPayload.
--
-- Range-partitioned by week on occurred_at (vanilla Postgres,
-- RDS / Aurora / Cloud SQL safe — no extensions). Mirrors query_plans:
-- the M3 log insights read the hot label columns over a time window.

CREATE TABLE log_events (
    server_id        TEXT NOT NULL,
    event_type       TEXT NOT NULL,
    severity         TEXT NOT NULL,
    occurred_at      TIMESTAMPTZ NOT NULL,
    logged_at        TIMESTAMPTZ NOT NULL,
    pid              BIGINT NOT NULL,
    backend_type     TEXT NOT NULL,
    database_name    TEXT NOT NULL,
    user_name        TEXT NOT NULL,
    application_name TEXT NOT NULL,
    client_addr_hash TEXT NOT NULL,
    sql_state        TEXT NOT NULL,
    session_line_num BIGINT NOT NULL,
    transaction_id   BIGINT NOT NULL,
    data_tier        SMALLINT NOT NULL DEFAULT 1
) PARTITION BY RANGE (occurred_at);

CREATE INDEX log_events_brin_time ON log_events USING brin (occurred_at);
CREATE INDEX log_events_srv_type ON log_events (server_id, event_type, occurred_at);
