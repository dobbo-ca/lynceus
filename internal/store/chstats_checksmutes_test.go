package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/dobbo-ca/lynceus/internal/testch"
)

// checksmutesSetup boots ClickHouse, migrates, and returns a ready chStats.
func checksmutesSetup(t *testing.T) (context.Context, store.Stats) {
	t.Helper()
	ctx := context.Background()
	conn := testch.Start(t)
	if err := store.ApplyClickHouseMigrations(ctx, conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return ctx, store.NewCHStats(conn)
}

// checksmutesList is a thin ListMutes wrapper that fails the test on error.
func checksmutesList(t *testing.T, ctx context.Context, s store.Stats, serverID string) []store.MuteRow {
	t.Helper()
	got, err := s.ListMutes(ctx, serverID)
	if err != nil {
		t.Fatalf("list mutes: %v", err)
	}
	return got
}

func TestCH_checksmutes_ChecksResultsRoundTrip(t *testing.T) {
	ctx, s := checksmutesSetup(t)

	base := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	rows := []store.ChecksResultRow{
		// Older observation for the same (check, object): must be shadowed by the newer one.
		{ServerID: "srv-a", EvaluatedAt: base, CheckID: "vacuum.wraparound", Category: "vacuum",
			Severity: "critical", Status: "firing", Object: "public.orders", Detail: "old", Muted: false, DataTier: 1},
		{ServerID: "srv-a", EvaluatedAt: base.Add(time.Minute), CheckID: "vacuum.wraparound", Category: "vacuum",
			Severity: "critical", Status: "firing", Object: "public.orders", Detail: "new", Muted: true, DataTier: 1},
		// A different check/object (warning severity => sorts after critical).
		{ServerID: "srv-a", EvaluatedAt: base, CheckID: "bloat.table", Category: "bloat",
			Severity: "warning", Status: "firing", Object: "public.events", Detail: "bloaty", DataTier: 1},
		// A T2 row that must never surface in the T1 latest read.
		{ServerID: "srv-a", EvaluatedAt: base.Add(2 * time.Minute), CheckID: "vacuum.wraparound", Category: "vacuum",
			Severity: "critical", Status: "firing", Object: "public.orders", Detail: "t2 secret", DataTier: 2},
		// A different server: must be excluded.
		{ServerID: "srv-b", EvaluatedAt: base, CheckID: "vacuum.wraparound", Category: "vacuum",
			Severity: "critical", Status: "firing", Object: "public.orders", Detail: "other srv", DataTier: 1},
		// DataTier==0 must coerce to 1: exercise the write path's coercion.
		{ServerID: "srv-a", EvaluatedAt: base, CheckID: "conn.saturation", Category: "connections",
			Severity: "info", Status: "firing", Object: "", Detail: "coerced", DataTier: 0},
	}
	if err := s.WriteChecksResults(ctx, rows); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := s.LatestChecksResults(ctx, "srv-a", base.Add(-time.Hour), base.Add(time.Hour))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	// Three distinct (check_id, object) T1 keys for srv-a. Ordered by severity
	// ascending (critical < info < warning lexically): vacuum, conn, bloat.
	if len(got) != 3 {
		t.Fatalf("want 3 latest rows, got %d: %+v", len(got), got)
	}
	if got[0].CheckID != "vacuum.wraparound" || got[0].Detail != "new" || !got[0].Muted {
		t.Errorf("row0 = %+v, want latest vacuum.wraparound detail=new muted=true", got[0])
	}
	if got[1].CheckID != "conn.saturation" || got[1].DataTier != 1 {
		t.Errorf("row1 = %+v, want conn.saturation with coerced data_tier=1", got[1])
	}
	if got[2].CheckID != "bloat.table" || got[2].Object != "public.events" {
		t.Errorf("row2 = %+v, want bloat.table public.events", got[2])
	}
	for _, r := range got {
		if r.DataTier != 1 {
			t.Errorf("T1 read leaked non-T1 row: %+v", r)
		}
		if r.Detail == "t2 secret" {
			t.Errorf("T1 read leaked T2 row: %+v", r)
		}
		if r.ServerID != "srv-a" {
			t.Errorf("read leaked other server: %+v", r)
		}
	}
}

func TestCH_checksmutes_MuteLifecycle(t *testing.T) {
	ctx, s := checksmutesSetup(t)

	future := time.Now().Add(time.Hour)

	// (a) SetMute then ListMutes shows it.
	if err := s.SetMute(ctx, "srv-a", "vacuum.wraparound", "public.orders", future, "planned maintenance"); err != nil {
		t.Fatalf("set: %v", err)
	}
	muted := checksmutesList(t, ctx, s, "srv-a")
	if len(muted) != 1 {
		t.Fatalf("after SetMute want 1 mute, got %d: %+v", len(muted), muted)
	}
	if muted[0].CheckID != "vacuum.wraparound" || muted[0].Object != "public.orders" || muted[0].Reason != "planned maintenance" {
		t.Errorf("mute = %+v, want vacuum.wraparound/public.orders/planned maintenance", muted[0])
	}

	// A mute for a different server must not leak in.
	if other := checksmutesList(t, ctx, s, "srv-b"); len(other) != 0 {
		t.Fatalf("srv-b should have no mutes, got %+v", other)
	}

	// (b) ClearMute then ListMutes hides it.
	if err := s.ClearMute(ctx, "srv-a", "vacuum.wraparound", "public.orders"); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if muted := checksmutesList(t, ctx, s, "srv-a"); len(muted) != 0 {
		t.Fatalf("after ClearMute want 0 mutes, got %d: %+v", len(muted), muted)
	}

	// (c) A mute whose until is in the past is NOT listed.
	past := time.Now().Add(-time.Hour)
	if err := s.SetMute(ctx, "srv-a", "bloat.table", "", past, "already expired"); err != nil {
		t.Fatalf("set past: %v", err)
	}
	if muted := checksmutesList(t, ctx, s, "srv-a"); len(muted) != 0 {
		t.Fatalf("expired mute must not be listed, got %+v", muted)
	}

	// Re-muting a previously cleared key must resurface it (latest version wins).
	if err := s.SetMute(ctx, "srv-a", "vacuum.wraparound", "public.orders", future, "again"); err != nil {
		t.Fatalf("re-set: %v", err)
	}
	muted = checksmutesList(t, ctx, s, "srv-a")
	if len(muted) != 1 || muted[0].Reason != "again" {
		t.Fatalf("after re-SetMute want 1 mute reason=again, got %+v", muted)
	}
}
