package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/dobbo-ca/lynceus/internal/store"
)

func TestWriteFreezeAges_createsPartitionAndRoundtrips(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyStatsMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := store.NewStats(pool)

	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC) // a Tuesday
	rows := []store.FreezeAgeRow{
		{
			ServerID: "srv-a", CollectedAt: now,
			Scope: "database", SchemaName: "", ObjectName: "appdb", FQN: "appdb",
			XIDAge: 600_000_000, MXIDAge: 12_000, AutovacuumFreezeMaxAge: 200_000_000,
		},
		{
			ServerID: "srv-a", CollectedAt: now,
			Scope: "table", SchemaName: "public", ObjectName: "orders", FQN: "public.orders",
			XIDAge: 1_800_000_000, MXIDAge: 5, AutovacuumFreezeMaxAge: 200_000_000,
		},
	}
	if err := s.WriteFreezeAges(ctx, rows); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := s.LatestFreezeAges(ctx, "srv-a", now)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 rows, got %d: %+v", len(got), got)
	}

	byFQN := map[string]store.FreezeAgeRow{}
	for _, r := range got {
		byFQN[r.FQN] = r
	}
	db, ok := byFQN["appdb"]
	if !ok || db.Scope != "database" || db.XIDAge != 600_000_000 || db.AutovacuumFreezeMaxAge != 200_000_000 {
		t.Fatalf("database row not preserved: %+v", db)
	}
	tbl, ok := byFQN["public.orders"]
	if !ok || tbl.Scope != "table" || tbl.XIDAge != 1_800_000_000 || tbl.MXIDAge != 5 {
		t.Fatalf("table row not preserved: %+v", tbl)
	}
}
