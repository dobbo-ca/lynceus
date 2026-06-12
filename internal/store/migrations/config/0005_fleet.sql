-- Fleet entity model (bead ly-99s.1): Cluster -> Instance -> Database(stream).
-- A config-only GROUPING layer over the server_id-keyed stats store: the
-- partitioned stats tables, audit_log, and capability_policy are UNCHANGED.
-- The existing `servers` table is reused as the per-stream "monitored database"
-- row (servers.id is the server_id stream key); this migration adds the two
-- missing parent levels plus one FK. Reads roll up by joining here.
-- Vanilla PostgreSQL only (RDS / Aurora / Cloud SQL safe — no extensions).
-- An Org/Account tenant layer above cluster is a future seam (cluster.org_id).

CREATE TABLE cluster (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE instance (
    id          TEXT PRIMARY KEY,
    cluster_id  TEXT NOT NULL REFERENCES cluster(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    role        TEXT NOT NULL DEFAULT 'unknown',  -- primary|replica|unknown; populated by fleet C (ly-99s.3)
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX instance_cluster ON instance (cluster_id);

-- servers becomes the per-stream "monitored database" row. id stays the
-- server_id stream key; instance_id links it up the hierarchy; database_name
-- records the Postgres datname (NULL until the collector topology bead sets it).
ALTER TABLE servers ADD COLUMN instance_id   TEXT REFERENCES instance(id) ON DELETE SET NULL;
ALTER TABLE servers ADD COLUMN database_name TEXT;
CREATE INDEX servers_instance ON servers (instance_id);
