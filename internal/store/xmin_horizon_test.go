package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/dobbo-ca/lynceus/internal/store"
)

func TestWriteXminHorizon_roundtrips(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyStatsMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := store.NewStats(pool)

	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC) // a Tuesday
	rows := []store.XminHorizonRow{
		{
			ServerID: "srv-a", CollectedAt: now,
			OldestXminAge: 123_456_789, HolderKind: "replication_slot",
		},
	}
	if err := s.WriteXminHorizons(ctx, rows); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, ok, err := s.LatestXminHorizon(ctx, "srv-a", now)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !ok {
		t.Fatalf("LatestXminHorizon found=false, want true")
	}
	if got.OldestXminAge != 123_456_789 || got.HolderKind != "replication_slot" {
		t.Fatalf("row not preserved: %+v", got)
	}
	if got.DataTier != 1 {
		t.Fatalf("data_tier = %d, want 1 (coerced)", got.DataTier)
	}
}
