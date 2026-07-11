package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dobbo-ca/lynceus/internal/api"
	"github.com/dobbo-ca/lynceus/internal/store"
)

// setupConsole seeds cluster(clusterName) → instance "primary" → server stream
// "srv-con" (database "appdb", t2_enabled). Returns the server, the cluster id,
// the instance id (for node-scope encodings) and the config pool (for audit
// assertions). Name it "orders-prod" for the granted flow, anything else for
// the locked flow (StubGrantReader rule).
func setupConsole(t *testing.T, clusterName string) (*httptest.Server, string, string, *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	pool := newPGPool(t)
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate config: %v", err)
	}
	cfg := store.NewConfig(pool)
	cl, err := cfg.CreateCluster(ctx, clusterName)
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}
	inst, err := cfg.CreateInstance(ctx, cl.ID, "primary")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO servers (id, name, instance_id, database_name, t2_enabled) VALUES ($1,$1,$2,$3,true)`,
		"srv-con", inst.ID, "appdb"); err != nil {
		t.Fatalf("seed server stream: %v", err)
	}
	srv := httptest.NewServer(api.NewServer(api.Config{DevAuth: true}, store.NewStats(pool), cfg).Handler())
	t.Cleanup(srv.Close)
	return srv, cl.ID, inst.ID, pool
}

// consoleURL builds a console URL with the scope param plus any extras.
func consoleURL(base, scopeEnc string, extra url.Values) string {
	q := url.Values{}
	if scopeEnc != "" {
		q.Set("scope", scopeEnc)
	}
	for k, vs := range extra {
		for _, v := range vs {
			q.Add(k, v)
		}
	}
	if enc := q.Encode(); enc != "" {
		return base + "?" + enc
	}
	return base
}

func TestConsolePage_grantedRendersPickerAndBanner(t *testing.T) {
	srv, clusterID, _, _ := setupConsole(t, "orders-prod")
	resp, err := http.Get(consoleURL(srv.URL+"/console", "cluster:"+clusterID, nil))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	html := readBody(t, resp)
	for _, want := range []string{
		"<!doctype html>",
		`id="console-body"`,
		"SESSION GRANT ACTIVE", // orders-prod is granted
		"primary",              // node chip
		"appdb",                // database chip
	} {
		if !strings.Contains(strings.ToLower(html), strings.ToLower(want)) {
			t.Errorf("page missing %q", want)
		}
	}
}

func TestConsolePartial_returnsFragmentOnly(t *testing.T) {
	srv, clusterID, _, _ := setupConsole(t, "orders-prod")
	resp, _ := http.Get(consoleURL(srv.URL+"/partial/console", "cluster:"+clusterID, nil))
	defer func() { _ = resp.Body.Close() }()
	html := readBody(t, resp)
	if strings.Contains(strings.ToLower(html), "<!doctype html>") {
		t.Error("partial returned a full document; want fragment")
	}
	if !strings.Contains(html, `id="console-body"`) {
		t.Error("partial missing swap-target id")
	}
}

func TestConsole_runInertUntilNodeAndDbSelected(t *testing.T) {
	srv, clusterID, _, _ := setupConsole(t, "orders-prod")
	sc := "cluster:" + clusterID
	// No selection: RUN inert.
	r1, _ := http.Get(consoleURL(srv.URL+"/partial/console", sc, nil))
	defer func() { _ = r1.Body.Close() }()
	if h := readBody(t, r1); strings.Contains(h, "data-console-run") {
		t.Error("RUN must be inert with no node/db selected")
	}
	// Both selected: RUN active.
	r2, _ := http.Get(consoleURL(srv.URL+"/partial/console", sc, url.Values{"node": {"primary"}, "db": {"appdb"}}))
	defer func() { _ = r2.Body.Close() }()
	h2 := readBody(t, r2)
	if !strings.Contains(h2, "data-console-run") {
		t.Error("RUN must be active once node+db resolve")
	}
	if !strings.Contains(h2, "primary · db: appdb") {
		t.Error("editor should show the resolved target name")
	}
}

func TestConsole_lockedAxisStaysInertAcrossChipClick(t *testing.T) {
	srv, clusterID, instID, _ := setupConsole(t, "orders-prod")

	// Node scope: the node axis is fixed. Database chips must carry the scope
	// (scope=node:…) so a click keeps the node fixed; the node chip is inert.
	nodeScope := "node:" + clusterID + ":" + instID
	r1, _ := http.Get(consoleURL(srv.URL+"/partial/console", nodeScope, nil))
	defer func() { _ = r1.Body.Close() }()
	h1 := readBody(t, r1)
	if !strings.Contains(h1, "data-console-chip-fixed") {
		t.Error("node scope must render the fixed node axis as an inert chip")
	}
	if !strings.Contains(h1, "scope=node") {
		t.Error("database chip hrefs must carry scope=node:… to preserve the fixed node axis")
	}
	// Simulate the database chip click: node stays fixed.
	r2, _ := http.Get(consoleURL(srv.URL+"/partial/console", nodeScope, url.Values{"db": {"appdb"}}))
	defer func() { _ = r2.Body.Close() }()
	if !strings.Contains(readBody(t, r2), "data-console-chip-fixed") {
		t.Error("after choosing a database the node axis must remain fixed (inert)")
	}

	// Symmetric database scope: database fixed, pick node.
	dbScope := "db:" + clusterID + ":appdb"
	r3, _ := http.Get(consoleURL(srv.URL+"/partial/console", dbScope, nil))
	defer func() { _ = r3.Body.Close() }()
	h3 := readBody(t, r3)
	if !strings.Contains(h3, "data-console-chip-fixed") {
		t.Error("database scope must render the fixed database axis as an inert chip")
	}
	if !strings.Contains(h3, "scope=db") {
		t.Error("node chip hrefs must carry scope=db:… to preserve the fixed database axis")
	}
}

func TestConsole_lockedClusterShowsRequestGate(t *testing.T) {
	srv, clusterID, _, _ := setupConsole(t, "staging")
	resp, _ := http.Get(consoleURL(srv.URL+"/console", "cluster:"+clusterID, nil))
	defer func() { _ = resp.Body.Close() }()
	html := readBody(t, resp)
	if !strings.Contains(html, "REQUEST SESSION GRANT →") {
		t.Error("ungranted cluster must show the request-access gate")
	}
	if !strings.Contains(html, "/capabilities?scope=cluster") {
		t.Error("request gate must link to the cluster Capabilities page")
	}
	if strings.Contains(html, "data-console-run") {
		t.Error("locked state must not render an active editor")
	}
}

func TestConsolePage_unknownClusterIs404(t *testing.T) {
	srv, _, _, _ := setupConsole(t, "orders-prod")
	resp, _ := http.Get(consoleURL(srv.URL+"/console", "cluster:does-not-exist", nil))
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}
