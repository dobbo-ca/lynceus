CREATE TABLE IF NOT EXISTS log_events (
  -- Classified Postgres log events, normalized at the collector. Every column
  -- is classification metadata only (fixed-vocabulary strings, catalog idents,
  -- a hashed client IP, coarse counters) — NEVER statement text, bind params,
  -- error detail, or the raw message (see the LogEvent privacy contract test).
  server_id String,
  event_type String,
  severity String,
  occurred_at DateTime64(3, 'UTC'),
  logged_at DateTime64(3, 'UTC'),
  pid Int64,
  backend_type String,
  database_name String,
  user_name String,
  application_name String,
  client_addr_hash String,
  sql_state String,
  session_line_num Int64,
  transaction_id Int64,
  data_tier Int16 DEFAULT 1
) ENGINE = MergeTree
PARTITION BY toYYYYMM(occurred_at)
ORDER BY (server_id, event_type, occurred_at)
TTL toDateTime(occurred_at) + INTERVAL 90 DAY;
