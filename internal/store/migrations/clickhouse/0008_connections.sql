CREATE TABLE IF NOT EXISTS connection_samples (
  server_id String,
  observed_at DateTime64(3, 'UTC'),
  pid Int64,
  state String,
  active_seconds Int64,
  xact_seconds Int64,
  state_seconds Int64,
  wait_event_type String,
  data_tier Int16 DEFAULT 1
) ENGINE = MergeTree
PARTITION BY toYYYYMM(observed_at)
ORDER BY (server_id, observed_at, pid)
TTL toDateTime(observed_at) + INTERVAL 90 DAY;

CREATE TABLE IF NOT EXISTS blocking_edges (
  server_id String,
  observed_at DateTime64(3, 'UTC'),
  blocked_pid Int64,
  blocker_pid Int64,
  blocked_wait_seconds Int64,
  data_tier Int16 DEFAULT 1
) ENGINE = MergeTree
PARTITION BY toYYYYMM(observed_at)
ORDER BY (server_id, observed_at, blocked_pid, blocker_pid)
TTL toDateTime(observed_at) + INTERVAL 90 DAY;
