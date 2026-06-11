package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/dobbo-ca/lynceus/internal/store"
)

func TestWriteConnectionSamples_createsPartitionAndRoundtrips(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyStatsMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := store.NewStats(pool)

	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC) // a Tuesday
	older := now.Add(-1 * time.Minute)

	// An older batch that must be shadowed by the latest read.
	if err := s.WriteConnectionSamples(ctx, []store.ConnectionSampleRow{
		{ServerID: "srv-a", ObservedAt: older, PID: 1, State: "active", ActiveSeconds: 10},
	}); err != nil {
		t.Fatalf("write older: %v", err)
	}
	rows := []store.ConnectionSampleRow{
		{ServerID: "srv-a", ObservedAt: now, PID: 42, State: "active", ActiveSeconds: 600, XactSeconds: 650, StateSeconds: 600, WaitEventType: "Lock"},
		{ServerID: "srv-a", ObservedAt: now, PID: 43, State: "idle in transaction", ActiveSeconds: 5, XactSeconds: 400, StateSeconds: 350},
	}
	if err := s.WriteConnectionSamples(ctx, rows); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := s.LatestConnectionSamples(ctx, "srv-a", now)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 latest rows, got %d: %+v", len(got), got)
	}
	byPID := map[int64]store.ConnectionSampleRow{}
	for _, r := range got {
		byPID[r.PID] = r
	}
	if r := byPID[42]; r.State != "active" || r.ActiveSeconds != 600 || r.WaitEventType != "Lock" {
		t.Fatalf("pid 42 not preserved: %+v", r)
	}
	if r := byPID[43]; r.State != "idle in transaction" || r.StateSeconds != 350 {
		t.Fatalf("pid 43 not preserved: %+v", r)
	}
}

func TestWriteBlockingEdges_createsPartitionAndRoundtrips(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyStatsMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := store.NewStats(pool)

	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	if err := s.WriteBlockingEdges(ctx, []store.BlockingEdgeRow{
		{ServerID: "srv-a", ObservedAt: now, BlockedPID: 43, BlockerPID: 42, BlockedWaitSeconds: 90},
	}); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := s.LatestBlockingEdges(ctx, "srv-a", now)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 1 || got[0].BlockedPID != 43 || got[0].BlockerPID != 42 || got[0].BlockedWaitSeconds != 90 {
		t.Fatalf("edge not preserved: %+v", got)
	}
}
