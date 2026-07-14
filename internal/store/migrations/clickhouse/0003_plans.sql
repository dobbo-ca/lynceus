CREATE TABLE IF NOT EXISTS query_plans (
  server_id String,
  fingerprint String,
  captured_at DateTime64(3, 'UTC'),
  format_version Int32,
  total_cost Float64,
  actual_total_time_ms Float64,
  plan_tree String,
  data_tier Int16 DEFAULT 1
) ENGINE = MergeTree
PARTITION BY toYYYYMM(captured_at)
ORDER BY (server_id, fingerprint, captured_at)
TTL toDateTime(captured_at) + INTERVAL 90 DAY;
