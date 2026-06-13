package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/dobbo-ca/lynceus/internal/store"
)

func seedQueryStats(t *testing.T, ctx context.Context, s *store.Stats, now time.Time) {
	t.Helper()
	rows := []store.QueryStat{
		{ServerID: "srv-a", CollectedAt: now, Fingerprint: "fp-1", NormalizedQuery: "SELECT $1",
			Calls: 100, TotalTimeMs: 200, MeanTimeMs: 2.0},
		{ServerID: "srv-b", CollectedAt: now, Fingerprint: "fp-2", NormalizedQuery: "SELECT $1 FROM t",
			Calls: 300, TotalTimeMs: 30, MeanTimeMs: 0.1},
		{ServerID: "srv-other", CollectedAt: now, Fingerprint: "fp-x", NormalizedQuery: "SELECT 9",
			Calls: 999, TotalTimeMs: 9999, MeanTimeMs: 10},
	}
	if err := s.WriteQueryStats(ctx, rows); err != nil {
		t.Fatalf("seed query_stats: %v", err)
	}
}

func TestQueryReadsForServers_scopeAndAggregate(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyStatsMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := store.NewStats(pool)
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	seedQueryStats(t, ctx, s, now)

	set := []string{"srv-a", "srv-b"} // excludes srv-other
	since, until := now.Add(-time.Hour), now.Add(time.Hour)

	tp, err := s.ThroughputForServers(ctx, set, since, until)
	if err != nil {
		t.Fatalf("ThroughputForServers: %v", err)
	}
	if tp.Calls != 400 || tp.TotalTimeMs != 230 {
		t.Fatalf("throughput = %+v, want calls=400 total=230", tp)
	}

	top, err := s.TopQueriesForServers(ctx, set, since, until, 10)
	if err != nil {
		t.Fatalf("TopQueriesForServers: %v", err)
	}
	if len(top) != 2 || top[0].Fingerprint != "fp-1" {
		t.Fatalf("top = %+v, want 2 rows, fp-1 first", top)
	}

	buckets, err := s.QPSBucketsForServers(ctx, set, since, until)
	if err != nil {
		t.Fatalf("QPSBucketsForServers: %v", err)
	}
	var total int64
	for _, b := range buckets {
		total += b.Calls
	}
	if total != 400 {
		t.Fatalf("qps buckets total calls = %d, want 400", total)
	}

	tp0, err := s.ThroughputForServers(ctx, []string{}, since, until)
	if err != nil || tp0.Calls != 0 {
		t.Fatalf("empty-set throughput = %+v err=%v, want 0", tp0, err)
	}
}
