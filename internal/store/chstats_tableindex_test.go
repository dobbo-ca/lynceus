package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/dobbo-ca/lynceus/internal/testch"
)

func tableindexTableStatRow(server string, collectedAt time.Time, fqn string) store.TableStatRow {
	return store.TableStatRow{
		ServerID:    server,
		CollectedAt: collectedAt,
		SchemaName:  "reporting", ObjectName: "events", FQN: fqn,
		TotalBytes: 300, HeapBytes: 100, ToastBytes: 120, IndexesBytes: 80,
		RowEstimate: 1000, LiveTuples: 900, DeadTuples: 50, NModSinceAnalyze: 12,
		SeqScan: 4, IdxScan: 30, NTupIns: 1000, NTupUpd: 200, NTupDel: 50, NTupHotUpd: 150,
		VacuumCount: 2, AutovacuumCount: 3,
	}
}

func TestCH_tableindex_TableStats_WriteAndLatest_RoundTrip(t *testing.T) {
	ctx := context.Background()
	conn := testch.Start(t)
	if err := store.ApplyClickHouseMigrations(ctx, conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := store.NewCHStats(conn)

	base := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	// Two fqns; one has an older + newer snapshot to prove "latest per fqn".
	older := tableindexTableStatRow("srv-1", base, "reporting.events")
	older.TotalBytes = 1000
	newer := tableindexTableStatRow("srv-1", base.Add(time.Minute), "reporting.events")
	newer.TotalBytes = 4000
	other := tableindexTableStatRow("srv-1", base, "reporting.audit")
	// LastVacuum set on one row to exercise the Nullable round-trip; the
	// zero-valued last_* timestamps must come back zero, not epoch.
	vac := base.Add(-2 * time.Hour)
	newer.LastVacuum = vac

	if err := s.WriteTableStats(ctx, []store.TableStatRow{older, newer, other}); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := s.LatestTableStats(ctx, "srv-1", base.Add(time.Hour))
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 rows (one per fqn), got %d: %+v", len(got), got)
	}
	// Ordered by fqn ascending: reporting.audit before reporting.events.
	if got[0].FQN != "reporting.audit" || got[1].FQN != "reporting.events" {
		t.Fatalf("fqn ordering wrong: %q then %q", got[0].FQN, got[1].FQN)
	}
	ev := got[1]
	if ev.TotalBytes != 4000 {
		t.Errorf("latest events row = %d bytes, want the newer snapshot (4000)", ev.TotalBytes)
	}
	if ev.HeapBytes != 100 || ev.ToastBytes != 120 || ev.IndexesBytes != 80 {
		t.Errorf("size split not round-tripped: %+v", ev)
	}
	if ev.LiveTuples != 900 || ev.DeadTuples != 50 {
		t.Errorf("tuple metrics not round-tripped: %+v", ev)
	}
	if ev.VacuumCount != 2 || ev.AutovacuumCount != 3 {
		t.Errorf("vacuum metrics not round-tripped: %+v", ev)
	}
	if ev.DataTier != 1 {
		t.Errorf("data_tier = %d, want coerced to 1", ev.DataTier)
	}
	if !ev.LastVacuum.Equal(vac) {
		t.Errorf("LastVacuum = %v, want %v (nullable round-trip)", ev.LastVacuum, vac)
	}
	if !ev.LastAutovacuum.IsZero() || !ev.LastAnalyze.IsZero() || !ev.LastAutoanalyze.IsZero() {
		t.Errorf("unset last_* timestamps should read back zero (NULL), got %+v", ev)
	}
}

func TestCH_tableindex_TableStats_LatestAsOf(t *testing.T) {
	ctx := context.Background()
	conn := testch.Start(t)
	if err := store.ApplyClickHouseMigrations(ctx, conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := store.NewCHStats(conn)

	base := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	older := tableindexTableStatRow("srv-1", base, "reporting.events")
	older.TotalBytes = 1000
	newer := tableindexTableStatRow("srv-1", base.Add(time.Minute), "reporting.events")
	newer.TotalBytes = 4000
	if err := s.WriteTableStats(ctx, []store.TableStatRow{older, newer}); err != nil {
		t.Fatalf("write: %v", err)
	}

	// asOf strictly before the newer snapshot returns the older one.
	got, err := s.LatestTableStats(ctx, "srv-1", base.Add(30*time.Second))
	if err != nil {
		t.Fatalf("latest asOf: %v", err)
	}
	if len(got) != 1 || got[0].TotalBytes != 1000 {
		t.Fatalf("asOf before newer snapshot = %+v, want older (1000)", got)
	}
}

func TestCH_tableindex_TableStats_EmptyNoop(t *testing.T) {
	ctx := context.Background()
	conn := testch.Start(t)
	if err := store.ApplyClickHouseMigrations(ctx, conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := store.NewCHStats(conn).WriteTableStats(ctx, nil); err != nil {
		t.Fatalf("empty write should be a no-op, got %v", err)
	}
}

func TestCH_tableindex_IndexStats_WriteAndLatest_RoundTrip(t *testing.T) {
	ctx := context.Background()
	conn := testch.Start(t)
	if err := store.ApplyClickHouseMigrations(ctx, conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := store.NewCHStats(conn)

	now := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)
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
	if pk.TableFQN != "public.orders" || !pk.IsPrimary || !pk.IsUnique || !pk.IsValid || !pk.IsReady || pk.IdxScan != 9000 {
		t.Fatalf("pk row not preserved: %+v", pk)
	}
	if pk.DataTier != 1 {
		t.Errorf("pk data_tier = %d, want coerced to 1", pk.DataTier)
	}
	bad := byFQN["public.orders_status_idx"]
	if bad.IsValid || bad.IsUnique || bad.IsPrimary || !bad.IsReady || bad.IdxScan != 0 || bad.SizeBytes != 524_288_000 {
		t.Fatalf("invalid/unused row booleans/values not preserved: %+v", bad)
	}
}

func TestCH_tableindex_IndexStats_LatestPerFQN(t *testing.T) {
	ctx := context.Background()
	conn := testch.Start(t)
	if err := store.ApplyClickHouseMigrations(ctx, conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := store.NewCHStats(conn)

	base := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)
	old := store.IndexStatRow{
		ServerID: "srv-b", CollectedAt: base,
		SchemaName: "public", ObjectName: "t_idx", FQN: "public.t_idx",
		TableFQN: "public.t", IdxScan: 5, SizeBytes: 100,
		IsValid: true, IsReady: true, IsUnique: false, IsPrimary: false,
	}
	fresh := old
	fresh.CollectedAt = base.Add(time.Minute)
	fresh.IdxScan = 99

	if err := s.WriteIndexStats(ctx, []store.IndexStatRow{old, fresh}); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := s.LatestIndexStats(ctx, "srv-b", base.Add(time.Hour))
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 latest row per fqn, got %d: %+v", len(got), got)
	}
	if got[0].IdxScan != 99 {
		t.Errorf("latest idx_scan = %d, want the newer snapshot (99)", got[0].IdxScan)
	}

	// asOf before the newer snapshot returns the older counter value.
	prev, err := s.LatestIndexStats(ctx, "srv-b", base.Add(30*time.Second))
	if err != nil {
		t.Fatalf("latest asOf: %v", err)
	}
	if len(prev) != 1 || prev[0].IdxScan != 5 {
		t.Fatalf("asOf before newer snapshot wrong: %+v", prev)
	}
}
