package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/dobbo-ca/lynceus/internal/testch"
)

func TestCH_insights_WriteCountTop_RoundTrip(t *testing.T) {
	ctx := context.Background()
	conn := testch.Start(t)
	if err := store.ApplyClickHouseMigrations(ctx, conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := store.NewCHStats(conn)

	base := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	rows := []store.InsightRow{
		{
			// DataTier left 0 on purpose: WriteInsights must normalize it to 1.
			ServerID: "srv-1", CapturedAt: base, Kind: "slow_scan", Severity: "high",
			Fingerprint: "fp-a", Relation: "orders", NodePath: "Seq Scan(orders)",
			RowsReturned: 10, RowsScanned: 100000, Selectivity: 0.0001,
			Detail: "Seq Scan on orders read 100000 rows and discarded 99990.",
		},
		{
			ServerID: "srv-2", CapturedAt: base.Add(time.Minute), Kind: "slow_scan", Severity: "medium",
			Fingerprint: "fp-b", Relation: "events", NodePath: "Seq Scan(events)",
			RowsReturned: 100, RowsScanned: 5100, Selectivity: 0.0196,
			Detail: "Seq Scan on events read 5100 rows and discarded 5000.",
		},
		{
			// Tier-2 row must be excluded by the data_tier = 1 filter.
			ServerID: "srv-1", CapturedAt: base, Kind: "slow_scan", Severity: "low",
			Fingerprint: "fp-c", Relation: "secret", NodePath: "Seq Scan(secret)",
			RowsReturned: 1, RowsScanned: 2, Selectivity: 0.5,
			Detail: "tier-2, must not surface", DataTier: 2,
		},
	}
	if err := s.WriteInsights(ctx, rows); err != nil {
		t.Fatalf("write: %v", err)
	}

	since, until := base.Add(-time.Hour), base.Add(time.Hour)

	n, err := s.InsightCountForServers(ctx, []string{"srv-1", "srv-2"}, since, until)
	if err != nil || n != 2 {
		t.Fatalf("InsightCountForServers = %d err=%v, want 2 (T2 excluded)", n, err)
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
		t.Fatalf("top = %d rows, want 2 (T2 excluded)", len(top))
	}
	// Ordered by captured_at DESC => srv-2 (base+1m) first.
	if top[0].ServerID != "srv-2" {
		t.Errorf("row0 = %q, want srv-2 (most recent)", top[0].ServerID)
	}

	var got store.InsightRow
	for _, r := range top {
		if r.ServerID == "srv-1" {
			got = r
		}
	}
	if got.Relation != "orders" || got.Severity != "high" || got.RowsScanned != 100000 ||
		got.RowsReturned != 10 || got.Selectivity != 0.0001 || got.Fingerprint != "fp-a" ||
		got.NodePath != "Seq Scan(orders)" || got.DataTier != 1 {
		t.Fatalf("srv-1 insight round-trip wrong (DataTier must normalize 0->1): %+v", got)
	}
}

func TestCH_insights_WriteEmpty_Noop(t *testing.T) {
	ctx := context.Background()
	conn := testch.Start(t)
	if err := store.ApplyClickHouseMigrations(ctx, conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := store.NewCHStats(conn).WriteInsights(ctx, nil); err != nil {
		t.Fatalf("empty WriteInsights should be a no-op, got %v", err)
	}
}
