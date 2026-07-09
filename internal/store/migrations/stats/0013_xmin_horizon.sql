-- xmin_horizon: cluster-global oldest-xmin observation (ly-32k). One row per
-- full snapshot carrying the AGE (in transactions) of the oldest xid still
-- pinned by a backend / replication slot / prepared xact, plus a fixed
-- holder_kind label. Feeds the "blocked by xmin horizon" vacuum check. Vanilla
-- Postgres, RDS-safe (no extensions). All columns are age counts / a bounded
-- label — T1 (data_tier defaults to 1).
CREATE TABLE xmin_horizon (
    server_id       TEXT        NOT NULL,
    collected_at    TIMESTAMPTZ NOT NULL,
    oldest_xmin_age BIGINT      NOT NULL,
    holder_kind     TEXT        NOT NULL,
    data_tier       SMALLINT    NOT NULL DEFAULT 1
) PARTITION BY RANGE (collected_at);

CREATE INDEX xmin_horizon_brin_time ON xmin_horizon USING brin (collected_at);
CREATE INDEX xmin_horizon_srv_time  ON xmin_horizon (server_id, collected_at);
