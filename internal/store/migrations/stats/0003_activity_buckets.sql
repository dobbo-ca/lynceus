-- Connection-state histograms from pg_stat_activity.
-- One row per (server_id, database_name, state, wait_event_type,
-- wait_event, bucket_start) — counts and labels only, NEVER query text.
-- See docs/specs/2026-05-29-lynceus-design.md §2 and the
-- ActivityBucket privacy contract test.
--
-- Range-partitioned by week on bucket_start (vanilla Postgres,
-- RDS / Aurora / Cloud SQL safe — no extensions).

CREATE TABLE activity_buckets (
    server_id        TEXT NOT NULL,
    database_name    TEXT NOT NULL,
    state            TEXT NOT NULL,
    wait_event_type  TEXT NOT NULL,
    wait_event       TEXT NOT NULL,
    bucket_start     TIMESTAMPTZ NOT NULL,
    bucket_seconds   INTEGER NOT NULL,
    sample_count     INTEGER NOT NULL,
    count_sum        BIGINT  NOT NULL,
    count_max        BIGINT  NOT NULL,
    data_tier        SMALLINT NOT NULL DEFAULT 1
) PARTITION BY RANGE (bucket_start);

CREATE INDEX activity_buckets_brin_time ON activity_buckets USING brin (bucket_start);
CREATE INDEX activity_buckets_srv_state ON activity_buckets (server_id, state, bucket_start);
