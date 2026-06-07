-- Extracted auto_explain plans, normalized at the collector.
-- One row per captured plan execution-sample. plan_tree holds the
-- normalized PlanNode tree as JSONB (literal-free — every condition is
-- placeholder-only or omitted; see the QueryPlan privacy contract test).
-- There is deliberately NO raw-plan-text column: the literal-bearing
-- source plan body stays in the collector-local T2 LogPayload.
--
-- Range-partitioned by week on captured_at (vanilla Postgres,
-- RDS / Aurora / Cloud SQL safe — no extensions). M3 EXPLAIN insight
-- passes read plan_tree via JSONB operators plus the hot scalar columns.

CREATE TABLE query_plans (
    server_id            TEXT NOT NULL,
    fingerprint          TEXT NOT NULL,
    captured_at          TIMESTAMPTZ NOT NULL,
    format_version       INTEGER NOT NULL,
    total_cost           DOUBLE PRECISION NOT NULL,
    actual_total_time_ms DOUBLE PRECISION NOT NULL,
    plan_tree            JSONB NOT NULL,
    data_tier            SMALLINT NOT NULL DEFAULT 1
) PARTITION BY RANGE (captured_at);

CREATE INDEX query_plans_brin_time ON query_plans USING brin (captured_at);
CREATE INDEX query_plans_srv_fp ON query_plans (server_id, fingerprint, captured_at);
