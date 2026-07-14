# ClickHouse Stats Store (chStats) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make ClickHouse a stats-store backend behind the existing `store.Stats` interface (engine-swap of the current collector-normalized `QueryStat` contract), selected by config, without breaking the privacy invariants.

**Architecture:** A new `chStats` implements `store.Stats` against ClickHouse, with T1 (`query_stats`) and T2 (`query_stats_t2`) in physically separate MergeTree tables from day one. `pgxStats` keeps working; the backend is chosen by a required `LYNCEUS_STATS_BACKEND` env. The `Stats` interface loses its Postgres-specific `Pool()` method; the checks scheduler's advisory lock moves to the always-vanilla-Postgres config DB.

**Tech Stack:** Go 1.26, `github.com/ClickHouse/clickhouse-go/v2` (native protocol), `github.com/testcontainers/testcontainers-go/modules/clickhouse` (integration tests against real ClickHouse), existing pgx/pgxpool for Postgres.

**Source spec:** `docs/superpowers/specs/2026-07-14-clickhouse-stats-store-chstats-design.md`. **ADR:** `docs/research/2026-07-14-clickhouse-bedrock-architecture.md`.

## Global Constraints

- Authoritative, tamper-evident audit log stays **vanilla Postgres** (hash-chain). CH holds data only; never the authoritative audit record.
- No new raw/literal field on any T1 proto message. This work does **not** touch the wire contract.
- `data_tier` is carried through to ClickHouse (as a column).
- No feature may depend on a specific backend — `store.Stats` is the seam.
- Integration tests hit a **real** database via testcontainers — never mock the database.
- Dependency pins must match the repo's `testcontainers-go v0.42.0`: use `modules/clickhouse v0.42.0`.
- ClickHouse driver: `github.com/ClickHouse/clickhouse-go/v2` (a recent v2.x, e.g. v2.47.0; must build under Go 1.26).
- ClickHouse identifier gotcha: the column named `rows` must be backticked (`` `rows` ``) in DDL and every query string. Never place a `?` bind placeholder inside backticks.
- TDD: write the failing test first, watch it fail, then implement.

**This plan covers ADR sub-projects #3 (chStats) as tasks t1–t4.** ADR #4 (MV+RBAC) and #7 (isolated raw-table T2) are separate sub-projects that get their own spec → plan → implementation (beads t5/t6 file them; design captured in spec §9). They are **out of this plan's execution scope**; do not implement them here.

---

### Task 1 (t1): Remove `Pool()` from `Stats`; checks scheduler locks on the config-PG pool

**Files:**
- Modify: `internal/store/stats.go` (interface block ~line 16 — remove `Pool()` from the interface; keep the concrete `pgxStats.Pool()` method ~line 73-75)
- Modify: `internal/checks/scheduler.go` (add `lockPool` field + `NewScheduler` param; `RunOnce` uses `sc.lockPool` instead of `sc.stats.Pool()`)
- Modify: `cmd/ingestion/main.go` (open a config-DB pool from `LYNCEUS_CONFIG_DSN`, pass it as the scheduler lock pool)
- Test: `internal/checks/scheduler_test.go` (update `NewScheduler` call sites to pass the container pool as the lock pool)

**Interfaces:**
- Consumes: existing `store.Stats`, `pgxpool.Pool`.
- Produces: `checks.NewScheduler(s store.Stats, lockPool *pgxpool.Pool, cs []Check, n Notifier) *Scheduler`. `store.Stats` no longer declares `Pool()`.

- [ ] **Step 1: Update the scheduler test to the new signature (failing compile = the red)**

In `internal/checks/scheduler_test.go`, every `checks.NewScheduler(stats, ...)` call gains the lock pool (the same pgx pool the test already builds for its container) as the 2nd arg:

```go
sc := checks.NewScheduler(st, pool, checks.DefaultChecks(), checks.NopNotifier{})
```

(Use whatever the test's existing `*pgxpool.Pool` variable is named for the container DB.)

- [ ] **Step 2: Run it to verify it fails to compile**

Run: `go build ./internal/checks/... 2>&1 | head`
Expected: FAIL — `too many arguments in call to checks.NewScheduler` (proves the signature is what we're about to change).

- [ ] **Step 3: Change the `Stats` interface — remove `Pool()`**

In `internal/store/stats.go`, delete this line from the `Stats` interface block:

```go
	Pool() *pgxpool.Pool
```

Leave the concrete `func (s *pgxStats) Pool() *pgxpool.Pool { return s.pool }` method in place (still used as a plain method). `pgxpool` stays imported (used elsewhere in the file).

- [ ] **Step 4: Refactor the scheduler to a dedicated lock pool**

In `internal/checks/scheduler.go`:

```go
import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dobbo-ca/lynceus/internal/advisor"
	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
	"github.com/dobbo-ca/lynceus/internal/store"
)

type Scheduler struct {
	stats    store.Stats
	lockPool *pgxpool.Pool // config DB (always vanilla Postgres) — advisory-lock coordination
	checks   []Check
	notify   Notifier
	interval time.Duration
	now      func() time.Time
}

// NewScheduler builds a Scheduler. lockPool is the config-DB pool used only
// for the cross-replica pg advisory lock (the stats backend may be
// ClickHouse, which has no advisory locks).
func NewScheduler(s store.Stats, lockPool *pgxpool.Pool, cs []Check, n Notifier) *Scheduler {
	if n == nil {
		n = NopNotifier{}
	}
	return &Scheduler{stats: s, lockPool: lockPool, checks: cs, notify: n, interval: 60 * time.Second, now: time.Now}
}
```

In `RunOnce`, change the first line from `sc.stats.Pool().Acquire(ctx)` to:

```go
	conn, err := sc.lockPool.Acquire(ctx)
```

(The rest of `RunOnce` — `pg_try_advisory_lock` / `pg_advisory_unlock` on `conn` — is unchanged.)

- [ ] **Step 5: Wire the config pool into cmd/ingestion**

In `cmd/ingestion/main.go`, after the stats `pool` is created, read the config DSN and open a config pool, then pass it to the scheduler:

```go
	configDSN := os.Getenv("LYNCEUS_CONFIG_DSN")
	if configDSN == "" {
		log.Fatal("LYNCEUS_CONFIG_DSN required")
	}
	if err := secure.CheckDatabaseDSN(configDSN, secure.RequireTLS()); err != nil {
		log.Fatal(err)
	}
	configPool, err := pgxpool.New(ctx, configDSN)
	if err != nil {
		log.Fatalf("connect config db: %v", err)
	}
	defer configPool.Close()
```

Change the scheduler construction to pass `configPool` as the lock pool:

```go
	scheduler := checks.NewScheduler(store.NewStats(pool), configPool, checks.DefaultChecks(), checks.NopNotifier{}).
		WithInterval(checksInterval)
```

- [ ] **Step 6: Build and run the checks tests**

Run: `go build ./... && go test ./internal/checks/... ./cmd/... 2>&1 | tail -20`
Expected: PASS (build clean; scheduler tests green against the container pool).

- [ ] **Step 7: Commit**

```bash
git add internal/store/stats.go internal/checks/scheduler.go cmd/ingestion/main.go internal/checks/scheduler_test.go
git commit -m "refactor(store): drop Pool() from Stats; checks scheduler locks on config PG pool

The Stats seam must not leak a *pgxpool.Pool — the ClickHouse backend has
no advisory locks. Move the checks scheduler's cross-replica pg advisory
lock onto the config DB (always vanilla Postgres); cmd/ingestion now opens
a config pool for it. pgxStats keeps a concrete Pool() method."
```

---

### Task 2 (t2): chStats skeleton + CH migrations + `internal/testch` + first red→green slice

**Files:**
- Modify: `go.mod` / `go.sum` (add `clickhouse-go/v2`, `modules/clickhouse`)
- Create: `internal/store/migrations/clickhouse/0001_query_stats.sql`
- Create: `internal/store/chmigrate.go` (embed FS + `ApplyClickHouseMigrations`)
- Create: `internal/store/chstats.go` (`chStats`, `NewCHStats`, `var _ Stats`, 3 real methods + ~34 stubs)
- Create: `internal/testch/testch.go` (container harness, mirrors `internal/testpg`)
- Test: `internal/store/chstats_test.go` (round-trip + separation)

**Interfaces:**
- Consumes: `store.Stats`, `store.QueryStat`, `store.TopQuery`, `clickhouse.Conn` (`github.com/ClickHouse/clickhouse-go/v2/lib/driver`).
- Produces: `store.NewCHStats(conn driver.Conn) *chStats`; `store.ApplyClickHouseMigrations(ctx context.Context, conn driver.Conn) error`; `testch.Start(t *testing.T) driver.Conn`.

- [ ] **Step 1: Add dependencies**

Run:
```bash
go get github.com/ClickHouse/clickhouse-go/v2@latest
go get github.com/testcontainers/testcontainers-go/modules/clickhouse@v0.42.0
```
Expected: `go.mod` gains both requires; `go.sum` updated. (`go mod tidy` will run after code lands.)

- [ ] **Step 2: Write the ClickHouse migration**

Create `internal/store/migrations/clickhouse/0001_query_stats.sql`:

```sql
CREATE TABLE IF NOT EXISTS query_stats (
  server_id String,
  collected_at DateTime64(3, 'UTC'),
  fingerprint String,
  normalized_query String,
  data_tier Int16 DEFAULT 1,
  calls Int64,
  total_time_ms Float64,
  mean_time_ms Float64,
  `rows` Int64,
  shared_blks_hit Int64,
  shared_blks_read Int64
) ENGINE = MergeTree
PARTITION BY toYYYYMM(collected_at)
ORDER BY (server_id, fingerprint, collected_at)
TTL toDateTime(collected_at) + INTERVAL 90 DAY;

CREATE TABLE IF NOT EXISTS query_stats_t2 (
  server_id String,
  collected_at DateTime64(3, 'UTC'),
  fingerprint String,
  normalized_query String,
  data_tier Int16 DEFAULT 2,
  calls Int64,
  total_time_ms Float64,
  mean_time_ms Float64,
  `rows` Int64,
  shared_blks_hit Int64,
  shared_blks_read Int64
) ENGINE = MergeTree
PARTITION BY toYYYYMM(collected_at)
ORDER BY (server_id, collected_at)
TTL toDateTime(collected_at) + INTERVAL 7 DAY;
```

Note: the two `CREATE TABLE` statements are applied as **separate** ClickHouse queries (CH does not run multiple DDL statements in one query) — see the runner in Step 3, which splits on `;`.

- [ ] **Step 3: Write the ClickHouse migration runner**

Create `internal/store/chmigrate.go`:

```go
package store

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

//go:embed migrations/clickhouse/*.sql
var clickhouseMigrations embed.FS

// ApplyClickHouseMigrations applies every migrations/clickhouse/*.sql file
// in lexical order, idempotently. Applied versions (filename minus .sql) are
// recorded in a schema_migrations table so re-runs skip completed files.
// ClickHouse has no transactional DDL; the DDL is CREATE TABLE IF NOT EXISTS,
// so re-applying a file is harmless even if a crash lands between apply and
// record.
func ApplyClickHouseMigrations(ctx context.Context, conn driver.Conn) error {
	if err := conn.Exec(ctx,
		`CREATE TABLE IF NOT EXISTS schema_migrations (
			version String,
			at DateTime DEFAULT now()
		) ENGINE = MergeTree ORDER BY version`,
	); err != nil {
		return fmt.Errorf("init schema_migrations: %w", err)
	}

	entries, err := fs.ReadDir(clickhouseMigrations, "migrations/clickhouse")
	if err != nil {
		return fmt.Errorf("read clickhouse migrations: %w", err)
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)

	for _, name := range files {
		version := strings.TrimSuffix(name, ".sql")

		var n uint64
		if err := conn.QueryRow(ctx,
			"SELECT count() FROM schema_migrations WHERE version = ?", version,
		).Scan(&n); err != nil {
			return fmt.Errorf("check %s: %w", version, err)
		}
		if n > 0 {
			continue
		}

		body, err := fs.ReadFile(clickhouseMigrations, "migrations/clickhouse/"+name)
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}
		for _, stmt := range strings.Split(string(body), ";") {
			stmt = strings.TrimSpace(stmt)
			if stmt == "" {
				continue
			}
			if err := conn.Exec(ctx, stmt); err != nil {
				return fmt.Errorf("apply %s: %w", name, err)
			}
		}
		if err := conn.Exec(ctx,
			"INSERT INTO schema_migrations (version) VALUES (?)", version,
		); err != nil {
			return fmt.Errorf("record %s: %w", version, err)
		}
	}
	return nil
}
```

- [ ] **Step 4: Write the testcontainer harness**

Create `internal/testch/testch.go`:

```go
// Package testch centralizes the ClickHouse testcontainer setup used by the
// integration tests, mirroring internal/testpg for Postgres.
//
// The clickhouse module's default wait strategy only checks HTTP 200 on 8123,
// which can go ready slightly before the native 9000 listener serves queries,
// so Start polls Ping over the native protocol before returning.
package testch

import (
	"context"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/testcontainers/testcontainers-go"
	tcclickhouse "github.com/testcontainers/testcontainers-go/modules/clickhouse"
)

// Start boots a ClickHouse container (matching the dev image), waits until it
// accepts native queries, and returns a ready driver.Conn. The container and
// connection are torn down via t.Cleanup. If Docker/testcontainers is
// unavailable, the test is skipped.
func Start(t *testing.T) driver.Conn {
	t.Helper()
	ctx := context.Background()

	c, err := tcclickhouse.Run(ctx,
		"clickhouse/clickhouse-server:25.8",
		tcclickhouse.WithDatabase("lynceus_stats"),
		tcclickhouse.WithUsername("test"),
		tcclickhouse.WithPassword("test"),
	)
	if err != nil {
		t.Skipf("docker/testcontainers unavailable: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(c) })

	dsn, err := c.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	opts, err := clickhouse.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	conn, err := clickhouse.Open(opts)
	if err != nil {
		t.Fatalf("open clickhouse: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	var pingErr error
	for i := 0; i < 30; i++ {
		if pingErr = conn.Ping(ctx); pingErr == nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if pingErr != nil {
		t.Fatalf("clickhouse not ready: %v", pingErr)
	}
	return conn
}
```

- [ ] **Step 5: Write the failing round-trip + separation test**

Create `internal/store/chstats_test.go`:

```go
package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/dobbo-ca/lynceus/internal/testch"
)

func TestCHStats_WriteAndTopQueries_RoundTrip(t *testing.T) {
	ctx := context.Background()
	conn := testch.Start(t)
	if err := store.ApplyClickHouseMigrations(ctx, conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := store.NewCHStats(conn)

	base := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	rows := []store.QueryStat{
		{ServerID: "srv1", CollectedAt: base, Fingerprint: "fp-a", NormalizedQuery: "SELECT a", DataTier: 1, Calls: 10, TotalTimeMs: 100},
		{ServerID: "srv1", CollectedAt: base, Fingerprint: "fp-b", NormalizedQuery: "SELECT b", DataTier: 1, Calls: 5, TotalTimeMs: 500},
		{ServerID: "srv1", CollectedAt: base.Add(time.Minute), Fingerprint: "fp-a", NormalizedQuery: "SELECT a", DataTier: 1, Calls: 20, TotalTimeMs: 300},
	}
	if err := s.WriteQueryStats(ctx, rows); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := s.TopQueriesByTotalTime(ctx, base.Add(-time.Hour), base.Add(time.Hour), 10)
	if err != nil {
		t.Fatalf("top: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 fingerprints, got %d: %+v", len(got), got)
	}
	// fp-a: total 400 (100+300), fp-b: 500. Ordered by total desc => fp-b first.
	if got[0].Fingerprint != "fp-b" || got[0].TotalTimeMs != 500 || got[0].Calls != 5 {
		t.Errorf("row0 = %+v, want fp-b total=500 calls=5", got[0])
	}
	if got[1].Fingerprint != "fp-a" || got[1].TotalTimeMs != 400 || got[1].Calls != 30 {
		t.Errorf("row1 = %+v, want fp-a total=400 calls=30", got[1])
	}
}

func TestCHStats_TierSeparation(t *testing.T) {
	ctx := context.Background()
	conn := testch.Start(t)
	if err := store.ApplyClickHouseMigrations(ctx, conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := store.NewCHStats(conn)

	base := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	rows := []store.QueryStat{
		{ServerID: "srv1", CollectedAt: base, Fingerprint: "fp-t1", NormalizedQuery: "SELECT 1", DataTier: 1, Calls: 1, TotalTimeMs: 10},
		{ServerID: "srv1", CollectedAt: base, Fingerprint: "fp-t2", NormalizedQuery: "SELECT * WHERE ssn='123-45-6789'", DataTier: 2, Calls: 2, TotalTimeMs: 20},
	}
	if err := s.WriteQueryStats(ctx, rows); err != nil {
		t.Fatalf("write: %v", err)
	}

	top, err := s.TopQueriesByTotalTime(ctx, base.Add(-time.Hour), base.Add(time.Hour), 10)
	if err != nil {
		t.Fatalf("top: %v", err)
	}
	if len(top) != 1 || top[0].Fingerprint != "fp-t1" {
		t.Fatalf("T1 read leaked or missing rows: %+v", top)
	}

	t2, err := s.ReadQueryStatsTier2(ctx, "srv1", base.Add(-time.Hour), base.Add(time.Hour), 10)
	if err != nil {
		t.Fatalf("t2 read: %v", err)
	}
	if len(t2) != 1 || t2[0].Fingerprint != "fp-t2" || t2[0].DataTier != 2 {
		t.Fatalf("T2 read wrong: %+v", t2)
	}
}
```

- [ ] **Step 6: Run to verify it fails**

Run: `go test ./internal/store/ -run TestCHStats -v 2>&1 | tail -20`
Expected: FAIL to compile — `undefined: store.NewCHStats` / `store.ApplyClickHouseMigrations` not yet defined (or defined, methods missing).

- [ ] **Step 7: Implement chStats (3 real methods + stubs)**

Create `internal/store/chstats.go`. Real methods:

```go
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

var _ Stats = (*chStats)(nil)

// chStats is the ClickHouse-backed Stats implementation. T1 rows live in
// query_stats, T2 (literal-bearing) rows in the separate query_stats_t2
// table (see the migrations and design spec §4).
type chStats struct {
	conn driver.Conn
}

// NewCHStats binds a chStats to an open native ClickHouse connection.
func NewCHStats(conn driver.Conn) *chStats { return &chStats{conn: conn} }

const chQueryStatsCols = "server_id, collected_at, fingerprint, normalized_query, data_tier, " +
	"calls, total_time_ms, mean_time_ms, `rows`, shared_blks_hit, shared_blks_read"

// WriteQueryStats routes rows by data tier: DataTier==2 -> query_stats_t2,
// everything else (0 normalized to 1) -> query_stats. Two batches, one call.
func (s *chStats) WriteQueryStats(ctx context.Context, rows []QueryStat) error {
	if len(rows) == 0 {
		return nil
	}
	var t1, t2 []QueryStat
	for i := range rows {
		r := rows[i]
		if r.DataTier == 0 {
			r.DataTier = 1
		}
		if r.DataTier == 2 {
			t2 = append(t2, r)
		} else {
			t1 = append(t1, r)
		}
	}
	if err := s.insertQueryStats(ctx, "query_stats", t1); err != nil {
		return err
	}
	return s.insertQueryStats(ctx, "query_stats_t2", t2)
}

func (s *chStats) insertQueryStats(ctx context.Context, table string, rows []QueryStat) error {
	if len(rows) == 0 {
		return nil
	}
	batch, err := s.conn.PrepareBatch(ctx, "INSERT INTO "+table+" ("+chQueryStatsCols+")")
	if err != nil {
		return err
	}
	for i := range rows {
		r := &rows[i]
		if err := batch.Append(
			r.ServerID, r.CollectedAt, r.Fingerprint, r.NormalizedQuery, r.DataTier,
			r.Calls, r.TotalTimeMs, r.MeanTimeMs, r.Rows, r.SharedBlksHit, r.SharedBlksRead,
		); err != nil {
			_ = batch.Abort()
			return err
		}
	}
	return batch.Send()
}

// TopQueriesByTotalTime returns up to limit T1 queries in [since, until)
// ordered by total time descending. Reads query_stats only.
func (s *chStats) TopQueriesByTotalTime(ctx context.Context, since, until time.Time, limit int) ([]TopQuery, error) {
	rows, err := s.conn.Query(ctx,
		`SELECT fingerprint, normalized_query, SUM(calls), SUM(total_time_ms)
		   FROM query_stats
		  WHERE collected_at >= ? AND collected_at < ? AND data_tier = 1
		  GROUP BY fingerprint, normalized_query
		  ORDER BY SUM(total_time_ms) DESC
		  LIMIT ?`,
		since, until, uint64(limit),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []TopQuery
	for rows.Next() {
		var q TopQuery
		if err := rows.Scan(&q.Fingerprint, &q.NormalizedQuery, &q.Calls, &q.TotalTimeMs); err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

// ReadQueryStatsTier2 is the ONLY read of the literal-bearing T2 table. It is
// unguarded on purpose: the T2Reader gateway is its sole caller and enforces
// fast-reject + authz + audit-before-read. Reads query_stats_t2 only.
func (s *chStats) ReadQueryStatsTier2(ctx context.Context, serverID string, since, until time.Time, limit int) ([]QueryStat, error) {
	rows, err := s.conn.Query(ctx,
		`SELECT `+chQueryStatsCols+`
		   FROM query_stats_t2
		  WHERE server_id = ? AND collected_at >= ? AND collected_at < ?
		  ORDER BY collected_at DESC
		  LIMIT ?`,
		serverID, since, until, uint64(limit),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []QueryStat
	for rows.Next() {
		var q QueryStat
		if err := rows.Scan(
			&q.ServerID, &q.CollectedAt, &q.Fingerprint, &q.NormalizedQuery, &q.DataTier,
			&q.Calls, &q.TotalTimeMs, &q.MeanTimeMs, &q.Rows, &q.SharedBlksHit, &q.SharedBlksRead,
		); err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}
```

Then stub every other `Stats` method so the interface is satisfied. Each stub returns the zero value plus a sentinel error, e.g.:

```go
func (s *chStats) WaitEventHistogram(ctx context.Context, serverID string, since, until time.Time) ([]WaitEventCount, error) {
	return nil, fmt.Errorf("chStats.WaitEventHistogram: not implemented")
}
// ... one per remaining interface method (see stats.go and t2_read.go for the full set)
```

The complete stub list (34 methods) — copy each signature verbatim from the `Stats` interface: `WaitEventHistogram`, `WriteActivityBuckets`, `RecentServerIDs`, `ThroughputForServers`, `TopQueriesForServers`, `QPSBucketsForServers`, `ActivitySummaryForServers`, `WriteQueryPlans`, `TopPlansByQuery`, `ListPlanKeys`, `WriteInsights`, `InsightCountForServers`, `TopInsightsForServers`, `WriteTableStats`, `LatestTableStats`, `WriteIndexStats`, `LatestIndexStats`, `WriteFreezeAges`, `LatestFreezeAges`, `WriteXminHorizons`, `LatestXminHorizon`, `WriteSettings`, `LatestSettings`, `WriteConnectionSamples`, `WriteBlockingEdges`, `LatestConnectionSamples`, `LatestBlockingEdges`, `WriteChecksResults`, `LatestChecksResults`, `SetMute`, `ClearMute`, `ListMutes`, `WriteLogEvents`. (`WriteQueryStats`, `TopQueriesByTotalTime`, `ReadQueryStatsTier2` are the three real ones above.)

- [ ] **Step 8: Tidy modules, run the tests green**

Run:
```bash
go mod tidy
go test ./internal/store/ -run TestCHStats -v 2>&1 | tail -30
```
Expected: PASS (`TestCHStats_WriteAndTopQueries_RoundTrip`, `TestCHStats_TierSeparation`). Skips only if Docker is unavailable.

- [ ] **Step 9: Full build + vet**

Run: `go build ./... && go vet ./... 2>&1 | tail`
Expected: clean.

- [ ] **Step 10: Commit**

```bash
git add go.mod go.sum internal/store/chstats.go internal/store/chmigrate.go internal/store/migrations/clickhouse/ internal/testch/ internal/store/chstats_test.go
git commit -m "feat(store): ClickHouse Stats backend (chStats) — first slice

chStats implements store.Stats against ClickHouse with T1 (query_stats) and
T2 (query_stats_t2) in physically separate MergeTree tables. This slice
implements WriteQueryStats (tier-routed batches), TopQueriesByTotalTime, and
ReadQueryStatsTier2, verified round-trip + tier-separation against a real
ClickHouse via testcontainers. Remaining Stats methods are stubbed pending t3."
```

---

### Task 3 (t3): Fill the remaining ~34 chStats methods (TDD per domain group)

**Files:** `internal/store/chstats.go` (replace stubs), `internal/store/chstats_*_test.go` (per group), `internal/store/migrations/clickhouse/000N_*.sql` (a CH table per domain that pgxStats stores: activity_buckets, query_plans, insights, table_stats, index_stats, freeze_ages, xmin_horizon, settings, connection_samples, blocking_edges, checks_results, check_mutes, log_events).

**Approach:** For each domain group, follow the exact pattern established in Task 2 — (1) add the CH DDL migration for that group's table(s), mirroring the corresponding `internal/store/migrations/stats/*.sql` schema in ClickHouse types (String / DateTime64(3,'UTC') / Int64 / Float64 / Int16 / Bool as `UInt8`), MergeTree with a sensible `ORDER BY` and monthly partition + TTL; (2) write the failing per-group round-trip test against `testch`; (3) implement the write (batch) + read methods, translating the pgx SQL in the matching `internal/store/*.go` file (`connections.go`, `plans.go`, `insights.go`, `table_stats.go`, `index_stats.go`, `freeze_ages.go`, `xmin_horizon.go`, `settings.go`, `checks_results.go`, `log_events.go`, `rollup.go` for the aggregates) to ClickHouse SQL; (4) run green; (5) commit per group.

Key translation notes carried from Task 2:
- Batch INSERT via `PrepareBatch` with an explicit column list; pass exact-width Go ints (`int16`/`int64`).
- Positional `?` placeholders; `LIMIT ?` takes `uint64`.
- `SUM(Int64) -> int64`, `SUM(Float64) -> float64`.
- "Latest as-of" reads (pgx uses `DISTINCT ON` / window functions): use ClickHouse `argMax(col, collected_at)` grouped by the entity key, or `LIMIT 1 BY key ORDER BY collected_at DESC`.
- Booleans: store as `UInt8`, scan into `bool` (clickhouse-go maps `UInt8`↔`bool` when the Go dest is `*bool`; otherwise store/scan `uint8`).

**Acceptance:** No chStats method returns the not-implemented sentinel; each domain group has red→green integration tests against a real ClickHouse; semantics match the pgxStats equivalents; `go test ./internal/store/...` passes.

*(This task is deliberately pattern-driven rather than spelling out all 34 method bodies: each is a mechanical translation of an existing, tested pgx method, and the executor has the Task 2 template plus the pgx source to translate. Expand each group into its own bite-sized sub-steps at execution time.)*

---

### Task 4 (t4): `store.OpenStats` backend factory + `cmd/*` wiring + env docs

**Files:** Create `internal/store/open.go` (`OpenStats`); modify `cmd/ingestion/main.go` + `cmd/api/main.go` to obtain `Stats` via `OpenStats`; test `internal/store/open_test.go`; update `README`/`docs` env notes.

**Interfaces:**
- Produces: `func OpenStats(ctx context.Context) (Stats, error)`.

- [ ] **Step 1: Failing test for the factory**

Create `internal/store/open_test.go` covering three branches (table-driven), using `t.Setenv`:
- `LYNCEUS_STATS_BACKEND=postgres` with a `LYNCEUS_STATS_DSN` (testcontainer PG) → returns a non-nil `Stats`, no error.
- `LYNCEUS_STATS_BACKEND=clickhouse` with `LYNCEUS_CLICKHOUSE_DSN` (testch container DSN) → returns non-nil `Stats`, no error.
- unset / unknown value → returns a non-nil error.

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/store/ -run TestOpenStats` → FAIL (`undefined: store.OpenStats`).

- [ ] **Step 3: Implement `OpenStats`**

```go
package store

import (
	"context"
	"fmt"
	"os"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/jackc/pgx/v5/pgxpool"
)

// OpenStats builds the stats backend selected by the REQUIRED
// LYNCEUS_STATS_BACKEND env var (no default): "postgres" uses LYNCEUS_STATS_DSN,
// "clickhouse" uses LYNCEUS_CLICKHOUSE_DSN. Migrations are applied before return.
func OpenStats(ctx context.Context) (Stats, error) {
	switch backend := os.Getenv("LYNCEUS_STATS_BACKEND"); backend {
	case "postgres":
		dsn := os.Getenv("LYNCEUS_STATS_DSN")
		if dsn == "" {
			return nil, fmt.Errorf("LYNCEUS_STATS_DSN required for postgres backend")
		}
		pool, err := pgxpool.New(ctx, dsn)
		if err != nil {
			return nil, err
		}
		if err := ApplyStatsMigrations(ctx, pool); err != nil {
			return nil, err
		}
		return NewStats(pool), nil
	case "clickhouse":
		dsn := os.Getenv("LYNCEUS_CLICKHOUSE_DSN")
		if dsn == "" {
			return nil, fmt.Errorf("LYNCEUS_CLICKHOUSE_DSN required for clickhouse backend")
		}
		opts, err := clickhouse.ParseDSN(dsn)
		if err != nil {
			return nil, err
		}
		conn, err := clickhouse.Open(opts)
		if err != nil {
			return nil, err
		}
		if err := ApplyClickHouseMigrations(ctx, conn); err != nil {
			return nil, err
		}
		return NewCHStats(conn), nil
	case "":
		return nil, fmt.Errorf("LYNCEUS_STATS_BACKEND required (postgres|clickhouse)")
	default:
		return nil, fmt.Errorf("unknown LYNCEUS_STATS_BACKEND %q (want postgres|clickhouse)", backend)
	}
}
```

- [ ] **Step 4: Run green** — `go test ./internal/store/ -run TestOpenStats -v` → PASS.

- [ ] **Step 5: Wire the mains.** Replace the direct `store.NewStats(pool)` construction in `cmd/ingestion/main.go` and `cmd/api/main.go` with `store.OpenStats(ctx)` for the stats seam. Keep the read-replica `WithReadPool` optimization for the Postgres path (guard by backend or move behind `OpenStats` later). Resolve CH DSN vs `secure.CheckDatabaseDSN` (skip the PG-oriented DSN check for `clickhouse://` DSNs, or extend `secure` — decide in this task).

- [ ] **Step 6: Docs.** Note in the README / dev docs that `LYNCEUS_STATS_BACKEND` is now **required**; existing Postgres deployments and CI must set `LYNCEUS_STATS_BACKEND=postgres`.

- [ ] **Step 7: Commit.**

```bash
git add internal/store/open.go internal/store/open_test.go cmd/ingestion/main.go cmd/api/main.go README.md
git commit -m "feat(store): OpenStats backend factory (LYNCEUS_STATS_BACKEND, required)"
```

---

## Deferred sub-projects (separate spec → plan → implementation)

Per ADR §6, these are their own sub-projects, **not** implemented by this plan. Beads t5/t6 track them; design is captured in spec §9.

- **t5 (ADR #4): Normalization materialized view + ClickHouse RBAC.** `query_stats` becomes an MV target populated from a raw table (not written directly); RBAC grants the T1 target and denies raw to analysis/Bedrock roles.
- **t6 (ADR #7): Isolated raw T2 table + gateway creds split + `query_log` scrub.** Moves `query_stats_t2` to a dedicated CH database/role, gives the gateway isolated creds (read T2 only), scrubs `query_log` on that path, retargets `ReadQueryStatsTier2`, applies the retention decision. **Closes the spec §7 interim isolation gap — must land before any production T2 literal is written to ClickHouse.**

---

## Self-Review

**Spec coverage:** §1 goal → t1–t4. §2 decisions: engine-swap (t2), separate T1/T2 tables (t2 migration), `Pool()` removal + config lock (t1), required backend factory (t4). §3 architecture → t1/t2/t4. §4 privacy (separate tables, gateway unchanged) → t2. §5 testing (testch, red→green, separation) → t2. §6 invariants → global constraints + t1/t2/t4. §7 interim gap → flagged in t2 commit + Deferred/t6. §8 task sequence → tasks 1–4 + deferred. §9 follow-on design → Deferred section. §10 open items: T2 retention (t6), CH DSN/TLS (t4 step 5), ORDER BY tuning (t3). No gaps.

**Placeholder scan:** t1/t2/t4 contain complete code and exact commands. t3 is intentionally pattern-driven (34 mechanical translations of existing tested pgx methods) with the template + translation notes — flagged as such, not a hidden TODO. t5/t6 are explicitly out of scope (separate sub-projects), not placeholders.

**Type consistency:** `NewCHStats(driver.Conn) *chStats`, `ApplyClickHouseMigrations(ctx, driver.Conn) error`, `testch.Start(t) driver.Conn`, `OpenStats(ctx) (Stats, error)`, `NewScheduler(store.Stats, *pgxpool.Pool, []Check, Notifier)` — consistent across tasks. `chQueryStatsCols` shared by write + T2 read. `LIMIT ?` bound with `uint64` throughout.
