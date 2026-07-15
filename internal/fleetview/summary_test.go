package fleetview_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dobbo-ca/lynceus/internal/fleetview"
	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/dobbo-ca/lynceus/internal/testch"
	"github.com/dobbo-ca/lynceus/internal/testpg"
)

// newStores spins up two backends. Config lives in Postgres (with its own
// schema_migrations); stats lives in ClickHouse (the sole stats store). The
// aggregator — which takes a Config and a Stats backed by different engines —
// is tested against that same split. The config pool is returned too, for
// seeding the servers table directly.
func newStores(t *testing.T) (store.Config, store.Stats, *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	configPool := testpg.Start(t)
	if err := store.ApplyConfigMigrations(ctx, configPool); err != nil {
		t.Fatalf("config migrate: %v", err)
	}
	conn := testch.Start(t)
	if err := store.ApplyClickHouseMigrations(ctx, conn); err != nil {
		t.Fatalf("stats migrate: %v", err)
	}
	return store.NewConfig(configPool), store.NewCHStats(conn), configPool
}

func TestListClusterSummaries_rollsUpAcrossStreams(t *testing.T) {
	cfg, stats, configPool := newStores(t)
	ctx := context.Background()

	for _, id := range []string{"srv-a", "srv-b"} {
		if _, err := configPool.Exec(ctx, `INSERT INTO servers (id, name) VALUES ($1, $1)`, id); err != nil {
			t.Fatalf("seed server %s: %v", id, err)
		}
	}
	cl, err := cfg.CreateCluster(ctx, "prod")
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}
	inst, err := cfg.CreateInstance(ctx, cl.ID, "primary")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	for _, id := range []string{"srv-a", "srv-b"} {
		if err := cfg.AssignServerToInstance(ctx, id, inst.ID); err != nil {
			t.Fatalf("assign %s: %v", id, err)
		}
	}

	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	if err := stats.WriteQueryStats(ctx, []store.QueryStat{
		{ServerID: "srv-a", CollectedAt: now, Fingerprint: "fp-1", NormalizedQuery: "SELECT $1",
			Calls: 100, TotalTimeMs: 200, MeanTimeMs: 2.0},
		{ServerID: "srv-b", CollectedAt: now, Fingerprint: "fp-2", NormalizedQuery: "SELECT $1 FROM t",
			Calls: 300, TotalTimeMs: 30, MeanTimeMs: 0.1},
	}); err != nil {
		t.Fatalf("seed stats: %v", err)
	}
	if err := stats.WriteInsights(ctx, []store.InsightRow{
		{ServerID: "srv-a", CapturedAt: now, Kind: "slow_scan", Severity: "high",
			Fingerprint: "fp-1", Relation: "orders", NodePath: "Seq Scan(orders)",
			RowsReturned: 1, RowsScanned: 100000, Selectivity: 0.00001, Detail: "x"},
	}); err != nil {
		t.Fatalf("seed insights: %v", err)
	}

	since, until := now.Add(-time.Hour), now.Add(time.Hour)
	sums, err := fleetview.ListClusterSummaries(ctx, cfg, stats, since, until)
	if err != nil {
		t.Fatalf("ListClusterSummaries: %v", err)
	}
	if len(sums) != 1 {
		t.Fatalf("summaries = %d, want 1", len(sums))
	}
	s := sums[0]
	if s.Cluster.ID != cl.ID {
		t.Fatalf("cluster id = %q, want %q", s.Cluster.ID, cl.ID)
	}
	if s.InstanceCount != 1 || s.StreamCount != 2 {
		t.Fatalf("counts: instances=%d streams=%d, want 1/2", s.InstanceCount, s.StreamCount)
	}
	if s.Calls != 400 {
		t.Fatalf("calls = %d, want 400 (combined)", s.Calls)
	}
	if s.AvgLatencyMs < 0.57 || s.AvgLatencyMs > 0.58 {
		t.Fatalf("avg latency = %v, want ~0.575", s.AvgLatencyMs)
	}
	if s.InsightCount != 1 {
		t.Fatalf("insight count = %d, want 1", s.InsightCount)
	}
}

func TestListClusterSummaries_clusterWithNoStreams(t *testing.T) {
	cfg, stats, _ := newStores(t)
	ctx := context.Background()

	cl, err := cfg.CreateCluster(ctx, "empty")
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	sums, err := fleetview.ListClusterSummaries(ctx, cfg, stats, now.Add(-time.Hour), now.Add(time.Hour))
	if err != nil {
		t.Fatalf("ListClusterSummaries: %v", err)
	}
	if len(sums) != 1 || sums[0].Cluster.ID != cl.ID || sums[0].StreamCount != 0 || sums[0].Calls != 0 {
		t.Fatalf("empty cluster summary wrong: %+v", sums)
	}
}

func TestListClusterSummaries_severityAndVersionRollup(t *testing.T) {
	cfg, stats, configPool := newStores(t)
	ctx := context.Background()

	if _, err := configPool.Exec(ctx, `INSERT INTO servers (id, name) VALUES ($1, $1)`, "srv-test"); err != nil {
		t.Fatalf("seed server: %v", err)
	}
	cl, err := cfg.CreateCluster(ctx, "orders-prod")
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}
	inst, err := cfg.CreateInstance(ctx, cl.ID, "primary")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	if err := cfg.AssignServerToInstance(ctx, "srv-test", inst.ID); err != nil {
		t.Fatalf("AssignServerToInstance: %v", err)
	}

	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	if err := stats.WriteChecksResults(ctx, []store.ChecksResultRow{
		{ServerID: "srv-test", EvaluatedAt: now, CheckID: "settings.fsync", Category: "settings",
			Severity: "critical", Status: "firing", Object: "server", DataTier: 1},
		{ServerID: "srv-test", EvaluatedAt: now, CheckID: "settings.autovacuum", Category: "settings",
			Severity: "warning", Status: "firing", Object: "server", DataTier: 1},
	}); err != nil {
		t.Fatalf("seed checks: %v", err)
	}
	// Seed the REAL collector field server_version_num (not server_version) so
	// the test exercises the production data path.
	if err := stats.WriteSettings(ctx, []store.SettingRow{
		{ServerID: "srv-test", CollectedAt: now, Name: "server_version_num", Value: "160003", DataTier: 1},
		{ServerID: "srv-test", CollectedAt: now, Name: "max_connections", Value: "200", DataTier: 1},
	}); err != nil {
		t.Fatalf("seed settings: %v", err)
	}

	sums, err := fleetview.ListClusterSummaries(ctx, cfg, stats, now.Add(-time.Hour), now.Add(time.Hour))
	if err != nil {
		t.Fatalf("summaries: %v", err)
	}
	got := sums[0]
	if got.CritOpen != 1 || got.WarnOpen != 1 {
		t.Fatalf("severity rollup = crit %d warn %d, want 1/1", got.CritOpen, got.WarnOpen)
	}
	if got.Version != "16.3" {
		t.Fatalf("version = %q, want 16.3 (derived from server_version_num=160003)", got.Version)
	}
}

func TestListNodeGroups_rollsUpNodesAndSettings(t *testing.T) {
	cfg, stats, configPool := newStores(t)
	ctx := context.Background()

	if _, err := configPool.Exec(ctx, `INSERT INTO servers (id, name) VALUES ($1, $1)`, "srv-p"); err != nil {
		t.Fatalf("seed server: %v", err)
	}
	cl, err := cfg.CreateCluster(ctx, "orders-prod")
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}
	inst, err := cfg.CreateInstance(ctx, cl.ID, "node-a")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	if _, err := configPool.Exec(ctx, `UPDATE instance SET role='primary' WHERE id=$1`, inst.ID); err != nil {
		t.Fatalf("set role: %v", err)
	}
	if err := cfg.AssignServerToInstance(ctx, "srv-p", inst.ID); err != nil {
		t.Fatalf("assign: %v", err)
	}

	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	if err := stats.WriteSettings(ctx, []store.SettingRow{
		{ServerID: "srv-p", CollectedAt: now, Name: "server_version_num", Value: "160003", DataTier: 1},
		{ServerID: "srv-p", CollectedAt: now, Name: "max_connections", Value: "200", DataTier: 1},
	}); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	if err := stats.WriteActivityBuckets(ctx, []store.ActivityBucket{
		{ServerID: "srv-p", Database: "orders", State: "active",
			BucketStart: now, BucketSeconds: 60, SampleCount: 6, CountSum: 87, CountMax: 87},
	}); err != nil {
		t.Fatalf("seed activity: %v", err)
	}

	since, until := now.Add(-time.Hour), now.Add(time.Hour)
	groups, err := fleetview.ListNodeGroups(ctx, cfg, stats, since, until)
	if err != nil {
		t.Fatalf("ListNodeGroups: %v", err)
	}
	if len(groups) != 1 || len(groups[0].Nodes) != 1 {
		t.Fatalf("groups/nodes = %d/%v, want 1/1", len(groups), len(groups))
	}
	g, n := groups[0], groups[0].Nodes[0]
	if g.Version != "16.3" {
		t.Fatalf("group version = %q, want 16.3", g.Version)
	}
	if n.Role != "PRIMARY" || n.Version != "16.3" || n.MaxConns != 200 || n.ActiveConns != 87 {
		t.Fatalf("node = role %q ver %q conns %d/%d, want PRIMARY/16.3/87/200",
			n.Role, n.Version, n.ActiveConns, n.MaxConns)
	}
}

func TestListDatabaseGroups_perDatabaseAndSkipsBlankName(t *testing.T) {
	cfg, stats, configPool := newStores(t)
	ctx := context.Background()

	cl, err := cfg.CreateCluster(ctx, "orders-prod")
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}
	inst, err := cfg.CreateInstance(ctx, cl.ID, "node-a")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	seed := func(serverID, dbName string) {
		if _, err := configPool.Exec(ctx, `INSERT INTO servers (id, name) VALUES ($1, $1)`, serverID); err != nil {
			t.Fatalf("seed server %s: %v", serverID, err)
		}
		if err := cfg.AssignServerToInstance(ctx, serverID, inst.ID); err != nil {
			t.Fatalf("assign %s: %v", serverID, err)
		}
		if dbName != "" {
			if _, err := configPool.Exec(ctx, `UPDATE servers SET database_name=$1 WHERE id=$2`, dbName, serverID); err != nil {
				t.Fatalf("set database_name: %v", err)
			}
		}
	}
	seed("srv-orders", "orders")
	seed("srv-billing", "billing")
	seed("srv-blank", "") // blank database_name — no row expected

	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	groups, err := fleetview.ListDatabaseGroups(ctx, cfg, stats, now.Add(-time.Hour), now.Add(time.Hour))
	if err != nil {
		t.Fatalf("ListDatabaseGroups: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("groups = %d, want 1", len(groups))
	}
	names := map[string]bool{}
	for _, e := range groups[0].Entries {
		names[e.Name] = true
	}
	if len(names) != 2 || !names["orders"] || !names["billing"] {
		t.Fatalf("database names = %v, want {orders,billing} (blank-name stream skipped)", names)
	}
}
