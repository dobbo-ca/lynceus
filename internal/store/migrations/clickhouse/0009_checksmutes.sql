-- checks_results: ClickHouse port of the Postgres checks_results store. One row
-- per firing check observation per evaluation tick. All columns are
-- identifiers / fixed enums / counts / bounded package-authored strings — T1
-- (data_tier defaults to 1). muted is a UInt8 (0/1) boolean.
CREATE TABLE IF NOT EXISTS checks_results (
  server_id String,
  evaluated_at DateTime64(3, 'UTC'),
  check_id String,
  category String,
  severity String,
  status String,
  object String,
  detail String,
  muted UInt8 DEFAULT 0,
  data_tier Int16 DEFAULT 1
) ENGINE = MergeTree
PARTITION BY toYYYYMM(evaluated_at)
ORDER BY (server_id, check_id, object, evaluated_at)
TTL toDateTime(evaluated_at) + INTERVAL 90 DAY;

-- check_mutes: operator-set suppression of a (server_id, check_id[, object]).
-- object='' applies to every object of that check on that server.
--
-- ClickHouse has no UPDATE/DELETE, so mutations are modeled as append-only
-- versions collapsed by a ReplacingMergeTree keyed on (server_id, check_id,
-- object) with updated_at as the version column. `deleted` is a tombstone:
-- SetMute appends deleted=0, ClearMute appends deleted=1. Readers take the
-- latest version per key via argMax(updated_at) (correct even before background
-- merges physically collapse older versions, and across partitions).
-- updated_at is DateTime64(9) so two mutations of the same key in quick
-- succession still order deterministically.
--
-- No TTL: unlike the partitioned, time-series checks_results, the Postgres
-- check_mutes is a small permanent operational table with no retention — a mute
-- set far into the future must not be silently dropped.
CREATE TABLE IF NOT EXISTS check_mutes (
  server_id String,
  check_id String,
  object String,
  muted_until DateTime64(3, 'UTC'),
  reason String,
  deleted UInt8 DEFAULT 0,
  updated_at DateTime64(9, 'UTC')
) ENGINE = ReplacingMergeTree(updated_at)
PARTITION BY toYYYYMM(updated_at)
ORDER BY (server_id, check_id, object);
