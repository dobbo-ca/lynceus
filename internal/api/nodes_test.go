package api_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dobbo-ca/lynceus/internal/store"
)

func setupNodes(t *testing.T) *httptest.Server {
	t.Helper()
	ctx := context.Background()
	srv, cfg, stats, configPool := newVerticalFleet(t)
	// Seed an hour back so rows land strictly inside the handler's
	// [now-1d, now) window (exclusive upper bound); seeding at exactly now
	// races the request instant under the stats store's timestamp precision.
	now := time.Now().UTC().Add(-time.Hour)

	// --- orders-prod (healthy, rich metrics) ---
	for _, id := range []string{"srv-orders-primary", "srv-orders-replica"} {
		if _, err := configPool.Exec(ctx, `INSERT INTO servers (id, name) VALUES ($1, $1)`, id); err != nil {
			t.Fatalf("seed server %s: %v", id, err)
		}
	}
	cl, err := cfg.CreateCluster(ctx, "orders-prod")
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}
	primary, err := cfg.CreateInstance(ctx, cl.ID, "srv-orders-primary")
	if err != nil {
		t.Fatalf("CreateInstance primary: %v", err)
	}
	replica, err := cfg.CreateInstance(ctx, cl.ID, "srv-orders-replica")
	if err != nil {
		t.Fatalf("CreateInstance replica: %v", err)
	}
	// CreateInstance defaults role to 'unknown'; set the real roles directly.
	if _, err := configPool.Exec(ctx, `UPDATE instance SET role='primary' WHERE id=$1`, primary.ID); err != nil {
		t.Fatalf("set primary role: %v", err)
	}
	if _, err := configPool.Exec(ctx, `UPDATE instance SET role='replica' WHERE id=$1`, replica.ID); err != nil {
		t.Fatalf("set replica role: %v", err)
	}
	if err := cfg.AssignServerToInstance(ctx, "srv-orders-primary", primary.ID); err != nil {
		t.Fatalf("assign primary: %v", err)
	}
	if err := cfg.AssignServerToInstance(ctx, "srv-orders-replica", replica.ID); err != nil {
		t.Fatalf("assign replica: %v", err)
	}
	if err := stats.WriteSettings(ctx, []store.SettingRow{
		{ServerID: "srv-orders-primary", CollectedAt: now, Name: "server_version_num", Value: "160003", DataTier: 1},
		{ServerID: "srv-orders-primary", CollectedAt: now, Name: "max_connections", Value: "200", DataTier: 1},
	}); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	if err := stats.WriteActivityBuckets(ctx, []store.ActivityBucket{
		{ServerID: "srv-orders-primary", Database: "orders", State: "active",
			BucketStart: now, BucketSeconds: 60, SampleCount: 6, CountSum: 87, CountMax: 87},
	}); err != nil {
		t.Fatalf("seed activity: %v", err)
	}

	// --- two degraded clusters with distinct severities ---
	seedDegraded := func(clusterName, serverID, severity string) {
		if _, err := configPool.Exec(ctx, `INSERT INTO servers (id, name) VALUES ($1, $1)`, serverID); err != nil {
			t.Fatalf("seed server %s: %v", serverID, err)
		}
		dcl, err := cfg.CreateCluster(ctx, clusterName)
		if err != nil {
			t.Fatalf("CreateCluster %s: %v", clusterName, err)
		}
		dinst, err := cfg.CreateInstance(ctx, dcl.ID, serverID)
		if err != nil {
			t.Fatalf("CreateInstance %s: %v", clusterName, err)
		}
		if err := cfg.AssignServerToInstance(ctx, serverID, dinst.ID); err != nil {
			t.Fatalf("assign %s: %v", serverID, err)
		}
		if err := stats.WriteChecksResults(ctx, []store.ChecksResultRow{{
			ServerID: serverID, EvaluatedAt: now, CheckID: "settings.fsync",
			Category: "settings", Severity: severity, Status: "firing", Object: "server", DataTier: 1,
		}}); err != nil {
			t.Fatalf("seed check %s: %v", clusterName, err)
		}
	}
	seedDegraded("zzz-payments", "srv-payments", "critical") // SevRank 2
	seedDegraded("mmm-billing", "srv-billing", "warning")    // SevRank 1
	return srv
}

func TestNodes_GroupRowsSearchAndHealthSort(t *testing.T) {
	srv := setupNodes(t)
	html := getBody(t, srv.URL+"/nodes") // default sort=health

	// Row anatomy on the healthy cluster (full page, inside the shell).
	for _, want := range []string{
		`id="nodes-screen"`, `class="topbar"`, "orders-prod", "v16.3",
		"NODE HEALTH", "role-primary", "role-replica",
		"87 / 200", // conns / max_connections
	} {
		if !strings.Contains(html, want) {
			t.Errorf("nodes page missing %q", want)
		}
	}

	// HEALTH sort (default): crit → warn → clean. Assert on the body-only partial
	// so the top-bar scope picker's name-ordered cluster list can't dominate the
	// index search. Three groups reliably expose an index-off-by-one sort bug.
	frag := getBody(t, srv.URL+"/partial/nodes")
	iCrit := strings.Index(frag, "zzz-payments")
	iWarn := strings.Index(frag, "mmm-billing")
	iClean := strings.Index(frag, "orders-prod")
	if !(iCrit < iWarn && iWarn < iClean) {
		t.Fatalf("HEALTH sort order wrong: crit@%d warn@%d clean@%d (want crit<warn<clean)", iCrit, iWarn, iClean)
	}

	// Empty search state.
	miss := getBody(t, srv.URL+"/nodes?q=no-such-cluster")
	if !strings.Contains(miss, "NO CLUSTERS OR NODES MATCH") {
		t.Fatal("empty-search state missing")
	}
}
