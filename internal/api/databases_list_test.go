package api_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dobbo-ca/lynceus/internal/store"
)

func setupDatabasesScreen(t *testing.T) *httptest.Server {
	t.Helper()
	ctx := context.Background()
	srv, cfg, stats, configPool := newVerticalFleet(t)
	now := time.Now().UTC()

	cl, err := cfg.CreateCluster(ctx, "orders-prod")
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}
	inst, err := cfg.CreateInstance(ctx, cl.ID, "node-a")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	for _, d := range []struct{ serverID, dbName string }{
		{"srv-orders", "orders"},
		{"srv-billing", "billing"},
	} {
		if _, err := configPool.Exec(ctx, `INSERT INTO servers (id, name) VALUES ($1, $1)`, d.serverID); err != nil {
			t.Fatalf("seed server %s: %v", d.serverID, err)
		}
		if err := cfg.AssignServerToInstance(ctx, d.serverID, inst.ID); err != nil {
			t.Fatalf("assign %s: %v", d.serverID, err)
		}
		// Stamp the (nullable) database_name — the field ListDatabaseGroups groups
		// by. Direct SQL stands in for the enrollment path (ly-8b0.8).
		if _, err := configPool.Exec(ctx, `UPDATE servers SET database_name=$1 WHERE id=$2`, d.dbName, d.serverID); err != nil {
			t.Fatalf("set database_name %s: %v", d.serverID, err)
		}
		if err := stats.WriteQueryStats(ctx, []store.QueryStat{
			{ServerID: d.serverID, CollectedAt: now.Add(-time.Hour), Fingerprint: "fp-" + d.dbName,
				NormalizedQuery: "SELECT $1", Calls: 3600, TotalTimeMs: 720},
		}); err != nil {
			t.Fatalf("seed query stats %s: %v", d.serverID, err)
		}
	}
	return srv
}

func TestDatabasesList_QualifiedRowsAndCount(t *testing.T) {
	srv := setupDatabasesScreen(t)
	html := getBody(t, srv.URL+"/databases/all")
	for _, want := range []string{
		`id="databases-screen"`,
		`class="topbar"`, // rendered inside the design shell
		"2 DATABASES ACROSS 1 CLUSTER",
		"A DATABASE IS IDENTIFIED BY CLUSTER + NAME", // info strip
		"orders-prod/orders", "orders-prod/billing", // cluster-qualified identities
	} {
		if !strings.Contains(html, want) {
			t.Errorf("databases page missing %q", want)
		}
	}
}
