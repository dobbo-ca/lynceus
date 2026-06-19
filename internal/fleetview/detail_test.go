package fleetview_test

import (
	"context"
	"testing"
	"time"

	"github.com/dobbo-ca/lynceus/internal/fleetview"
	"github.com/dobbo-ca/lynceus/internal/store"
)

func TestGetClusterDetail_found(t *testing.T) {
	cfg, stats, configPool := newStores(t)
	ctx := context.Background()

	// Seed two servers.
	for _, id := range []string{"ovr-a", "ovr-b"} {
		if _, err := configPool.Exec(ctx, `INSERT INTO servers (id, name) VALUES ($1, $1)`, id); err != nil {
			t.Fatalf("seed server %s: %v", id, err)
		}
	}

	cl, err := cfg.CreateCluster(ctx, "overview-cluster")
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}
	inst, err := cfg.CreateInstance(ctx, cl.ID, "primary")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	for _, id := range []string{"ovr-a", "ovr-b"} {
		if err := cfg.AssignServerToInstance(ctx, id, inst.ID); err != nil {
			t.Fatalf("assign %s: %v", id, err)
		}
	}

	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	if err := stats.WriteQueryStats(ctx, []store.QueryStat{
		{ServerID: "ovr-a", CollectedAt: now, Fingerprint: "fp-x", NormalizedQuery: "SELECT $1",
			Calls: 200, TotalTimeMs: 400, MeanTimeMs: 2.0},
		{ServerID: "ovr-b", CollectedAt: now, Fingerprint: "fp-y", NormalizedQuery: "SELECT $1 FROM t",
			Calls: 50, TotalTimeMs: 500, MeanTimeMs: 10.0},
	}); err != nil {
		t.Fatalf("seed stats: %v", err)
	}
	if err := stats.WriteInsights(ctx, []store.InsightRow{
		{ServerID: "ovr-a", CapturedAt: now, Kind: "slow_scan", Severity: "high",
			Fingerprint: "fp-x", Relation: "orders", NodePath: "Seq Scan(orders)",
			RowsReturned: 1, RowsScanned: 100000, Selectivity: 0.00001, Detail: "detail"},
	}); err != nil {
		t.Fatalf("seed insights: %v", err)
	}

	since, until := now.Add(-time.Hour), now.Add(time.Hour)
	detail, found, err := fleetview.GetClusterDetail(ctx, cfg, stats, cl.ID, since, until)
	if err != nil {
		t.Fatalf("GetClusterDetail: %v", err)
	}
	if !found {
		t.Fatal("found = false, want true")
	}
	if detail.Cluster.ID != cl.ID {
		t.Fatalf("cluster id = %q, want %q", detail.Cluster.ID, cl.ID)
	}
	if detail.StreamCount != 2 {
		t.Fatalf("StreamCount = %d, want 2", detail.StreamCount)
	}
	if detail.Calls != 250 {
		t.Fatalf("Calls = %d, want 250", detail.Calls)
	}
	if len(detail.Instances) != 1 {
		t.Fatalf("Instances len = %d, want 1", len(detail.Instances))
	}
	if detail.Instances[0].Calls != 250 {
		t.Fatalf("instance Calls = %d, want 250", detail.Instances[0].Calls)
	}
	if len(detail.TopQueries) == 0 {
		t.Fatal("TopQueries empty, want at least one row")
	}
	if detail.InsightCount != 1 {
		t.Fatalf("InsightCount = %d, want 1", detail.InsightCount)
	}
}

func TestGetClusterDetail_notFound(t *testing.T) {
	cfg, stats, _ := newStores(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	_, found, err := fleetview.GetClusterDetail(ctx, cfg, stats, "does-not-exist", now.Add(-time.Hour), now.Add(time.Hour))
	if err != nil {
		t.Fatalf("GetClusterDetail: %v", err)
	}
	if found {
		t.Fatal("found = true for unknown clusterID, want false")
	}
}
