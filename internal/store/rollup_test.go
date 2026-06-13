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

func TestActivitySummaryForServers(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyStatsMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := store.NewStats(pool)

	t0 := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Minute) // a later bucket
	buckets := []store.ActivityBucket{
		{ServerID: "srv-a", Database: "app", State: "active", WaitEventType: "", WaitEvent: "",
			BucketStart: t0, BucketSeconds: 60, SampleCount: 6, CountSum: 30, CountMax: 5},
		{ServerID: "srv-a", Database: "app", State: "active", WaitEventType: "", WaitEvent: "",
			BucketStart: t1, BucketSeconds: 60, SampleCount: 6, CountSum: 48, CountMax: 8},
		{ServerID: "srv-b", Database: "app", State: "active", WaitEventType: "", WaitEvent: "",
			BucketStart: t1, BucketSeconds: 60, SampleCount: 6, CountSum: 24, CountMax: 4},
		{ServerID: "srv-a", Database: "app", State: "active", WaitEventType: "IO", WaitEvent: "DataFileRead",
			BucketStart: t1, BucketSeconds: 60, SampleCount: 6, CountSum: 40, CountMax: 7},
		{ServerID: "srv-other", Database: "app", State: "active", WaitEventType: "", WaitEvent: "",
			BucketStart: t1, BucketSeconds: 60, SampleCount: 6, CountSum: 600, CountMax: 99},
	}
	if err := s.WriteActivityBuckets(ctx, buckets); err != nil {
		t.Fatalf("seed activity: %v", err)
	}

	set := []string{"srv-a", "srv-b"}
	since, until := t0.Add(-time.Hour), t1.Add(time.Hour)
	a, err := s.ActivitySummaryForServers(ctx, set, since, until)
	if err != nil {
		t.Fatalf("ActivitySummaryForServers: %v", err)
	}
	// newest bucket is t1; active CountMax summed over the set = 8 + 4 + 7 = 19.
	if a.ActiveConns != 19 {
		t.Fatalf("active conns = %d, want 19", a.ActiveConns)
	}
	if a.TopWait != "DataFileRead" {
		t.Fatalf("top wait = %q, want DataFileRead", a.TopWait)
	}

	a2, err := s.ActivitySummaryForServers(ctx, []string{"srv-b"}, since, until)
	if err != nil {
		t.Fatalf("ActivitySummaryForServers srv-b: %v", err)
	}
	if a2.TopWait != "" {
		t.Fatalf("srv-b top wait = %q, want empty", a2.TopWait)
	}
}
