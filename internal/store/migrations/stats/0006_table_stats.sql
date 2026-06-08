-- Per-table size/growth + TOAST/heap/index breakdown plus dead-tuple and
-- vacuum/analyze metrics, sampled from pg_class + pg_stat_user_tables on the
-- slow (~10m) full cadence. One row per (server_id, fqn, collected_at) — all
-- sizes/counts/timestamps, NEVER a column value or predicate. See the
-- TableStat privacy contract test.
--
-- Range-partitioned by week on collected_at (vanilla Postgres,
-- RDS / Aurora / Cloud SQL safe — no extensions). Append-only growth series:
-- two snapshots a week apart make growth derivable.

CREATE TABLE table_stats (
    server_id           TEXT NOT NULL,
    collected_at        TIMESTAMPTZ NOT NULL,
    schema_name         TEXT NOT NULL,
    object_name         TEXT NOT NULL,
    fqn                 TEXT NOT NULL,
    total_bytes         BIGINT NOT NULL,
    heap_bytes          BIGINT NOT NULL,
    toast_bytes         BIGINT NOT NULL,
    indexes_bytes       BIGINT NOT NULL,
    row_estimate        BIGINT NOT NULL,
    live_tuples         BIGINT NOT NULL,
    dead_tuples         BIGINT NOT NULL,
    n_mod_since_analyze BIGINT NOT NULL,
    seq_scan            BIGINT NOT NULL,
    idx_scan            BIGINT NOT NULL,
    n_tup_ins           BIGINT NOT NULL,
    n_tup_upd           BIGINT NOT NULL,
    n_tup_del           BIGINT NOT NULL,
    n_tup_hot_upd       BIGINT NOT NULL,
    last_vacuum         TIMESTAMPTZ,
    last_autovacuum     TIMESTAMPTZ,
    last_analyze        TIMESTAMPTZ,
    last_autoanalyze    TIMESTAMPTZ,
    vacuum_count        BIGINT NOT NULL,
    autovacuum_count    BIGINT NOT NULL,
    data_tier           SMALLINT NOT NULL DEFAULT 1
) PARTITION BY RANGE (collected_at);

CREATE INDEX table_stats_brin_time ON table_stats USING brin (collected_at);
CREATE INDEX table_stats_srv_fqn   ON table_stats (server_id, fqn, collected_at);
