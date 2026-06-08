-- Schema-object inventory with stable first-seen timestamps.
--
-- One row per (server_id, kind, fqn). first_seen_at is set on initial
-- insert and never updated; last_seen_at and size_bytes_latest are
-- refreshed on every collector snapshot. The table lives in the stats
-- DB so first-seen survives collector restarts and is queryable
-- downstream alongside the rest of the inventory.
--
-- Column naming follows proto SchemaObject field names directly:
--   schema (field 2), name (field 3), size_bytes (field 5).
-- size_bytes_latest diverges intentionally: the proto carries the
-- per-snapshot value; this column stores the latest known size so
-- the inventory view always reflects current state without aggregation.
--
-- This is the CURRENT-STATE upsert table. The sibling ly-xqf.6
-- table_stats table (0006_table_stats.sql) is the APPEND-ONLY weekly-
-- partitioned growth time series; the two coexist deliberately.
--
-- Vanilla PostgreSQL — no extensions. Runs on RDS / Aurora / Cloud SQL.

CREATE TABLE schema_objects (
    server_id            TEXT        NOT NULL,
    kind                 SMALLINT    NOT NULL, -- mirrors proto ObjectKind numeric value
    fqn                  TEXT        NOT NULL, -- "schema.name", or "schema." for kind=SCHEMA
    schema               TEXT        NOT NULL, -- proto SchemaObject.schema (field 2)
    name                 TEXT        NOT NULL, -- proto SchemaObject.name (field 3); empty for kind=SCHEMA
    size_bytes_latest    BIGINT      NOT NULL DEFAULT 0, -- latest snapshot value of proto size_bytes (field 5)
    is_partition         BOOLEAN     NOT NULL DEFAULT false,
    parent_fqn           TEXT        NOT NULL DEFAULT '',
    data_tier            SMALLINT    NOT NULL DEFAULT 1, -- T1 (kept for parity with query_stats)
    first_seen_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (server_id, kind, fqn)
);

-- Cheap server-scoped scans for the inventory view / treemap.
CREATE INDEX schema_objects_server_kind ON schema_objects (server_id, kind);
-- Find children of a partitioned parent cheaply.
CREATE INDEX schema_objects_parent      ON schema_objects (server_id, parent_fqn)
    WHERE parent_fqn <> '';
