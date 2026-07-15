package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/dobbo-ca/lynceus/internal/testch"
)

// A T2 read must not appear in system.query_log (log_queries=0 on the T2 path).
// Deterministic on the shared container because package tests run sequentially:
// a CH-sourced baseline windows out earlier tests, and an unscrubbed control
// query proves the window/filter actually detects a `FROM query_stats_t2` SELECT.
func TestCHStats_T2Read_ScrubsQueryLog(t *testing.T) {
	ctx := context.Background()
	conn := testch.Start(t)
	if err := store.ApplyClickHouseMigrations(ctx, conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := store.NewCHStats(conn)

	when := time.Now().UTC()
	if err := s.WriteQueryStats(ctx, []store.QueryStat{{
		ServerID: "srv-scrub", CollectedAt: when, Fingerprint: "fp",
		NormalizedQuery: "SELECT * FROM t WHERE ssn = '123-45-6789'", DataTier: 2, Calls: 1, TotalTimeMs: 1,
	}}); err != nil {
		t.Fatalf("seed t2: %v", err)
	}

	baseline := func() string {
		var s string
		if err := conn.QueryRow(ctx, "SELECT toString(now64(6))").Scan(&s); err != nil {
			t.Fatalf("baseline: %v", err)
		}
		return s
	}
	t2Selects := func(since string) uint64 {
		var n uint64
		if err := conn.QueryRow(ctx, `SELECT count() FROM system.query_log
			WHERE event_time_microseconds >= parseDateTime64BestEffort(?)
			  AND type = 'QueryFinish'
			  AND positionCaseInsensitive(query, 'from query_stats_t2') > 0`, since).Scan(&n); err != nil {
			t.Fatalf("query_log count: %v", err)
		}
		return n
	}

	base := baseline()
	if _, err := s.ReadQueryStatsTier2(ctx, "srv-scrub", when.Add(-time.Hour), when.Add(time.Hour), 10); err != nil {
		t.Fatalf("t2 read: %v", err)
	}
	if err := conn.Exec(ctx, "SYSTEM FLUSH LOGS"); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if got := t2Selects(base); got != 0 {
		t.Fatalf("scrubbed T2 read leaked into query_log: %d rows", got)
	}

	// Positive control: an UNSCRUBBED SELECT FROM query_stats_t2 must be detected.
	if err := conn.Exec(ctx, "SELECT count() FROM query_stats_t2 WHERE server_id = 'srv-scrub'"); err != nil {
		t.Fatalf("control select: %v", err)
	}
	if err := conn.Exec(ctx, "SYSTEM FLUSH LOGS"); err != nil {
		t.Fatalf("flush2: %v", err)
	}
	if got := t2Selects(base); got == 0 {
		t.Fatalf("control failed: query_log window/filter detected 0 SELECTs (test is blind)")
	}
}
