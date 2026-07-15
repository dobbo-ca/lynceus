package fleetview_test

import (
	"context"
	"testing"
	"time"

	"github.com/dobbo-ca/lynceus/internal/fleetview"
	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/dobbo-ca/lynceus/internal/testch"
)

func TestScopeIssues_firingChecksAndInsights(t *testing.T) {
	ctx := context.Background()
	conn := testch.Start(t)
	if err := store.ApplyClickHouseMigrations(ctx, conn); err != nil {
		t.Fatalf("stats migrate: %v", err)
	}
	stats := store.NewCHStats(conn)
	now := time.Now().UTC()
	since := now.AddDate(0, 0, -1)

	if err := stats.WriteChecksResults(ctx, []store.ChecksResultRow{
		{ServerID: "n1", EvaluatedAt: now.Add(-10 * time.Minute), CheckID: "settings.fsync",
			Category: "settings", Severity: "critical", Status: "firing", Object: "fsync", Detail: "fsync is off"},
		{ServerID: "n1", EvaluatedAt: now.Add(-10 * time.Minute), CheckID: "settings.muted",
			Category: "settings", Severity: "warning", Status: "firing", Object: "x", Detail: "muted one", Muted: true},
		{ServerID: "n1", EvaluatedAt: now.Add(-10 * time.Minute), CheckID: "settings.ok",
			Category: "settings", Severity: "info", Status: "ok", Object: "y", Detail: "all good"},
	}); err != nil {
		t.Fatalf("write checks: %v", err)
	}
	if err := stats.WriteInsights(ctx, []store.InsightRow{
		{ServerID: "n1", CapturedAt: now.Add(-5 * time.Minute), Kind: "slow_scan", Severity: "medium",
			Fingerprint: "fp-abc", Relation: "orders", Detail: "seq scan on orders"},
	}); err != nil {
		t.Fatalf("write insights: %v", err)
	}

	got, err := fleetview.ScopeIssues(ctx, stats, []string{"n1"}, since, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("ScopeIssues: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 issues (firing check + insight), got %d: %+v", len(got), got)
	}
	if got[0].Kind != "check" || got[0].Severity != "crit" || got[0].ID != "settings.fsync" {
		t.Errorf("issue[0] = %+v; want crit check settings.fsync first", got[0])
	}
	if got[1].Kind != "insight" || got[1].Severity != "warn" || got[1].Ref != "fp-abc" {
		t.Errorf("issue[1] = %+v; want warn insight fp-abc", got[1])
	}
	if fleetview.WorstSeverity(got) != "crit" {
		t.Errorf("WorstSeverity = %q, want crit", fleetview.WorstSeverity(got))
	}
}
