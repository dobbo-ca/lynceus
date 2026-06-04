# Read/Write DSN Split Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give `store.Config` and `store.Stats` a primary (RW) + optional replica (RO) pool pair so standalone reads can be served from a read replica, keeping load off the primary.

**Architecture:** Each store keeps its existing field `pool` as the primary (read-write) connection and gains an `ro` field for the read replica, defaulting to `pool` when no replica is attached. A `WithReadPool` builder attaches the replica. Standalone reads route to `ro`; writes, DDL/migrations, and the read-your-writes audit-chain transaction route to `pool`. `cmd/api` opts in via new optional `LYNCEUS_CONFIG_RO_DSN` / `LYNCEUS_STATS_RO_DSN` env vars.

**Tech Stack:** Go, pgx/v5 + pgxpool, testcontainers-go (real Postgres, no mocks).

**Spec:** `docs/superpowers/specs/2026-06-02-read-write-dsn-split-design.md` · **Issue:** ly-lt9

---

## Background / key facts

- **Routing rule (read-your-writes):** a read uses the primary only when it must observe a write from the same operation. Everything else reads the replica.
- **Routing table:**
  - Config → primary: `AppendAudit`/`AppendAuditReturning` (single tx; tail-hash read is read-your-writes), `SetCapabilityPolicy`, migrations. Config → replica: `ListAudit`, `VerifyChain`, `GetCapabilityPolicy`, `EffectiveCapability`, `ListCapabilityPolicies`.
  - Stats → primary: `WriteQueryStats`, `EnsureWeeklyPartition`, `DropPartitionsOlderThan` (incl. its `SELECT` — it is partition management, not a user read), migrations. Stats → replica: `TopQueriesByTotalTime`.
- **Keep field name `pool`** for the primary. Reads switch from `c.pool` / `s.pool` to `c.ro` / `s.ro`. This avoids breaking the concurrently-developed capability-policy writes (ly-xnk.2) that reference `c.pool`.
- **Test isolation:** an "empty replica" deterministically proves a read used `ro` and not `pool` — write to the primary, then assert the RO-routed read returns nothing. `newPool(t)` in `internal/store/store_test.go` starts one fresh `postgres:16` container; call it twice for primary + replica.
- **Current struct decls:** `type Config struct{ pool *pgxpool.Pool }` (config.go:15), `type Stats struct{ pool *pgxpool.Pool }` (stats.go:18).
- **`cmd/api/main.go`** already opens `pool` (stats) and `configPool` (config) and calls `api.NewServer(api.Config{...}, store.NewStats(pool), store.NewConfig(configPool))`.

## File Structure

- **Modify** `internal/store/config.go` — `Config` struct gets `ro` field; `NewConfig` sets both; add `WithReadPool`; `ListAudit` + `VerifyChain` read from `ro`.
- **Modify** `internal/store/capability_policy.go` — `GetCapabilityPolicy`, `EffectiveCapability`, `ListCapabilityPolicies` read from `ro`.
- **Modify** `internal/store/stats.go` — `Stats` struct gets `ro` field; `NewStats` sets both; add `WithReadPool`; `TopQueriesByTotalTime` reads from `ro`.
- **Create** `internal/store/pool_routing_test.go` — proves reads route to the replica and fall back to the primary when no replica is set, for both stores.
- **Modify** `cmd/api/main.go` — open optional RO pools and attach via `WithReadPool`.

---

## Task 1: Config — RW/RO pools + read routing

**Files:**
- Modify: `internal/store/config.go`
- Modify: `internal/store/capability_policy.go`
- Test: `internal/store/pool_routing_test.go` (create)

- [ ] **Step 1: Write the failing test**

Create `internal/store/pool_routing_test.go`:

```go
package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/dobbo-ca/lynceus/internal/store"
)

func TestConfig_ReadsRouteToReplica(t *testing.T) {
	primary := newPool(t)
	replica := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, primary); err != nil {
		t.Fatalf("migrate primary: %v", err)
	}
	if err := store.ApplyConfigMigrations(ctx, replica); err != nil {
		t.Fatalf("migrate replica: %v", err)
	}

	cfg := store.NewConfig(primary).WithReadPool(replica)

	// Write lands on the primary.
	if _, err := cfg.AppendAuditReturning(ctx, store.AuditEntry{Actor: "a", Action: "x"}); err != nil {
		t.Fatalf("append: %v", err)
	}

	// Read is served from the (empty) replica → sees nothing.
	got, err := cfg.ListAudit(ctx, store.AuditFilter{Limit: 100})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("read served from primary; want empty replica view, got %d rows", len(got))
	}

	// Sanity: the row really is on the primary.
	var n int
	if err := primary.QueryRow(ctx, `SELECT count(*) FROM audit_log`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("primary count = %d, want 1", n)
	}
}

func TestConfig_NoReplica_ReadsFromPrimary(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg := store.NewConfig(pool) // no WithReadPool → reads fall back to primary

	if _, err := cfg.AppendAuditReturning(ctx, store.AuditEntry{Actor: "a", Action: "x"}); err != nil {
		t.Fatalf("append: %v", err)
	}
	got, err := cfg.ListAudit(ctx, store.AuditFilter{Limit: 100})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("fallback read got %d rows, want 1", len(got))
	}
}

func TestStats_ReadsRouteToReplica(t *testing.T) {
	primary := newPool(t)
	replica := newPool(t)
	ctx := context.Background()
	if err := store.ApplyStatsMigrations(ctx, primary); err != nil {
		t.Fatalf("migrate primary: %v", err)
	}
	if err := store.ApplyStatsMigrations(ctx, replica); err != nil {
		t.Fatalf("migrate replica: %v", err)
	}

	s := store.NewStats(primary).WithReadPool(replica)
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	if err := s.WriteQueryStats(ctx, []store.QueryStat{
		{ServerID: "srv", CollectedAt: now, Fingerprint: "fp", NormalizedQuery: "SELECT 1", Calls: 1, TotalTimeMs: 1},
	}); err != nil {
		t.Fatalf("write: %v", err)
	}

	top, err := s.TopQueriesByTotalTime(ctx, now.Add(-time.Hour), now.Add(time.Hour), 10)
	if err != nil {
		t.Fatalf("top: %v", err)
	}
	if len(top) != 0 {
		t.Fatalf("read served from primary; want empty replica view, got %d rows", len(top))
	}
}

func TestStats_NoReplica_ReadsFromPrimary(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyStatsMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := store.NewStats(pool) // no WithReadPool
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	if err := s.WriteQueryStats(ctx, []store.QueryStat{
		{ServerID: "srv", CollectedAt: now, Fingerprint: "fp", NormalizedQuery: "SELECT 1", Calls: 1, TotalTimeMs: 1},
	}); err != nil {
		t.Fatalf("write: %v", err)
	}
	top, err := s.TopQueriesByTotalTime(ctx, now.Add(-time.Hour), now.Add(time.Hour), 10)
	if err != nil {
		t.Fatalf("top: %v", err)
	}
	if len(top) != 1 {
		t.Fatalf("fallback read got %d rows, want 1", len(top))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run 'TestConfig_Reads|TestConfig_NoReplica' 2>&1 | tail`
Expected: compile failure — `cfg.WithReadPool undefined`. (Stats cases also fail to compile; that's fine — Task 2 fixes them. They live in the same file so the package won't compile until both tasks land; verify per-task via the named `-run` filter, which still surfaces the compile error for the relevant symbols.)

- [ ] **Step 3: Add the `ro` field, constructor, and builder to Config**

In `internal/store/config.go`, replace the struct + constructor (lines 14–18):

```go
// Config is typed access to the config/metadata database.
type Config struct {
	pool *pgxpool.Pool // primary (read-write): writes + read-your-writes reads
	ro   *pgxpool.Pool // read replica; defaults to pool when not split
}

// NewConfig returns a Config bound to its primary pool. Standalone reads
// fall back to the primary until a replica is attached via WithReadPool.
func NewConfig(pool *pgxpool.Pool) *Config { return &Config{pool: pool, ro: pool} }

// WithReadPool attaches a read-replica pool used to serve standalone
// reads (ListAudit, VerifyChain, capability-policy getters). A nil ro is
// ignored. Returns the receiver for chaining.
func (c *Config) WithReadPool(ro *pgxpool.Pool) *Config {
	if ro != nil {
		c.ro = ro
	}
	return c
}
```

- [ ] **Step 4: Route Config reads to `ro`**

In `internal/store/config.go`, in `ListAudit`, change:

```go
	rows, err := c.pool.Query(ctx, q, args...)
```
to:
```go
	rows, err := c.ro.Query(ctx, q, args...)
```

In `VerifyChain`, change:

```go
	rows, err := c.pool.Query(ctx, q, args...)
```
to:
```go
	rows, err := c.ro.Query(ctx, q, args...)
```

(Leave `AppendAuditReturning`'s `c.pool.BeginTx` and its in-transaction `tx.QueryRow` untouched — that transaction stays on the primary.)

In `internal/store/capability_policy.go`, route the three read methods to `ro`:

- `GetCapabilityPolicy`: change both `row = c.pool.QueryRow(ctx,` occurrences to `row = c.ro.QueryRow(ctx,`.
- `EffectiveCapability`: change `row := c.pool.QueryRow(ctx,` to `row := c.ro.QueryRow(ctx,`.
- `ListCapabilityPolicies`: change `rows, err := c.pool.Query(ctx,` to `rows, err := c.ro.Query(ctx,`.

(Leave `SetCapabilityPolicy`'s `c.pool.QueryRow` upsert on the primary.)

- [ ] **Step 5: Run the Config tests to verify they pass**

Run: `go test ./internal/store/ -run 'TestConfig_Reads|TestConfig_NoReplica' -v 2>&1 | tail`
Expected: both PASS. (Self-skip if Docker is unavailable — run on a Docker host.)

- [ ] **Step 6: Commit**

```bash
git add internal/store/config.go internal/store/capability_policy.go internal/store/pool_routing_test.go
git commit -m "feat(store): RW/RO pool split for Config reads (ly-lt9)"
```

---

## Task 2: Stats — RW/RO pools + read routing

**Files:**
- Modify: `internal/store/stats.go`
- Test: `internal/store/pool_routing_test.go` (already created in Task 1)

- [ ] **Step 1: Confirm the Stats tests fail to compile**

Run: `go test ./internal/store/ -run 'TestStats_Reads|TestStats_NoReplica' 2>&1 | tail`
Expected: compile failure — `s.WithReadPool undefined`.

- [ ] **Step 2: Add the `ro` field, constructor, and builder to Stats**

In `internal/store/stats.go`, replace the struct + constructor (lines 18–21):

```go
type Stats struct {
	pool *pgxpool.Pool // primary (read-write): writes, DDL, migrations
	ro   *pgxpool.Pool // read replica; defaults to pool when not split
}

// NewStats returns a Stats bound to its primary pool. Standalone reads
// fall back to the primary until a replica is attached via WithReadPool.
func NewStats(pool *pgxpool.Pool) *Stats { return &Stats{pool: pool, ro: pool} }

// WithReadPool attaches a read-replica pool used to serve standalone
// reads (TopQueriesByTotalTime). A nil ro is ignored. Returns the
// receiver for chaining.
func (s *Stats) WithReadPool(ro *pgxpool.Pool) *Stats {
	if ro != nil {
		s.ro = ro
	}
	return s
}
```

- [ ] **Step 3: Route the Stats read to `ro`**

In `internal/store/stats.go`, in `TopQueriesByTotalTime`, change:

```go
	rows, err := s.pool.Query(ctx,
```
to:
```go
	rows, err := s.ro.Query(ctx,
```

(Leave `WriteQueryStats` (`s.pool.Begin`), `EnsureWeeklyPartition` (`s.pool.Exec`), and `DropPartitionsOlderThan` (both its `s.pool.Query` listing and `s.pool.Exec` drop) on the primary — they are writes / partition management.)

- [ ] **Step 4: Run the Stats tests to verify they pass**

Run: `go test ./internal/store/ -run 'TestStats_Reads|TestStats_NoReplica' -v 2>&1 | tail`
Expected: both PASS.

- [ ] **Step 5: Run the whole store package to confirm no regressions**

Run: `go test ./internal/store/ 2>&1 | tail`
Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/store/stats.go
git commit -m "feat(store): RW/RO pool split for Stats reads (ly-lt9)"
```

---

## Task 3: cmd/api — wire optional RO DSNs

**Files:**
- Modify: `cmd/api/main.go`

- [ ] **Step 1: Read optional RO DSNs**

In `cmd/api/main.go`, after the existing `configDSN` block, add:

```go
	statsRODSN := os.Getenv("LYNCEUS_STATS_RO_DSN")
	configRODSN := os.Getenv("LYNCEUS_CONFIG_RO_DSN")
```

- [ ] **Step 2: Open RO pools (when set) and attach them**

In `cmd/api/main.go`, replace the server-construction block:

```go
	configPool, err := pgxpool.New(ctx, configDSN)
	if err != nil {
		log.Fatalf("connect config db: %v", err)
	}
	defer configPool.Close()

	srv := api.NewServer(api.Config{DevAuth: devAuth},
		store.NewStats(pool), store.NewConfig(configPool))
```

with:

```go
	configPool, err := pgxpool.New(ctx, configDSN)
	if err != nil {
		log.Fatalf("connect config db: %v", err)
	}
	defer configPool.Close()

	statsRO := openReadPool(ctx, statsRODSN, "stats")
	defer closePool(statsRO)
	configRO := openReadPool(ctx, configRODSN, "config")
	defer closePool(configRO)

	srv := api.NewServer(api.Config{DevAuth: devAuth},
		store.NewStats(pool).WithReadPool(statsRO),
		store.NewConfig(configPool).WithReadPool(configRO))
```

- [ ] **Step 3: Add the helpers**

In `cmd/api/main.go`, add at the end of the file:

```go
// openReadPool opens a read-replica pool when dsn is non-empty; a fatal
// error is raised on a bad DSN so misconfiguration is caught at startup.
// Returns nil when dsn is empty, in which case the store falls back to
// its primary pool.
func openReadPool(ctx context.Context, dsn, name string) *pgxpool.Pool {
	if dsn == "" {
		return nil
	}
	p, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Fatalf("connect %s read replica: %v", name, err)
	}
	return p
}

// closePool closes a pool if it is non-nil.
func closePool(p *pgxpool.Pool) {
	if p != nil {
		p.Close()
	}
}
```

- [ ] **Step 4: Build**

Run: `go build ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/api/main.go
git commit -m "feat(api): wire optional RO replica DSNs for stats+config (ly-lt9)"
```

---

## Task 4: Full verification

- [ ] **Step 1: Build, vet, full test**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all PASS (integration tests self-skip only without Docker — run on a Docker host for a true green).

- [ ] **Step 2: Race check the store package**

Run: `go test -race ./internal/store/ 2>&1 | tail`
Expected: PASS, no race warnings.

- [ ] **Step 3: Commit anything outstanding**

```bash
git status
# commit any leftover with an appropriate message
```

---

## Acceptance criteria (from ly-lt9)

- [x] `store.Config` and `store.Stats` hold rw (`pool`) + ro (`ro`) pools — Tasks 1, 2.
- [x] Reads route to `ro`; writes/RMW/DDL/migrations route to `pool` — Tasks 1, 2 (per routing table).
- [x] `AppendAudit*` tail read stays on the primary — Task 1 (transaction untouched).
- [x] RO falls back to RW when unset — `NewConfig`/`NewStats` set `ro = pool`; `WithReadPool(nil)` is a no-op (Tasks 1, 2; fallback tests).
- [x] `cmd/api` wires `LYNCEUS_CONFIG_RO_DSN` + `LYNCEUS_STATS_RO_DSN` — Task 3.
- [x] Integration test proves a read is served from the RO pool + fallback test — Task 1 (`TestConfig_ReadsRouteToReplica`, `TestStats_ReadsRouteToReplica`, `*_NoReplica_ReadsFromPrimary`).
- [x] Full suite green — Task 4.

## Self-review notes

- **Spec coverage:** pool-pair (Tasks 1–2), routing table (Tasks 1–2 edits match the spec table exactly, incl. `DropPartitionsOlderThan` SELECT staying on primary and `VerifyChain` on replica), env vars + fallback (Task 3), tests incl. fallback (Task 1). pgbouncer note is documentation-only in the spec — no task, by design.
- **Placeholder scan:** none — all edits show exact before/after code and exact commands.
- **Type consistency:** `WithReadPool(ro *pgxpool.Pool)` returns the receiver on both `*Config` and `*Stats`; constructors set `ro = pool`; reads use `c.ro` / `s.ro` consistently. `openReadPool`/`closePool` signatures match their call sites in Task 3.
