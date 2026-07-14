-- Detected query anti-patterns (insights), derived server-side at ingestion
-- from extracted T1 plans. One row per (server_id, fingerprint, captured_at,
-- kind). Every column is a structural identifier or an aggregate count —
-- literal-free (T1), mirroring the insight.Insight struct. No T2 companion
-- table: the insights read path (count/top) is always data_tier = 1.
--
-- MergeTree analogue of the vanilla-Postgres range-partitioned insights table
-- (migrations/stats/0005_insights.sql). ORDER BY mirrors the PG secondary index
-- on (server_id, kind, captured_at). Reads filter server_id IN (...) over a
-- captured_at range and order by captured_at DESC.
CREATE TABLE IF NOT EXISTS insights (
  server_id String,
  captured_at DateTime64(3, 'UTC'),
  kind String,
  severity String,
  fingerprint String,
  relation String,
  node_path String,
  rows_returned Int64,
  rows_scanned Int64,
  selectivity Float64,
  detail String,
  data_tier Int16 DEFAULT 1
) ENGINE = MergeTree
PARTITION BY toYYYYMM(captured_at)
ORDER BY (server_id, kind, captured_at)
TTL toDateTime(captured_at) + INTERVAL 90 DAY;
