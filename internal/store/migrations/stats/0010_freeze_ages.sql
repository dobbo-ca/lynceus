-- freeze_ages: per-database + per-table transaction-id / MultiXact freeze
-- AGES (counts only — never raw xids). Feeds the wraparound check
-- (ly-u4t.26) + VACUUM advisor Freezing view. Vanilla Postgres, RDS-safe
-- (no extensions). All columns are identifiers / age counts — T1
-- (data_tier defaults to 1).
CREATE TABLE freeze_ages (
    server_id                 TEXT        NOT NULL,
    collected_at              TIMESTAMPTZ NOT NULL,
    scope                     TEXT        NOT NULL,
    schema_name               TEXT        NOT NULL,
    object_name               TEXT        NOT NULL,
    fqn                       TEXT        NOT NULL,
    xid_age                   BIGINT      NOT NULL,
    mxid_age                  BIGINT      NOT NULL,
    autovacuum_freeze_max_age BIGINT      NOT NULL,
    data_tier                 SMALLINT    NOT NULL DEFAULT 1
) PARTITION BY RANGE (collected_at);

CREATE INDEX freeze_ages_brin_time ON freeze_ages USING brin (collected_at);
CREATE INDEX freeze_ages_srv_fqn   ON freeze_ages (server_id, fqn, collected_at);
