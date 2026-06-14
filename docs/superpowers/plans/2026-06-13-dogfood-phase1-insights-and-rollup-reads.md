# Dogfood Phase 1 — Insights Pipeline + Cluster Roll-up Reads Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the pure-backend foundation for the dogfood dashboard (bead **ly-yuc.1**): a persisted `insights` stats table, server-side insight derivation at ingestion, the stats reads scoped to a *set* of `server_id`s, and a `fleetview` aggregator that rolls those up per cluster — all fully testable with synthetic data, with **no** collector or PlanetScale dependency.

**Architecture:** Insights are **derived server-side at ingestion** (the reframe that supersedes the spec's collector-side decision — see note below): when a `Snapshot` carries `query_plans`, the ingest server runs the existing `internal/insight` engine over them and persists `InsightRow`s to a new range-partitioned `insights` table, mirroring the `query_plans` write path exactly. Cluster roll-ups cross two databases (config holds the `cluster→instance→servers` topology; stats holds the metrics), so they are assembled in Go in a new `internal/fleetview` package: resolve a cluster's `server_id` set via Fleet A's `Config.ServerIDsForCluster`, then query the stats store for that set.

> **Reframe note (supersedes design spec §4):** The design chose collector-side `Insight` proto emission. During planning we found the collector does not ship `query_plans` yet (`ExtractPlans` has no production caller) and PlanetScale managed PG may not expose `auto_explain`. So Phase 1 derives insights **server-side at ingestion** from the already-T1 `query_plans` (privacy-equivalent, far less wiring, no new proto). Wiring the collector to actually ship plans + verifying PlanetScale `auto_explain` is deferred to Phase 4 (ly-yuc.4). Until then these reads simply return empty/zero, which the UI renders gracefully.

**Tech Stack:** Go, pgx/v5 (`CopyFrom`, `= ANY($1)` array params), vanilla PostgreSQL native range partitioning (RDS/Aurora-safe, no extensions), testcontainers + `internal/testpg.ReadyWait()`. Reuses `internal/insight` (pure engine) and Fleet A `Config` funcs.

---

## File structure

- `internal/store/migrations/stats/0005_insights.sql` — **create**: `insights` table, range-partitioned by `captured_at`. Auto-embedded by the `//go:embed migrations/stats/*.sql` glob in `migrate.go` — no Go change there.
- `internal/store/insights.go` — **create**: `InsightRow`, `insightsColumns`, `WriteInsights`, `EnsureInsightsWeeklyPartition`, `insightsPartitionName`, `InsightCountForServers`, `TopInsightsForServers`. Mirrors `internal/store/plans.go`.
- `internal/store/rollup.go` — **create**: the `*Stats` reads scoped to a `server_id` set — `TopQueriesForServers`, `QPSBucketsForServers`, `ThroughputForServers`, `ActivitySummaryForServers` (+ `QPSBucket`, `Throughput`, `ActivitySummary` result types).
- `internal/store/insights_test.go`, `internal/store/rollup_test.go` — **create**: integration tests (external `store_test` package, reuse `newPool`).
- `internal/ingest/server.go` — **modify**: add `snapshotToInsights` (runs the engine) + a guarded `WriteInsights` block in `(*Server).handle`, mirroring the `query_plans` block.
- `internal/ingest/server_test.go` — **modify**: add a test that a Snapshot carrying a slow-scan plan persists an `insights` row.
- `internal/fleetview/summary.go` — **create**: `ClusterSummary` + `ListClusterSummaries(ctx, cfg, stats, since, until)`.
- `internal/fleetview/summary_test.go` — **create**: integration test applying BOTH config + stats migrations to one pool.

---

## Task 1: `insights` table migration

**Files:**
- Create: `internal/store/migrations/stats/0005_insights.sql`
- Test: `internal/store/insights_test.go`

- [ ] **Step 1: Write the failing partition-strategy test**

Create `internal/store/insights_test.go`:

```go
package store_test

import (
	"context"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/store"
)

// TestApplyStatsMigrations_createsPartitionedInsights verifies 0005_insights.sql
// creates a range-partitioned insights table (mirrors the query_plans check).
func TestApplyStatsMigrations_createsPartitionedInsights(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyStatsMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var strategy string
	if err := pool.QueryRow(ctx,
		`SELECT partstrat::text FROM pg_partitioned_table
		   WHERE partrelid = 'insights'::regclass`,
	).Scan(&strategy); err != nil {
		t.Fatalf("insights not partitioned: %v", err)
	}
	if strategy != "r" {
		t.Fatalf("partition strategy = %q, want 'r' (range)", strategy)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestApplyStatsMigrations_createsPartitionedInsights`
Expected: FAIL — `insights not partitioned: ... relation "insights" does not exist`.

- [ ] **Step 3: Write the migration**

Create `internal/store/migrations/stats/0005_insights.sql`:

```sql
-- Detected query anti-patterns (insights), derived from extracted T1 plans.
-- One row per (server_id, fingerprint, captured_at, kind). Every column is a
-- structural identifier or an aggregate count — literal-free (T1), mirroring the
-- insight.Insight struct. Derived server-side at ingestion from query_plans
-- (which are themselves already normalized at the collector).
--
-- Range-partitioned by week on captured_at (vanilla Postgres, RDS / Aurora /
-- Cloud SQL safe — no extensions). Partitions are created at runtime in Go
-- (EnsureInsightsWeeklyPartition), same as query_plans.

CREATE TABLE insights (
    server_id     TEXT NOT NULL,
    captured_at   TIMESTAMPTZ NOT NULL,
    kind          TEXT NOT NULL,
    severity      TEXT NOT NULL,
    fingerprint   TEXT NOT NULL,
    relation      TEXT NOT NULL,
    node_path     TEXT NOT NULL,
    rows_returned BIGINT NOT NULL,
    rows_scanned  BIGINT NOT NULL,
    selectivity   DOUBLE PRECISION NOT NULL,
    detail        TEXT NOT NULL,
    data_tier     SMALLINT NOT NULL DEFAULT 1
) PARTITION BY RANGE (captured_at);

CREATE INDEX insights_brin_time ON insights USING brin (captured_at);
CREATE INDEX insights_srv_kind  ON insights (server_id, kind, captured_at);
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestApplyStatsMigrations_createsPartitionedInsights`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/migrations/stats/0005_insights.sql internal/store/insights_test.go
git commit -m "store(insights): 0005 range-partitioned insights table (ly-yuc.1)"
```

---

## Task 2: store — `WriteInsights` + insight reads

**Files:**
- Create: `internal/store/insights.go`
- Test: `internal/store/insights_test.go` (extend)

- [ ] **Step 1: Write the failing write/read round-trip test**

Append to `internal/store/insights_test.go`:

```go
import "time" // add to the existing import block

func TestWriteInsights_roundtripCountAndTop(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyStatsMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := store.NewStats(pool)

	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC) // a Wednesday
	rows := []store.InsightRow{
		{
			ServerID: "srv-1", CapturedAt: now, Kind: "slow_scan", Severity: "high",
			Fingerprint: "fp-a", Relation: "orders", NodePath: "Seq Scan(orders)",
			RowsReturned: 10, RowsScanned: 100000, Selectivity: 0.0001,
			Detail: "Seq Scan on orders read 100000 rows and discarded 99990 (0.01% returned).",
		},
		{
			ServerID: "srv-2", CapturedAt: now, Kind: "slow_scan", Severity: "medium",
			Fingerprint: "fp-b", Relation: "events", NodePath: "Seq Scan(events)",
			RowsReturned: 100, RowsScanned: 5100, Selectivity: 0.0196,
			Detail: "Seq Scan on events read 5100 rows and discarded 5000 (1.96% returned).",
		},
	}
	if err := s.WriteInsights(ctx, rows); err != nil {
		t.Fatalf("write: %v", err)
	}

	// A weekly partition was created automatically.
	var partCount int
	_ = pool.QueryRow(ctx,
		`SELECT count(*) FROM pg_inherits WHERE inhparent = 'insights'::regclass`,
	).Scan(&partCount)
	if partCount == 0 {
		t.Fatal("write did not create a weekly partition")
	}

	win := func() (time.Time, time.Time) { return now.Add(-time.Hour), now.Add(time.Hour) }
	since, until := win()

	// Count across both servers.
	n, err := s.InsightCountForServers(ctx, []string{"srv-1", "srv-2"}, since, until)
	if err != nil || n != 2 {
		t.Fatalf("InsightCountForServers = %d err=%v, want 2", n, err)
	}
	// Count scoped to one server.
	n1, err := s.InsightCountForServers(ctx, []string{"srv-1"}, since, until)
	if err != nil || n1 != 1 {
		t.Fatalf("InsightCountForServers[srv-1] = %d err=%v, want 1", n1, err)
	}

	// Top insights returns rows for the set, most recent first; fields round-trip.
	top, err := s.TopInsightsForServers(ctx, []string{"srv-1", "srv-2"}, since, until, 10)
	if err != nil {
		t.Fatalf("TopInsightsForServers: %v", err)
	}
	if len(top) != 2 {
		t.Fatalf("top = %d rows, want 2", len(top))
	}
	// Find srv-1's row and check it round-tripped.
	var got store.InsightRow
	for _, r := range top {
		if r.ServerID == "srv-1" {
			got = r
		}
	}
	if got.Relation != "orders" || got.Severity != "high" || got.RowsScanned != 100000 {
		t.Fatalf("srv-1 insight round-trip wrong: %+v", got)
	}
}

func TestWriteInsights_emptyNoop(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyStatsMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := store.NewStats(pool).WriteInsights(ctx, nil); err != nil {
		t.Fatalf("empty WriteInsights should be a no-op, got %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestWriteInsights`
Expected: build failure — `s.WriteInsights undefined` / `store.InsightRow undefined`.

- [ ] **Step 3: Write `internal/store/insights.go`**

Create `internal/store/insights.go` (mirrors `plans.go`; `isoWeekBounds` lives in `stats.go`, same package):

```go
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// InsightRow is one detected anti-pattern as stored in the stats DB. Every field
// is a structural identifier or an aggregate count (T1, literal-free) — it maps
// 1:1 to insight.Insight. DataTier zero is treated as 1 (T1) on insert.
type InsightRow struct {
	ServerID     string
	CapturedAt   time.Time
	Kind         string
	Severity     string
	Fingerprint  string
	Relation     string
	NodePath     string
	RowsReturned int64
	RowsScanned  int64
	Selectivity  float64
	Detail       string
	DataTier     int16
}

// insightsColumns is the COPY column order for WriteInsights.
var insightsColumns = []string{
	"server_id", "captured_at", "kind", "severity", "fingerprint",
	"relation", "node_path", "rows_returned", "rows_scanned",
	"selectivity", "detail", "data_tier",
}

// WriteInsights appends a batch of derived insights via COPY, creating any
// missing weekly partitions first. Mirrors WriteQueryPlans.
func (s *Stats) WriteInsights(ctx context.Context, rows []InsightRow) error {
	if len(rows) == 0 {
		return nil
	}
	weeks := map[string]time.Time{}
	for _, r := range rows {
		weeks[insightsPartitionName(r.CapturedAt)] = r.CapturedAt
	}
	for _, ts := range weeks {
		if err := s.EnsureInsightsWeeklyPartition(ctx, ts); err != nil {
			return err
		}
	}

	src := pgx.CopyFromSlice(len(rows), func(i int) ([]any, error) {
		r := rows[i]
		if r.DataTier == 0 {
			r.DataTier = 1
		}
		return []any{
			r.ServerID, r.CapturedAt, r.Kind, r.Severity, r.Fingerprint,
			r.Relation, r.NodePath, r.RowsReturned, r.RowsScanned,
			r.Selectivity, r.Detail, r.DataTier,
		}, nil
	})
	_, err := s.pool.CopyFrom(ctx, pgx.Identifier{"insights"}, insightsColumns, src)
	return err
}

// InsightCountForServers counts T1 insights for the given server_id set in
// [since, until). serverIDs is passed as a Postgres array (= ANY($1)).
func (s *Stats) InsightCountForServers(
	ctx context.Context, serverIDs []string, since, until time.Time,
) (int, error) {
	var n int
	err := s.ro.QueryRow(ctx,
		`SELECT count(*) FROM insights
		  WHERE server_id = ANY($1)
		    AND captured_at >= $2 AND captured_at < $3
		    AND data_tier = 1`,
		serverIDs, since, until,
	).Scan(&n)
	return n, err
}

// TopInsightsForServers returns up to limit T1 insights for the server_id set in
// [since, until), most recent first.
func (s *Stats) TopInsightsForServers(
	ctx context.Context, serverIDs []string, since, until time.Time, limit int,
) ([]InsightRow, error) {
	rows, err := s.ro.Query(ctx,
		`SELECT server_id, captured_at, kind, severity, fingerprint,
		        relation, node_path, rows_returned, rows_scanned,
		        selectivity, detail, data_tier
		   FROM insights
		  WHERE server_id = ANY($1)
		    AND captured_at >= $2 AND captured_at < $3
		    AND data_tier = 1
		  ORDER BY captured_at DESC
		  LIMIT $4`,
		serverIDs, since, until, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []InsightRow
	for rows.Next() {
		var r InsightRow
		if err := rows.Scan(
			&r.ServerID, &r.CapturedAt, &r.Kind, &r.Severity, &r.Fingerprint,
			&r.Relation, &r.NodePath, &r.RowsReturned, &r.RowsScanned,
			&r.Selectivity, &r.Detail, &r.DataTier,
		); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// EnsureInsightsWeeklyPartition creates the weekly partition for ts on insights
// if it does not already exist. Idempotent.
func (s *Stats) EnsureInsightsWeeklyPartition(ctx context.Context, ts time.Time) error {
	name := insightsPartitionName(ts)
	from, to := isoWeekBounds(ts)
	_, err := s.pool.Exec(ctx, fmt.Sprintf(
		`CREATE TABLE IF NOT EXISTS %s PARTITION OF insights
		 FOR VALUES FROM ('%s') TO ('%s')`,
		name,
		from.Format("2006-01-02"),
		to.Format("2006-01-02"),
	))
	return err
}

func insightsPartitionName(ts time.Time) string {
	y, w := ts.UTC().ISOWeek()
	return fmt.Sprintf("insights_%04d_%02d", y, w)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestWriteInsights`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/insights.go internal/store/insights_test.go
git commit -m "store(insights): WriteInsights + InsightCount/TopInsights for a server set (ly-yuc.1)"
```

---

## Task 3: ingestion — server-side insight derivation

**Files:**
- Modify: `internal/ingest/server.go`
- Test: `internal/ingest/server_test.go` (extend)

- [ ] **Step 1: Write the failing ingest test**

Append to `internal/ingest/server_test.go` (it already imports `context`, `testing`, `time`, `lynceusv1`, `collector`, `ingest`, `store`; if `lynceusv1` is not yet imported, add `lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"`):

```go
// TestServer_derivesAndPersistsInsightsFromPlan sends a Snapshot carrying a
// slow-scan QueryPlan and asserts the ingest server derives + persists an
// insights row (server-side derivation; no collector emission).
func TestServer_derivesAndPersistsInsightsFromPlan(t *testing.T) {
	pool, srv := setup(t, ingest.Config{DevToken: "dev", RateLimit: 10, RateBurst: 10})
	ctx := context.Background()

	captured := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	// A Seq Scan that reads 100000 rows and returns 10 -> slow_scan / high.
	snap := &lynceusv1.Snapshot{
		ServerId:        "srv-ins",
		CollectedAtUnix: captured.Unix(),
		QueryPlans: []*lynceusv1.QueryPlan{{
			Fingerprint:    "fp-slow",
			CapturedAtUnix: captured.Unix(),
			FormatVersion:  1,
			Root: &lynceusv1.PlanNode{
				NodeType:            "Seq Scan",
				RelationName:        "events",
				ActualRows:          10,
				ActualLoops:         1,
				RowsRemovedByFilter: 99990,
			},
		}},
	}

	ship := collector.NewShipper(wsURL(srv.URL), "dev")
	if err := ship.Send(ctx, snap); err != nil {
		t.Fatalf("send: %v", err)
	}

	var rows int
	for i := 0; i < 50 && rows == 0; i++ {
		_ = pool.QueryRow(ctx,
			`SELECT count(*) FROM insights WHERE server_id='srv-ins' AND kind='slow_scan'`,
		).Scan(&rows)
		if rows > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if rows != 1 {
		t.Fatalf("insights row count = %d, want 1", rows)
	}

	var sev, rel string
	if err := pool.QueryRow(ctx,
		`SELECT severity, relation FROM insights WHERE server_id='srv-ins'`,
	).Scan(&sev, &rel); err != nil {
		t.Fatalf("read insight: %v", err)
	}
	if sev != "high" || rel != "events" {
		t.Fatalf("insight = (%s, %s), want (high, events)", sev, rel)
	}

	var dlq int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM dlq`).Scan(&dlq)
	if dlq != 0 {
		t.Errorf("dlq count = %d, want 0", dlq)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ingest/ -run TestServer_derivesAndPersistsInsightsFromPlan`
Expected: FAIL — `insights row count = 0, want 1` (the server doesn't derive insights yet). (The `query_plans` write already succeeds; only the new `insights` write is missing.)

- [ ] **Step 3: Add the converter + write block in `server.go`**

First add the import to `internal/ingest/server.go`'s import block:

```go
	"github.com/dobbo-ca/lynceus/internal/insight"
```

Add this converter near the other `snapshotTo…` functions (after `snapshotToQueryPlans`):

```go
// snapshotToInsights derives T1 insights from the snapshot's normalized plans
// by running the (pure) insight engine, stamping each with its plan's
// captured_at and the snapshot's server_id. Server-side derivation — the input
// plans are already literal-free, so no literal can appear here.
func snapshotToInsights(snap *lynceusv1.Snapshot) []store.InsightRow {
	var out []store.InsightRow
	for _, p := range snap.QueryPlans {
		capturedAt := time.Unix(p.CapturedAtUnix, 0).UTC()
		for _, in := range insight.DetectAll(p) {
			out = append(out, store.InsightRow{
				ServerID:     snap.ServerId,
				CapturedAt:   capturedAt,
				Kind:         string(in.Kind),
				Severity:     string(in.Severity),
				Fingerprint:  in.Fingerprint,
				Relation:     in.Relation,
				NodePath:     in.NodePath,
				RowsReturned: in.RowsReturned,
				RowsScanned:  in.RowsScanned,
				Selectivity:  in.Selectivity,
				Detail:       in.Detail,
				DataTier:     1,
			})
		}
	}
	return out
}
```

In `(*Server).handle`, add a guarded write block immediately AFTER the existing `query_plans` block and BEFORE the final `_ = conn.Close(websocket.StatusNormalClosure, "")`:

```go
	if insights := snapshotToInsights(&snap); len(insights) > 0 {
		if err := s.stats.WriteInsights(ctx, insights); err != nil {
			s.parkDLQ(ctx, snap.ServerId, "write insights: "+err.Error(), data)
			_ = conn.Close(websocket.StatusInternalError, "")
			return
		}
	}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/ingest/ -run TestServer_derivesAndPersistsInsightsFromPlan`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ingest/server.go internal/ingest/server_test.go
git commit -m "ingest(insights): derive + persist insights from snapshot plans server-side (ly-yuc.1)"
```

---

## Task 4: store — query_stats reads scoped to a server set

**Files:**
- Create: `internal/store/rollup.go`
- Test: `internal/store/rollup_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/store/rollup_test.go`:

```go
package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/dobbo-ca/lynceus/internal/store"
)

func seedQueryStats(t *testing.T, ctx context.Context, s *store.Stats, now time.Time) {
	t.Helper()
	rows := []store.QueryStat{
		{ServerID: "srv-a", CollectedAt: now, Fingerprint: "fp-1", NormalizedQuery: "SELECT $1",
			Calls: 100, TotalTimeMs: 200, MeanTimeMs: 2.0},
		{ServerID: "srv-b", CollectedAt: now, Fingerprint: "fp-2", NormalizedQuery: "SELECT $1 FROM t",
			Calls: 300, TotalTimeMs: 30, MeanTimeMs: 0.1},
		{ServerID: "srv-other", CollectedAt: now, Fingerprint: "fp-x", NormalizedQuery: "SELECT 9",
			Calls: 999, TotalTimeMs: 9999, MeanTimeMs: 10},
	}
	if err := s.WriteQueryStats(ctx, rows); err != nil {
		t.Fatalf("seed query_stats: %v", err)
	}
}

func TestQueryReadsForServers_scopeAndAggregate(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyStatsMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := store.NewStats(pool)
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	seedQueryStats(t, ctx, s, now)

	set := []string{"srv-a", "srv-b"} // excludes srv-other
	since, until := now.Add(-time.Hour), now.Add(time.Hour)

	// Throughput: only srv-a + srv-b counted.
	tp, err := s.ThroughputForServers(ctx, set, since, until)
	if err != nil {
		t.Fatalf("ThroughputForServers: %v", err)
	}
	if tp.Calls != 400 || tp.TotalTimeMs != 230 {
		t.Fatalf("throughput = %+v, want calls=400 total=230", tp)
	}

	// Top queries: ordered by total time desc, srv-other excluded.
	top, err := s.TopQueriesForServers(ctx, set, since, until, 10)
	if err != nil {
		t.Fatalf("TopQueriesForServers: %v", err)
	}
	if len(top) != 2 || top[0].Fingerprint != "fp-1" {
		t.Fatalf("top = %+v, want 2 rows, fp-1 first", top)
	}

	// QPS buckets: one hourly bucket holding the summed calls for the set.
	buckets, err := s.QPSBucketsForServers(ctx, set, since, until)
	if err != nil {
		t.Fatalf("QPSBucketsForServers: %v", err)
	}
	var total int64
	for _, b := range buckets {
		total += b.Calls
	}
	if total != 400 {
		t.Fatalf("qps buckets total calls = %d, want 400", total)
	}

	// Empty set is a safe no-op (no rows, zero totals).
	tp0, err := s.ThroughputForServers(ctx, []string{}, since, until)
	if err != nil || tp0.Calls != 0 {
		t.Fatalf("empty-set throughput = %+v err=%v, want 0", tp0, err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestQueryReadsForServers`
Expected: build failure — `s.ThroughputForServers undefined`.

- [ ] **Step 3: Write `internal/store/rollup.go`**

Create `internal/store/rollup.go`:

```go
package store

import (
	"context"
	"time"
)

// Throughput is the aggregate query volume for a server set over a window.
type Throughput struct {
	Calls       int64
	TotalTimeMs float64
}

// QPSBucket is the summed calls for a server set in one hourly time bucket.
type QPSBucket struct {
	BucketStart time.Time
	Calls       int64
}

// ThroughputForServers sums calls + total_time_ms for the server_id set in
// [since, until). Used to derive combined q/s and call-weighted avg latency.
func (s *Stats) ThroughputForServers(
	ctx context.Context, serverIDs []string, since, until time.Time,
) (Throughput, error) {
	var t Throughput
	err := s.ro.QueryRow(ctx,
		`SELECT COALESCE(SUM(calls), 0), COALESCE(SUM(total_time_ms), 0)
		   FROM query_stats
		  WHERE server_id = ANY($1)
		    AND collected_at >= $2 AND collected_at < $3
		    AND data_tier = 1`,
		serverIDs, since, until,
	).Scan(&t.Calls, &t.TotalTimeMs)
	return t, err
}

// TopQueriesForServers is TopQueriesByTotalTime scoped to a server_id set —
// the per-cluster variant. Ordered by total time descending.
func (s *Stats) TopQueriesForServers(
	ctx context.Context, serverIDs []string, since, until time.Time, limit int,
) ([]TopQuery, error) {
	rows, err := s.ro.Query(ctx,
		`SELECT fingerprint, normalized_query, SUM(calls), SUM(total_time_ms)
		   FROM query_stats
		  WHERE server_id = ANY($1)
		    AND collected_at >= $2 AND collected_at < $3
		    AND data_tier = 1
		  GROUP BY fingerprint, normalized_query
		  ORDER BY SUM(total_time_ms) DESC
		  LIMIT $4`,
		serverIDs, since, until, limit,
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

// QPSBucketsForServers returns hourly buckets of summed calls for the server_id
// set in [since, until), oldest first — the data behind a q/s sparkline.
func (s *Stats) QPSBucketsForServers(
	ctx context.Context, serverIDs []string, since, until time.Time,
) ([]QPSBucket, error) {
	rows, err := s.ro.Query(ctx,
		`SELECT date_trunc('hour', collected_at) AS bucket, SUM(calls)
		   FROM query_stats
		  WHERE server_id = ANY($1)
		    AND collected_at >= $2 AND collected_at < $3
		    AND data_tier = 1
		  GROUP BY bucket
		  ORDER BY bucket ASC`,
		serverIDs, since, until,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []QPSBucket
	for rows.Next() {
		var b QPSBucket
		if err := rows.Scan(&b.BucketStart, &b.Calls); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestQueryReadsForServers`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/rollup.go internal/store/rollup_test.go
git commit -m "store(rollup): query_stats reads scoped to a server set (ly-yuc.1)"
```

---

## Task 5: store — activity summary for a server set

**Files:**
- Modify: `internal/store/rollup.go`
- Test: `internal/store/rollup_test.go` (extend)

- [ ] **Step 1: Write the failing test**

Append to `internal/store/rollup_test.go`:

```go
func TestActivitySummaryForServers(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyStatsMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := store.NewStats(pool)

	t0 := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Minute) // a later bucket
	buckets := []store.ActivityBucket{
		// older bucket
		{ServerID: "srv-a", Database: "app", State: "active", WaitEventType: "", WaitEvent: "",
			BucketStart: t0, BucketSeconds: 60, SampleCount: 6, CountSum: 30, CountMax: 5},
		// newest bucket: active conns we expect to read (peak per server, summed)
		{ServerID: "srv-a", Database: "app", State: "active", WaitEventType: "", WaitEvent: "",
			BucketStart: t1, BucketSeconds: 60, SampleCount: 6, CountSum: 48, CountMax: 8},
		{ServerID: "srv-b", Database: "app", State: "active", WaitEventType: "", WaitEvent: "",
			BucketStart: t1, BucketSeconds: 60, SampleCount: 6, CountSum: 24, CountMax: 4},
		// a wait event, should win top-wait over the window
		{ServerID: "srv-a", Database: "app", State: "active", WaitEventType: "IO", WaitEvent: "DataFileRead",
			BucketStart: t1, BucketSeconds: 60, SampleCount: 6, CountSum: 40, CountMax: 7},
		// excluded server
		{ServerID: "srv-other", Database: "app", State: "active", WaitEventType: "", WaitEvent: "",
			BucketStart: t1, BucketSeconds: 60, SampleCount: 6, CountSum: 600, CountMax: 99},
	}
	if err := s.WriteActivityBuckets(ctx, buckets); err != nil {
		t.Fatalf("seed activity: %v", err)
	}

	set := []string{"srv-a", "srv-b"}
	since, until := t0.Add(-time.Hour), t1.Add(time.Hour)
	a, err := s.ActivitySummaryForServers(ctx, set, since, until)
	if err != nil {
		t.Fatalf("ActivitySummaryForServers: %v", err)
	}
	// newest bucket is t1; active CountMax summed over the set = 8 + 4 + 7 = 19.
	if a.ActiveConns != 19 {
		t.Fatalf("active conns = %d, want 19", a.ActiveConns)
	}
	if a.TopWait != "DataFileRead" {
		t.Fatalf("top wait = %q, want DataFileRead", a.TopWait)
	}

	// No-wait window still works (empty top wait, no error).
	a2, err := s.ActivitySummaryForServers(ctx, []string{"srv-b"}, since, until)
	if err != nil {
		t.Fatalf("ActivitySummaryForServers srv-b: %v", err)
	}
	if a2.TopWait != "" {
		t.Fatalf("srv-b top wait = %q, want empty", a2.TopWait)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestActivitySummaryForServers`
Expected: build failure — `s.ActivitySummaryForServers undefined`.

- [ ] **Step 3: Add `ActivitySummaryForServers` to `internal/store/rollup.go`**

Add to the import block of `internal/store/rollup.go`:

```go
	"errors"

	"github.com/jackc/pgx/v5"
```

Append to `internal/store/rollup.go`:

```go
// ActivitySummary is the connection-state snapshot for a server set: peak active
// connections in the most recent bucket and the dominant wait event over the
// window.
type ActivitySummary struct {
	ActiveConns int64
	TopWait     string // "" if nothing was waiting
}

// ActivitySummaryForServers reads the latest active-connection peak and the
// top wait event for the server_id set in [since, until).
func (s *Stats) ActivitySummaryForServers(
	ctx context.Context, serverIDs []string, since, until time.Time,
) (ActivitySummary, error) {
	var a ActivitySummary

	// Peak active connections in the most recent bucket within the window,
	// summed across the server set.
	if err := s.ro.QueryRow(ctx,
		`SELECT COALESCE(SUM(count_max), 0)
		   FROM activity_buckets
		  WHERE server_id = ANY($1) AND state = 'active' AND data_tier = 1
		    AND bucket_start = (
		      SELECT max(bucket_start) FROM activity_buckets
		       WHERE server_id = ANY($1)
		         AND bucket_start >= $2 AND bucket_start < $3
		         AND data_tier = 1
		    )`,
		serverIDs, since, until,
	).Scan(&a.ActiveConns); err != nil {
		return a, err
	}

	// Dominant wait event over the whole window (most accumulated count).
	var wait string
	err := s.ro.QueryRow(ctx,
		`SELECT wait_event
		   FROM activity_buckets
		  WHERE server_id = ANY($1) AND wait_event_type <> '' AND data_tier = 1
		    AND bucket_start >= $2 AND bucket_start < $3
		  GROUP BY wait_event
		  ORDER BY SUM(count_sum) DESC
		  LIMIT 1`,
		serverIDs, since, until,
	).Scan(&wait)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return a, err
	}
	a.TopWait = wait // "" when ErrNoRows (nothing waiting)
	return a, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestActivitySummaryForServers`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/rollup.go internal/store/rollup_test.go
git commit -m "store(rollup): activity summary (active conns + top wait) for a server set (ly-yuc.1)"
```

---

## Task 6: `fleetview` — cluster summary aggregator

**Files:**
- Create: `internal/fleetview/summary.go`
- Test: `internal/fleetview/summary_test.go`

- [ ] **Step 1: Write the failing integration test**

Create `internal/fleetview/summary_test.go` (its own package; needs a local pool helper since `store_test.newPool` is unexported):

```go
package fleetview_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/dobbo-ca/lynceus/internal/fleetview"
	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/dobbo-ca/lynceus/internal/testpg"
)

// newPool starts a fresh postgres:16 container with BOTH the config and stats
// schemas applied (they share no table names), so one pool backs both a
// store.Config and a store.Stats — mirroring how the aggregator reads them.
func newPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	c, err := tcpostgres.Run(ctx, "postgres:16",
		tcpostgres.WithDatabase("lynceus_test"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		testpg.ReadyWait(),
	)
	if err != nil {
		t.Skipf("docker/testcontainers unavailable: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(c) })
	url, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("config migrate: %v", err)
	}
	if err := store.ApplyStatsMigrations(ctx, pool); err != nil {
		t.Fatalf("stats migrate: %v", err)
	}
	return pool
}

func TestListClusterSummaries_rollsUpAcrossStreams(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	cfg := store.NewConfig(pool)
	stats := store.NewStats(pool)

	// Fleet: one cluster, one instance, two server streams.
	for _, id := range []string{"srv-a", "srv-b"} {
		if _, err := pool.Exec(ctx, `INSERT INTO servers (id, name) VALUES ($1, $1)`, id); err != nil {
			t.Fatalf("seed server %s: %v", id, err)
		}
	}
	cl, err := cfg.CreateCluster(ctx, "prod")
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}
	inst, err := cfg.CreateInstance(ctx, cl.ID, "primary")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	for _, id := range []string{"srv-a", "srv-b"} {
		if err := cfg.AssignServerToInstance(ctx, id, inst.ID); err != nil {
			t.Fatalf("assign %s: %v", id, err)
		}
	}

	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	if err := stats.WriteQueryStats(ctx, []store.QueryStat{
		{ServerID: "srv-a", CollectedAt: now, Fingerprint: "fp-1", NormalizedQuery: "SELECT $1",
			Calls: 100, TotalTimeMs: 200, MeanTimeMs: 2.0},
		{ServerID: "srv-b", CollectedAt: now, Fingerprint: "fp-2", NormalizedQuery: "SELECT $1 FROM t",
			Calls: 300, TotalTimeMs: 30, MeanTimeMs: 0.1},
	}); err != nil {
		t.Fatalf("seed stats: %v", err)
	}
	if err := stats.WriteInsights(ctx, []store.InsightRow{
		{ServerID: "srv-a", CapturedAt: now, Kind: "slow_scan", Severity: "high",
			Fingerprint: "fp-1", Relation: "orders", NodePath: "Seq Scan(orders)",
			RowsReturned: 1, RowsScanned: 100000, Selectivity: 0.00001, Detail: "x"},
	}); err != nil {
		t.Fatalf("seed insights: %v", err)
	}

	since, until := now.Add(-time.Hour), now.Add(time.Hour)
	sums, err := fleetview.ListClusterSummaries(ctx, cfg, stats, since, until)
	if err != nil {
		t.Fatalf("ListClusterSummaries: %v", err)
	}
	if len(sums) != 1 {
		t.Fatalf("summaries = %d, want 1", len(sums))
	}
	s := sums[0]
	if s.Cluster.ID != cl.ID {
		t.Fatalf("cluster id = %q, want %q", s.Cluster.ID, cl.ID)
	}
	if s.InstanceCount != 1 || s.StreamCount != 2 {
		t.Fatalf("counts: instances=%d streams=%d, want 1/2", s.InstanceCount, s.StreamCount)
	}
	if s.Calls != 400 {
		t.Fatalf("calls = %d, want 400 (combined)", s.Calls)
	}
	// avg latency = 230ms / 400 calls = 0.575ms
	if s.AvgLatencyMs < 0.57 || s.AvgLatencyMs > 0.58 {
		t.Fatalf("avg latency = %v, want ~0.575", s.AvgLatencyMs)
	}
	if s.InsightCount != 1 {
		t.Fatalf("insight count = %d, want 1", s.InsightCount)
	}
}

func TestListClusterSummaries_clusterWithNoStreams(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	cfg := store.NewConfig(pool)
	stats := store.NewStats(pool)

	cl, err := cfg.CreateCluster(ctx, "empty")
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	sums, err := fleetview.ListClusterSummaries(ctx, cfg, stats, now.Add(-time.Hour), now.Add(time.Hour))
	if err != nil {
		t.Fatalf("ListClusterSummaries: %v", err)
	}
	if len(sums) != 1 || sums[0].Cluster.ID != cl.ID || sums[0].StreamCount != 0 || sums[0].Calls != 0 {
		t.Fatalf("empty cluster summary wrong: %+v", sums)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/fleetview/`
Expected: build failure — `package internal/fleetview: no Go files` / `fleetview.ListClusterSummaries undefined`.

- [ ] **Step 3: Write `internal/fleetview/summary.go`**

Create `internal/fleetview/summary.go`:

```go
// Package fleetview assembles UI view-models that span both stores: cluster
// topology lives in the config DB (store.Config) while metrics live in the
// stats DB (store.Stats), so the roll-up across a cluster's server streams is
// done here in Go rather than in a single SQL join.
package fleetview

import (
	"context"
	"time"

	"github.com/dobbo-ca/lynceus/internal/store"
)

// ClusterSummary is the dashboard view-model for one cluster: its identity plus
// metrics rolled up across all of its server streams (combined).
type ClusterSummary struct {
	Cluster       store.Cluster
	InstanceCount int
	StreamCount   int
	Calls         int64             // total calls across the cluster in the window
	AvgLatencyMs  float64           // SUM(total_time_ms)/SUM(calls); 0 if no calls
	QPSBuckets    []store.QPSBucket // hourly summed calls, for the sparkline
	ActiveConns   int64
	TopWait       string
	InsightCount  int
}

// ListClusterSummaries returns one summary per cluster, rolling stats up across
// each cluster's server_id set (resolved from the config DB).
func ListClusterSummaries(
	ctx context.Context, cfg *store.Config, stats *store.Stats, since, until time.Time,
) ([]ClusterSummary, error) {
	clusters, err := cfg.ListClusters(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]ClusterSummary, 0, len(clusters))
	for _, cl := range clusters {
		serverIDs, err := cfg.ServerIDsForCluster(ctx, cl.ID)
		if err != nil {
			return nil, err
		}
		instances, err := cfg.ListInstances(ctx, cl.ID)
		if err != nil {
			return nil, err
		}

		sum := ClusterSummary{
			Cluster:       cl,
			InstanceCount: len(instances),
			StreamCount:   len(serverIDs),
		}
		if len(serverIDs) == 0 {
			out = append(out, sum)
			continue
		}

		tp, err := stats.ThroughputForServers(ctx, serverIDs, since, until)
		if err != nil {
			return nil, err
		}
		sum.Calls = tp.Calls
		if tp.Calls > 0 {
			sum.AvgLatencyMs = tp.TotalTimeMs / float64(tp.Calls)
		}

		if sum.QPSBuckets, err = stats.QPSBucketsForServers(ctx, serverIDs, since, until); err != nil {
			return nil, err
		}

		act, err := stats.ActivitySummaryForServers(ctx, serverIDs, since, until)
		if err != nil {
			return nil, err
		}
		sum.ActiveConns = act.ActiveConns
		sum.TopWait = act.TopWait

		if sum.InsightCount, err = stats.InsightCountForServers(ctx, serverIDs, since, until); err != nil {
			return nil, err
		}

		out = append(out, sum)
	}
	return out, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/fleetview/`
Expected: PASS (both cases).

- [ ] **Step 5: Commit**

```bash
git add internal/fleetview/summary.go internal/fleetview/summary_test.go
git commit -m "fleetview: ListClusterSummaries rolls stats up per cluster (ly-yuc.1)"
```

---

## Task 7: Full verification + PR

- [ ] **Step 1: Whole suite with the race detector**

Run: `go test ./... -race`
Expected: all packages PASS. Integration tests use testcontainers; if Docker is unavailable they `t.Skip`, not fail.

- [ ] **Step 2: Lint clean**

Run: `~/go/bin/golangci-lint run` (v2.12.2)
Expected: no findings on the touched files. (`gocyclo` threshold is 20 — keep any test helper simple; split if needed.)

- [ ] **Step 3: Push and open the PR off origin/main**

```bash
git branch --show-current   # confirm THIS session's worktree branch (dogfood-dashboard-3a9f)
git push -u origin HEAD
gh pr create --base main \
  --title "feat(dogfood): Phase 1 — insights pipeline + cluster roll-up reads (ly-yuc.1)" \
  --body "<summary: server-side insight derivation at ingestion, insights table, server-set stats reads, fleetview aggregator, the collector-side->server-side reframe, testing>"
```

- [ ] **Step 4: Watch CI green, then move the bead**

```bash
gh pr checks <n> --watch
bd label remove ly-yuc.1 needs-plan && bd label add ly-yuc.1 ready-test
bd note ly-yuc.1 "PR #<n>: insights table + server-side derivation at ingestion + server-set rollup reads + fleetview aggregator. Reframe: insights derived server-side (not collector-emitted); collector plan-shipping + PlanetScale auto_explain deferred to ly-yuc.4. Unblocks Phase 2 (ly-yuc.2 dashboard)."
```

After merge: `bd close ly-yuc.1`.

---

## Self-review

**Spec coverage (Phase 1 slice of the design spec):**
- `insights` table (range-partitioned, T1, literal-free) → Task 1. ✓
- Persisted insights + write path → Task 2 (`WriteInsights`) + Task 3 (derivation at ingestion). ✓
- Insight compute location = **server-side derivation** (reframe; supersedes spec §4 collector-side) → Task 3, documented at top + in the bead note. ✓
- Cluster roll-up reads building on Fleet A `ServerIDsForCluster`: `ListClusterSummaries` (Task 6), per-cluster q/s series (`QPSBucketsForServers`), per-cluster top queries (`TopQueriesForServers`), per-cluster activity (`ActivitySummaryForServers`), per-cluster insights (`InsightCountForServers`/`TopInsightsForServers`). ✓
- Privacy: only T1 fields stored/read; insights derived from already-normalized plans; no new wire/proto field added (so the proto contract test is untouched and still holds). ✓
- Deferred (NOT in this plan, by decision): collector plan-shipping, new `Insight` proto, PlanetScale `auto_explain` verification → Phase 4 (ly-yuc.4). Noted. ✓
- Testing: testcontainers integration tests for the migration, writes/reads, ingestion derivation, and the cross-store aggregator. ✓

**Placeholder scan:** none — every step shows complete SQL/Go/commands. The only `<n>`/`<summary>` tokens are in the PR step (inherently interactive).

**Type consistency:** `InsightRow` fields are defined in Task 2 and used identically in Task 3 (`snapshotToInsights`) and Task 6 (test seed). `Throughput`/`QPSBucket` (Task 4) and `ActivitySummary` (Task 5) are consumed by `ClusterSummary`/`ListClusterSummaries` (Task 6) with matching field names. Method names (`WriteInsights`, `InsightCountForServers`, `TopInsightsForServers`, `ThroughputForServers`, `TopQueriesForServers`, `QPSBucketsForServers`, `ActivitySummaryForServers`) match across plan + tests. The `insightsColumns` COPY order matches the `CopyFromSlice` value order and the `0005_insights.sql` column order. `serverIDs []string` is passed as a pgx array param to every `= ANY($1)` query.
