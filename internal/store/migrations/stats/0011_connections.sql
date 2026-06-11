-- Per-backend connection observations + blocking edges from pg_stat_activity /
-- pg_blocking_pids(). Feeds the Connections checks (ly-u4t.22). Counts, pids,
-- and fixed state labels only — NEVER query text (T1, data_tier defaults to 1).
-- Range-partitioned by week on observed_at (vanilla Postgres, RDS-safe).

CREATE TABLE connection_samples (
    server_id        TEXT        NOT NULL,
    observed_at      TIMESTAMPTZ NOT NULL,
    pid              BIGINT      NOT NULL,
    state            TEXT        NOT NULL,
    active_seconds   BIGINT      NOT NULL,
    xact_seconds     BIGINT      NOT NULL,
    state_seconds    BIGINT      NOT NULL,
    wait_event_type  TEXT        NOT NULL,
    data_tier        SMALLINT    NOT NULL DEFAULT 1
) PARTITION BY RANGE (observed_at);

CREATE INDEX connection_samples_brin_time ON connection_samples USING brin (observed_at);
CREATE INDEX connection_samples_srv_time  ON connection_samples (server_id, observed_at);

CREATE TABLE blocking_edges (
    server_id            TEXT        NOT NULL,
    observed_at          TIMESTAMPTZ NOT NULL,
    blocked_pid          BIGINT      NOT NULL,
    blocker_pid          BIGINT      NOT NULL,
    blocked_wait_seconds BIGINT      NOT NULL,
    data_tier            SMALLINT    NOT NULL DEFAULT 1
) PARTITION BY RANGE (observed_at);

CREATE INDEX blocking_edges_brin_time ON blocking_edges USING brin (observed_at);
CREATE INDEX blocking_edges_srv_time  ON blocking_edges (server_id, observed_at);
