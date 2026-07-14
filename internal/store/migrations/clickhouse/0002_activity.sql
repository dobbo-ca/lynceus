CREATE TABLE IF NOT EXISTS activity_buckets (
  server_id String,
  database_name String,
  state String,
  wait_event_type String,
  wait_event String,
  bucket_start DateTime64(3, 'UTC'),
  bucket_seconds Int32,
  sample_count Int32,
  count_sum Int64,
  count_max Int64,
  data_tier Int16 DEFAULT 1
) ENGINE = MergeTree
PARTITION BY toYYYYMM(bucket_start)
ORDER BY (server_id, bucket_start, state)
TTL toDateTime(bucket_start) + INTERVAL 90 DAY;
