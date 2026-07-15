package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/dobbo-ca/lynceus/internal/testch"
)

func TestOpenStats_RequiresBackend(t *testing.T) {
	t.Setenv("LYNCEUS_STATS_BACKEND", "")
	if _, err := store.OpenStats(context.Background()); err == nil {
		t.Fatal("want error when LYNCEUS_STATS_BACKEND is unset")
	}
}

func TestOpenStats_UnknownBackend(t *testing.T) {
	t.Setenv("LYNCEUS_STATS_BACKEND", "mysql")
	if _, err := store.OpenStats(context.Background()); err == nil {
		t.Fatal("want error for unknown backend value")
	}
}

func TestOpenStats_ClickHouse(t *testing.T) {
	ctx := context.Background()
	_, dsn := testch.StartDSN(t)
	t.Setenv("LYNCEUS_STATS_BACKEND", "clickhouse")
	t.Setenv("LYNCEUS_CLICKHOUSE_DSN", dsn)

	s, err := store.OpenStats(ctx)
	if err != nil {
		t.Fatalf("OpenStats(clickhouse): %v", err)
	}
	assertStatsRoundTrip(t, s)
}

// assertStatsRoundTrip proves the returned backend is wired and functional:
// a written T1 row is read back by TopQueriesByTotalTime.
func assertStatsRoundTrip(t *testing.T, s store.Stats) {
	t.Helper()
	ctx := context.Background()
	base := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	if err := s.WriteQueryStats(ctx, []store.QueryStat{
		{ServerID: "s", CollectedAt: base, Fingerprint: "fp", NormalizedQuery: "SELECT 1", DataTier: 1, Calls: 1, TotalTimeMs: 9},
	}); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := s.TopQueriesByTotalTime(ctx, base.Add(-time.Hour), base.Add(time.Hour), 10)
	if err != nil {
		t.Fatalf("top: %v", err)
	}
	if len(got) != 1 || got[0].Fingerprint != "fp" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
}
