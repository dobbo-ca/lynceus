# Fleet A — Entity / Data Model Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a config-only grouping layer — `cluster` + `instance` tables and an `instance_id` link on the existing `servers` table — so the flat `server_id` streams roll up into a `Cluster → Instance → Database(stream)` hierarchy, with zero change to the `server_id`-keyed stats store.

**Architecture:** Reuse `servers` as the per-stream "monitored database" row (its `id` already is the `server_id` stream key). Add two parent tables and one FK. A handful of `Config` store methods create entities, resolve a stream to its parents, and turn an instance/cluster into the set of `server_id`s to read from the unchanged stats store. An idempotent `BackfillFleet` links existing streams 1:1.

**Tech Stack:** Go, pgx/v5, `github.com/google/uuid` (id generation), vanilla PostgreSQL native migrations (no extensions, RDS/Aurora-safe), testcontainers + `internal/testpg.ReadyWait()`.

**Design spec:** `docs/superpowers/specs/2026-06-11-fleet-A-entity-model-design.md`.

---

## File structure

- `internal/store/migrations/config/0005_fleet.sql` — **create**: `cluster` + `instance` tables; `servers` gains `instance_id` + `database_name`. Auto-picked up by the `//go:embed migrations/config/*.sql` glob in `internal/store/migrate.go`.
- `internal/store/fleet.go` — **create**: `Cluster`/`Instance`/`ServerStream` types + `Config` methods (create/assign/list/resolve/roll-up) + `BackfillFleet`. Kept separate from `config.go` (which owns audit/capability) so the fleet model is a focused unit.
- `internal/store/fleet_test.go` — **create**: migration, store-func, and backfill integration tests (external `store_test` package; reuses the existing `newPool(t)` helper in `store_test.go`).

No `cmd/` changes: config migrations and the backfill are run out-of-band by the operator/migration step (the codebase does not auto-apply config migrations in `cmd/api`). `BackfillFleet` is exposed for that step and exercised by tests.

---

## Task 1: Migration — `cluster` / `instance` tables + `servers` link

**Files:**
- Create: `internal/store/migrations/config/0005_fleet.sql`
- Test: `internal/store/fleet_test.go`

- [ ] **Step 1: Write the failing migration test**

Create `internal/store/fleet_test.go`:

```go
package store_test

import (
	"context"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/store"
)

// TestFleetMigration_createsEntitiesAndServerLink verifies 0005_fleet.sql adds
// the cluster + instance tables and the new servers columns, and that
// re-applying the config migrations is a no-op (Migrate tracks applied versions).
func TestFleetMigration_createsEntitiesAndServerLink(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// cluster + instance tables exist.
	for _, tbl := range []string{"cluster", "instance"} {
		var ok bool
		if err := pool.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_name = $1)`, tbl,
		).Scan(&ok); err != nil || !ok {
			t.Fatalf("table %q missing: ok=%v err=%v", tbl, ok, err)
		}
	}

	// servers gained instance_id + database_name.
	for _, col := range []string{"instance_id", "database_name"} {
		var ok bool
		if err := pool.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM information_schema.columns
			   WHERE table_name = 'servers' AND column_name = $1)`, col,
		).Scan(&ok); err != nil || !ok {
			t.Fatalf("servers.%s missing: ok=%v err=%v", col, ok, err)
		}
	}

	// instance.cluster_id FK enforces referential integrity.
	if _, err := pool.Exec(ctx,
		`INSERT INTO instance (id, cluster_id, name) VALUES ('i-x', 'no-such-cluster', 'x')`,
	); err == nil {
		t.Fatal("expected FK violation inserting instance with unknown cluster_id")
	}

	// idempotency: re-applying is a no-op.
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("re-apply: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestFleetMigration`
Expected: FAIL — `table "cluster" missing` (migration not yet present).

- [ ] **Step 3: Write the migration**

Create `internal/store/migrations/config/0005_fleet.sql`:

```sql
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestFleetMigration`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/migrations/config/0005_fleet.sql internal/store/fleet_test.go
git commit -m "store(fleet): 0005 cluster/instance tables + servers.instance_id link (ly-99s.1)"
```

---

## Task 2: Store — entity types + create/assign/list/resolve/roll-up

**Files:**
- Create: `internal/store/fleet.go`
- Test: `internal/store/fleet_test.go` (extend)

- [ ] **Step 1: Write the failing store-func test**

Append to `internal/store/fleet_test.go`:

```go
func TestFleetStore_createResolveAndRollup(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg := store.NewConfig(pool)

	// Two server streams that will live under one instance, plus a second
	// instance under the same cluster with its own stream.
	for _, id := range []string{"srv-app", "srv-reporting", "srv-replica"} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO servers (id, name) VALUES ($1, $1)`, id); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}

	cl, err := cfg.CreateCluster(ctx, "prod-cluster")
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}
	primary, err := cfg.CreateInstance(ctx, cl.ID, "primary")
	if err != nil {
		t.Fatalf("CreateInstance primary: %v", err)
	}
	replica, err := cfg.CreateInstance(ctx, cl.ID, "replica")
	if err != nil {
		t.Fatalf("CreateInstance replica: %v", err)
	}
	if primary.Role != "unknown" {
		t.Fatalf("new instance role = %q, want default \"unknown\"", primary.Role)
	}

	// primary instance serves two databases; replica serves one.
	for _, sid := range []string{"srv-app", "srv-reporting"} {
		if err := cfg.AssignServerToInstance(ctx, sid, primary.ID); err != nil {
			t.Fatalf("assign %s: %v", sid, err)
		}
	}
	if err := cfg.AssignServerToInstance(ctx, "srv-replica", replica.ID); err != nil {
		t.Fatalf("assign replica: %v", err)
	}

	// ResolveServer returns the full chain.
	ss, inst, gotCl, err := cfg.ResolveServer(ctx, "srv-app")
	if err != nil {
		t.Fatalf("ResolveServer: %v", err)
	}
	if ss.ServerID != "srv-app" || ss.InstanceID != primary.ID ||
		inst.ID != primary.ID || inst.ClusterID != cl.ID || gotCl.ID != cl.ID {
		t.Fatalf("resolve chain wrong: ss=%+v inst=%+v cl=%+v", ss, inst, gotCl)
	}

	// Roll-up: instance -> its stream ids; cluster -> all stream ids.
	got, err := cfg.ServerIDsForInstance(ctx, primary.ID)
	if err != nil {
		t.Fatalf("ServerIDsForInstance: %v", err)
	}
	if len(got) != 2 || got[0] != "srv-app" || got[1] != "srv-reporting" {
		t.Fatalf("instance stream ids = %v, want [srv-app srv-reporting]", got)
	}
	all, err := cfg.ServerIDsForCluster(ctx, cl.ID)
	if err != nil {
		t.Fatalf("ServerIDsForCluster: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("cluster stream ids = %v, want 3", all)
	}

	// Listing helpers for the UI.
	clusters, err := cfg.ListClusters(ctx)
	if err != nil || len(clusters) != 1 || clusters[0].ID != cl.ID {
		t.Fatalf("ListClusters = %+v err=%v", clusters, err)
	}
	insts, err := cfg.ListInstances(ctx, cl.ID)
	if err != nil || len(insts) != 2 {
		t.Fatalf("ListInstances = %+v err=%v", insts, err)
	}
	streams, err := cfg.ListServerStreams(ctx, primary.ID)
	if err != nil || len(streams) != 2 {
		t.Fatalf("ListServerStreams = %+v err=%v", streams, err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestFleetStore`
Expected: build failure — `cfg.CreateCluster undefined`.

- [ ] **Step 3: Write the store types + functions**

Create `internal/store/fleet.go`:

```go
package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Cluster is one logical grouping of instances (a primary + its replicas).
type Cluster struct {
	ID        string
	Name      string
	CreatedAt time.Time
}

// Instance is one Postgres endpoint within a cluster. Role is populated by the
// primary/replica consolidation bead (ly-99s.3); it defaults to "unknown".
type Instance struct {
	ID        string
	ClusterID string
	Name      string
	Role      string // primary | replica | unknown
	CreatedAt time.Time
}

// ServerStream is the per-stream "monitored database" row (the reused servers
// table). ServerID is the stats-store stream key. InstanceID / DatabaseName are
// "" when not yet linked/known.
type ServerStream struct {
	ServerID     string
	Name         string
	InstanceID   string
	DatabaseName string
	T2Enabled    bool
	CreatedAt    time.Time
}

// CreateCluster inserts a cluster with a generated id and returns it.
func (c *Config) CreateCluster(ctx context.Context, name string) (Cluster, error) {
	cl := Cluster{ID: uuid.NewString(), Name: name}
	err := c.pool.QueryRow(ctx,
		`INSERT INTO cluster (id, name) VALUES ($1, $2) RETURNING created_at`,
		cl.ID, cl.Name,
	).Scan(&cl.CreatedAt)
	return cl, err
}

// CreateInstance inserts an instance under clusterID with a generated id and
// returns it (with the DB-defaulted role).
func (c *Config) CreateInstance(ctx context.Context, clusterID, name string) (Instance, error) {
	in := Instance{ID: uuid.NewString(), ClusterID: clusterID, Name: name}
	err := c.pool.QueryRow(ctx,
		`INSERT INTO instance (id, cluster_id, name) VALUES ($1, $2, $3)
		 RETURNING role, created_at`,
		in.ID, in.ClusterID, in.Name,
	).Scan(&in.Role, &in.CreatedAt)
	return in, err
}

// AssignServerToInstance links a server stream to an instance.
func (c *Config) AssignServerToInstance(ctx context.Context, serverID, instanceID string) error {
	_, err := c.pool.Exec(ctx,
		`UPDATE servers SET instance_id = $2 WHERE id = $1`, serverID, instanceID)
	return err
}

// ListClusters returns all clusters ordered by name.
func (c *Config) ListClusters(ctx context.Context) ([]Cluster, error) {
	rows, err := c.ro.Query(ctx, `SELECT id, name, created_at FROM cluster ORDER BY name, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Cluster
	for rows.Next() {
		var cl Cluster
		if err := rows.Scan(&cl.ID, &cl.Name, &cl.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, cl)
	}
	return out, rows.Err()
}

// ListInstances returns the instances under clusterID ordered by name.
func (c *Config) ListInstances(ctx context.Context, clusterID string) ([]Instance, error) {
	rows, err := c.ro.Query(ctx,
		`SELECT id, cluster_id, name, role, created_at FROM instance
		  WHERE cluster_id = $1 ORDER BY name, id`, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Instance
	for rows.Next() {
		var in Instance
		if err := rows.Scan(&in.ID, &in.ClusterID, &in.Name, &in.Role, &in.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

// serverStreamCols is the shared projection for ServerStream scans. instance_id
// and database_name are nullable in the schema → COALESCE to "".
const serverStreamCols = `id, name, COALESCE(instance_id, ''), COALESCE(database_name, ''), t2_enabled, created_at`

func scanServerStream(row pgx.Row) (ServerStream, error) {
	var s ServerStream
	err := row.Scan(&s.ServerID, &s.Name, &s.InstanceID, &s.DatabaseName, &s.T2Enabled, &s.CreatedAt)
	return s, err
}

// ListServerStreams returns the server streams (monitored databases) under
// instanceID ordered by id.
func (c *Config) ListServerStreams(ctx context.Context, instanceID string) ([]ServerStream, error) {
	rows, err := c.ro.Query(ctx,
		`SELECT `+serverStreamCols+` FROM servers WHERE instance_id = $1 ORDER BY id`, instanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ServerStream
	for rows.Next() {
		s, err := scanServerStream(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ResolveServer returns the stream row plus its parent instance and cluster.
// Errors with pgx.ErrNoRows if serverID is unknown or not yet linked.
func (c *Config) ResolveServer(ctx context.Context, serverID string) (ServerStream, Instance, Cluster, error) {
	var (
		s  ServerStream
		in Instance
		cl Cluster
	)
	err := c.ro.QueryRow(ctx,
		`SELECT s.id, s.name, COALESCE(s.instance_id, ''), COALESCE(s.database_name, ''), s.t2_enabled, s.created_at,
		        i.id, i.cluster_id, i.name, i.role, i.created_at,
		        c.id, c.name, c.created_at
		   FROM servers s
		   JOIN instance i ON i.id = s.instance_id
		   JOIN cluster  c ON c.id = i.cluster_id
		  WHERE s.id = $1`, serverID,
	).Scan(
		&s.ServerID, &s.Name, &s.InstanceID, &s.DatabaseName, &s.T2Enabled, &s.CreatedAt,
		&in.ID, &in.ClusterID, &in.Name, &in.Role, &in.CreatedAt,
		&cl.ID, &cl.Name, &cl.CreatedAt,
	)
	return s, in, cl, err
}

// ServerIDsForInstance returns the server_id stream keys under instanceID — the
// set to read from the (unchanged) stats store to roll up an instance.
func (c *Config) ServerIDsForInstance(ctx context.Context, instanceID string) ([]string, error) {
	return c.scanServerIDs(ctx,
		`SELECT id FROM servers WHERE instance_id = $1 ORDER BY id`, instanceID)
}

// ServerIDsForCluster returns the server_id stream keys across every instance in
// clusterID — the set to read from the stats store to roll up a cluster.
func (c *Config) ServerIDsForCluster(ctx context.Context, clusterID string) ([]string, error) {
	return c.scanServerIDs(ctx,
		`SELECT s.id FROM servers s
		   JOIN instance i ON i.id = s.instance_id
		  WHERE i.cluster_id = $1 ORDER BY s.id`, clusterID)
}

func (c *Config) scanServerIDs(ctx context.Context, q string, arg string) ([]string, error) {
	rows, err := c.ro.Query(ctx, q, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestFleetStore`
Expected: PASS. (If `go` reports `github.com/google/uuid` is an indirect dep, run `go mod tidy` to promote it — it is already in `go.mod` at v1.6.0.)

- [ ] **Step 5: Commit**

```bash
git add internal/store/fleet.go internal/store/fleet_test.go go.mod go.sum
git commit -m "store(fleet): cluster/instance entity CRUD + resolve + server_id roll-up (ly-99s.1)"
```

---

## Task 3: Store — idempotent `BackfillFleet`

**Files:**
- Modify: `internal/store/fleet.go`
- Test: `internal/store/fleet_test.go` (extend)

- [ ] **Step 1: Write the failing backfill test**

Append to `internal/store/fleet_test.go`:

```go
func TestBackfillFleet_linksLegacyServers1to1AndIsIdempotent(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg := store.NewConfig(pool)

	// Two legacy server streams with no instance link.
	for _, r := range [][2]string{{"srv-1", "prod db"}, {"srv-2", "stage db"}} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO servers (id, name) VALUES ($1, $2)`, r[0], r[1]); err != nil {
			t.Fatalf("seed %s: %v", r[0], err)
		}
	}

	if err := cfg.BackfillFleet(ctx); err != nil {
		t.Fatalf("BackfillFleet: %v", err)
	}

	// Each legacy stream now resolves to its own cluster+instance, names kept.
	for _, sid := range []string{"srv-1", "srv-2"} {
		ss, inst, cl, err := cfg.ResolveServer(ctx, sid)
		if err != nil {
			t.Fatalf("ResolveServer %s after backfill: %v", sid, err)
		}
		if ss.InstanceID == "" || inst.ClusterID != cl.ID {
			t.Fatalf("%s not fully linked: ss=%+v inst=%+v cl=%+v", sid, ss, inst, cl)
		}
	}
	assertCounts := func(wantClusters, wantInstances int) {
		t.Helper()
		var nc, ni int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM cluster`).Scan(&nc); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM instance`).Scan(&ni); err != nil {
			t.Fatal(err)
		}
		if nc != wantClusters || ni != wantInstances {
			t.Fatalf("counts: clusters=%d instances=%d, want %d/%d", nc, ni, wantClusters, wantInstances)
		}
	}
	assertCounts(2, 2)

	// Idempotent: a second run links nothing new (no duplicate clusters/instances).
	if err := cfg.BackfillFleet(ctx); err != nil {
		t.Fatalf("BackfillFleet re-run: %v", err)
	}
	assertCounts(2, 2)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestBackfillFleet`
Expected: build failure — `cfg.BackfillFleet undefined`.

- [ ] **Step 3: Implement `BackfillFleet`**

Append to `internal/store/fleet.go`:

```go
// BackfillFleet links every server stream that has no instance yet to a freshly
// created 1:1 cluster + instance (name derived from the stream). Existing
// single-stream deployments become a cluster-of-one / instance-of-one with no
// behavior change. Idempotent: only NULL-instance_id rows are processed, so a
// re-run creates nothing. Intended to run alongside ApplyConfigMigrations.
func (c *Config) BackfillFleet(ctx context.Context) error {
	rows, err := c.pool.Query(ctx,
		`SELECT id, name FROM servers WHERE instance_id IS NULL ORDER BY id`)
	if err != nil {
		return err
	}
	type pending struct{ id, name string }
	var todo []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.name); err != nil {
			rows.Close()
			return err
		}
		todo = append(todo, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, p := range todo {
		name := p.name
		if name == "" {
			name = p.id
		}
		cl, err := c.CreateCluster(ctx, name)
		if err != nil {
			return err
		}
		in, err := c.CreateInstance(ctx, cl.ID, name)
		if err != nil {
			return err
		}
		if err := c.AssignServerToInstance(ctx, p.id, in.ID); err != nil {
			return err
		}
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestBackfillFleet`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/fleet.go internal/store/fleet_test.go
git commit -m "store(fleet): idempotent BackfillFleet links legacy server streams 1:1 (ly-99s.1)"
```

---

## Task 4: Full verification + PR

- [ ] **Step 1: Run the whole suite with the race detector**

Run: `go test ./... -race`
Expected: all packages PASS. Integration tests use testcontainers + `testpg.ReadyWait()`; if Docker is unavailable they `t.Skip`, not fail.

- [ ] **Step 2: Lint clean**

Run: `~/go/bin/golangci-lint run` (v2.12.2)
Expected: no findings on the touched files. If a stale finding appears in a sibling `.claude/worktrees/*` path, run `~/go/bin/golangci-lint cache clean` and re-run scoped to `./internal/store/...`.

- [ ] **Step 3: Push the branch and open the PR off origin/main**

```bash
git branch --show-current   # confirm THIS session's worktree branch
git push -u origin HEAD
gh pr create --base main \
  --title "feat(fleet): entity model — cluster/instance grouping over server_id streams (ly-99s.1)" \
  --body "<summary: design link, the grouping-layer decision (no stats migration), 0005 migration, store funcs, BackfillFleet, testing>"
```

- [ ] **Step 4: Watch CI green, then move the bead**

```bash
gh pr checks <n> --watch
bd label remove ly-99s.1 needs-plan && bd label add ly-99s.1 ready-impl
bd note ly-99s.1 "PR #<n>: 0005 cluster/instance tables + servers link, fleet store funcs + BackfillFleet. Unblocks B/C/D/E once merged."
```

After merge: `bd close ly-99s.1`.

---

## Self-review

**Spec coverage:**
- `cluster`/`instance` tables + `servers` link → Task 1 migration. ✓
- Reuse `servers` as the stream row (no parallel table) → Task 1 (`ALTER TABLE servers`). ✓
- No stats-store / audit_log / capability_policy migration → only config schema touched; `server_id` semantics unchanged. ✓
- Store funcs (`CreateCluster`/`CreateInstance`/`AssignServerToInstance`/`ListClusters`/`ListInstances`/`ListServerStreams`/`ResolveServer`/`ServerIDsForInstance`/`ServerIDsForCluster`) → Task 2. ✓
- `BackfillFleet` idempotent 1:1 → Task 3. ✓
- `role` column present (populated by C) → Task 1 schema + asserted default in Task 2. ✓
- Org/Account future seam → documented in the migration comment (`cluster.org_id` later); no code now (YAGNI). ✓
- Vanilla Postgres / no extensions / no TimescaleDB → migration uses core SQL only; ids generated in Go (no `gen_random_uuid`). ✓

**Placeholder scan:** none — every step shows complete SQL/Go/commands. The only `<n>`/`<summary>` tokens are in the PR step, which is inherently interactive.

**Type consistency:** `Cluster{ID,Name,CreatedAt}`, `Instance{ID,ClusterID,Name,Role,CreatedAt}`, `ServerStream{ServerID,Name,InstanceID,DatabaseName,T2Enabled,CreatedAt}` are defined in Task 2 and used identically in Tasks 2–3 tests and `ResolveServer`/`BackfillFleet`. Method names (`CreateCluster`, `CreateInstance`, `AssignServerToInstance`, `ResolveServer`, `ServerIDsForInstance`, `ServerIDsForCluster`, `BackfillFleet`) match across plan + tests. The `serverStreamCols` projection order matches `scanServerStream`'s scan order. Migration column order (`instance_id`, `database_name`) matches the test's column-existence checks.
```
