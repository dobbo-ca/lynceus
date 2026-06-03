// Integration tests for the store package. They spin up a real
// PostgreSQL via testcontainers — we never mock the database, because
// the schema (partitioning, audit columns) is part of what we're
// validating.
package store_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/dobbo-ca/lynceus/internal/store"
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
		tcpostgres.BasicWaitStrategies(),
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
