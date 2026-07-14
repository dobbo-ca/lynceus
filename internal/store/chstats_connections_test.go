package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/dobbo-ca/lynceus/internal/testch"
)

// connectionsSetupCH boots a ClickHouse container, applies the CH migrations,
// and returns a ready chStats-backed Stats. Prefixed to avoid collisions with
// other domains' test helpers in package store_test.
func connectionsSetupCH(t *testing.T) (context.Context, store.Stats) {
	t.Helper()
	ctx := context.Background()
	conn := testch.Start(t)
	if err := store.ApplyClickHouseMigrations(ctx, conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return ctx, store.NewCHStats(conn)
}

func TestCH_connections_ConnectionSamplesRoundTrip(t *testing.T) {
	ctx, s := connectionsSetupCH(t)

	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	older := now.Add(-1 * time.Minute)

	// An older batch that must be shadowed by the latest-as-of read.
	if err := s.WriteConnectionSamples(ctx, []store.ConnectionSampleRow{
		{ServerID: "srv-a", ObservedAt: older, PID: 1, State: "active", ActiveSeconds: 10},
	}); err != nil {
		t.Fatalf("write older: %v", err)
	}
	rows := []store.ConnectionSampleRow{
		{ServerID: "srv-a", ObservedAt: now, PID: 42, State: "active", ActiveSeconds: 600, XactSeconds: 650, StateSeconds: 600, WaitEventType: "Lock"},
		{ServerID: "srv-a", ObservedAt: now, PID: 43, State: "idle in transaction", ActiveSeconds: 5, XactSeconds: 400, StateSeconds: 350},
		// A different server at the same instant must not leak into srv-a's read.
		{ServerID: "srv-b", ObservedAt: now, PID: 99, State: "active", ActiveSeconds: 1},
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
	// Ordered by pid ascending.
	if got[0].PID != 42 || got[1].PID != 43 {
		t.Fatalf("pid ordering wrong: %+v", got)
	}
	byPID := map[int64]store.ConnectionSampleRow{}
	for _, r := range got {
		byPID[r.PID] = r
	}
	if r := byPID[42]; r.State != "active" || r.ActiveSeconds != 600 || r.XactSeconds != 650 || r.StateSeconds != 600 || r.WaitEventType != "Lock" {
		t.Fatalf("pid 42 not preserved: %+v", r)
	}
	if r := byPID[43]; r.State != "idle in transaction" || r.StateSeconds != 350 || r.XactSeconds != 400 {
		t.Fatalf("pid 43 not preserved: %+v", r)
	}
	// DataTier 0 must be coerced to 1 on write.
	if r := byPID[42]; r.DataTier != 1 {
		t.Fatalf("pid 42 data_tier = %d, want 1", r.DataTier)
	}
}

func TestCH_connections_ConnectionSamplesAsOfShadowsNewer(t *testing.T) {
	ctx, s := connectionsSetupCH(t)

	t0 := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	t1 := t0.Add(1 * time.Minute)
	if err := s.WriteConnectionSamples(ctx, []store.ConnectionSampleRow{
		{ServerID: "srv-a", ObservedAt: t0, PID: 1, State: "active", ActiveSeconds: 5},
		{ServerID: "srv-a", ObservedAt: t1, PID: 2, State: "active", ActiveSeconds: 7},
	}); err != nil {
		t.Fatalf("write: %v", err)
	}

	// asOf == t0 must return only the t0 batch, not the newer t1 row.
	got, err := s.LatestConnectionSamples(ctx, "srv-a", t0)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 1 || got[0].PID != 1 {
		t.Fatalf("as-of read wrong: %+v", got)
	}

	// Unknown server yields no rows (and no error).
	empty, err := s.LatestConnectionSamples(ctx, "nope", t1)
	if err != nil {
		t.Fatalf("read unknown: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("want no rows for unknown server, got %+v", empty)
	}
}

func TestCH_connections_BlockingEdgesRoundTrip(t *testing.T) {
	ctx, s := connectionsSetupCH(t)

	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	older := now.Add(-1 * time.Minute)

	if err := s.WriteBlockingEdges(ctx, []store.BlockingEdgeRow{
		{ServerID: "srv-a", ObservedAt: older, BlockedPID: 1, BlockerPID: 2, BlockedWaitSeconds: 10},
	}); err != nil {
		t.Fatalf("write older: %v", err)
	}
	if err := s.WriteBlockingEdges(ctx, []store.BlockingEdgeRow{
		{ServerID: "srv-a", ObservedAt: now, BlockedPID: 43, BlockerPID: 42, BlockedWaitSeconds: 90},
		{ServerID: "srv-a", ObservedAt: now, BlockedPID: 44, BlockerPID: 42, BlockedWaitSeconds: 30},
	}); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := s.LatestBlockingEdges(ctx, "srv-a", now)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 latest edges, got %d: %+v", len(got), got)
	}
	// Ordered by (blocked_pid, blocker_pid).
	if got[0].BlockedPID != 43 || got[1].BlockedPID != 44 {
		t.Fatalf("edge ordering wrong: %+v", got)
	}
	if got[0].BlockerPID != 42 || got[0].BlockedWaitSeconds != 90 {
		t.Fatalf("edge not preserved: %+v", got[0])
	}
	if got[0].DataTier != 1 {
		t.Fatalf("data_tier = %d, want 1", got[0].DataTier)
	}
}
