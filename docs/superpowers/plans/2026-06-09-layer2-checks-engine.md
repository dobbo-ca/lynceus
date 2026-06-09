# Layer 2 — Checks/Alerts Engine + TXID/MultiXact Wraparound Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the keystone Checks/Alerts engine (`ly-u4t.20`) — a server-side scheduler that periodically evaluates pluggable check predicates over the latest per-server stats, persists severity-tagged results to a new `checks_results` table, honors capability gating + muting, and dispatches notifications through a seam — then land its first real bundle, the critical-safety TXID/MultiXact **wraparound** check (`ly-u4t.26`), which also delivers the VACUUM Advisor's deferred "Freezing" view.

**Architecture:** A pure `internal/checks` engine mirrors the existing `internal/insight` registry shape (`Check` interface + `Severity` enum + `Result` + `Run`). A `checks.Scheduler` runs **inside the ingestion server** (the write-side service that owns the stats writer pool + migrations), evaluates on a ticker under a Postgres advisory lock (so only one replica acts), and persists to a new partitioned `checks_results` table. The api-side `/checks` page reads the latest results (mirroring the `/vacuum-advisor` handler+templ pattern). Wraparound needs freeze-age data the store does not yet carry, so it adds a collector `FreezeAgeReader` → new literal-free T1 `FreezeAge` proto message → `freeze_ages` store table → a `WraparoundCheck` registered in the engine, plus a Freezing view on the VACUUM advisor.

**Tech Stack:** Go, pgx/v5 (`CopyFrom`, advisory locks), protobuf (`protoc-gen-go` v1.36.11), templ v0.3.1020 + HTMX, testcontainers-go (real Postgres, never mocked), native declarative range-partitioned vanilla Postgres (RDS-safe, no extensions).

**Privacy invariant (every task):** No literal-capable field may be added to any T1 proto message — the contract test (`internal/proto/lynceus/v1/contract_test.go`) enforces it. Freeze ages and check fields are integer counts / package-authored bounded strings / identifiers only. Allowlist each new field explicitly.

**Architecture invariant (re-verify after Part B):** collector stays outbound-only — exactly ONE `pgxpool.New` in `cmd/collector`, ZERO in `internal/collector`, zero `internal/store`/config-DB imports in `internal/collector`.

---

## Conventions (from the codebase exploration)

- **New partitioned table** = a new `internal/store/migrations/stats/000N_<name>.sql` file (lexically ordered, embedded glob auto-picks it up; no Go registration). `PARTITION BY RANGE (<ts_col>)`, a BRIN index on the time column, a btree on `(server_id, <entity>, <ts>)`, and `data_tier SMALLINT NOT NULL DEFAULT 1`. Highest existing = `0007_log_events.sql` → next free numbers are `0008`, `0009`, `0010`.
- **Writer** = `internal/store/<name>.go`: a `…Row` struct, a `…Columns []string` matching DDL order, `Write…` (early-return empty → ensure weekly partition per distinct week → `pgx.CopyFromSlice` → `s.pool.CopyFrom`), `Ensure…WeeklyPartition` (reuse `isoWeekBounds`/`…PartitionName`), reads on `s.ro` filtered `AND data_tier = 1`. Coerce `DataTier == 0 → 1`.
- **Pure engine** = mirror `internal/insight/insight.go`: string `Severity`, a `Check` interface, a package `registry` + `Register`, a `Run(in, checks) []Result`.
- **api page** = mirror `internal/api/vacuum_advisor.go` (`handleXxxPage`/`handleXxxPartial`/`fetchXxx`) + `web/<name>.templ` (`XxxPage`/`XxxTable` + `XxxRow` view-model) + two `routes()` registrations + a `web/layout.templ` nav link. Run `make templ` after templ edits.
- **proto** = edit `proto/lynceus/v1/snapshot.proto`, `make proto`, generated Go lands in `internal/proto/lynceus/v1/`.
- **Commands:** build `go build ./...`; targeted test `go test ./internal/<pkg>/... -timeout 5m`; full suite (end of plan) `go test ./... -p 1 -timeout 20m` (parallel testcontainers contend → use `-p 1`); arch grep (after Part B):
  `grep -rn "pgxpool.New" cmd/collector | grep -v _test` (expect 1), `grep -rn "pgxpool.New" internal/collector | grep -v _test` (expect 0), `grep -rn "internal/store" internal/collector | grep -v _test` (expect 0).

---

## File Structure

**Part A — Checks framework (`ly-u4t.20`):**
- Create `internal/checks/checks.go` — pure engine: `Severity`, `Status`, `Result`, `Check` interface, `registry`/`Register`, `DefaultChecks`, `Run`, `Input`.
- Create `internal/checks/checks_test.go` — engine unit tests with fake checks.
- Create `internal/checks/scheduler.go` — `Scheduler` (ticker + advisory lock + assemble Input + persist + notify), `Notifier` interface + `NopNotifier`.
- Create `internal/checks/scheduler_test.go` — `RunOnce` integration test (testcontainers).
- Create `internal/store/migrations/stats/0008_checks_results.sql` — `checks_results` table.
- Create `internal/store/migrations/stats/0009_check_mutes.sql` — `check_mutes` table (non-partitioned, stats DB).
- Create `internal/store/checks_results.go` — `ChecksResultRow`, `WriteChecksResults`, `EnsureChecksResultsWeeklyPartition`, `LatestChecksResults`; mute reads/writes `IsMuted`/`ListMutes`/`SetMute`/`ClearMute`.
- Create `internal/store/checks_results_test.go` — store round-trip tests.
- Create `internal/store/servers.go` — `RecentServerIDs(ctx, since) ([]string, error)`.
- Modify `cmd/ingestion/main.go` — start `checks.Scheduler` goroutine.
- Create `internal/api/checks.go` — `/checks` handler/partial/fetch.
- Create `internal/api/checks_test.go` — handler render test.
- Create `web/checks.templ` (+ generated `web/checks_templ.go`) — `ChecksRow`, `ChecksPage`, `ChecksTable`.
- Modify `internal/api/server.go` — register `/checks` + `/partial/checks`.
- Modify `web/layout.templ` (+ regen) — nav link.

**Part B — Wraparound check (`ly-u4t.26`) + Freezing view:**
- Modify `proto/lynceus/v1/snapshot.proto` — `FreezeAge` message + `repeated FreezeAge freeze_ages = 9` on `Snapshot` (regen `snapshot.pb.go`).
- Modify `internal/proto/lynceus/v1/contract_test.go` — `TestFreezeAgeHasOnlyAggregateFields` + add `freeze_ages` to Snapshot allowlist.
- Modify `internal/caps/caps.go` — `FreezeAge Capability = "freeze_age"` const + `Declared()`.
- Create `internal/collector/freeze_age_reader.go` (+ test) — gated reader: per-DB `age(datfrozenxid)`/`mxid_age(datminmxid)` + per-table `age(relfrozenxid)`/`mxid_age(relminmxid)`.
- Modify `cmd/collector/main.go` — construct + call reader in `runFull`, add `FreezeAges:` to the Snapshot literal.
- Create `internal/store/migrations/stats/0010_freeze_ages.sql` — `freeze_ages` table.
- Create `internal/store/freeze_ages.go` (+ test) — `FreezeAgeRow`, `WriteFreezeAges`, `EnsureFreezeAgesWeeklyPartition`, `LatestFreezeAges`.
- Modify `internal/ingest/server.go` — `snapshotToFreezeAges` mapper + `WriteFreezeAges` routing block.
- Create `internal/checks/wraparound.go` (+ test) — `WraparoundCheck` registered in `DefaultChecks`.
- Modify `internal/checks/scheduler.go` — assemble `FreezeAges` into `Input`.
- Modify `internal/advisor/vacuum.go` (+ test) — add `CatFreezing` from freeze-age input.
- Modify `internal/api/vacuum_advisor.go` — feed freeze ages into the advisor input.

---

## PART A — Checks Engine Framework (`ly-u4t.20`)

### Task A1: Pure engine types + registry + Run

**Files:**
- Create: `internal/checks/checks.go`
- Test: `internal/checks/checks_test.go`

- [ ] **Step 1: Write the failing test** (`internal/checks/checks_test.go`)

```go
package checks

import "testing"

// fakeCheck emits one result iff trigger is true.
type fakeCheck struct {
	id      string
	cat     string
	trigger bool
	sev     Severity
}

func (f fakeCheck) ID() string       { return f.id }
func (f fakeCheck) Category() string { return f.cat }
func (f fakeCheck) Eval(in Input) []Result {
	if !f.trigger {
		return nil
	}
	return []Result{{
		CheckID: f.id, Category: f.cat, Severity: f.sev,
		Status: StatusFiring, Object: "rel1", Detail: "boom",
	}}
}

func TestRunCollectsFiringResultsAndStampsServerTime(t *testing.T) {
	in := Input{ServerID: "srv-a"}
	got := Run(in, []Check{
		fakeCheck{id: "c.fire", cat: "queries", trigger: true, sev: SeverityCritical},
		fakeCheck{id: "c.quiet", cat: "queries", trigger: false},
	})
	if len(got) != 1 {
		t.Fatalf("want 1 result, got %d", len(got))
	}
	r := got[0]
	if r.CheckID != "c.fire" || r.Severity != SeverityCritical || r.Status != StatusFiring {
		t.Fatalf("unexpected result: %+v", r)
	}
	if r.ServerID != "srv-a" {
		t.Fatalf("Run must stamp ServerID from Input, got %q", r.ServerID)
	}
}

func TestSeverityRankOrders(t *testing.T) {
	if !(SeverityCritical.rank() > SeverityWarning.rank() &&
		SeverityWarning.rank() > SeverityInfo.rank()) {
		t.Fatal("rank order must be critical > warning > info")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/checks/... -run TestRun -timeout 2m`
Expected: FAIL — `undefined: Input/Run/Check/...`.

- [ ] **Step 3: Write minimal implementation** (`internal/checks/checks.go`)

```go
// Package checks is the Lynceus Checks/Alerts engine. It runs pure check
// predicates over the latest per-server stats and emits severity-tagged
// Results. Like internal/insight, it is I/O-free and deterministic: the
// scheduler (scheduler.go) does the store reads and persistence. Every
// Result field is a count, a fixed enum, an identifier, or a bounded
// package-authored string — never a literal from the monitored database,
// preserving the T1 privacy contract.
package checks

import "time"

// Severity is the alert level of a check result.
type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

func (s Severity) rank() int {
	switch s {
	case SeverityCritical:
		return 3
	case SeverityWarning:
		return 2
	case SeverityInfo:
		return 1
	default:
		return 0
	}
}

// Status is whether a check is currently firing.
type Status string

const (
	StatusOK     Status = "ok"
	StatusFiring Status = "firing"
)

// Input is the per-server snapshot a check evaluates. The scheduler
// assembles it from store reads. Fields are added as bundles need them
// (Part B adds FreezeAges). now is injected for deterministic tests.
type Input struct {
	ServerID   string
	Now        time.Time
	TableStats []TableInfo
	FreezeAges []FreezeInfo // populated in Part B (wraparound)
}

// TableInfo is the check-local projection of store.TableStatRow.
type TableInfo struct {
	Relation         string
	LiveTuples       int64
	DeadTuples       int64
	NModSinceAnalyze int64
	SeqScan          int64
	IdxScan          int64
}

// FreezeInfo is the check-local projection of store.FreezeAgeRow (Part B).
type FreezeInfo struct {
	Scope        string // "database" or "table"
	Relation     string // fqn for tables; database name for db scope
	XIDAge       int64  // age(relfrozenxid) / age(datfrozenxid)
	MXIDAge      int64  // mxid_age(relminmxid) / mxid_age(datminmxid)
	AutovacuumFreezeMaxAge int64 // server setting (count)
}

// Result is one firing check observation. Object is an identifier label
// (relation / database / fingerprint), never a literal value.
type Result struct {
	ServerID string
	CheckID  string
	Category string
	Severity Severity
	Status   Status
	Object   string
	Detail   string
}

// Check is one pluggable predicate. Bundles implement it and Register.
type Check interface {
	ID() string
	Category() string
	Eval(in Input) []Result
}

var registry []Check

// Register adds a check to the default set. Called from bundle init().
func Register(c Check) { registry = append(registry, c) }

// DefaultChecks returns the registered checks (copy).
func DefaultChecks() []Check {
	out := make([]Check, len(registry))
	copy(out, registry)
	return out
}

// Run evaluates every check against in and returns all firing results,
// each stamped with in.ServerID.
func Run(in Input, checks []Check) []Result {
	var out []Result
	for _, c := range checks {
		for _, r := range c.Eval(in) {
			r.ServerID = in.ServerID
			out = append(out, r)
		}
	}
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/checks/... -timeout 2m`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/checks/checks.go internal/checks/checks_test.go
git commit -m "feat(checks): pure engine — Check/Severity/Result/registry/Run (ly-u4t.20)"
```

---

### Task A2: `checks_results` + `check_mutes` schema + store writer/reader

**Files:**
- Create: `internal/store/migrations/stats/0008_checks_results.sql`
- Create: `internal/store/migrations/stats/0009_check_mutes.sql`
- Create: `internal/store/checks_results.go`
- Create: `internal/store/servers.go`
- Test: `internal/store/checks_results_test.go`

- [ ] **Step 1: Write the DDL** (`0008_checks_results.sql`)

```sql
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
```

- [ ] **Step 2: Write the DDL** (`0009_check_mutes.sql`)

```sql
-- check_mutes: operator-set suppression of a (server_id, check_id[, object]).
-- Co-located in the stats DB so the ingestion scheduler needs only its
-- stats pool. Small operational table — not partitioned. A mute with
-- object='' applies to every object of that check on that server.
CREATE TABLE check_mutes (
    server_id   TEXT        NOT NULL,
    check_id    TEXT        NOT NULL,
    object      TEXT        NOT NULL DEFAULT '',
    muted_until TIMESTAMPTZ NOT NULL,
    reason      TEXT        NOT NULL DEFAULT '',
    PRIMARY KEY (server_id, check_id, object)
);
```

- [ ] **Step 3: Write the failing store test** (`internal/store/checks_results_test.go`)

Mirror the existing `table_stats_test.go` harness (testcontainers Postgres + `ApplyStatsMigrations`). Use the package's existing test helper for spinning up the stats DB (look at `table_stats_test.go` top — reuse the same `newTestStats(t)`/`startStatsPG(t)` helper that file uses; do not invent a new one).

```go
func TestWriteAndReadChecksResults(t *testing.T) {
	ctx := context.Background()
	s := newTestStats(t) // same helper table_stats_test.go uses
	at := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	rows := []ChecksResultRow{{
		ServerID: "srv-a", EvaluatedAt: at, CheckID: "vacuum.wraparound",
		Category: "vacuum", Severity: "critical", Status: "firing",
		Object: "public.orders", Detail: "xid age 1.6e9 of 2e9",
	}}
	if err := s.WriteChecksResults(ctx, rows); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := s.LatestChecksResults(ctx, "srv-a", at.Add(-time.Hour), at.Add(time.Hour))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 1 || got[0].CheckID != "vacuum.wraparound" || got[0].Severity != "critical" {
		t.Fatalf("unexpected: %+v", got)
	}
}

func TestMuteRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := newTestStats(t)
	until := time.Now().Add(time.Hour)
	if err := s.SetMute(ctx, "srv-a", "vacuum.wraparound", "", until, "planned maintenance"); err != nil {
		t.Fatalf("set: %v", err)
	}
	muted, err := s.ListMutes(ctx, "srv-a")
	if err != nil || len(muted) != 1 {
		t.Fatalf("list: %v %+v", err, muted)
	}
}
```

- [ ] **Step 4: Run test to verify it fails**

Run: `go test ./internal/store/... -run 'TestWriteAndReadChecksResults|TestMuteRoundTrip' -timeout 8m`
Expected: FAIL — `undefined: ChecksResultRow/WriteChecksResults/...`.

- [ ] **Step 5: Implement** (`internal/store/checks_results.go`)

```go
package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// ChecksResultRow is one persisted Checks engine result. DataTier zero is
// coerced to 1 (T1) on insert.
type ChecksResultRow struct {
	ServerID    string
	EvaluatedAt time.Time
	CheckID     string
	Category    string
	Severity    string
	Status      string
	Object      string
	Detail      string
	Muted       bool
	DataTier    int16
}

var checksResultsColumns = []string{
	"server_id", "evaluated_at", "check_id", "category", "severity",
	"status", "object", "detail", "muted", "data_tier",
}

// WriteChecksResults bulk-inserts results, creating weekly partitions as
// needed. Empty input is a no-op.
func (s *Stats) WriteChecksResults(ctx context.Context, rows []ChecksResultRow) error {
	if len(rows) == 0 {
		return nil
	}
	weeks := map[string]time.Time{}
	for _, r := range rows {
		weeks[checksResultsPartitionName(r.EvaluatedAt)] = r.EvaluatedAt
	}
	for _, ts := range weeks {
		if err := s.EnsureChecksResultsWeeklyPartition(ctx, ts); err != nil {
			return err
		}
	}
	src := pgx.CopyFromSlice(len(rows), func(i int) ([]any, error) {
		r := rows[i]
		if r.DataTier == 0 {
			r.DataTier = 1
		}
		return []any{
			r.ServerID, r.EvaluatedAt, r.CheckID, r.Category, r.Severity,
			r.Status, r.Object, r.Detail, r.Muted, r.DataTier,
		}, nil
	})
	_, err := s.pool.CopyFrom(ctx, pgx.Identifier{"checks_results"}, checksResultsColumns, src)
	return err
}

func (s *Stats) EnsureChecksResultsWeeklyPartition(ctx context.Context, ts time.Time) error {
	return s.ensureWeeklyPartition(ctx, "checks_results", checksResultsPartitionName(ts), ts)
}

func checksResultsPartitionName(ts time.Time) string {
	y, w := ts.UTC().ISOWeek()
	return sprintfPartition("checks_results", y, w)
}

// LatestChecksResults returns the most recent result per (check_id, object)
// for server in [since, until). T1 only.
func (s *Stats) LatestChecksResults(ctx context.Context, serverID string, since, until time.Time) ([]ChecksResultRow, error) {
	rows, err := s.ro.Query(ctx,
		`SELECT server_id, evaluated_at, check_id, category, severity, status, object, detail, muted, data_tier
		   FROM checks_results
		  WHERE server_id = $1 AND evaluated_at >= $2 AND evaluated_at < $3 AND data_tier = 1
		    AND (check_id, object, evaluated_at) IN (
		        SELECT check_id, object, max(evaluated_at) FROM checks_results
		         WHERE server_id = $1 AND evaluated_at >= $2 AND evaluated_at < $3 AND data_tier = 1
		         GROUP BY check_id, object)
		  ORDER BY severity, check_id, object`,
		serverID, since, until)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ChecksResultRow
	for rows.Next() {
		var r ChecksResultRow
		if err := rows.Scan(&r.ServerID, &r.EvaluatedAt, &r.CheckID, &r.Category,
			&r.Severity, &r.Status, &r.Object, &r.Detail, &r.Muted, &r.DataTier); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// MuteRow is an operator suppression entry.
type MuteRow struct {
	ServerID   string
	CheckID    string
	Object     string
	MutedUntil time.Time
	Reason     string
}

// SetMute upserts a mute. object="" mutes every object of check on server.
func (s *Stats) SetMute(ctx context.Context, serverID, checkID, object string, until time.Time, reason string) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO check_mutes (server_id, check_id, object, muted_until, reason)
		 VALUES ($1,$2,$3,$4,$5)
		 ON CONFLICT (server_id, check_id, object)
		 DO UPDATE SET muted_until = EXCLUDED.muted_until, reason = EXCLUDED.reason`,
		serverID, checkID, object, until, reason)
	return err
}

// ClearMute deletes a mute.
func (s *Stats) ClearMute(ctx context.Context, serverID, checkID, object string) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM check_mutes WHERE server_id=$1 AND check_id=$2 AND object=$3`,
		serverID, checkID, object)
	return err
}

// ListMutes returns active (non-expired) mutes for server.
func (s *Stats) ListMutes(ctx context.Context, serverID string) ([]MuteRow, error) {
	rows, err := s.ro.Query(ctx,
		`SELECT server_id, check_id, object, muted_until, reason
		   FROM check_mutes WHERE server_id=$1 AND muted_until > now()`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MuteRow
	for rows.Next() {
		var m MuteRow
		if err := rows.Scan(&m.ServerID, &m.CheckID, &m.Object, &m.MutedUntil, &m.Reason); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
```

> **NOTE for implementer:** `ensureWeeklyPartition`, `sprintfPartition`, and `isoWeekBounds` may not exist as named helpers — the existing tables inline the `CREATE TABLE IF NOT EXISTS … PARTITION OF` + `fmt.Sprintf("<t>_%04d_%02d", y, w)`. **First read `internal/store/stats.go` + `table_stats.go`** and either (a) reuse the real helper names if present, or (b) inline the same `CREATE TABLE IF NOT EXISTS %s PARTITION OF checks_results FOR VALUES FROM (…) TO (…)` exactly as `EnsureTableStatsWeeklyPartition` does, with `isoWeekBounds(ts)` and the `_%04d_%02d` name. Do not introduce new helper names that don't already exist; match the file you're mirroring.

- [ ] **Step 6: Implement `RecentServerIDs`** (`internal/store/servers.go`)

```go
package store

import (
	"context"
	"time"
)

// RecentServerIDs returns distinct server_ids that have shipped any
// table_stats since `since`. Used by the Checks scheduler to enumerate
// targets. T1 only.
func (s *Stats) RecentServerIDs(ctx context.Context, since time.Time) ([]string, error) {
	rows, err := s.ro.Query(ctx,
		`SELECT DISTINCT server_id FROM table_stats
		  WHERE collected_at >= $1 AND data_tier = 1 ORDER BY server_id`, since)
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

- [ ] **Step 7: Run tests to verify they pass**

Run: `go test ./internal/store/... -run 'TestWriteAndReadChecksResults|TestMuteRoundTrip' -timeout 8m`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/store/migrations/stats/0008_checks_results.sql \
        internal/store/migrations/stats/0009_check_mutes.sql \
        internal/store/checks_results.go internal/store/servers.go \
        internal/store/checks_results_test.go
git commit -m "feat(store): checks_results + check_mutes tables, writer/reader, RecentServerIDs (ly-u4t.20)"
```

---

### Task A3: Scheduler (advisory-locked ticker) + Notifier seam

**Files:**
- Create: `internal/checks/scheduler.go`
- Test: `internal/checks/scheduler_test.go`

**Design:** `Scheduler.RunOnce(ctx)` is the testable unit. It (1) `pg_try_advisory_lock(lockKey)` on one pooled conn — returns early if not acquired (another replica owns this tick); (2) `RecentServerIDs`; (3) per server, assemble `Input` from store reads + list active mutes; (4) `Run`; (5) mark each result `Muted` if a mute matches (check_id + object/"" ); (6) `WriteChecksResults`; (7) for each non-muted firing result, call `notify.Notify`. `Run(ctx)` wraps it in a ticker until ctx is done. The store reads are taken through a small interface so the test can use a real store (testcontainers) end-to-end.

- [ ] **Step 1: Write the failing test** (`internal/checks/scheduler_test.go`)

```go
package checks

import (
	"context"
	"testing"
	"time"

	"github.com/dobbo-ca/lynceus/internal/store"
)

type recordingNotifier struct{ got []Result }

func (n *recordingNotifier) Notify(_ context.Context, r Result) error {
	n.got = append(n.got, r)
	return nil
}

// alwaysCritical fires once for server input regardless of data.
type alwaysCritical struct{}

func (alwaysCritical) ID() string       { return "test.always" }
func (alwaysCritical) Category() string  { return "test" }
func (alwaysCritical) Eval(in Input) []Result {
	return []Result{{CheckID: "test.always", Category: "test",
		Severity: SeverityCritical, Status: StatusFiring, Object: "obj1", Detail: "x"}}
}

func TestSchedulerRunOncePersistsAndNotifies(t *testing.T) {
	ctx := context.Background()
	s := newSchedulerTestStore(t) // seeds one table_stats row for server "srv-a"; see helper note
	notif := &recordingNotifier{}
	sc := NewScheduler(s, []Check{alwaysCritical{}}, notif).WithNow(func() time.Time {
		return time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	})
	if err := sc.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	res, err := s.LatestChecksResults(ctx, "srv-a",
		time.Date(2026, 6, 9, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC))
	if err != nil || len(res) != 1 {
		t.Fatalf("persist: err=%v rows=%d", err, len(res))
	}
	if len(notif.got) != 1 || notif.got[0].CheckID != "test.always" {
		t.Fatalf("notify: %+v", notif.got)
	}
}

func TestSchedulerHonorsMute(t *testing.T) {
	ctx := context.Background()
	s := newSchedulerTestStore(t)
	if err := s.SetMute(ctx, "srv-a", "test.always", "", time.Now().Add(time.Hour), "muted"); err != nil {
		t.Fatalf("mute: %v", err)
	}
	notif := &recordingNotifier{}
	sc := NewScheduler(s, []Check{alwaysCritical{}}, notif)
	if err := sc.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(notif.got) != 0 {
		t.Fatalf("muted check must not notify, got %+v", notif.got)
	}
}
```

> **Helper note:** `newSchedulerTestStore(t)` must spin up a stats Postgres (same testcontainers helper the store tests use, exported or duplicated minimally into the checks test), run `store.ApplyStatsMigrations`, and seed one `table_stats` row for `srv-a` via `WriteTableStats` so `RecentServerIDs` finds it. Reuse `store`'s test helper if accessible from this package; otherwise add a tiny local container bootstrap mirroring `internal/store/table_stats_test.go`. The scheduler depends only on the concrete `*store.Stats`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/checks/... -run TestScheduler -timeout 8m`
Expected: FAIL — `undefined: NewScheduler`.

- [ ] **Step 3: Implement** (`internal/checks/scheduler.go`)

```go
package checks

import (
	"context"
	"log"
	"time"

	"github.com/dobbo-ca/lynceus/internal/store"
)

// schedulerLockKey is a fixed pg advisory-lock key so that across N
// ingestion replicas only one evaluates+persists per tick.
const schedulerLockKey int64 = 7426398501234599001

// Notifier receives non-muted firing results. Email/Slack (ly-7ck.5/.6)
// implement it; the default is a no-op.
type Notifier interface {
	Notify(ctx context.Context, r Result) error
}

// NopNotifier drops results.
type NopNotifier struct{}

func (NopNotifier) Notify(context.Context, Result) error { return nil }

// Scheduler periodically evaluates checks over the latest per-server stats
// and persists results. It runs in the ingestion service (write side).
type Scheduler struct {
	stats    *store.Stats
	checks   []Check
	notify   Notifier
	interval time.Duration
	now      func() time.Time
}

// NewScheduler builds a Scheduler with the given store, checks, and
// notifier. interval defaults to 60s; now defaults to time.Now.
func NewScheduler(s *store.Stats, cs []Check, n Notifier) *Scheduler {
	if n == nil {
		n = NopNotifier{}
	}
	return &Scheduler{stats: s, checks: cs, notify: n, interval: 60 * time.Second, now: time.Now}
}

func (sc *Scheduler) WithInterval(d time.Duration) *Scheduler {
	if d > 0 {
		sc.interval = d
	}
	return sc
}
func (sc *Scheduler) WithNow(f func() time.Time) *Scheduler {
	if f != nil {
		sc.now = f
	}
	return sc
}

// Run ticks RunOnce until ctx is cancelled. First tick fires immediately.
func (sc *Scheduler) Run(ctx context.Context) {
	t := time.NewTicker(sc.interval)
	defer t.Stop()
	if err := sc.RunOnce(ctx); err != nil {
		log.Printf("checks: first run: %v", err)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := sc.RunOnce(ctx); err != nil {
				log.Printf("checks: run: %v", err)
			}
		}
	}
}

// RunOnce evaluates every server once under the advisory lock. If the lock
// is held by another replica it returns nil (that replica owns this tick).
func (sc *Scheduler) RunOnce(ctx context.Context) error {
	conn, err := sc.stats.Pool().Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	var locked bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, schedulerLockKey).Scan(&locked); err != nil {
		return err
	}
	if !locked {
		return nil
	}
	defer conn.Exec(ctx, `SELECT pg_advisory_unlock($1)`, schedulerLockKey)

	now := sc.now().UTC()
	servers, err := sc.stats.RecentServerIDs(ctx, now.AddDate(0, 0, -1))
	if err != nil {
		return err
	}
	for _, srv := range servers {
		in, err := sc.assembleInput(ctx, srv, now)
		if err != nil {
			log.Printf("checks: assemble %s: %v", srv, err)
			continue
		}
		results := Run(in, sc.checks)
		mutes, _ := sc.stats.ListMutes(ctx, srv)
		var rows []store.ChecksResultRow
		for _, r := range results {
			muted := isMuted(mutes, r)
			rows = append(rows, store.ChecksResultRow{
				ServerID: r.ServerID, EvaluatedAt: now, CheckID: r.CheckID,
				Category: r.Category, Severity: string(r.Severity), Status: string(r.Status),
				Object: r.Object, Detail: r.Detail, Muted: muted,
			})
			if !muted && r.Status == StatusFiring {
				if err := sc.notify.Notify(ctx, r); err != nil {
					log.Printf("checks: notify %s/%s: %v", r.CheckID, r.Object, err)
				}
			}
		}
		if err := sc.stats.WriteChecksResults(ctx, rows); err != nil {
			return err
		}
	}
	return nil
}

func isMuted(mutes []store.MuteRow, r Result) bool {
	for _, m := range mutes {
		if m.CheckID == r.CheckID && (m.Object == "" || m.Object == r.Object) {
			return true
		}
	}
	return false
}

// assembleInput reads the latest per-server stats the checks need. Part B
// extends it with FreezeAges.
func (sc *Scheduler) assembleInput(ctx context.Context, serverID string, now time.Time) (Input, error) {
	in := Input{ServerID: serverID, Now: now}
	tables, err := sc.stats.LatestTableStats(ctx, serverID, now)
	if err != nil {
		return in, err
	}
	for _, t := range tables {
		in.TableStats = append(in.TableStats, TableInfo{
			Relation: t.FQN, LiveTuples: t.LiveTuples, DeadTuples: t.DeadTuples,
			NModSinceAnalyze: t.NModSinceAnalyze, SeqScan: t.SeqScan, IdxScan: t.IdxScan,
		})
	}
	return in, nil
}
```

> **NOTE:** `Scheduler` calls `sc.stats.Pool()`. Confirm `*store.Stats` exposes a primary-pool accessor; `Config` has `Pool()` (config.go:37). If `Stats` lacks one, **add** `func (s *Stats) Pool() *pgxpool.Pool { return s.pool }` to `internal/store/stats.go` in this task (one line, mirrors `Config.Pool()`).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/checks/... -timeout 8m`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/checks/scheduler.go internal/checks/scheduler_test.go internal/store/stats.go
git commit -m "feat(checks): advisory-locked scheduler + Notifier seam, muting (ly-u4t.20)"
```

---

### Task A4: Wire scheduler into ingestion

**Files:**
- Modify: `cmd/ingestion/main.go`

- [ ] **Step 1: Add the scheduler goroutine** after `srv := ingest.NewServer(...)` (around line 50). Insert:

```go
	checksInterval := time.Duration(envInt("LYNCEUS_CHECKS_INTERVAL_SEC", 60)) * time.Second
	scheduler := checks.NewScheduler(store.NewStats(pool), checks.DefaultChecks(), checks.NopNotifier{}).
		WithInterval(checksInterval)
	go scheduler.Run(ctx)
```

Add `"github.com/dobbo-ca/lynceus/internal/checks"` to imports. `time` is already imported.

- [ ] **Step 2: Build**

Run: `go build ./...`
Expected: success.

- [ ] **Step 3: Commit**

```bash
git add cmd/ingestion/main.go
git commit -m "feat(ingestion): start Checks scheduler goroutine (ly-u4t.20)"
```

---

### Task A5: `/checks` page (handler + templ + nav)

**Files:**
- Create: `internal/api/checks.go`
- Test: `internal/api/checks_test.go`
- Create: `web/checks.templ` (+ regen `web/checks_templ.go`)
- Modify: `internal/api/server.go`
- Modify: `web/layout.templ` (+ regen)

- [ ] **Step 1: Write the templ page** (`web/checks.templ`) — mirror `web/vacuum_advisor.templ`:

```go
package web

// ChecksRow is the view-model for one check result.
type ChecksRow struct {
	Severity string
	Category string
	CheckID  string
	Object   string
	Detail   string
	Muted    bool
}

templ ChecksPage(rows []ChecksRow) {
	@Layout("Lynceus — checks", "scheduled health checks + alerts") {
		<p hx-get="/partial/checks" hx-trigger="every 30s" hx-target="#checks-table" hx-swap="outerHTML"></p>
		@ChecksTable(rows)
	}
}

templ ChecksTable(rows []ChecksRow) {
	<div id="checks-table">
		if len(rows) == 0 {
			<p class="empty">No firing checks — all monitored servers healthy.</p>
		} else {
			<table>
				<thead><tr><th>Severity</th><th>Category</th><th>Check</th><th>Object</th><th>Detail</th><th>Muted</th></tr></thead>
				<tbody>
					for _, r := range rows {
						<tr>
							<td><code>{ r.Severity }</code></td>
							<td><code>{ r.Category }</code></td>
							<td><code>{ r.CheckID }</code></td>
							<td><code>{ r.Object }</code></td>
							<td>{ r.Detail }</td>
							<td>
								if r.Muted {
									<code>muted</code>
								}
							</td>
						</tr>
					}
				</tbody>
			</table>
		}
	}
}
```

- [ ] **Step 2: Add nav link** in `web/layout.templ` nav block, after the Waits link:

```html
		<a href="/checks">Checks</a>
```

- [ ] **Step 3: Regenerate templ**

Run: `make templ`
Expected: `web/checks_templ.go` created, `web/layout_templ.go` updated.

- [ ] **Step 4: Write the handler** (`internal/api/checks.go`) — mirror `vacuum_advisor.go`:

```go
package api

import (
	"net/http"
	"time"

	"github.com/dobbo-ca/lynceus/web"
)

func (s *Server) handleChecksPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.ChecksPage(s.fetchChecks(r)).Render(r.Context(), w)
}

func (s *Server) handleChecksPartial(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.ChecksTable(s.fetchChecks(r)).Render(r.Context(), w)
}

func (s *Server) fetchChecks(r *http.Request) []web.ChecksRow {
	now := time.Now().UTC()
	since := now.AddDate(0, 0, -1)
	servers, err := s.stats.RecentServerIDs(r.Context(), since)
	if err != nil {
		return nil
	}
	var out []web.ChecksRow
	for _, srv := range servers {
		res, err := s.stats.LatestChecksResults(r.Context(), srv, since, now.Add(time.Minute))
		if err != nil {
			continue
		}
		for _, c := range res {
			out = append(out, web.ChecksRow{
				Severity: c.Severity, Category: c.Category, CheckID: c.CheckID,
				Object: c.Object, Detail: c.Detail, Muted: c.Muted,
			})
		}
	}
	return out
}
```

- [ ] **Step 5: Register routes** in `internal/api/server.go` `routes()`, after the waits registration:

```go
	s.mux.HandleFunc("GET /checks", s.handleChecksPage)
	s.mux.HandleFunc("GET /partial/checks", s.handleChecksPartial)
```

- [ ] **Step 6: Write the handler test** (`internal/api/checks_test.go`) — mirror an existing handler test (e.g. `vacuum_advisor_test.go`): construct a `Server` with a real test store, seed one `checks_results` row, GET `/checks`, assert 200 + body contains the check id. Reuse the existing api test harness in that file.

```go
func TestChecksPageRenders(t *testing.T) {
	s, st := newTestServer(t) // same helper vacuum_advisor_test.go uses
	ctx := context.Background()
	at := time.Now().UTC()
	_ = st.WriteTableStats(ctx, []store.TableStatRow{{ServerID: "srv-a", CollectedAt: at, FQN: "public.t", SchemaName: "public", ObjectName: "t"}})
	_ = st.WriteChecksResults(ctx, []store.ChecksResultRow{{
		ServerID: "srv-a", EvaluatedAt: at, CheckID: "test.always", Category: "test",
		Severity: "critical", Status: "firing", Object: "obj1", Detail: "x",
	}})
	req := httptest.NewRequest("GET", "/checks", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "test.always") {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
}
```

> **Helper note:** match whatever constructor the sibling `*_test.go` files use (`newTestServer`, `newServerWithStore`, etc.). Read `internal/api/vacuum_advisor_test.go` first and copy its harness exactly.

- [ ] **Step 7: Run tests**

Run: `go test ./internal/api/... -run TestChecks -timeout 8m && go build ./...`
Expected: PASS + build success.

- [ ] **Step 8: Commit**

```bash
git add internal/api/checks.go internal/api/checks_test.go web/checks.templ web/checks_templ.go web/layout.templ web/layout_templ.go internal/api/server.go
git commit -m "feat(api): /checks page surfacing checks_results (ly-u4t.20)"
```

**At this point `ly-u4t.20` is complete:** engine + table + scheduler + gating/muting + page. The page renders "no firing checks" until a bundle registers a real check — Part B lands the first one.

---

## PART B — TXID/MultiXact Wraparound Check (`ly-u4t.26`) + Freezing view

> Reference (in the bead notes): postgres.ai howto on monitoring TXID wraparound — query `age(datfrozenxid)` / `age(relfrozenxid)`, headroom vs `autovacuum_freeze_max_age` (default 200M; hard stop at ~2.1B), MultiXact via `mxid_age(datminmxid)` / `mxid_age(relminmxid)`.

### Task B1: `FreezeAge` proto message + contract test

**Files:**
- Modify: `proto/lynceus/v1/snapshot.proto`
- Modify: `internal/proto/lynceus/v1/contract_test.go`

- [ ] **Step 1: Add the message + envelope field** to `snapshot.proto`. After the `TableStat` message add:

```proto
// FreezeAge carries transaction-id / MultiXact freeze AGES (counts), never
// raw xids. Used by the wraparound check (ly-u4t.26). scope is "database"
// or "table"; for "database" the identifier is the datname and table
// fields are 0. All age fields are non-negative counts — T1 safe.
message FreezeAge {
  string scope = 1;               // "database" | "table"
  string schema = 2;              // "" for database scope
  string name = 3;                // table name or database name
  string fqn = 4;                 // schema.name for tables; datname for db
  int64 xid_age = 5;              // age(relfrozenxid) | age(datfrozenxid)
  int64 mxid_age = 6;             // mxid_age(relminmxid) | mxid_age(datminmxid)
  int64 autovacuum_freeze_max_age = 7; // server setting (count) for headroom
}
```

And on `Snapshot`, after `repeated LogEvent log_events = 8;`:

```proto
  repeated FreezeAge freeze_ages = 9;
```

- [ ] **Step 2: Regenerate**

Run: `make proto`
Expected: `internal/proto/lynceus/v1/snapshot.pb.go` updated with `FreezeAge` + `GetFreezeAges()`.

- [ ] **Step 3: Add the contract test** in `internal/proto/lynceus/v1/contract_test.go` — mirror `TestTableStatHasOnlyAggregateFields`:

```go
func TestFreezeAgeHasOnlyAggregateFields(t *testing.T) {
	allowed := map[string]struct{}{
		"scope": {}, "schema": {}, "name": {}, "fqn": {},
		"xid_age": {}, "mxid_age": {}, "autovacuum_freeze_max_age": {},
	}
	assertOnlyAllowed(t, (&lynceusv1.FreezeAge{}).ProtoReflect().Descriptor().Fields(), allowed, "FreezeAge")
}
```

And add `"freeze_ages": {},` to the Snapshot allowlist map in `TestSnapshotCarriesLogEvents` (or the snapshot-envelope allowlist test).

- [ ] **Step 4: Run the contract test**

Run: `go test ./internal/proto/... -timeout 3m`
Expected: PASS (proves no literal-capable field slipped in).

- [ ] **Step 5: Commit**

```bash
git add proto/lynceus/v1/snapshot.proto internal/proto/lynceus/v1/snapshot.pb.go internal/proto/lynceus/v1/contract_test.go
git commit -m "feat(proto): FreezeAge T1 message (counts only) for wraparound (ly-u4t.26)"
```

---

### Task B2: Collector freeze-age reader + capability + wiring

**Files:**
- Modify: `internal/caps/caps.go`
- Create: `internal/collector/freeze_age_reader.go`
- Test: `internal/collector/freeze_age_reader_test.go`
- Modify: `cmd/collector/main.go`

- [ ] **Step 1: Add capability** in `internal/caps/caps.go`: add `FreezeAge Capability = "freeze_age"` after `TableSize` and append `FreezeAge` to the `Declared()` slice.

- [ ] **Step 2: Write the failing reader test** (`internal/collector/freeze_age_reader_test.go`) — mirror `table_stats_reader_test.go` (testcontainers monitored PG, create a table, run reader, assert ≥1 table-scope row + 1 database-scope row with non-negative ages):

```go
func TestFreezeAgeReaderReadsDatabaseAndTableScopes(t *testing.T) {
	ctx := context.Background()
	pool := startMonitoredPG(t) // same helper table_stats_reader_test.go uses
	_, err := pool.Exec(ctx, `CREATE TABLE froz_demo(id int)`)
	if err != nil { t.Fatal(err) }
	r := NewFreezeAgeReader(pool, NewSchemaFilter(nil, nil), openGate(t), "postgres")
	rows, err := r.Read(ctx, "srv-a")
	if err != nil { t.Fatalf("read: %v", err) }
	var sawDB, sawTable bool
	for _, fa := range rows {
		if fa.Scope == "database" { sawDB = true }
		if fa.Scope == "table" && fa.Fqn == "public.froz_demo" { sawTable = true }
		if fa.XidAge < 0 { t.Fatalf("negative xid age: %+v", fa) }
	}
	if !sawDB || !sawTable {
		t.Fatalf("want db+table scopes, db=%v table=%v rows=%d", sawDB, sawTable, len(rows))
	}
}
```

> **Helper note:** copy the exact monitored-PG + gate + filter setup from `internal/collector/table_stats_reader_test.go` (`NewSchemaFilter` signature, how the gate is built so `caps.FreezeAge` is allowed). Read that file first.

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/collector/... -run TestFreezeAgeReader -timeout 8m`
Expected: FAIL — `undefined: NewFreezeAgeReader`.

- [ ] **Step 4: Implement** (`internal/collector/freeze_age_reader.go`) — mirror `table_stats_reader.go` structure (struct, `NewFreezeAgeReader`, gated `Read`). SQL:

```go
// per-table (gate FreezeAge; filter by schema)
const freezeTableSQL = `
SELECT n.nspname, c.relname,
       age(c.relfrozenxid)::bigint        AS xid_age,
       mxid_age(c.relminmxid)::bigint     AS mxid_age
  FROM pg_class c
  JOIN pg_namespace n ON n.oid = c.relnamespace
 WHERE c.relkind IN ('r','m','t')
   AND n.nspname NOT IN ('pg_catalog','information_schema','pg_toast')`

// per-database (one row for current_database())
const freezeDBSQL = `
SELECT datname,
       age(datfrozenxid)::bigint    AS xid_age,
       mxid_age(datminmxid)::bigint AS mxid_age
  FROM pg_database WHERE datname = current_database()`

// autovacuum_freeze_max_age once (SHOW / current_setting), attach to each row.
const freezeMaxAgeSQL = `SELECT current_setting('autovacuum_freeze_max_age')::bigint`
```

`Read` returns `[]*lynceusv1.FreezeAge`: gate-check (`r.gate.Allowed(r.db, caps.FreezeAge)` → `nil,nil` if disallowed), read `freezeMaxAgeSQL` once, run `freezeDBSQL` (scope "database", schema "", fqn=datname), run `freezeTableSQL` (apply `r.filter.IsAllowed(nspname)`, scope "table", fqn="schema.name"), set `AutovacuumFreezeMaxAge` on every row. Mirror the gate/filter/scan idioms exactly from `table_stats_reader.go`.

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/collector/... -run TestFreezeAgeReader -timeout 8m`
Expected: PASS.

- [ ] **Step 6: Wire into collector** (`cmd/collector/main.go`): construct `freezeReader := collector.NewFreezeAgeReader(pool, filter, gate, db)` next to `tableStatsReader` (~line 87); in `runFull`, `freezeAges, err := freezeReader.Read(ctx, cfg.serverID)` (handle err like the others); add `FreezeAges: freezeAges` to the `&lynceusv1.Snapshot{...}` literal (~lines 109-115).

- [ ] **Step 7: Build + arch grep**

Run: `go build ./... && grep -rn "pgxpool.New" internal/collector | grep -v _test | wc -l` (expect `0`) and `grep -rn "internal/store" internal/collector | grep -v _test | wc -l` (expect `0`).
Expected: build success, both counts `0`.

- [ ] **Step 8: Commit**

```bash
git add internal/caps/caps.go internal/collector/freeze_age_reader.go internal/collector/freeze_age_reader_test.go cmd/collector/main.go
git commit -m "feat(collector): freeze-age reader (relfrozenxid/datfrozenxid/mxid ages), gated, T1 (ly-u4t.26)"
```

---

### Task B3: `freeze_ages` store table + writer/reader + ingest routing

**Files:**
- Create: `internal/store/migrations/stats/0010_freeze_ages.sql`
- Create: `internal/store/freeze_ages.go`
- Test: `internal/store/freeze_ages_test.go`
- Modify: `internal/ingest/server.go`

- [ ] **Step 1: DDL** (`0010_freeze_ages.sql`) — mirror `0006_table_stats.sql`:

```sql
-- freeze_ages: per-database + per-table transaction-id / MultiXact freeze
-- AGES (counts only — never raw xids). Feeds the wraparound check
-- (ly-u4t.26) + VACUUM advisor Freezing view. Vanilla Postgres, RDS-safe.
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
```

- [ ] **Step 2: Failing store test** (`internal/store/freeze_ages_test.go`) — mirror `table_stats_test.go`: write 1 db-scope + 1 table-scope row, read back via `LatestFreezeAges`, assert ages preserved.

- [ ] **Step 3: Run → fail.** `go test ./internal/store/... -run TestFreezeAges -timeout 8m` → FAIL undefined.

- [ ] **Step 4: Implement** (`internal/store/freeze_ages.go`) — mirror `table_stats.go` exactly: `FreezeAgeRow{ServerID, CollectedAt, Scope, SchemaName, ObjectName, FQN, XIDAge, MXIDAge, AutovacuumFreezeMaxAge, DataTier}`, `freezeAgesColumns` (DDL order), `WriteFreezeAges` (ensure-weekly-partition + CopyFromSlice + coerce DataTier), `EnsureFreezeAgesWeeklyPartition`, `freezeAgesPartitionName`, `LatestFreezeAges(ctx, serverID, asOf)` (latest per fqn, `data_tier=1`, mirror `LatestTableStats`).

- [ ] **Step 5: Ingest routing** (`internal/ingest/server.go`): add `snapshotToFreezeAges(&snap)` mapper (mirror `snapshotToTableStats`, lines ~232-280 — map `[]*lynceusv1.FreezeAge` → `[]store.FreezeAgeRow`, `CollectedAt` from snapshot time, `DataTier: 1`) and a guarded write block in `handle` (mirror the table_stats block ~135-141):

```go
	if fa := snapshotToFreezeAges(&snap); len(fa) > 0 {
		if err := s.stats.WriteFreezeAges(ctx, fa); err != nil {
			s.parkDLQ(ctx, snap.ServerId, "write freeze_ages: "+err.Error(), data)
			_ = conn.Close(websocket.StatusInternalError, "")
			return
		}
	}
```

- [ ] **Step 6: Run tests** `go test ./internal/store/... ./internal/ingest/... -run 'FreezeAges|Freeze' -timeout 10m` → PASS; `go build ./...` → success.

- [ ] **Step 7: Commit**

```bash
git add internal/store/migrations/stats/0010_freeze_ages.sql internal/store/freeze_ages.go internal/store/freeze_ages_test.go internal/ingest/server.go
git commit -m "feat(store,ingest): freeze_ages table + writer/reader + ingest routing (ly-u4t.26)"
```

---

### Task B4: WraparoundCheck + scheduler input + Freezing advisor view

**Files:**
- Create: `internal/checks/wraparound.go`
- Test: `internal/checks/wraparound_test.go`
- Modify: `internal/checks/scheduler.go`
- Modify: `internal/advisor/vacuum.go`
- Test: `internal/advisor/vacuum_test.go`
- Modify: `internal/api/vacuum_advisor.go`

- [ ] **Step 1: Failing check test** (`internal/checks/wraparound_test.go`):

```go
package checks

import "testing"

func TestWraparoundCriticalAboveHeadroom(t *testing.T) {
	in := Input{ServerID: "srv-a", FreezeAges: []FreezeInfo{
		{Scope: "table", Relation: "public.hot", XIDAge: 1_800_000_000, AutovacuumFreezeMaxAge: 200_000_000},
		{Scope: "table", Relation: "public.cool", XIDAge: 50_000_000, AutovacuumFreezeMaxAge: 200_000_000},
	}}
	got := WraparoundCheck{}.Eval(in)
	if len(got) != 1 || got[0].Object != "public.hot" || got[0].Severity != SeverityCritical {
		t.Fatalf("want 1 critical for public.hot, got %+v", got)
	}
}

func TestWraparoundWarningInWarnBand(t *testing.T) {
	in := Input{ServerID: "srv-a", FreezeAges: []FreezeInfo{
		{Scope: "database", Relation: "appdb", XIDAge: 600_000_000, AutovacuumFreezeMaxAge: 200_000_000},
	}}
	got := WraparoundCheck{}.Eval(in)
	if len(got) != 1 || got[0].Severity != SeverityWarning {
		t.Fatalf("want 1 warning, got %+v", got)
	}
}
```

**Thresholds (fixed, documented):** hard ceiling ~2.1B. Use ratio to a 2.0B wraparound budget: `crit` when `xid_age >= 1.5e9` OR `mxid_age >= 1.5e9` (≈75% of budget — emergency autovacuum territory); `warn` when `>= 0.5e9` (well past one `autovacuum_freeze_max_age` cycle, lagging). Evaluate both `xid_age` and `mxid_age`; report the worse. Detail string is counts only (e.g. `"xid age 1800000000 (90% of 2.0B wraparound budget); autovacuum_freeze_max_age=200000000"`).

- [ ] **Step 2: Run → fail.** `go test ./internal/checks/... -run TestWraparound -timeout 2m` → undefined.

- [ ] **Step 3: Implement** (`internal/checks/wraparound.go`):

```go
package checks

import "fmt"

func init() { Register(WraparoundCheck{}) }

// WraparoundCheck flags databases/tables whose transaction-id or MultiXact
// freeze age approaches the ~2.1B wraparound ceiling. Critical-safety:
// hitting the ceiling forces Postgres read-only. Counts only — T1.
type WraparoundCheck struct{}

const (
	wrapBudget   = 2_000_000_000.0
	wrapCritical = 1_500_000_000 // ~75% of budget
	wrapWarning  = 500_000_000   // lagging past a freeze cycle
)

func (WraparoundCheck) ID() string      { return "vacuum.wraparound" }
func (WraparoundCheck) Category() string { return "vacuum" }

func (WraparoundCheck) Eval(in Input) []Result {
	var out []Result
	for _, f := range in.FreezeAges {
		age := f.XIDAge
		kind := "transaction-id"
		if f.MXIDAge > age {
			age, kind = f.MXIDAge, "MultiXact"
		}
		var sev Severity
		switch {
		case age >= wrapCritical:
			sev = SeverityCritical
		case age >= wrapWarning:
			sev = SeverityWarning
		default:
			continue
		}
		out = append(out, Result{
			CheckID:  "vacuum.wraparound",
			Category: "vacuum",
			Severity: sev,
			Status:   StatusFiring,
			Object:   f.Relation,
			Detail: fmt.Sprintf("%s freeze age %d (%.0f%% of %.1fB wraparound budget); autovacuum_freeze_max_age=%d",
				kind, age, float64(age)/wrapBudget*100, wrapBudget/1e9, f.AutovacuumFreezeMaxAge),
		})
	}
	return out
}
```

- [ ] **Step 4: Extend scheduler input** — in `internal/checks/scheduler.go` `assembleInput`, after table stats:

```go
	fz, err := sc.stats.LatestFreezeAges(ctx, serverID, now)
	if err != nil {
		return in, err
	}
	for _, f := range fz {
		in.FreezeAges = append(in.FreezeAges, FreezeInfo{
			Scope: f.Scope, Relation: f.FQN, XIDAge: f.XIDAge, MXIDAge: f.MXIDAge,
			AutovacuumFreezeMaxAge: f.AutovacuumFreezeMaxAge,
		})
	}
```

- [ ] **Step 5: Run → pass.** `go test ./internal/checks/... -timeout 8m` → PASS.

- [ ] **Step 6: Add Freezing view to VACUUM advisor** (`internal/advisor/vacuum.go`): add `CatFreezing VacuumCategory = "freezing"`; add a `FreezeAges []FreezeInfo`-style input (advisor-local projection — reuse a small struct `TableFreezeInfo{Relation string; XIDAge, MXIDAge int64}`) to `VacuumAdvice`'s signature **or** add a sibling `FreezeAdvice(freezes []TableFreezeInfo, now) []VacuumRecommendation`. Prefer the **sibling function** to avoid breaking `VacuumAdvice`'s existing signature/tests. Emit a `CatFreezing` recommendation (severity High when `age>=1.5e9`, Medium `>=0.5e9`) with a counts-only Detail. Add a test `TestFreezeAdviceFlagsHighAge` in `vacuum_test.go`.

- [ ] **Step 7: Feed freezes into the advisor handler** (`internal/api/vacuum_advisor.go`): in `fetchVacuumAdvice`, after building table info, call `s.stats.LatestFreezeAges(ctx, srv, now)`, map to `[]advisor.TableFreezeInfo`, call `advisor.FreezeAdvice(...)`, append its rows to `out` (Category "freezing"). This delivers the deferred Freezing view on `/vacuum-advisor`.

- [ ] **Step 8: Run → pass.** `go test ./internal/advisor/... ./internal/api/... -run 'Freeze|Vacuum|Checks' -timeout 10m && go build ./...` → PASS + build.

- [ ] **Step 9: Commit**

```bash
git add internal/checks/wraparound.go internal/checks/wraparound_test.go internal/checks/scheduler.go internal/advisor/vacuum.go internal/advisor/vacuum_test.go internal/api/vacuum_advisor.go
git commit -m "feat(checks,advisor): TXID/MultiXact wraparound check + VACUUM Freezing view (ly-u4t.26)"
```

---

## Final verification (run after all tasks)

- [ ] **Full suite** `go test ./... -p 1 -timeout 25m` → all packages ok (expect ~265+ tests across 18 pkgs incl. new `internal/checks`).
- [ ] **Build** `go build ./...` → success.
- [ ] **Arch invariant** — collector outbound-only:
  - `grep -rn "pgxpool.New" cmd/collector | grep -v _test | wc -l` → `1`
  - `grep -rn "pgxpool.New" internal/collector | grep -v _test | wc -l` → `0`
  - `grep -rn "internal/store" internal/collector | grep -v _test | wc -l` → `0`
- [ ] **Privacy** `go test ./internal/proto/... -timeout 3m` → contract test green (FreezeAge + checks carry no literal-capable field).
- [ ] **Close beads** `bd close ly-u4t.20 ly-u4t.26` on merge.

---

## Self-Review notes

- **Spec coverage:** `ly-u4t.20` → Tasks A1–A5 (severity ✓ info/warning/critical, scheduling ✓ ticker+advisory lock, results table ✓ checks_results, gating ✓ capability gate on the freeze reader + muting). `ly-u4t.26` → Tasks B1–B4 (TXID ✓, MultiXact ✓ via mxid_age, critical-safety ✓ critical severity, Freezing view ✓ B4 step 6-7). Notifications (`ly-7ck.5/.6`) are out of scope here but the `Notifier` seam (A3) is their attach point — separate beads.
- **Type consistency:** `Input`/`Result`/`Severity`/`Check` defined in A1 used unchanged in A3/B4; `FreezeInfo` added in A1 (empty until B), populated in B4; `ChecksResultRow` columns match `0008` DDL order; `FreezeAgeRow` matches `0010` DDL order.
- **Muting scope decision:** `check_mutes` lives in the **stats DB** (not config) so the ingestion scheduler needs only its existing stats pool — documented in `0009`'s comment. Revisit if mutes must be audited (then move to config DB + audit_log).
- **Replica safety:** the advisory lock (A3) prevents duplicate evaluation/notification across ingestion replicas — aligns with the Workstream-B "one replica runs DDL" concern in the roadmap.
