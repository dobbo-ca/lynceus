-- Time-series stats schema.
-- Vanilla PostgreSQL with native declarative range partitioning by week.
-- No extensions — runs on RDS / Aurora / Cloud SQL.

CREATE TABLE query_stats (
    server_id         TEXT NOT NULL,
    collected_at      TIMESTAMPTZ NOT NULL,
    fingerprint       TEXT NOT NULL,
    normalized_query  TEXT NOT NULL,
    data_tier         SMALLINT NOT NULL DEFAULT 1,
    calls             BIGINT,
    total_time_ms     DOUBLE PRECISION,
    mean_time_ms      DOUBLE PRECISION,
    rows              BIGINT,
    shared_blks_hit   BIGINT,
    shared_blks_read  BIGINT
) PARTITION BY RANGE (collected_at);

CREATE INDEX query_stats_brin_time ON query_stats USING brin (collected_at);
CREATE INDEX query_stats_srv_fp    ON query_stats (server_id, fingerprint);
