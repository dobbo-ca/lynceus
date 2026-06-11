# M3 Checks Bundle — Schema (Invalid Indexes, Unused Indexes) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add two scheduler-side checks — `schema.invalid_index` and `schema.unused_index` — backed by a new per-index T1 collector reader, wire message, and store table (bead ly-u4t.23).

**Architecture:** Mirror the Connections bundle (PR #26) and the FreezeAge bundle exactly. A new `IndexStat` T1 wire message carries per-index identifiers + counters + structural booleans (NEVER an index expression or predicate — those can embed literals and are T2). A new weekly-partitioned `index_stats` store table persists it; a new `IndexStatsReader` reads `pg_index ⋈ pg_class ⋈ pg_stat_user_indexes` on the slow (~10m) full cadence; the checks scheduler reads the latest rows into `Input.Indexes` and two pure checks evaluate them. Evaluation stays scheduler-side, results carry counts/enums/identifiers only.

**Tech Stack:** Go, protobuf (proto3, `make proto`), pgx/v5 COPY, native range partitioning (vanilla Postgres, RDS-safe), testcontainers + `internal/testpg.ReadyWait()`.

---

## The key design question (resolved)

**Does existing stored data suffice? No.**

- **Unused indexes** need a *per-index* scan count. `Input.TableStats[].IdxScan` (from `pg_stat_user_tables`) is the *per-table aggregate* across all of a table's indexes — it cannot tell you *which* index is unused. The per-index counter lives in `pg_stat_user_indexes.idx_scan`, which no reader ships today.
- **Invalid indexes** need `pg_index.indisvalid` (a failed `CREATE INDEX CONCURRENTLY` leaves an invalid index that the planner ignores but writes still maintain). No reader ships any `pg_index` column today; the inventory's `OBJECT_KIND_INDEX` rows carry only kind/schema/name/fqn/size.

**Decision: a new dedicated per-index T1 path**, identical in shape to TableStat / FreezeAge / ConnectionSample. This keeps churny per-index counters out of the structural `schema_objects` upsert table and keeps every check uniform (muting, results table, severity, notify). It does *not* depend on the still-open ly-xqf.7 ("M2: Per-table index list"); this bundle ships the minimal per-index fields its two checks need, and ly-xqf.7 can layer richer per-index UI on the same table later.

**T1 field set (each field justified, no speculation):**

| field | source | used by |
|---|---|---|
| `schema`, `name`, `fqn` | index namespace + relname | Object identifier for both checks |
| `table_fqn` | `pg_index.indrelid` → `schema.tablename` | check Detail (which table) |
| `idx_scan` | `pg_stat_user_indexes.idx_scan` | unused check threshold |
| `size_bytes` | `pg_relation_size(indexrelid)` | unused severity ladder + prioritization |
| `is_valid` | `pg_index.indisvalid` | invalid check predicate |
| `is_ready` | `pg_index.indisready` | invalid check Detail (in-progress vs failed) |
| `is_unique` | `pg_index.indisunique` | unused check suppression (constraint-backing) |
| `is_primary` | `pg_index.indisprimary` | unused check suppression (constraint-backing) |

Deliberately **excluded** (would be literal-bearing → T2, or unused → YAGNI): `pg_get_indexdef` / index expression, `pg_index.indpred` partial-index predicate, `idx_tup_read`, `idx_tup_fetch`.

In Postgres an index always lives in the same namespace as its table, so the SchemaFilter is applied once on the index schema; `table_fqn`'s schema equals it.

---

## File structure

- `proto/lynceus/v1/snapshot.proto` — **modify**: add `IndexStat` message + `Snapshot.index_stats = 12`.
- `internal/proto/lynceus/v1/snapshot.pb.go` — **regenerate** via `make proto`.
- `internal/proto/lynceus/v1/contract_test.go` — **modify**: T1 allowlist test for `IndexStat`, scalar-shape test, `Snapshot` allowlist + `index_stats` element-type test.
- `internal/caps/caps.go` — **modify**: add `IndexStats` capability constant + `Declared()` entry.
- `internal/store/migrations/stats/0012_index_stats.sql` — **create**: weekly-partitioned table.
- `internal/store/index_stats.go` — **create**: `IndexStatRow`, `WriteIndexStats` (COPY), partition ensure, `LatestIndexStats`.
- `internal/store/index_stats_test.go` — **create**: roundtrip integration test.
- `internal/collector/index_stats_reader.go` — **create**: `IndexStatsReader` + SQL.
- `internal/collector/index_stats_reader_test.go` — **create**: integration test (valid + invalid index) + gated-off test.
- `internal/ingest/server.go` — **modify**: `snapshotToIndexStats` mapper + persist call in `persistSnapshot`.
- `internal/ingest/server_test.go` — **modify**: assert index_stats persist in the snapshot round-trip test.
- `internal/checks/checks.go` — **modify**: `IndexInfo` struct + `Input.Indexes` field.
- `internal/checks/schema_indexes.go` — **create**: `InvalidIndexCheck`, `UnusedIndexCheck`, `init()`+`Register()`.
- `internal/checks/schema_indexes_test.go` — **create**: severity-ladder + registration unit tests.
- `internal/checks/scheduler.go` — **modify**: assemble `Input.Indexes` from `LatestIndexStats`.
- `cmd/collector/main.go` — **modify**: construct `IndexStatsReader`, read in `runFull()`, attach to snapshot, update log line.

---

## Task 1: Proto — `IndexStat` message + `Snapshot.index_stats`

**Files:**
- Modify: `proto/lynceus/v1/snapshot.proto`
- Regenerate: `internal/proto/lynceus/v1/snapshot.pb.go`
- Test: `internal/proto/lynceus/v1/contract_test.go`

- [ ] **Step 1: Write the failing contract tests**

Add to `internal/proto/lynceus/v1/contract_test.go`:

```go
// TestIndexStatHasOnlyAggregateFields enforces the T1 privacy guarantee for
// the per-index message (ly-u4t.23). IndexStat must carry only catalog
// identifiers (schema/name/fqn/table_fqn), a scan COUNTER, a size byte-count,
// and structural catalog booleans — never the index expression
// (pg_get_indexdef) or a partial-index predicate (pg_index.indpred), both of
// which can embed literal values from the monitored database. Those belong in
// a separate T2 message gated behind RBAC + audit.
func TestIndexStatHasOnlyAggregateFields(t *testing.T) {
	allowed := map[string]struct{}{
		"schema": {}, "name": {}, "fqn": {}, "table_fqn": {},
		"idx_scan": {}, "size_bytes": {},
		"is_valid": {}, "is_ready": {}, "is_unique": {}, "is_primary": {},
	}
	assertOnlyAllowed(t, (&lynceusv1.IndexStat{}).ProtoReflect().Descriptor().Fields(), allowed, "IndexStat")
}

// TestIndexStatScalarFieldShapes guards against a refactor that swaps an
// identifier string for bytes or a nested message able to embed unstructured
// content. Only the four identifiers are strings.
func TestIndexStatScalarFieldShapes(t *testing.T) {
	fields := (&lynceusv1.IndexStat{}).ProtoReflect().Descriptor().Fields()
	for _, fn := range []string{"schema", "name", "fqn", "table_fqn"} {
		f := fields.ByName(protoreflect.Name(fn))
		if f == nil {
			t.Fatalf("field %q missing from IndexStat", fn)
		}
		if got := f.Kind().String(); got != "string" {
			t.Fatalf("IndexStat.%s must be string kind, got %s", fn, got)
		}
	}
}

// TestSnapshotCarriesIndexStats verifies the index_stats field exists on the
// Snapshot wrapper as a repeated IndexStat at field number 12.
func TestSnapshotCarriesIndexStats(t *testing.T) {
	fields := (&lynceusv1.Snapshot{}).ProtoReflect().Descriptor().Fields()
	f := fields.ByName("index_stats")
	if f == nil {
		t.Fatal("index_stats field missing from Snapshot")
	}
	if f.Number() != 12 {
		t.Fatalf("index_stats field number = %d, want 12", f.Number())
	}
	if got := f.Message(); got == nil || got.Name() != "IndexStat" {
		t.Fatalf("index_stats must be repeated IndexStat, got %v", got)
	}
}
```

Also add `"index_stats": {},` to the `allowed` map inside the existing `TestSnapshotCarriesLogEvents` (the Snapshot envelope allowlist), so it does not fail on the new field.

- [ ] **Step 2: Run tests to verify they fail to compile**

Run: `go test ./internal/proto/lynceus/v1/ -run TestIndexStat`
Expected: build failure — `undefined: lynceusv1.IndexStat`.

- [ ] **Step 3: Add the proto message + Snapshot field**

In `proto/lynceus/v1/snapshot.proto`, add field 12 to `Snapshot` (immediately after `blocking_edges = 11;`):

```proto
  // Per-index scan counter + structural validity/uniqueness flags from
  // pg_index + pg_stat_user_indexes, on the slow (~10m) full cadence. Feeds
  // the Schema checks (ly-u4t.23): invalid indexes + unused indexes. See
  // IndexStat — T1, identifiers/counts/booleans only, NEVER an index
  // expression or predicate.
  repeated IndexStat index_stats = 12;
```

Then add the message (place it after `TableStat`, before `FreezeAge`):

```proto
// IndexStat is one per-index scan counter + size + structural validity/
// uniqueness flags, sampled from pg_index + pg_class + pg_stat_user_indexes
// on the slow (~10m) full cadence. Feeds the Schema checks (ly-u4t.23):
// invalid indexes (indisvalid=false) and unused indexes (low idx_scan).
//
// INVARIANT: every field is a catalog IDENTIFIER (schema/name/fqn/table_fqn),
// a scan COUNTER, a byte SIZE, or a structural catalog BOOLEAN. It carries NO
// index expression (pg_get_indexdef), partial-index predicate
// (pg_index.indpred), column value, or any per-execution literal — those can
// embed customer data and require a separate T2 message gated behind RBAC +
// audit. Same privacy class as TableStat (counts + identifiers only).
message IndexStat {
  string schema    = 1;  // index/table namespace IDENTIFIER (filtered at the collector boundary)
  string name      = 2;  // index IDENTIFIER
  string fqn       = 3;  // "schema.index" IDENTIFIER — join key
  string table_fqn = 4;  // "schema.table" the index belongs to IDENTIFIER

  int64 idx_scan   = 5;  // pg_stat_user_indexes.idx_scan          [COUNTER]
  int64 size_bytes = 6;  // pg_relation_size(indexrelid)           [SIZE]

  bool is_valid   = 7;   // pg_index.indisvalid  — false = failed CREATE INDEX CONCURRENTLY
  bool is_ready   = 8;   // pg_index.indisready  — false = still building
  bool is_unique  = 9;   // pg_index.indisunique — backs a UNIQUE constraint
  bool is_primary = 10;  // pg_index.indisprimary — backs a PRIMARY KEY
}
```

- [ ] **Step 4: Regenerate Go from proto**

Run: `make proto`
Expected: `internal/proto/lynceus/v1/snapshot.pb.go` regenerated with `IndexStat` + `Snapshot.IndexStats`. No manual edits to the `.pb.go`.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/proto/lynceus/v1/`
Expected: PASS (all contract tests, including the three new ones and the updated Snapshot allowlist).

- [ ] **Step 6: Commit**

```bash
git add proto/lynceus/v1/snapshot.proto internal/proto/lynceus/v1/snapshot.pb.go internal/proto/lynceus/v1/contract_test.go
git commit -m "proto(checks): IndexStat T1 message + Snapshot.index_stats (ly-u4t.23)"
```

---

## Task 2: Capability — `IndexStats`

**Files:**
- Modify: `internal/caps/caps.go`
- Test: `internal/caps/probes_test.go` (no change needed; `Declared()` is asserted complete elsewhere)

- [ ] **Step 1: Add the capability constant**

In `internal/caps/caps.go`, add after the `FreezeAge` constant in the `const (...)` block:

```go
	// IndexStats gates the per-index scan/validity reader feeding the Schema
	// checks (ly-u4t.23): invalid indexes + unused indexes.
	IndexStats Capability = "index_stats"
```

- [ ] **Step 2: Add it to `Declared()`**

In the `Declared()` slice, add `IndexStats,` after `FreezeAge,`:

```go
		SchemaInventory,
		TableSize,
		FreezeAge,
		IndexStats,
```

- [ ] **Step 3: Verify it builds**

Run: `go build ./internal/caps/`
Expected: builds clean.

- [ ] **Step 4: Commit**

```bash
git add internal/caps/caps.go
git commit -m "caps: declare index_stats capability (ly-u4t.23)"
```

---

## Task 3: Store — `index_stats` table, writer, reader

**Files:**
- Create: `internal/store/migrations/stats/0012_index_stats.sql`
- Create: `internal/store/index_stats.go`
- Test: `internal/store/index_stats_test.go`

- [ ] **Step 1: Write the failing roundtrip test**

Create `internal/store/index_stats_test.go`:

```go
package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/dobbo-ca/lynceus/internal/store"
)

func TestWriteIndexStats_createsPartitionAndRoundtrips(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyStatsMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := store.NewStats(pool)

	now := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC) // a Thursday
	rows := []store.IndexStatRow{
		{
			ServerID: "srv-a", CollectedAt: now,
			SchemaName: "public", ObjectName: "orders_pkey", FQN: "public.orders_pkey",
			TableFQN: "public.orders", IdxScan: 9000, SizeBytes: 8192,
			IsValid: true, IsReady: true, IsUnique: true, IsPrimary: true,
		},
		{
			ServerID: "srv-a", CollectedAt: now,
			SchemaName: "public", ObjectName: "orders_status_idx", FQN: "public.orders_status_idx",
			TableFQN: "public.orders", IdxScan: 0, SizeBytes: 524_288_000,
			IsValid: false, IsReady: true, IsUnique: false, IsPrimary: false,
		},
	}
	if err := s.WriteIndexStats(ctx, rows); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := s.LatestIndexStats(ctx, "srv-a", now)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 rows, got %d: %+v", len(got), got)
	}
	byFQN := map[string]store.IndexStatRow{}
	for _, r := range got {
		byFQN[r.FQN] = r
	}
	pk := byFQN["public.orders_pkey"]
	if pk.TableFQN != "public.orders" || !pk.IsPrimary || !pk.IsUnique || pk.IdxScan != 9000 {
		t.Fatalf("pk row not preserved: %+v", pk)
	}
	bad := byFQN["public.orders_status_idx"]
	if bad.IsValid || bad.IdxScan != 0 || bad.SizeBytes != 524_288_000 {
		t.Fatalf("invalid/unused row not preserved: %+v", bad)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestWriteIndexStats`
Expected: build failure — `undefined: store.IndexStatRow`.

- [ ] **Step 3: Write the migration**

Create `internal/store/migrations/stats/0012_index_stats.sql`:

```sql
-- Per-index scan counter + size + structural validity/uniqueness flags,
-- sampled from pg_index + pg_class + pg_stat_user_indexes on the slow (~10m)
-- full cadence. Feeds the Schema checks (ly-u4t.23): invalid indexes
-- (is_valid=false) and unused indexes (low idx_scan). One row per
-- (server_id, fqn, collected_at) — identifiers, counts, sizes, and catalog
-- booleans ONLY, NEVER an index expression or predicate. See the IndexStat
-- privacy contract test.
--
-- Range-partitioned by week on collected_at (vanilla Postgres,
-- RDS / Aurora / Cloud SQL safe — no extensions). Append-only series:
-- mirrors table_stats (0006) and freeze_ages (0010).

CREATE TABLE index_stats (
    server_id    TEXT NOT NULL,
    collected_at TIMESTAMPTZ NOT NULL,
    schema_name  TEXT NOT NULL,
    object_name  TEXT NOT NULL,
    fqn          TEXT NOT NULL,
    table_fqn    TEXT NOT NULL,
    idx_scan     BIGINT NOT NULL,
    size_bytes   BIGINT NOT NULL,
    is_valid     BOOLEAN NOT NULL,
    is_ready     BOOLEAN NOT NULL,
    is_unique    BOOLEAN NOT NULL,
    is_primary   BOOLEAN NOT NULL,
    data_tier    SMALLINT NOT NULL DEFAULT 1
) PARTITION BY RANGE (collected_at);

CREATE INDEX index_stats_brin_time ON index_stats USING brin (collected_at);
CREATE INDEX index_stats_srv_fqn   ON index_stats (server_id, fqn, collected_at);
```

- [ ] **Step 4: Write the store code**

Create `internal/store/index_stats.go`:

```go
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// IndexStatRow is one T1 row of per-index scan counter + size + structural
// validity/uniqueness flags. DataTier zero is coerced to 1 (T1) on insert.
// It carries NO index expression or predicate — those are literal-bearing
// and belong to T2.
type IndexStatRow struct {
	ServerID    string
	CollectedAt time.Time
	SchemaName  string
	ObjectName  string
	FQN         string
	TableFQN    string

	IdxScan   int64
	SizeBytes int64
	IsValid   bool
	IsReady   bool
	IsUnique  bool
	IsPrimary bool

	DataTier int16 // 0 -> coerced to 1
}

// indexStatsColumns is the COPY column order for WriteIndexStats; it matches
// the 0012_index_stats.sql column order.
var indexStatsColumns = []string{
	"server_id", "collected_at", "schema_name", "object_name", "fqn", "table_fqn",
	"idx_scan", "size_bytes", "is_valid", "is_ready", "is_unique", "is_primary",
	"data_tier",
}

// WriteIndexStats appends a batch of per-index stat rows via the COPY protocol,
// creating any missing weekly partitions first. Empty input is a no-op.
// Mirrors WriteTableStats / WriteFreezeAges.
func (s *Stats) WriteIndexStats(ctx context.Context, rows []IndexStatRow) error {
	if len(rows) == 0 {
		return nil
	}

	weeks := map[string]time.Time{}
	for i := range rows {
		r := &rows[i]
		weeks[indexStatsPartitionName(r.CollectedAt)] = r.CollectedAt
	}
	for _, ts := range weeks {
		if err := s.EnsureIndexStatsWeeklyPartition(ctx, ts); err != nil {
			return err
		}
	}

	src := pgx.CopyFromSlice(len(rows), func(i int) ([]any, error) {
		r := rows[i]
		if r.DataTier == 0 {
			r.DataTier = 1
		}
		return []any{
			r.ServerID, r.CollectedAt, r.SchemaName, r.ObjectName, r.FQN, r.TableFQN,
			r.IdxScan, r.SizeBytes, r.IsValid, r.IsReady, r.IsUnique, r.IsPrimary,
			r.DataTier,
		}, nil
	})
	_, err := s.pool.CopyFrom(ctx, pgx.Identifier{"index_stats"}, indexStatsColumns, src)
	return err
}

// EnsureIndexStatsWeeklyPartition creates the weekly partition for ts on
// index_stats if it does not already exist. Idempotent.
func (s *Stats) EnsureIndexStatsWeeklyPartition(ctx context.Context, ts time.Time) error {
	name := indexStatsPartitionName(ts)
	from, to := isoWeekBounds(ts)
	_, err := s.pool.Exec(ctx, fmt.Sprintf(
		`CREATE TABLE IF NOT EXISTS %s PARTITION OF index_stats
		 FOR VALUES FROM ('%s') TO ('%s')`,
		name,
		from.Format("2006-01-02"),
		to.Format("2006-01-02"),
	))
	return err
}

func indexStatsPartitionName(ts time.Time) string {
	y, w := ts.UTC().ISOWeek()
	return fmt.Sprintf("index_stats_%04d_%02d", y, w)
}

const indexStatsSelect = `SELECT server_id, collected_at, schema_name, object_name, fqn, table_fqn,
        idx_scan, size_bytes, is_valid, is_ready, is_unique, is_primary, data_tier
   FROM index_stats`

func scanIndexStatRows(rows pgx.Rows) ([]IndexStatRow, error) {
	defer rows.Close()
	var out []IndexStatRow
	for rows.Next() {
		var r IndexStatRow
		if err := rows.Scan(
			&r.ServerID, &r.CollectedAt, &r.SchemaName, &r.ObjectName, &r.FQN, &r.TableFQN,
			&r.IdxScan, &r.SizeBytes, &r.IsValid, &r.IsReady, &r.IsUnique, &r.IsPrimary, &r.DataTier,
		); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// LatestIndexStats returns the most recent index_stats row per fqn for
// serverID at or before asOf. data_tier = 1 only (T1). Served from the read
// replica. Mirrors LatestTableStats.
func (s *Stats) LatestIndexStats(ctx context.Context, serverID string, asOf time.Time) ([]IndexStatRow, error) {
	rows, err := s.ro.Query(ctx,
		indexStatsSelect+`
		  WHERE server_id = $1 AND collected_at <= $2 AND data_tier = 1
		    AND (fqn, collected_at) IN (
		        SELECT fqn, max(collected_at) FROM index_stats
		         WHERE server_id = $1 AND collected_at <= $2 AND data_tier = 1
		         GROUP BY fqn)
		  ORDER BY fqn`,
		serverID, asOf,
	)
	if err != nil {
		return nil, err
	}
	return scanIndexStatRows(rows)
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestWriteIndexStats`
Expected: PASS (testcontainer spins up via the package's `newPool` helper).

- [ ] **Step 6: Commit**

```bash
git add internal/store/migrations/stats/0012_index_stats.sql internal/store/index_stats.go internal/store/index_stats_test.go
git commit -m "store: index_stats partitioned table + COPY writer + LatestIndexStats (ly-u4t.23)"
```

---

## Task 4: Collector — `IndexStatsReader`

**Files:**
- Create: `internal/collector/index_stats_reader.go`
- Test: `internal/collector/index_stats_reader_test.go`

- [ ] **Step 1: Write the failing integration + gated-off tests**

Create `internal/collector/index_stats_reader_test.go`:

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
	"github.com/dobbo-ca/lynceus/internal/testpg"
)

func TestIndexStatsReaderReadsValidAndInvalidIndexes(t *testing.T) {
	ctx := context.Background()

	c, err := tcpostgres.Run(ctx,
		"postgres:16",
		tcpostgres.WithDatabase("lynceus_target"),
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
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	// A table with a PK (unique+primary), a secondary index, and a
	// deliberately-invalidated index (set indisvalid=false directly in the
	// catalog — the supported way to simulate a failed CREATE INDEX
	// CONCURRENTLY in a test).
	if _, err := pool.Exec(ctx, `CREATE TABLE idx_demo(id int PRIMARY KEY, status text)`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `CREATE INDEX idx_demo_status ON idx_demo(status)`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE pg_index SET indisvalid = false
		   WHERE indexrelid = 'idx_demo_status'::regclass`); err != nil {
		t.Fatal(err)
	}

	r := collector.NewIndexStatsReader(pool, mustFilter(t), caps.NewGate(), "lynceus_target")
	rows, err := r.Read(ctx, "srv-a")
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	byName := map[string]bool{} // name -> is_valid
	var sawPK, sawInvalid bool
	for _, is := range rows {
		byName[is.GetName()] = is.GetIsValid()
		if is.GetName() == "idx_demo_pkey" {
			sawPK = true
			if !is.GetIsPrimary() || !is.GetIsUnique() || is.GetTableFqn() != "public.idx_demo" {
				t.Errorf("pkey flags wrong: %+v", is)
			}
		}
		if is.GetName() == "idx_demo_status" {
			sawInvalid = true
			if is.GetIsValid() {
				t.Errorf("idx_demo_status should be invalid: %+v", is)
			}
		}
		if is.GetSizeBytes() < 0 {
			t.Fatalf("negative size: %+v", is)
		}
	}
	if !sawPK || !sawInvalid {
		t.Fatalf("want pkey + invalid index; pk=%v invalid=%v rows=%d", sawPK, sawInvalid, len(rows))
	}
}

// TestIndexStatsReader_gatedOffReturnsNoRows proves the IndexStats capability
// gate short-circuits Read before any query: a nil pool would panic if the
// reader touched the DB, so a clean nil result means the gate suppressed it.
func TestIndexStatsReader_gatedOffReturnsNoRows(t *testing.T) {
	g := caps.NewGate()
	g.Replace(map[caps.GateKey]bool{{Db: "lynceus_target", Cap: caps.IndexStats}: false})
	r := collector.NewIndexStatsReader(nil, mustFilter(t), g, "lynceus_target")

	rows, err := r.Read(context.Background(), "srv-a")
	if err != nil {
		t.Fatalf("gated-off Read returned error: %v", err)
	}
	if rows != nil {
		t.Errorf("gated-off Read returned %d rows, want nil (no query)", len(rows))
	}
}
```

Add the small shared helper at the bottom of this new file (the other reader tests construct the filter inline; this keeps the two tests DRY):

```go
func mustFilter(t *testing.T) *collector.SchemaFilter {
	t.Helper()
	f, err := collector.NewSchemaFilter("", "")
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	return f
}
```

> Note: if `mustFilter` collides with an existing helper in `package collector_test`, drop this definition and reuse the existing one. Verify with `grep -rn "func mustFilter" internal/collector/`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/collector/ -run TestIndexStatsReader`
Expected: build failure — `undefined: collector.NewIndexStatsReader`.

- [ ] **Step 3: Write the reader**

Create `internal/collector/index_stats_reader.go`:

```go
// internal/collector/index_stats_reader.go
package collector

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dobbo-ca/lynceus/internal/caps"
	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
)

// IndexStatsReader reads per-index scan counters, sizes, and structural
// validity/uniqueness flags from pg_index + pg_class + pg_stat_user_indexes on
// a monitored Postgres. Every returned field is a catalog identifier, a scan
// COUNT, a byte SIZE, or a catalog BOOLEAN — never an index expression
// (pg_get_indexdef) or partial-index predicate (pg_index.indpred), preserving
// the T1 privacy contract. Feeds the Schema checks (ly-u4t.23).
//
// The query DELIBERATELY selects only identifiers, the idx_scan counter,
// pg_relation_size, and four pg_index booleans. It takes ACCESS-SHARE catalog
// locks only, so it belongs on the slow (~10m) full cadence beside the table
// and freeze-age readers, never the ~10s activity cadence. Read-only / RDS-safe.
//
// In Postgres an index always shares its table's namespace, so filter is
// applied once on the index schema; table_fqn's schema is identical.
//
// filter is the SAME SchemaFilter instance shared with the other catalog
// readers: a schema excluded by ignore_schema_regexp produces zero index rows,
// keeping the redaction boundary identical across readers.
type IndexStatsReader struct {
	pool   *pgxpool.Pool
	filter *SchemaFilter
	gate   *caps.Gate
	db     string // current_database() of pool, the gate key
}

// NewIndexStatsReader returns a reader bound to pool, gated by filter and the
// capability gate. db is the connection's current_database().
func NewIndexStatsReader(pool *pgxpool.Pool, filter *SchemaFilter, gate *caps.Gate, db string) *IndexStatsReader {
	return &IndexStatsReader{pool: pool, filter: filter, gate: gate, db: db}
}

// indexStatsSQL joins the index relation (ic) to its table (tc) via pg_index,
// left-joining pg_stat_user_indexes for the scan counter (an index may have no
// stats row yet → COALESCE to 0).
const indexStatsSQL = `
SELECT n.nspname                                  AS schema,
       ic.relname                                 AS index_name,
       tn.nspname || '.' || tc.relname            AS table_fqn,
       COALESCE(psui.idx_scan, 0)::bigint         AS idx_scan,
       pg_relation_size(ic.oid)::bigint           AS size_bytes,
       i.indisvalid, i.indisready, i.indisunique, i.indisprimary
  FROM pg_index i
  JOIN pg_class      ic ON ic.oid = i.indexrelid
  JOIN pg_namespace  n  ON n.oid  = ic.relnamespace
  JOIN pg_class      tc ON tc.oid = i.indrelid
  JOIN pg_namespace  tn ON tn.oid = tc.relnamespace
  LEFT JOIN pg_stat_user_indexes psui ON psui.indexrelid = i.indexrelid
 WHERE ic.relkind = 'i'
   AND n.nspname NOT IN ('pg_catalog','information_schema','pg_toast')
 ORDER BY n.nspname, ic.relname`

// Read returns one IndexStat per allowed index. Rows whose schema is excluded
// by the SchemaFilter are skipped entirely.
func (r *IndexStatsReader) Read(ctx context.Context, serverID string) ([]*lynceusv1.IndexStat, error) {
	_ = serverID // reserved for future per-server scoping; identifiers are server-agnostic here
	if !r.gate.Allowed(r.db, caps.IndexStats) {
		return nil, nil // capability disabled: build & ship nothing
	}

	rows, err := r.pool.Query(ctx, indexStatsSQL)
	if err != nil {
		return nil, fmt.Errorf("query index stats: %w", err)
	}
	defer rows.Close()

	var out []*lynceusv1.IndexStat
	for rows.Next() {
		var (
			schema, name, tableFQN              string
			idxScan, sizeBytes                  int64
			isValid, isReady, isUniq, isPrimary bool
		)
		if err := rows.Scan(
			&schema, &name, &tableFQN,
			&idxScan, &sizeBytes,
			&isValid, &isReady, &isUniq, &isPrimary,
		); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		if !r.filter.IsAllowed(schema) {
			continue
		}
		out = append(out, &lynceusv1.IndexStat{
			Schema:    schema,
			Name:      name,
			Fqn:       schema + "." + name,
			TableFqn:  tableFQN,
			IdxScan:   idxScan,
			SizeBytes: sizeBytes,
			IsValid:   isValid,
			IsReady:   isReady,
			IsUnique:  isUniq,
			IsPrimary: isPrimary,
		})
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/collector/ -run TestIndexStatsReader`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/collector/index_stats_reader.go internal/collector/index_stats_reader_test.go
git commit -m "collector: IndexStatsReader — per-index scan/size/validity (ly-u4t.23)"
```

---

## Task 5: Ingest — persist `index_stats`

**Files:**
- Modify: `internal/ingest/server.go`
- Test: `internal/ingest/server_test.go`

- [ ] **Step 1: Add the failing assertion to the snapshot round-trip test**

First inspect the existing persistence test to find the pattern:

Run: `grep -n "table_stats\|TableStats\|persistSnapshot\|WriteTableStats\|LatestTableStats\|func Test" internal/ingest/server_test.go`

Find the test that ships a full snapshot and asserts persisted rows (the one exercising `TableStats`/`FreezeAges`). In that test, add an `IndexStats` slice to the constructed `*lynceusv1.Snapshot`:

```go
		IndexStats: []*lynceusv1.IndexStat{{
			Schema: "public", Name: "t_pkey", Fqn: "public.t_pkey",
			TableFqn: "public.t", IdxScan: 5, SizeBytes: 8192,
			IsValid: true, IsReady: true, IsUnique: true, IsPrimary: true,
		}},
```

and after the existing post-conditions, assert it persisted:

```go
	idxRows, err := st.LatestIndexStats(ctx, serverID, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatalf("LatestIndexStats: %v", err)
	}
	if len(idxRows) != 1 || idxRows[0].FQN != "public.t_pkey" || !idxRows[0].IsPrimary {
		t.Fatalf("index_stats not persisted: %+v", idxRows)
	}
```

> Adapt variable names (`st`, `serverID`, `ctx`) to whatever the existing test uses. If no single full-snapshot persistence test exists, add a focused test mirroring the freeze-age persistence test in the same file.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ingest/ -run <TheTestName>`
Expected: FAIL — `LatestIndexStats` returns 0 rows (mapper + persist not yet wired), or build failure if `IndexStats` field referenced before regen (it exists from Task 1, so this should be a runtime assertion failure).

- [ ] **Step 3: Add the mapper**

In `internal/ingest/server.go`, add after `snapshotToFreezeAges`:

```go
func snapshotToIndexStats(snap *lynceusv1.Snapshot) []store.IndexStatRow {
	collectedAt := time.Unix(snap.CollectedAtUnix, 0).UTC()
	if collectedAt.IsZero() || snap.CollectedAtUnix == 0 {
		collectedAt = time.Now().UTC()
	}
	out := make([]store.IndexStatRow, 0, len(snap.IndexStats))
	for _, ix := range snap.IndexStats {
		out = append(out, store.IndexStatRow{
			ServerID:    snap.ServerId,
			CollectedAt: collectedAt,
			SchemaName:  ix.Schema,
			ObjectName:  ix.Name,
			FQN:         ix.Fqn,
			TableFQN:    ix.TableFqn,

			IdxScan:   ix.IdxScan,
			SizeBytes: ix.SizeBytes,
			IsValid:   ix.IsValid,
			IsReady:   ix.IsReady,
			IsUnique:  ix.IsUnique,
			IsPrimary: ix.IsPrimary,

			DataTier: 1,
		})
	}
	return out
}
```

- [ ] **Step 4: Wire it into `persistSnapshot`**

In `persistSnapshot`, add after the `snapshotToFreezeAges` block (and before/after the connections blocks — order is not significant):

```go
	if ix := snapshotToIndexStats(snap); len(ix) > 0 {
		if err := s.stats.WriteIndexStats(ctx, ix); err != nil {
			return "write index_stats", err
		}
	}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/ingest/ -run <TheTestName>`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/ingest/server.go internal/ingest/server_test.go
git commit -m "ingest: persist Snapshot.index_stats into index_stats (ly-u4t.23)"
```

---

## Task 6: Checks — `Input.Indexes` + two checks

**Files:**
- Modify: `internal/checks/checks.go`
- Create: `internal/checks/schema_indexes.go`
- Test: `internal/checks/schema_indexes_test.go`

- [ ] **Step 1: Write the failing unit tests**

Create `internal/checks/schema_indexes_test.go`:

```go
package checks

import "testing"

func TestInvalidIndexCheck_firesOnInvalidOnly(t *testing.T) {
	in := &Input{Indexes: []IndexInfo{
		{FQN: "public.good", TableFQN: "public.t", IsValid: true, IsReady: true},
		{FQN: "public.bad", TableFQN: "public.t", IsValid: false, IsReady: true},
	}}
	got := InvalidIndexCheck{}.Eval(in)
	if len(got) != 1 {
		t.Fatalf("want 1 firing result, got %d: %+v", len(got), got)
	}
	r := got[0]
	if r.CheckID != "schema.invalid_index" || r.Category != "schema" ||
		r.Severity != SeverityWarning || r.Status != StatusFiring || r.Object != "public.bad" {
		t.Fatalf("bad result shape: %+v", r)
	}
}

func TestUnusedIndexCheck_severityLadderAndSuppressions(t *testing.T) {
	in := &Input{Indexes: []IndexInfo{
		// primary key: suppressed even though unused.
		{FQN: "public.t_pkey", TableFQN: "public.t", IsValid: true, IsPrimary: true, IsUnique: true, IdxScan: 0, SizeBytes: 1 << 30},
		// unique (constraint-backing): suppressed.
		{FQN: "public.t_uq", TableFQN: "public.t", IsValid: true, IsUnique: true, IdxScan: 0, SizeBytes: 1 << 30},
		// invalid: owned by the invalid check, suppressed here.
		{FQN: "public.t_invalid", TableFQN: "public.t", IsValid: false, IdxScan: 0, SizeBytes: 1 << 30},
		// scanned above threshold: not unused.
		{FQN: "public.t_hot", TableFQN: "public.t", IsValid: true, IdxScan: 5000, SizeBytes: 1 << 30},
		// unused but trivially small: below the byte floor, suppressed.
		{FQN: "public.t_tiny", TableFQN: "public.t", IsValid: true, IdxScan: 0, SizeBytes: 4096},
		// unused, medium: INFO.
		{FQN: "public.t_med", TableFQN: "public.t", IsValid: true, IdxScan: 3, SizeBytes: 5 << 20},
		// unused, large: WARNING.
		{FQN: "public.t_big", TableFQN: "public.t", IsValid: true, IdxScan: 0, SizeBytes: 500 << 20},
	}}
	got := UnusedIndexCheck{}.Eval(in)
	bySev := map[Severity]Result{}
	for _, r := range got {
		if r.CheckID != "schema.unused_index" || r.Category != "schema" || r.Status != StatusFiring {
			t.Fatalf("bad result shape: %+v", r)
		}
		bySev[r.Severity] = r
	}
	if len(got) != 2 {
		t.Fatalf("want exactly 2 results (med=info, big=warning), got %d: %+v", len(got), got)
	}
	if bySev[SeverityInfo].Object != "public.t_med" {
		t.Fatalf("info Object = %q, want public.t_med", bySev[SeverityInfo].Object)
	}
	if bySev[SeverityWarning].Object != "public.t_big" {
		t.Fatalf("warning Object = %q, want public.t_big", bySev[SeverityWarning].Object)
	}
}

func TestSchemaChecksRegistered(t *testing.T) {
	want := map[string]bool{
		"schema.invalid_index": false,
		"schema.unused_index":  false,
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

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/checks/ -run "InvalidIndex|UnusedIndex|SchemaChecks"`
Expected: build failure — `undefined: IndexInfo`, `InvalidIndexCheck`, `UnusedIndexCheck`.

- [ ] **Step 3: Add `IndexInfo` + `Input.Indexes`**

In `internal/checks/checks.go`, add the field to `Input` (after the `Blocking` field):

```go
	Indexes []IndexInfo // populated by the scheduler (ly-u4t.23)
```

and add the projection struct near the other `*Info` structs (after `BlockEdge`):

```go
// IndexInfo is the check-local projection of store.IndexStatRow. Identifiers,
// a scan count, a size, and structural booleans only — T1.
type IndexInfo struct {
	Schema    string
	Name      string
	FQN       string
	TableFQN  string
	IdxScan   int64
	SizeBytes int64
	IsValid   bool
	IsReady   bool
	IsUnique  bool
	IsPrimary bool
}
```

- [ ] **Step 4: Write the checks**

Create `internal/checks/schema_indexes.go`:

```go
package checks

import "fmt"

func init() {
	Register(InvalidIndexCheck{})
	Register(UnusedIndexCheck{})
}

// InvalidIndexCheck flags indexes with pg_index.indisvalid = false. An invalid
// index is ignored by the planner yet still maintained on every write — the
// usual cause is a failed CREATE INDEX CONCURRENTLY. Always actionable
// (DROP + recreate), so warning severity. Identifiers only — T1.
type InvalidIndexCheck struct{}

func (InvalidIndexCheck) ID() string       { return "schema.invalid_index" }
func (InvalidIndexCheck) Category() string { return "schema" }

func (InvalidIndexCheck) Eval(in *Input) []Result {
	var out []Result
	for _, ix := range in.Indexes {
		if ix.IsValid {
			continue
		}
		out = append(out, Result{
			CheckID:  "schema.invalid_index",
			Category: "schema",
			Severity: SeverityWarning,
			Status:   StatusFiring,
			Object:   ix.FQN,
			Detail: fmt.Sprintf(
				"index on %s is INVALID (is_ready=%v) — the planner ignores it but writes still maintain it; usually a failed CREATE INDEX CONCURRENTLY. DROP and recreate.",
				ix.TableFQN, ix.IsReady),
		})
	}
	return out
}

// UnusedIndexCheck flags valid, non-constraint-backing indexes with a low
// cumulative scan count and a non-trivial size — dead weight that wastes
// storage and adds write amplification. Advisory (mirrors IndexAdvisorCheck):
// info by default, warning when the index is large.
//
// Suppressions avoid false positives:
//   - invalid indexes are owned by InvalidIndexCheck;
//   - PRIMARY KEY / UNIQUE indexes back constraints and cannot simply be
//     dropped, so they are never "unused" in the actionable sense;
//   - indexes below unusedMinBytes are too small to be worth flagging.
//
// LIMITATION: idx_scan is cumulative since the last stats reset
// (pg_stat_reset / pg_stat_user_indexes has no per-index reset timestamp).
// Treat a firing result as "investigate over a full workload cycle", not
// "drop immediately". Counts/identifiers only — T1.
type UnusedIndexCheck struct{}

const (
	unusedScanThreshold int64 = 50          // idx_scan at or below this is "effectively unused"
	unusedMinBytes      int64 = 1 << 20     // 1 MiB — ignore trivially small indexes
	unusedWarnBytes     int64 = 100_000_000 // ~100 MB — large unused index escalates to warning
)

func (UnusedIndexCheck) ID() string       { return "schema.unused_index" }
func (UnusedIndexCheck) Category() string { return "schema" }

func (UnusedIndexCheck) Eval(in *Input) []Result {
	var out []Result
	for _, ix := range in.Indexes {
		if !ix.IsValid || ix.IsPrimary || ix.IsUnique {
			continue
		}
		if ix.IdxScan > unusedScanThreshold || ix.SizeBytes < unusedMinBytes {
			continue
		}
		sev := SeverityInfo
		if ix.SizeBytes >= unusedWarnBytes {
			sev = SeverityWarning
		}
		out = append(out, Result{
			CheckID:  "schema.unused_index",
			Category: "schema",
			Severity: sev,
			Status:   StatusFiring,
			Object:   ix.FQN,
			Detail: fmt.Sprintf(
				"index on %s scanned %d times (<= %d) using %d bytes — likely unused; confirm over a full workload cycle, then consider DROP.",
				ix.TableFQN, ix.IdxScan, unusedScanThreshold, ix.SizeBytes),
		})
	}
	return out
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/checks/ -run "InvalidIndex|UnusedIndex|SchemaChecks"`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/checks/checks.go internal/checks/schema_indexes.go internal/checks/schema_indexes_test.go
git commit -m "checks: schema.invalid_index + schema.unused_index bundle (ly-u4t.23)"
```

---

## Task 7: Scheduler — assemble `Input.Indexes`

**Files:**
- Modify: `internal/checks/scheduler.go`

- [ ] **Step 1: Add the assembly block**

In `internal/checks/scheduler.go`, inside `assembleInput`, add after the `LatestBlockingEdges` block (and before the Index Advisor block):

```go
	idxStats, err := sc.stats.LatestIndexStats(ctx, serverID, now)
	if err != nil {
		return in, err
	}
	for i := range idxStats {
		ix := &idxStats[i]
		in.Indexes = append(in.Indexes, IndexInfo{
			Schema: ix.SchemaName, Name: ix.ObjectName, FQN: ix.FQN, TableFQN: ix.TableFQN,
			IdxScan: ix.IdxScan, SizeBytes: ix.SizeBytes,
			IsValid: ix.IsValid, IsReady: ix.IsReady, IsUnique: ix.IsUnique, IsPrimary: ix.IsPrimary,
		})
	}
```

- [ ] **Step 2: Verify the whole checks package builds + tests pass**

Run: `go test ./internal/checks/`
Expected: PASS (unit checks + the testcontainer scheduler tests — the scheduler integration test already seeds a table_stats row; `LatestIndexStats` on an empty table returns zero rows, which is fine).

- [ ] **Step 3: Commit**

```bash
git add internal/checks/scheduler.go
git commit -m "checks(scheduler): assemble Input.Indexes from LatestIndexStats (ly-u4t.23)"
```

---

## Task 8: Collector wiring — read index stats on the full cadence

**Files:**
- Modify: `cmd/collector/main.go`

- [ ] **Step 1: Construct the reader**

In `cmd/collector/main.go`, after `freezeReader := collector.NewFreezeAgeReader(...)`:

```go
	indexStatsReader := collector.NewIndexStatsReader(pool, filter, gate, db)
```

- [ ] **Step 2: Read it in `runFull` and attach to the snapshot**

Inside `runFull`, after the `freezeAges, err := freezeReader.Read(...)` block:

```go
		indexStats, err := indexStatsReader.Read(ctx, cfg.serverID)
		if err != nil {
			log.Printf("collector: index stats read: %v", err)
		}
```

Add the field to the `snap := &lynceusv1.Snapshot{...}` literal:

```go
			IndexStats:      indexStats,
```

Update the success log line to include the new count:

```go
		log.Printf("shipped %d query_stats, %d schema_objects, %d table_stats, %d freeze_ages, %d index_stats", len(stats), len(objs), len(tableStats), len(freezeAges), len(indexStats))
```

- [ ] **Step 3: Verify the collector builds**

Run: `go build ./cmd/collector/`
Expected: builds clean.

- [ ] **Step 4: Commit**

```bash
git add cmd/collector/main.go
git commit -m "collector(main): ship index_stats on the full cadence (ly-u4t.23)"
```

---

## Task 9: Full verification + PR

- [ ] **Step 1: Run the whole suite with the race detector**

Run: `go test ./... -race`
Expected: all packages PASS. Integration tests use testcontainers + `testpg.ReadyWait()`; if Docker is unavailable they `t.Skip`, not fail.

- [ ] **Step 2: Lint clean**

Run: `golangci-lint run` (v2.12.2)
Expected: no findings on the touched files. Watch specifically for: `gocyclo` on `assembleInput` / `persistSnapshot` (if either trips the threshold, extract a helper or add the same `//nolint:gocyclo // orchestration` waiver the existing code uses — match the established pattern, do not lower the bar), and unused-helper warnings on `mustFilter`.

- [ ] **Step 3: Confirm the privacy contract holds**

Run: `go test ./internal/proto/lynceus/v1/ -run "IndexStat|Snapshot"`
Expected: PASS — proves `IndexStat` carries only the allowlisted identifier/count/boolean fields and the Snapshot envelope grew by exactly one allowlisted repeated message.

- [ ] **Step 4: Push the branch and open the PR off origin/main**

```bash
git branch --show-current   # confirm it is THIS session's worktree branch
git push -u origin HEAD
gh pr create --base main --title "feat(checks): M3 Schema checks bundle — invalid + unused indexes (ly-u4t.23)" --body "<summary mirroring PR #26: design decision, layer-by-layer changes, invariants, testing>"
```

- [ ] **Step 5: Watch CI go green, then move the bead**

```bash
gh pr checks <n> --watch
bd label remove ly-u4t.23 ready-impl && bd label add ly-u4t.23 ready-test
bd note ly-u4t.23 "PR #<n>: IndexStat T1 reader/wire/store + schema.invalid_index & schema.unused_index checks. Awaiting review/merge."
```

After merge: `bd close ly-u4t.23`.

---

## Self-review

**Spec coverage:**
- Invalid index detection → Task 6 `InvalidIndexCheck` (driven by `IndexStat.is_valid` from Task 1/4). ✓
- Unused index detection (low scan counts) → Task 6 `UnusedIndexCheck` (driven by `IndexStat.idx_scan`). ✓
- "Local" / scheduler-side evaluation → checks are pure (`Eval`), scheduler does I/O (Task 7), mirroring every other bundle. ✓
- The key design question (existing data vs new reader) → resolved up top; new per-index T1 path because per-index `idx_scan` + `indisvalid` are not collected today. ✓
- T1 invariant → contract test (Task 1) allowlists only identifiers/counts/booleans; index expression + predicate explicitly excluded. ✓
- Read-only / outbound-only → reader issues `SELECT` only (Task 4); collector stays a websocket client (Task 8). ✓

**Type consistency:** `IndexStatRow` (store) ↔ `IndexStat` (proto, `TableFqn`/`IsValid` Go field casing) ↔ `IndexInfo` (checks) field names verified consistent across Tasks 1/3/5/6/7. COPY column list (Task 3) matches migration column order (Task 3) matches scan order. Check IDs `schema.invalid_index` / `schema.unused_index` and category `schema` identical across check code (Task 6) and tests.

**Placeholder scan:** none — every code step shows complete code; the only adaptation notes (ingest test variable names, `mustFilter` collision) are flagged with exact `grep` commands to resolve them.
