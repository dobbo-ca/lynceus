package store_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/dobbo-ca/lynceus/internal/store"
)

func TestApplyConfigMigrations_addsChainColumnsAndTrigger(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

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

	var hasUnique bool
	_ = pool.QueryRow(ctx,
		`SELECT EXISTS(
		   SELECT 1 FROM pg_indexes
		   WHERE tablename='audit_log' AND indexdef ILIKE '%UNIQUE%row_hash%'
		 )`,
	).Scan(&hasUnique)
	if !hasUnique {
		t.Error("expected UNIQUE index on audit_log(row_hash)")
	}

	// Idempotent re-apply.
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
	for _, b := range rec.PrevHash {
		if b != 0 {
			t.Fatalf("first row's prev_hash must be zero, got %x", rec.PrevHash)
		}
	}

	rec2, err := cfg.AppendAuditReturning(ctx, store.AuditEntry{Actor: "bob", Action: "login"})
	if err != nil {
		t.Fatalf("append 2: %v", err)
	}
	if !bytes.Equal(rec2.PrevHash, rec.RowHash) {
		t.Fatalf("rec2.prev_hash %x != rec.row_hash %x", rec2.PrevHash, rec.RowHash)
	}
}

func TestVerifyChain_intactAfterAppends(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg := store.NewConfig(pool)

	for i := 0; i < 10; i++ {
		if _, err := cfg.AppendAuditReturning(ctx, store.AuditEntry{
			Actor: "alice", Action: "viewed.t2", DataTier: 2,
			Detail: map[string]any{"i": i},
		}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	bad, reason, err := cfg.VerifyChain(ctx, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if bad != -1 {
		t.Fatalf("verify reported bad=%d reason=%q on intact chain", bad, reason)
	}
}

func TestVerifyChain_detectsRowMutation(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg := store.NewConfig(pool)

	for i := 0; i < 5; i++ {
		if _, err := cfg.AppendAuditReturning(ctx, store.AuditEntry{
			Actor: "alice", Action: "viewed.t2", DataTier: 2,
		}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	// Tamper: bypass the append-only trigger and mutate id=3.
	if _, err := pool.Exec(ctx, `ALTER TABLE audit_log DISABLE TRIGGER USER`); err != nil {
		t.Fatalf("disable trigger: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE audit_log SET actor='mallory' WHERE id = 3`); err != nil {
		t.Fatalf("mutate: %v", err)
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE audit_log ENABLE TRIGGER USER`); err != nil {
		t.Fatalf("enable trigger: %v", err)
	}

	bad, reason, err := cfg.VerifyChain(ctx, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if bad != 2 { // 0-based: id=3 is the 3rd row → idx 2
		t.Fatalf("expected bad=2 (id=3), got bad=%d reason=%q", bad, reason)
	}
}

func TestVerifyChain_detectsDeletion(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg := store.NewConfig(pool)

	for i := 0; i < 5; i++ {
		if _, err := cfg.AppendAuditReturning(ctx, store.AuditEntry{
			Actor: "alice", Action: "viewed.t2", DataTier: 2,
		}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	// Tamper: bypass the trigger and delete id=3, leaving an id gap.
	if _, err := pool.Exec(ctx, `ALTER TABLE audit_log DISABLE TRIGGER USER`); err != nil {
		t.Fatalf("disable trigger: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM audit_log WHERE id = 3`); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE audit_log ENABLE TRIGGER USER`); err != nil {
		t.Fatalf("enable trigger: %v", err)
	}

	bad, reason, err := cfg.VerifyChain(ctx, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if bad != 2 { // walk: id 1,2,4 → at idx 2 the id jumps 2→4
		t.Fatalf("expected bad=2 (id gap at id=4), got bad=%d reason=%q", bad, reason)
	}
}
