# ly-cwr.7 Ingestion on ClickHouse — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `cmd/ingestion` run on the ClickHouse backend by moving the last two PG-coupled writes (DLQ, schema_objects) onto the `store.Stats` seam and selecting the backend via `store.OpenStats`.

**Architecture:** Add `ParkDLQ` and `WriteSchemaObjects` to the `store.Stats` interface; implement both on `pgxStats` (existing PG tables) and `chStats` (two new ClickHouse migrations). `ingest.Server` drops its `*pgxpool.Pool` and calls the seam. `cmd/ingestion` obtains `Stats` from `store.OpenStats`, keeping a config pool only for the checks scheduler's advisory lock.

**Tech Stack:** Go, pgx v5 (Postgres), clickhouse-go v2 (ClickHouse), testcontainers (`internal/testpg`, `internal/testch`), protobuf wire contract.

**Spec:** `docs/superpowers/specs/2026-07-14-ly-cwr7-ingestion-on-clickhouse-design.md`

## Global Constraints

- Integration tests hit **real** engines via testcontainers — never mock the database. CH tests use `internal/testch`; PG tests use `internal/testpg`/`tcpostgres`.
- No new literal-bearing field on any T1 proto message (this change touches **no** proto).
- Authoritative `audit_log` stays vanilla Postgres (hash-chain) — untouched here.
- `data_tier` rides every telemetry row; `DataTier == 0` normalizes to `1` (T1) on write, matching existing writers.
- Keep both backends satisfying `store.Stats` (`var _ Stats = (*pgxStats)(nil)` and `var _ Stats = (*chStats)(nil)`) — the PG stats impl is retired later in ly-cwr.8, not here.
- ClickHouse has no `UPDATE`/`DELETE`: current-state tables use `ReplacingMergeTree`/`AggregatingMergeTree`; reads use `argMax`/`FINAL`.
- CH migrations are semicolon-split and re-run-safe (`CREATE TABLE IF NOT EXISTS`), applied by `store.ApplyClickHouseMigrations`.
- Commit after each task. Work stays on the current worktree branch; do not push without explicit approval.

---

### Task 1: `ParkDLQ` on the `Stats` seam (PG + ClickHouse)

**Files:**
- Create: `internal/store/migrations/clickhouse/0011_dlq.sql`
- Create: `internal/store/dlq.go` (pgxStats.ParkDLQ)
- Create: `internal/store/chstats_dlq.go` (chStats.ParkDLQ)
- Create: `internal/store/dlq_test.go` (pgxStats)
- Create: `internal/store/chstats_dlq_test.go` (chStats)
- Modify: `internal/store/stats.go` (add method to the `Stats` interface)

**Interfaces:**
- Produces: `ParkDLQ(ctx context.Context, serverID, reason string, raw []byte) error` on `store.Stats` (both impls).

- [ ] **Step 1: Add the ClickHouse `dlq` migration**

Create `internal/store/migrations/clickhouse/0011_dlq.sql`:

```sql
-- Dead-letter queue: parks ingest frames that could not be accepted
-- (rate-limited, malformed, or write error). raw is the serialized Snapshot
-- protobuf (T1, literal-free by contract) so a future retry can re-decode it.
-- Append-only, TTL-bounded — there is no retry consumer today; the TTL bounds
-- growth. server_id is '' for pre-server_id failures (unmarshal errors).
CREATE TABLE IF NOT EXISTS dlq (
  received_at DateTime64(3, 'UTC') DEFAULT now64(3),
  server_id   String,
  reason      String,
  raw         String
) ENGINE = MergeTree
PARTITION BY toYYYYMM(received_at)
ORDER BY (received_at, server_id)
TTL toDateTime(received_at) + INTERVAL 14 DAY;
```

- [ ] **Step 2: Write the failing chStats.ParkDLQ test**

Create `internal/store/chstats_dlq_test.go`:

```go
package store_test

import (
	"context"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/dobbo-ca/lynceus/internal/testch"
)

func TestCH_dlq_ParkDLQ_RoundTrip(t *testing.T) {
	ctx := context.Background()
	conn := testch.Start(t)
	if err := store.ApplyClickHouseMigrations(ctx, conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := store.NewCHStats(conn)

	raw := []byte{0x0a, 0x03, 'a', 'b', 'c'} // arbitrary bytes — String must be binary-safe
	if err := s.ParkDLQ(ctx, "srv-1", "rate_limited", raw); err != nil {
		t.Fatalf("park: %v", err)
	}
	// server_id='' path (unmarshal failure) must also land.
	if err := s.ParkDLQ(ctx, "", "unmarshal: boom", []byte("x")); err != nil {
		t.Fatalf("park empty server: %v", err)
	}

	var total uint64
	if err := conn.QueryRow(ctx, `SELECT count() FROM dlq`).Scan(&total); err != nil {
		t.Fatalf("count: %v", err)
	}
	if total != 2 {
		t.Fatalf("dlq rows = %d, want 2", total)
	}

	var reason string
	var gotRaw []byte
	if err := conn.QueryRow(ctx,
		`SELECT reason, raw FROM dlq WHERE server_id = ?`, "srv-1",
	).Scan(&reason, &gotRaw); err != nil {
		t.Fatalf("select: %v", err)
	}
	if reason != "rate_limited" || string(gotRaw) != string(raw) {
		t.Fatalf("row = (%q, %v), want (rate_limited, %v)", reason, gotRaw, raw)
	}
}
```

- [ ] **Step 3: Run it — verify it fails**

Run: `go test ./internal/store/ -run TestCH_dlq_ParkDLQ_RoundTrip -v`
Expected: FAIL to compile — `s.ParkDLQ undefined`.

- [ ] **Step 4: Implement chStats.ParkDLQ**

Create `internal/store/chstats_dlq.go`:

```go
package store

import (
	"context"
	"time"
)

// ParkDLQ appends one failed ingest frame to the ClickHouse dlq table. raw is
// the serialized Snapshot protobuf (T1, literal-free) stored in a binary-safe
// String column. received_at is stamped server-side. Append-only; there is no
// retry consumer today (the table is TTL-bounded).
func (s *chStats) ParkDLQ(ctx context.Context, serverID, reason string, raw []byte) error {
	batch, err := s.conn.PrepareBatch(ctx, "INSERT INTO dlq (received_at, server_id, reason, raw)")
	if err != nil {
		return err
	}
	if err := batch.Append(time.Now().UTC(), serverID, reason, raw); err != nil {
		_ = batch.Abort()
		return err
	}
	return batch.Send()
}
```

- [ ] **Step 5: Run it — verify it passes**

Run: `go test ./internal/store/ -run TestCH_dlq_ParkDLQ_RoundTrip -v`
Expected: PASS (skips if Docker unavailable).

- [ ] **Step 6: Write the failing pgxStats.ParkDLQ test**

Create `internal/store/dlq_test.go`:

```go
package store_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/dobbo-ca/lynceus/internal/testpg"
)

func TestPG_ParkDLQ_RoundTrip(t *testing.T) {
	ctx := context.Background()
	c, err := tcpostgres.Run(ctx, "postgres:16",
		tcpostgres.WithDatabase("lynceus_stats"),
		tcpostgres.WithUsername("test"), tcpostgres.WithPassword("test"),
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
	if err := store.ApplyStatsMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	s := store.NewStats(pool)
	if err := s.ParkDLQ(ctx, "srv-9", "write: boom", []byte("payload")); err != nil {
		t.Fatalf("park: %v", err)
	}
	if err := s.ParkDLQ(ctx, "", "unmarshal: boom", []byte("y")); err != nil {
		t.Fatalf("park empty: %v", err)
	}

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM dlq`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Fatalf("dlq rows = %d, want 2", n)
	}
	// server_id='' stored as NULL (NULLIF).
	var nullServers int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM dlq WHERE server_id IS NULL`).Scan(&nullServers); err != nil {
		t.Fatalf("count null: %v", err)
	}
	if nullServers != 1 {
		t.Fatalf("null server_id rows = %d, want 1", nullServers)
	}
}
```

- [ ] **Step 7: Run it — verify it fails**

Run: `go test ./internal/store/ -run TestPG_ParkDLQ_RoundTrip -v`
Expected: FAIL to compile — `s.ParkDLQ undefined`.

- [ ] **Step 8: Implement pgxStats.ParkDLQ**

Create `internal/store/dlq.go`:

```go
package store

import "context"

// ParkDLQ appends one failed ingest frame to the Postgres dlq table
// (migrations/stats/0002_dlq.sql). An empty serverID is stored as NULL. raw is
// the serialized Snapshot protobuf (T1, literal-free).
func (s *pgxStats) ParkDLQ(ctx context.Context, serverID, reason string, raw []byte) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO dlq (server_id, reason, raw) VALUES (NULLIF($1, ''), $2, $3)`,
		serverID, reason, raw,
	)
	return err
}
```

- [ ] **Step 9: Run it — verify it passes**

Run: `go test ./internal/store/ -run TestPG_ParkDLQ_RoundTrip -v`
Expected: PASS.

- [ ] **Step 10: Add `ParkDLQ` to the `Stats` interface**

In `internal/store/stats.go`, add to the `Stats` interface (after `WriteLogEvents`):

```go
	WriteLogEvents(ctx context.Context, rows []LogEventRow) error
	ParkDLQ(ctx context.Context, serverID, reason string, raw []byte) error
```

- [ ] **Step 11: Build — verify both impls satisfy the interface**

Run: `go build ./...`
Expected: success (the `var _ Stats = (*pgxStats)(nil)` / `(*chStats)(nil)` assertions hold).

- [ ] **Step 12: Commit**

```bash
git add internal/store/migrations/clickhouse/0011_dlq.sql \
  internal/store/dlq.go internal/store/chstats_dlq.go \
  internal/store/dlq_test.go internal/store/chstats_dlq_test.go \
  internal/store/stats.go
git commit -m "feat(store): ParkDLQ on the Stats seam (PG + ClickHouse dlq)"
```

---

### Task 2: `WriteSchemaObjects` on the `Stats` seam (PG + ClickHouse)

**Files:**
- Create: `internal/store/migrations/clickhouse/0012_schema_objects.sql`
- Create: `internal/store/chstats_schema_objects.go` (chStats.WriteSchemaObjects)
- Create: `internal/store/chstats_schema_objects_test.go`
- Modify: `internal/store/schema_objects.go` (add pgxStats.WriteSchemaObjects delegating to the existing upsert)
- Modify: `internal/store/stats.go` (add method to the `Stats` interface)

**PG-side test coverage:** pgxStats.WriteSchemaObjects is a pure delegation to
`UpsertSchemaObjects`, whose semantics are already covered by
`TestSchemaObjects_FirstSeenIsStableAcrossUpserts` (`schema_objects_test.go`), and
whose interface path is exercised end-to-end by the existing PG
`TestServer_persistsSchemaObjectsWithServerSideFirstSeen` once Task 3 rewires the
server. No new PG unit test is added here (DRY); the new behavior worth a test is
the ClickHouse `AggregatingMergeTree` path below.

**Interfaces:**
- Consumes: `SchemaObjectRow{ServerID string; Kind int16; FQN, SchemaName, ObjectName string; SizeBytes int64; IsPartition bool; ParentFQN string}` (already defined in `schema_objects.go`); `chTableIndexBool(bool) uint8` (already defined in `chstats_tableindex.go`).
- Produces: `WriteSchemaObjects(ctx context.Context, rows []SchemaObjectRow) error` on `store.Stats` (both impls).

- [ ] **Step 1: Add the ClickHouse `schema_objects` migration**

Create `internal/store/migrations/clickhouse/0012_schema_objects.sql`:

```sql
-- Object inventory, current-state with a stable first_seen_at (load-bearing for
-- the future Index Advisor / schema-change insights). AggregatingMergeTree
-- collapses to one row per (server_id, kind, fqn) — bounding volume — while
-- min(first_seen_at)/max(last_seen_at) preserve the earliest first-seen and
-- latest last-seen without a read-before-write. anyLast holds the latest scalar
-- values. The writer appends raw values (SimpleAggregateFunction accepts scalars
-- on INSERT), stamping first_seen_at = last_seen_at = now(). No TTL: permanent
-- current-state (like check_mutes), first_seen must never be silently dropped.
CREATE TABLE IF NOT EXISTS schema_objects (
  server_id      String,
  kind           Int16,
  fqn            String,
  schema         SimpleAggregateFunction(anyLast, String),
  name           SimpleAggregateFunction(anyLast, String),
  size_bytes     SimpleAggregateFunction(anyLast, Int64),
  is_partition   SimpleAggregateFunction(anyLast, UInt8),
  parent_fqn     SimpleAggregateFunction(anyLast, String),
  data_tier      SimpleAggregateFunction(anyLast, Int16),
  first_seen_at  SimpleAggregateFunction(min, DateTime64(3, 'UTC')),
  last_seen_at   SimpleAggregateFunction(max, DateTime64(3, 'UTC'))
) ENGINE = AggregatingMergeTree
ORDER BY (server_id, kind, fqn);
```

- [ ] **Step 2: Write the failing chStats.WriteSchemaObjects test**

Create `internal/store/chstats_schema_objects_test.go`:

```go
package store_test

import (
	"context"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/dobbo-ca/lynceus/internal/testch"
)

func TestCH_schemaObjects_WriteAndFirstSeenStable(t *testing.T) {
	ctx := context.Background()
	conn := testch.Start(t)
	if err := store.ApplyClickHouseMigrations(ctx, conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := store.NewCHStats(conn)

	row := store.SchemaObjectRow{
		ServerID: "srv-inv", Kind: 1, FQN: "public.orders",
		SchemaName: "public", ObjectName: "orders", SizeBytes: 8192,
	}
	if err := s.WriteSchemaObjects(ctx, []store.SchemaObjectRow{row}); err != nil {
		t.Fatalf("write 1: %v", err)
	}

	// first_seen after the first observation.
	var fs1 string
	if err := conn.QueryRow(ctx,
		`SELECT toString(min(first_seen_at)) FROM schema_objects
		  WHERE server_id = ? AND kind = ? AND fqn = ?`,
		"srv-inv", int16(1), "public.orders",
	).Scan(&fs1); err != nil {
		t.Fatalf("read fs1: %v", err)
	}
	if fs1 == "" {
		t.Fatal("first_seen_at must be stamped server-side on write")
	}

	// Second observation, larger size — size updates, first_seen must not.
	row.SizeBytes = 16384
	if err := s.WriteSchemaObjects(ctx, []store.SchemaObjectRow{row}); err != nil {
		t.Fatalf("write 2: %v", err)
	}

	// Exactly one logical row per key (AggregatingMergeTree collapses via FINAL).
	var distinct uint64
	if err := conn.QueryRow(ctx,
		`SELECT count() FROM (SELECT 1 FROM schema_objects FINAL
		   WHERE server_id = ? GROUP BY server_id, kind, fqn)`, "srv-inv",
	).Scan(&distinct); err != nil {
		t.Fatalf("distinct: %v", err)
	}
	if distinct != 1 {
		t.Fatalf("distinct keys = %d, want 1", distinct)
	}

	var size int64
	var fs2 string
	if err := conn.QueryRow(ctx,
		`SELECT size_bytes, toString(first_seen_at) FROM schema_objects FINAL
		  WHERE server_id = ? AND kind = ? AND fqn = ?`,
		"srv-inv", int16(1), "public.orders",
	).Scan(&size, &fs2); err != nil {
		t.Fatalf("read merged: %v", err)
	}
	if size != 16384 {
		t.Errorf("size_bytes = %d, want 16384 (latest observation)", size)
	}
	if fs2 != fs1 {
		t.Errorf("first_seen_at = %q, want stable %q", fs2, fs1)
	}
}
```

- [ ] **Step 3: Run it — verify it fails**

Run: `go test ./internal/store/ -run TestCH_schemaObjects_WriteAndFirstSeenStable -v`
Expected: FAIL to compile — `s.WriteSchemaObjects undefined`.

- [ ] **Step 4: Implement chStats.WriteSchemaObjects**

Create `internal/store/chstats_schema_objects.go`:

```go
package store

import (
	"context"
	"time"
)

// schemaObjectCHColumns is the INSERT column order for the ClickHouse
// schema_objects table (migrations/clickhouse/0012_schema_objects.sql).
const schemaObjectCHColumns = "server_id, kind, fqn, schema, name, size_bytes, " +
	"is_partition, parent_fqn, data_tier, first_seen_at, last_seen_at"

// WriteSchemaObjects appends the current-state inventory into the
// AggregatingMergeTree schema_objects table. first_seen_at and last_seen_at are
// stamped to the write time; min/max collapse them across re-observations so
// first_seen stays stable (mirrors the pgxStats now()-stamped upsert). Values
// are raw scalars — SimpleAggregateFunction columns accept them on INSERT.
func (s *chStats) WriteSchemaObjects(ctx context.Context, rows []SchemaObjectRow) error {
	if len(rows) == 0 {
		return nil
	}
	now := time.Now().UTC()
	batch, err := s.conn.PrepareBatch(ctx, "INSERT INTO schema_objects ("+schemaObjectCHColumns+")")
	if err != nil {
		return err
	}
	for i := range rows {
		r := rows[i]
		if err := batch.Append(
			r.ServerID, r.Kind, r.FQN, r.SchemaName, r.ObjectName, r.SizeBytes,
			chTableIndexBool(r.IsPartition), r.ParentFQN, int16(1), now, now,
		); err != nil {
			_ = batch.Abort()
			return err
		}
	}
	return batch.Send()
}
```

- [ ] **Step 5: Run it — verify it passes**

Run: `go test ./internal/store/ -run TestCH_schemaObjects_WriteAndFirstSeenStable -v`
Expected: PASS.

- [ ] **Step 6: Implement pgxStats.WriteSchemaObjects (delegates to the existing upsert)**

In `internal/store/schema_objects.go`, add:

```go
// WriteSchemaObjects satisfies store.Stats for the Postgres backend by
// delegating to the first_seen-preserving upsert. Keeping the delegation (not a
// second SQL copy) means the upsert semantics live in exactly one place.
func (s *pgxStats) WriteSchemaObjects(ctx context.Context, rows []SchemaObjectRow) error {
	return NewSchemaObjects(s.pool).UpsertSchemaObjects(ctx, rows)
}
```

- [ ] **Step 7: Add `WriteSchemaObjects` to the `Stats` interface**

In `internal/store/stats.go`, add to the `Stats` interface (after the `ParkDLQ` line from Task 1):

```go
	ParkDLQ(ctx context.Context, serverID, reason string, raw []byte) error
	WriteSchemaObjects(ctx context.Context, rows []SchemaObjectRow) error
```

- [ ] **Step 8: Build + run the store tests**

Run: `go build ./... && go test ./internal/store/ -run 'SchemaObjects' -v`
Expected: build success (both impls satisfy `Stats`); the CH `AggregatingMergeTree` test and the existing PG `FirstSeenIsStableAcrossUpserts` test PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/store/migrations/clickhouse/0012_schema_objects.sql \
  internal/store/chstats_schema_objects.go internal/store/chstats_schema_objects_test.go \
  internal/store/schema_objects.go internal/store/stats.go
git commit -m "feat(store): WriteSchemaObjects on the Stats seam (PG upsert + CH AggregatingMergeTree)"
```

---

### Task 3: `ingest.Server` uses the `Stats` seam (drop the pool)

**Files:**
- Modify: `internal/ingest/server.go`
- Modify: `internal/ingest/server_test.go` (drop the third `NewServer` arg in `setup`)

**Interfaces:**
- Consumes: `store.Stats.ParkDLQ`, `store.Stats.WriteSchemaObjects` (Tasks 1–2).
- Produces: `ingest.NewServer(cfg Config, stats store.Stats) *Server` (pool param removed).

- [ ] **Step 1: Update `setup` in `server_test.go` to the new signature (failing build)**

In `internal/ingest/server_test.go`, change the constructor line in `setup`:

```go
	srv := httptest.NewServer(ingest.NewServer(cfg, store.NewStats(pool)).Handler())
```

(Remove the trailing `, pool` argument. The single Postgres container still backs
both the stats writes and the dlq/schema_objects tables via `ApplyStatsMigrations`.)

- [ ] **Step 2: Run the package build — verify it fails**

Run: `go build ./internal/ingest/`
Expected: FAIL — `too many arguments in call to ingest.NewServer` (server.go still has the 3-arg signature).

- [ ] **Step 3: Rewrite `NewServer` + the two writes in `server.go`**

In `internal/ingest/server.go`:

Replace the `Server` struct fields (remove `schemaObjects` and `pool`):

```go
type Server struct {
	cfg   Config
	stats store.Stats

	mu       sync.Mutex
	limiters map[string]*rate.Limiter
}
```

Replace `NewServer` (drop the pool param + field wiring):

```go
// NewServer returns a Server. stats is the typed writer/DLQ seam; DLQ and
// schema_objects now ride store.Stats, so the server holds no raw pool.
func NewServer(cfg Config, stats store.Stats) *Server {
	if cfg.ReadTimeout == 0 {
		cfg.ReadTimeout = 30 * time.Second
	}
	if cfg.RateBurst == 0 {
		cfg.RateBurst = 1
	}
	return &Server{
		cfg:      cfg,
		stats:    stats,
		limiters: map[string]*rate.Limiter{},
	}
}
```

Replace `parkDLQ` (route through the seam; keep the log-and-swallow behavior):

```go
func (s *Server) parkDLQ(ctx context.Context, serverID, reason string, raw []byte) {
	if err := s.stats.ParkDLQ(ctx, serverID, reason, raw); err != nil && !errors.Is(err, context.Canceled) {
		log.Printf("ingest: dlq insert failed: %v", err)
	}
}
```

In `persistExtendedRows`, replace the schema_objects write:

```go
	if objs := snapshotToSchemaObjects(snap); len(objs) > 0 {
		if err := s.stats.WriteSchemaObjects(ctx, objs); err != nil {
			return "write schema_objects", err
		}
	}
```

Remove the now-unused `github.com/jackc/pgx/v5/pgxpool` import from `server.go`.

- [ ] **Step 4: Build the package + run the existing server tests**

Run: `go build ./... && go test ./internal/ingest/ -v`
Expected: build success; the existing PG server tests PASS (query_stats, schema_objects, dlq, table/index/settings, insights, log-events, over-limit-DLQ). They skip if Docker is unavailable.

- [ ] **Step 5: Commit**

```bash
git add internal/ingest/server.go internal/ingest/server_test.go
git commit -m "refactor(ingest): route DLQ + schema_objects through store.Stats; drop the raw pool"
```

---

### Task 4: `cmd/ingestion` selects the backend via `store.OpenStats`

**Files:**
- Modify: `cmd/ingestion/main.go`

**Interfaces:**
- Consumes: `store.OpenStats(ctx) (store.Stats, error)`; `ingest.NewServer(cfg, stats)` (Task 3); `checks.NewScheduler(s store.Stats, lockPool *pgxpool.Pool, cs []Check, n Notifier)`.

- [ ] **Step 1: Replace the direct stats-pool wiring with `OpenStats`**

In `cmd/ingestion/main.go`:

Remove the `LYNCEUS_STATS_DSN` block (the `dsn := os.Getenv("LYNCEUS_STATS_DSN")` requirement, its `secure.CheckDatabaseDSN`, the `pool, err := pgxpool.New(ctx, dsn)` + `defer pool.Close()`, and the `store.ApplyStatsMigrations(ctx, pool)` call).

Open the backend-selected stats handle (after the `signal.NotifyContext` block):

```go
	stats, err := store.OpenStats(ctx)
	if err != nil {
		log.Fatalf("open stats backend: %v", err) //nolint:gocritic // exitAfterDefer: best-effort cleanup on fatal exit
	}
```

Keep the config pool (still required for the scheduler's advisory lock) exactly as-is:

```go
	configPool, err := pgxpool.New(ctx, configDSN)
	if err != nil {
		log.Fatalf("connect config db: %v", err)
	}
	defer configPool.Close()
```

Wire the server + scheduler off the one `stats` handle:

```go
	srv := ingest.NewServer(ingest.Config{
		DevToken: token, RateLimit: rateLimit, RateBurst: rateBurst,
	}, stats)

	checksInterval := time.Duration(envInt("LYNCEUS_CHECKS_INTERVAL_SEC", 60)) * time.Second
	scheduler := checks.NewScheduler(stats, configPool, checks.DefaultChecks(), checks.NopNotifier{}).
		WithInterval(checksInterval)
	go scheduler.Run(ctx)
```

Remove the now-unused `secure` import **only if** nothing else in the file uses it — `configDSN` is still validated by `secure.CheckDatabaseDSN`, so `secure` stays. Do not remove `pgxpool` (still used for `configPool`).

- [ ] **Step 2: Build + vet**

Run: `go build ./... && go vet ./cmd/ingestion/`
Expected: success, no unused-import errors.

- [ ] **Step 3: Commit**

```bash
git add cmd/ingestion/main.go
git commit -m "feat(ingestion): select stats backend via store.OpenStats; config pool only for scheduler lock"
```

---

### Task 5: End-to-end ClickHouse ingestion test (acceptance)

**Files:**
- Modify: `internal/ingest/server_test.go` (add the CH e2e test)

**Interfaces:**
- Consumes: `testch.StartDSN(t) (driver.Conn, string)`; `store.OpenStats`; `ingest.NewServer(cfg, stats)`; `collector.NewShipper`.

- [ ] **Step 1: Write the failing CH e2e test**

In `internal/ingest/server_test.go`, add (add `"github.com/dobbo-ca/lynceus/internal/testch"` to imports):

```go
// TestIngest_clickhouseBackend_e2e is the ly-cwr.7 acceptance test: it wires the
// ingest server against a real ClickHouse selected through store.OpenStats (the
// same path cmd/ingestion uses), ships a Snapshot, and asserts telemetry +
// schema_objects land in ClickHouse and nothing is parked. It wires the Server
// directly (no config pool / scheduler — those are not on the ingest write path).
func TestIngest_clickhouseBackend_e2e(t *testing.T) {
	ctx := context.Background()
	conn, dsn := testch.StartDSN(t)

	t.Setenv("LYNCEUS_STATS_BACKEND", "clickhouse")
	t.Setenv("LYNCEUS_CLICKHOUSE_DSN", dsn)
	stats, err := store.OpenStats(ctx) // applies CH migrations
	if err != nil {
		t.Fatalf("open stats: %v", err)
	}

	srv := httptest.NewServer(ingest.NewServer(ingest.Config{
		DevToken: "dev", RateLimit: 10, RateBurst: 10,
	}, stats).Handler())
	t.Cleanup(srv.Close)

	snap := &lynceusv1.Snapshot{
		ServerId:        "srv-ch",
		CollectedAtUnix: time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC).Unix(),
		QueryStats: []*lynceusv1.QueryStat{{
			Fingerprint: "fp-ch", NormalizedQuery: "SELECT $1", Calls: 3, TotalTimeMs: 12,
		}},
		SchemaObjects: []*lynceusv1.SchemaObject{{
			Kind: lynceusv1.ObjectKind_OBJECT_KIND_TABLE,
			Schema: "public", Name: "orders", Fqn: "public.orders", SizeBytes: 8192,
		}},
	}
	if err := collector.NewShipper(wsURL(srv.URL), "dev").Send(ctx, snap); err != nil {
		t.Fatalf("send: %v", err)
	}

	// query_stats landed in ClickHouse.
	var qs uint64
	for i := 0; i < 100 && qs == 0; i++ {
		_ = conn.QueryRow(ctx,
			`SELECT count() FROM query_stats WHERE server_id='srv-ch' AND fingerprint='fp-ch'`).Scan(&qs)
		if qs > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if qs != 1 {
		t.Fatalf("query_stats in CH = %d, want 1", qs)
	}

	// schema_objects landed in ClickHouse.
	var so uint64
	if err := conn.QueryRow(ctx,
		`SELECT count() FROM schema_objects FINAL
		  WHERE server_id='srv-ch' AND fqn='public.orders'`).Scan(&so); err != nil {
		t.Fatalf("schema_objects count: %v", err)
	}
	if so != 1 {
		t.Fatalf("schema_objects in CH = %d, want 1", so)
	}

	// Happy path parks nothing.
	var dlq uint64
	_ = conn.QueryRow(ctx, `SELECT count() FROM dlq`).Scan(&dlq)
	if dlq != 0 {
		t.Fatalf("dlq rows = %d, want 0 on the happy path", dlq)
	}
}

// TestIngest_clickhouseBackend_parksOverLimit asserts an over-limit frame is
// parked in the ClickHouse dlq table (never dropped).
func TestIngest_clickhouseBackend_parksOverLimit(t *testing.T) {
	ctx := context.Background()
	conn, dsn := testch.StartDSN(t)
	t.Setenv("LYNCEUS_STATS_BACKEND", "clickhouse")
	t.Setenv("LYNCEUS_CLICKHOUSE_DSN", dsn)
	stats, err := store.OpenStats(ctx)
	if err != nil {
		t.Fatalf("open stats: %v", err)
	}

	srv := httptest.NewServer(ingest.NewServer(ingest.Config{
		DevToken: "dev", RateLimit: 1, RateBurst: 1,
	}, stats).Handler())
	t.Cleanup(srv.Close)

	ship := collector.NewShipper(wsURL(srv.URL), "dev")
	if err := ship.Send(ctx, makeSnapshot("srv-ch2", "fp-1", "SELECT $1", 1)); err != nil {
		t.Fatalf("first send: %v", err)
	}
	_ = ship.Send(ctx, makeSnapshot("srv-ch2", "fp-2", "SELECT $1", 2)) // over limit

	var dlq uint64
	for i := 0; i < 100 && dlq == 0; i++ {
		_ = conn.QueryRow(ctx,
			`SELECT count() FROM dlq WHERE server_id='srv-ch2' AND reason='rate_limited'`).Scan(&dlq)
		if dlq > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if dlq != 1 {
		t.Fatalf("CH dlq rows for srv-ch2 = %d, want 1", dlq)
	}
}
```

- [ ] **Step 2: Run the e2e tests — verify they pass**

Run: `go test ./internal/ingest/ -run 'TestIngest_clickhouseBackend' -v`
Expected: both PASS (skip if Docker unavailable).

- [ ] **Step 3: Full build + package tests**

Run: `go build ./... && go test ./internal/ingest/ ./internal/store/ -v`
Expected: build success; all ingest + store tests PASS/skip.

- [ ] **Step 4: Commit**

```bash
git add internal/ingest/server_test.go
git commit -m "test(ingest): end-to-end ClickHouse ingestion via store.OpenStats (ly-cwr.7 acceptance)"
```

---

## Post-implementation

- [ ] Run the full suite: `go test ./...` (Docker required for integration tests).
- [ ] `bd label remove ly-cwr.7 needs-plan && bd label add ly-cwr.7 ready-impl` was done when this plan landed; on completion move to `ready-test`, verify acceptance, then `bd close ly-cwr.7`.
- [ ] Update `docs/reference/clickhouse-schema.md`: the `dlq` / `schema_objects` tables move from "Pending" to live once merged.

## Acceptance (from the bead / spec §8)

- `cmd/ingestion` obtains `Stats` via `store.OpenStats` and runs end-to-end with `LYNCEUS_STATS_BACKEND=clickhouse` (Task 4 + Task 5).
- Integration test against real ClickHouse passes (Task 5).
- DLQ + schema_objects no longer require a stats PG pool (Tasks 1–3); `ingest.Server` holds no `*pgxpool.Pool`.
- No Postgres stats pool needed when `backend=clickhouse` (a config pool remains for the scheduler lock).

> **SUPERSEDED (2026-07-14):** the user chose CH-only (remove pgxStats). Tasks 2–5 here are
> replaced by `2026-07-14-ly-cwr8-clickhouse-only-remove-pgxstats.md`. Task 1 (ParkDLQ) already
> landed and is reused. This file is kept for history.
