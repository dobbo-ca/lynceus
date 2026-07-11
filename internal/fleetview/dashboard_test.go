package fleetview_test

import (
	"context"
	"testing"
	"time"

	"github.com/dobbo-ca/lynceus/internal/fleetview"
	"github.com/dobbo-ca/lynceus/internal/store"
)

func TestBuildFleetView_rollsUpSeverityHealthAndAttention(t *testing.T) {
	cfg, stats, configPool := newStores(t)
	ctx := context.Background()

	// two servers under one cluster/instance
	for _, id := range []string{"fv-srv-a", "fv-srv-b"} {
		if _, err := configPool.Exec(ctx, `INSERT INTO servers (id, name) VALUES ($1, $1)`, id); err != nil {
			t.Fatalf("seed server %s: %v", id, err)
		}
	}
	cl, err := cfg.CreateCluster(ctx, "orders-prod")
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}
	inst, err := cfg.CreateInstance(ctx, cl.ID, "srv-orders-primary")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	for _, id := range []string{"fv-srv-a", "fv-srv-b"} {
		if err := cfg.AssignServerToInstance(ctx, id, inst.ID); err != nil {
			t.Fatalf("assign %s: %v", id, err)
		}
	}

	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	// one firing critical check + one firing warning check (+ a muted one that must be ignored)
	if err := stats.WriteChecksResults(ctx, []store.ChecksResultRow{
		{ServerID: "fv-srv-a", EvaluatedAt: now.Add(-2 * time.Hour), CheckID: "settings.fsync",
			Category: "settings", Severity: "critical", Status: "firing", Object: "fsync",
			Detail: "fsync = off — a crash can lose committed transactions"},
		{ServerID: "fv-srv-a", EvaluatedAt: now.Add(-3 * time.Hour), CheckID: "vacuum.xmin_horizon",
			Category: "vacuum", Severity: "warning", Status: "firing", Object: "orders",
			Detail: "oldest xmin age 260M exceeds 200M"},
		{ServerID: "fv-srv-b", EvaluatedAt: now.Add(-1 * time.Hour), CheckID: "settings.work_mem",
			Category: "settings", Severity: "warning", Status: "firing", Object: "work_mem",
			Detail: "muted noise", Muted: true},
	}); err != nil {
		t.Fatalf("seed checks: %v", err)
	}
	// one high insight
	if err := stats.WriteInsights(ctx, []store.InsightRow{
		{ServerID: "fv-srv-a", CapturedAt: now.Add(-4 * time.Hour), Kind: "slow_scan", Severity: "high",
			Fingerprint: "f41b7d09", Relation: "orders_audit", NodePath: "Seq Scan(orders_audit)",
			RowsReturned: 1, RowsScanned: 1200000, Selectivity: 0.0000008,
			Detail: "Seq Scan on orders_audit reads 1.2M rows to return 1"},
	}); err != nil {
		t.Fatalf("seed insights: %v", err)
	}

	fv, err := fleetview.BuildFleetView(ctx, cfg, stats, now.Add(-24*time.Hour), now.Add(time.Minute))
	if err != nil {
		t.Fatalf("BuildFleetView: %v", err)
	}

	if fv.Healthy {
		t.Error("fleet has open issues; Healthy must be false")
	}
	// crit = critical check (1) + high insight (1); warn = warning check (1); muted excluded.
	if fv.OpenCrit != 2 || fv.OpenWarn != 1 || fv.OpenInfo != 0 {
		t.Fatalf("fleet totals = %d/%d/%d, want 2/1/0", fv.OpenCrit, fv.OpenWarn, fv.OpenInfo)
	}
	if fv.ClusterCount != 1 || fv.NodeCount != 1 || fv.DatabaseCount != 2 {
		t.Fatalf("counts clusters/nodes/dbs = %d/%d/%d, want 1/1/2", fv.ClusterCount, fv.NodeCount, fv.DatabaseCount)
	}
	if len(fv.Clusters) != 1 {
		t.Fatalf("clusters = %d, want 1", len(fv.Clusters))
	}
	c := fv.Clusters[0]
	if c.Crit != 2 || c.Warn != 1 || c.Info != 0 {
		t.Fatalf("cluster counts = %d/%d/%d, want 2/1/0", c.Crit, c.Warn, c.Info)
	}
	if c.Health != "DEGRADED" || c.HealthSev != fleetview.SevCrit {
		t.Fatalf("health = %q/%v, want DEGRADED/crit", c.Health, c.HealthSev)
	}
	if c.Engine != "POSTGRESQL" || c.EngineIcon != "eng-pg" {
		t.Fatalf("engine = %q/%q, want POSTGRESQL/eng-pg", c.Engine, c.EngineIcon)
	}
	// Needs-Attention: 3 items (muted excluded), sorted crit first then newest.
	if len(fv.Attention) != 3 {
		t.Fatalf("attention = %d, want 3", len(fv.Attention))
	}
	if fv.Attention[0].Sev != fleetview.SevCrit || fv.Attention[0].ID != "settings.fsync" {
		t.Fatalf("first attention = %+v, want crit settings.fsync", fv.Attention[0])
	}
	if fv.Attention[0].ServerName != "srv-orders-primary" {
		t.Fatalf("server name = %q, want srv-orders-primary", fv.Attention[0].ServerName)
	}
	// the insight is also crit-band and must carry its fingerprint + kind id.
	var sawInsight bool
	for _, a := range fv.Attention {
		if a.Kind == "insight" {
			sawInsight = true
			if a.ID != "insight: slow_scan" || a.Fingerprint != "f41b7d09" || a.ClusterID != cl.ID {
				t.Fatalf("insight item wrong: %+v", a)
			}
		}
	}
	if !sawInsight {
		t.Error("insight not surfaced in Needs-Attention")
	}
}

func TestBuildFleetView_healthyWhenNoOpenIssues(t *testing.T) {
	cfg, stats, configPool := newStores(t)
	ctx := context.Background()
	if _, err := configPool.Exec(ctx, `INSERT INTO servers (id, name) VALUES ('hv-srv','hv-srv')`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cl, err := cfg.CreateCluster(ctx, "quiet")
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}
	inst, _ := cfg.CreateInstance(ctx, cl.ID, "primary")
	if err := cfg.AssignServerToInstance(ctx, "hv-srv", inst.ID); err != nil {
		t.Fatalf("assign: %v", err)
	}
	now := time.Now().UTC()
	fv, err := fleetview.BuildFleetView(ctx, cfg, stats, now.Add(-24*time.Hour), now.Add(time.Minute))
	if err != nil {
		t.Fatalf("BuildFleetView: %v", err)
	}
	if !fv.Healthy || len(fv.Attention) != 0 {
		t.Fatalf("no open issues -> Healthy=true, empty attention; got healthy=%v n=%d", fv.Healthy, len(fv.Attention))
	}
	if fv.OpenCrit+fv.OpenWarn+fv.OpenInfo != 0 {
		t.Fatalf("healthy fleet must zero all severity totals")
	}
	if len(fv.Clusters) != 1 || fv.Clusters[0].Health != "HEALTHY" || fv.Clusters[0].HealthSev != fleetview.SevInfo {
		t.Fatalf("healthy cluster mislabeled: %+v", fv.Clusters)
	}
}
