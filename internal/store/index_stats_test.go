package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/dobbo-ca/lynceus/internal/store"
)

func TestWriteIndexStats_createsPartitionAndRoundtrips(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyStatsMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := store.NewStats(pool)

	now := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC) // a Thursday
	rows := []store.IndexStatRow{
		{
			ServerID: "srv-a", CollectedAt: now,
			SchemaName: "public", ObjectName: "orders_pkey", FQN: "public.orders_pkey",
			TableFQN: "public.orders", IdxScan: 9000, SizeBytes: 8192,
			IsValid: true, IsReady: true, IsUnique: true, IsPrimary: true,
		},
		{
			ServerID: "srv-a", CollectedAt: now,
			SchemaName: "public", ObjectName: "orders_status_idx", FQN: "public.orders_status_idx",
			TableFQN: "public.orders", IdxScan: 0, SizeBytes: 524_288_000,
			IsValid: false, IsReady: true, IsUnique: false, IsPrimary: false,
		},
	}
	if err := s.WriteIndexStats(ctx, rows); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := s.LatestIndexStats(ctx, "srv-a", now)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 rows, got %d: %+v", len(got), got)
	}
	byFQN := map[string]store.IndexStatRow{}
	for _, r := range got {
		byFQN[r.FQN] = r
	}
	pk := byFQN["public.orders_pkey"]
	if pk.TableFQN != "public.orders" || !pk.IsPrimary || !pk.IsUnique || pk.IdxScan != 9000 {
		t.Fatalf("pk row not preserved: %+v", pk)
	}
	bad := byFQN["public.orders_status_idx"]
	if bad.IsValid || bad.IdxScan != 0 || bad.SizeBytes != 524_288_000 {
		t.Fatalf("invalid/unused row not preserved: %+v", bad)
	}
}
