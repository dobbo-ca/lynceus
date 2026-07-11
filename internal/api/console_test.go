package api_test

import (
	"context"
	"net/http"
	"net/http/cookiejar"
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

func TestConsoleRun_executesAuditsAndShowsResult(t *testing.T) {
	srv, clusterID, _, pool := setupConsole(t, "orders-prod")
	runURL := consoleURL(srv.URL+"/partial/console/run", "cluster:"+clusterID, nil)
	form := url.Values{"sql": {"SELECT relname FROM pg_stat_user_tables"}, "node": {"primary"}, "db": {"appdb"}}
	resp, err := http.PostForm(runURL, form)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	html := readBody(t, resp)
	if !strings.Contains(html, "T2 READ LOGGED ·") || !strings.Contains(html, "STATEMENT HISTORY") {
		t.Fatalf("run body missing result/history markers")
	}
	if !strings.Contains(html, "RELNAME") {
		t.Error("run body missing result header")
	}
	recs, err := store.NewConfig(pool).ListAudit(context.Background(), store.AuditFilter{Action: "console.query.execute"})
	if err != nil {
		t.Fatalf("ListAudit: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("audit rows = %d, want 1", len(recs))
	}
	r0 := recs[0]
	if r0.Actor != "dev-admin" || r0.DataTier != 2 || r0.ServerID != "srv-con" {
		t.Errorf("audit row = %+v, want actor=dev-admin tier=2 server=srv-con", r0)
	}
	if !strings.Contains(string(r0.Detail), "console.query.execute") && !strings.Contains(string(r0.Detail), "statement_sha256") {
		t.Errorf("audit detail missing structural keys: %s", r0.Detail)
	}
}

func TestConsoleRun_refusedWithoutGrant_writesNoAudit(t *testing.T) {
	srv, clusterID, _, pool := setupConsole(t, "staging") // ungranted
	runURL := consoleURL(srv.URL+"/partial/console/run", "cluster:"+clusterID, nil)
	form := url.Values{"sql": {"SELECT 1"}, "node": {"primary"}, "db": {"appdb"}}
	resp, _ := http.PostForm(runURL, form)
	defer func() { _ = resp.Body.Close() }()
	html := readBody(t, resp)
	if !strings.Contains(html, "REQUEST SESSION GRANT →") {
		t.Error("ungranted RUN must return the request-access gate, not results")
	}
	recs, _ := store.NewConfig(pool).ListAudit(context.Background(), store.AuditFilter{Action: "console.query.execute"})
	if len(recs) != 0 {
		t.Errorf("ungranted RUN wrote %d audit rows, want 0", len(recs))
	}
}

func runOnce(t *testing.T, srv *httptest.Server, clusterID string) {
	t.Helper()
	runURL := consoleURL(srv.URL+"/partial/console/run", "cluster:"+clusterID, nil)
	form := url.Values{"sql": {"SELECT relname FROM pg_stat_user_tables"}, "node": {"primary"}, "db": {"appdb"}}
	resp, err := http.PostForm(runURL, form)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	_ = resp.Body.Close()
}

func TestConsole_paginationAndPageSizeCookie(t *testing.T) {
	srv, clusterID, _, _ := setupConsole(t, "orders-prod")
	runOnce(t, srv, clusterID) // 54 rows cached

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	sc := "cluster:" + clusterID
	get := func(extra url.Values) string {
		resp, err := client.Get(consoleURL(srv.URL+"/partial/console", sc, extra))
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		return readBody(t, resp)
	}
	sel := url.Values{"node": {"primary"}, "db": {"appdb"}}
	// Default page size 25 → "ROWS 1–25 OF 54".
	if !strings.Contains(get(sel), "ROWS 1–25 OF 54") {
		t.Error("default page label wrong")
	}
	// Choose 50 → sets cookie + "ROWS 1–50 OF 54".
	if !strings.Contains(get(url.Values{"node": {"primary"}, "db": {"appdb"}, "pagesize": {"50"}}), "ROWS 1–50 OF 54") {
		t.Error("pagesize=50 not applied")
	}
	// Subsequent request without the param uses the persisted 50.
	if !strings.Contains(get(sel), "ROWS 1–50 OF 54") {
		t.Error("rows-per-page not persisted per user (cookie)")
	}
	// Page 1 at size 50 → "ROWS 51–54 OF 54".
	if !strings.Contains(get(url.Values{"node": {"primary"}, "db": {"appdb"}, "page": {"1"}}), "ROWS 51–54 OF 54") {
		t.Error("second page label wrong")
	}
}

func TestConsoleExport_csvAndSql(t *testing.T) {
	srv, clusterID, _, _ := setupConsole(t, "orders-prod")
	runOnce(t, srv, clusterID)
	sc := "cluster:" + clusterID
	csv, _ := http.Get(consoleURL(srv.URL+"/console/export", sc, url.Values{"format": {"csv"}}))
	defer func() { _ = csv.Body.Close() }()
	if cd := csv.Header.Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Errorf("csv missing attachment disposition: %q", cd)
	}
	if b := readBody(t, csv); !strings.HasPrefix(b, "relname,n_dead_tup,last_autovacuum") {
		t.Errorf("csv header wrong: %q", b[:40])
	}
	sqlResp, _ := http.Get(consoleURL(srv.URL+"/console/export", sc, url.Values{"format": {"sql"}}))
	defer func() { _ = sqlResp.Body.Close() }()
	if b := readBody(t, sqlResp); !strings.Contains(b, "INSERT INTO result") {
		t.Error("sql export missing INSERTs")
	}
}

// restoreTokens extracts every `restore=<fullhex>` token from rendered history
// HTML, in document order (newest-first). templ escapes `&` as `&amp;` in
// attribute values, so a token ends at the first `&`, `"` or `'`.
func restoreTokens(html string) []string {
	var toks []string
	s := html
	for {
		i := strings.Index(s, "restore=")
		if i < 0 {
			break
		}
		s = s[i+len("restore="):]
		end := strings.IndexAny(s, "&\"'")
		if end < 0 {
			break
		}
		toks = append(toks, s[:end])
		s = s[end:]
	}
	return toks
}

func TestConsole_historyRestoreLoadsStatement(t *testing.T) {
	srv, clusterID, _, _ := setupConsole(t, "orders-prod")
	sc := "cluster:" + clusterID
	// Two distinct runs, oldest first.
	for _, q := range []string{"SELECT 1", "SELECT 2"} {
		form := url.Values{"sql": {q}, "node": {"primary"}, "db": {"appdb"}}
		resp, _ := http.PostForm(consoleURL(srv.URL+"/partial/console/run", sc, nil), form)
		_ = resp.Body.Close()
	}
	// History is newest-first → tokens are [SELECT 2, SELECT 1].
	list, _ := http.Get(consoleURL(srv.URL+"/partial/console", sc, url.Values{"node": {"primary"}, "db": {"appdb"}}))
	defer func() { _ = list.Body.Close() }()
	toks := restoreTokens(readBody(t, list))
	if len(toks) < 2 {
		t.Fatalf("want >=2 restore tokens in history, got %d", len(toks))
	}
	oldest := toks[len(toks)-1] // the run whose SQL was "SELECT 1"

	// GET the restore URL for the OLDER run — the editor must reload exactly that
	// statement (SELECT 1), not the newer one.
	rr, _ := http.Get(consoleURL(srv.URL+"/partial/console", sc, url.Values{"node": {"primary"}, "db": {"appdb"}, "restore": {oldest}}))
	defer func() { _ = rr.Body.Close() }()
	rh := readBody(t, rr)
	if !strings.Contains(rh, "SELECT 1</textarea>") {
		t.Error("restore must reload the older statement (SELECT 1) into the editor")
	}
	if strings.Contains(rh, "SELECT 2</textarea>") {
		t.Error("restoring the older run must not reload the newer statement into the editor")
	}
}
