package api_test

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dobbo-ca/lynceus/internal/api"
	"github.com/dobbo-ca/lynceus/internal/store"
)

// pgxpoolT aliases the pool type the shared harness (server_test.go) returns,
// keeping this file's helper signatures readable.
type pgxpoolT = pgxpool.Pool

func apiConfigDevAuth() api.Config { return api.Config{DevAuth: true} }

func itoaTest(id int64) string { return strconv.FormatInt(id, 10) }

// seedScripts inserts a representative script set and returns dev-admin's
// PERSONAL script id (dev-admin is the DevAuth viewer).
func seedScripts(t *testing.T, pool *pgxpoolT) int64 {
	t.Helper()
	ctx := context.Background()
	cfg := store.NewConfig(pool)
	must := func(in store.CreateScriptInput) store.SavedScript {
		s, err := cfg.CreateScript(ctx, in)
		if err != nil {
			t.Fatalf("seed %s: %v", in.Name, err)
		}
		return s
	}
	must(store.CreateScriptInput{Name: "dead-tuples-by-table", Description: "Dead tuples per table",
		SQLText: "SELECT relname FROM pg_stat_user_tables", Scope: "GLOBAL", Owner: "m.chen"})
	must(store.CreateScriptInput{Name: "idle-in-transaction", Description: "Idle tx > 15m",
		SQLText: "SELECT pid FROM pg_stat_activity", Scope: "TEAM", Owner: "j.alvarez", OwnerGroup: "dba-oncall"})
	mine := must(store.CreateScriptInput{Name: "replica-lag-quick", Description: "Replay lag per replica",
		SQLText: "SELECT client_addr FROM pg_stat_replication", Scope: "PERSONAL", Owner: "dev-admin"})
	return mine.ID
}

func seedRunFleet(t *testing.T, pool *pgxpoolT) {
	t.Helper()
	ctx := context.Background()
	cfg := store.NewConfig(pool)
	cl, err := cfg.CreateCluster(ctx, "orders-prod")
	if err != nil {
		t.Fatalf("cluster: %v", err)
	}
	in, err := cfg.CreateInstance(ctx, cl.ID, "srv-orders-primary")
	if err != nil {
		t.Fatalf("instance: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO servers (id, name, database_name, instance_id) VALUES ($1,$2,$3,$4)`,
		"srv-run-1", "srv-orders-primary", "orders", in.ID); err != nil {
		t.Fatalf("seed server: %v", err)
	}
}

func getBody200(t *testing.T, u string) string {
	t.Helper()
	resp, err := http.Get(u)
	if err != nil {
		t.Fatalf("GET %s: %v", u, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("GET %s status = %d, want 200", u, resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func TestSavedScriptsPage_rendersRowsScopeBadgesAndTokens(t *testing.T) {
	pool, srv := setupAudit(t, apiConfigDevAuth())
	_ = seedScripts(t, pool)

	resp, err := http.Get(srv.URL + "/scripts")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	html := string(body)
	for _, want := range []string{
		"<!doctype html>",
		`id="scripts-table"`,
		`hx-get="/partial/scripts"`,
		`href="/static/css/scripts.css"`,
		"dead-tuples-by-table", // GLOBAL script name
		"replica-lag-quick",    // owner's PERSONAL script
		"var(--acc2)",          // GLOBAL scope-badge token color
		"visible to everyone in the org",
		`href="/scripts/`, // detail link
		`/delete`,         // delete form for owned script
	} {
		if !strings.Contains(html, want) {
			t.Errorf("scripts page missing %q", want)
		}
	}
}

func TestSavedScriptsPartial_filtersByQueryAndIsFragment(t *testing.T) {
	pool, srv := setupAudit(t, apiConfigDevAuth())
	_ = seedScripts(t, pool)

	resp, err := http.Get(srv.URL + "/partial/scripts?q=idle")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	html := string(body)
	if strings.Contains(html, "<!doctype html>") {
		t.Error("partial returned a full document; expected a fragment")
	}
	if !strings.Contains(html, "idle-in-transaction") {
		t.Error("q=idle missing the matching script")
	}
	if strings.Contains(html, "dead-tuples-by-table") {
		t.Error("q=idle leaked a non-matching script")
	}
}

func TestScriptDetailPage_ownerSeesScopeSwitchAndRunCard(t *testing.T) {
	pool, srv := setupAudit(t, apiConfigDevAuth())
	id := seedScripts(t, pool) // owner is dev-admin (the DevAuth viewer)

	resp, err := http.Get(srv.URL + "/scripts/" + itoaTest(id))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	html := string(body)
	for _, want := range []string{
		"replica-lag-quick",                           // script name
		"SELECT client_addr FROM pg_stat_replication", // SQL block (user metadata, not monitored-DB literal)
		`id="access-card"`,
		`id="run-card"`,
		"sd-scopebtn",              // owner scope switch present
		"SEARCH &amp; SELECT A TARGET", // initial RUN label
		`hx-get="/partial/scripts/` + itoaTest(id) + `/run"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("detail page missing %q", want)
		}
	}
	// Owner must NOT see the "managed by" read-only badge.
	if strings.Contains(html, "MANAGED BY") {
		t.Error("owner should not see the read-only managed-by badge")
	}
}

func TestScriptDetailPage_nonOwnerSeesManagedBy(t *testing.T) {
	pool, srv := setupAudit(t, apiConfigDevAuth())
	ctx := context.Background()
	cfg := store.NewConfig(pool)
	// Owned by someone else; dev-admin is admin but not the owner.
	other, err := cfg.CreateScript(ctx, store.CreateScriptInput{
		Name: "blocking-lock-tree", Description: "who blocks whom",
		SQLText: "SELECT 1", Scope: "GLOBAL", Owner: "j.alvarez"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	resp, err := http.Get(srv.URL + "/scripts/" + itoaTest(other.ID))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	html := string(body)
	if !strings.Contains(html, "MANAGED BY j.alvarez") {
		t.Error("non-owner should see 'MANAGED BY j.alvarez'")
	}
	if strings.Contains(html, "sd-scopebtn") {
		t.Error("non-owner must not get the scope-switch buttons")
	}
}

func TestScriptDetailPage_nonVisiblePersonalReturns404(t *testing.T) {
	pool, srv := setupAudit(t, apiConfigDevAuth())
	ctx := context.Background()
	cfg := store.NewConfig(pool)
	// A PERSONAL script owned by someone else. The DevAuth viewer (dev-admin)
	// is an admin but NOT the owner, so the detail read must 404 and never
	// leak the SQL — proving the read is visibility-gated, not admin-gated.
	secret, err := cfg.CreateScript(ctx, store.CreateScriptInput{
		Name: "secret-personal", Description: "mine only",
		SQLText: "SELECT secret FROM vault", Scope: "PERSONAL", Owner: "j.alvarez"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	resp, err := http.Get(srv.URL + "/scripts/" + itoaTest(secret.ID))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for a non-visible PERSONAL script", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(body), "SELECT secret FROM vault") {
		t.Error("non-visible PERSONAL script leaked its SQL to a non-owner")
	}
}

func TestScriptScopeChange_ownerUpdatesAndAudits(t *testing.T) {
	pool, srv := setupAudit(t, apiConfigDevAuth())
	id := seedScripts(t, pool) // owner == dev-admin

	form := "scope=GLOBAL"
	resp, err := http.Post(srv.URL+"/scripts/"+itoaTest(id)+"/scope",
		"application/x-www-form-urlencoded", strings.NewReader(form))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	html := string(body)
	if strings.Contains(html, "<!doctype html>") {
		t.Error("scope change should return the access-card fragment, not a full page")
	}
	if !strings.Contains(html, `id="access-card"`) {
		t.Error("scope change response missing the access-card fragment")
	}
	if !strings.Contains(html, "everyone in the org") {
		t.Error("scope change did not reflect the new GLOBAL visibility")
	}

	ctx := context.Background()
	cfg := store.NewConfig(pool)
	sc, _, _ := cfg.GetScript(ctx, id)
	if sc.Scope != "GLOBAL" {
		t.Errorf("persisted scope = %q, want GLOBAL", sc.Scope)
	}
	var n int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action='saved_script.scope.change'`).Scan(&n)
	if n != 1 {
		t.Errorf("scope-change audit rows = %d, want 1", n)
	}
}

// TestScriptScopeChange_devAuthDisabledReturns401 pins the MIDDLEWARE gate:
// with DevAuth off, withAuth 401s before any script handler runs. (This is a
// distinct layer from the handler's own 403 owner/admin gate, covered below.)
func TestScriptScopeChange_devAuthDisabledReturns401(t *testing.T) {
	_, srv := setupAudit(t, api.Config{DevAuth: false})
	resp, err := http.Post(srv.URL+"/scripts/1/scope",
		"application/x-www-form-urlencoded", strings.NewReader("scope=GLOBAL"))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 (DevAuth off)", resp.StatusCode)
	}
}

// TestScriptScopeChange_handlerForbidsNonOwnerNonAdmin exercises the HANDLER's
// 403 path (scriptWriteStatus mapping store.ErrScriptForbidden ->
// http.StatusForbidden). It impersonates a principal who is neither the owner
// nor an admin via the dev-only X-Dev-Actor / X-Dev-Admin headers, so the
// request passes DevAuth's middleware but the store's owner-or-admin gate
// rejects it. Also asserts no scope change persisted and no audit row written.
func TestScriptScopeChange_handlerForbidsNonOwnerNonAdmin(t *testing.T) {
	pool, srv := setupAudit(t, apiConfigDevAuth())
	id := seedScripts(t, pool) // owner == dev-admin, scope == PERSONAL

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/scripts/"+itoaTest(id)+"/scope",
		strings.NewReader("scope=GLOBAL"))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Dev-Actor", "intruder") // not the owner
	req.Header.Set("X-Dev-Admin", "false")     // not an admin
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (handler owner/admin gate)", resp.StatusCode)
	}

	ctx := context.Background()
	cfg := store.NewConfig(pool)
	sc, _, _ := cfg.GetScript(ctx, id)
	if sc.Scope != "PERSONAL" {
		t.Errorf("scope changed to %q despite 403", sc.Scope)
	}
	var n int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action='saved_script.scope.change'`).Scan(&n)
	if n != 0 {
		t.Errorf("forbidden scope change wrote %d audit rows, want 0", n)
	}
}

func TestScriptDelete_removesRowAndReturnsTableFragment(t *testing.T) {
	pool, srv := setupAudit(t, apiConfigDevAuth())
	id := seedScripts(t, pool)

	resp, err := http.Post(srv.URL+"/scripts/"+itoaTest(id)+"/delete",
		"application/x-www-form-urlencoded", strings.NewReader(""))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `id="scripts-table"`) {
		t.Error("delete should return the refreshed scripts-table fragment")
	}
	ctx := context.Background()
	if _, ok, _ := store.NewConfig(pool).GetScript(ctx, id); ok {
		t.Error("script still present after delete")
	}
}

func TestScriptCreate_insertsAndRedirects(t *testing.T) {
	pool, srv := setupAudit(t, apiConfigDevAuth())

	// Do not auto-follow the redirect so we can assert on Location.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	form := "name=new-script&description=made+in+test&sql=SELECT+42&scope=TEAM"
	resp, err := client.Post(srv.URL+"/scripts",
		"application/x-www-form-urlencoded", strings.NewReader(form))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); !strings.HasPrefix(loc, "/scripts/") {
		t.Errorf("Location = %q, want /scripts/<id>", loc)
	}
	var n int
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM saved_scripts WHERE name='new-script'`).Scan(&n)
	if n != 1 {
		t.Errorf("created rows = %d, want 1", n)
	}
}

func TestScriptRun_targetSelectionThenNodeDBEnablesRun(t *testing.T) {
	pool, srv := setupAudit(t, apiConfigDevAuth())
	id := seedScripts(t, pool)
	seedRunFleet(t, pool) // one cluster / node / database

	base := srv.URL + "/partial/scripts/" + itoaTest(id) + "/run"

	// 1. No selection: RUN inert, prompts to select a target.
	html := getBody200(t, base)
	if !strings.Contains(html, "SEARCH &amp; SELECT A TARGET") {
		t.Error("initial run card should prompt to select a target")
	}
	if !strings.Contains(html, "orders-prod") {
		t.Error("target search should list the cluster")
	}

	// 2. Select the cluster target: NODE + DATABASE chip rows appear, RUN still inert.
	html = getBody200(t, base+"?target="+url.QueryEscape("cluster|orders-prod|"))
	if !strings.Contains(html, "SELECT NODE &amp; DATABASE TO RUN") {
		t.Errorf("after cluster select, expected node/db prompt; got:\n%s", html)
	}
	if !strings.Contains(html, "srv-orders-primary") {
		t.Error("node chips missing after cluster select")
	}

	// 3. Pick node + database: RUN becomes a live console hand-off link.
	sel := "?target=" + url.QueryEscape("cluster|orders-prod|") +
		"&node=srv-orders-primary&db=orders"
	html = getBody200(t, base+sel)
	if !strings.Contains(html, "RUN ON srv-orders-primary · orders →") {
		t.Errorf("ready run label missing; got:\n%s", html)
	}
	if !strings.Contains(html, `href="/console?`) {
		t.Error("ready RUN must link to the console hand-off URL")
	}
	if !strings.Contains(html, "script="+itoaTest(id)) ||
		!strings.Contains(html, "node=srv-orders-primary") ||
		!strings.Contains(html, "db=orders") {
		t.Error("hand-off URL missing script/node/db params")
	}
	// RUN carries execute-intent (run=1); the console decides grant-vs-gate.
	if !strings.Contains(html, "run=1") {
		t.Error("RUN hand-off must carry run=1 (execute-intent) — otherwise it is indistinguishable from a load")
	}
}

func TestScriptRun_loadHrefHasNoRunParam(t *testing.T) {
	// The list-row LOAD hand-off is load-without-run: /console?script=<id>
	// with NO run=1, so it can never be confused with the RUN card's
	// execute-intent link. (Guards the run=false branch of consoleHandoffURL.)
	pool, srv := setupAudit(t, apiConfigDevAuth())
	_ = seedScripts(t, pool)
	html := getBody200(t, srv.URL+"/scripts")
	if !strings.Contains(html, `href="/console?script=`) {
		t.Error("list is missing the load-into-console link")
	}
	if strings.Contains(html, "run=1") {
		t.Error("the list LOAD link must NOT carry run=1 (load-without-run)")
	}
}

func TestScriptSearchFragment_filtersAndLinksManage(t *testing.T) {
	pool, srv := setupAudit(t, apiConfigDevAuth())
	_ = seedScripts(t, pool)

	// Empty query: browse all visible scripts + MANAGE SCRIPTS link.
	html := getBody200(t, srv.URL+"/partial/scripts/search")
	if strings.Contains(html, "<!doctype html>") {
		t.Error("search fragment must not be a full page")
	}
	if !strings.Contains(html, "MANAGE SCRIPTS") || !strings.Contains(html, `href="/scripts"`) {
		t.Error("search fragment missing MANAGE SCRIPTS link")
	}
	if !strings.Contains(html, "dead-tuples-by-table") {
		t.Error("empty search should browse all visible scripts")
	}

	// Typed query filters; a load link points at the console hand-off.
	html = getBody200(t, srv.URL+"/partial/scripts/search?q=replica")
	if !strings.Contains(html, "replica-lag-quick") {
		t.Error("q=replica missing the matching script")
	}
	if strings.Contains(html, "dead-tuples-by-table") {
		t.Error("q=replica leaked a non-matching script")
	}
	if !strings.Contains(html, `href="/console?script=`) {
		t.Error("search item missing the load-into-console link")
	}

	// No matches shows the empty state.
	html = getBody200(t, srv.URL+"/partial/scripts/search?q=zzzzz")
	if !strings.Contains(html, "NO SCRIPTS MATCH") {
		t.Error("no-match search should show the empty state")
	}
}
