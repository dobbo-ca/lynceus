-- Dead-letter queue: parks ingest frames that could not be accepted
-- (rate-limited, malformed, or write error). raw is the serialized Snapshot
-- protobuf (T1, literal-free by contract) so a future retry can re-decode it.
-- Append-only, TTL-bounded — there is no retry consumer today, so the TTL
-- bounds growth. server_id is '' for pre-server_id failures (unmarshal errors).
CREATE TABLE IF NOT EXISTS dlq (
  received_at DateTime64(3, 'UTC') DEFAULT now64(3),
  server_id   String,
  reason      String,
  raw         String
) ENGINE = MergeTree
PARTITION BY toYYYYMM(received_at)
ORDER BY (received_at, server_id)
TTL toDateTime(received_at) + INTERVAL 14 DAY;
