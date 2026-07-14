CREATE TABLE IF NOT EXISTS query_stats (
  server_id String,
  collected_at DateTime64(3, 'UTC'),
  fingerprint String,
  normalized_query String,
  data_tier Int16 DEFAULT 1,
  calls Int64,
  total_time_ms Float64,
  mean_time_ms Float64,
  `rows` Int64,
  shared_blks_hit Int64,
  shared_blks_read Int64
) ENGINE = MergeTree
PARTITION BY toYYYYMM(collected_at)
ORDER BY (server_id, fingerprint, collected_at)
TTL toDateTime(collected_at) + INTERVAL 90 DAY;

CREATE TABLE IF NOT EXISTS query_stats_t2 (
  server_id String,
  collected_at DateTime64(3, 'UTC'),
  fingerprint String,
  normalized_query String,
  data_tier Int16 DEFAULT 2,
  calls Int64,
  total_time_ms Float64,
  mean_time_ms Float64,
  `rows` Int64,
  shared_blks_hit Int64,
  shared_blks_read Int64
) ENGINE = MergeTree
PARTITION BY toYYYYMM(collected_at)
ORDER BY (server_id, collected_at)
TTL toDateTime(collected_at) + INTERVAL 7 DAY;
