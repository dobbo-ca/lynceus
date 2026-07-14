package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/dobbo-ca/lynceus/internal/testch"
)

// activityStart boots a ClickHouse container, applies the CH migrations, and
// returns the raw conn plus a chStats. The raw conn lets RecentServerIDs's test
// stand up the table_stats table (owned by another domain's migration).
func activityStart(t *testing.T) (context.Context, driver.Conn, store.Stats) {
	t.Helper()
	ctx := context.Background()
	conn := testch.Start(t)
	if err := store.ApplyClickHouseMigrations(ctx, conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return ctx, conn, store.NewCHStats(conn)
}

func TestCH_activity_WaitEventHistogram_RoundTrip(t *testing.T) {
	ctx, _, s := activityStart(t)

	base := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	buckets := []store.ActivityBucket{
		// Active-on-CPU (empty wait labels) — must survive as its own row.
		{ServerID: "srv1", Database: "db", State: "active", BucketStart: base, BucketSeconds: 60, SampleCount: 10, CountSum: 5, CountMax: 3, DataTier: 1},
		// IO/DataFileRead across two buckets → total 8+2, buckets=2.
		{ServerID: "srv1", Database: "db", State: "active", WaitEventType: "IO", WaitEvent: "DataFileRead", BucketStart: base, BucketSeconds: 60, SampleCount: 10, CountSum: 8, CountMax: 4, DataTier: 1},
		{ServerID: "srv1", Database: "db", State: "active", WaitEventType: "IO", WaitEvent: "DataFileRead", BucketStart: base.Add(time.Minute), BucketSeconds: 60, SampleCount: 10, CountSum: 2, CountMax: 1, DataTier: 1},
		{ServerID: "srv1", Database: "db", State: "active", WaitEventType: "Lock", WaitEvent: "tuple", BucketStart: base, BucketSeconds: 60, SampleCount: 10, CountSum: 4, CountMax: 2, DataTier: 1},
		// Tier-2 noise must be excluded from the T1 read.
		{ServerID: "srv1", Database: "db", State: "active", WaitEventType: "IO", WaitEvent: "DataFileRead", BucketStart: base, BucketSeconds: 60, SampleCount: 10, CountSum: 100, CountMax: 50, DataTier: 2},
		// Different server must be excluded.
		{ServerID: "srv2", Database: "db", State: "active", WaitEventType: "IO", WaitEvent: "DataFileRead", BucketStart: base, BucketSeconds: 60, SampleCount: 10, CountSum: 50, CountMax: 20, DataTier: 1},
	}
	if err := s.WriteActivityBuckets(ctx, buckets); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := s.WaitEventHistogram(ctx, "srv1", base.Add(-time.Hour), base.Add(time.Hour))
	if err != nil {
		t.Fatalf("histogram: %v", err)
	}
	// Expected, busiest first: IO/DataFileRead=10 (2 buckets), ""/""=5, Lock/tuple=4.
	if len(got) != 3 {
		t.Fatalf("want 3 rows, got %d: %+v", len(got), got)
	}
	if got[0].WaitEventType != "IO" || got[0].WaitEvent != "DataFileRead" || got[0].Total != 10 || got[0].Buckets != 2 {
		t.Errorf("row0 = %+v, want IO/DataFileRead total=10 buckets=2", got[0])
	}
	if got[1].WaitEventType != "" || got[1].WaitEvent != "" || got[1].Total != 5 || got[1].Buckets != 1 {
		t.Errorf("row1 = %+v, want empty/empty total=5 buckets=1", got[1])
	}
	if got[2].WaitEventType != "Lock" || got[2].WaitEvent != "tuple" || got[2].Total != 4 {
		t.Errorf("row2 = %+v, want Lock/tuple total=4", got[2])
	}
}

func TestCH_activity_RollupForServers_RoundTrip(t *testing.T) {
	ctx, _, s := activityStart(t)

	base := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	qs := []store.QueryStat{
		{ServerID: "srv1", CollectedAt: base, Fingerprint: "fp-a", NormalizedQuery: "SELECT a", DataTier: 1, Calls: 10, TotalTimeMs: 100},
		{ServerID: "srv1", CollectedAt: base.Add(time.Minute), Fingerprint: "fp-a", NormalizedQuery: "SELECT a", DataTier: 1, Calls: 20, TotalTimeMs: 300},
		{ServerID: "srv1", CollectedAt: base, Fingerprint: "fp-b", NormalizedQuery: "SELECT b", DataTier: 1, Calls: 5, TotalTimeMs: 500},
		{ServerID: "srv2", CollectedAt: base, Fingerprint: "fp-c", NormalizedQuery: "SELECT c", DataTier: 1, Calls: 7, TotalTimeMs: 70},
		// Tier-2 noise on srv1 must be excluded from every T1 read.
		{ServerID: "srv1", CollectedAt: base, Fingerprint: "fp-secret", NormalizedQuery: "SELECT secret", DataTier: 2, Calls: 999, TotalTimeMs: 9999},
	}
	if err := s.WriteQueryStats(ctx, qs); err != nil {
		t.Fatalf("write: %v", err)
	}

	from, to := base.Add(-time.Hour), base.Add(time.Hour)

	// Throughput scoped to srv1: calls 35, total 900 (tier-2 + srv2 excluded).
	tp, err := s.ThroughputForServers(ctx, []string{"srv1"}, from, to)
	if err != nil {
		t.Fatalf("throughput: %v", err)
	}
	if tp.Calls != 35 || tp.TotalTimeMs != 900 {
		t.Errorf("throughput = %+v, want calls=35 total=900", tp)
	}

	// Multi-server IN binding: srv1+srv2 → calls 35+7=42.
	tp2, err := s.ThroughputForServers(ctx, []string{"srv1", "srv2"}, from, to)
	if err != nil {
		t.Fatalf("throughput2: %v", err)
	}
	if tp2.Calls != 42 {
		t.Errorf("throughput2 calls = %d, want 42", tp2.Calls)
	}

	// TopQueries scoped to srv1: fp-b (500) before fp-a (400).
	top, err := s.TopQueriesForServers(ctx, []string{"srv1"}, from, to, 10)
	if err != nil {
		t.Fatalf("top: %v", err)
	}
	if len(top) != 2 {
		t.Fatalf("want 2 top rows, got %d: %+v", len(top), top)
	}
	if top[0].Fingerprint != "fp-b" || top[0].TotalTimeMs != 500 || top[0].Calls != 5 {
		t.Errorf("top0 = %+v, want fp-b total=500 calls=5", top[0])
	}
	if top[1].Fingerprint != "fp-a" || top[1].TotalTimeMs != 400 || top[1].Calls != 30 {
		t.Errorf("top1 = %+v, want fp-a total=400 calls=30", top[1])
	}

	// QPS buckets scoped to srv1: one hourly bucket (12:00), summed calls 35.
	qps, err := s.QPSBucketsForServers(ctx, []string{"srv1"}, from, to)
	if err != nil {
		t.Fatalf("qps: %v", err)
	}
	if len(qps) != 1 {
		t.Fatalf("want 1 qps bucket, got %d: %+v", len(qps), qps)
	}
	if !qps[0].BucketStart.Equal(base) || qps[0].Calls != 35 {
		t.Errorf("qps0 = %+v, want bucket=%v calls=35", qps[0], base)
	}
}

func TestCH_activity_ActivitySummaryForServers(t *testing.T) {
	ctx, _, s := activityStart(t)

	base := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	newest := base.Add(5 * time.Minute)
	buckets := []store.ActivityBucket{
		// Older bucket (12:00).
		{ServerID: "srv1", Database: "db", State: "active", BucketStart: base, CountSum: 3, CountMax: 2, DataTier: 1},
		{ServerID: "srv1", Database: "db", State: "active", WaitEventType: "IO", WaitEvent: "DataFileRead", BucketStart: base, CountSum: 5, CountMax: 1, DataTier: 1},
		// Newest bucket (12:05) — the peak window: active count_max 4+3=7.
		{ServerID: "srv1", Database: "db", State: "active", BucketStart: newest, CountSum: 1, CountMax: 4, DataTier: 1},
		{ServerID: "srv1", Database: "db", State: "active", WaitEventType: "Lock", WaitEvent: "tuple", BucketStart: newest, CountSum: 9, CountMax: 3, DataTier: 1},
		// Idle at newest bucket must NOT count toward the active peak.
		{ServerID: "srv1", Database: "db", State: "idle", BucketStart: newest, CountSum: 0, CountMax: 7, DataTier: 1},
		// Tier-2 noise excluded.
		{ServerID: "srv1", Database: "db", State: "active", WaitEventType: "IO", WaitEvent: "x", BucketStart: newest, CountSum: 100, CountMax: 999, DataTier: 2},
	}
	if err := s.WriteActivityBuckets(ctx, buckets); err != nil {
		t.Fatalf("write: %v", err)
	}

	sum, err := s.ActivitySummaryForServers(ctx, []string{"srv1"}, base.Add(-time.Hour), base.Add(time.Hour))
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	// Peak active in newest bucket: 4+3=7. Dominant wait over window: tuple
	// (count_sum 9) beats DataFileRead (5).
	if sum.ActiveConns != 7 {
		t.Errorf("active conns = %d, want 7", sum.ActiveConns)
	}
	if sum.TopWait != "tuple" {
		t.Errorf("top wait = %q, want tuple", sum.TopWait)
	}
}

func TestCH_activity_RecentServerIDs(t *testing.T) {
	ctx, conn, s := activityStart(t)

	// table_stats is owned by another domain's CH migration; stand up the
	// columns RecentServerIDs reads so this test is self-contained. In the
	// integrated tree the real migration creates it first and this is a no-op.
	if err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS table_stats (
		server_id String,
		collected_at DateTime64(3, 'UTC'),
		data_tier Int16 DEFAULT 1
	) ENGINE = MergeTree ORDER BY (server_id, collected_at)`); err != nil {
		t.Fatalf("create table_stats: %v", err)
	}

	base := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	batch, err := conn.PrepareBatch(ctx, "INSERT INTO table_stats (server_id, collected_at, data_tier)")
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	rows := []struct {
		id   string
		at   time.Time
		tier int16
	}{
		{"srv-a", base, 1},                       // included
		{"srv-b", base, 1},                       // included
		{"srv-a", base.Add(time.Minute), 1},      // dup id, still one
		{"srv-old", base.Add(-2 * time.Hour), 1}, // before since → excluded
		{"srv-t2", base, 2},                      // tier 2 → excluded
	}
	for _, r := range rows {
		if err := batch.Append(r.id, r.at, r.tier); err != nil {
			_ = batch.Abort()
			t.Fatalf("append: %v", err)
		}
	}
	if err := batch.Send(); err != nil {
		t.Fatalf("send: %v", err)
	}

	got, err := s.RecentServerIDs(ctx, base.Add(-time.Hour))
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	want := []string{"srv-a", "srv-b"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}
