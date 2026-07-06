package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/dobbo-ca/lynceus/internal/store"
)

func seedAuditRows(t *testing.T, cfg store.Config) {
	t.Helper()
	ctx := context.Background()
	rows := []store.AuditEntry{
		{Actor: "alice", Action: "login", ServerID: "srv-1", DataTier: 0},
		{Actor: "alice", Action: "viewed.t2", ServerID: "srv-1", DataTier: 2, Detail: map[string]any{"fp": "abc"}},
		{Actor: "bob", Action: "viewed.t2", ServerID: "srv-2", DataTier: 2},
		{Actor: "bob", Action: "config.toggle", ServerID: "srv-2", DataTier: 1},
		{Actor: "carol", Action: "login", DataTier: 0},
	}
	for i, e := range rows {
		if _, err := cfg.AppendAuditReturning(ctx, e); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}
}

func TestListAudit_noFilter_returnsAllMostRecentFirst(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg := store.NewConfig(pool)
	seedAuditRows(t, cfg)

	got, err := cfg.ListAudit(ctx, store.AuditFilter{Limit: 100})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("rows = %d, want 5", len(got))
	}
	// Most recent first → highest id first → last seeded actor "carol".
	if got[0].Actor != "carol" {
		t.Errorf("got[0].Actor = %q, want carol (most recent first)", got[0].Actor)
	}
}

func TestListAudit_filtersByActorActionServerTier(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg := store.NewConfig(pool)
	seedAuditRows(t, cfg)

	byActor, err := cfg.ListAudit(ctx, store.AuditFilter{Actor: "alice", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(byActor) != 2 {
		t.Errorf("actor=alice rows = %d, want 2", len(byActor))
	}

	byAction, err := cfg.ListAudit(ctx, store.AuditFilter{Action: "viewed.t2", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(byAction) != 2 {
		t.Errorf("action=viewed.t2 rows = %d, want 2", len(byAction))
	}

	byServer, err := cfg.ListAudit(ctx, store.AuditFilter{ServerID: "srv-2", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(byServer) != 2 {
		t.Errorf("server=srv-2 rows = %d, want 2", len(byServer))
	}

	tier := int16(2)
	byTier, err := cfg.ListAudit(ctx, store.AuditFilter{Tier: &tier, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(byTier) != 2 {
		t.Errorf("tier=2 rows = %d, want 2", len(byTier))
	}

	// Combined filter narrows further.
	both, err := cfg.ListAudit(ctx, store.AuditFilter{Actor: "bob", Tier: &tier, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(both) != 1 || both[0].Action != "viewed.t2" {
		t.Errorf("actor=bob tier=2 = %+v, want exactly bob/viewed.t2", both)
	}
}

func TestListAudit_filtersByTimeRangeAndAppliesLimit(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg := store.NewConfig(pool)
	seedAuditRows(t, cfg)

	now := time.Now().UTC()

	// Window covering "now" returns everything.
	wide, err := cfg.ListAudit(ctx, store.AuditFilter{
		Since: now.Add(-time.Hour), Until: now.Add(time.Hour), Limit: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(wide) != 5 {
		t.Errorf("wide window rows = %d, want 5", len(wide))
	}

	// Window entirely in the future returns nothing.
	future, err := cfg.ListAudit(ctx, store.AuditFilter{
		Since: now.Add(time.Hour), Limit: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(future) != 0 {
		t.Errorf("future window rows = %d, want 0", len(future))
	}

	// Limit caps the result set.
	capped, err := cfg.ListAudit(ctx, store.AuditFilter{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(capped) != 2 {
		t.Errorf("limit=2 rows = %d, want 2", len(capped))
	}
}
