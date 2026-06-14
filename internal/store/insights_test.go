package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/dobbo-ca/lynceus/internal/store"
)

// TestApplyStatsMigrations_createsPartitionedInsights verifies 0005_insights.sql
// creates a range-partitioned insights table (mirrors the query_plans check).
func TestApplyStatsMigrations_createsPartitionedInsights(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyStatsMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var strategy string
	if err := pool.QueryRow(ctx,
		`SELECT partstrat::text FROM pg_partitioned_table
		   WHERE partrelid = 'insights'::regclass`,
	).Scan(&strategy); err != nil {
		t.Fatalf("insights not partitioned: %v", err)
	}
	if strategy != "r" {
		t.Fatalf("partition strategy = %q, want 'r' (range)", strategy)
	}
}

func TestWriteInsights_roundtripCountAndTop(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyStatsMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := store.NewStats(pool)

	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC) // a Wednesday
	rows := []store.InsightRow{
		{
			ServerID: "srv-1", CapturedAt: now, Kind: "slow_scan", Severity: "high",
			Fingerprint: "fp-a", Relation: "orders", NodePath: "Seq Scan(orders)",
			RowsReturned: 10, RowsScanned: 100000, Selectivity: 0.0001,
			Detail: "Seq Scan on orders read 100000 rows and discarded 99990 (0.01% returned).",
		},
		{
			ServerID: "srv-2", CapturedAt: now, Kind: "slow_scan", Severity: "medium",
			Fingerprint: "fp-b", Relation: "events", NodePath: "Seq Scan(events)",
			RowsReturned: 100, RowsScanned: 5100, Selectivity: 0.0196,
			Detail: "Seq Scan on events read 5100 rows and discarded 5000 (1.96% returned).",
		},
	}
	if err := s.WriteInsights(ctx, rows); err != nil {
		t.Fatalf("write: %v", err)
	}

	var partCount int
	_ = pool.QueryRow(ctx,
		`SELECT count(*) FROM pg_inherits WHERE inhparent = 'insights'::regclass`,
	).Scan(&partCount)
	if partCount == 0 {
		t.Fatal("write did not create a weekly partition")
	}

	since, until := now.Add(-time.Hour), now.Add(time.Hour)

	n, err := s.InsightCountForServers(ctx, []string{"srv-1", "srv-2"}, since, until)
	if err != nil || n != 2 {
		t.Fatalf("InsightCountForServers = %d err=%v, want 2", n, err)
	}
	n1, err := s.InsightCountForServers(ctx, []string{"srv-1"}, since, until)
	if err != nil || n1 != 1 {
		t.Fatalf("InsightCountForServers[srv-1] = %d err=%v, want 1", n1, err)
	}

	top, err := s.TopInsightsForServers(ctx, []string{"srv-1", "srv-2"}, since, until, 10)
	if err != nil {
		t.Fatalf("TopInsightsForServers: %v", err)
	}
	if len(top) != 2 {
		t.Fatalf("top = %d rows, want 2", len(top))
	}
	var got store.InsightRow
	for _, r := range top {
		if r.ServerID == "srv-1" {
			got = r
		}
	}
	if got.Relation != "orders" || got.Severity != "high" || got.RowsScanned != 100000 {
		t.Fatalf("srv-1 insight round-trip wrong: %+v", got)
	}
}

func TestWriteInsights_emptyNoop(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyStatsMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := store.NewStats(pool).WriteInsights(ctx, nil); err != nil {
		t.Fatalf("empty WriteInsights should be a no-op, got %v", err)
	}
}
