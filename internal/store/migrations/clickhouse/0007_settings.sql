-- settings: curated pg_settings tuning GUCs (numeric/bool/enum values only —
-- the collector ships a fixed allowlist by name, never SELECT *). Mirrors the
-- vanilla-Postgres settings table (0014_settings.sql). value is a bounded
-- config string, redacted at the collector allowlist boundary. T1 (data_tier
-- defaults to 1). pending_restart is a bool stored as UInt8. ORDER BY
-- (server_id, name, collected_at) serves the latest-as-of-per-name read.
CREATE TABLE IF NOT EXISTS settings (
  server_id String,
  collected_at DateTime64(3, 'UTC'),
  name String,
  value String,
  unit String,
  source String,
  pending_restart UInt8 DEFAULT 0,
  data_tier Int16 DEFAULT 1
) ENGINE = MergeTree
PARTITION BY toYYYYMM(collected_at)
ORDER BY (server_id, name, collected_at)
TTL toDateTime(collected_at) + INTERVAL 90 DAY;
