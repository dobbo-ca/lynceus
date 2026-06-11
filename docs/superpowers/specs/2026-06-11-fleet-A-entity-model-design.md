# Fleet A — Entity / Data Model Design

**Bead:** ly-99s.1 (keystone of epic ly-99s — Fleet topology & organization). **Blocks:** ly-99s.2
(collector topology), ly-99s.3 (primary/replica consolidation), ly-99s.4 (organization UI), ly-99s.5
(cloud ingestion), and ly-u4t.25 (disk-space leaf).

## Problem

Lynceus is keyed on a single flat identity: a free-form, operator-set `server_id`
(`LYNCEUS_SERVER_ID`). One `server_id` = one collector = one DSN = one database on one instance. It is
the atomic key in **every** partitioned stats table, in `audit_log.server_id`, in `capability_policy`,
in the `/api/servers/{id}/…` routes, and in the policy snapshot. The only config entity is flat
`servers(id, name, t2_enabled)`.

The fleet vision needs a hierarchy — **Cluster (primary + replicas) → Instance (a Postgres endpoint) →
Database (a monitored database stream within an instance)** — so that one collector can report for 1+
instances and all/selected databases, a primary and its replicas consolidate into one logical unit, and
a UI can organize the structures.

## Decision (chosen approach: grouping layer, no stats migration)

Add a **config-only grouping layer** over the existing `server_id`-keyed stats store. The partitioned
stats tables, `audit_log`, `capability_policy`, and the `server_id` routes are **unchanged**. New config
tables group the existing `server_id` streams and record the `server_id ↔ (instance, cluster)` mapping;
reads roll up by joining config.

Key simplification: the existing **`servers` table already is the per-stream entity** — its `id` is the
`server_id` stream key, with `name` + `t2_enabled`. So A does **not** invent a parallel "database" table;
it reuses `servers` as the Database/stream row and adds the two missing parent levels plus one FK.

```
cluster (new)
  └─ instance (new)
       └─ servers (existing; id = server_id stream key)   ← reused as the "monitored database stream"
```

### Trade-off accepted

`server_id` stays overloaded as "one instance+database telemetry stream," and instance-scope vs
database-scope is by convention, not schema. **Instance-level metrics** (disk/host metrics, connection
samples) attribute to the instance via the `servers.instance_id` FK — the collector ships them on a
designated stream for that instance, and the `HostMetric.instance` field (see the disk-space design)
carries the instance identity explicitly. This is the price of avoiding a risky migration of every
partitioned stats table, and it unblocks B/C/D fastest. Promotion to first-class `database_id` columns
in the stats store remains possible later as its own bead if per-database fidelity in shared tables is
needed.

## Schema — config DB migration `0005_fleet.sql`

Vanilla PostgreSQL only (RDS/Aurora-safe, no extensions). Table names avoid the reserved word
`database` by reusing `servers`.

```sql
CREATE TABLE cluster (
    id          TEXT PRIMARY KEY,                 -- generated id
    name        TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
    -- future seam: org_id TEXT REFERENCES org(id)  (single-tenant for now)
);

CREATE TABLE instance (
    id          TEXT PRIMARY KEY,                 -- generated id
    cluster_id  TEXT NOT NULL REFERENCES cluster(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    role        TEXT NOT NULL DEFAULT 'unknown',  -- primary|replica|unknown; populated by fleet C
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX instance_cluster ON instance (cluster_id);

-- servers becomes the per-stream "monitored database" row; add the parent link
-- and (optionally) the Postgres database name. id stays the server_id stream key.
ALTER TABLE servers ADD COLUMN instance_id   TEXT REFERENCES instance(id) ON DELETE SET NULL;
ALTER TABLE servers ADD COLUMN database_name TEXT;        -- the PG datname; NULL until B sets it
CREATE INDEX servers_instance ON servers (instance_id);
```

### Backfill (idempotent, runs after migration)

For each existing `servers` row with `instance_id IS NULL`, create a 1:1 `cluster` + `instance`
(`name` derived from `servers.name`) and set `servers.instance_id`. Existing single-stream deployments
become a cluster-of-one / instance-of-one with zero behavior change. Generated ids are created in Go
(PG12 baseline has no core `gen_random_uuid()`), keeping the backfill portable and deterministic per
run. Idempotent: re-running skips rows already linked.

## Store API (`internal/store/config.go` additions)

A ships the model + the read/write surface the downstream beads need; it does **not** touch the
collector (B) or the UI (D).

- `BackfillFleet(ctx) error` — one-time, idempotent linkage described above.
- `CreateCluster(ctx, name) (Cluster, error)`, `CreateInstance(ctx, clusterID, name) (Instance, error)`,
  `AssignServerToInstance(ctx, serverID, instanceID) error` — CRUD used by B/D.
- `ListClusters(ctx) ([]Cluster, error)`, `ListInstances(ctx, clusterID) ([]Instance, error)`,
  `ListServerStreams(ctx, instanceID) ([]ServerStream, error)` — hierarchy traversal for the UI.
- `ResolveServer(ctx, serverID) (ServerStream, Instance, Cluster, error)` — stream → parents.
- `ServerIDsForInstance(ctx, instanceID) ([]string, error)`,
  `ServerIDsForCluster(ctx, clusterID) ([]string, error)` — roll-up helpers: turn an instance/cluster
  into the set of `server_id`s to read from the (unchanged) stats store.

Roll-up read pattern for B/C/D: resolve the instance/cluster → its `server_id` set via the helpers, then
issue the existing `server_id`-keyed stats reads and aggregate. No stats-store change.

## Data flow (unchanged ingest, new grouping on read)

```
collector ──(server_id stream)──► ingest ──► stats store           (UNCHANGED)
                                                  ▲
api/UI/scheduler: cluster/instance ──► ServerIDsForInstance/Cluster ─┘  (JOIN config → server_id set → read)
```

## Invariants preserved

- **No stats-store migration**; `server_id` reads/writes unchanged.
- **Vanilla Postgres** config schema (no extensions) — RDS/Aurora-safe.
- **No TimescaleDB dependency.**
- `audit_log.server_id`, `capability_policy`, `/api/servers/{id}/…`, and the policy snapshot keep
  working verbatim (server_id semantics untouched).

## Testing (TDD, testcontainers + `internal/testpg.ReadyWait()`)

- **migration:** `0005_fleet.sql` creates `cluster`/`instance` + `servers.instance_id`/`database_name`;
  re-apply is idempotent.
- **backfill:** seed N legacy `servers` rows → `BackfillFleet` creates N cluster/instance chains, links
  each, preserves names; second run is a no-op (no duplicates).
- **store funcs:** create cluster→instance, assign a server stream, then `ResolveServer` returns the full
  chain; `ServerIDsForInstance`/`ServerIDsForCluster` return the expected stream sets (incl. multiple
  streams under one instance, multiple instances under one cluster).

## Out of scope (this bead)

Collector multi-instance/multi-db wiring (B); replica detection/consolidation logic (C — A only adds the
`role` column); UI/API routes (D); cloud ingestion (E); first-class `database_id` columns in the stats
store; an Org/Account tenant layer (designed as a future `cluster.org_id` seam).
```
