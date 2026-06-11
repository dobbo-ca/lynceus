# M3 Checks Bundle — Connections (Active long-running, Idle Tx, Blocking) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add three scheduler-side connection health checks — long-running active queries, idle-in-transaction beyond a threshold, and blocking (A→B) — fed by a new T1 collector reader that ships per-backend durations and blocking pairs.

**Architecture:** The existing checks engine (`internal/checks`) evaluates pure `Check.Eval(in *Input)` predicates scheduler-side over the latest per-server stats. The blocker (`ly-xqf.1`) activity reader only ships **aggregated** `ActivityBucket` counts (per state/wait label) — it carries **no per-backend duration and no blocking relationship**, so none of these three checks are derivable from stored data today. We therefore add a new collector reader (`ConnectionsReader`) that reads `pg_stat_activity` durations and `pg_blocking_pids()` live, ships two new T1 messages (`ConnectionSample`, `BlockingEdge` — pids + durations + fixed state enums only, never query text), persists them in two partitioned stats tables, and surfaces them on `checks.Input`. Evaluation stays scheduler-side so the new checks inherit muting, the results table, severity, and notification uniformly with every other bundle (wraparound, index advisor).

**Tech Stack:** Go, protobuf (`make proto`), pgx/v5 + COPY, partitioned vanilla Postgres (RDS-safe, no extensions), testcontainers via `internal/testpg.ReadyWait()`, golangci-lint v2.12.2.

---

## Design Decision (resolves the bead's key question)

**Q: Can "long-running active" and "blocking (A→B)" be detected from stored data, or is a new field/read needed? Collector-side or scheduler-side evaluation?**

**A: A new collector read is required; evaluation stays scheduler-side.**

- The activity pipeline (`ActivityReader` → `ActivityBucket` → `activity_buckets`) aggregates to `count_sum`/`count_max` per `(database, state, wait_event_type, wait_event)` over a 60s window. It has **no pid, no duration, and no blocking edge**. From it you can derive *how many* connections are `idle in transaction`, but never *how long any one* has been, and **never** an A→B blocking relationship (`pg_blocking_pids()` is inherently a live, point-in-time read).
- So all three checks need data that does not exist yet. We ship it as new **T1** wire messages: `ConnectionSample` (pid, state enum, `active_seconds`/`xact_seconds`/`state_seconds`, `wait_event_type`) and `BlockingEdge` (`blocked_pid`, `blocker_pid`, `blocked_wait_seconds`). pids, durations, and fixed state strings are explicitly T1 per the privacy invariant (`identifiers (relation/database/pid)` + aggregate metrics); **no query text is ever selected**, mirroring `ActivityReader`'s deliberately-narrow column list.
- **Thresholds live in the checks (hardcoded constants), exactly like `WraparoundCheck`.** The collector ships the raw normalized metric; the scheduler-side check applies the threshold. This keeps the collector threshold-free and the bundle consistent with every existing check.
- The collector applies one privacy-neutral **performance** floor only: it ships a backend row only when `state_change < now() - interval '1 second'` (filtering out the high-churn short-query tail), capped at 500 rows. 1s is far below any check threshold (≥300s), so it never interferes with scheduler-side evaluation.

**Cadence:** connections are sampled point-in-time and shipped on the activity-flush cadence (60s, `cfg.activityFlush`), matching the 60s scheduler tick. (Coarser 10m `runFull` would miss minute-scale incidents; 10s sample cadence would over-ship.) The point-in-time sampling limitation — blocking that opens and clears entirely between two 60s reads is not observed — is documented and acceptable for MVP.

---

## File Structure

**Create:**
- `proto/lynceus/v1/snapshot.proto` (modify) — add `ConnectionSample`, `BlockingEdge` messages + Snapshot fields 10/11.
- `internal/store/migrations/stats/0011_connections.sql` — two partitioned tables.
- `internal/store/connections.go` — `ConnectionSampleRow`, `BlockingEdgeRow`, Write + Latest readers.
- `internal/store/connections_test.go` — roundtrip integration tests.
- `internal/collector/connections_reader.go` — `ConnectionsReader` (two live queries).
- `internal/collector/connections_reader_test.go` — integration test against real PG.
- `internal/checks/connections_active.go` — `ActiveLongRunningCheck`.
- `internal/checks/connections_idle.go` — `IdleInTransactionCheck`.
- `internal/checks/connections_blocking.go` — `BlockingCheck`.
- `internal/checks/connections_test.go` — pure Eval unit tests for all three.

**Modify:**
- `internal/proto/lynceus/v1/contract_test.go` — allowlists for the two new messages + Snapshot envelope.
- `internal/checks/checks.go` — add `Connections []ConnInfo` and `Blocking []BlockEdge` to `Input` + projection structs.
- `internal/checks/scheduler.go` — `assembleInput` reads `LatestConnectionSamples` + `LatestBlockingEdges`.
- `internal/ingest/server.go` — persist `connection_samples` + `blocking_edges` from the snapshot.
- `cmd/collector/main.go` — construct `ConnectionsReader`, ship on the flush tick.

---

## Task 1: Proto — ConnectionSample / BlockingEdge messages + Snapshot fields + contract tests

**Files:**
- Modify: `proto/lynceus/v1/snapshot.proto`
- Modify: `internal/proto/lynceus/v1/contract_test.go`

- [ ] **Step 1: Add the two messages and Snapshot fields to the proto**

In `proto/lynceus/v1/snapshot.proto`, add to the `Snapshot` message (after `freeze_ages = 9;`, before the closing brace at line ~64):

```proto
  // Per-backend connection observations from pg_stat_activity — durations +
  // pid + fixed state enum, NEVER query text. Feeds the Connections checks
  // (ly-u4t.22): long-running active + idle-in-transaction. T1.
  repeated ConnectionSample connection_samples = 10;

  // Blocking relationships (A blocks B) from pg_blocking_pids() — pids only.
  // Feeds the Connections blocking check (ly-u4t.22). T1.
  repeated BlockingEdge blocking_edges = 11;
```

Then add the two message definitions after the `ActivityBucket` message (after its closing brace, ~line 121):

```proto
// ConnectionSample is one point-in-time observation of a single client
// backend from pg_stat_activity. Like ActivityBucket it DELIBERATELY never
// carries the `query` column or any literal; only the backend pid, a fixed
// state label, integer durations, and the wait_event_type label travel.
// Live query text is the separate T2 connection-traces feature (ly-xqf.4).
//
// INVARIANT: every field is an identifier (pid), a fixed-vocabulary label
// (state, wait_event_type), or a non-negative duration COUNT in seconds.
message ConnectionSample {
  string server_id        = 1;
  int64  observed_at_unix = 2;
  int64  pid              = 3;  // backend pid — ephemeral OS identifier
  string state            = 4;  // active | idle in transaction | idle in transaction (aborted)
  int64  active_seconds   = 5;  // now - query_start  (current statement age)
  int64  xact_seconds     = 6;  // now - xact_start   (transaction age)
  int64  state_seconds    = 7;  // now - state_change (time in current state)
  string wait_event_type  = 8;  // pg_stat_activity.wait_event_type; "" if not waiting
}

// BlockingEdge is one A→B lock-wait relationship derived from
// pg_blocking_pids(). pids only — no relation name, no query text. T1.
message BlockingEdge {
  string server_id            = 1;
  int64  observed_at_unix     = 2;
  int64  blocked_pid          = 3;  // backend waiting on a lock
  int64  blocker_pid          = 4;  // backend holding the conflicting lock
  int64  blocked_wait_seconds = 5;  // now - blocked.state_change
}
```

- [ ] **Step 2: Regenerate Go from proto**

Run: `make proto`
Expected: `internal/proto/lynceus/v1/snapshot.pb.go` regenerates with `ConnectionSample`, `BlockingEdge` structs and `Snapshot.ConnectionSamples` / `Snapshot.BlockingEdges` fields. No error.

- [ ] **Step 3: Write the failing contract tests**

In `internal/proto/lynceus/v1/contract_test.go`, add three tests (place near the other T1 allowlist tests):

```go
// TestConnectionSampleHasOnlyAggregateFields enforces the T1 privacy guarantee
// for per-backend connection observations. ConnectionSample must carry only the
// backend pid, a fixed state/wait label, and integer durations — never the
// pg_stat_activity `query` column or any literal value.
func TestConnectionSampleHasOnlyAggregateFields(t *testing.T) {
	allowed := map[string]struct{}{
		"server_id": {}, "observed_at_unix": {}, "pid": {}, "state": {},
		"active_seconds": {}, "xact_seconds": {}, "state_seconds": {},
		"wait_event_type": {},
	}
	assertOnlyAllowed(t, (&lynceusv1.ConnectionSample{}).ProtoReflect().Descriptor().Fields(), allowed, "ConnectionSample")

	for _, name := range []string{"state", "wait_event_type"} {
		f := (&lynceusv1.ConnectionSample{}).ProtoReflect().Descriptor().Fields().ByName(protoreflect.Name(name))
		if f == nil {
			t.Fatalf("field %q missing from ConnectionSample", name)
		}
		if got := f.Kind().String(); got != "string" {
			t.Fatalf("ConnectionSample.%s must be string kind, got %s", name, got)
		}
	}
}

// TestBlockingEdgeHasOnlyPidFields enforces the T1 privacy guarantee for the
// blocking relationship message: pids and a wait duration only.
func TestBlockingEdgeHasOnlyPidFields(t *testing.T) {
	allowed := map[string]struct{}{
		"server_id": {}, "observed_at_unix": {},
		"blocked_pid": {}, "blocker_pid": {}, "blocked_wait_seconds": {},
	}
	assertOnlyAllowed(t, (&lynceusv1.BlockingEdge{}).ProtoReflect().Descriptor().Fields(), allowed, "BlockingEdge")
}
```

Also extend the existing `TestSnapshotCarriesLogEvents` allowlist map (the `allowed` map asserted against the `Snapshot` descriptor) to include the two new fields:

```go
		"connection_samples": {},
		"blocking_edges":     {},
```

- [ ] **Step 4: Run the contract tests**

Run: `go test ./internal/proto/... -run 'ConnectionSample|BlockingEdge|SnapshotCarries' -v`
Expected: PASS (messages exist, only allowlisted fields present).

- [ ] **Step 5: Commit**

```bash
git add proto/lynceus/v1/snapshot.proto internal/proto/lynceus/v1/
git commit -m "feat(proto): T1 ConnectionSample + BlockingEdge messages for Connections checks (ly-u4t.22)"
```

---

## Task 2: Store — connections tables + Write/Latest readers

**Files:**
- Create: `internal/store/migrations/stats/0011_connections.sql`
- Create: `internal/store/connections.go`
- Test: `internal/store/connections_test.go`

- [ ] **Step 1: Write the migration**

Create `internal/store/migrations/stats/0011_connections.sql`:

```sql
-- Per-backend connection observations + blocking edges from pg_stat_activity /
-- pg_blocking_pids(). Feeds the Connections checks (ly-u4t.22). Counts, pids,
-- and fixed state labels only — NEVER query text (T1, data_tier defaults to 1).
-- Range-partitioned by week on observed_at (vanilla Postgres, RDS-safe).

CREATE TABLE connection_samples (
    server_id        TEXT        NOT NULL,
    observed_at      TIMESTAMPTZ NOT NULL,
    pid              BIGINT      NOT NULL,
    state            TEXT        NOT NULL,
    active_seconds   BIGINT      NOT NULL,
    xact_seconds     BIGINT      NOT NULL,
    state_seconds    BIGINT      NOT NULL,
    wait_event_type  TEXT        NOT NULL,
    data_tier        SMALLINT    NOT NULL DEFAULT 1
) PARTITION BY RANGE (observed_at);

CREATE INDEX connection_samples_brin_time ON connection_samples USING brin (observed_at);
CREATE INDEX connection_samples_srv_time  ON connection_samples (server_id, observed_at);

CREATE TABLE blocking_edges (
    server_id            TEXT        NOT NULL,
    observed_at          TIMESTAMPTZ NOT NULL,
    blocked_pid          BIGINT      NOT NULL,
    blocker_pid          BIGINT      NOT NULL,
    blocked_wait_seconds BIGINT      NOT NULL,
    data_tier            SMALLINT    NOT NULL DEFAULT 1
) PARTITION BY RANGE (observed_at);

CREATE INDEX blocking_edges_brin_time ON blocking_edges USING brin (observed_at);
CREATE INDEX blocking_edges_srv_time  ON blocking_edges (server_id, observed_at);
```

- [ ] **Step 2: Write the store roundtrip test (failing)**

Create `internal/store/connections_test.go`:

```go
package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/dobbo-ca/lynceus/internal/store"
)

func TestWriteConnectionSamples_createsPartitionAndRoundtrips(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyStatsMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := store.NewStats(pool)

	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC) // a Tuesday
	older := now.Add(-1 * time.Minute)

	// An older batch that must be shadowed by the latest read.
	if err := s.WriteConnectionSamples(ctx, []store.ConnectionSampleRow{
		{ServerID: "srv-a", ObservedAt: older, PID: 1, State: "active", ActiveSeconds: 10},
	}); err != nil {
		t.Fatalf("write older: %v", err)
	}
	rows := []store.ConnectionSampleRow{
		{ServerID: "srv-a", ObservedAt: now, PID: 42, State: "active", ActiveSeconds: 600, XactSeconds: 650, StateSeconds: 600, WaitEventType: "Lock"},
		{ServerID: "srv-a", ObservedAt: now, PID: 43, State: "idle in transaction", ActiveSeconds: 5, XactSeconds: 400, StateSeconds: 350},
	}
	if err := s.WriteConnectionSamples(ctx, rows); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := s.LatestConnectionSamples(ctx, "srv-a", now)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 latest rows, got %d: %+v", len(got), got)
	}
	byPID := map[int64]store.ConnectionSampleRow{}
	for _, r := range got {
		byPID[r.PID] = r
	}
	if r := byPID[42]; r.State != "active" || r.ActiveSeconds != 600 || r.WaitEventType != "Lock" {
		t.Fatalf("pid 42 not preserved: %+v", r)
	}
	if r := byPID[43]; r.State != "idle in transaction" || r.StateSeconds != 350 {
		t.Fatalf("pid 43 not preserved: %+v", r)
	}
}

func TestWriteBlockingEdges_createsPartitionAndRoundtrips(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyStatsMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := store.NewStats(pool)

	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	if err := s.WriteBlockingEdges(ctx, []store.BlockingEdgeRow{
		{ServerID: "srv-a", ObservedAt: now, BlockedPID: 43, BlockerPID: 42, BlockedWaitSeconds: 90},
	}); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := s.LatestBlockingEdges(ctx, "srv-a", now)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 1 || got[0].BlockedPID != 43 || got[0].BlockerPID != 42 || got[0].BlockedWaitSeconds != 90 {
		t.Fatalf("edge not preserved: %+v", got)
	}
}
```

- [ ] **Step 3: Run to verify it fails**

Run: `go test ./internal/store/ -run 'ConnectionSamples|BlockingEdges' -v`
Expected: FAIL — `undefined: store.ConnectionSampleRow` etc. (won't compile).

- [ ] **Step 4: Implement the store**

Create `internal/store/connections.go` (mirrors `freeze_ages.go`):

```go
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// ConnectionSampleRow is one T1 point-in-time pg_stat_activity backend
// observation: pid, fixed state label, integer durations — never query text.
// DataTier zero is coerced to 1 (T1) on insert.
type ConnectionSampleRow struct {
	ServerID      string
	ObservedAt    time.Time
	PID           int64
	State         string
	ActiveSeconds int64
	XactSeconds   int64
	StateSeconds  int64
	WaitEventType string
	DataTier      int16 // 0 -> coerced to 1
}

// BlockingEdgeRow is one T1 A→B lock-wait relationship from pg_blocking_pids().
type BlockingEdgeRow struct {
	ServerID           string
	ObservedAt         time.Time
	BlockedPID         int64
	BlockerPID         int64
	BlockedWaitSeconds int64
	DataTier           int16 // 0 -> coerced to 1
}

var connectionSamplesColumns = []string{
	"server_id", "observed_at", "pid", "state",
	"active_seconds", "xact_seconds", "state_seconds", "wait_event_type", "data_tier",
}

var blockingEdgesColumns = []string{
	"server_id", "observed_at", "blocked_pid", "blocker_pid", "blocked_wait_seconds", "data_tier",
}

// WriteConnectionSamples appends a batch via COPY, creating any missing weekly
// partitions first. Empty input is a no-op. Mirrors WriteFreezeAges.
func (s *Stats) WriteConnectionSamples(ctx context.Context, rows []ConnectionSampleRow) error {
	if len(rows) == 0 {
		return nil
	}
	weeks := map[string]time.Time{}
	for i := range rows {
		weeks[connectionSamplesPartitionName(rows[i].ObservedAt)] = rows[i].ObservedAt
	}
	for _, ts := range weeks {
		if err := s.ensureWeeklyPartition(ctx, "connection_samples", connectionSamplesPartitionName(ts), ts); err != nil {
			return err
		}
	}
	src := pgx.CopyFromSlice(len(rows), func(i int) ([]any, error) {
		r := rows[i]
		if r.DataTier == 0 {
			r.DataTier = 1
		}
		return []any{
			r.ServerID, r.ObservedAt, r.PID, r.State,
			r.ActiveSeconds, r.XactSeconds, r.StateSeconds, r.WaitEventType, r.DataTier,
		}, nil
	})
	_, err := s.pool.CopyFrom(ctx, pgx.Identifier{"connection_samples"}, connectionSamplesColumns, src)
	return err
}

// WriteBlockingEdges appends a batch via COPY, creating partitions first.
func (s *Stats) WriteBlockingEdges(ctx context.Context, rows []BlockingEdgeRow) error {
	if len(rows) == 0 {
		return nil
	}
	weeks := map[string]time.Time{}
	for i := range rows {
		weeks[blockingEdgesPartitionName(rows[i].ObservedAt)] = rows[i].ObservedAt
	}
	for _, ts := range weeks {
		if err := s.ensureWeeklyPartition(ctx, "blocking_edges", blockingEdgesPartitionName(ts), ts); err != nil {
			return err
		}
	}
	src := pgx.CopyFromSlice(len(rows), func(i int) ([]any, error) {
		r := rows[i]
		if r.DataTier == 0 {
			r.DataTier = 1
		}
		return []any{
			r.ServerID, r.ObservedAt, r.BlockedPID, r.BlockerPID, r.BlockedWaitSeconds, r.DataTier,
		}, nil
	})
	_, err := s.pool.CopyFrom(ctx, pgx.Identifier{"blocking_edges"}, blockingEdgesColumns, src)
	return err
}

// ensureWeeklyPartition creates the weekly partition `name` of `parent` for ts
// if absent. Idempotent. Shared by the two connections tables.
func (s *Stats) ensureWeeklyPartition(ctx context.Context, parent, name string, ts time.Time) error {
	from, to := isoWeekBounds(ts)
	_, err := s.pool.Exec(ctx, fmt.Sprintf(
		`CREATE TABLE IF NOT EXISTS %s PARTITION OF %s FOR VALUES FROM ('%s') TO ('%s')`,
		name, parent, from.Format("2006-01-02"), to.Format("2006-01-02"),
	))
	return err
}

func connectionSamplesPartitionName(ts time.Time) string {
	y, w := ts.UTC().ISOWeek()
	return fmt.Sprintf("connection_samples_%04d_%02d", y, w)
}

func blockingEdgesPartitionName(ts time.Time) string {
	y, w := ts.UTC().ISOWeek()
	return fmt.Sprintf("blocking_edges_%04d_%02d", y, w)
}

const connectionSamplesSelect = `SELECT server_id, observed_at, pid, state,
        active_seconds, xact_seconds, state_seconds, wait_event_type, data_tier
   FROM connection_samples`

// LatestConnectionSamples returns the most-recent observation batch (all rows
// sharing the max observed_at) for serverID at or before asOf. Served from the
// read replica. data_tier = 1 only (T1).
func (s *Stats) LatestConnectionSamples(ctx context.Context, serverID string, asOf time.Time) ([]ConnectionSampleRow, error) {
	rows, err := s.ro.Query(ctx,
		connectionSamplesSelect+`
		  WHERE server_id = $1 AND data_tier = 1
		    AND observed_at = (
		        SELECT max(observed_at) FROM connection_samples
		         WHERE server_id = $1 AND observed_at <= $2 AND data_tier = 1)
		  ORDER BY pid`,
		serverID, asOf,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ConnectionSampleRow
	for rows.Next() {
		var r ConnectionSampleRow
		if err := rows.Scan(&r.ServerID, &r.ObservedAt, &r.PID, &r.State,
			&r.ActiveSeconds, &r.XactSeconds, &r.StateSeconds, &r.WaitEventType, &r.DataTier); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

const blockingEdgesSelect = `SELECT server_id, observed_at, blocked_pid, blocker_pid, blocked_wait_seconds, data_tier
   FROM blocking_edges`

// LatestBlockingEdges returns the most-recent blocking batch for serverID at or
// before asOf. data_tier = 1 only (T1).
func (s *Stats) LatestBlockingEdges(ctx context.Context, serverID string, asOf time.Time) ([]BlockingEdgeRow, error) {
	rows, err := s.ro.Query(ctx,
		blockingEdgesSelect+`
		  WHERE server_id = $1 AND data_tier = 1
		    AND observed_at = (
		        SELECT max(observed_at) FROM blocking_edges
		         WHERE server_id = $1 AND observed_at <= $2 AND data_tier = 1)
		  ORDER BY blocked_pid, blocker_pid`,
		serverID, asOf,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BlockingEdgeRow
	for rows.Next() {
		var r BlockingEdgeRow
		if err := rows.Scan(&r.ServerID, &r.ObservedAt, &r.BlockedPID, &r.BlockerPID,
			&r.BlockedWaitSeconds, &r.DataTier); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
```

> **Note:** confirm `isoWeekBounds`, `s.pool`, and `s.ro` exist in package `store` (they back `freeze_ages.go`). If `ensureWeeklyPartition` collides with an existing helper name, rename to `ensureConnWeeklyPartition`.

- [ ] **Step 5: Run to verify it passes**

Run: `go test ./internal/store/ -run 'ConnectionSamples|BlockingEdges' -v`
Expected: PASS (both roundtrip + latest-batch shadowing).

- [ ] **Step 6: Commit**

```bash
git add internal/store/migrations/stats/0011_connections.sql internal/store/connections.go internal/store/connections_test.go
git commit -m "feat(store): connection_samples + blocking_edges partitioned tables with Latest readers (ly-u4t.22)"
```

---

## Task 3: Collector — ConnectionsReader (live pg_stat_activity durations + pg_blocking_pids)

**Files:**
- Create: `internal/collector/connections_reader.go`
- Test: `internal/collector/connections_reader_test.go`

- [ ] **Step 1: Write the failing integration test**

Create `internal/collector/connections_reader_test.go`. It opens a second blocking transaction in a goroutine, then asserts the reader sees the active duration row and the blocking edge. (Model the container setup on `activity_reader_test.go`.)

```go
package collector_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/dobbo-ca/lynceus/internal/caps"
	"github.com/dobbo-ca/lynceus/internal/collector"
	"github.com/dobbo-ca/lynceus/internal/testpg"
)

func TestConnectionsReader_seesDurationsAndBlocking(t *testing.T) {
	ctx := context.Background()
	ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("app"), tcpostgres.WithUsername("u"), tcpostgres.WithPassword("p"),
		testpg.ReadyWait())
	if err != nil {
		t.Fatalf("start container: %v", err)
	}
	defer func() { _ = ctr.Terminate(ctx) }()

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("dsn: %v", err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()

	if _, err := pool.Exec(ctx, `CREATE TABLE t (id int primary key); INSERT INTO t VALUES (1)`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Session A: hold a row lock inside an open transaction (becomes idle-in-txn).
	connA, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire A: %v", err)
	}
	defer connA.Release()
	if _, err := connA.Exec(ctx, `BEGIN`); err != nil {
		t.Fatalf("A begin: %v", err)
	}
	if _, err := connA.Exec(ctx, `UPDATE t SET id = id WHERE id = 1`); err != nil {
		t.Fatalf("A update: %v", err)
	}

	// Session B: try to update the same row → blocks on A.
	connB, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire B: %v", err)
	}
	defer connB.Release()
	go func() {
		_, _ = connB.Exec(ctx, `UPDATE t SET id = id WHERE id = 1`)
	}()

	// Give B time to enter the lock wait.
	deadline := time.Now().Add(10 * time.Second)
	r := collector.NewConnectionsReader(pool, caps.NewGate(), "app")
	var (
		samples []collector.ConnectionSample
		edges   []collector.BlockingPair
	)
	for time.Now().Before(deadline) {
		samples, edges, err = r.Read(ctx)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if len(edges) > 0 {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}

	if len(edges) == 0 {
		t.Fatalf("expected at least one blocking edge, got none (samples=%+v)", samples)
	}
	// The blocking edge's blocker must differ from the blocked pid.
	if edges[0].BlockerPID == edges[0].BlockedPID || edges[0].BlockerPID == 0 {
		t.Fatalf("bad blocking edge: %+v", edges[0])
	}
}

func TestConnectionsReader_gatedOff(t *testing.T) {
	ctx := context.Background()
	gate := caps.NewGate()
	gate.Replace(map[caps.Key]bool{{DB: "app", Cap: caps.PgStatActivityFullRead}: false})
	r := collector.NewConnectionsReader(nil, gate, "app")
	samples, edges, err := r.Read(ctx)
	if err != nil || samples != nil || edges != nil {
		t.Fatalf("gated-off reader must no-op, got samples=%v edges=%v err=%v", samples, edges, err)
	}
}
```

> **Note:** match the exact gate-key construction used by `gated_reader_test.go` / `caps` (the `Replace` payload shape and `Allowed` signature). If `caps.Key`/`caps.NewGate().Replace(...)` differ, copy the form already used in `activity_reader`'s test. The gate-off test must exercise the same early-return branch as `ActivityReader.Read`.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/collector/ -run ConnectionsReader -v`
Expected: FAIL — `undefined: collector.NewConnectionsReader` (won't compile).

- [ ] **Step 3: Implement the reader**

Create `internal/collector/connections_reader.go` (mirrors `activity_reader.go`):

```go
// internal/collector/connections_reader.go
package collector

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dobbo-ca/lynceus/internal/caps"
)

// ConnectionsReader samples per-backend durations and lock-wait relationships
// from pg_stat_activity / pg_blocking_pids() for the Connections checks
// (ly-u4t.22). Like ActivityReader, the column lists DELIBERATELY exclude
// `query`, `query_id`, and every other literal-bearing column: only the backend
// pid, a fixed state label, integer durations, and wait_event_type travel. Live
// query text is the separate T2 connection-traces feature (ly-xqf.4).
type ConnectionsReader struct {
	pool *pgxpool.Pool
	gate *caps.Gate
	db   string // current_database() of pool, the gate key
}

// ConnectionSample is one point-in-time backend observation (durations in
// whole seconds). It carries no literal.
type ConnectionSample struct {
	PID           int64
	State         string
	ActiveSeconds int64
	XactSeconds   int64
	StateSeconds  int64
	WaitEventType string
}

// BlockingPair is one A→B lock-wait relationship; pids only.
type BlockingPair struct {
	BlockedPID         int64
	BlockerPID         int64
	BlockedWaitSeconds int64
}

func NewConnectionsReader(pool *pgxpool.Pool, gate *caps.Gate, db string) *ConnectionsReader {
	return &ConnectionsReader{pool: pool, gate: gate, db: db}
}

// connSamplesSQL selects "notable" client backends only: those whose current
// state has held for >1s. The 1s floor is a privacy-neutral performance bound
// (sheds the high-churn short-query tail) far below any check threshold.
const connSamplesSQL = `
SELECT pid,
       COALESCE(state, '')                                              AS state,
       COALESCE(EXTRACT(EPOCH FROM (now() - query_start))::bigint, 0)   AS active_seconds,
       COALESCE(EXTRACT(EPOCH FROM (now() - xact_start))::bigint, 0)    AS xact_seconds,
       COALESCE(EXTRACT(EPOCH FROM (now() - state_change))::bigint, 0)  AS state_seconds,
       COALESCE(wait_event_type, '')                                    AS wait_event_type
  FROM pg_stat_activity
 WHERE backend_type = 'client backend'
   AND state IN ('active', 'idle in transaction', 'idle in transaction (aborted)')
   AND state_change < now() - interval '1 second'
 ORDER BY state_change ASC
 LIMIT 500`

// connBlockingSQL derives A→B edges from pg_blocking_pids(). pids only.
const connBlockingSQL = `
SELECT blocked.pid                                                       AS blocked_pid,
       bp.pid                                                            AS blocker_pid,
       COALESCE(EXTRACT(EPOCH FROM (now() - blocked.state_change))::bigint, 0) AS blocked_wait_seconds
  FROM pg_stat_activity blocked
  CROSS JOIN LATERAL unnest(pg_blocking_pids(blocked.pid)) AS bp(pid)
 WHERE blocked.backend_type = 'client backend'
 LIMIT 500`

// Read returns notable backend samples and blocking pairs observed now. Returns
// (nil, nil, nil) when the pg_stat_activity capability is gated off — identical
// to ActivityReader.Read.
func (r *ConnectionsReader) Read(ctx context.Context) ([]ConnectionSample, []BlockingPair, error) {
	if !r.gate.Allowed(r.db, caps.PgStatActivityFullRead) {
		return nil, nil, nil
	}

	sRows, err := r.pool.Query(ctx, connSamplesSQL)
	if err != nil {
		return nil, nil, fmt.Errorf("query connection samples: %w", err)
	}
	var samples []ConnectionSample
	for sRows.Next() {
		var s ConnectionSample
		if err := sRows.Scan(&s.PID, &s.State, &s.ActiveSeconds, &s.XactSeconds, &s.StateSeconds, &s.WaitEventType); err != nil {
			sRows.Close()
			return nil, nil, fmt.Errorf("scan connection sample: %w", err)
		}
		samples = append(samples, s)
	}
	sRows.Close()
	if err := sRows.Err(); err != nil {
		return nil, nil, err
	}

	bRows, err := r.pool.Query(ctx, connBlockingSQL)
	if err != nil {
		return nil, nil, fmt.Errorf("query blocking edges: %w", err)
	}
	defer bRows.Close()
	var edges []BlockingPair
	for bRows.Next() {
		var e BlockingPair
		if err := bRows.Scan(&e.BlockedPID, &e.BlockerPID, &e.BlockedWaitSeconds); err != nil {
			return nil, nil, fmt.Errorf("scan blocking edge: %w", err)
		}
		edges = append(edges, e)
	}
	return samples, edges, bRows.Err()
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/collector/ -run ConnectionsReader -v`
Expected: PASS — the blocking edge is observed; the gated-off reader no-ops.

- [ ] **Step 5: Commit**

```bash
git add internal/collector/connections_reader.go internal/collector/connections_reader_test.go
git commit -m "feat(collector): ConnectionsReader — T1 per-backend durations + pg_blocking_pids edges (ly-u4t.22)"
```

---

## Task 4: Collector wiring — ship connections on the flush cadence

**Files:**
- Modify: `cmd/collector/main.go`

- [ ] **Step 1: Construct the reader**

In `cmd/collector/main.go`, after the `activityReader := ...` line (~line 44), add:

```go
	connectionsReader := collector.NewConnectionsReader(pool, gate, db)
```

- [ ] **Step 2: Ship connections inside the flush function**

Extend `flushActivity` (the 60s flush) so the same tick also samples + ships connections. Add this block at the end of `flushActivity`, before its closing brace (after the activity ship `log.Printf(...)`):

```go
		samples, edges, err := connectionsReader.Read(ctx)
		if err != nil {
			log.Printf("read connections: %v", err)
			return
		}
		if len(samples) == 0 && len(edges) == 0 {
			return
		}
		nowUnix := time.Now().Unix()
		protoSamples := make([]*lynceusv1.ConnectionSample, 0, len(samples))
		for i := range samples {
			s := &samples[i]
			protoSamples = append(protoSamples, &lynceusv1.ConnectionSample{
				ServerId: cfg.serverID, ObservedAtUnix: nowUnix, Pid: s.PID, State: s.State,
				ActiveSeconds: s.ActiveSeconds, XactSeconds: s.XactSeconds,
				StateSeconds: s.StateSeconds, WaitEventType: s.WaitEventType,
			})
		}
		protoEdges := make([]*lynceusv1.BlockingEdge, 0, len(edges))
		for i := range edges {
			e := &edges[i]
			protoEdges = append(protoEdges, &lynceusv1.BlockingEdge{
				ServerId: cfg.serverID, ObservedAtUnix: nowUnix,
				BlockedPid: e.BlockedPID, BlockerPid: e.BlockerPID, BlockedWaitSeconds: e.BlockedWaitSeconds,
			})
		}
		connSnap := &lynceusv1.Snapshot{
			ServerId: cfg.serverID, CollectedAtUnix: nowUnix,
			ConnectionSamples: protoSamples, BlockingEdges: protoEdges,
		}
		if err := shipper.Send(ctx, connSnap); err != nil {
			log.Printf("ship connections: %v", err)
			return
		}
		log.Printf("shipped %d connection_samples, %d blocking_edges", len(protoSamples), len(protoEdges))
```

> **Note:** if `flushActivity` early-returns when `len(buckets) == 0`, connections would never ship on idle servers. Restructure so the connections block runs regardless of bucket count — e.g. drop the early `return` on empty buckets and guard only the activity-ship portion with `if len(protoBuckets) > 0 { ... }`, leaving the connections block to always run. Verify the function still compiles and the activity path is unchanged when buckets are present.

- [ ] **Step 3: Build the collector**

Run: `go build ./cmd/collector/`
Expected: builds clean.

- [ ] **Step 4: Commit**

```bash
git add cmd/collector/main.go
git commit -m "feat(collector): sample + ship connection_samples/blocking_edges on flush cadence (ly-u4t.22)"
```

---

## Task 5: Ingestion — persist connection_samples + blocking_edges

**Files:**
- Modify: `internal/ingest/server.go`

- [ ] **Step 1: Add the persist calls**

In `internal/ingest/server.go`, after the freeze-ages (or last) persist block inside the snapshot handler (mirror the `activity_buckets` block at ~line 114), add:

```go
	if cs := snapshotToConnectionSamples(&snap); len(cs) > 0 {
		if err := s.stats.WriteConnectionSamples(ctx, cs); err != nil {
			s.parkDLQ(ctx, snap.ServerId, "write connection_samples: "+err.Error(), data)
			_ = conn.Close(websocket.StatusInternalError, "")
			return
		}
	}
	if be := snapshotToBlockingEdges(&snap); len(be) > 0 {
		if err := s.stats.WriteBlockingEdges(ctx, be); err != nil {
			s.parkDLQ(ctx, snap.ServerId, "write blocking_edges: "+err.Error(), data)
			_ = conn.Close(websocket.StatusInternalError, "")
			return
		}
	}
```

- [ ] **Step 2: Add the mapper functions**

Near `snapshotToActivityBuckets` (~line 340), add:

```go
func snapshotToConnectionSamples(snap *lynceusv1.Snapshot) []store.ConnectionSampleRow {
	out := make([]store.ConnectionSampleRow, 0, len(snap.ConnectionSamples))
	for _, c := range snap.ConnectionSamples {
		out = append(out, store.ConnectionSampleRow{
			ServerID:      snap.ServerId,
			ObservedAt:    time.Unix(c.ObservedAtUnix, 0).UTC(),
			PID:           c.Pid,
			State:         c.State,
			ActiveSeconds: c.ActiveSeconds,
			XactSeconds:   c.XactSeconds,
			StateSeconds:  c.StateSeconds,
			WaitEventType: c.WaitEventType,
			DataTier:      1,
		})
	}
	return out
}

func snapshotToBlockingEdges(snap *lynceusv1.Snapshot) []store.BlockingEdgeRow {
	out := make([]store.BlockingEdgeRow, 0, len(snap.BlockingEdges))
	for _, e := range snap.BlockingEdges {
		out = append(out, store.BlockingEdgeRow{
			ServerID:           snap.ServerId,
			ObservedAt:         time.Unix(e.ObservedAtUnix, 0).UTC(),
			BlockedPID:         e.BlockedPid,
			BlockerPID:         e.BlockerPid,
			BlockedWaitSeconds: e.BlockedWaitSeconds,
			DataTier:           1,
		})
	}
	return out
}
```

- [ ] **Step 3: Build + run ingest tests**

Run: `go build ./internal/ingest/ && go test ./internal/ingest/ -run Activity -v`
Expected: builds clean; existing ingest tests still pass.

- [ ] **Step 4: Commit**

```bash
git add internal/ingest/server.go
git commit -m "feat(ingest): persist connection_samples + blocking_edges from snapshot (ly-u4t.22)"
```

---

## Task 6: Checks — Input fields + scheduler assembly

**Files:**
- Modify: `internal/checks/checks.go`
- Modify: `internal/checks/scheduler.go`

- [ ] **Step 1: Add projection structs + Input fields**

In `internal/checks/checks.go`, add to the `Input` struct (after `IndexRecs`):

```go
	Connections []ConnInfo  // populated by the scheduler (ly-u4t.22)
	Blocking    []BlockEdge // populated by the scheduler (ly-u4t.22)
```

And add the two projection types (after `FreezeInfo`):

```go
// ConnInfo is the check-local projection of store.ConnectionSampleRow.
type ConnInfo struct {
	PID           int64
	State         string
	ActiveSeconds int64
	XactSeconds   int64
	StateSeconds  int64
	WaitEventType string
}

// BlockEdge is the check-local projection of store.BlockingEdgeRow.
type BlockEdge struct {
	BlockedPID         int64
	BlockerPID         int64
	BlockedWaitSeconds int64
}
```

- [ ] **Step 2: Read them in assembleInput**

In `internal/checks/scheduler.go`, inside `assembleInput`, after the `LatestFreezeAges` block (before the Index Advisor block), add:

```go
	conns, err := sc.stats.LatestConnectionSamples(ctx, serverID, now)
	if err != nil {
		return in, err
	}
	for i := range conns {
		c := &conns[i]
		in.Connections = append(in.Connections, ConnInfo{
			PID: c.PID, State: c.State, ActiveSeconds: c.ActiveSeconds,
			XactSeconds: c.XactSeconds, StateSeconds: c.StateSeconds, WaitEventType: c.WaitEventType,
		})
	}
	edges, err := sc.stats.LatestBlockingEdges(ctx, serverID, now)
	if err != nil {
		return in, err
	}
	for i := range edges {
		e := &edges[i]
		in.Blocking = append(in.Blocking, BlockEdge{
			BlockedPID: e.BlockedPID, BlockerPID: e.BlockerPID, BlockedWaitSeconds: e.BlockedWaitSeconds,
		})
	}
```

- [ ] **Step 3: Build**

Run: `go build ./internal/checks/`
Expected: builds clean (no check consumes the new fields yet — that's Tasks 7-9).

- [ ] **Step 4: Commit**

```bash
git add internal/checks/checks.go internal/checks/scheduler.go
git commit -m "feat(checks): surface connection samples + blocking edges on Input (ly-u4t.22)"
```

---

## Task 7: Check — long-running active queries

**Files:**
- Create: `internal/checks/connections_active.go`
- Test: `internal/checks/connections_test.go`

- [ ] **Step 1: Write the failing unit test**

Create `internal/checks/connections_test.go` with the active-check cases (it will grow in Tasks 8-9):

```go
package checks

import "testing"

func TestActiveLongRunningCheck_severityLadder(t *testing.T) {
	in := &Input{Connections: []ConnInfo{
		{PID: 1, State: "active", ActiveSeconds: 50},   // below warn
		{PID: 2, State: "active", ActiveSeconds: 400},  // warning
		{PID: 3, State: "active", ActiveSeconds: 1000}, // critical
		{PID: 4, State: "idle in transaction", ActiveSeconds: 9999}, // wrong state, ignored
	}}
	got := ActiveLongRunningCheck{}.Eval(in)
	if len(got) != 2 {
		t.Fatalf("want 2 firing results, got %d: %+v", len(got), got)
	}
	bySev := map[Severity]Result{}
	for _, r := range got {
		bySev[r.Severity] = r
		if r.CheckID != "connections.long_running_active" || r.Category != "connections" || r.Status != StatusFiring {
			t.Fatalf("bad result shape: %+v", r)
		}
	}
	if _, ok := bySev[SeverityWarning]; !ok {
		t.Fatalf("missing warning result: %+v", got)
	}
	if _, ok := bySev[SeverityCritical]; !ok {
		t.Fatalf("missing critical result: %+v", got)
	}
	if got := bySev[SeverityCritical].Object; got != "pid:3" {
		t.Fatalf("critical Object = %q, want pid:3", got)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/checks/ -run TestActiveLongRunningCheck -v`
Expected: FAIL — `undefined: ActiveLongRunningCheck`.

- [ ] **Step 3: Implement the check**

Create `internal/checks/connections_active.go`:

```go
package checks

import "fmt"

func init() { Register(ActiveLongRunningCheck{}) }

// ActiveLongRunningCheck flags client backends whose CURRENT statement has been
// executing longer than a threshold (a runaway query, a missing index scan, or
// a stuck lock-waiter). Duration only — T1.
type ActiveLongRunningCheck struct{}

const (
	activeWarnSeconds     = 300 // 5 min
	activeCriticalSeconds = 900 // 15 min
)

func (ActiveLongRunningCheck) ID() string       { return "connections.long_running_active" }
func (ActiveLongRunningCheck) Category() string { return "connections" }

func (ActiveLongRunningCheck) Eval(in *Input) []Result {
	var out []Result
	for _, c := range in.Connections {
		if c.State != "active" {
			continue
		}
		var sev Severity
		switch {
		case c.ActiveSeconds >= activeCriticalSeconds:
			sev = SeverityCritical
		case c.ActiveSeconds >= activeWarnSeconds:
			sev = SeverityWarning
		default:
			continue
		}
		out = append(out, Result{
			CheckID:  "connections.long_running_active",
			Category: "connections",
			Severity: sev,
			Status:   StatusFiring,
			Object:   fmt.Sprintf("pid:%d", c.PID),
			Detail:   fmt.Sprintf("active query running %ds (wait_event_type=%q)", c.ActiveSeconds, c.WaitEventType),
		})
	}
	return out
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/checks/ -run TestActiveLongRunningCheck -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/checks/connections_active.go internal/checks/connections_test.go
git commit -m "feat(checks): connections.long_running_active check (ly-u4t.22)"
```

---

## Task 8: Check — idle in transaction beyond threshold

**Files:**
- Create: `internal/checks/connections_idle.go`
- Modify: `internal/checks/connections_test.go`

- [ ] **Step 1: Add the failing unit test**

Append to `internal/checks/connections_test.go`:

```go
func TestIdleInTransactionCheck_severityLadder(t *testing.T) {
	in := &Input{Connections: []ConnInfo{
		{PID: 1, State: "idle in transaction", StateSeconds: 50},             // below warn
		{PID: 2, State: "idle in transaction", StateSeconds: 400},            // warning
		{PID: 3, State: "idle in transaction (aborted)", StateSeconds: 1000}, // critical
		{PID: 4, State: "active", StateSeconds: 9999},                        // wrong state, ignored
	}}
	got := IdleInTransactionCheck{}.Eval(in)
	if len(got) != 2 {
		t.Fatalf("want 2 firing results, got %d: %+v", len(got), got)
	}
	for _, r := range got {
		if r.CheckID != "connections.idle_in_transaction" || r.Category != "connections" || r.Status != StatusFiring {
			t.Fatalf("bad result shape: %+v", r)
		}
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/checks/ -run TestIdleInTransactionCheck -v`
Expected: FAIL — `undefined: IdleInTransactionCheck`.

- [ ] **Step 3: Implement the check**

Create `internal/checks/connections_idle.go`:

```go
package checks

import "fmt"

func init() { Register(IdleInTransactionCheck{}) }

// IdleInTransactionCheck flags backends sitting idle inside an open transaction
// past a threshold. These hold the xmin horizon (blocking VACUUM) and any locks
// the transaction already took, so a long idle-in-txn is a real availability
// hazard. Time-in-state only — T1.
type IdleInTransactionCheck struct{}

const (
	idleTxnWarnSeconds     = 300 // 5 min
	idleTxnCriticalSeconds = 900 // 15 min
)

func (IdleInTransactionCheck) ID() string       { return "connections.idle_in_transaction" }
func (IdleInTransactionCheck) Category() string { return "connections" }

func (IdleInTransactionCheck) Eval(in *Input) []Result {
	var out []Result
	for _, c := range in.Connections {
		if c.State != "idle in transaction" && c.State != "idle in transaction (aborted)" {
			continue
		}
		var sev Severity
		switch {
		case c.StateSeconds >= idleTxnCriticalSeconds:
			sev = SeverityCritical
		case c.StateSeconds >= idleTxnWarnSeconds:
			sev = SeverityWarning
		default:
			continue
		}
		out = append(out, Result{
			CheckID:  "connections.idle_in_transaction",
			Category: "connections",
			Severity: sev,
			Status:   StatusFiring,
			Object:   fmt.Sprintf("pid:%d", c.PID),
			Detail:   fmt.Sprintf("%s for %ds (xact age %ds)", c.State, c.StateSeconds, c.XactSeconds),
		})
	}
	return out
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/checks/ -run TestIdleInTransactionCheck -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/checks/connections_idle.go internal/checks/connections_test.go
git commit -m "feat(checks): connections.idle_in_transaction check (ly-u4t.22)"
```

---

## Task 9: Check — blocking (A→B) + full-suite gates + scheduler integration

**Files:**
- Create: `internal/checks/connections_blocking.go`
- Modify: `internal/checks/connections_test.go`

- [ ] **Step 1: Add the failing unit test**

Append to `internal/checks/connections_test.go`:

```go
func TestBlockingCheck_warnAndCritical(t *testing.T) {
	in := &Input{Blocking: []BlockEdge{
		{BlockedPID: 11, BlockerPID: 10, BlockedWaitSeconds: 5},  // warning
		{BlockedPID: 21, BlockerPID: 20, BlockedWaitSeconds: 90}, // critical (>= 60s)
	}}
	got := BlockingCheck{}.Eval(in)
	if len(got) != 2 {
		t.Fatalf("want 2 results, got %d: %+v", len(got), got)
	}
	bySev := map[Severity]Result{}
	for _, r := range got {
		bySev[r.Severity] = r
		if r.CheckID != "connections.blocking" || r.Category != "connections" || r.Status != StatusFiring {
			t.Fatalf("bad result shape: %+v", r)
		}
	}
	if bySev[SeverityCritical].Object != "pid:21" {
		t.Fatalf("critical Object = %q, want pid:21", bySev[SeverityCritical].Object)
	}
	if _, ok := bySev[SeverityWarning]; !ok {
		t.Fatalf("missing warning result: %+v", got)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/checks/ -run TestBlockingCheck -v`
Expected: FAIL — `undefined: BlockingCheck`.

- [ ] **Step 3: Implement the check**

Create `internal/checks/connections_blocking.go`:

```go
package checks

import "fmt"

func init() { Register(BlockingCheck{}) }

// BlockingCheck flags A→B lock-wait relationships (B waits on a lock held by A).
// Any active blocking edge is at least a warning; a long wait escalates to
// critical. Object is the blocked pid (the victim). pids only — T1.
type BlockingCheck struct{}

const blockingCriticalWaitSeconds = 60

func (BlockingCheck) ID() string       { return "connections.blocking" }
func (BlockingCheck) Category() string { return "connections" }

func (BlockingCheck) Eval(in *Input) []Result {
	out := make([]Result, 0, len(in.Blocking))
	for _, e := range in.Blocking {
		sev := SeverityWarning
		if e.BlockedWaitSeconds >= blockingCriticalWaitSeconds {
			sev = SeverityCritical
		}
		out = append(out, Result{
			CheckID:  "connections.blocking",
			Category: "connections",
			Severity: sev,
			Status:   StatusFiring,
			Object:   fmt.Sprintf("pid:%d", e.BlockedPID),
			Detail:   fmt.Sprintf("pid %d blocked by pid %d for %ds", e.BlockedPID, e.BlockerPID, e.BlockedWaitSeconds),
		})
	}
	return out
}
```

- [ ] **Step 4: Run the full checks package + verify registration**

Run: `go test ./internal/checks/ -v`
Expected: PASS. Confirm the three new checks are registered by `DefaultChecks()` (they self-register via `init()`). Optionally add a guard:

```go
func TestConnectionsChecksRegistered(t *testing.T) {
	want := map[string]bool{
		"connections.long_running_active": false,
		"connections.idle_in_transaction": false,
		"connections.blocking":            false,
	}
	for _, c := range DefaultChecks() {
		if _, ok := want[c.ID()]; ok {
			want[c.ID()] = true
		}
	}
	for id, found := range want {
		if !found {
			t.Fatalf("check %q not registered", id)
		}
	}
}
```

- [ ] **Step 5: Full race suite + lint**

Run: `go test ./... -race -p 1`
Expected: all packages PASS (integration tests use `testpg.ReadyWait()`).

Run: `golangci-lint run`
Expected: clean exit (0 findings). Fix any gocyclo on `cmd/collector/main.go` by keeping the existing `//nolint:gocyclo` waiver; if the connections block pushes complexity, extract the ship logic into a `shipConnections(ctx)` helper.

- [ ] **Step 6: Commit**

```bash
git add internal/checks/connections_blocking.go internal/checks/connections_test.go
git commit -m "feat(checks): connections.blocking check + registration guard (ly-u4t.22)"
```

---

## Self-Review Notes (spec coverage)

- **(1) long-running active** → Task 7 (`ActiveLongRunningCheck`), fed by `ConnectionSample.active_seconds` (Tasks 1-6).
- **(2) idle-in-transaction beyond threshold** → Task 8 (`IdleInTransactionCheck`), fed by `state_seconds` for `idle in transaction[(aborted)]`.
- **(3) blocking (A→B)** → Task 9 (`BlockingCheck`), fed by `pg_blocking_pids()` edges.
- **Privacy/T1** → enforced by contract tests (Task 1); reader selects no literal columns (Task 3); store/proto carry pids + durations + fixed labels only.
- **Read-only / outbound-only** → reader issues only `SELECT`; collector stays a websocket client.
- **testpg.ReadyWait()** → used in all new integration tests (Tasks 2, 3).

## Post-implementation (lifecycle)

After all tasks green: `bd label remove ly-u4t.22 ready-impl && bd label add ly-u4t.22 ready-test`, push the branch, open a PR off `origin/main`, watch required checks to green, then move to `ready-test`/close with a `bd note` linking the PR.
