CREATE TABLE IF NOT EXISTS freeze_ages (
  server_id String,
  collected_at DateTime64(3, 'UTC'),
  scope String,
  schema_name String,
  object_name String,
  fqn String,
  xid_age Int64,
  mxid_age Int64,
  autovacuum_freeze_max_age Int64,
  data_tier Int16 DEFAULT 1
) ENGINE = MergeTree
PARTITION BY toYYYYMM(collected_at)
ORDER BY (server_id, fqn, collected_at)
TTL toDateTime(collected_at) + INTERVAL 90 DAY;

CREATE TABLE IF NOT EXISTS xmin_horizon (
  server_id String,
  collected_at DateTime64(3, 'UTC'),
  oldest_xmin_age Int64,
  holder_kind String,
  data_tier Int16 DEFAULT 1
) ENGINE = MergeTree
PARTITION BY toYYYYMM(collected_at)
ORDER BY (server_id, collected_at)
TTL toDateTime(collected_at) + INTERVAL 90 DAY;
