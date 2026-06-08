package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/dobbo-ca/lynceus/internal/store"
)

func TestApplyStatsMigrations_createsPartitionedTableStats(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyStatsMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var strategy string
	err := pool.QueryRow(ctx,
		`SELECT partstrat::text FROM pg_partitioned_table
		 WHERE partrelid = 'table_stats'::regclass`,
	).Scan(&strategy)
	if err != nil {
		t.Fatalf("table_stats not partitioned: %v", err)
	}
	if strategy != "r" {
		t.Fatalf("partition strategy = %q, want 'r' (range)", strategy)
	}
}

func tableStatRow(server string, collectedAt time.Time, fqn string) store.TableStatRow {
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

func TestWriteTableStats_createsPartitionAndRoundtrips(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyStatsMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := store.NewStats(pool)

	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC) // a Wednesday
	row := tableStatRow("srv-1", now, "reporting.events")
	if err := s.WriteTableStats(ctx, []store.TableStatRow{row}); err != nil {
		t.Fatalf("write: %v", err)
	}

	var partCount int
	_ = pool.QueryRow(ctx,
		`SELECT count(*) FROM pg_inherits WHERE inhparent = 'table_stats'::regclass`,
	).Scan(&partCount)
	if partCount == 0 {
		t.Fatal("write did not create a weekly partition")
	}

	out, err := s.LatestTableStats(ctx, "srv-1", now.Add(time.Hour))
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("got %d rows, want 1", len(out))
	}
	got := out[0]
	if got.FQN != "reporting.events" {
		t.Errorf("fqn = %q, want reporting.events", got.FQN)
	}
	if got.TotalBytes != 300 || got.HeapBytes != 100 || got.ToastBytes != 120 || got.IndexesBytes != 80 {
		t.Errorf("size split = total=%d heap=%d toast=%d idx=%d, want 300/100/120/80",
			got.TotalBytes, got.HeapBytes, got.ToastBytes, got.IndexesBytes)
	}
	if got.DeadTuples != 50 || got.LiveTuples != 900 || got.VacuumCount != 2 || got.AutovacuumCount != 3 {
		t.Errorf("tuple/vacuum metrics not round-tripped: %+v", got)
	}
}

func TestTableSizeSeries_growth(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyStatsMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := store.NewStats(pool)

	wk1 := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC) // Wednesday week 22
	wk2 := wk1.AddDate(0, 0, 7)                           // same weekday, next week

	r1 := tableStatRow("srv-1", wk1, "reporting.events")
	r1.TotalBytes = 1000
	r2 := tableStatRow("srv-1", wk2, "reporting.events")
	r2.TotalBytes = 4000
	if err := s.WriteTableStats(ctx, []store.TableStatRow{r1, r2}); err != nil {
		t.Fatalf("write: %v", err)
	}

	series, err := s.TableSizeSeries(ctx, "srv-1", "reporting.events",
		wk1.Add(-time.Hour), wk2.Add(time.Hour))
	if err != nil {
		t.Fatalf("series: %v", err)
	}
	if len(series) != 2 {
		t.Fatalf("got %d points, want 2", len(series))
	}
	if !series[0].CollectedAt.Before(series[1].CollectedAt) {
		t.Fatalf("series not time-ordered: %v then %v", series[0].CollectedAt, series[1].CollectedAt)
	}
	if series[0].TotalBytes != 1000 || series[1].TotalBytes != 4000 {
		t.Errorf("growth not preserved: %d then %d, want 1000 then 4000",
			series[0].TotalBytes, series[1].TotalBytes)
	}
}

func TestWriteTableStats_emptyNoop(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyStatsMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := store.NewStats(pool).WriteTableStats(ctx, nil); err != nil {
		t.Fatalf("empty write should be a no-op, got %v", err)
	}
}
