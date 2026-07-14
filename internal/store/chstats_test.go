package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/dobbo-ca/lynceus/internal/testch"
)

func TestCHStats_WriteAndTopQueries_RoundTrip(t *testing.T) {
	ctx := context.Background()
	conn := testch.Start(t)
	if err := store.ApplyClickHouseMigrations(ctx, conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := store.NewCHStats(conn)

	base := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	rows := []store.QueryStat{
		{ServerID: "srv1", CollectedAt: base, Fingerprint: "fp-a", NormalizedQuery: "SELECT a", DataTier: 1, Calls: 10, TotalTimeMs: 100},
		{ServerID: "srv1", CollectedAt: base, Fingerprint: "fp-b", NormalizedQuery: "SELECT b", DataTier: 1, Calls: 5, TotalTimeMs: 500},
		{ServerID: "srv1", CollectedAt: base.Add(time.Minute), Fingerprint: "fp-a", NormalizedQuery: "SELECT a", DataTier: 1, Calls: 20, TotalTimeMs: 300},
	}
	if err := s.WriteQueryStats(ctx, rows); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := s.TopQueriesByTotalTime(ctx, base.Add(-time.Hour), base.Add(time.Hour), 10)
	if err != nil {
		t.Fatalf("top: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 fingerprints, got %d: %+v", len(got), got)
	}
	// fp-a: total 400 (100+300), calls 30; fp-b: total 500, calls 5.
	// Ordered by total desc => fp-b first.
	if got[0].Fingerprint != "fp-b" || got[0].TotalTimeMs != 500 || got[0].Calls != 5 {
		t.Errorf("row0 = %+v, want fp-b total=500 calls=5", got[0])
	}
	if got[1].Fingerprint != "fp-a" || got[1].TotalTimeMs != 400 || got[1].Calls != 30 {
		t.Errorf("row1 = %+v, want fp-a total=400 calls=30", got[1])
	}
}

func TestCHStats_TierSeparation(t *testing.T) {
	ctx := context.Background()
	conn := testch.Start(t)
	if err := store.ApplyClickHouseMigrations(ctx, conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := store.NewCHStats(conn)

	base := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	rows := []store.QueryStat{
		{ServerID: "srv1", CollectedAt: base, Fingerprint: "fp-t1", NormalizedQuery: "SELECT 1", DataTier: 1, Calls: 1, TotalTimeMs: 10},
		{ServerID: "srv1", CollectedAt: base, Fingerprint: "fp-t2", NormalizedQuery: "SELECT * WHERE ssn='123-45-6789'", DataTier: 2, Calls: 2, TotalTimeMs: 20},
	}
	if err := s.WriteQueryStats(ctx, rows); err != nil {
		t.Fatalf("write: %v", err)
	}

	// T1 read must NOT see the tier-2 row.
	top, err := s.TopQueriesByTotalTime(ctx, base.Add(-time.Hour), base.Add(time.Hour), 10)
	if err != nil {
		t.Fatalf("top: %v", err)
	}
	if len(top) != 1 || top[0].Fingerprint != "fp-t1" {
		t.Fatalf("T1 read leaked or missing rows: %+v", top)
	}

	// T2 read returns only the tier-2 row from query_stats_t2.
	t2, err := s.ReadQueryStatsTier2(ctx, "srv1", base.Add(-time.Hour), base.Add(time.Hour), 10)
	if err != nil {
		t.Fatalf("t2 read: %v", err)
	}
	if len(t2) != 1 || t2[0].Fingerprint != "fp-t2" || t2[0].DataTier != 2 {
		t.Fatalf("T2 read wrong: %+v", t2)
	}
}
