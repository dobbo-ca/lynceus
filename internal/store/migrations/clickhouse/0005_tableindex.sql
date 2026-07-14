CREATE TABLE IF NOT EXISTS table_stats (
  server_id String,
  collected_at DateTime64(3, 'UTC'),
  schema_name String,
  object_name String,
  fqn String,
  total_bytes Int64,
  heap_bytes Int64,
  toast_bytes Int64,
  indexes_bytes Int64,
  row_estimate Int64,
  live_tuples Int64,
  dead_tuples Int64,
  n_mod_since_analyze Int64,
  seq_scan Int64,
  idx_scan Int64,
  n_tup_ins Int64,
  n_tup_upd Int64,
  n_tup_del Int64,
  n_tup_hot_upd Int64,
  last_vacuum Nullable(DateTime64(3, 'UTC')),
  last_autovacuum Nullable(DateTime64(3, 'UTC')),
  last_analyze Nullable(DateTime64(3, 'UTC')),
  last_autoanalyze Nullable(DateTime64(3, 'UTC')),
  vacuum_count Int64,
  autovacuum_count Int64,
  data_tier Int16 DEFAULT 1
) ENGINE = MergeTree
PARTITION BY toYYYYMM(collected_at)
ORDER BY (server_id, fqn, collected_at)
TTL toDateTime(collected_at) + INTERVAL 90 DAY;

CREATE TABLE IF NOT EXISTS index_stats (
  server_id String,
  collected_at DateTime64(3, 'UTC'),
  schema_name String,
  object_name String,
  fqn String,
  table_fqn String,
  idx_scan Int64,
  size_bytes Int64,
  is_valid UInt8,
  is_ready UInt8,
  is_unique UInt8,
  is_primary UInt8,
  data_tier Int16 DEFAULT 1
) ENGINE = MergeTree
PARTITION BY toYYYYMM(collected_at)
ORDER BY (server_id, fqn, collected_at)
TTL toDateTime(collected_at) + INTERVAL 90 DAY;
