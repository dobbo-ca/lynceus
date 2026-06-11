-- Per-index scan counter + size + structural validity/uniqueness flags,
-- sampled from pg_index + pg_class + pg_stat_user_indexes on the slow (~10m)
-- full cadence. Feeds the Schema checks (ly-u4t.23): invalid indexes
-- (is_valid=false) and unused indexes (low idx_scan). One row per
-- (server_id, fqn, collected_at) — identifiers, counts, sizes, and catalog
-- booleans ONLY, NEVER an index expression or predicate. See the IndexStat
-- privacy contract test.
--
-- Range-partitioned by week on collected_at (vanilla Postgres,
-- RDS / Aurora / Cloud SQL safe — no extensions). Append-only series:
-- mirrors table_stats (0006) and freeze_ages (0010).

CREATE TABLE index_stats (
    server_id    TEXT NOT NULL,
    collected_at TIMESTAMPTZ NOT NULL,
    schema_name  TEXT NOT NULL,
    object_name  TEXT NOT NULL,
    fqn          TEXT NOT NULL,
    table_fqn    TEXT NOT NULL,
    idx_scan     BIGINT NOT NULL,
    size_bytes   BIGINT NOT NULL,
    is_valid     BOOLEAN NOT NULL,
    is_ready     BOOLEAN NOT NULL,
    is_unique    BOOLEAN NOT NULL,
    is_primary   BOOLEAN NOT NULL,
    data_tier    SMALLINT NOT NULL DEFAULT 1
) PARTITION BY RANGE (collected_at);

CREATE INDEX index_stats_brin_time ON index_stats USING brin (collected_at);
CREATE INDEX index_stats_srv_fqn   ON index_stats (server_id, fqn, collected_at);
