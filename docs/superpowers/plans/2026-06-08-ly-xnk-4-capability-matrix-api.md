# Capability Matrix API Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Expose a per-server capability matrix (discovered availability × effective policy × final-enabled) over an authenticated GET endpoint, plus an audited POST toggle that reuses the existing `SetCapabilityPolicy` writer, backed by a new `discovered_capability` config table and its store methods.

**Architecture:** A new `discovered_capability` table (config DB) persists each `caps.Discover` result so the matrix GET has a "discovered" column to join, with `UpsertDiscoveredCapabilities`/`ListDiscoveredCapabilities` store methods mirroring `capability_policy.go`. Two new HTTP handlers in `internal/api/capabilities.go` — `GET /api/servers/{id}/capabilities` joins discovered × `ListCapabilityPolicies` × `caps.Declared()` in Go (absent policy ⇒ enabled), and `POST /api/servers/{id}/capabilities/{cap}` reuses `SetCapabilityPolicy` (which appends a tamper-evident audit row first, then upserts the policy). Both inherit the `withAuth`/`DevAuth` 401 gate already wrapping the mux. No proto changes.

**Tech Stack:** Go, protobuf (`make proto`), pgx/pgxpool, templ+HTMX (where relevant), testcontainers (`postgres:16`) for integration tests.

**Bead:** ly-xnk.4  ·  **Spec:** docs/specs/2026-06-08-layer0-foundation.md  ·  **Layer:** 0 Foundation

---

## Scope boundary with ly-xnk.3

This plan (ly-xnk.4) owns: the `discovered_capability` migration + store methods, and the capability **matrix** GET + audited toggle POST. The sibling bead **ly-xnk.3** (separate plan) owns the collector-local `caps.Gate`, the `caps.Allowed` resolver helper, the reader-gate retrofit, and the `GET /api/servers/{id}/policy-snapshot` endpoint. Do NOT add the gate, the resolver helper, or `/policy-snapshot` here. This plan consumes only the already-shipped store resolver `Config.EffectiveCapability` (`internal/store/capability_policy.go:168`) and `caps.Declared()` (`internal/caps/caps.go:40`).

## File Structure

| File | Action | Responsibility |
|---|---|---|
| `internal/store/migrations/config/0004_discovered_capability.sql` | Create | `discovered_capability` table; NULLS-NOT-DISTINCT uniqueness mirroring `0003_capability_policy.sql:28-29`. |
| `internal/store/discovered_capability.go` | Create | `DiscoveredCapability` struct, `NewDiscoveredCapabilities`/receiver methods `UpsertDiscoveredCapabilities` + `ListDiscoveredCapabilities`. |
| `internal/store/discovered_capability_test.go` | Create | Integration tests (testcontainers) for the migration + upsert/list round-trip. |
| `internal/api/capabilities.go` | Create | `handleCapabilityMatrix` (GET join) + `handleCapabilityToggle` (audited POST) + DTOs + `actorFromContext`. |
| `internal/api/capabilities_test.go` | Create | Integration tests: toggle writes policy+audit, 401 without DevAuth, matrix join, absent-policy-defaults-enabled. |
| `internal/api/server.go` | Modify (routes at `:40-48`; `Server` already holds `conf` at `:25`) | Register the two new routes. |

No proto changes. No change to `NewServer`, `cmd/api/main.go`, or any `*.templ` file (the matrix is JSON, not SSR).

---

## Tasks

### Task 1: `discovered_capability` migration + store methods

**Files:**
- Create `internal/store/migrations/config/0004_discovered_capability.sql`
- Create `internal/store/discovered_capability.go`
- Test: Create `internal/store/discovered_capability_test.go`

The migration is auto-discovered: `migrate.go:18` embeds `migrations/config/*.sql` and `migrate.go:48` `sort.Strings(files)` applies them in lexical order, so `0004` runs after `0003`. The store methods mirror `ListCapabilityPolicies` (`capability_policy.go:198`, reads `c.ro`) and the upsert pattern of `SetCapabilityPolicy` (`capability_policy.go:76-88`). The package import path is `github.com/dobbo-ca/lynceus/internal/store` and `.../internal/caps` (module `github.com/dobbo-ca/lynceus`, confirmed `go.mod`).

- [ ] **Step 1: Write the failing test** — create `internal/store/discovered_capability_test.go`. This is package `store_test`; the `newPool` helper lives in `store_test.go:22`. We import `internal/caps` to build a `caps.Set` (`caps.go:68`, `map[Capability]Status`).

```go
// Integration tests for discovered-capability storage. Real Postgres via
// testcontainers (the newPool helper lives in store_test.go) — we never
// mock the database, because the NULLS NOT DISTINCT uniqueness and the
// FK to servers are part of what we're validating.
package store_test

import (
	"context"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/caps"
	"github.com/dobbo-ca/lynceus/internal/store"
)

func TestDiscoveredCapabilityMigration_createsTable(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for _, col := range []string{
		"server_id", "database_name", "capability", "available", "reason", "observed_at",
	} {
		var ok bool
		_ = pool.QueryRow(ctx,
			`SELECT EXISTS(
			   SELECT 1 FROM information_schema.columns
			   WHERE table_name='discovered_capability' AND column_name=$1
			 )`, col,
		).Scan(&ok)
		if !ok {
			t.Errorf("discovered_capability.%s missing", col)
		}
	}
	// idempotent re-apply.
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("re-apply: %v", err)
	}
}

func TestUpsertDiscoveredCapabilities_roundtripsAndIsIdempotent(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO servers (id, name) VALUES ('srv-1', 'srv one')`,
	); err != nil {
		t.Fatalf("seed server: %v", err)
	}
	dc := store.NewDiscoveredCapabilities(pool)

	set := caps.Set{
		caps.PgStatStatements: {Available: true, Reason: "1.10"},
		caps.AutoExplain:      {Available: false, Reason: "extension not installed"},
	}
	if err := dc.UpsertDiscoveredCapabilities(ctx, "srv-1", "appdb", set); err != nil {
		t.Fatalf("upsert #1: %v", err)
	}

	got, err := dc.ListDiscoveredCapabilities(ctx, "srv-1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2", len(got))
	}
	byCap := map[string]store.DiscoveredCapability{}
	for _, d := range got {
		byCap[d.Capability] = d
	}
	if d := byCap["pg_stat_statements"]; !d.Available || d.Reason != "1.10" || d.DatabaseName != "appdb" {
		t.Errorf("pg_stat_statements row = %+v, want available/1.10/appdb", d)
	}
	if d := byCap["auto_explain"]; d.Available || d.Reason != "extension not installed" {
		t.Errorf("auto_explain row = %+v, want unavailable/not-installed", d)
	}

	// Re-upsert the same key with a flipped verdict: idempotent, still 2 rows.
	set2 := caps.Set{
		caps.PgStatStatements: {Available: false, Reason: "revoked"},
		caps.AutoExplain:      {Available: false, Reason: "extension not installed"},
	}
	if err := dc.UpsertDiscoveredCapabilities(ctx, "srv-1", "appdb", set2); err != nil {
		t.Fatalf("upsert #2: %v", err)
	}
	got2, err := dc.ListDiscoveredCapabilities(ctx, "srv-1")
	if err != nil {
		t.Fatalf("list #2: %v", err)
	}
	if len(got2) != 2 {
		t.Fatalf("after re-upsert got %d rows, want 2 (upsert, not insert-twice)", len(got2))
	}
	for _, d := range got2 {
		if d.Capability == "pg_stat_statements" && (d.Available || d.Reason != "revoked") {
			t.Errorf("pg_stat_statements not updated: %+v", d)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails** — the package will not compile (no `NewDiscoveredCapabilities`, no migration).

```
go test ./internal/store/ -run 'TestDiscoveredCapability|TestUpsertDiscoveredCapabilities' 2>&1 | head -20
```

Expected: `./discovered_capability_test.go:XX:NN: undefined: store.NewDiscoveredCapabilities` and `undefined: store.DiscoveredCapability` (build failure / `FAIL`).

- [ ] **Step 3a: Implement the migration** — create `internal/store/migrations/config/0004_discovered_capability.sql`. Mirrors the NULLS-NOT-DISTINCT uniqueness of `0003_capability_policy.sql:28-29` and the FK + `ON DELETE CASCADE` of `0003:20`.

```sql
-- internal/store/migrations/config/0004_discovered_capability.sql
--
-- ly-xnk.4 — Discovered capability inventory.
--
-- Persists the result of caps.Discover for each (server, database,
-- capability): whether the capability is available on the monitored
-- Postgres and a short, package-authored reason. The capability matrix
-- API (GET /api/servers/{id}/capabilities) joins this "discovered" axis
-- against capability_policy (operator intent) to compute final-enabled.
--
--   database_name IS NULL  -> the server-level / connection-database probe
--   database_name = '<db>'  -> a per-database probe result
--
-- reason is always a bounded, package-authored string (caps.Status.Reason,
-- caps.go:54-64) — never a literal from the monitored database.
--
-- Vanilla PostgreSQL — no extensions (must run on RDS / Aurora).
-- NULLS NOT DISTINCT (core PostgreSQL 15+) makes the unique index treat
-- NULL database_name rows as equal, so each (server, capability) holds at
-- most one server-level discovered row.

CREATE TABLE discovered_capability (
    server_id     TEXT        NOT NULL REFERENCES servers (id) ON DELETE CASCADE,
    database_name TEXT,
    capability    TEXT        NOT NULL,
    available     BOOLEAN     NOT NULL,
    reason        TEXT        NOT NULL DEFAULT '',
    observed_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT discovered_capability_uniq
        UNIQUE NULLS NOT DISTINCT (server_id, database_name, capability)
);

-- The matrix GET reads all rows for one server at a time.
CREATE INDEX discovered_capability_server_idx
    ON discovered_capability (server_id);
```

- [ ] **Step 3b: Implement the store methods** — create `internal/store/discovered_capability.go`. The upsert loops the `caps.Set` and runs one parameterized upsert per entry inside a transaction; `DatabaseName == ""` maps to SQL NULL (the same `dbArg any` trick as `capability_policy.go:68-71`). `ListDiscoveredCapabilities` reads `c.ro` and orders for stable display, mirroring `ListCapabilityPolicies` (`capability_policy.go:198-228`).

```go
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dobbo-ca/lynceus/internal/caps"
)

// DiscoveredCapability is one row of discovered_capability: whether a
// capability is available on a server (DatabaseName == "" for the
// server-level / connection-database probe) or for a specific database,
// plus the package-authored reason and when it was observed.
type DiscoveredCapability struct {
	ServerID     string
	DatabaseName string // "" means the server-level probe (stored as SQL NULL)
	Capability   string
	Available    bool
	Reason       string
	ObservedAt   time.Time
}

// DiscoveredCapabilities is typed access to the discovered_capability
// table. It uses the same primary/read-replica pool split as Config.
type DiscoveredCapabilities struct {
	pool *pgxpool.Pool
	ro   *pgxpool.Pool
}

// NewDiscoveredCapabilities returns a store bound to its primary pool;
// standalone reads fall back to the primary until a replica is attached
// via WithReadPool.
func NewDiscoveredCapabilities(pool *pgxpool.Pool) *DiscoveredCapabilities {
	return &DiscoveredCapabilities{pool: pool, ro: pool}
}

// WithReadPool attaches a read-replica pool used to serve
// ListDiscoveredCapabilities. A nil ro is ignored. Returns the receiver
// for chaining.
func (d *DiscoveredCapabilities) WithReadPool(ro *pgxpool.Pool) *DiscoveredCapabilities {
	if ro != nil {
		d.ro = ro
	}
	return d
}

// UpsertDiscoveredCapabilities persists one caps.Discover result. Each
// entry in set becomes (or refreshes) a discovered_capability row keyed
// by (server_id, database_name, capability); observed_at is refreshed to
// now() on every call. databaseName == "" is stored as SQL NULL.
func (d *DiscoveredCapabilities) UpsertDiscoveredCapabilities(ctx context.Context, serverID, databaseName string, set caps.Set) error {
	if serverID == "" {
		return fmt.Errorf("UpsertDiscoveredCapabilities: serverID required")
	}
	var dbArg any
	if databaseName != "" {
		dbArg = databaseName
	}

	tx, err := d.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for capability, status := range set {
		if _, err := tx.Exec(ctx,
			`INSERT INTO discovered_capability
			   (server_id, database_name, capability, available, reason, observed_at)
			 VALUES ($1, $2, $3, $4, $5, now())
			 ON CONFLICT (server_id, database_name, capability)
			 DO UPDATE SET
			   available   = EXCLUDED.available,
			   reason      = EXCLUDED.reason,
			   observed_at = EXCLUDED.observed_at`,
			serverID, dbArg, string(capability), status.Available, status.Reason,
		); err != nil {
			return fmt.Errorf("upsert %s: %w", capability, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// ListDiscoveredCapabilities returns every discovered_capability row for
// one server, ordered for stable display (by capability, then
// server-level rows before per-database rows, then database_name).
func (d *DiscoveredCapabilities) ListDiscoveredCapabilities(ctx context.Context, serverID string) ([]DiscoveredCapability, error) {
	rows, err := d.ro.Query(ctx,
		`SELECT server_id, database_name, capability, available, reason, observed_at
		   FROM discovered_capability
		  WHERE server_id = $1
		  ORDER BY capability, (database_name IS NOT NULL), database_name`,
		serverID)
	if err != nil {
		return nil, fmt.Errorf("list discovered capabilities: %w", err)
	}
	defer rows.Close()

	var out []DiscoveredCapability
	for rows.Next() {
		var (
			dc     DiscoveredCapability
			dbName *string
		)
		if err := rows.Scan(&dc.ServerID, &dbName, &dc.Capability,
			&dc.Available, &dc.Reason, &dc.ObservedAt); err != nil {
			return nil, fmt.Errorf("scan discovered capability: %w", err)
		}
		if dbName != nil {
			dc.DatabaseName = *dbName
		}
		out = append(out, dc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate discovered capabilities: %w", err)
	}
	return out, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

```
go test ./internal/store/ -run 'TestDiscoveredCapability|TestUpsertDiscoveredCapabilities' -v 2>&1 | tail -20
```

Expected: `--- PASS: TestDiscoveredCapabilityMigration_createsTable`, `--- PASS: TestUpsertDiscoveredCapabilities_roundtripsAndIsIdempotent`, and `ok  	github.com/dobbo-ca/lynceus/internal/store`. (If Docker is unavailable the helper `t.Skipf`s — `store_test.go:34` — and the run reports `SKIP`/`ok`; that is acceptable per repo convention but prefer running where Docker is available.)

- [ ] **Step 5: Commit**

```
git add internal/store/migrations/config/0004_discovered_capability.sql internal/store/discovered_capability.go internal/store/discovered_capability_test.go
git commit -m "feat(store): discovered_capability table + upsert/list (ly-xnk.4)"
```

---

### Task 2: Capability matrix GET handler

**Files:**
- Create `internal/api/capabilities.go` (matrix portion; toggle added in Task 3)
- Modify `internal/api/server.go` (`routes()` at `:40-48` — add the GET route)
- Modify `internal/api/server.go` (`Server` struct at `:22-27` — add a `disc *store.DiscoveredCapabilities` field, constructed in `NewServer` from the existing `conf`'s pool)
- Test: Create `internal/api/capabilities_test.go` (matrix tests; toggle/401 added in Task 3)

The handler joins three sources in Go: `s.disc.ListDiscoveredCapabilities` (Task 1), `s.conf.ListCapabilityPolicies` (`capability_policy.go:198`), and `caps.Declared()` (`caps.go:40`) for the capability axis so every declared capability appears. `FinalEnabled = DiscoveredAvail && effective`, where **absent policy ⇒ enabled** (the ly-xnk.3 default). We resolve effective policy per declared capability via `s.conf.EffectiveCapability` (`capability_policy.go:168`), passing the server-level database key (`""`); `found == false` ⇒ enabled. Auth is inherited from `withAuth` (`server.go:54`) — 401 when `DevAuth` off, exactly like `/audit`.

**Wiring the new store into `NewServer`.** `NewServer` (`server.go:31`) receives `conf *store.Config` but not the discovered-capability store. Construct `DiscoveredCapabilities` inside `capabilities.go` lazily is NOT possible (no pool handle on `Server`). Instead, add a field and build it in `NewServer` from a pool the server already owns. The simplest correct wiring without changing the `NewServer` signature: have the matrix handler use a `*store.DiscoveredCapabilities` stored on `Server`, set in `NewServer` — but `NewServer` has no pool. Therefore we extend the `Server` struct and set `disc` in `NewServer` by reusing the config pool. Since `Server` does not currently expose `conf`'s pool, we add the field and a one-line setter call in `NewServer` that constructs it from a pool passed in. To avoid changing `cmd/api/main.go`, we instead derive `disc` from `conf` via a new `store.Config` accessor is overkill; the minimal change is to widen `NewServer` is also avoided. **Chosen approach:** store the `*store.Config` we already have AND construct `DiscoveredCapabilities` from the same underlying pool by adding a tiny exported accessor on `Config`. See Step 3 for the exact, minimal edits.

- [ ] **Step 1: Write the failing test** — create `internal/api/capabilities_test.go` with the matrix join + default-enabled cases. Package `api_test`. We reuse `setupAudit` (`server_test.go:68`, applies config migrations and wires a server over the config pool) so both `capability_policy` and `discovered_capability` tables exist on the test pool. We seed `servers`, discovered rows (raw SQL), and a policy row (via `store.NewConfig(pool).SetCapabilityPolicy`).

```go
package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/api"
	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
)

// capabilityCell mirrors the JSON the matrix endpoint returns.
type capabilityCell struct {
	Capability       string `json:"capability"`
	DatabaseName     string `json:"database_name"`
	DiscoveredAvail  bool   `json:"discovered_available"`
	DiscoveredReason string `json:"discovered_reason"`
	PolicyEnabled    *bool  `json:"policy_enabled"`
	PolicySource     string `json:"policy_source"`
	FinalEnabled     bool   `json:"final_enabled"`
}
type capabilityMatrix struct {
	ServerID string           `json:"server_id"`
	Cells    []capabilityCell `json:"cells"`
}

func seedServer(t *testing.T, pool *pgxpool.Pool, id string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO servers (id, name) VALUES ($1, $1)`, id); err != nil {
		t.Fatalf("seed server: %v", err)
	}
}

func cellByCap(cells []capabilityCell, cap string) (capabilityCell, bool) {
	for _, c := range cells {
		if c.Capability == cap {
			return c, true
		}
	}
	return capabilityCell{}, false
}

func TestCapabilityMatrix_joinsDiscoveredAndPolicy(t *testing.T) {
	pool, srv := setupAudit(t, api.Config{DevAuth: true})
	seedServer(t, pool, "srv-1")
	ctx := context.Background()

	// pg_stat_statements: discovered=true, policy=disabled => final false.
	if _, err := pool.Exec(ctx,
		`INSERT INTO discovered_capability (server_id, database_name, capability, available, reason)
		 VALUES ('srv-1', NULL, 'pg_stat_statements', true, '1.10')`); err != nil {
		t.Fatalf("seed discovered: %v", err)
	}
	if _, err := store.NewConfig(pool).SetCapabilityPolicy(ctx, store.SetCapabilityPolicyInput{
		ServerID: "srv-1", Capability: "pg_stat_statements", Enabled: false, SetBy: "alice",
	}); err != nil {
		t.Fatalf("seed policy: %v", err)
	}
	// auto_explain: discovered=true, NO policy row => policy_enabled nil, final follows discovered (true).
	if _, err := pool.Exec(ctx,
		`INSERT INTO discovered_capability (server_id, database_name, capability, available, reason)
		 VALUES ('srv-1', NULL, 'auto_explain', true, '')`); err != nil {
		t.Fatalf("seed discovered #2: %v", err)
	}

	resp, err := http.Get(srv.URL + "/api/servers/srv-1/capabilities")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q, want application/json", ct)
	}

	var got capabilityMatrix
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.ServerID != "srv-1" {
		t.Errorf("server_id = %q, want srv-1", got.ServerID)
	}

	pss, ok := cellByCap(got.Cells, "pg_stat_statements")
	if !ok {
		t.Fatal("pg_stat_statements cell missing")
	}
	if !pss.DiscoveredAvail {
		t.Error("pg_stat_statements should be discovered-available")
	}
	if pss.PolicyEnabled == nil || *pss.PolicyEnabled {
		t.Errorf("pg_stat_statements policy_enabled = %v, want non-nil false", pss.PolicyEnabled)
	}
	if pss.PolicySource != string(store.PolicySourceServerDefault) {
		t.Errorf("pg_stat_statements policy_source = %q, want server-default", pss.PolicySource)
	}
	if pss.FinalEnabled {
		t.Error("pg_stat_statements final_enabled should be false (discovered && policy-off)")
	}

	ae, ok := cellByCap(got.Cells, "auto_explain")
	if !ok {
		t.Fatal("auto_explain cell missing")
	}
	if ae.PolicyEnabled != nil {
		t.Errorf("auto_explain policy_enabled = %v, want nil (no policy row)", ae.PolicyEnabled)
	}
	if ae.PolicySource != "" {
		t.Errorf("auto_explain policy_source = %q, want empty", ae.PolicySource)
	}
	if !ae.FinalEnabled {
		t.Error("auto_explain final_enabled should be true (discovered && absent policy => enabled)")
	}
}

func TestCapabilityMatrix_absentPolicyDefaultsEnabled(t *testing.T) {
	pool, srv := setupAudit(t, api.Config{DevAuth: true})
	seedServer(t, pool, "srv-1")
	// Discovered-available, no policy row anywhere.
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO discovered_capability (server_id, database_name, capability, available, reason)
		 VALUES ('srv-1', NULL, 'pg_buffercache', true, 'installed')`); err != nil {
		t.Fatalf("seed discovered: %v", err)
	}

	resp, err := http.Get(srv.URL + "/api/servers/srv-1/capabilities")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	var got capabilityMatrix
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	c, ok := cellByCap(got.Cells, "pg_buffercache")
	if !ok {
		t.Fatal("pg_buffercache cell missing")
	}
	if c.PolicyEnabled != nil {
		t.Errorf("policy_enabled = %v, want nil", c.PolicyEnabled)
	}
	if !c.FinalEnabled {
		t.Error("final_enabled should be true when discovered and no policy (default enabled)")
	}
}
```

- [ ] **Step 2: Run test to verify it fails** — the route/handler/struct field don't exist yet.

```
go test ./internal/api/ -run TestCapabilityMatrix 2>&1 | head -20
```

Expected: a build error such as `undefined: ...` is not produced because the test file only references existing symbols (`api.Config`, `store.*`); instead the GET returns 404 (no route registered) and `json.Decode` fails, so the test reports `FAIL` with e.g. `status = 404, want 200`. Either a compile error (if you reference a not-yet-added symbol) or this `FAIL` is the expected red state.

- [ ] **Step 3a: Wire the store into `Server`** — modify `internal/api/server.go`. Add a `disc` field to the struct and construct it in `NewServer`. `NewServer` only has `conf *store.Config`; add a minimal exported accessor `Config.Pool()` so `NewServer` can build `DiscoveredCapabilities` from the same pool without changing the `NewServer` signature or `cmd/api/main.go`.

In `internal/store/config.go`, after `WithReadPool` (`config.go:27-32`), add:

```go
// Pool returns the primary pool. Used by callers that need to construct
// a sibling store (e.g. DiscoveredCapabilities) over the same config DB
// connection without threading a second pool through their constructor.
func (c *Config) Pool() *pgxpool.Pool { return c.pool }
```

In `internal/api/server.go`, change the `Server` struct (`server.go:22-27`) from:

```go
type Server struct {
	cfg   Config
	stats *store.Stats
	conf  *store.Config
	mux   *http.ServeMux
}
```

to:

```go
type Server struct {
	cfg   Config
	stats *store.Stats
	conf  *store.Config
	disc  *store.DiscoveredCapabilities
	mux   *http.ServeMux
}
```

and change `NewServer` (`server.go:31-35`) from:

```go
func NewServer(cfg Config, stats *store.Stats, conf *store.Config) *Server {
	s := &Server{cfg: cfg, stats: stats, conf: conf, mux: http.NewServeMux()}
	s.routes()
	return s
}
```

to:

```go
func NewServer(cfg Config, stats *store.Stats, conf *store.Config) *Server {
	s := &Server{
		cfg:   cfg,
		stats: stats,
		conf:  conf,
		disc:  store.NewDiscoveredCapabilities(conf.Pool()),
		mux:   http.NewServeMux(),
	}
	s.routes()
	return s
}
```

Then add the GET route to `routes()` (`server.go:40-48`), after the existing audit lines:

```go
	s.mux.HandleFunc("GET /api/servers/{id}/capabilities", s.handleCapabilityMatrix)
```

- [ ] **Step 3b: Implement the matrix handler** — create `internal/api/capabilities.go`. The toggle handler is added in Task 3; this file now holds only the GET handler, DTOs, and `actorFromContext` (used by both). Import `internal/caps` for `Declared()` and `internal/store` for `EffectiveCapability`/`PolicySource`.

```go
package api

import (
	"encoding/json"
	"net/http"

	"github.com/dobbo-ca/lynceus/internal/caps"
	"github.com/dobbo-ca/lynceus/internal/store"
)

// capabilityCellDTO is one row of the capability matrix: the discovered
// availability of a capability crossed with operator policy and the
// resulting final-enabled decision. Every field is an enum capability
// string, a bounded package-authored reason, a boolean, or an
// operator-supplied database identifier — no monitored-DB literal.
type capabilityCellDTO struct {
	Capability       string `json:"capability"`
	DatabaseName     string `json:"database_name"`     // "" = server-wide
	DiscoveredAvail  bool   `json:"discovered_available"`
	DiscoveredReason string `json:"discovered_reason"` // bounded, package-authored
	PolicyEnabled    *bool  `json:"policy_enabled"`    // nil = no explicit policy row
	PolicySource     string `json:"policy_source"`     // "server-default"|"database-override"|""
	FinalEnabled     bool   `json:"final_enabled"`     // discovered && effective(default-enabled)
}

type capabilityMatrixDTO struct {
	ServerID string              `json:"server_id"`
	Cells    []capabilityCellDTO `json:"cells"`
}

// actorFromContext returns the principal to attribute audited writes to.
// Real OIDC actor wiring is the Milestone-5 follow-up; under DevAuth this
// is the constant dev-admin stub.
func actorFromContext(_ *http.Request) string { return "dev-admin" }

// handleCapabilityMatrix returns the discovered × policy × final-enabled
// matrix for one server. The capability axis is caps.Declared() so every
// declared capability appears even with no discovery or policy row.
// Absent policy => enabled (the effective-policy default).
func (s *Server) handleCapabilityMatrix(w http.ResponseWriter, r *http.Request) {
	serverID := r.PathValue("id")
	ctx := r.Context()

	discovered, err := s.disc.ListDiscoveredCapabilities(ctx, serverID)
	if err != nil {
		http.Error(w, "failed to load discovered capabilities", http.StatusInternalServerError)
		return
	}
	// Index the server-level discovered row (database_name == "") per
	// capability; the matrix here reports the server-level cell.
	discByCap := make(map[string]store.DiscoveredCapability, len(discovered))
	for _, d := range discovered {
		if d.DatabaseName == "" {
			discByCap[d.Capability] = d
		}
	}

	out := capabilityMatrixDTO{ServerID: serverID}
	for _, c := range caps.Declared() {
		capStr := string(c)
		cell := capabilityCellDTO{Capability: capStr}
		if d, ok := discByCap[capStr]; ok {
			cell.DiscoveredAvail = d.Available
			cell.DiscoveredReason = d.Reason
		}

		enabled, source, found, err := s.conf.EffectiveCapability(ctx, serverID, "", capStr)
		if err != nil {
			http.Error(w, "failed to resolve effective policy", http.StatusInternalServerError)
			return
		}
		effective := true // absent policy => enabled (ly-xnk.3 default)
		if found {
			eCopy := enabled
			cell.PolicyEnabled = &eCopy
			cell.PolicySource = string(source)
			effective = enabled
		}

		cell.FinalEnabled = cell.DiscoveredAvail && effective
		out.Cells = append(out.Cells, cell)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}
```

- [ ] **Step 4: Run test to verify it passes**

```
go test ./internal/api/ -run TestCapabilityMatrix -v 2>&1 | tail -20
```

Expected: `--- PASS: TestCapabilityMatrix_joinsDiscoveredAndPolicy`, `--- PASS: TestCapabilityMatrix_absentPolicyDefaultsEnabled`, and `ok  	github.com/dobbo-ca/lynceus/internal/api`. Also run `go build ./...` and expect no output (clean build) to confirm the `NewServer`/`Config.Pool()` edits compile against `cmd/api/main.go`.

- [ ] **Step 5: Commit**

```
git add internal/store/config.go internal/api/server.go internal/api/capabilities.go internal/api/capabilities_test.go
git commit -m "feat(api): capability matrix GET joining discovered x policy x declared (ly-xnk.4)"
```

---

### Task 3: Audited capability toggle POST handler

**Files:**
- Modify `internal/api/capabilities.go` (add `toggleRequestDTO` + `handleCapabilityToggle`)
- Modify `internal/api/server.go` (`routes()` — add the POST route)
- Test: Modify `internal/api/capabilities_test.go` (add toggle + 401 tests)

The POST reuses `SetCapabilityPolicy` (`capability_policy.go:42`) verbatim, which appends a tamper-evident audit row first via `AppendAuditReturning` (`config.go:158`) then upserts the policy row carrying `audit_chain_id` — so the toggle is audited for free with `action='capability_policy.set'`. The capability is read from `r.PathValue("cap")`, the body carries `{database, enabled, reason}`. Auth (401 without DevAuth) is inherited from `withAuth` (`server.go:54`); we mirror the existing 401 assertion (`audit_test.go:87`).

- [ ] **Step 1: Write the failing test** — append to `internal/api/capabilities_test.go`. Add `bytes`, `strings` imports at the top of the file (the matrix test currently imports `context`, `encoding/json`, `net/http`, `testing`, plus `api`/`store`/`pgxpool`).

```go
func TestCapabilityToggle_devAuth_writesPolicyAndAudit(t *testing.T) {
	pool, srv := setupAudit(t, api.Config{DevAuth: true})
	seedServer(t, pool, "srv-1")
	ctx := context.Background()

	body := strings.NewReader(`{"database":"","enabled":true,"reason":"operator enabled"}`)
	resp, err := http.Post(
		srv.URL+"/api/servers/srv-1/capabilities/pg_stat_statements",
		"application/json", body)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	// A capability_policy row exists, enabled, for the server-wide default.
	var enabled bool
	if err := pool.QueryRow(ctx,
		`SELECT enabled FROM capability_policy
		   WHERE server_id='srv-1' AND database_name IS NULL AND capability='pg_stat_statements'`,
	).Scan(&enabled); err != nil {
		t.Fatalf("policy row missing: %v", err)
	}
	if !enabled {
		t.Error("policy row should be enabled")
	}

	// Exactly one audit row for the toggle, with the dev-admin actor.
	var n int
	var actor string
	if err := pool.QueryRow(ctx,
		`SELECT count(*), COALESCE(max(actor),'') FROM audit_log
		   WHERE action='capability_policy.set' AND server_id='srv-1'`,
	).Scan(&n, &actor); err != nil {
		t.Fatalf("audit query: %v", err)
	}
	if n != 1 {
		t.Errorf("audit rows = %d, want 1", n)
	}
	if actor != "dev-admin" {
		t.Errorf("audit actor = %q, want dev-admin", actor)
	}
}

func TestCapabilityToggle_withoutDevAuth_returns401(t *testing.T) {
	pool, srv := setupAudit(t, api.Config{DevAuth: false})
	seedServer(t, pool, "srv-1")

	body := bytes.NewReader([]byte(`{"enabled":true}`))
	resp, err := http.Post(
		srv.URL+"/api/servers/srv-1/capabilities/pg_stat_statements",
		"application/json", body)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}

	// No policy row was written (request rejected before the handler).
	var n int
	_ = pool.QueryRow(context.Background(),
		`SELECT count(*) FROM capability_policy WHERE server_id='srv-1'`,
	).Scan(&n)
	if n != 0 {
		t.Errorf("policy rows = %d, want 0 (toggle blocked by auth)", n)
	}
}
```

Update the import block at the top of `internal/api/capabilities_test.go` to:

```go
import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/api"
	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
)
```

- [ ] **Step 2: Run test to verify it fails** — the POST route/handler don't exist.

```
go test ./internal/api/ -run TestCapabilityToggle 2>&1 | head -20
```

Expected: `TestCapabilityToggle_devAuth_writesPolicyAndAudit` FAILs with `status = 405, want 200` (the GET-only `/api/servers/{id}/capabilities` path does not match the `{cap}` sub-path, and no POST route exists, so `ServeMux` returns 404/405) — a clear red state. The 401 test may already pass (auth blocks before routing), which is expected.

- [ ] **Step 3a: Add the POST route** — modify `routes()` in `internal/api/server.go`, after the GET capabilities line added in Task 2:

```go
	s.mux.HandleFunc("POST /api/servers/{id}/capabilities/{cap}", s.handleCapabilityToggle)
```

- [ ] **Step 3b: Implement the toggle handler** — append to `internal/api/capabilities.go`. `SetCapabilityPolicy` validates `ServerID`/`Capability`/`SetBy` (`capability_policy.go:43-51`); `actorFromContext` supplies the non-empty `dev-admin`. Optionally reject unknown capabilities against `caps.Declared()`.

```go
type toggleRequestDTO struct {
	DatabaseName string `json:"database"` // "" => server-wide default
	Enabled      bool   `json:"enabled"`
	Reason       string `json:"reason"`
}

// handleCapabilityToggle sets one capability_policy row for a server (or a
// database within it) and records a tamper-evident audit entry, reusing
// store.SetCapabilityPolicy (which appends the audit row first, then
// upserts the policy carrying its audit_chain_id).
func (s *Server) handleCapabilityToggle(w http.ResponseWriter, r *http.Request) {
	serverID := r.PathValue("id")
	capability := r.PathValue("cap")

	if !isDeclaredCapability(capability) {
		http.Error(w, "unknown capability", http.StatusBadRequest)
		return
	}

	var req toggleRequestDTO
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	got, err := s.conf.SetCapabilityPolicy(r.Context(), store.SetCapabilityPolicyInput{
		ServerID:     serverID,
		DatabaseName: req.DatabaseName,
		Capability:   capability,
		Enabled:      req.Enabled,
		SetBy:        actorFromContext(r),
		Reason:       req.Reason,
	})
	if err != nil {
		http.Error(w, "failed to set capability policy", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		ServerID     string `json:"server_id"`
		DatabaseName string `json:"database_name"`
		Capability   string `json:"capability"`
		Enabled      bool   `json:"enabled"`
		AuditChainID int64  `json:"audit_chain_id"`
	}{
		ServerID:     got.ServerID,
		DatabaseName: got.DatabaseName,
		Capability:   got.Capability,
		Enabled:      got.Enabled,
		AuditChainID: got.AuditChainID,
	})
}

// isDeclaredCapability reports whether capability is one caps.Declared()
// knows about — rejecting typos before they create a policy row.
func isDeclaredCapability(capability string) bool {
	for _, c := range caps.Declared() {
		if string(c) == capability {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run test to verify it passes**

```
go test ./internal/api/ -run TestCapabilityToggle -v 2>&1 | tail -20
```

Expected: `--- PASS: TestCapabilityToggle_devAuth_writesPolicyAndAudit`, `--- PASS: TestCapabilityToggle_withoutDevAuth_returns401`, `ok  	github.com/dobbo-ca/lynceus/internal/api`. Then run the whole capabilities suite plus a build:

```
go test ./internal/api/ -run 'TestCapability' 2>&1 | tail -5
go build ./...
```

Expected: `ok  	github.com/dobbo-ca/lynceus/internal/api` and no build output.

- [ ] **Step 5: Commit**

```
git add internal/api/capabilities.go internal/api/server.go internal/api/capabilities_test.go
git commit -m "feat(api): audited capability toggle POST reusing SetCapabilityPolicy (ly-xnk.4)"
```

---

### Task 4: Full-suite verification

**Files:** none (verification only).

- [ ] **Step 1: Run the store + api packages green**

```
go test ./internal/store/ ./internal/api/ 2>&1 | tail -10
```

Expected: `ok  	github.com/dobbo-ca/lynceus/internal/store` and `ok  	github.com/dobbo-ca/lynceus/internal/api` (or `SKIP`-driven `ok` if Docker is unavailable; prefer a Docker-enabled run so the integration assertions actually execute).

- [ ] **Step 2: Build everything**

```
go build ./...
```

Expected: no output (exit 0). Confirms `Config.Pool()` and the widened `Server` struct compile against `cmd/api/main.go` and every other consumer of `store.NewConfig`/`api.NewServer`.

- [ ] **Step 3: Confirm no proto drift** — this plan adds no proto fields, so `make proto` must produce no diff and the privacy contract test must still pass unchanged.

```
make proto
git diff --stat -- internal/proto proto
go test ./internal/proto/...
```

Expected: `git diff --stat` prints nothing (no regenerated changes), and `ok  	github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1`. (No `assertOnlyAllowed` allowlist edit is required for ly-xnk.4 because it introduces no new T1 message or field — the matrix and policy snapshot travel as JSON, not over the wire, per spec §4.4.4.)

- [ ] **Step 4: No commit** — Task 4 only verifies; nothing to commit. If `make proto` produced an unexpected diff, STOP and investigate (it indicates an out-of-date generated file unrelated to this plan, not a change this plan should make).
