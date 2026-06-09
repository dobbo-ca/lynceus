-- checks_results: append-only, time-range-partitioned store of Checks
-- engine output. Vanilla Postgres (RDS/Aurora/Cloud SQL safe — no
-- extensions). One row per firing check observation per evaluation tick.
-- All columns are identifiers / fixed enums / counts / bounded
-- package-authored strings — T1 (data_tier defaults to 1).
CREATE TABLE checks_results (
    server_id    TEXT        NOT NULL,
    evaluated_at TIMESTAMPTZ NOT NULL,
    check_id     TEXT        NOT NULL,
    category     TEXT        NOT NULL,
    severity     TEXT        NOT NULL,
    status       TEXT        NOT NULL,
    object       TEXT        NOT NULL,
    detail       TEXT        NOT NULL,
    muted        BOOLEAN     NOT NULL DEFAULT false,
    data_tier    SMALLINT    NOT NULL DEFAULT 1
) PARTITION BY RANGE (evaluated_at);

CREATE INDEX checks_results_brin_time ON checks_results USING brin (evaluated_at);
CREATE INDEX checks_results_srv_check ON checks_results (server_id, check_id, evaluated_at);
