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
