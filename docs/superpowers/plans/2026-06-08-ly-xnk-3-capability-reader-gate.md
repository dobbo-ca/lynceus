# Capability Reader Gate Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a cheap, collector-local in-memory effective-policy `caps.Gate` (fail-open, refreshed on the full-snapshot ticker via a new `GET /policy-snapshot` endpoint) that every collector reader consults before issuing its query, so no row is built or shipped for a disabled capability.

**Architecture:** The collector has NO config-DB handle — it only connects to the monitored Postgres plus a websocket (`cmd/collector/main.go:24-33`). So the authoritative resolver `store.Config.EffectiveCapability` (`internal/store/capability_policy.go:168`) cannot be consulted per-read. Instead the api_server exposes a JSON `GET /api/servers/{id}/policy-snapshot` that the collector GETs on its full ticker and swaps into an in-memory `caps.Gate` (an `O(1)` map lookup under `RWMutex`). Readers call `gate.Allowed(db, cap)` and return `nil, nil` when disabled. Absent key ⇒ enabled (fail-open) so a fresh or unreachable collector never goes silently dark.

**Tech Stack:** Go, protobuf (`make proto`), pgx/pgxpool, templ+HTMX (where relevant), testcontainers (`postgres:16`) for integration tests.

**Bead:** ly-xnk.3  ·  **Spec:** docs/specs/2026-06-08-layer0-foundation.md  ·  **Layer:** 0 Foundation

---

## Scope notes & integration points

- **No proto change** (spec §4.4.4). The gate is enforced collector-side; the policy snapshot travels as a JSON GET, not over the wire. Therefore **no `contract_test.go` change is required by this bead.** (The contract-test gate is load-bearing for any T1 proto change — there is none here.)
- **Fail-open is a deliberate design decision** (spec §4.4.2 B2, §4.4.5): absent key ⇒ enabled. A fresh/unrefreshed collector keeps collecting until the first successful policy fetch.
- **Integration points with sibling beads:**
  - This plan adds `caps.SchemaInventory` + `caps.TableSize` constants to `caps.Declared()`. The NEW schema/table readers (ly-xqf.5 / ly-xqf.6) are **born with the gate** — each checks its OWN capability (`Inventory` → `caps.SchemaInventory`, `TableStatsReader` → `caps.TableSize`). Those readers are built in their own beads; this plan only lands the constants + the gate type + the retrofit onto the two EXISTING readers (`Reader`, `ActivityReader`). When ly-xqf.5/.6 land, their constructors take `gate *caps.Gate, db string` and call `r.gate.Allowed(r.db, caps.SchemaInventory)` / `caps.TableSize` exactly like Task 5/6 here. **Wiring those two new readers into the gate is their bead's responsibility; this plan adds Task 8 main-wiring only for `Reader`/`ActivityReader`** and leaves a documented seam.
  - `caps.Allowed` (Task 2) consumes `store.Config.EffectiveCapability` via the `PolicyResolver` interface — `*store.Config` already satisfies it (`capability_policy.go:168`).
  - The `/policy-snapshot` handler (Task 4) reuses `store.Config.ListCapabilityPolicies` (`capability_policy.go:198`) — no new store method.

## File Structure

| File | Action | Responsibility |
|---|---|---|
| `internal/caps/policy.go` | Create | `PolicyResolver` interface + `Allowed()` — server-side default-enabled resolver wrapping `store.EffectiveCapability`. |
| `internal/caps/policy_test.go` | Create | `TestAllowed_overrideBeatsDefault_andAbsentEnabled` — real-DB resolver test. |
| `internal/caps/gate.go` | Create | `Gate` (in-memory cached `Allowed`/`Replace`), `gateKey`, `NewGate`. |
| `internal/caps/gate_test.go` | Create | `TestGate_AbsentKeyFailsOpen`, `TestGate_ReplaceThenDisabled`, `TestGate_ConcurrentReadDuringReplace` (`-race`). |
| `internal/caps/caps.go` | Modify (`:25-35` const block, `:40-52` `Declared`) | Add `SchemaInventory`, `TableSize` capability constants. |
| `internal/caps/caps_test.go` | Modify (`:14-37` `TestDeclared_listsAllKnownCapabilities`) | Extend the pinned list with the two new constants. |
| `internal/api/capabilities.go` | Create | `handlePolicySnapshot` JSON GET handler + `policySnapshotEntry` DTO. |
| `internal/api/capabilities_test.go` | Create | `TestPolicySnapshot_returnsEnabledFlagsPerCapability`, `TestPolicySnapshot_withoutDevAuth_returns401`. |
| `internal/api/server.go` | Modify (`:40-48` `routes()`) | Register `GET /api/servers/{id}/policy-snapshot`. |
| `internal/collector/reader.go` | Modify (`:23-28` struct/ctor, `:33-34` `Read`) | Add `gate`/`db` fields; gate check before the query. |
| `internal/collector/activity_reader.go` | Modify (`:25-32` struct/ctor, `:36-37` `Read`) | Add `gate`/`db` fields; gate check before the query. |
| `internal/collector/gated_reader_test.go` | Create | `TestReader_gatedOff_returnsNoRows` — real-DB proof no query issued. |
| `internal/collector/policy_refresh.go` | Create | `FetchPolicySnapshot(ctx, baseURL, serverID) (map[caps.GateKey]bool, error)` HTTP client for the collector. |
| `cmd/collector/main.go` | Modify (`:24-33` setup, `:30-31` reader ctors, `:98` first run, `:101-119` tickers, config struct/loadConfig `:122-161`) | Build gate, resolve `db` via `current_database()`, pass into readers, refresh policy on `fullTicker`, kick one refresh before first `runFull`. |

---

## Tasks

### Task 1: Add `SchemaInventory` + `TableSize` capability constants

The gate keys on `caps.Capability`. The new schema/table readers (ly-xqf.5/.6) each gate on their own capability, so the constants must exist and appear in `Declared()` (so they show up in the matrix). This is the smallest, dependency-free first step.

**Files:**
- Modify: `internal/caps/caps.go` (const block `:25-35`, `Declared()` `:40-52`)
- Modify (Test): `internal/caps/caps_test.go` (`TestDeclared_listsAllKnownCapabilities` `:14-37`)

- [ ] **Step 1: Write the failing test** — extend the pinned `Declared()` list in `internal/caps/caps_test.go`. Replace the `want := []caps.Capability{...}` slice (lines 15-25) with:

```go
	want := []caps.Capability{
		caps.AutoExplain,
		caps.LogDestination,
		caps.PgBuffercache,
		caps.PgStatActivityFullRead,
		caps.PgStatStatements,
		caps.PgStatTuple,
		caps.PgWaitSampling,
		caps.RolePermissions,
		caps.SchemaInventory,
		caps.ServerVersion,
		caps.TableSize,
	}
```

- [ ] **Step 2: Run test to verify it fails** — the new constants don't exist yet, so this is a compile failure:

```
go test ./internal/caps/ -run TestDeclared_listsAllKnownCapabilities
```

Expected output contains:

```
internal/caps/caps_test.go:XX:8: undefined: caps.SchemaInventory
internal/caps/caps_test.go:XX:8: undefined: caps.TableSize
FAIL	github.com/dobbo-ca/lynceus/internal/caps [build failed]
```

- [ ] **Step 3: Implement** — in `internal/caps/caps.go`, add the two constants to the `const (...)` block (after `RolePermissions` at line 34, before the closing `)`):

```go
	RolePermissions        Capability = "role_permissions"
	// SchemaInventory gates the schema/object inventory reader (ly-xqf.5).
	// Catalog reads are always available, but the operator may disable
	// shipping inventory via capability policy.
	SchemaInventory Capability = "schema_inventory"
	// TableSize gates the per-table size/growth/TOAST reader (ly-xqf.6).
	TableSize Capability = "table_size"
```

Then add both to the `Declared()` return slice (after `RolePermissions,` at line 50):

```go
		RolePermissions,
		SchemaInventory,
		TableSize,
```

- [ ] **Step 4: Run test to verify it passes** —

```
go test ./internal/caps/ -run TestDeclared_listsAllKnownCapabilities
```

Expected output:

```
ok  	github.com/dobbo-ca/lynceus/internal/caps	0.0XXs
```

- [ ] **Step 5: Commit** —

```
git add internal/caps/caps.go internal/caps/caps_test.go
git commit -m "feat(caps): declare schema_inventory + table_size capabilities (ly-xnk.3)"
```

---

### Task 2: Server-side `caps.Allowed` resolver (default-enabled semantics)

`store.Config.EffectiveCapability` returns `found=false` when no policy row exists and lets the caller pick the absent-policy default. `caps.Allowed` encodes ly-xnk.3's fail-open rule (`absent ⇒ enabled`) over a `PolicyResolver` interface so it is unit-testable and decoupled from `*store.Config`.

**Files:**
- Create: `internal/caps/policy.go`
- Create (Test): `internal/caps/policy_test.go`

- [ ] **Step 1: Write the failing test** — create `internal/caps/policy_test.go`. This is a real-DB test mirroring `internal/store/capability_policy_test.go:191-251` (`TestEffectiveCapability_overrideBeatsDefault`) seeding pattern. It lives in the `store_test`-style package but in `internal/caps`, so it imports `store` to seed and constructs `*store.Config` (which satisfies `PolicyResolver`):

```go
package caps_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/dobbo-ca/lynceus/internal/caps"
	"github.com/dobbo-ca/lynceus/internal/store"
)

// newConfigPool starts a postgres:16 container, applies config migrations,
// and returns a pool. Mirrors internal/api/server_test.go:21-47 + 71.
func newConfigPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	c, err := tcpostgres.Run(ctx,
		"postgres:16",
		tcpostgres.WithDatabase("lynceus_test"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Skipf("docker/testcontainers unavailable: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(c) })
	url, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate config: %v", err)
	}
	return pool
}

func TestAllowed_overrideBeatsDefault_andAbsentEnabled(t *testing.T) {
	pool := newConfigPool(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx,
		`INSERT INTO servers (id, name) VALUES ('srv-1', 'srv one')`,
	); err != nil {
		t.Fatalf("seed server: %v", err)
	}
	cfg := store.NewConfig(pool)

	// Absent policy => fail-open enabled.
	ok, err := caps.Allowed(ctx, cfg, "srv-1", "appdb", caps.PgStatStatements)
	if err != nil {
		t.Fatalf("allowed (absent): %v", err)
	}
	if !ok {
		t.Fatal("absent policy must default to ENABLED (fail-open)")
	}

	// Server-wide default: disabled.
	if _, err := cfg.SetCapabilityPolicy(ctx, store.SetCapabilityPolicyInput{
		ServerID: "srv-1", Capability: "pg_stat_statements",
		Enabled: false, SetBy: "alice",
	}); err != nil {
		t.Fatalf("set default: %v", err)
	}
	ok, err = caps.Allowed(ctx, cfg, "srv-1", "appdb", caps.PgStatStatements)
	if err != nil {
		t.Fatalf("allowed (default): %v", err)
	}
	if ok {
		t.Fatal("server-default disabled must yield false")
	}

	// DB override re-enables for appdb. Override wins.
	if _, err := cfg.SetCapabilityPolicy(ctx, store.SetCapabilityPolicyInput{
		ServerID: "srv-1", DatabaseName: "appdb", Capability: "pg_stat_statements",
		Enabled: true, SetBy: "bob",
	}); err != nil {
		t.Fatalf("set override: %v", err)
	}
	ok, err = caps.Allowed(ctx, cfg, "srv-1", "appdb", caps.PgStatStatements)
	if err != nil {
		t.Fatalf("allowed (override): %v", err)
	}
	if !ok {
		t.Fatal("db override enabled must win over disabled default")
	}
}
```

- [ ] **Step 2: Run test to verify it fails** — `caps.Allowed` doesn't exist:

```
go test ./internal/caps/ -run TestAllowed_overrideBeatsDefault_andAbsentEnabled
```

Expected output contains:

```
internal/caps/policy_test.go:XX: undefined: caps.Allowed
FAIL	github.com/dobbo-ca/lynceus/internal/caps [build failed]
```

- [ ] **Step 3: Implement** — create `internal/caps/policy.go`:

```go
package caps

import (
	"context"

	"github.com/dobbo-ca/lynceus/internal/store"
)

// PolicyResolver is the subset of *store.Config that Allowed needs. It
// is satisfied by *store.Config (capability_policy.go:168).
type PolicyResolver interface {
	EffectiveCapability(ctx context.Context, serverID, databaseName, capability string) (enabled bool, source store.PolicySource, found bool, err error)
}

// Allowed resolves whether a capability is enabled for (serverID, db)
// using ly-xnk.3 default-enabled semantics:
//
//	per-db override  ??  server-wide default  ??  ENABLED (fail-open)
//
// Absent policy returns true so a freshly-provisioned server collects
// until an operator deliberately disables a capability.
func Allowed(ctx context.Context, r PolicyResolver, serverID, db string, c Capability) (bool, error) {
	enabled, _, found, err := r.EffectiveCapability(ctx, serverID, db, string(c))
	if err != nil {
		return false, err
	}
	if !found {
		return true, nil // absent policy => enabled
	}
	return enabled, nil
}
```

- [ ] **Step 4: Run test to verify it passes** —

```
go test ./internal/caps/ -run TestAllowed_overrideBeatsDefault_andAbsentEnabled
```

Expected output (or `ok ... [no tests to run]`-free PASS; Docker required):

```
ok  	github.com/dobbo-ca/lynceus/internal/caps	X.XXs
```

If Docker is unavailable the test self-skips (`t.Skipf`), which still exits 0.

- [ ] **Step 5: Commit** —

```
git add internal/caps/policy.go internal/caps/policy_test.go
git commit -m "feat(caps): Allowed resolver with fail-open default-enabled semantics (ly-xnk.3)"
```

---

### Task 3: Collector-local cached `caps.Gate`

This is the cheap path readers call. `Allowed` is an `O(1)` map lookup under `RLock` with zero I/O; `Replace` atomically swaps a freshly-fetched snapshot (called on `fullTicker`, never per read). Absent key ⇒ true (fail-open). `GateKey` is exported because the collector's HTTP refresh client (Task 7) and `cmd/collector/main.go` build the snapshot map.

**Files:**
- Create: `internal/caps/gate.go`
- Create (Test): `internal/caps/gate_test.go`

- [ ] **Step 1: Write the failing test** — create `internal/caps/gate_test.go`. Pure in-memory, designed to be run under `-race`:

```go
package caps_test

import (
	"sync"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/caps"
)

func TestGate_AbsentKeyFailsOpen(t *testing.T) {
	g := caps.NewGate()
	// Never Replaced: every lookup defaults to enabled.
	if !g.Allowed("appdb", caps.PgStatStatements) {
		t.Fatal("fresh gate must fail OPEN (absent key => enabled)")
	}
	// After a Replace that omits this key, it still fails open.
	g.Replace(map[caps.GateKey]bool{
		{Db: "appdb", Cap: caps.PgStatActivityFullRead}: false,
	})
	if !g.Allowed("appdb", caps.PgStatStatements) {
		t.Fatal("key absent from snapshot must fail OPEN")
	}
}

func TestGate_ReplaceThenDisabled(t *testing.T) {
	g := caps.NewGate()
	g.Replace(map[caps.GateKey]bool{
		{Db: "appdb", Cap: caps.PgStatStatements}:  false,
		{Db: "appdb", Cap: caps.SchemaInventory}:   true,
		{Db: "otherdb", Cap: caps.PgStatStatements}: true,
	})
	if g.Allowed("appdb", caps.PgStatStatements) {
		t.Error("appdb pg_stat_statements explicitly disabled, want false")
	}
	if !g.Allowed("appdb", caps.SchemaInventory) {
		t.Error("appdb schema_inventory explicitly enabled, want true")
	}
	// Per-db scoping: otherdb is enabled for the same capability.
	if !g.Allowed("otherdb", caps.PgStatStatements) {
		t.Error("otherdb pg_stat_statements enabled, want true")
	}
}

func TestGate_ConcurrentReadDuringReplace(t *testing.T) {
	g := caps.NewGate()
	g.Replace(map[caps.GateKey]bool{
		{Db: "appdb", Cap: caps.PgStatStatements}: true,
	})
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); _ = g.Allowed("appdb", caps.PgStatStatements) }()
		go func() {
			defer wg.Done()
			g.Replace(map[caps.GateKey]bool{
				{Db: "appdb", Cap: caps.PgStatStatements}: false,
			})
		}()
	}
	wg.Wait()
}
```

- [ ] **Step 2: Run test to verify it fails** — `Gate`/`GateKey`/`NewGate` don't exist:

```
go test ./internal/caps/ -run TestGate
```

Expected output contains:

```
internal/caps/gate_test.go:XX: undefined: caps.NewGate
internal/caps/gate_test.go:XX: undefined: caps.GateKey
FAIL	github.com/dobbo-ca/lynceus/internal/caps [build failed]
```

- [ ] **Step 3: Implement** — create `internal/caps/gate.go`:

```go
package caps

import "sync"

// GateKey identifies one effective-policy decision by (database,
// capability). The collector connects to a single database, so db is the
// connection's current_database(); per-database overrides for OTHER
// databases on the same server are not honored by a single connection
// (documented limitation, spec §4.4.2 B4).
type GateKey struct {
	Db  string
	Cap Capability
}

// Gate is a collector-local in-memory snapshot of effective capability
// policy. Allowed is an O(1) map lookup under RLock with zero I/O —
// readers call it before every query. Replace atomically swaps a freshly
// fetched snapshot and is called on the full-snapshot ticker, never per
// read. The collector has no config-DB handle (spec §4.4.0), so the
// authoritative resolver runs server-side and reaches the collector via
// GET /policy-snapshot.
type Gate struct {
	mu      sync.RWMutex
	enabled map[GateKey]bool
}

// NewGate returns an empty Gate. With no snapshot loaded, every Allowed
// returns true (fail-open) so a collector is never silently dark before
// its first successful policy fetch.
func NewGate() *Gate {
	return &Gate{enabled: make(map[GateKey]bool)}
}

// Allowed reports whether capability c is enabled for database db. An
// absent key returns true (fail-open / default-enabled).
func (g *Gate) Allowed(db string, c Capability) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	enabled, ok := g.enabled[GateKey{Db: db, Cap: c}]
	if !ok {
		return true // absent => enabled (fail-open)
	}
	return enabled
}

// Replace atomically installs a new snapshot. A nil snap clears the gate
// (back to all-enabled).
func (g *Gate) Replace(snap map[GateKey]bool) {
	if snap == nil {
		snap = make(map[GateKey]bool)
	}
	g.mu.Lock()
	g.enabled = snap
	g.mu.Unlock()
}
```

- [ ] **Step 4: Run test to verify it passes (under -race)** —

```
go test -race ./internal/caps/ -run TestGate
```

Expected output:

```
ok  	github.com/dobbo-ca/lynceus/internal/caps	X.XXs
```

(No `DATA RACE` lines.)

- [ ] **Step 5: Commit** —

```
git add internal/caps/gate.go internal/caps/gate_test.go
git commit -m "feat(caps): in-memory fail-open Gate with atomic Replace (ly-xnk.3)"
```

---

### Task 4: `GET /api/servers/{id}/policy-snapshot` endpoint

The collector has no config DB, so the api_server exposes the effective policy as JSON. The handler reads `ListCapabilityPolicies` (`capability_policy.go:198`) and returns one entry per stored policy row: `{capability, database_name, enabled}`. Only enum capability strings, the operator-supplied `database_name`, and booleans — no monitored-DB-derived data, literal-free by construction.

**Files:**
- Create: `internal/api/capabilities.go`
- Modify: `internal/api/server.go` (`routes()` `:40-48`)
- Create (Test): `internal/api/capabilities_test.go`

- [ ] **Step 1: Write the failing test** — create `internal/api/capabilities_test.go`. Uses the `setupAudit` helper (`server_test.go:68-78`) which applies config migrations, plus direct seeding mirroring `capability_policy_test.go:191-219`:

```go
package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/api"
	"github.com/dobbo-ca/lynceus/internal/store"
)

func TestPolicySnapshot_returnsEnabledFlagsPerCapability(t *testing.T) {
	pool, srv := setupAudit(t, api.Config{DevAuth: true})
	ctx := context.Background()

	if _, err := pool.Exec(ctx,
		`INSERT INTO servers (id, name) VALUES ('srv-1', 'srv one')`,
	); err != nil {
		t.Fatalf("seed server: %v", err)
	}
	cfg := store.NewConfig(pool)
	// Server-wide default disabled for pg_stat_statements.
	if _, err := cfg.SetCapabilityPolicy(ctx, store.SetCapabilityPolicyInput{
		ServerID: "srv-1", Capability: "pg_stat_statements",
		Enabled: false, SetBy: "alice",
	}); err != nil {
		t.Fatalf("set default: %v", err)
	}
	// Per-db override enabling schema_inventory on appdb.
	if _, err := cfg.SetCapabilityPolicy(ctx, store.SetCapabilityPolicyInput{
		ServerID: "srv-1", DatabaseName: "appdb", Capability: "schema_inventory",
		Enabled: true, SetBy: "bob",
	}); err != nil {
		t.Fatalf("set override: %v", err)
	}

	resp, err := http.Get(srv.URL + "/api/servers/srv-1/policy-snapshot")
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

	var got []struct {
		Capability   string `json:"capability"`
		DatabaseName string `json:"database_name"`
		Enabled      bool   `json:"enabled"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("entries = %d, want 2 (%+v)", len(got), got)
	}

	type key struct {
		cap, db string
		on      bool
	}
	seen := map[key]bool{}
	for _, e := range got {
		seen[key{e.Capability, e.DatabaseName, e.Enabled}] = true
	}
	if !seen[key{"pg_stat_statements", "", false}] {
		t.Errorf("missing server-wide pg_stat_statements=false; got %+v", got)
	}
	if !seen[key{"schema_inventory", "appdb", true}] {
		t.Errorf("missing appdb schema_inventory=true; got %+v", got)
	}
}

func TestPolicySnapshot_withoutDevAuth_returns401(t *testing.T) {
	_, srv := setupAudit(t, api.Config{DevAuth: false})
	resp, err := http.Get(srv.URL + "/api/servers/srv-1/policy-snapshot")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}
```

- [ ] **Step 2: Run test to verify it fails** — the route and handler don't exist; the GET returns 404 (or the test won't compile if the file references a missing symbol — it does not, so expect a runtime 404):

```
go test ./internal/api/ -run TestPolicySnapshot
```

Expected output contains:

```
--- FAIL: TestPolicySnapshot_returnsEnabledFlagsPerCapability
    capabilities_test.go:XX: status = 404, want 200
FAIL	github.com/dobbo-ca/lynceus/internal/api	X.XXs
```

(`TestPolicySnapshot_withoutDevAuth_returns401` passes incidentally because the unauth middleware 401s before routing — that is correct.)

- [ ] **Step 3: Implement** — create `internal/api/capabilities.go`:

```go
package api

import (
	"encoding/json"
	"net/http"
)

// policySnapshotEntry is one effective-policy fact shipped to the
// collector: a closed-vocabulary capability string, the operator-supplied
// database_name ("" = server-wide default), and a boolean. No
// monitored-database-derived data — literal-free by construction.
type policySnapshotEntry struct {
	Capability   string `json:"capability"`
	DatabaseName string `json:"database_name"`
	Enabled      bool   `json:"enabled"`
}

// handlePolicySnapshot returns every stored capability_policy row for the
// server as JSON. The collector GETs this on its full-snapshot ticker and
// swaps it into its in-memory caps.Gate (the collector has no config-DB
// handle — spec §4.4.0). Absent rows are NOT enumerated here; the gate
// fails open on any key it doesn't find.
func (s *Server) handlePolicySnapshot(w http.ResponseWriter, r *http.Request) {
	serverID := r.PathValue("id")
	rows, err := s.conf.ListCapabilityPolicies(r.Context(), serverID)
	if err != nil {
		http.Error(w, "list capability policies", http.StatusInternalServerError)
		return
	}
	out := make([]policySnapshotEntry, 0, len(rows))
	for _, p := range rows {
		out = append(out, policySnapshotEntry{
			Capability:   p.Capability,
			DatabaseName: p.DatabaseName,
			Enabled:      p.Enabled,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}
```

Then register the route in `internal/api/server.go` `routes()` — add after the audit-partial line (`:44`):

```go
	s.mux.HandleFunc("GET /api/servers/{id}/policy-snapshot", s.handlePolicySnapshot)
```

- [ ] **Step 4: Run test to verify it passes** —

```
go test ./internal/api/ -run TestPolicySnapshot
```

Expected output:

```
ok  	github.com/dobbo-ca/lynceus/internal/api	X.XXs
```

- [ ] **Step 5: Commit** —

```
git add internal/api/capabilities.go internal/api/capabilities_test.go internal/api/server.go
git commit -m "feat(api): GET /policy-snapshot endpoint for collector gate refresh (ly-xnk.3)"
```

---

### Task 5: Retrofit the gate into `Reader` (pg_stat_statements)

`Reader.Read` must consult the gate before issuing its query, returning `nil, nil` when `caps.PgStatStatements` is disabled for the connection's database. The struct gains `gate *caps.Gate` and `db string`; the constructor signature changes. (`cmd/collector/main.go` is updated in Task 8 — the build breaks between Task 5 and Task 8 only at the `main` package, which is fine because tests target `internal/collector`.)

**Files:**
- Modify: `internal/collector/reader.go` (struct `:23-25`, ctor `:28`, `Read` `:33-34`)
- Create (Test): `internal/collector/gated_reader_test.go`

- [ ] **Step 1: Write the failing test** — create `internal/collector/gated_reader_test.go`. Real Postgres with `pg_stat_statements`, mirroring `reader_test.go:21-72` setup. It proves the gate (not a query error) suppresses output: with the gate disabled the reader returns `nil, nil` even though rows exist; flipped on, rows return.

```go
package collector_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/dobbo-ca/lynceus/internal/caps"
	"github.com/dobbo-ca/lynceus/internal/collector"
)

func TestReader_gatedOff_returnsNoRows(t *testing.T) {
	ctx := context.Background()
	c, err := tcpostgres.Run(ctx,
		"postgres:16",
		tcpostgres.WithDatabase("appdb"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		testcontainers.WithCmd("postgres", "-c", "shared_preload_libraries=pg_stat_statements"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Skipf("docker/testcontainers unavailable: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(c) })

	url, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	for _, stmt := range []string{
		`CREATE EXTENSION IF NOT EXISTS pg_stat_statements`,
		`CREATE TABLE users (id INT PRIMARY KEY)`,
		`SELECT pg_stat_statements_reset()`,
		`SELECT id FROM users WHERE id = 1`,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("setup %q: %v", stmt, err)
		}
	}

	gate := caps.NewGate()
	// Disable pg_stat_statements for appdb.
	gate.Replace(map[caps.GateKey]bool{
		{Db: "appdb", Cap: caps.PgStatStatements}: false,
	})

	r := collector.NewReader(pool, gate, "appdb")
	stats, err := r.Read(ctx)
	if err != nil {
		t.Fatalf("read (gated off): %v", err)
	}
	if stats != nil {
		t.Fatalf("gated-off reader returned %d rows, want nil", len(stats))
	}

	// Flip on: rows now return (proves the gate, not a query failure,
	// suppressed output).
	gate.Replace(map[caps.GateKey]bool{
		{Db: "appdb", Cap: caps.PgStatStatements}: true,
	})
	stats, err = r.Read(ctx)
	if err != nil {
		t.Fatalf("read (gated on): %v", err)
	}
	if len(stats) == 0 {
		t.Fatal("gated-on reader returned no rows, want >0")
	}
}
```

- [ ] **Step 2: Run test to verify it fails** — `NewReader` still takes one arg:

```
go test ./internal/collector/ -run TestReader_gatedOff_returnsNoRows
```

Expected output contains:

```
internal/collector/gated_reader_test.go:XX: too many arguments in call to collector.NewReader
	have (*pgxpool.Pool, *caps.Gate, string)
	want (*pgxpool.Pool)
FAIL	github.com/dobbo-ca/lynceus/internal/collector [build failed]
```

- [ ] **Step 3: Implement** — edit `internal/collector/reader.go`. Add the `caps` import to the import block (after the `normalize` import line 18):

```go
	"github.com/dobbo-ca/lynceus/internal/caps"
	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
	"github.com/dobbo-ca/lynceus/internal/normalize"
```

Replace the struct + constructor (lines 23-28):

```go
// Reader queries pg_stat_statements on a monitored Postgres instance
// and returns T1 (normalized) query statistics. Read is gated: when the
// pg_stat_statements capability is disabled for the connection's
// database, Read issues no query and returns no rows.
type Reader struct {
	pool *pgxpool.Pool
	gate *caps.Gate
	db   string // current_database() of pool, used as the gate key
}

// NewReader returns a Reader bound to pool. gate is consulted before
// every Read; db is the connection's current_database() (the gate key).
func NewReader(pool *pgxpool.Pool, gate *caps.Gate, db string) *Reader {
	return &Reader{pool: pool, gate: gate, db: db}
}
```

Add the gate check at the top of `Read` — insert before the existing `rows, err := r.pool.Query(...)` (line 34):

```go
func (r *Reader) Read(ctx context.Context) ([]*lynceusv1.QueryStat, error) {
	if !r.gate.Allowed(r.db, caps.PgStatStatements) {
		return nil, nil // capability disabled: build & ship nothing
	}
	rows, err := r.pool.Query(ctx,
```

- [ ] **Step 4: Run test to verify it passes** — also re-run the existing reader test to confirm the new arg didn't break it (it will fail to compile because `reader_test.go:74` calls `NewReader(pool)`; fix that single call site too):

First update `internal/collector/reader_test.go` line 74 from `r := collector.NewReader(pool)` to:

```go
	r := collector.NewReader(pool, caps.NewGate(), "lynceus_target")
```

and add `"github.com/dobbo-ca/lynceus/internal/caps"` to that file's import block. Then:

```
go test ./internal/collector/ -run 'TestReader_gatedOff_returnsNoRows|TestReader_returnsNormalizedQueriesWithNoLiterals'
```

Expected output:

```
ok  	github.com/dobbo-ca/lynceus/internal/collector	X.XXs
```

- [ ] **Step 5: Commit** —

```
git add internal/collector/reader.go internal/collector/reader_test.go internal/collector/gated_reader_test.go
git commit -m "feat(collector): gate pg_stat_statements Reader on capability policy (ly-xnk.3)"
```

---

### Task 6: Retrofit the gate into `ActivityReader` (pg_stat_activity)

`ActivityReader.Read` gates on `caps.PgStatActivityFullRead`. The reader groups per-`datname`, but a single connection can't pre-filter other databases' rows, so the gate is checked once with the connection's own database — DB-level activity policy is effectively server-scoped here (documented limitation, spec §4.4.2 B4).

**Files:**
- Modify: `internal/collector/activity_reader.go` (struct `:25-32`, `Read` `:36-37`)
- Modify (Test): existing `ActivityReader` tests' constructor call sites (see Step 4).

- [ ] **Step 1: Write the failing test** — extend `internal/collector/gated_reader_test.go` (created in Task 5) with an activity-gate test. Append:

```go
func TestActivityReader_gatedOff_returnsNoRows(t *testing.T) {
	ctx := context.Background()
	c, err := tcpostgres.Run(ctx,
		"postgres:16",
		tcpostgres.WithDatabase("appdb"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Skipf("docker/testcontainers unavailable: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(c) })

	url, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	gate := caps.NewGate()
	gate.Replace(map[caps.GateKey]bool{
		{Db: "appdb", Cap: caps.PgStatActivityFullRead}: false,
	})

	r := collector.NewActivityReader(pool, gate, "appdb")
	samples, err := r.Read(ctx)
	if err != nil {
		t.Fatalf("read (gated off): %v", err)
	}
	if samples != nil {
		t.Fatalf("gated-off activity reader returned %d samples, want nil", len(samples))
	}

	gate.Replace(map[caps.GateKey]bool{
		{Db: "appdb", Cap: caps.PgStatActivityFullRead}: true,
	})
	samples, err = r.Read(ctx)
	if err != nil {
		t.Fatalf("read (gated on): %v", err)
	}
	// At least this connection's own client backend is visible.
	if len(samples) == 0 {
		t.Fatal("gated-on activity reader returned no samples, want >0")
	}
}
```

- [ ] **Step 2: Run test to verify it fails** — `NewActivityReader` still takes one arg:

```
go test ./internal/collector/ -run TestActivityReader_gatedOff_returnsNoRows
```

Expected output contains:

```
internal/collector/gated_reader_test.go:XX: too many arguments in call to collector.NewActivityReader
	have (*pgxpool.Pool, *caps.Gate, string)
	want (*pgxpool.Pool)
FAIL	github.com/dobbo-ca/lynceus/internal/collector [build failed]
```

- [ ] **Step 3: Implement** — edit `internal/collector/activity_reader.go`. Add the import block alongside `pgxpool` (the file currently imports only `context`, `fmt`, `pgxpool` at lines 4-9):

```go
import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dobbo-ca/lynceus/internal/caps"
)
```

Replace the struct + constructor (lines 25-32):

```go
type ActivityReader struct {
	pool *pgxpool.Pool
	gate *caps.Gate
	db   string // current_database() of pool, used as the gate key
}

// NewActivityReader returns a reader bound to pool. gate is consulted
// before every Read; db is the connection's current_database().
func NewActivityReader(pool *pgxpool.Pool, gate *caps.Gate, db string) *ActivityReader {
	return &ActivityReader{pool: pool, gate: gate, db: db}
}
```

Add the gate check at the top of `Read` — insert before `rows, err := r.pool.Query(...)` (line 37):

```go
func (r *ActivityReader) Read(ctx context.Context) ([]ActivitySample, error) {
	// One connection cannot pre-filter other databases' pg_stat_activity
	// rows, so the activity capability is gated once with the connection's
	// own database — effectively server-scoped (spec §4.4.2 B4).
	if !r.gate.Allowed(r.db, caps.PgStatActivityFullRead) {
		return nil, nil // capability disabled: build & ship nothing
	}
	rows, err := r.pool.Query(ctx,
```

- [ ] **Step 4: Run test to verify it passes** — first fix any existing `NewActivityReader(pool)` call sites in collector tests. Find them:

```
grep -rn 'NewActivityReader(' internal/collector/
```

For each test call site (e.g. an `activity_reader_test.go`), change `collector.NewActivityReader(pool)` to `collector.NewActivityReader(pool, caps.NewGate(), "<that test's db name>")` and add the `caps` import. Then:

```
go test ./internal/collector/ -run 'TestActivityReader'
```

Expected output:

```
ok  	github.com/dobbo-ca/lynceus/internal/collector	X.XXs
```

- [ ] **Step 5: Commit** —

```
git add internal/collector/activity_reader.go internal/collector/gated_reader_test.go internal/collector/activity_reader_test.go
git commit -m "feat(collector): gate pg_stat_activity reader on capability policy (ly-xnk.3)"
```

---

### Task 7: Collector policy-snapshot HTTP client

The collector fetches `/policy-snapshot` and turns the JSON into the `map[caps.GateKey]bool` that `Gate.Replace` consumes. It maps the server's `{capability, database_name, enabled}` entries to gate keys: the gate keys on the collector's connection `db`, so a server-wide entry (`database_name == ""`) and a db-override entry for the collector's own db both apply — the override taking precedence, matching `EffectiveCapability` semantics. This is the collector-side mirror of the server resolver.

**Files:**
- Create: `internal/collector/policy_refresh.go`
- Create (Test): `internal/collector/policy_refresh_test.go`

- [ ] **Step 1: Write the failing test** — create `internal/collector/policy_refresh_test.go`. Pure HTTP test against an `httptest.Server` returning a canned snapshot (no Postgres needed):

```go
package collector_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/caps"
	"github.com/dobbo-ca/lynceus/internal/collector"
)

func TestFetchPolicySnapshot_mapsServerDefaultAndOverride(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/servers/srv-1/policy-snapshot" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"capability":"pg_stat_statements","database_name":"","enabled":false},
			{"capability":"pg_stat_statements","database_name":"appdb","enabled":true},
			{"capability":"schema_inventory","database_name":"otherdb","enabled":false}
		]`))
	}))
	defer srv.Close()

	snap, err := collector.FetchPolicySnapshot(context.Background(), srv.URL, "srv-1", "appdb")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}

	// Server-wide pg_stat_statements is false, but appdb override re-enables it.
	if got, ok := snap[caps.GateKey{Db: "appdb", Cap: caps.PgStatStatements}]; !ok || got != true {
		t.Errorf("appdb pg_stat_statements = (%v,%v), want (true,true) — override beats default", got, ok)
	}
	// schema_inventory override is for otherdb, NOT the collector's appdb,
	// so it must NOT enter the snapshot keyed on appdb.
	if _, ok := snap[caps.GateKey{Db: "appdb", Cap: caps.SchemaInventory}]; ok {
		t.Error("otherdb override leaked into appdb gate key")
	}
}

func TestFetchPolicySnapshot_serverDefaultOnly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"capability":"pg_stat_statements","database_name":"","enabled":false}]`))
	}))
	defer srv.Close()

	snap, err := collector.FetchPolicySnapshot(context.Background(), srv.URL, "srv-1", "appdb")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if got := snap[caps.GateKey{Db: "appdb", Cap: caps.PgStatStatements}]; got != false {
		t.Errorf("appdb pg_stat_statements = %v, want false (server default applies)", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails** — `FetchPolicySnapshot` doesn't exist:

```
go test ./internal/collector/ -run TestFetchPolicySnapshot
```

Expected output contains:

```
internal/collector/policy_refresh_test.go:XX: undefined: collector.FetchPolicySnapshot
FAIL	github.com/dobbo-ca/lynceus/internal/collector [build failed]
```

- [ ] **Step 3: Implement** — create `internal/collector/policy_refresh.go`:

```go
package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/dobbo-ca/lynceus/internal/caps"
)

// policySnapshotEntry mirrors the api server's JSON shape
// (internal/api/capabilities.go).
type policySnapshotEntry struct {
	Capability   string `json:"capability"`
	DatabaseName string `json:"database_name"`
	Enabled      bool   `json:"enabled"`
}

// FetchPolicySnapshot GETs the effective capability policy for serverID
// from the api server and resolves it for the collector's own database
// db into a map[caps.GateKey]bool suitable for caps.Gate.Replace.
//
// Resolution mirrors store.EffectiveCapability: a db-specific override
// for db wins over the server-wide default ("" database_name). Entries
// scoped to a DIFFERENT database are ignored — the collector connects to
// one database and cannot honor another's per-db policy.
func FetchPolicySnapshot(ctx context.Context, baseURL, serverID, db string) (map[caps.GateKey]bool, error) {
	url := fmt.Sprintf("%s/api/servers/%s/policy-snapshot", baseURL, serverID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build policy-snapshot request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get policy-snapshot: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("policy-snapshot status %d", resp.StatusCode)
	}

	var entries []policySnapshotEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return nil, fmt.Errorf("decode policy-snapshot: %w", err)
	}

	// Two passes so an override always wins regardless of JSON order:
	// first apply server-wide defaults, then overlay this db's overrides.
	out := make(map[caps.GateKey]bool)
	for _, e := range entries {
		if e.DatabaseName == "" {
			out[caps.GateKey{Db: db, Cap: caps.Capability(e.Capability)}] = e.Enabled
		}
	}
	for _, e := range entries {
		if e.DatabaseName == db {
			out[caps.GateKey{Db: db, Cap: caps.Capability(e.Capability)}] = e.Enabled
		}
	}
	return out, nil
}
```

- [ ] **Step 4: Run test to verify it passes** —

```
go test ./internal/collector/ -run TestFetchPolicySnapshot
```

Expected output:

```
ok  	github.com/dobbo-ca/lynceus/internal/collector	X.XXs
```

- [ ] **Step 5: Commit** —

```
git add internal/collector/policy_refresh.go internal/collector/policy_refresh_test.go
git commit -m "feat(collector): policy-snapshot HTTP client resolving override-beats-default (ly-xnk.3)"
```

---

### Task 8: Wire the gate into `cmd/collector/main.go`

Build the gate, resolve the connection's `db` once via `SELECT current_database()`, pass `gate`/`db` into both reader constructors, add a `refreshPolicy` closure on `fullTicker`, and kick one refresh before the first `runFull()` so the very first snapshot already respects policy. The api base URL for `/policy-snapshot` comes from a new config var. This is the step that restores `main` to a buildable state after Tasks 5-6 changed the constructor signatures.

**Files:**
- Modify: `cmd/collector/main.go` (imports `:13-15`, setup `:24-33`, first run `:98`, tickers `:101-119`, `config` struct `:122-130`, `loadConfig` `:132-161`)

- [ ] **Step 1: Write the failing test** — there is no unit test for `cmd/collector/main.go` (it is wiring). The verification is `go build ./...`. Confirm it currently FAILS because Tasks 5-6 changed the constructor arity:

```
go build ./cmd/collector/
```

Expected output contains:

```
cmd/collector/main.go:30:XX: not enough arguments in call to collector.NewReader
	have (*pgxpool.Pool)
	want (*pgxpool.Pool, *caps.Gate, string)
cmd/collector/main.go:31:XX: not enough arguments in call to collector.NewActivityReader
	have (*pgxpool.Pool)
	want (*pgxpool.Pool, *caps.Gate, string)
```

- [ ] **Step 2: Run build to verify it fails** — (same command, documented as the red gate):

```
go build ./cmd/collector/
```

Expected: non-zero exit with the two "not enough arguments" errors above.

- [ ] **Step 3: Implement** — edit `cmd/collector/main.go`.

Add `caps` and `collector` is already imported; add the `caps` import to the import block (after the existing `collector` import line 13):

```go
	"github.com/dobbo-ca/lynceus/internal/caps"
	"github.com/dobbo-ca/lynceus/internal/collector"
	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
```

Replace the setup block (lines 24-33) — after the `pool` is built, resolve `db`, build the gate, and pass both into the readers:

```go
	pool, err := pgxpool.New(ctx, cfg.pgDSN)
	if err != nil {
		log.Fatalf("connect monitored postgres: %v", err)
	}
	defer pool.Close()

	// Resolve the monitored connection's database once — it is the gate key.
	var db string
	if err := pool.QueryRow(ctx, `SELECT current_database()`).Scan(&db); err != nil {
		log.Fatalf("resolve current_database: %v", err)
	}

	gate := caps.NewGate()
	reader := collector.NewReader(pool, gate, db)
	activityReader := collector.NewActivityReader(pool, gate, db)
	aggregator := collector.NewActivityAggregator(cfg.serverID, cfg.activityFlush)
	shipper := collector.NewShipper(cfg.ingestURL, cfg.token)

	// refreshPolicy fetches effective capability policy from the api server
	// and atomically swaps it into the gate. The collector has no config-DB
	// handle (spec §4.4.0), so policy reaches it only via this HTTP fetch.
	// A failure logs and leaves the previous snapshot in place — fail-open
	// means an unreachable api keeps collecting, never goes silently dark.
	refreshPolicy := func() {
		if cfg.apiBaseURL == "" {
			return // not configured: gate stays empty => all-enabled
		}
		snap, err := collector.FetchPolicySnapshot(ctx, cfg.apiBaseURL, cfg.serverID, db)
		if err != nil {
			log.Printf("refresh policy snapshot: %v", err)
			return
		}
		gate.Replace(snap)
		log.Printf("refreshed policy snapshot: %d entries", len(snap))
	}
```

Update the "kick off one of each immediately" block (lines 97-99) so policy is refreshed BEFORE the first full snapshot:

```go
	// Kick off one of each immediately. Refresh policy first so the very
	// first full snapshot already respects capability policy.
	refreshPolicy()
	runFull()
	sampleActivity()
```

Add the refresh onto the `fullTicker` arm (lines 112-113):

```go
		case <-fullTicker.C:
			refreshPolicy()
			runFull()
```

Add `apiBaseURL` to the `config` struct (after `token` at line 126):

```go
	token            string
	apiBaseURL       string        // LYNCEUS_API_BASE_URL; "" disables policy fetch (gate stays all-enabled)
```

Add the env read in `loadConfig` (in the struct literal after `token:` at line 137):

```go
		token:            os.Getenv("LYNCEUS_COLLECTOR_TOKEN"),
		apiBaseURL:       os.Getenv("LYNCEUS_API_BASE_URL"),
```

(`apiBaseURL` is intentionally NOT in the required-var gate at line 157 — an empty value leaves the gate all-enabled, preserving today's behavior for deployments that haven't wired policy yet.)

- [ ] **Step 4: Run build to verify it passes** —

```
go build ./...
```

Expected output: clean (no errors, exit 0).

- [ ] **Step 5: Commit** —

```
git add cmd/collector/main.go
git commit -m "feat(collector): wire caps.Gate + policy refresh into collector main (ly-xnk.3)"
```

---

### Task 9: Full verification sweep

Confirm the whole bead is green end-to-end and nothing regressed. No new code — this is the final gate before handoff.

**Files:** none (verification only).

- [ ] **Step 1: Build everything** —

```
go build ./...
```

Expected: clean, exit 0.

- [ ] **Step 2: Run the caps + collector + api packages under -race** —

```
go test -race ./internal/caps/ ./internal/collector/ ./internal/api/
```

Expected output (all three lines `ok`; integration tests self-skip if Docker is unavailable, which still exits 0):

```
ok  	github.com/dobbo-ca/lynceus/internal/caps	X.XXs
ok  	github.com/dobbo-ca/lynceus/internal/collector	X.XXs
ok  	github.com/dobbo-ca/lynceus/internal/api	X.XXs
```

- [ ] **Step 3: Confirm the contract test is still green (no proto change, but prove no regression)** —

```
go test ./internal/proto/...
```

Expected output:

```
ok  	github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1	0.0XXs
```

- [ ] **Step 4: Full suite** —

```
go test ./...
```

Expected: all packages `ok` (or `[no test files]`); no `FAIL`. Integration suites self-skip when Docker is unavailable.

- [ ] **Step 5: Close the bead / hand off** — record completion against the bead and report status (do not commit/push beyond the per-task commits above unless instructed):

```
bd update ly-xnk.3 --status done
```

If `bd` is not the active workflow at execution time, instead report: all nine tasks committed, `go build ./...` clean, `go test -race ./internal/caps/ ./internal/collector/ ./internal/api/` green, contract test green, no proto change. Note for ly-xqf.5/.6 owners: their new readers must take `(pool, gate, db)` and call `r.gate.Allowed(r.db, caps.SchemaInventory)` / `caps.TableSize`, and `cmd/collector/main.go` must pass the shared `gate`/`db` into their constructors when those readers are wired into `runFull` (the seam is already present).
