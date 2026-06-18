package api_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dobbo-ca/lynceus/internal/api"
	"github.com/dobbo-ca/lynceus/internal/store"
)

// setupOverview wires a server with two separate DBs (config + stats),
// seeds one cluster/instance/2 server streams + query stats + one insight,
// and returns the httptest.Server plus the cluster ID and a seeded fingerprint.
func setupOverview(t *testing.T) (srv *httptest.Server, clusterID, fp string) {
	t.Helper()
	ctx := context.Background()

	configPool := newDBPool(t)
	statsPool := newDBPool(t)

	if err := store.ApplyConfigMigrations(ctx, configPool); err != nil {
		t.Fatalf("config migrate: %v", err)
	}
	if err := store.ApplyStatsMigrations(ctx, statsPool); err != nil {
		t.Fatalf("stats migrate: %v", err)
	}

	cfg := store.NewConfig(configPool)
	stats := store.NewStats(statsPool)

	// Seed two server rows.
	for _, id := range []string{"ov-srv-a", "ov-srv-b"} {
		if _, err := configPool.Exec(ctx, `INSERT INTO servers (id, name) VALUES ($1, $1)`, id); err != nil {
			t.Fatalf("seed server %s: %v", id, err)
		}
	}

	cl, err := cfg.CreateCluster(ctx, "my-cluster")
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}
	inst, err := cfg.CreateInstance(ctx, cl.ID, "primary")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	for _, id := range []string{"ov-srv-a", "ov-srv-b"} {
		if err := cfg.AssignServerToInstance(ctx, id, inst.ID); err != nil {
			t.Fatalf("assign %s: %v", id, err)
		}
	}

	now := time.Now().UTC()
	fingerprint := "fp-ovtest"
	if err := stats.WriteQueryStats(ctx, []store.QueryStat{
		{ServerID: "ov-srv-a", CollectedAt: now.Add(-30 * time.Minute), Fingerprint: fingerprint,
			NormalizedQuery: "SELECT $1 FROM t WHERE id = $2", Calls: 150, TotalTimeMs: 300},
	}); err != nil {
		t.Fatalf("seed stats: %v", err)
	}
	if err := stats.WriteInsights(ctx, []store.InsightRow{
		{ServerID: "ov-srv-a", CapturedAt: now.Add(-30 * time.Minute),
			Kind: "slow_scan", Severity: "high",
			Fingerprint: fingerprint, Relation: "t",
			NodePath: "Seq Scan(t)", RowsReturned: 1, RowsScanned: 50000,
			Selectivity: 0.00002, Detail: "full table scan"},
	}); err != nil {
		t.Fatalf("seed insights: %v", err)
	}

	httpSrv := httptest.NewServer(
		api.NewServer(api.Config{DevAuth: true}, stats, cfg).Handler(),
	)
	t.Cleanup(httpSrv.Close)
	return httpSrv, cl.ID, fingerprint
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

// TestOverviewPage_returns200WithContent asserts the full page renders DOCTYPE,
// cluster name, and the "Overview" sidebar item.
func TestOverviewPage_returns200WithContent(t *testing.T) {
	srv, clusterID, _ := setupOverview(t)

	resp, err := http.Get(srv.URL + "/databases/" + clusterID)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	b := readBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", resp.StatusCode, b)
	}
	if !strings.Contains(strings.ToLower(b), "<!doctype html>") {
		t.Error("body missing <!doctype html>")
	}
	if !strings.Contains(b, "my-cluster") {
		t.Error("body missing cluster name 'my-cluster'")
	}
	if !strings.Contains(b, "Overview") {
		t.Error("body missing 'Overview' sidebar text")
	}
	if !strings.Contains(b, "SELECT") {
		t.Error("body missing query row")
	}
}

// TestOverviewPage_unknownCluster_returns404 asserts GET /databases/unknown → 404.
func TestOverviewPage_unknownCluster_returns404(t *testing.T) {
	srv, _, _ := setupOverview(t)

	resp, err := http.Get(srv.URL + "/databases/does-not-exist")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

// TestQueryDrilldown_returnsFragment asserts the drilldown partial returns a
// fragment (no DOCTYPE) containing the plan-view div or the empty-plan message.
func TestQueryDrilldown_returnsFragment(t *testing.T) {
	srv, clusterID, fp := setupOverview(t)

	url := srv.URL + "/partial/databases/" + clusterID + "/query/" + fp
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	b := readBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", resp.StatusCode, b)
	}
	if strings.Contains(strings.ToLower(b), "<!doctype") {
		t.Error("drilldown returned full document; expected fragment")
	}
	// Either the plan tree renders or the empty-plan message appears; both
	// contain "plan-view" div.
	if !strings.Contains(b, "plan-view") {
		t.Errorf("drilldown missing plan-view element; body: %s", b)
	}
}
