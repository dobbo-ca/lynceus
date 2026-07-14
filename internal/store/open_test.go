package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/dobbo-ca/lynceus/internal/testch"
	"github.com/dobbo-ca/lynceus/internal/testpg"
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

func TestOpenStats_Postgres(t *testing.T) {
	ctx := context.Background()
	dsn := startPG(t)
	t.Setenv("LYNCEUS_REQUIRE_TLS", "false") // plaintext testcontainer DSN
	t.Setenv("LYNCEUS_STATS_BACKEND", "postgres")
	t.Setenv("LYNCEUS_STATS_DSN", dsn)
	t.Setenv("LYNCEUS_STATS_RO_DSN", "")

	s, err := store.OpenStats(ctx)
	if err != nil {
		t.Fatalf("OpenStats(postgres): %v", err)
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

func startPG(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	c, err := tcpostgres.Run(ctx, "postgres:16",
		tcpostgres.WithDatabase("lynceus_test"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		testpg.ReadyWait(),
	)
	if err != nil {
		t.Skipf("docker/testcontainers unavailable: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(c) })
	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	return dsn
}
