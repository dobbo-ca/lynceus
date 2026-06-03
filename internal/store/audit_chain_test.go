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

	if _, err := pool.Exec(ctx, `ALTER TABLE audit_log DISABLE TRIGGER USER`); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM audit_log WHERE id = 3`); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE audit_log ENABLE TRIGGER USER`); err != nil {
		t.Fatalf("enable: %v", err)
	}

	bad, reason, err := cfg.VerifyChain(ctx, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	// After deleting id=3 the walk sees ids 1,2,4,5. The first failure
	// is at idx 2 (id=4) due to the id-gap check.
	if bad != 2 {
		t.Fatalf("expected bad=2 (id gap at id=4), got bad=%d reason=%q", bad, reason)
	}
}

func TestVerifyChain_detectsOutOfOrderInsertion(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg := store.NewConfig(pool)

	for i := 0; i < 3; i++ {
		if _, err := cfg.AppendAuditReturning(ctx, store.AuditEntry{
			Actor: "alice", Action: "login",
		}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	// Disable trigger; splice an attacker row with a fabricated prev_hash
	// that does not match id=1's row_hash.
	if _, err := pool.Exec(ctx, `ALTER TABLE audit_log DISABLE TRIGGER USER`); err != nil {
		t.Fatalf("disable: %v", err)
	}
	// Shift ids 2,3 → 3,4 (still strictly increasing) so we can splice a
	// fabricated row at id=2 before the original id=3.
	if _, err := pool.Exec(ctx, `UPDATE audit_log SET id = id + 10 WHERE id IN (2,3)`); err != nil {
		t.Fatalf("shift: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE audit_log SET id = id - 9 WHERE id IN (12,13)`); err != nil {
		t.Fatalf("re-shift: %v", err)
	}
	// Now ids in table: 1, 3, 4. Splice id=2 with fabricated prev.
	fakePrev := make([]byte, 32) // not equal to row 1's row_hash
	fakePrev[0] = 0xFF
	fakeHash := make([]byte, 32)
	for i := range fakeHash {
		fakeHash[i] = byte(i + 1)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO audit_log (id, actor, action, prev_hash, row_hash, at)
		 VALUES (2, 'mallory', 'planted', $1, $2, now())`, fakePrev, fakeHash,
	); err != nil {
		t.Fatalf("splice: %v", err)
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE audit_log ENABLE TRIGGER USER`); err != nil {
		t.Fatalf("enable: %v", err)
	}

	bad, reason, err := cfg.VerifyChain(ctx, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	// idx 0 = id=1 (good); idx 1 = id=2 (planted, prev_hash mismatch).
	if bad != 1 {
		t.Fatalf("expected bad=1, got bad=%d reason=%q", bad, reason)
	}
}

func TestAppendAudit_concurrentAppendersProduceValidChain(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg := store.NewConfig(pool)

	const writers = 8
	const perWriter = 25
	errCh := make(chan error, writers)
	for w := 0; w < writers; w++ {
		go func(w int) {
			for i := 0; i < perWriter; i++ {
				_, err := cfg.AppendAuditReturning(ctx, store.AuditEntry{
					Actor:  "actor",
					Action: "concurrent",
					Detail: map[string]any{"w": w, "i": i},
				})
				if err != nil {
					errCh <- err
					return
				}
			}
			errCh <- nil
		}(w)
	}
	for i := 0; i < writers; i++ {
		if err := <-errCh; err != nil {
			t.Fatalf("writer: %v", err)
		}
	}

	bad, reason, err := cfg.VerifyChain(ctx, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if bad != -1 {
		t.Fatalf("chain broken under concurrent appenders: bad=%d reason=%q", bad, reason)
	}

	var total int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM audit_log`).Scan(&total)
	if total != writers*perWriter {
		t.Fatalf("row count = %d, want %d", total, writers*perWriter)
	}
}
