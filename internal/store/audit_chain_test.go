package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/dobbo-ca/lynceus/internal/store"
)

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
	if bad != 2 { // 0-based: id=1 → idx 0, id=2 → idx 1, id=3 → idx 2
		t.Fatalf("expected bad=2 (id=3), got bad=%d reason=%q", bad, reason)
	}
}
