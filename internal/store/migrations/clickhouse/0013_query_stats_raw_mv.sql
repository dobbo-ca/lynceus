ALTER TABLE query_stats_t2 ADD COLUMN IF NOT EXISTS raw_query String;

CREATE MATERIALIZED VIEW IF NOT EXISTS mv_query_stats_t2_to_t1 TO query_stats AS
SELECT server_id, collected_at, fingerprint, normalized_query, 1 AS data_tier,
       calls, total_time_ms, mean_time_ms, `rows`, shared_blks_hit, shared_blks_read
FROM query_stats_t2;
