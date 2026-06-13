-- Detected query anti-patterns (insights), derived from extracted T1 plans.
-- One row per (server_id, fingerprint, captured_at, kind). Every column is a
-- structural identifier or an aggregate count — literal-free (T1), mirroring the
-- insight.Insight struct. Derived server-side at ingestion from query_plans
-- (which are themselves already normalized at the collector).
--
-- Range-partitioned by week on captured_at (vanilla Postgres, RDS / Aurora /
-- Cloud SQL safe — no extensions). Partitions are created at runtime in Go
-- (EnsureInsightsWeeklyPartition), same as query_plans.

CREATE TABLE insights (
    server_id     TEXT NOT NULL,
    captured_at   TIMESTAMPTZ NOT NULL,
    kind          TEXT NOT NULL,
    severity      TEXT NOT NULL,
    fingerprint   TEXT NOT NULL,
    relation      TEXT NOT NULL,
    node_path     TEXT NOT NULL,
    rows_returned BIGINT NOT NULL,
    rows_scanned  BIGINT NOT NULL,
    selectivity   DOUBLE PRECISION NOT NULL,
    detail        TEXT NOT NULL,
    data_tier     SMALLINT NOT NULL DEFAULT 1
) PARTITION BY RANGE (captured_at);

CREATE INDEX insights_brin_time ON insights USING brin (captured_at);
CREATE INDEX insights_srv_kind  ON insights (server_id, kind, captured_at);
