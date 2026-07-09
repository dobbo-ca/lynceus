// Integration tests for the store package. They spin up a real
// PostgreSQL via testcontainers — we never mock the database, because
// the schema (partitioning, audit columns) is part of what we're
// validating.
package store_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/dobbo-ca/lynceus/internal/testpg"
)

// newPool starts a fresh postgres:16 container and returns a connected
// pool. Cleanup terminates the container.
func newPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	c, err := tcpostgres.Run(ctx,
		"postgres:16",
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
	return pool
}

func TestApplyConfigMigrations_createsAuditWithDataTier(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// audit_log table exists with data_tier column (privacy invariant).
	var hasCol bool
	err := pool.QueryRow(ctx,
		`SELECT EXISTS(
		   SELECT 1 FROM information_schema.columns
		   WHERE table_name = 'audit_log' AND column_name = 'data_tier'
		 )`,
	).Scan(&hasCol)
	if err != nil || !hasCol {
		t.Fatalf("audit_log.data_tier missing: hasCol=%v err=%v", hasCol, err)
	}

	// idempotency: re-running is a no-op (no error, no duplicate).
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("re-apply: %v", err)
	}
}

func TestApplyStatsMigrations_createsPartitionedQueryStats(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyStatsMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// query_stats is RANGE-partitioned (privacy/scalability invariant).
	var strategy string
	err := pool.QueryRow(ctx,
		`SELECT partstrat::text FROM pg_partitioned_table
		 WHERE partrelid = 'query_stats'::regclass`,
	).Scan(&strategy)
	if err != nil {
		t.Fatalf("query_stats not partitioned: %v", err)
	}
	if strategy != "r" {
		t.Fatalf("partition strategy = %q, want 'r' (range)", strategy)
	}

	// data_tier column exists.
	var hasCol bool
	_ = pool.QueryRow(ctx,
		`SELECT EXISTS(
		   SELECT 1 FROM information_schema.columns
		   WHERE table_name = 'query_stats' AND column_name = 'data_tier'
		 )`,
	).Scan(&hasCol)
	if !hasCol {
		t.Fatal("query_stats.data_tier missing")
	}
}

func TestAuditAppend_roundtrips(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg := store.NewConfig(pool)

	err := cfg.AppendAudit(ctx, store.AuditEntry{
		Actor:    "alice",
		Action:   "viewed.t2",
		ServerID: "srv-1",
		DataTier: 2,
		Detail:   map[string]any{"fingerprint": "abc123"},
	})
	if err != nil {
		t.Fatalf("append: %v", err)
	}

	var actor, action string
	var tier int16
	if err := pool.QueryRow(ctx,
		`SELECT actor, action, data_tier FROM audit_log WHERE server_id = $1`,
		"srv-1",
	).Scan(&actor, &action, &tier); err != nil {
		t.Fatalf("select: %v", err)
	}
	if actor != "alice" || action != "viewed.t2" || tier != 2 {
		t.Errorf("got (%q, %q, %d), want (alice, viewed.t2, 2)", actor, action, tier)
	}
}

func TestApplyConfigMigrations_addsChainColumnsAndTrigger(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// prev_hash + row_hash exist on audit_log.
	for _, col := range []string{"prev_hash", "row_hash"} {
		var ok bool
		_ = pool.QueryRow(ctx,
			`SELECT EXISTS(
			   SELECT 1 FROM information_schema.columns
			   WHERE table_name='audit_log' AND column_name=$1
			 )`, col,
		).Scan(&ok)
		if !ok {
			t.Errorf("audit_log.%s missing", col)
		}
	}

	// row_hash is uniquely constrained.
	var hasUnique bool
	_ = pool.QueryRow(ctx,
		`SELECT EXISTS(
		   SELECT 1 FROM pg_indexes
		   WHERE tablename='audit_log' AND indexdef ILIKE '%UNIQUE%(row_hash)%'
		 )`,
	).Scan(&hasUnique)
	if !hasUnique {
		t.Error("expected UNIQUE index on audit_log(row_hash)")
	}

	// Append-only trigger rejects UPDATE and DELETE.
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("idempotent re-apply: %v", err)
	}
	cfg := store.NewConfig(pool)
	if err := cfg.AppendAudit(ctx, store.AuditEntry{Actor: "a", Action: "x"}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE audit_log SET actor='mallory'`); err == nil {
		t.Error("UPDATE on audit_log should be rejected by trigger")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM audit_log`); err == nil {
		t.Error("DELETE on audit_log should be rejected by trigger")
	}
}

func TestAppendAudit_populatesChainColumns(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg := store.NewConfig(pool)

	rec, err := cfg.AppendAuditReturning(ctx, store.AuditEntry{
		Actor: "alice", Action: "login", Detail: map[string]any{"ip": "127.0.0.1"},
	})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if rec.ID == 0 {
		t.Fatal("ID not returned")
	}
	if len(rec.PrevHash) != 32 || len(rec.RowHash) != 32 {
		t.Fatalf("hash length: prev=%d row=%d", len(rec.PrevHash), len(rec.RowHash))
	}
	// First row's prev is genesis (all zero).
	for _, b := range rec.PrevHash {
		if b != 0 {
			t.Fatalf("first row's prev_hash must be zero, got %x", rec.PrevHash)
		}
	}

	// Second append chains onto the first.
	rec2, err := cfg.AppendAuditReturning(ctx, store.AuditEntry{Actor: "bob", Action: "login"})
	if err != nil {
		t.Fatalf("append 2: %v", err)
	}
	if !bytes.Equal(rec2.PrevHash, rec.RowHash) {
		t.Fatalf("rec2.prev_hash %x != rec.row_hash %x", rec2.PrevHash, rec.RowHash)
	}
}

func TestWriteQueryStats_createsPartitionAndRoundtripsTopQueries(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyStatsMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := store.NewStats(pool)

	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC) // a Wednesday
	rows := []store.QueryStat{
		{
			ServerID: "srv-1", CollectedAt: now,
			Fingerprint: "fp-fast", NormalizedQuery: "SELECT 1",
			Calls: 100, TotalTimeMs: 10.0, MeanTimeMs: 0.1,
		},
		{
			ServerID: "srv-1", CollectedAt: now,
			Fingerprint: "fp-slow", NormalizedQuery: "SELECT * FROM big WHERE x = $1",
			Calls: 5, TotalTimeMs: 500.0, MeanTimeMs: 100.0,
		},
	}
	if err := s.WriteQueryStats(ctx, rows); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Partition for that week was created automatically.
	var partCount int
	_ = pool.QueryRow(ctx,
		`SELECT count(*) FROM pg_inherits WHERE inhparent = 'query_stats'::regclass`,
	).Scan(&partCount)
	if partCount == 0 {
		t.Fatal("write did not create a weekly partition")
	}

	// Top queries returns slow first.
	top, err := s.TopQueriesByTotalTime(ctx, now.Add(-time.Hour), now.Add(time.Hour), 10)
	if err != nil {
		t.Fatalf("top: %v", err)
	}
	if len(top) != 2 {
		t.Fatalf("got %d rows, want 2", len(top))
	}
	if top[0].Fingerprint != "fp-slow" || top[0].TotalTimeMs != 500.0 {
		t.Errorf("top[0] = %+v, want fp-slow / 500.0", top[0])
	}

	// EnsureWeeklyPartition is idempotent.
	if err := s.EnsureWeeklyPartition(ctx, now); err != nil {
		t.Fatalf("ensure idempotent: %v", err)
	}
}

func TestWriteQueryStats_defaultsTierAndRoutesMultiWeek(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyStatsMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := store.NewStats(pool)

	wk1 := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC) // ISO week A
	wk2 := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)  // ISO week B
	rows := []store.QueryStat{
		// DataTier left 0 — must be stored as 1 (T1 default).
		{ServerID: "srv-1", CollectedAt: wk1, Fingerprint: "fp-a", NormalizedQuery: "SELECT 1", Calls: 1, TotalTimeMs: 1},
		{ServerID: "srv-1", CollectedAt: wk2, Fingerprint: "fp-b", NormalizedQuery: "SELECT 2", Calls: 1, TotalTimeMs: 1},
	}
	if err := s.WriteQueryStats(ctx, rows); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Both weekly partitions were created and rows routed across them.
	var partCount, rowCount int
	_ = pool.QueryRow(ctx,
		`SELECT count(*) FROM pg_inherits WHERE inhparent = 'query_stats'::regclass`,
	).Scan(&partCount)
	if partCount != 2 {
		t.Fatalf("partitions = %d, want 2", partCount)
	}
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM query_stats`).Scan(&rowCount)
	if rowCount != 2 {
		t.Fatalf("rows = %d, want 2", rowCount)
	}

	// DataTier 0 was defaulted to 1 on insert.
	var tier int16
	if err := pool.QueryRow(ctx,
		`SELECT data_tier FROM query_stats WHERE fingerprint = $1`, "fp-a",
	).Scan(&tier); err != nil {
		t.Fatalf("select tier: %v", err)
	}
	if tier != 1 {
		t.Errorf("data_tier = %d, want 1 (defaulted)", tier)
	}
}

func TestApplyStatsMigrations_createsPartitionedActivityBuckets(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyStatsMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var strategy string
	err := pool.QueryRow(ctx,
		`SELECT partstrat::text FROM pg_partitioned_table
		 WHERE partrelid = 'activity_buckets'::regclass`,
	).Scan(&strategy)
	if err != nil {
		t.Fatalf("activity_buckets not partitioned: %v", err)
	}
	if strategy != "r" {
		t.Fatalf("partition strategy = %q, want 'r' (range)", strategy)
	}
}

func TestWriteActivityBuckets_createsPartitionAndRoundtrips(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyStatsMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := store.NewStats(pool)

	now := time.Date(2026, 5, 27, 12, 34, 0, 0, time.UTC) // a Wednesday
	rows := []store.ActivityBucket{
		{
			ServerID: "srv-1", Database: "app", State: "active",
			BucketStart: now, BucketSeconds: 60,
			SampleCount: 6, CountSum: 30, CountMax: 7,
		},
		{
			ServerID: "srv-1", Database: "app", State: "idle",
			BucketStart: now, BucketSeconds: 60,
			SampleCount: 6, CountSum: 42, CountMax: 9,
		},
		{
			ServerID: "srv-1", Database: "app", State: "active",
			WaitEventType: "IO", WaitEvent: "DataFileRead",
			BucketStart: now, BucketSeconds: 60,
			SampleCount: 4, CountSum: 8, CountMax: 3,
		},
	}
	if err := s.WriteActivityBuckets(ctx, rows); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Partition was created.
	var partCount int
	_ = pool.QueryRow(ctx,
		`SELECT count(*) FROM pg_inherits WHERE inhparent = 'activity_buckets'::regclass`,
	).Scan(&partCount)
	if partCount == 0 {
		t.Fatal("write did not create a weekly partition")
	}

	// Round-trip read by state.
	out, err := s.TopActivityBucketsByState(ctx,
		"srv-1", now.Add(-time.Hour), now.Add(time.Hour), 10)
	if err != nil {
		t.Fatalf("top: %v", err)
	}
	want := map[string]int64{"active": 30 + 8, "idle": 42}
	got := map[string]int64{}
	for _, b := range out {
		got[b.State] += b.CountSum
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("state %q sum = %d, want %d", k, got[k], v)
		}
	}
}

func TestWaitEventHistogram_aggregatesByEvent(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyStatsMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := store.NewStats(pool)

	base := time.Date(2024, 1, 8, 0, 0, 0, 0, time.UTC) // a Monday
	rows := []store.ActivityBucket{
		{ServerID: "s1", Database: "db", State: "active", WaitEventType: "IO", WaitEvent: "DataFileRead",
			BucketStart: base, BucketSeconds: 10, SampleCount: 1, CountSum: 30, CountMax: 5},
		{ServerID: "s1", Database: "db", State: "active", WaitEventType: "IO", WaitEvent: "DataFileRead",
			BucketStart: base.Add(time.Minute), BucketSeconds: 10, SampleCount: 1, CountSum: 20, CountMax: 4},
		{ServerID: "s1", Database: "db", State: "active", WaitEventType: "Lock", WaitEvent: "tuple",
			BucketStart: base, BucketSeconds: 10, SampleCount: 1, CountSum: 5, CountMax: 2},
		{ServerID: "s1", Database: "db", State: "active", WaitEventType: "", WaitEvent: "",
			BucketStart: base, BucketSeconds: 10, SampleCount: 1, CountSum: 40, CountMax: 8}, // on-CPU
	}
	if err := s.WriteActivityBuckets(ctx, rows); err != nil {
		t.Fatal(err)
	}
	got, err := s.WaitEventHistogram(ctx, "s1", base.Add(-time.Hour), base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	// Ordered by total desc: IO/DataFileRead(50) > CPU(40) > Lock(5).
	if len(got) != 3 {
		t.Fatalf("rows = %d, want 3: %+v", len(got), got)
	}
	if got[0].WaitEventType != "IO" || got[0].Total != 50 {
		t.Errorf("top = %+v, want IO/50", got[0])
	}
	// on-CPU row preserved (empty type/event), not dropped.
	var sawCPU bool
	for _, g := range got {
		if g.WaitEventType == "" && g.Total == 40 {
			sawCPU = true
		}
	}
	if !sawCPU {
		t.Errorf("on-CPU bucket dropped: %+v", got)
	}
}

func TestApplyStatsMigrations_createsSchemaObjects(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyStatsMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Table exists.
	var exists bool
	_ = pool.QueryRow(ctx,
		`SELECT EXISTS(
		   SELECT 1 FROM information_schema.tables
		   WHERE table_name = 'schema_objects'
		 )`,
	).Scan(&exists)
	if !exists {
		t.Fatal("schema_objects table missing")
	}

	// PK columns (server_id, kind, fqn) are present.
	for _, col := range []string{"server_id", "kind", "fqn"} {
		var ok bool
		_ = pool.QueryRow(ctx,
			`SELECT EXISTS(
			   SELECT 1 FROM information_schema.columns
			   WHERE table_name = 'schema_objects' AND column_name = $1
			 )`, col,
		).Scan(&ok)
		if !ok {
			t.Errorf("schema_objects.%s missing", col)
		}
	}

	// Column names match proto field names: schema, name (not schema_name/object_name).
	for _, col := range []string{"schema", "name"} {
		var ok bool
		_ = pool.QueryRow(ctx,
			`SELECT EXISTS(
			   SELECT 1 FROM information_schema.columns
			   WHERE table_name = 'schema_objects' AND column_name = $1
			 )`, col,
		).Scan(&ok)
		if !ok {
			t.Errorf("schema_objects.%s missing (must match proto SchemaObject field name)", col)
		}
	}
	for _, badCol := range []string{"schema_name", "object_name"} {
		var bad bool
		_ = pool.QueryRow(ctx,
			`SELECT EXISTS(
			   SELECT 1 FROM information_schema.columns
			   WHERE table_name = 'schema_objects' AND column_name = $1
			 )`, badCol,
		).Scan(&bad)
		if bad {
			t.Errorf("schema_objects.%s must not exist (renamed to match proto)", badCol)
		}
	}

	// first_seen_at is not overwritten on upsert (ON CONFLICT preserves it).
	// Insert a row, then upsert again with a different last_seen_at and verify
	// first_seen_at did not change.
	_, err := pool.Exec(ctx,
		`INSERT INTO schema_objects
		   (server_id, kind, fqn, schema, name, size_bytes_latest, first_seen_at, last_seen_at)
		 VALUES
		   ('srv-test', 2, 'public.widgets', 'public', 'widgets', 1024,
		    '2026-01-01 00:00:00Z', '2026-01-01 00:00:00Z')
		 ON CONFLICT (server_id, kind, fqn) DO UPDATE
		   SET size_bytes_latest = EXCLUDED.size_bytes_latest,
		       last_seen_at      = EXCLUDED.last_seen_at`,
	)
	if err != nil {
		t.Fatalf("initial insert: %v", err)
	}
	_, err = pool.Exec(ctx,
		`INSERT INTO schema_objects
		   (server_id, kind, fqn, schema, name, size_bytes_latest, first_seen_at, last_seen_at)
		 VALUES
		   ('srv-test', 2, 'public.widgets', 'public', 'widgets', 2048,
		    '2026-06-01 00:00:00Z', '2026-06-01 00:00:00Z')
		 ON CONFLICT (server_id, kind, fqn) DO UPDATE
		   SET size_bytes_latest = EXCLUDED.size_bytes_latest,
		       last_seen_at      = EXCLUDED.last_seen_at`,
	)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	var firstSeen string
	var sizeLatest int64
	if err := pool.QueryRow(ctx,
		`SELECT first_seen_at::text, size_bytes_latest FROM schema_objects
		  WHERE server_id = 'srv-test' AND kind = 2 AND fqn = 'public.widgets'`,
	).Scan(&firstSeen, &sizeLatest); err != nil {
		t.Fatalf("select: %v", err)
	}
	if !strings.HasPrefix(firstSeen, "2026-01-01") {
		t.Errorf("first_seen_at = %q, want 2026-01-01 (must not be overwritten by upsert)", firstSeen)
	}
	if sizeLatest != 2048 {
		t.Errorf("size_bytes_latest = %d, want 2048 (must be updated by upsert)", sizeLatest)
	}

	// Idempotency: re-running migrations is a no-op.
	if err := store.ApplyStatsMigrations(ctx, pool); err != nil {
		t.Fatalf("re-apply: %v", err)
	}
}

func TestDropPartitionsOlderThan_dropsOldKeepsNew(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyStatsMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := store.NewStats(pool)

	old := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC) // long ago
	new := time.Date(2026, 5, 27, 0, 0, 0, 0, time.UTC)
	if err := s.EnsureWeeklyPartition(ctx, old); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureWeeklyPartition(ctx, new); err != nil {
		t.Fatal(err)
	}

	cutoff := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	dropped, err := s.DropPartitionsOlderThan(ctx, cutoff)
	if err != nil {
		t.Fatalf("drop: %v", err)
	}
	if dropped != 1 {
		t.Errorf("dropped = %d, want 1", dropped)
	}

	var remaining int
	_ = pool.QueryRow(ctx,
		`SELECT count(*) FROM pg_inherits WHERE inhparent = 'query_stats'::regclass`,
	).Scan(&remaining)
	if remaining != 1 {
		t.Errorf("remaining partitions = %d, want 1", remaining)
	}
}

func TestWriteQueryStats_cachesEnsuredPartition(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyStatsMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := store.NewStats(pool)

	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC) // a Wednesday
	batch := []store.QueryStat{
		{ServerID: "srv-1", CollectedAt: now, Fingerprint: "fp-a", NormalizedQuery: "SELECT 1", Calls: 1, TotalTimeMs: 1},
	}
	// Two writes for the SAME ISO week via the same *pgxStats: the second
	// must hit the ensured-partition cache and skip the CREATE TABLE.
	if err := s.WriteQueryStats(ctx, batch); err != nil {
		t.Fatalf("write 1: %v", err)
	}
	if err := s.WriteQueryStats(ctx, batch); err != nil {
		t.Fatalf("write 2: %v", err)
	}

	// Exactly one weekly partition — not duplicated across the two writes.
	var partCount int
	_ = pool.QueryRow(ctx,
		`SELECT count(*) FROM pg_inherits WHERE inhparent = 'query_stats'::regclass`,
	).Scan(&partCount)
	if partCount != 1 {
		t.Fatalf("partitions = %d, want 1", partCount)
	}

	// Both writes landed: 2 rows total.
	var rowCount int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM query_stats`).Scan(&rowCount)
	if rowCount != 2 {
		t.Fatalf("rows = %d, want 2", rowCount)
	}
}

func TestDropPartitionsOlderThan_evictsCache(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyStatsMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := store.NewStats(pool)

	old := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	new := time.Date(2026, 5, 27, 0, 0, 0, 0, time.UTC)
	if err := s.EnsureWeeklyPartition(ctx, old); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureWeeklyPartition(ctx, new); err != nil {
		t.Fatal(err)
	}

	cutoff := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	dropped, err := s.DropPartitionsOlderThan(ctx, cutoff)
	if err != nil {
		t.Fatalf("drop: %v", err)
	}
	if dropped != 1 {
		t.Fatalf("dropped = %d, want 1", dropped)
	}

	var count int
	_ = pool.QueryRow(ctx,
		`SELECT count(*) FROM pg_inherits WHERE inhparent = 'query_stats'::regclass`,
	).Scan(&count)
	if count != 1 {
		t.Fatalf("partitions after drop = %d, want 1", count)
	}

	// The drop must have evicted old from the cache, so re-ensuring it
	// runs the CREATE TABLE again instead of silently skipping it.
	if err := s.EnsureWeeklyPartition(ctx, old); err != nil {
		t.Fatalf("re-ensure old: %v", err)
	}
	_ = pool.QueryRow(ctx,
		`SELECT count(*) FROM pg_inherits WHERE inhparent = 'query_stats'::regclass`,
	).Scan(&count)
	if count != 2 {
		t.Fatalf("partitions after re-ensure = %d, want 2 (cache not evicted on drop)", count)
	}
}
