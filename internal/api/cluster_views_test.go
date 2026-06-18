package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dobbo-ca/lynceus/internal/api"
	"github.com/dobbo-ca/lynceus/internal/store"
)

// setupClusterViews wires a server with two separate DBs (config + stats),
// seeds one cluster/instance/2 server streams + query stats + one insight,
// and returns the httptest.Server plus the cluster ID and a seeded fingerprint.
func setupClusterViews(t *testing.T) (srv *httptest.Server, clusterID, fp string) {
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

	for _, id := range []string{"cv-srv-a", "cv-srv-b"} {
		if _, err := configPool.Exec(ctx, `INSERT INTO servers (id, name) VALUES ($1, $1)`, id); err != nil {
			t.Fatalf("seed server %s: %v", id, err)
		}
	}

	cl, err := cfg.CreateCluster(ctx, "cv-cluster")
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}
	inst, err := cfg.CreateInstance(ctx, cl.ID, "primary")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	for _, id := range []string{"cv-srv-a", "cv-srv-b"} {
		if err := cfg.AssignServerToInstance(ctx, id, inst.ID); err != nil {
			t.Fatalf("assign %s: %v", id, err)
		}
	}

	now := time.Now().UTC()
	fingerprint := "fp-cvtest"
	if err := stats.WriteQueryStats(ctx, []store.QueryStat{
		{ServerID: "cv-srv-a", CollectedAt: now.Add(-30 * time.Minute), Fingerprint: fingerprint,
			NormalizedQuery: "SELECT $1 FROM cv_table WHERE id = $2", Calls: 120, TotalTimeMs: 240},
	}); err != nil {
		t.Fatalf("seed stats: %v", err)
	}
	if err := stats.WriteInsights(ctx, []store.InsightRow{
		{ServerID: "cv-srv-a", CapturedAt: now.Add(-30 * time.Minute),
			Kind: "slow_scan", Severity: "high",
			Fingerprint: fingerprint, Relation: "cv_table",
			NodePath: "Seq Scan(cv_table)", RowsReturned: 1, RowsScanned: 40000,
			Selectivity: 0.000025, Detail: "full table scan on cv_table"},
	}); err != nil {
		t.Fatalf("seed insights: %v", err)
	}

	httpSrv := httptest.NewServer(
		api.NewServer(api.Config{DevAuth: true}, stats, cfg).Handler(),
	)
	t.Cleanup(httpSrv.Close)
	return httpSrv, cl.ID, fingerprint
}

func TestClusterQueriesPage_returns200(t *testing.T) {
	srv, clusterID, fp := setupClusterViews(t)

	resp, err := http.Get(srv.URL + "/databases/" + clusterID + "/queries")
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
	if !strings.Contains(b, "cv-cluster") {
		t.Error("body missing cluster name 'cv-cluster'")
	}
	// Active sidebar link on "queries"
	if !strings.Contains(b, `sidebar-link--active`) {
		t.Error("body missing sidebar-link--active class")
	}
	if !strings.Contains(b, `href="/databases/`+clusterID+`/queries"`) {
		t.Error("body missing active href for /queries")
	}
	// View-specific marker: the seeded fingerprint / query text
	if !strings.Contains(b, fp) {
		t.Errorf("body missing query fingerprint %q", fp)
	}
}

func TestClusterInsightsPage_returns200(t *testing.T) {
	srv, clusterID, _ := setupClusterViews(t)

	resp, err := http.Get(srv.URL + "/databases/" + clusterID + "/insights")
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
	if !strings.Contains(b, "cv-cluster") {
		t.Error("body missing cluster name 'cv-cluster'")
	}
	if !strings.Contains(b, `sidebar-link--active`) {
		t.Error("body missing sidebar-link--active class")
	}
	if !strings.Contains(b, `href="/databases/`+clusterID+`/insights"`) {
		t.Error("body missing active href for /insights")
	}
	// View-specific marker: the seeded insight detail
	if !strings.Contains(b, "cv_table") {
		t.Error("body missing insight relation 'cv_table'")
	}
}

func TestClusterActivityPage_returns200(t *testing.T) {
	srv, clusterID, _ := setupClusterViews(t)

	resp, err := http.Get(srv.URL + "/databases/" + clusterID + "/activity")
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
	if !strings.Contains(b, "cv-cluster") {
		t.Error("body missing cluster name 'cv-cluster'")
	}
	if !strings.Contains(b, `sidebar-link--active`) {
		t.Error("body missing sidebar-link--active class")
	}
	if !strings.Contains(b, `href="/databases/`+clusterID+`/activity"`) {
		t.Error("body missing active href for /activity")
	}
	// View-specific marker
	if !strings.Contains(b, "active connections") {
		t.Error("body missing 'active connections' text")
	}
}

func TestClusterSettingsPage_returns200(t *testing.T) {
	srv, clusterID, _ := setupClusterViews(t)

	resp, err := http.Get(srv.URL + "/databases/" + clusterID + "/settings")
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
	if !strings.Contains(b, "cv-cluster") {
		t.Error("body missing cluster name 'cv-cluster'")
	}
	if !strings.Contains(b, `sidebar-link--active`) {
		t.Error("body missing sidebar-link--active class")
	}
	if !strings.Contains(b, `href="/databases/`+clusterID+`/settings"`) {
		t.Error("body missing active href for /settings")
	}
	// View-specific marker
	if !strings.Contains(b, "Cluster ID") {
		t.Error("body missing 'Cluster ID' text")
	}
}

func TestClusterViews_unknownCluster_returns404(t *testing.T) {
	srv, _, _ := setupClusterViews(t)

	resp, err := http.Get(srv.URL + "/databases/does-not-exist/queries")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}
