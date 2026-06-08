# ly-xqf.5 — Schema / Object Inventory + Size Treemap (First-Seen) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a collector-side schema/object inventory reader that walks the monitored Postgres catalogs and produces a normalized inventory of schemas, tables, indexes, views, functions, and sequences with sizes (`pg_relation_size` / `pg_total_relation_size`) and a stable first-seen timestamp per object — gated behind a collector-side `ignore_schema_regexp` / `include_schema_regexp` filter so sensitive schema names never reach the wire.

**Architecture:** A new `internal/collector` reader (`inventory.go`) issues one read-only catalog query per object kind, applies the configured schema regex filter **at the collector boundary** (before constructing any protobuf), and returns a slice of T1 `SchemaObject` messages. First-seen timestamps live in the **stats DB** (so they survive collector restarts) in a new `schema_objects` table keyed on `(server_id, kind, fqn)`. A new T1 proto message `SchemaObject` is added to `proto/lynceus/v1/snapshot.proto` and carried on the existing `Snapshot`. A contract test enforces that the new message has only structural-metadata + size fields — no field capable of carrying table DATA.

> **Sibling reader (`ly-xqf.6`) and Snapshot field-number reservation.** `ly-xqf.6` (table size / growth + TOAST) adds a SIBLING `table_stats` table — a **weekly range-partitioned, append-only growth time series** — that **reuses the SAME `SchemaFilter` instance** as this reader (one filter is built once in `cmd/collector/main.go` and passed to both `NewInventory` and `NewTableStatsReader`, so the privacy boundary is identical for both). The two storage models are intentionally distinct and coexist: this plan's `schema_objects` stays a **current-state upsert keyed `(server_id, kind, fqn)` with a stable first-seen** (`first_seen_at` never overwritten on conflict), while `ly-xqf.6`'s `table_stats` is the **append-only per-snapshot growth series** (so growth is derivable). Mixing them would lose either first-seen stability or growth history. On the wire, `Snapshot` field numbers are reserved across the three independent Layer-0 beads to avoid collisions: **6 = `schema_objects` (this bead, ly-xqf.5), 7 = `table_stats` (ly-xqf.6), 8 = `log_events` (ly-cxe.2)**. This plan claims **field 6** for `schema_objects`; it must never reuse 7 or 8. (See spec §4.2 / §4.1 field-number reservation.)

**Tech Stack:** Go 1.23+, `jackc/pgx/v5`, Protocol Buffers, `google.golang.org/protobuf/reflect/protoreflect`, `regexp` (Go RE2 — not Postgres-side regex), testcontainers-go (`postgres:16`) for integration tests, existing embedded migration runner in `internal/store`.

**Spec references:**
- `docs/specs/2026-05-29-lynceus-design.md` §2 (Privacy & Data-Classification Model — T1 has no field capable of carrying a literal value).
- `docs/specs/2026-05-29-lynceus-features.md` §9 — *"Schema/object inventory (tables, indexes, views, functions; size treemap; first-seen) — MUST, local. Schema names may be sensitive → `ignore_schema_regexp` filter."*
- `docs/specs/2026-06-08-layer0-foundation.md` §4.2 — reconciliation: keep this plan verbatim except migration renumbering (`0005_schema_objects.sql`) and the `table_stats` sibling / Snapshot field-number reservation notes.
- `docs/superpowers/plans/2026-05-29-lynceus-mvp-vertical-slice.md` — plan style reference.

**Scope:**
- IS: schemas, tables (incl. partitioned tables, materialized views), indexes, views, functions, sequences; per-object byte size (`pg_total_relation_size` for relations, `pg_relation_size` for indexes; views/functions/sequences carry size = 0); stable `first_seen_at` per `(server_id, kind, fqn)`.
- IS: collector-boundary regex filter (`include_schema_regexp` allowlist + `ignore_schema_regexp` denylist) — applied **before** any proto is constructed.
- ISN'T: column-level stats (`null_frac`, `n_distinct`, avg width) — `ly-xqf.8`.
- ISN'T: partition breakdown (per-partition sizes) — `ly-xqf.14`. Integration point: `pg_class.relispartition` is exposed on `SchemaObject` so a future plan can group children under parents without re-querying.
- ISN'T: per-table size/growth time series + TOAST/heap/index split + vacuum/dead-tuple metrics — `ly-xqf.6` (its own append-only weekly-partitioned `table_stats` table; reuses this plan's `SchemaFilter`).
- ISN'T: HOT update tracking — `ly-xqf.13`.
- ISN'T: buffer-cache stats — `ly-xqf.9`.
- ISN'T: any UI — downstream.

---

## File Structure

```
lynceus/
  proto/
    lynceus/v1/
      snapshot.proto                                # MODIFY: add SchemaObject + ObjectKind + Snapshot.schema_objects (field 6)
  internal/
    proto/lynceus/v1/
      schema_object_contract_test.go                # CREATE: T1 contract test for SchemaObject (no DATA-bearing field)
  internal/
    collector/
      inventory.go                                  # CREATE: catalog reader + regex filter (boundary enforcement)
      inventory_test.go                             # CREATE: integration test (real Postgres, regex filter exclusion)
      inventory_filter.go                           # CREATE: SchemaFilter (include+ignore regex, IsAllowed) — REUSED by ly-xqf.6 TableStatsReader
      inventory_filter_test.go                      # CREATE: unit tests for SchemaFilter
    store/
      migrations/stats/
        0005_schema_objects.sql                     # CREATE: schema_objects table with PK (server_id, kind, fqn) — 0003/0004 already taken
      schema_objects.go                             # CREATE: UpsertSchemaObjects, FirstSeenAt lookup
      schema_objects_test.go                        # CREATE: integration test for first-seen idempotency
```

`internal/collector/inventory.go` is the only place that constructs `lynceusv1.SchemaObject` values. The filter is invoked **inside** the reader, before construction — there is no path from a Postgres row to a proto value that bypasses the filter. This is the privacy boundary. (`ly-xqf.6`'s `TableStatsReader` reuses the exact same `SchemaFilter` instance, so a filtered schema yields zero rows on both readers.)

---

## Task 1: T1 proto — add `SchemaObject` message and `ObjectKind` enum

**Files:**
- Modify: `proto/lynceus/v1/snapshot.proto`
- Create: `internal/proto/lynceus/v1/schema_object_contract_test.go`

- [ ] **Step 1: Write the failing contract test FIRST.** It must (a) require `SchemaObject` to exist, (b) restrict its fields to a known allowlist of structural-metadata + size fields, (c) require `Snapshot.schema_objects` to exist as a repeated field of `SchemaObject`.

Create `internal/proto/lynceus/v1/schema_object_contract_test.go`:

```go
// Package lynceusv1_test enforces the T1 privacy contract for the
// schema-object inventory message. SchemaObject is allowed to carry
// schema/relation NAMES (those are the inventory's value; privacy for
// names is enforced at the collector boundary via the ignore/include
// schema regex filter), but it MUST NOT carry any field capable of
// holding table DATA — no column samples, no row contents, no DEFAULT
// expressions, no comments, no ACLs, no constraint values.
package lynceusv1_test

import (
	"testing"

	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
)

// TestSchemaObjectHasOnlyStructuralFields enforces the T1 invariant for
// SchemaObject: every field must be a structural identifier (schema /
// relation / kind / parent fqn), a size metric, a boolean catalog flag,
// or a first-seen timestamp. Any field that could carry table DATA
// (column samples, defaults, constraint values, free-form comments,
// ACL strings) must fail this test.
func TestSchemaObjectHasOnlyStructuralFields(t *testing.T) {
	allowed := map[string]struct{}{
		"kind":               {}, // ObjectKind enum
		"schema":             {}, // namespace name (sensitive — filtered upstream)
		"name":               {}, // relation/function/sequence name
		"fqn":                {}, // "schema.name" — derived, stable identifier
		"size_bytes":         {}, // pg_total_relation_size / pg_relation_size
		"is_partition":       {}, // pg_class.relispartition
		"parent_fqn":         {}, // inherited parent (partition parent), "" if none
		"first_seen_at_unix": {}, // stable timestamp from the stats DB
	}

	fields := (&lynceusv1.SchemaObject{}).ProtoReflect().Descriptor().Fields()
	for i := 0; i < fields.Len(); i++ {
		name := string(fields.Get(i).Name())
		if _, ok := allowed[name]; !ok {
			t.Fatalf(
				"unexpected field %q in T1 SchemaObject — possible DATA leak. "+
					"T1 SchemaObject may carry STRUCTURAL metadata only. If you "+
					"need column defaults, constraint values, comments, or ACLs, "+
					"define a separate T2 message gated behind RBAC + audit.",
				name,
			)
		}
	}
}

// TestSchemaObjectFieldKinds guards against silent type widenings that
// could let an attacker stuff arbitrary bytes through a "scalar" field.
func TestSchemaObjectFieldKinds(t *testing.T) {
	fields := (&lynceusv1.SchemaObject{}).ProtoReflect().Descriptor().Fields()

	wantKind := map[string]string{
		"schema":             "string",
		"name":               "string",
		"fqn":                "string",
		"parent_fqn":         "string",
		"size_bytes":         "int64",
		"is_partition":       "bool",
		"first_seen_at_unix": "int64",
	}
	for n, want := range wantKind {
		f := fields.ByName(protoName(n))
		if f == nil {
			t.Fatalf("SchemaObject missing required field %q", n)
		}
		if got := f.Kind().String(); got != want {
			t.Fatalf("SchemaObject.%s kind = %q, want %q", n, got, want)
		}
	}

	// kind must be the ObjectKind enum, not a free-form string.
	kindField := fields.ByName("kind")
	if kindField == nil {
		t.Fatal("SchemaObject missing required field \"kind\"")
	}
	if kindField.Kind().String() != "enum" {
		t.Fatalf("SchemaObject.kind kind = %q, want \"enum\" (ObjectKind)", kindField.Kind().String())
	}
}

// TestSnapshotCarriesSchemaObjects enforces that the existing Snapshot
// message carries a repeated SchemaObject — the inventory ships on the
// existing wire envelope, no second protocol.
func TestSnapshotCarriesSchemaObjects(t *testing.T) {
	fields := (&lynceusv1.Snapshot{}).ProtoReflect().Descriptor().Fields()
	f := fields.ByName("schema_objects")
	if f == nil {
		t.Fatal("Snapshot missing required repeated field \"schema_objects\"")
	}
	if !f.IsList() {
		t.Fatal("Snapshot.schema_objects must be repeated")
	}
	if f.Message() == nil || string(f.Message().Name()) != "SchemaObject" {
		t.Fatalf("Snapshot.schema_objects must be repeated SchemaObject, got %v", f.Message())
	}
}
```

Add this helper at the bottom of the test file (or in a shared `helpers_test.go` if one exists in this package):

```go
import "google.golang.org/protobuf/reflect/protoreflect"

func protoName(n string) protoreflect.Name { return protoreflect.Name(n) }
```

- [ ] **Step 2: Run the test and verify it fails.**

```
go test ./internal/proto/lynceus/v1/...
```

Expected: FAIL — `SchemaObject` type and `Snapshot.schema_objects` field do not exist yet (build error: `undefined: lynceusv1.SchemaObject`).

- [ ] **Step 3: Add the proto definitions.**

Edit `proto/lynceus/v1/snapshot.proto`. Append after the existing `QueryStat` message and update `Snapshot`:

```proto
// ObjectKind enumerates the schema-catalog object types Lynceus inventories.
// New kinds may be added; the wire enum is open by virtue of proto3 semantics.
enum ObjectKind {
  OBJECT_KIND_UNSPECIFIED = 0;
  OBJECT_KIND_SCHEMA      = 1;
  OBJECT_KIND_TABLE       = 2; // includes partitioned tables and materialized views
  OBJECT_KIND_INDEX       = 3;
  OBJECT_KIND_VIEW        = 4;
  OBJECT_KIND_FUNCTION    = 5;
  OBJECT_KIND_SEQUENCE    = 6;
}

// SchemaObject is one row of the monitored database's structural
// inventory. It carries STRUCTURAL metadata only — schema and relation
// names, an enum kind, a byte size, partition flags, and a first-seen
// timestamp. It NEVER carries column values, default expressions,
// constraint values, comments, or ACLs. Those are DATA-class and
// require a separate T2 message gated behind RBAC + audit.
//
// Schema NAMES themselves can be sensitive. The privacy mechanism for
// names is the collector's ignore_schema_regexp / include_schema_regexp
// filter applied BEFORE this message is constructed — not the proto.
message SchemaObject {
  ObjectKind kind     = 1;
  string schema       = 2; // namespace name
  string name         = 3; // object name within the schema (empty for kind=SCHEMA)
  string fqn          = 4; // "schema.name" — stable identifier, also empty-name-safe ("schema.")
  int64  size_bytes   = 5; // pg_total_relation_size for tables; pg_relation_size for indexes; 0 otherwise
  bool   is_partition = 6; // pg_class.relispartition — child of a partitioned parent
  string parent_fqn   = 7; // partition parent fqn, "" if none
  int64  first_seen_at_unix = 8; // stable first-seen for (server, kind, fqn) — looked up from stats DB
}
```

Update the existing `Snapshot` message — add the `schema_objects` field. **Use field number 6: it is the Layer-0 reservation for `schema_objects` (7 = `table_stats` / ly-xqf.6, 8 = `log_events` / ly-cxe.2 are reserved for sibling beads and MUST NOT be reused here).** Fields 1–5 are already taken (`server_id`, `collected_at_unix`, `query_stats`, `activity_buckets`, `query_plans`):

```proto
message Snapshot {
  string server_id = 1;
  int64 collected_at_unix = 2;
  repeated QueryStat query_stats = 3;

  // Per-bucket connection-state histograms from pg_stat_activity. See
  // ActivityBucket — T1, counts/labels only.
  repeated ActivityBucket activity_buckets = 4;

  // Normalized auto_explain plans extracted from the Postgres log. See
  // QueryPlan in plan.proto — T1, no literals.
  repeated QueryPlan query_plans = 5;

  // Structural schema/object inventory (schemas, tables, indexes, views,
  // functions, sequences) with sizes + stable first-seen. See SchemaObject
  // — T1, structural metadata only. Field 6 is the Layer-0 reservation;
  // 7=table_stats (ly-xqf.6), 8=log_events (ly-cxe.2) are reserved siblings.
  repeated SchemaObject schema_objects = 6;
}
```

- [ ] **Step 4: Regenerate Go bindings.**

```
make proto
```

Expected: writes updated files under `internal/proto/lynceus/v1/`; `go build ./...` succeeds.

- [ ] **Step 5: Run the contract test and verify it passes.**

```
go test ./internal/proto/lynceus/v1/...
```

Expected: PASS — both new tests and the pre-existing `TestQueryStatHasOnlyNormalizedFields` / `TestNormalizedQueryFieldShape` / `TestActivityBucketHasOnlyAggregateFields` / `TestQueryPlanHasNoLiteralFields` continue to pass.

- [ ] **Step 6: Commit.**

```
git add proto/lynceus/v1/snapshot.proto \
        internal/proto/lynceus/v1/snapshot.pb.go \
        internal/proto/lynceus/v1/schema_object_contract_test.go
git commit -m "feat(proto): add T1 SchemaObject + ObjectKind with structural-only contract test (ly-xqf.5)"
```

---

## Task 2: Stats-DB migration — `schema_objects` table with first-seen PK

**Files:**
- Create: `internal/store/migrations/stats/0005_schema_objects.sql`

> **Migration numbering note.** `0003`/`0004` are already taken on disk by `0003_activity_buckets.sql` and `0004_query_plans.sql`. The runner applies migrations in **lexical** order (`internal/store/migrate.go:48`, `sort.Strings(files)`), so this inventory migration MUST be **`0005_schema_objects.sql`**. (`ly-xqf.6` will follow with `0006_table_stats.sql` for its sibling growth-series table.)

- [ ] **Step 1: Write the migration.** First-seen timestamps live here — surviving collector restarts and giving downstream consumers a stable "schema changed" signal. The primary key is `(server_id, kind, fqn)`. Vanilla Postgres only — no extensions.

Create `internal/store/migrations/stats/0005_schema_objects.sql`:

```sql
-- Schema-object inventory with stable first-seen timestamps.
--
-- One row per (server_id, kind, fqn). first_seen_at is set on initial
-- insert and never updated; last_seen_at and size_bytes_latest are
-- refreshed on every collector snapshot. The table lives in the stats
-- DB so first-seen survives collector restarts and is queryable
-- downstream alongside the rest of the inventory.
--
-- This is the CURRENT-STATE upsert table. The sibling ly-xqf.6
-- table_stats table (0006_table_stats.sql) is the APPEND-ONLY weekly-
-- partitioned growth time series; the two coexist deliberately.
--
-- Vanilla PostgreSQL — no extensions. Runs on RDS / Aurora / Cloud SQL.

CREATE TABLE schema_objects (
    server_id            TEXT        NOT NULL,
    kind                 SMALLINT    NOT NULL, -- mirrors proto ObjectKind numeric value
    fqn                  TEXT        NOT NULL, -- "schema.name", or "schema." for kind=SCHEMA
    schema_name          TEXT        NOT NULL,
    object_name          TEXT        NOT NULL, -- empty string for kind=SCHEMA
    size_bytes_latest    BIGINT      NOT NULL DEFAULT 0,
    is_partition         BOOLEAN     NOT NULL DEFAULT false,
    parent_fqn           TEXT        NOT NULL DEFAULT '',
    data_tier            SMALLINT    NOT NULL DEFAULT 1, -- T1 (kept for parity with query_stats)
    first_seen_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (server_id, kind, fqn)
);

-- Cheap server-scoped scans for the inventory view / treemap.
CREATE INDEX schema_objects_server_kind ON schema_objects (server_id, kind);
-- Find children of a partitioned parent cheaply.
CREATE INDEX schema_objects_parent      ON schema_objects (server_id, parent_fqn)
    WHERE parent_fqn <> '';
```

- [ ] **Step 2: Verify the migration applies cleanly via the existing migration runner.** The existing `internal/store/migrate.go` already embeds `migrations/stats/*.sql` and applies them in lexical order (`migrate.go:48`) — no code change needed. `0005` sorts after `0004_query_plans.sql`, so ordering is correct.

```
go build ./internal/store/...
```

Expected: PASS (build only — runtime application is verified by Task 3's integration test).

- [ ] **Step 3: Commit.**

```
git add internal/store/migrations/stats/0005_schema_objects.sql
git commit -m "feat(store): add schema_objects table (0005) with (server_id, kind, fqn) PK and first-seen (ly-xqf.5)"
```

---

## Task 3: Stats-DB code — `Upsert` with first-seen-preserving semantics

**Files:**
- Create: `internal/store/schema_objects.go`
- Create: `internal/store/schema_objects_test.go`

- [ ] **Step 1: Write the failing integration test.** It must prove first-seen is preserved on re-upsert (the load-bearing property of this whole feature).

Create `internal/store/schema_objects_test.go`:

```go
// Integration test for the schema_objects upsert path. Spins up a real
// Postgres via testcontainers, applies the bundled stats migrations,
// and asserts:
//
//  1. UpsertSchemaObjects inserts new rows.
//  2. A second UpsertSchemaObjects for the same (server_id, kind, fqn)
//     PRESERVES first_seen_at and ADVANCES last_seen_at and size.
//  3. ListByServer returns the latest sizes + the original first_seen.
package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/dobbo-ca/lynceus/internal/store"
)

func TestSchemaObjects_FirstSeenIsStableAcrossUpserts(t *testing.T) {
	ctx := context.Background()

	c, err := tcpostgres.Run(ctx,
		"postgres:16",
		tcpostgres.WithDatabase("lynceus_stats"),
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

	if err := store.ApplyStatsMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	so := store.NewSchemaObjects(pool)

	const srv = "srv-1"
	row := store.SchemaObjectRow{
		ServerID:    srv,
		Kind:        int16(2), // OBJECT_KIND_TABLE
		FQN:         "public.users",
		SchemaName:  "public",
		ObjectName:  "users",
		SizeBytes:   1024,
		IsPartition: false,
	}

	// First upsert.
	if err := so.UpsertSchemaObjects(ctx, []store.SchemaObjectRow{row}); err != nil {
		t.Fatalf("upsert 1: %v", err)
	}
	got1, err := so.ListByServer(ctx, srv)
	if err != nil {
		t.Fatalf("list 1: %v", err)
	}
	if len(got1) != 1 {
		t.Fatalf("expected 1 row, got %d", len(got1))
	}
	firstSeen := got1[0].FirstSeenAt
	if firstSeen.IsZero() {
		t.Fatal("first_seen_at is zero")
	}

	// Wait long enough that any UPDATE of first_seen_at would be detectable.
	time.Sleep(50 * time.Millisecond)

	// Re-upsert with a new size — first_seen must NOT change.
	row.SizeBytes = 4096
	if err := so.UpsertSchemaObjects(ctx, []store.SchemaObjectRow{row}); err != nil {
		t.Fatalf("upsert 2: %v", err)
	}
	got2, err := so.ListByServer(ctx, srv)
	if err != nil {
		t.Fatalf("list 2: %v", err)
	}
	if len(got2) != 1 {
		t.Fatalf("expected 1 row after re-upsert, got %d", len(got2))
	}
	if !got2[0].FirstSeenAt.Equal(firstSeen) {
		t.Errorf("first_seen_at changed across upserts: was %v, now %v",
			firstSeen, got2[0].FirstSeenAt)
	}
	if got2[0].SizeBytes != 4096 {
		t.Errorf("size_bytes_latest not updated: got %d, want 4096", got2[0].SizeBytes)
	}
	if !got2[0].LastSeenAt.After(firstSeen) {
		t.Errorf("last_seen_at did not advance: first=%v last=%v",
			firstSeen, got2[0].LastSeenAt)
	}
}
```

- [ ] **Step 2: Run the test and verify it fails.**

```
go test ./internal/store/...
```

Expected: FAIL — `store.NewSchemaObjects`, `store.SchemaObjectRow`, `store.UpsertSchemaObjects`, `store.ListByServer` do not exist (build error).

- [ ] **Step 3: Implement `schema_objects.go`.**

Create `internal/store/schema_objects.go`:

```go
package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SchemaObjects provides typed access to the schema_objects table.
//
// Upserts preserve first_seen_at — the load-bearing semantic of this
// feature. Downstream consumers (M3 Index Advisor, "schema changed"
// insights) rely on first_seen_at being stable across collector
// restarts.
type SchemaObjects struct{ pool *pgxpool.Pool }

// NewSchemaObjects returns a SchemaObjects bound to pool.
func NewSchemaObjects(pool *pgxpool.Pool) *SchemaObjects {
	return &SchemaObjects{pool: pool}
}

// SchemaObjectRow is one row to upsert. Kind mirrors proto ObjectKind's
// numeric value. The caller is responsible for the collector-side
// schema-name filter — by the time a row reaches this struct, its
// schema is already approved for transmission.
type SchemaObjectRow struct {
	ServerID    string
	Kind        int16
	FQN         string
	SchemaName  string
	ObjectName  string
	SizeBytes   int64
	IsPartition bool
	ParentFQN   string
}

// SchemaObjectRecord is one row returned by ListByServer.
type SchemaObjectRecord struct {
	ServerID    string
	Kind        int16
	FQN         string
	SchemaName  string
	ObjectName  string
	SizeBytes   int64
	IsPartition bool
	ParentFQN   string
	FirstSeenAt time.Time
	LastSeenAt  time.Time
}

// UpsertSchemaObjects inserts or updates the given rows. On conflict
// the size, partition fields, and last_seen_at are refreshed but
// first_seen_at is NEVER overwritten — that is the stability guarantee.
// All inserts run in a single transaction.
func (s *SchemaObjects) UpsertSchemaObjects(ctx context.Context, rows []SchemaObjectRow) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	batch := &pgx.Batch{}
	for _, r := range rows {
		batch.Queue(
			`INSERT INTO schema_objects
			   (server_id, kind, fqn, schema_name, object_name,
			    size_bytes_latest, is_partition, parent_fqn,
			    first_seen_at, last_seen_at)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now(), now())
			 ON CONFLICT (server_id, kind, fqn) DO UPDATE SET
			   size_bytes_latest = EXCLUDED.size_bytes_latest,
			   is_partition      = EXCLUDED.is_partition,
			   parent_fqn        = EXCLUDED.parent_fqn,
			   last_seen_at      = now()
			 -- first_seen_at intentionally omitted: stable across upserts.
			`,
			r.ServerID, r.Kind, r.FQN, r.SchemaName, r.ObjectName,
			r.SizeBytes, r.IsPartition, r.ParentFQN,
		)
	}
	br := tx.SendBatch(ctx, batch)
	for range rows {
		if _, err := br.Exec(); err != nil {
			_ = br.Close()
			return err
		}
	}
	if err := br.Close(); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ListByServer returns every schema_objects row for the given server,
// ordered by (kind, fqn) for deterministic test assertions.
func (s *SchemaObjects) ListByServer(ctx context.Context, serverID string) ([]SchemaObjectRecord, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT server_id, kind, fqn, schema_name, object_name,
		        size_bytes_latest, is_partition, parent_fqn,
		        first_seen_at, last_seen_at
		   FROM schema_objects
		  WHERE server_id = $1
		  ORDER BY kind, fqn`,
		serverID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SchemaObjectRecord
	for rows.Next() {
		var r SchemaObjectRecord
		if err := rows.Scan(
			&r.ServerID, &r.Kind, &r.FQN, &r.SchemaName, &r.ObjectName,
			&r.SizeBytes, &r.IsPartition, &r.ParentFQN,
			&r.FirstSeenAt, &r.LastSeenAt,
		); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// FirstSeenAt returns the stored first_seen_at for a single object, or
// the zero Time if the object has never been seen. Used by the
// collector to stamp outgoing SchemaObject messages with a stable
// timestamp; if zero, the collector uses the snapshot's collected_at.
func (s *SchemaObjects) FirstSeenAt(ctx context.Context, serverID string, kind int16, fqn string) (time.Time, error) {
	var t time.Time
	err := s.pool.QueryRow(ctx,
		`SELECT first_seen_at FROM schema_objects
		  WHERE server_id = $1 AND kind = $2 AND fqn = $3`,
		serverID, kind, fqn,
	).Scan(&t)
	if err == pgx.ErrNoRows {
		return time.Time{}, nil
	}
	return t, err
}
```

- [ ] **Step 4: Run the test and verify it passes.**

```
go test ./internal/store/... -run TestSchemaObjects_FirstSeenIsStableAcrossUpserts
```

Expected: PASS.

- [ ] **Step 5: Commit.**

```
git add internal/store/schema_objects.go internal/store/schema_objects_test.go
git commit -m "feat(store): UpsertSchemaObjects preserves first_seen_at across re-upserts (ly-xqf.5)"
```

---

## Task 4: `SchemaFilter` — collector-boundary regex include/exclude

**Files:**
- Create: `internal/collector/inventory_filter.go`
- Create: `internal/collector/inventory_filter_test.go`

The filter is the privacy mechanism for schema NAMES. It runs **before** any proto value is constructed. The reader in Task 5 will refuse to emit a `SchemaObject` for any schema the filter rejects. (`ly-xqf.6`'s `TableStatsReader` consumes the SAME `*SchemaFilter` instance built once in `cmd/collector/main.go`, so the two readers share an identical boundary — keep this type's API stable for that reason.)

- [ ] **Step 1: Write the failing unit tests.**

Create `internal/collector/inventory_filter_test.go`:

```go
package collector_test

import (
	"testing"

	"github.com/dobbo-ca/lynceus/internal/collector"
)

func TestSchemaFilter_DefaultAllowsAll(t *testing.T) {
	f, err := collector.NewSchemaFilter("", "")
	if err != nil {
		t.Fatalf("NewSchemaFilter: %v", err)
	}
	for _, s := range []string{"public", "patient_phi", "internal", ""} {
		if !f.IsAllowed(s) {
			t.Errorf("default filter rejected %q", s)
		}
	}
}

func TestSchemaFilter_IgnoreExcludesMatch(t *testing.T) {
	f, err := collector.NewSchemaFilter("", "^patient_.*")
	if err != nil {
		t.Fatalf("NewSchemaFilter: %v", err)
	}
	if f.IsAllowed("patient_phi") {
		t.Error("expected patient_phi to be excluded by ignore_schema_regexp")
	}
	if !f.IsAllowed("public") {
		t.Error("expected public to be allowed")
	}
}

func TestSchemaFilter_IncludeIsAllowlist(t *testing.T) {
	f, err := collector.NewSchemaFilter("^(public|reporting)$", "")
	if err != nil {
		t.Fatalf("NewSchemaFilter: %v", err)
	}
	if !f.IsAllowed("public") {
		t.Error("expected public to be allowed")
	}
	if !f.IsAllowed("reporting") {
		t.Error("expected reporting to be allowed")
	}
	if f.IsAllowed("patient_phi") {
		t.Error("expected patient_phi to be rejected (not on allowlist)")
	}
}

// Ignore wins over include — a schema explicitly excluded must not be
// rescued by being on the include list. This makes the regex pair
// safe to combine (operator can allowlist "*" then carve out PHI).
func TestSchemaFilter_IgnoreWinsOverInclude(t *testing.T) {
	f, err := collector.NewSchemaFilter(".*", "^patient_.*")
	if err != nil {
		t.Fatalf("NewSchemaFilter: %v", err)
	}
	if f.IsAllowed("patient_phi") {
		t.Error("ignore_schema_regexp must override include_schema_regexp")
	}
	if !f.IsAllowed("public") {
		t.Error("public must remain allowed")
	}
}

// Postgres system schemas are always ignored — Lynceus never inventories them.
func TestSchemaFilter_AlwaysSkipsSystemSchemas(t *testing.T) {
	f, err := collector.NewSchemaFilter("", "")
	if err != nil {
		t.Fatalf("NewSchemaFilter: %v", err)
	}
	for _, s := range []string{"pg_catalog", "information_schema", "pg_toast"} {
		if f.IsAllowed(s) {
			t.Errorf("system schema %q must be rejected unconditionally", s)
		}
	}
}

func TestSchemaFilter_InvalidRegexpErrors(t *testing.T) {
	if _, err := collector.NewSchemaFilter("[", ""); err == nil {
		t.Error("expected error for invalid include regexp")
	}
	if _, err := collector.NewSchemaFilter("", "["); err == nil {
		t.Error("expected error for invalid ignore regexp")
	}
}
```

- [ ] **Step 2: Run the tests and verify they fail.**

```
go test ./internal/collector/... -run TestSchemaFilter
```

Expected: FAIL — `collector.NewSchemaFilter` does not exist.

- [ ] **Step 3: Implement `inventory_filter.go`.**

Create `internal/collector/inventory_filter.go`:

```go
package collector

import (
	"fmt"
	"regexp"
)

// SchemaFilter is the collector-boundary control for which Postgres
// schemas may be inventoried. Schema NAMES can be sensitive (the
// inventory is otherwise structural T1), so this filter runs before
// any SchemaObject proto value is constructed — there is no path from
// catalog row to wire that bypasses it.
//
// The SAME instance is shared by the ly-xqf.6 TableStatsReader so both
// catalog readers enforce an identical boundary; keep the API stable.
//
// Semantics:
//   * include == nil → allow any schema (default).
//   * include != nil → schema must MATCH to be considered.
//   * ignore  != nil → schema is REJECTED if it matches, even if the
//                     include regex matches. Ignore wins.
//   * pg_catalog, information_schema, pg_toast, and any schema whose
//     name starts with "pg_temp_" / "pg_toast_temp_" are always rejected.
//
// Both regexps are Go RE2 (`regexp` package), evaluated anchorless —
// callers wishing to anchor must use ^ and $ explicitly. This matches
// how operators expect to write `ignore_schema_regexp` rules.
type SchemaFilter struct {
	include *regexp.Regexp
	ignore  *regexp.Regexp
}

// NewSchemaFilter compiles the two optional regexps. An empty string
// for either disables that side of the filter.
func NewSchemaFilter(includeRE, ignoreRE string) (*SchemaFilter, error) {
	f := &SchemaFilter{}
	if includeRE != "" {
		re, err := regexp.Compile(includeRE)
		if err != nil {
			return nil, fmt.Errorf("compile include_schema_regexp: %w", err)
		}
		f.include = re
	}
	if ignoreRE != "" {
		re, err := regexp.Compile(ignoreRE)
		if err != nil {
			return nil, fmt.Errorf("compile ignore_schema_regexp: %w", err)
		}
		f.ignore = re
	}
	return f, nil
}

// IsAllowed reports whether schema may be inventoried.
func (f *SchemaFilter) IsAllowed(schema string) bool {
	if isSystemSchema(schema) {
		return false
	}
	if f.include != nil && !f.include.MatchString(schema) {
		return false
	}
	if f.ignore != nil && f.ignore.MatchString(schema) {
		return false
	}
	return true
}

func isSystemSchema(s string) bool {
	switch s {
	case "pg_catalog", "information_schema", "pg_toast":
		return true
	}
	// Per-session temp schemas: pg_temp_N, pg_toast_temp_N.
	if len(s) >= 8 && s[:8] == "pg_temp_" {
		return true
	}
	if len(s) >= 14 && s[:14] == "pg_toast_temp_" {
		return true
	}
	return false
}
```

- [ ] **Step 4: Run the tests and verify they pass.**

```
go test ./internal/collector/... -run TestSchemaFilter
```

Expected: PASS.

- [ ] **Step 5: Commit.**

```
git add internal/collector/inventory_filter.go internal/collector/inventory_filter_test.go
git commit -m "feat(collector): SchemaFilter — collector-boundary regex include/exclude for schema names (ly-xqf.5)"
```

---

## Task 5: Inventory reader — catalogs → filtered T1 `SchemaObject`s

**Files:**
- Create: `internal/collector/inventory.go`
- Create: `internal/collector/inventory_test.go`

This is the new analog of `internal/collector/reader.go`. It walks `pg_namespace`, `pg_class`, `pg_index`, `pg_proc`, computes sizes, applies the `SchemaFilter`, and returns `[]*lynceusv1.SchemaObject`. First-seen lookup is delegated to a small interface so the reader is unit-testable without the stats DB; in production it is wired to `store.SchemaObjects`.

- [ ] **Step 1: Write the failing integration test.** It must (a) seed multiple schemas + tables + indexes + a view + a function + a sequence, (b) prove the reader returns them with `size_bytes > 0` for tables, (c) prove that a schema matching `ignore_schema_regexp` produces NO objects of any kind.

Create `internal/collector/inventory_test.go`:

```go
// Integration test for the schema-object inventory reader. Spins up a
// real Postgres, creates two schemas (one sensitive), seeds objects in
// both, then asserts:
//
//  1. Inventory returns objects from the allowed schema with sizes.
//  2. The ignore_schema_regexp filter PREVENTS objects in the excluded
//     schema from appearing in the reader output at all — across every
//     ObjectKind. This is the privacy guarantee.
package collector_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/dobbo-ca/lynceus/internal/collector"
	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
)

// stubFirstSeen is a no-op FirstSeenLookup — returns zero, so the
// reader stamps now() onto outgoing objects. Real wiring uses
// store.SchemaObjects (covered by Task 3's tests).
type stubFirstSeen struct{}

func (stubFirstSeen) FirstSeenAt(_ context.Context, _ string, _ lynceusv1.ObjectKind, _ string) (time.Time, error) {
	return time.Time{}, nil
}

func TestInventory_ReturnsObjectsWithSizes(t *testing.T) {
	ctx := context.Background()

	c, err := tcpostgres.Run(ctx,
		"postgres:16",
		tcpostgres.WithDatabase("lynceus_target"),
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

	for _, stmt := range []string{
		`CREATE SCHEMA reporting`,
		`CREATE SCHEMA patient_phi`,
		`CREATE TABLE reporting.orders (id INT PRIMARY KEY, total NUMERIC)`,
		`CREATE INDEX orders_total_idx ON reporting.orders (total)`,
		`CREATE VIEW reporting.recent_orders AS SELECT id FROM reporting.orders`,
		`CREATE FUNCTION reporting.add_one(x INT) RETURNS INT
		   LANGUAGE sql IMMUTABLE AS $$ SELECT x + 1 $$`,
		`CREATE SEQUENCE reporting.order_seq`,
		// Insert rows so pg_total_relation_size returns > 0.
		`INSERT INTO reporting.orders
		   SELECT g, g::numeric FROM generate_series(1, 1000) g`,
		`ANALYZE reporting.orders`,

		// Sensitive schema — must be excluded entirely by the filter.
		`CREATE TABLE patient_phi.records (id INT PRIMARY KEY, name TEXT)`,
		`CREATE INDEX records_name_idx ON patient_phi.records (name)`,
		`INSERT INTO patient_phi.records VALUES (1, 'do-not-leak-this-name')`,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("setup %q: %v", stmt, err)
		}
	}

	filter, err := collector.NewSchemaFilter("", "^patient_.*")
	if err != nil {
		t.Fatal(err)
	}
	inv := collector.NewInventory(pool, filter, stubFirstSeen{})

	objs, err := inv.Read(ctx, "srv-1")
	if err != nil {
		t.Fatalf("inventory: %v", err)
	}
	if len(objs) == 0 {
		t.Fatal("inventory returned no objects")
	}

	// Index by FQN for assertions.
	byFQN := map[string]*lynceusv1.SchemaObject{}
	for _, o := range objs {
		byFQN[o.Fqn] = o
	}

	// (1) The allowed schema yielded at least one object of each expected kind.
	wantKinds := map[lynceusv1.ObjectKind]string{
		lynceusv1.ObjectKind_OBJECT_KIND_SCHEMA:   "reporting.",
		lynceusv1.ObjectKind_OBJECT_KIND_TABLE:    "reporting.orders",
		lynceusv1.ObjectKind_OBJECT_KIND_INDEX:    "reporting.orders_total_idx",
		lynceusv1.ObjectKind_OBJECT_KIND_VIEW:     "reporting.recent_orders",
		lynceusv1.ObjectKind_OBJECT_KIND_FUNCTION: "reporting.add_one",
		lynceusv1.ObjectKind_OBJECT_KIND_SEQUENCE: "reporting.order_seq",
	}
	for kind, fqn := range wantKinds {
		got, ok := byFQN[fqn]
		if !ok {
			t.Errorf("missing expected object %q (kind=%v)", fqn, kind)
			continue
		}
		if got.Kind != kind {
			t.Errorf("object %q kind = %v, want %v", fqn, got.Kind, kind)
		}
	}

	// (2) Sizes — the table and its index must be > 0; functions/views/sequences = 0.
	if t1 := byFQN["reporting.orders"]; t1 == nil || t1.SizeBytes <= 0 {
		t.Errorf("reporting.orders size_bytes must be > 0, got %v", t1)
	}
	if ix := byFQN["reporting.orders_total_idx"]; ix == nil || ix.SizeBytes <= 0 {
		t.Errorf("reporting.orders_total_idx size_bytes must be > 0, got %v", ix)
	}
	if v := byFQN["reporting.recent_orders"]; v == nil || v.SizeBytes != 0 {
		t.Errorf("view size_bytes must be 0, got %v", v)
	}

	// (3) THE PRIVACY GUARANTEE: no object from patient_phi may appear.
	for _, o := range objs {
		if strings.HasPrefix(o.Schema, "patient_") {
			t.Errorf("LEAK: filtered schema %q surfaced object %q", o.Schema, o.Fqn)
		}
		if strings.Contains(o.Fqn, "patient_") {
			t.Errorf("LEAK: filtered schema appears in fqn %q", o.Fqn)
		}
		if o.Name == "records" || o.Name == "records_name_idx" {
			t.Errorf("LEAK: filtered object %q surfaced", o.Name)
		}
		// First-seen must be set (stub returns zero → reader fills with now()).
		if o.FirstSeenAtUnix == 0 {
			t.Errorf("first_seen_at_unix not set on %q", o.Fqn)
		}
	}
}
```

- [ ] **Step 2: Run the test and verify it fails.**

```
go test ./internal/collector/... -run TestInventory_ReturnsObjectsWithSizes
```

Expected: FAIL — `collector.NewInventory` does not exist (build error).

- [ ] **Step 3: Implement `inventory.go`.**

Create `internal/collector/inventory.go`:

```go
// Schema-object inventory reader.
//
// Walks the monitored Postgres system catalogs and emits one
// SchemaObject per allowed namespace/relation/function/sequence.
// The SchemaFilter runs at the boundary — schemas it rejects produce
// no objects of any kind. This is the privacy mechanism for schema
// NAMES (the inventory is otherwise structural T1).
//
// First-seen timestamps are sourced from the stats DB via a small
// interface so the reader is unit-testable without that dependency.
package collector

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
)

// FirstSeenLookup returns the persisted first_seen_at for a given
// object, or the zero Time if it has never been recorded. The
// production implementation is store.SchemaObjects.FirstSeenAt — wired
// in cmd/collector/main.go with a small adapter that converts
// lynceusv1.ObjectKind ↔ int16.
type FirstSeenLookup interface {
	FirstSeenAt(ctx context.Context, serverID string, kind lynceusv1.ObjectKind, fqn string) (time.Time, error)
}

// Inventory reads the structural catalog of a monitored Postgres
// instance and returns it as a slice of T1 SchemaObject messages.
type Inventory struct {
	pool      *pgxpool.Pool
	filter    *SchemaFilter
	firstSeen FirstSeenLookup
}

// NewInventory binds an Inventory reader. filter must be non-nil — a
// permissive filter is created via NewSchemaFilter("", "").
func NewInventory(pool *pgxpool.Pool, filter *SchemaFilter, firstSeen FirstSeenLookup) *Inventory {
	return &Inventory{pool: pool, filter: filter, firstSeen: firstSeen}
}

// Read returns every allowed schema, table, index, view, function,
// and sequence on the monitored database. Rejected schemas produce
// zero objects of any kind. Errors from any single sub-query abort
// the whole read; partial results are not returned.
func (i *Inventory) Read(ctx context.Context, serverID string) ([]*lynceusv1.SchemaObject, error) {
	var out []*lynceusv1.SchemaObject

	// 1. Schemas.
	rows, err := i.pool.Query(ctx,
		`SELECT nspname FROM pg_namespace ORDER BY nspname`,
	)
	if err != nil {
		return nil, fmt.Errorf("query pg_namespace: %w", err)
	}
	for rows.Next() {
		var schema string
		if err := rows.Scan(&schema); err != nil {
			rows.Close()
			return nil, err
		}
		if !i.filter.IsAllowed(schema) {
			continue
		}
		out = append(out, i.build(ctx, serverID,
			lynceusv1.ObjectKind_OBJECT_KIND_SCHEMA, schema, "", 0, false, ""))
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	// 2. Tables (including partitioned tables and materialized views) +
	//    sizes via pg_total_relation_size. relkind: r=table, p=partitioned,
	//    m=materialized view, f=foreign. f is included as a "table-like"
	//    inventory entry but size will be 0.
	rows, err = i.pool.Query(ctx,
		`SELECT n.nspname, c.relname,
		        COALESCE(pg_total_relation_size(c.oid), 0)::bigint AS sz,
		        c.relispartition,
		        COALESCE(pn.nspname || '.' || pc.relname, '') AS parent_fqn
		   FROM pg_class c
		   JOIN pg_namespace n ON n.oid = c.relnamespace
		   LEFT JOIN pg_inherits i ON i.inhrelid = c.oid
		   LEFT JOIN pg_class    pc ON pc.oid = i.inhparent
		   LEFT JOIN pg_namespace pn ON pn.oid = pc.relnamespace
		  WHERE c.relkind IN ('r','p','m','f')
		  ORDER BY n.nspname, c.relname`,
	)
	if err != nil {
		return nil, fmt.Errorf("query pg_class tables: %w", err)
	}
	for rows.Next() {
		var (
			schema, name string
			sz           int64
			isPart       bool
			parentFQN    string
		)
		if err := rows.Scan(&schema, &name, &sz, &isPart, &parentFQN); err != nil {
			rows.Close()
			return nil, err
		}
		if !i.filter.IsAllowed(schema) {
			continue
		}
		// If the parent lives in a filtered schema, blank it out — we
		// must not leak a filtered name through the parent_fqn field.
		if parentFQN != "" {
			parentSchema := parentFQN
			for j := 0; j < len(parentFQN); j++ {
				if parentFQN[j] == '.' {
					parentSchema = parentFQN[:j]
					break
				}
			}
			if !i.filter.IsAllowed(parentSchema) {
				parentFQN = ""
			}
		}
		out = append(out, i.build(ctx, serverID,
			lynceusv1.ObjectKind_OBJECT_KIND_TABLE, schema, name, sz, isPart, parentFQN))
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	// 3. Indexes (relkind='i') with pg_relation_size.
	rows, err = i.pool.Query(ctx,
		`SELECT n.nspname, c.relname,
		        COALESCE(pg_relation_size(c.oid), 0)::bigint AS sz
		   FROM pg_class c
		   JOIN pg_namespace n ON n.oid = c.relnamespace
		  WHERE c.relkind = 'i'
		  ORDER BY n.nspname, c.relname`,
	)
	if err != nil {
		return nil, fmt.Errorf("query pg_class indexes: %w", err)
	}
	for rows.Next() {
		var (
			schema, name string
			sz           int64
		)
		if err := rows.Scan(&schema, &name, &sz); err != nil {
			rows.Close()
			return nil, err
		}
		if !i.filter.IsAllowed(schema) {
			continue
		}
		out = append(out, i.build(ctx, serverID,
			lynceusv1.ObjectKind_OBJECT_KIND_INDEX, schema, name, sz, false, ""))
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	// 4. Views (relkind='v'). Size meaningless → 0.
	rows, err = i.pool.Query(ctx,
		`SELECT n.nspname, c.relname
		   FROM pg_class c
		   JOIN pg_namespace n ON n.oid = c.relnamespace
		  WHERE c.relkind = 'v'
		  ORDER BY n.nspname, c.relname`,
	)
	if err != nil {
		return nil, fmt.Errorf("query pg_class views: %w", err)
	}
	for rows.Next() {
		var schema, name string
		if err := rows.Scan(&schema, &name); err != nil {
			rows.Close()
			return nil, err
		}
		if !i.filter.IsAllowed(schema) {
			continue
		}
		out = append(out, i.build(ctx, serverID,
			lynceusv1.ObjectKind_OBJECT_KIND_VIEW, schema, name, 0, false, ""))
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	// 5. Functions (pg_proc).
	rows, err = i.pool.Query(ctx,
		`SELECT n.nspname, p.proname
		   FROM pg_proc p
		   JOIN pg_namespace n ON n.oid = p.pronamespace
		  ORDER BY n.nspname, p.proname`,
	)
	if err != nil {
		return nil, fmt.Errorf("query pg_proc: %w", err)
	}
	for rows.Next() {
		var schema, name string
		if err := rows.Scan(&schema, &name); err != nil {
			rows.Close()
			return nil, err
		}
		if !i.filter.IsAllowed(schema) {
			continue
		}
		out = append(out, i.build(ctx, serverID,
			lynceusv1.ObjectKind_OBJECT_KIND_FUNCTION, schema, name, 0, false, ""))
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	// 6. Sequences (relkind='S').
	rows, err = i.pool.Query(ctx,
		`SELECT n.nspname, c.relname
		   FROM pg_class c
		   JOIN pg_namespace n ON n.oid = c.relnamespace
		  WHERE c.relkind = 'S'
		  ORDER BY n.nspname, c.relname`,
	)
	if err != nil {
		return nil, fmt.Errorf("query pg_class sequences: %w", err)
	}
	for rows.Next() {
		var schema, name string
		if err := rows.Scan(&schema, &name); err != nil {
			rows.Close()
			return nil, err
		}
		if !i.filter.IsAllowed(schema) {
			continue
		}
		out = append(out, i.build(ctx, serverID,
			lynceusv1.ObjectKind_OBJECT_KIND_SEQUENCE, schema, name, 0, false, ""))
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	return out, nil
}

// build constructs a SchemaObject, stamping first_seen_at from the
// FirstSeenLookup (falling back to now() when the lookup has no
// record — the upsert path will then persist that timestamp).
func (i *Inventory) build(
	ctx context.Context,
	serverID string,
	kind lynceusv1.ObjectKind,
	schema, name string,
	sizeBytes int64,
	isPartition bool,
	parentFQN string,
) *lynceusv1.SchemaObject {
	fqn := schema + "." + name // for kind=SCHEMA, name is "" → "schema."

	firstSeen := time.Time{}
	if i.firstSeen != nil {
		// Best-effort: a lookup error must not block the inventory.
		// The collector caller can re-stamp on the next snapshot.
		if t, err := i.firstSeen.FirstSeenAt(ctx, serverID, kind, fqn); err == nil {
			firstSeen = t
		}
	}
	if firstSeen.IsZero() {
		firstSeen = time.Now().UTC()
	}

	return &lynceusv1.SchemaObject{
		Kind:            kind,
		Schema:          schema,
		Name:            name,
		Fqn:             fqn,
		SizeBytes:       sizeBytes,
		IsPartition:     isPartition,
		ParentFqn:       parentFQN,
		FirstSeenAtUnix: firstSeen.Unix(),
	}
}
```

- [ ] **Step 4: Run the test and verify it passes.**

```
go test ./internal/collector/... -run TestInventory_ReturnsObjectsWithSizes
```

Expected: PASS.

- [ ] **Step 5: Run the full collector + store + proto suites to confirm no regression.**

```
go test ./internal/collector/... ./internal/store/... ./internal/proto/...
```

Expected: PASS for all packages.

- [ ] **Step 6: Commit.**

```
git add internal/collector/inventory.go internal/collector/inventory_test.go
git commit -m "feat(collector): schema/object inventory reader with size + first-seen + boundary filter (ly-xqf.5)"
```

---

## Task 6: Wire the inventory into the collector binary

**Files:**
- Modify: `cmd/collector/main.go`

The reader and the persistence are now in place; this task connects them so an end-to-end run produces a `Snapshot` carrying both `query_stats` and `schema_objects`, and upserts the inventory into the stats DB (which is also where first-seen is sourced from on the next cycle).

> **Shared-filter note (ly-xqf.6).** The single `SchemaFilter` built here from `LYNCEUS_INCLUDE_SCHEMA_REGEXP` / `LYNCEUS_IGNORE_SCHEMA_REGEXP` is the same instance `ly-xqf.6` will pass to `NewTableStatsReader`. Build it once, fail-fast on a bad regex, and reuse it — do not construct a second filter for the table-stats reader.

- [ ] **Step 1: Read the current `cmd/collector/main.go`** to identify the existing snapshot construction site (the `runFull` cadence around `main.go:36-52` where query stats already ship).

```
cat cmd/collector/main.go
```

- [ ] **Step 2: Modify `cmd/collector/main.go` to:**
  1. Read `LYNCEUS_INCLUDE_SCHEMA_REGEXP` and `LYNCEUS_IGNORE_SCHEMA_REGEXP` from the environment.
  2. Build a single `SchemaFilter` from those values (fail-fast if either fails to compile — bad regex must NOT silently disable filtering). This same instance is the one `ly-xqf.6` reuses for `NewTableStatsReader`.
  3. Construct a `store.SchemaObjects` against the stats DB pool (the binary already holds the pgx pool for shipping; if it doesn't, add one — see existing pattern in `cmd/ingestion/main.go`).
  4. Wrap that with a small adapter implementing `collector.FirstSeenLookup`:

```go
type firstSeenAdapter struct{ so *store.SchemaObjects }

func (a firstSeenAdapter) FirstSeenAt(
	ctx context.Context, serverID string, kind lynceusv1.ObjectKind, fqn string,
) (time.Time, error) {
	return a.so.FirstSeenAt(ctx, serverID, int16(kind), fqn)
}
```

  5. Construct an `Inventory` and call its `Read` on the same `runFull` cadence the existing reader runs on; attach the result to the outgoing `Snapshot.SchemaObjects`.
  6. After a snapshot is constructed (and before/after `Send` — best effort, log on error), upsert the inventory into the stats DB so first-seen is persisted:

```go
upserts := make([]store.SchemaObjectRow, 0, len(snap.SchemaObjects))
for _, o := range snap.SchemaObjects {
	upserts = append(upserts, store.SchemaObjectRow{
		ServerID:    snap.ServerId,
		Kind:        int16(o.Kind),
		FQN:         o.Fqn,
		SchemaName:  o.Schema,
		ObjectName:  o.Name,
		SizeBytes:   o.SizeBytes,
		IsPartition: o.IsPartition,
		ParentFQN:   o.ParentFqn,
	})
}
if err := schemaObjects.UpsertSchemaObjects(ctx, upserts); err != nil {
	log.Printf("schema_objects upsert: %v", err)
}
```

- [ ] **Step 3: Build and verify.**

```
go build ./...
```

Expected: PASS.

- [ ] **Step 4: Re-run the full test suite to ensure no regression.**

```
go test ./...
```

Expected: PASS.

- [ ] **Step 5: Commit.**

```
git add cmd/collector/main.go
git commit -m "feat(collector): wire schema inventory + first-seen persistence into snapshot loop (ly-xqf.5)"
```

---

## Self-Review

### Spec coverage

| Spec requirement | Plan task |
|---|---|
| Walk system catalogs for schemas, tables, indexes, views, functions, sequences | Task 5 (six separate sub-queries against `pg_namespace`, `pg_class` × {r/p/m/f, i, v, S}, `pg_proc`) |
| Sizes via `pg_total_relation_size` / `pg_relation_size` | Task 5 (tables: `pg_total_relation_size`; indexes: `pg_relation_size`; views/functions/sequences: 0) |
| Stable first-seen per object | Task 2 (`schema_objects` table PK on `(server_id, kind, fqn)`, `first_seen_at` never overwritten on conflict), Task 3 (`UpsertSchemaObjects` test proves it), Task 5 (`build` stamps `first_seen_at_unix` from lookup) |
| Privacy carve-out: `ignore_schema_regexp`-style filter at the collector | Task 4 (`SchemaFilter` with include + ignore, ignore-wins semantics, system schemas always blocked), Task 5 (filter applied inside every loop before `out` is appended; partition `parent_fqn` blanked if parent's schema is filtered) |
| T1 proto contract — no DATA-bearing fields on `SchemaObject` | Task 1 (contract test with allowlist + kind/type assertions; allowlist explicitly excludes columns, defaults, ACLs, constraints, comments) |
| Snapshot field-number reservation (`schema_objects=6`) | Task 1 (claims field 6 per the Layer-0 reservation; 7=`table_stats`/ly-xqf.6, 8=`log_events`/ly-cxe.2 reserved and not reused) |
| Real Postgres in tests | Task 3 (`testcontainers postgres:16`), Task 5 (`testcontainers postgres:16` with multi-schema seed + ANALYZE) |
| Regexp filter exclusion proved in tests | Task 4 (unit tests for filter semantics), Task 5 (integration test seeds `patient_phi` schema with table + index + row, asserts NO object surfaces) |
| Migration goes in `internal/store/migrations/stats/` (next free number) | Task 2 (`0005_schema_objects.sql` — `0003`/`0004` taken on disk; runner sorts lexically, `migrate.go:48`) |
| Vanilla Postgres, no extensions | Task 2 (no `CREATE EXTENSION`, only standard partial index and standard column types — runs on RDS / Aurora / Cloud SQL) |

### Integration points called out (not implemented here)

- **`ly-xqf.6` Table size/growth + TOAST** — adds a SIBLING `0006_table_stats.sql` weekly range-partitioned, append-only growth time series; **reuses this plan's `SchemaFilter` instance** (one filter built in `cmd/collector/main.go`, passed to both `NewInventory` and `NewTableStatsReader`). `schema_objects` (upsert, first-seen) and `table_stats` (append-only growth) coexist deliberately. Claims `Snapshot` field 7.
- **`ly-cxe.2` Log pipeline** — claims `Snapshot` field 8 (`log_events`); independent bead, lands in any order relative to this one.
- **M3 Index Advisor (`ly-u4t.12`)** — joins on `(server_id, kind=INDEX, fqn)` to attribute usage; can read `first_seen_at` to recommend "unused since first seen N days ago".
- **`ly-xqf.14` Partition breakdown** — already gets `is_partition` + `parent_fqn` on the wire; can group children under parents without a second catalog walk.
- **`ly-xqf.13` HOT update tracking** — joins on `(server_id, kind=TABLE, fqn)`.
- **`ly-xqf.8` Column statistics** — separate T1 message type (column-level stats only, no MCV/histogram bounds); will reference `schema_objects.fqn` as its foreign key.
- **`ly-xqf.9` Buffer-cache statistics** — joins on the same `(server_id, kind, fqn)` triple.

### Placeholder scan

- No "TBD" / "implement later" markers.
- No "add appropriate error handling" without showing exactly how (Task 5 shows every `rows.Err()` / `rows.Close()` and the parent-filter blanking).
- Every code block is complete and runnable as-is — no `...` stubs in the implementations.
- Every referenced symbol is defined in a previous task (`SchemaFilter` → Task 4; `FirstSeenLookup` → Task 5; `SchemaObjectRow`/`UpsertSchemaObjects` → Task 3; `SchemaObject` proto → Task 1).

### Type consistency

- Proto `ObjectKind` numeric values (1..6) match `int16` values stored in `schema_objects.kind` — `int16(kind)` cast appears at every boundary (Task 6 adapter; Task 3 test uses `int16(2)` for `OBJECT_KIND_TABLE`).
- `FirstSeenLookup` interface takes `lynceusv1.ObjectKind` (Task 5); production adapter (Task 6) converts to `int16` before hitting `store.SchemaObjects.FirstSeenAt`. The adapter exists precisely so the reader never depends on the store package.
- `SchemaObjectRow.Kind` is `int16` in Task 3; the collector adapter in Task 6 sets it via `int16(o.Kind)` from the proto enum — consistent across all call sites.
- FQN format is `"schema.name"` everywhere (Task 5 `build` constructs it; Task 1 contract test allows `parent_fqn` field of the same shape; Task 2 migration treats it as opaque `TEXT`). For `kind=SCHEMA`, the trailing-dot form `"schema."` is documented in both the migration comment and the contract test allowlist comment.

### Migration numbering audit

- On-disk stats migrations at plan time: `0001_init.sql`, `0002_dlq.sql`, `0003_activity_buckets.sql`, `0004_query_plans.sql`. The next free lexical number is **`0005`** → `0005_schema_objects.sql` (Task 2). The runner sorts with `sort.Strings(files)` (`internal/store/migrate.go:48`), so `0005` applies after `0004_query_plans.sql` — correct order, no collision. `ly-xqf.6`'s sibling table follows as `0006_table_stats.sql` (out of scope here; noted so the two plans don't collide on a number).
