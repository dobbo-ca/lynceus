package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/api"
	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/dobbo-ca/lynceus/internal/testch"
)

func clusterScopeURL(srvURL, clusterID string) string {
	v := url.Values{"scope": {"cluster:" + clusterID}}
	return srvURL + "/cluster?" + v.Encode()
}

func TestClusterScopedOverview_returns200WithIssuesAndNodeCards(t *testing.T) {
	srv, clusterID, _ := setupOverview(t)
	// setupOverview seeds a high-severity insight, so the cluster has open issues.
	resp, err := http.Get(clusterScopeURL(srv.URL, clusterID))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	b := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, b)
	}
	// Rendered inside the design Shell.
	if !strings.Contains(b, "LYNCEUS") || !strings.Contains(b, "scope-screen") {
		t.Error("cluster overview not rendered inside the design shell")
	}
	if !strings.Contains(b, "OPEN ISSUES ON THIS CLUSTER") {
		t.Errorf("missing scoped-issues card; body: %s", b)
	}
	if !strings.Contains(b, "scope-health") {
		t.Error("missing health rollup line")
	}
	// Node card with a ⌖ scope button pointing at the node-scope Overview (/nodes).
	if !strings.Contains(b, "scope-btn") || !strings.Contains(b, "/nodes?scope=node") {
		t.Errorf("node card missing ⌖ scope link to /nodes; body: %s", b)
	}
	if !strings.Contains(b, "node-role") {
		t.Error("node card missing role badge")
	}
}

func TestClusterScopedOverview_cleanStripWhenNoIssues(t *testing.T) {
	srv, clusterID := setupCleanCluster(t)
	resp, err := http.Get(clusterScopeURL(srv.URL, clusterID))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	b := readBody(t, resp)
	if !strings.Contains(b, "NO OPEN CHECKS OR INSIGHTS ON THIS CLUSTER") {
		t.Errorf("missing clean strip; body: %s", b)
	}
}

func TestClusterScopedOverview_missingScope404(t *testing.T) {
	srv, _, _ := setupOverview(t)
	resp, _ := http.Get(srv.URL + "/cluster")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status=%d, want 404 (no cluster scope)", resp.StatusCode)
	}
}

func TestCapabilitiesPage_scopedReturns200(t *testing.T) {
	srv, clusterID, _ := setupOverview(t)
	v := url.Values{"scope": {"cluster:" + clusterID}}
	resp, err := http.Get(srv.URL + "/capabilities?" + v.Encode())
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	b := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, b)
	}
	if !strings.Contains(b, "CAPABILITIES") || !strings.Contains(b, "LYNCEUS") {
		t.Error("capabilities page not rendered inside the shell")
	}
}

func TestCapabilitiesPage_fleetEmptyState(t *testing.T) {
	srv, _, _ := setupOverview(t)
	resp, err := http.Get(srv.URL + "/capabilities")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	b := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if !strings.Contains(strings.ToUpper(b), "SELECT A CLUSTER OR NODE SCOPE") {
		t.Errorf("fleet-scope capabilities missing empty state; body: %s", b)
	}
}

// setupCleanCluster wires a server with a cluster/instance/one server stream and
// NO checks or insights, so the scoped Overview renders the clean strip.
func setupCleanCluster(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	ctx := context.Background()
	configPool := newDBPool(t)
	conn := testch.Start(t)
	if err := store.ApplyConfigMigrations(ctx, configPool); err != nil {
		t.Fatalf("config migrate: %v", err)
	}
	if err := store.ApplyClickHouseMigrations(ctx, conn); err != nil {
		t.Fatalf("stats migrate: %v", err)
	}
	cfg := store.NewConfig(configPool)
	stats := store.NewCHStats(conn)
	if _, err := configPool.Exec(ctx, `INSERT INTO servers (id, name) VALUES ($1,$1)`, "clean-srv"); err != nil {
		t.Fatalf("seed server: %v", err)
	}
	cl, err := cfg.CreateCluster(ctx, "clean-cluster")
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}
	inst, err := cfg.CreateInstance(ctx, cl.ID, "primary")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	if err := cfg.AssignServerToInstance(ctx, "clean-srv", inst.ID); err != nil {
		t.Fatalf("assign: %v", err)
	}
	httpSrv := httptest.NewServer(api.NewServer(api.Config{DevAuth: true}, stats, cfg).Handler())
	t.Cleanup(httpSrv.Close)
	return httpSrv, cl.ID
}
