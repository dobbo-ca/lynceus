package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/dobbo-ca/lynceus/internal/testch"
)

// freezexminNewCH boots a ClickHouse container, applies the CH migrations, and
// returns a ready chStats. Prefixed to avoid collisions with other domains'
// test helpers in package store_test.
func freezexminNewCH(t *testing.T) (context.Context, store.Stats) {
	t.Helper()
	ctx := context.Background()
	conn := testch.Start(t)
	if err := store.ApplyClickHouseMigrations(ctx, conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return ctx, store.NewCHStats(conn)
}

// freezexminAssertFreeze checks a FreezeAgeRow's scope, age counts, and that
// data_tier was coerced to 1 on write.
func freezexminAssertFreeze(t *testing.T, got *store.FreezeAgeRow, wantScope string, wantXID, wantMXID int64) {
	t.Helper()
	if got.Scope != wantScope {
		t.Errorf("scope = %q, want %q (%+v)", got.Scope, wantScope, got)
	}
	if got.XIDAge != wantXID {
		t.Errorf("xid_age = %d, want %d (%+v)", got.XIDAge, wantXID, got)
	}
	if got.MXIDAge != wantMXID {
		t.Errorf("mxid_age = %d, want %d (%+v)", got.MXIDAge, wantMXID, got)
	}
	if got.DataTier != 1 {
		t.Errorf("data_tier = %d, want 1 coerced (%+v)", got.DataTier, got)
	}
}

func TestCH_freezexmin_FreezeAges_RoundTripAndLatest(t *testing.T) {
	ctx, s := freezexminNewCH(t)

	t0 := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Hour)

	rows := []store.FreezeAgeRow{
		// t0 snapshot for srv-a (older; DataTier unset -> coerced to 1).
		{ServerID: "srv-a", CollectedAt: t0, Scope: "database", SchemaName: "", ObjectName: "appdb", FQN: "appdb", XIDAge: 100, MXIDAge: 1, AutovacuumFreezeMaxAge: 200_000_000},
		{ServerID: "srv-a", CollectedAt: t0, Scope: "table", SchemaName: "public", ObjectName: "orders", FQN: "public.orders", XIDAge: 200, MXIDAge: 2, AutovacuumFreezeMaxAge: 200_000_000},
		// t1 snapshot for srv-a (newer; should win in latest-as-of at t1).
		{ServerID: "srv-a", CollectedAt: t1, Scope: "database", SchemaName: "", ObjectName: "appdb", FQN: "appdb", XIDAge: 150, MXIDAge: 3, AutovacuumFreezeMaxAge: 200_000_000},
		{ServerID: "srv-a", CollectedAt: t1, Scope: "table", SchemaName: "public", ObjectName: "orders", FQN: "public.orders", XIDAge: 250, MXIDAge: 4, AutovacuumFreezeMaxAge: 200_000_000},
		// A different server at t1 must not leak into srv-a reads.
		{ServerID: "srv-b", CollectedAt: t1, Scope: "database", SchemaName: "", ObjectName: "appdb", FQN: "appdb", XIDAge: 999, MXIDAge: 9, AutovacuumFreezeMaxAge: 200_000_000},
	}
	if err := s.WriteFreezeAges(ctx, rows); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Latest as of t1: newest row per fqn, ordered by fqn.
	got, err := s.LatestFreezeAges(ctx, "srv-a", t1)
	if err != nil {
		t.Fatalf("latest@t1: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 rows, got %d: %+v", len(got), got)
	}
	if got[0].FQN != "appdb" || got[1].FQN != "public.orders" {
		t.Fatalf("not ordered by fqn: %+v", got)
	}
	// The DataTier==1 assertions also prove coercion happened on write: had
	// DataTier stayed 0, the data_tier=1 read filter would have dropped the rows.
	freezexminAssertFreeze(t, &got[0], "database", 150, 3)
	freezexminAssertFreeze(t, &got[1], "table", 250, 4)
	if got[1].ObjectName != "orders" {
		t.Errorf("orders object_name = %q, want orders", got[1].ObjectName)
	}

	// Latest as of t0: only the older snapshot is visible (t1 excluded).
	gotT0, err := s.LatestFreezeAges(ctx, "srv-a", t0)
	if err != nil {
		t.Fatalf("latest@t0: %v", err)
	}
	if len(gotT0) != 2 {
		t.Fatalf("want 2 rows @t0, got %d: %+v", len(gotT0), gotT0)
	}
	if gotT0[0].XIDAge != 100 || gotT0[1].XIDAge != 200 {
		t.Errorf("t0 snapshot not returned: %+v", gotT0)
	}

	// Before any snapshot: empty.
	before, err := s.LatestFreezeAges(ctx, "srv-a", t0.Add(-time.Hour))
	if err != nil {
		t.Fatalf("latest before: %v", err)
	}
	if len(before) != 0 {
		t.Fatalf("want no rows before first snapshot, got %+v", before)
	}
}

func TestCH_freezexmin_XminHorizon_RoundTripAndNotFound(t *testing.T) {
	ctx, s := freezexminNewCH(t)

	t0 := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Hour)

	rows := []store.XminHorizonRow{
		{ServerID: "srv-a", CollectedAt: t0, OldestXminAge: 100, HolderKind: "backend"},
		{ServerID: "srv-a", CollectedAt: t1, OldestXminAge: 200, HolderKind: "replication_slot"},
	}
	if err := s.WriteXminHorizons(ctx, rows); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Latest as of t1: the newer row wins.
	got, ok, err := s.LatestXminHorizon(ctx, "srv-a", t1)
	if err != nil {
		t.Fatalf("latest@t1: %v", err)
	}
	if !ok {
		t.Fatalf("found=false, want true")
	}
	if got.OldestXminAge != 200 || got.HolderKind != "replication_slot" {
		t.Errorf("latest@t1 wrong: %+v", got)
	}
	if got.DataTier != 1 {
		t.Errorf("data_tier = %d, want 1 (coerced)", got.DataTier)
	}

	// Latest as of t0: only the older row is visible.
	gotT0, ok, err := s.LatestXminHorizon(ctx, "srv-a", t0)
	if err != nil {
		t.Fatalf("latest@t0: %v", err)
	}
	if !ok || gotT0.OldestXminAge != 100 || gotT0.HolderKind != "backend" {
		t.Errorf("latest@t0 wrong: ok=%v %+v", ok, gotT0)
	}

	// Unknown server: not found.
	if _, ok, err := s.LatestXminHorizon(ctx, "nope", t1); err != nil || ok {
		t.Fatalf("unknown server: ok=%v err=%v, want ok=false err=nil", ok, err)
	}

	// Before any snapshot: not found.
	if _, ok, err := s.LatestXminHorizon(ctx, "srv-a", t0.Add(-time.Hour)); err != nil || ok {
		t.Fatalf("before first snapshot: ok=%v err=%v, want ok=false err=nil", ok, err)
	}
}
