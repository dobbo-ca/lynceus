-- Object inventory, current-state with a stable first_seen_at (load-bearing for
-- the future Index Advisor / schema-change insights). AggregatingMergeTree
-- collapses to one row per (server_id, kind, fqn) — bounding volume — while
-- min(first_seen_at)/max(last_seen_at) preserve the earliest first-seen and
-- latest last-seen without a read-before-write. anyLast holds the latest scalar
-- values. The writer appends raw values (SimpleAggregateFunction accepts scalars
-- on INSERT), stamping first_seen_at = last_seen_at = now(). No TTL: permanent
-- current-state (like check_mutes), first_seen must never be silently dropped.
CREATE TABLE IF NOT EXISTS schema_objects (
  server_id      String,
  kind           Int16,
  fqn            String,
  schema         SimpleAggregateFunction(anyLast, String),
  name           SimpleAggregateFunction(anyLast, String),
  size_bytes     SimpleAggregateFunction(anyLast, Int64),
  is_partition   SimpleAggregateFunction(anyLast, UInt8),
  parent_fqn     SimpleAggregateFunction(anyLast, String),
  data_tier      SimpleAggregateFunction(anyLast, Int16),
  first_seen_at  SimpleAggregateFunction(min, DateTime64(3, 'UTC')),
  last_seen_at   SimpleAggregateFunction(max, DateTime64(3, 'UTC'))
) ENGINE = AggregatingMergeTree
ORDER BY (server_id, kind, fqn);
