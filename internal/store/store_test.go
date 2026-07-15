// Integration tests for the store package. They spin up a real
// PostgreSQL via testcontainers — we never mock the database, because
// the schema (partitioning, audit columns) is part of what we're
// validating.
package store_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/dobbo-ca/lynceus/internal/testpg"
)

// newPool returns a pool scoped to a fresh isolated database on the shared
// Postgres container (one container per package, dropped on cleanup).
func newPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return testpg.Start(t)
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
