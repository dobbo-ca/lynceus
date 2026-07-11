package api_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dobbo-ca/lynceus/internal/api"
	"github.com/dobbo-ca/lynceus/internal/scope"
	"github.com/dobbo-ca/lynceus/internal/store"
)

// seedTopology creates one cluster, one node, and one server stream carrying a
// database name, returning the cluster and instance for URL construction.
func seedTopology(t *testing.T, pool *pgxpool.Pool) (store.Cluster, store.Instance) {
	t.Helper()
	ctx := t.Context()
	cfg := store.NewConfig(pool)
	cl, err := cfg.CreateCluster(ctx, "orders-prod")
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}
	in, err := cfg.CreateInstance(ctx, cl.ID, "node-1")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO servers (id, name, database_name) VALUES ($1, $1, $2)`, "srv-a", "orders"); err != nil {
		t.Fatalf("seed server: %v", err)
	}
	if err := cfg.AssignServerToInstance(ctx, "srv-a", in.ID); err != nil {
		t.Fatalf("assign: %v", err)
	}
	return cl, in
}

func TestFleet_devAuth_rendersTopBar(t *testing.T) {
	_, srv := setupAudit(t, api.Config{DevAuth: true})
	resp, err := http.Get(srv.URL + "/fleet")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	html := body(t, resp)
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	for _, want := range []string{"LYNCEUS", "SCOPE:", "FLEET", "POLL", "id=\"theme-toggle\"", "/static/css/shell.css"} {
		if !strings.Contains(html, want) {
			t.Errorf("/fleet missing %q", want)
		}
	}
}

func TestFleet_withoutDevAuth_401(t *testing.T) {
	_, srv := setupAudit(t, api.Config{DevAuth: false})
	resp, err := http.Get(srv.URL + "/fleet")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestScopeOptions_searchMatchesTopology(t *testing.T) {
	pool, srv := setupAudit(t, api.Config{DevAuth: true})
	seedTopology(t, pool)

	resp, err := http.Get(srv.URL + "/partial/scope-options?q=orders")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	html := body(t, resp)
	for _, want := range []string{
		`id="scope-options"`,
		"orders-prod",          // CLUSTER
		"orders-prod / node-1", // NODE
		"orders-prod/orders",   // DATABASE (cluster-qualified)
		"CLUSTER", "NODE", "DATABASE",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("options missing %q; body=%s", want, html)
		}
	}
}

func TestScopeOptions_noMatchEmptyState(t *testing.T) {
	pool, srv := setupAudit(t, api.Config{DevAuth: true})
	seedTopology(t, pool)
	resp, err := http.Get(srv.URL + "/partial/scope-options?q=zzzzz")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if !strings.Contains(body(t, resp), "NO COMPONENTS MATCH") {
		t.Error("expected empty-state marker")
	}
}

func TestFleet_clusterScope_resolvesLabelAndReset(t *testing.T) {
	pool, srv := setupAudit(t, api.Config{DevAuth: true})
	cl, _ := seedTopology(t, pool)
	enc := scope.Scope{Kind: scope.Cluster, ClusterID: cl.ID}.Encode()

	resp, err := http.Get(srv.URL + "/fleet?scope=" + enc)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	html := body(t, resp)
	if !strings.Contains(html, "orders-prod") {
		t.Error("scoped chip must show the resolved cluster label")
	}
	if !strings.Contains(html, "← FLEET") {
		t.Error("scoped shell must show the ← FLEET reset")
	}
}
