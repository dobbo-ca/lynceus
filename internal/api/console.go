package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/dobbo-ca/lynceus/internal/console"
	"github.com/dobbo-ca/lynceus/internal/scope"
	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/dobbo-ca/lynceus/web"
)

const (
	consoleRowLimit    = 500
	consoleTimeoutSecs = 5
)

// handleConsolePage renders the full SQL Console screen inside the design Shell.
func (s *Server) handleConsolePage(w http.ResponseWriter, r *http.Request) {
	// Saved Scripts / Search RUN hand-off: /console?script=<id>&cluster=<name>&
	// node=&db=&run=1 executes the preloaded statement (audited, grant-gated)
	// before the VM is built, so the fresh run drives the render. Best-effort —
	// any failure just leaves the console showing the loaded (un-run) script.
	if r.URL.Query().Get("run") == "1" {
		s.runConsoleHandoff(w, r)
	}
	vm, ok := s.buildConsoleVM(w, r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	vm.Shell = s.buildShellView(r, "console")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.ConsolePage(vm).Render(r.Context(), w)
}

// handleConsolePartial renders the #console-body swap unit only.
func (s *Server) handleConsolePartial(w http.ResponseWriter, r *http.Request) {
	vm, ok := s.buildConsoleVM(w, r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.ConsoleBody(vm).Render(r.Context(), w)
}

// buildConsoleVM assembles the console screen from the scoped cluster topology,
// the caller's grant, and the session cache. The cluster (and any fixed node /
// database axis) comes from the shell's ?scope= param. Returns ok=false when the
// scope carries no known cluster (404). It takes w to persist the per-user
// rows-per-page cookie (Task 5).
func (s *Server) buildConsoleVM(w http.ResponseWriter, r *http.Request) (web.ConsoleVM, bool) {
	ctx := r.Context()
	q := r.URL.Query()
	actor := actorFromContext(r)

	clusters, err := s.conf.ListClusters(ctx)
	if err != nil {
		return web.ConsoleVM{}, false
	}
	sc, clusterID, clusterName, ok := consoleClusterFromQuery(q, clusters)
	if !ok {
		return web.ConsoleVM{}, false
	}

	// Nodes + databases for the cluster; resolve the scope's node id to a name.
	instances, err := s.conf.ListInstances(ctx, clusterID)
	if err != nil {
		return web.ConsoleVM{}, false
	}
	nodeNames := make([]string, 0, len(instances))
	nodeNameByID := map[string]string{}
	dbSet := map[string]struct{}{}
	var dbNames []string
	for _, in := range instances {
		nodeNames = append(nodeNames, in.Name)
		nodeNameByID[in.ID] = in.Name
		streams, err := s.conf.ListServerStreams(ctx, in.ID)
		if err != nil {
			return web.ConsoleVM{}, false
		}
		for _, st := range streams {
			if st.DatabaseName == "" {
				continue
			}
			if _, seen := dbSet[st.DatabaseName]; !seen {
				dbSet[st.DatabaseName] = struct{}{}
				dbNames = append(dbNames, st.DatabaseName)
			}
		}
	}

	// Axis fixing derives from the scope kind (node → node fixed, pick database;
	// database → database fixed, pick node). The scope param, carried into every
	// free-axis chip href, is what keeps a fixed axis fixed across a click.
	node := q.Get("node")
	db := q.Get("db")
	nodeFixed := sc.Kind == scope.Node
	dbFixed := sc.Kind == scope.Database
	if nodeFixed {
		node = nodeNameByID[sc.NodeID]
	}
	if dbFixed {
		db = sc.Database
	}

	scopeEnc := sc.Encode()
	base := "/partial/console"
	nodeChips := consoleChips(base, scopeEnc, "node", nodeNames, node, db, nodeFixed, dbFixed)
	dbChips := consoleChips(base, scopeEnc, "db", dbNames, node, db, nodeFixed, dbFixed)

	grant, granted, err := s.grants.ActiveGrant(ctx, clusterID, clusterName, actor)
	if err != nil {
		return web.ConsoleVM{}, false
	}

	vm := web.ConsoleVM{
		ClusterID:        clusterID,
		ClusterName:      clusterName,
		Granted:          granted,
		CapabilitiesHref: "/capabilities?" + url.Values{"scope": {scopeEnc}}.Encode(),
		Picker: web.ConsolePickerVM{
			ClusterLabel:  clusterName,
			GrantChip:     consoleGrantChip(grant, granted),
			Granted:       granted,
			Nodes:         nodeChips,
			Databases:     dbChips,
			NodeFixed:     nodeFixed,
			DatabaseFixed: dbFixed,
		},
	}
	if granted {
		vm.Grant = web.ConsoleGrantVM{
			Group: grant.Group, Incident: grant.Incident, Approver: grant.Approver,
			ReadOnly: grant.ReadOnly, Expires: consoleExpiry(grant),
			AuditHref: "/audit?action=console.query.execute",
		}
		ready := node != "" && db != ""
		vm.Editor = web.ConsoleEditorVM{
			TargetName:        consoleTargetName(node, db, ready),
			Node:              node,
			Database:          db,
			RowLimit:          consoleRowLimit,
			TimeoutSecs:       consoleTimeoutSecs,
			Ready:             ready,
			RunHref:           base + "/run?" + url.Values{"scope": {scopeEnc}}.Encode(),
			SaveScriptsHref:   "/scripts",                // seam → ly-ae6.9
			SearchScriptsHref: "/partial/scripts/search", // seam → ly-ae6.9
		}
		// Restore a cached run by full-hex id (history click) — else the latest
		// run for THIS (actor, cluster). Get/Latest/Recent are cluster-scoped so
		// a restore/latest/history read cannot cross scope.
		page := consolePage(r)
		pageSize := s.consolePageSize(w, r)
		var current console.Run
		var haveCurrent bool
		if id := q.Get("restore"); id != "" {
			current, haveCurrent = s.sessions.Get(actor, clusterID, id)
		} else {
			current, haveCurrent = s.sessions.Latest(actor, clusterID)
		}
		if haveCurrent {
			vm.HasResult = true
			vm.Result = consoleResultVM(current, page, pageSize, scopeEnc)
			vm.Editor.SQL = current.SQL // reload the restored/last statement into the editor
		} else if sql := s.consoleScriptSQL(r, q.Get("script")); sql != "" {
			// Saved Scripts / Search hand-off (no cached run to restore): preload
			// the selected script's SQL into the editor. Visibility-gated.
			vm.Editor.SQL = sql
		}
		vm.History = consoleHistoryVM(s.sessions.Recent(actor, clusterID), scopeEnc)
	}
	return vm, true
}

// consoleChipHref builds a picker href carrying the CURRENT selection of BOTH
// axes plus the shell scope. Preserving the scope param is what keeps a fixed
// axis fixed after the user clicks a free-axis chip.
func consoleChipHref(base, scopeEnc, node, db string) string {
	q := url.Values{}
	if scopeEnc != "" {
		q.Set("scope", scopeEnc)
	}
	if node != "" {
		q.Set("node", node)
	}
	if db != "" {
		q.Set("db", db)
	}
	if enc := q.Encode(); enc != "" {
		return base + "?" + enc
	}
	return base
}

// consoleChips builds one axis of picker chips (axis == "node" | "db"). Every
// emitted href carries the other axis's current selection AND the scope, so
// clicking a free-axis chip preserves the fixed axis. The fixed axis collapses
// to a single inert chip (empty Href → the templ renders a non-clickable span).
func consoleChips(base, scopeEnc, axis string, values []string, node, db string, nodeFixed, dbFixed bool) []web.ConsoleChip {
	locked, selected := nodeFixed, node
	if axis == "db" {
		locked, selected = dbFixed, db
	}
	build := func(v string) web.ConsoleChip {
		n, d := node, db
		if axis == "node" {
			n = v
		} else {
			d = v
		}
		c := web.ConsoleChip{Label: v, Href: consoleChipHref(base, scopeEnc, n, d), Selected: v == selected}
		if locked {
			c.Href = "" // fixed axis: inert, no navigation
		}
		return c
	}
	if locked && selected != "" {
		return []web.ConsoleChip{build(selected)}
	}
	out := make([]web.ConsoleChip, 0, len(values))
	for _, v := range values {
		out = append(out, build(v))
	}
	return out
}

func consoleGrantChip(g console.SessionGrant, granted bool) string {
	if !granted {
		return "○ NO SESSION GRANT ON THIS CLUSTER"
	}
	return "● SESSION GRANT ACTIVE — " + g.Group + " · READ-ONLY · EXPIRES " + consoleExpiry(g)
}

func consoleExpiry(g console.SessionGrant) string {
	d := g.ExpiresAt.Sub(g.GrantedAt)
	if d < 0 {
		d = 0
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	return fmt.Sprintf("%dH %dM", h, m)
}

func consoleTargetName(node, db string, ready bool) string {
	if !ready {
		return "(SELECT NODE & DATABASE ABOVE)"
	}
	return node + " · db: " + db
}

func consolePage(r *http.Request) int {
	n, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if n < 0 {
		n = 0
	}
	return n
}

// resolveConsoleTarget resolves (node name, database name) within a cluster to
// the server stream key and its T2 flag.
func (s *Server) resolveConsoleTarget(ctx context.Context, clusterID, node, db string) (serverID string, t2Enabled, ok bool, err error) {
	instances, err := s.conf.ListInstances(ctx, clusterID)
	if err != nil {
		return "", false, false, err
	}
	for _, in := range instances {
		if in.Name != node {
			continue
		}
		streams, err := s.conf.ListServerStreams(ctx, in.ID)
		if err != nil {
			return "", false, false, err
		}
		for _, st := range streams {
			if st.DatabaseName == db {
				return st.ServerID, st.T2Enabled, true, nil
			}
		}
	}
	return "", false, false, nil
}

// consoleResultVM slices the cached run's full result to the requested page and
// builds the pagination + export controls (all scoped to the run's target).
func consoleResultVM(run console.Run, page, pageSize int, scopeEnc string) web.ConsoleResultVM {
	res := run.Result
	total := res.RowCount()
	if pageSize <= 0 {
		pageSize = 25
	}
	pageCount := (total + pageSize - 1) / pageSize
	if pageCount < 1 {
		pageCount = 1
	}
	if page < 0 {
		page = 0
	}
	if page > pageCount-1 {
		page = pageCount - 1
	}
	start := page * pageSize
	end := start + pageSize
	if end > total {
		end = total
	}
	pageRows := res.Rows[start:end]
	lower := 0
	if total > 0 {
		lower = start + 1
	}
	tsv, tooLarge := console.CopyTSV(res)
	sizes := make([]web.ConsolePageSize, 0, 4)
	for _, n := range []int{10, 25, 50, 100} {
		sizes = append(sizes, web.ConsolePageSize{
			Label:    strconv.Itoa(n),
			Href:     consolePagedHref(scopeEnc, run.Node, run.Database, "pagesize", n),
			Selected: n == pageSize,
		})
	}
	return web.ConsoleResultVM{
		Columns:      res.Columns,
		Rows:         pageRows,
		TotalRows:    total,
		DurationMs:   res.DurationMs,
		Hash:         run.ShortHash,
		PageLabel:    fmt.Sprintf("ROWS %d–%d OF %d", lower, end, total),
		PrevHref:     consolePagedHref(scopeEnc, run.Node, run.Database, "page", max0(page-1)),
		NextHref:     consolePagedHref(scopeEnc, run.Node, run.Database, "page", minInt(page+1, pageCount-1)),
		PrevActive:   page > 0,
		NextActive:   page < pageCount-1,
		PageSizes:    sizes,
		CopyTSV:      tsv,
		CopyTooLarge: tooLarge,
		CsvHref:      consoleExportHref(scopeEnc, "csv"),
		SqlHref:      consoleExportHref(scopeEnc, "sql"),
	}
}

// consolePagedHref builds a partial URL carrying the scope + resolved target
// plus one paging parameter (page | pagesize).
func consolePagedHref(scopeEnc, node, db, key string, val int) string {
	q := url.Values{}
	if scopeEnc != "" {
		q.Set("scope", scopeEnc)
	}
	if node != "" {
		q.Set("node", node)
	}
	if db != "" {
		q.Set("db", db)
	}
	q.Set(key, strconv.Itoa(val))
	return "/partial/console?" + q.Encode()
}

func max0(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// consoleExportHref builds the scoped CSV/SQL download URL.
func consoleExportHref(scopeEnc, format string) string {
	return "/console/export?" + url.Values{"scope": {scopeEnc}, "format": {format}}.Encode()
}

// consoleHistoryVM renders the strict-audit statement history. The restore token
// is the FULL hex Run.ID (URL-safe); the displayed hash is the short form.
func consoleHistoryVM(runs []console.Run, scopeEnc string) []web.ConsoleHistoryRow {
	out := make([]web.ConsoleHistoryRow, 0, len(runs))
	for _, r := range runs {
		href := "/partial/console?" + url.Values{
			"scope": {scopeEnc}, "restore": {r.ID}, "node": {r.Node}, "db": {r.Database},
		}.Encode()
		out = append(out, web.ConsoleHistoryRow{
			TS:   r.At.UTC().Format("15:04:05Z"),
			Stmt: strings.Join(strings.Fields(r.SQL), " "),
			Ms:   fmt.Sprintf("%.1f ms", r.DurationMs),
			Hash: r.ShortHash,
			Href: href,
		})
	}
	return out
}

const consolePageSizeCookie = "lynceus.console.pagesize"

// consolePageSize resolves rows-per-page from (1) a ?pagesize= override, which
// it persists to a per-user cookie, then (2) the cookie, else 25.
func (s *Server) consolePageSize(w http.ResponseWriter, r *http.Request) int {
	if raw := r.URL.Query().Get("pagesize"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && consoleValidPageSize(n) {
			http.SetCookie(w, &http.Cookie{Name: consolePageSizeCookie, Value: raw, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode})
			return n
		}
	}
	if ck, err := r.Cookie(consolePageSizeCookie); err == nil {
		if n, err := strconv.Atoi(ck.Value); err == nil && consoleValidPageSize(n) {
			return n
		}
	}
	return 25
}

func consoleValidPageSize(n int) bool {
	return n == 10 || n == 25 || n == 50 || n == 100
}

// handleConsoleRun executes one statement against the resolved target and
// records a strict, fail-closed T2 audit row before returning any result:
// execute → audit → (audit error: discard, error) → cache + render. A run
// against a cluster with no active grant does not execute and does not audit.
func (s *Server) handleConsoleRun(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	actor := actorFromContext(r)

	sc := scope.Parse(r.URL.Query().Get("scope"))
	clusterID := sc.ClusterID
	if clusterID == "" {
		http.NotFound(w, r)
		return
	}
	clusters, err := s.conf.ListClusters(ctx)
	if err != nil {
		http.Error(w, "load clusters", http.StatusInternalServerError)
		return
	}
	var clusterName string
	for _, c := range clusters {
		if c.ID == clusterID {
			clusterName = c.Name
			break
		}
	}
	if clusterName == "" {
		http.NotFound(w, r)
		return
	}

	// Grant gate — no grant, no execution, no audit; re-render the body (which
	// shows the request-access gate).
	_, granted, err := s.grants.ActiveGrant(ctx, clusterID, clusterName, actor)
	if err != nil {
		http.Error(w, "grant check", http.StatusInternalServerError)
		return
	}
	if !granted {
		s.renderConsoleBody(w, r)
		return
	}

	node := r.FormValue("node")
	db := r.FormValue("db")
	executed, err := s.runConsoleStatement(ctx, clusterID, clusterName, node, db, r.FormValue("sql"), actor)
	if err != nil {
		// Fail closed: execution or its audit failed — withhold results.
		http.Error(w, "run failed — result withheld", http.StatusInternalServerError)
		return
	}
	if !executed {
		// Target not fully resolved, empty statement, or the per-server T2 kill
		// switch is off — re-render (RUN inert), no results, no audit.
		s.renderConsoleBody(w, r)
		return
	}

	// The freshly-cached run drives the re-render; carry the selection so the
	// picker stays resolved (scope is already in the request URL).
	r2 := r.Clone(ctx)
	q := r2.URL.Query()
	q.Set("node", node)
	q.Set("db", db)
	r2.URL.RawQuery = q.Encode()
	s.renderConsoleBody(w, r2)
}

// runConsoleStatement executes one statement against the resolved target and
// writes the fail-closed T2 audit row before caching the run. The caller MUST
// have already verified an active session grant on clusterID.
//
// It enforces the per-server T2 kill switch (servers.t2_enabled): a target
// whose stream has t2_enabled=false is refused with NO execution and NO audit,
// mirroring the canonical T2Reader gateway's fast-reject. Returns:
//   - executed=true when a run happened and was cached;
//   - executed=false, err=nil when the target is unresolved, the statement is
//     empty, or T2 is disabled on the target (nothing ran, nothing audited);
//   - err!=nil on an internal execute/audit failure the caller fails closed on.
func (s *Server) runConsoleStatement(ctx context.Context, clusterID, clusterName, node, db, sql, actor string) (bool, error) {
	sql = strings.TrimSpace(sql)
	serverID, t2Enabled, ok, err := s.resolveConsoleTarget(ctx, clusterID, node, db)
	if err != nil {
		return false, err
	}
	if !ok || sql == "" || !t2Enabled {
		return false, nil
	}

	res, err := s.exec.Execute(ctx, console.Statement{
		ClusterID: clusterID, ClusterName: clusterName, Node: node, Database: db,
		ServerID: serverID, SQL: sql, RowLimit: consoleRowLimit, TimeoutSecs: consoleTimeoutSecs, Actor: actor,
	})
	if err != nil {
		return false, err
	}

	// Strict audit BEFORE caching/returning results — fail closed. Detail is a
	// T2 governance artifact (statement text is permitted in the audit_log).
	rec, err := s.conf.AppendAuditReturning(ctx, store.AuditEntry{
		Actor: actor, Action: "console.query.execute", ServerID: serverID, DataTier: 2,
		Detail: map[string]any{
			"target_node":      node,
			"target_database":  db,
			"statement":        sql,
			"statement_sha256": sha256Hex(sql),
			"duration_ms":      res.DurationMs,
			"row_count":        res.RowCount(),
			"row_limit":        consoleRowLimit,
			"timeout_secs":     consoleTimeoutSecs,
			"read_only":        true,
		},
	})
	if err != nil {
		return false, err
	}

	// ID is the FULL hex (URL-safe lookup key for restore/Get); ShortHash is
	// display-only. Scope the cache entry to (actor, clusterID).
	s.sessions.Append(actor, clusterID, console.Run{
		ID: hex.EncodeToString(rec.RowHash), ShortHash: shortHash(rec.RowHash), ClusterID: clusterID,
		At: rec.At, SQL: sql, Node: node, Database: db,
		Result: res, DurationMs: res.DurationMs,
	})
	return true, nil
}

// runConsoleHandoff consumes a /console?script=<id>&cluster=<name>&node=&db=&
// run=1 hand-off from Saved Scripts / Search: it resolves the cluster (by name),
// loads the visible script's SQL, and — when an active grant exists on that
// cluster — executes it through the same audited path as a manual RUN. It is
// best-effort: any failure leaves the console showing the loaded (un-run)
// script, so it never blocks the page render.
func (s *Server) runConsoleHandoff(_ http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	actor := actorFromContext(r)

	clusters, err := s.conf.ListClusters(ctx)
	if err != nil {
		return
	}
	_, clusterID, clusterName, ok := consoleClusterFromQuery(q, clusters)
	if !ok {
		return
	}
	if _, granted, err := s.grants.ActiveGrant(ctx, clusterID, clusterName, actor); err != nil || !granted {
		return
	}
	sql := s.consoleScriptSQL(r, q.Get("script"))
	if sql == "" {
		return
	}
	_, _ = s.runConsoleStatement(ctx, clusterID, clusterName, q.Get("node"), q.Get("db"), sql, actor)
}

// consoleClusterFromQuery resolves the target cluster for a console request from
// either the scope= param (normal navigation) or the ?cluster=<NAME> hand-off
// param (Saved Scripts / Search, which carries no scope=). For a name hand-off
// it synthesizes a cluster scope so every downstream chip / run / export href
// carries scope=cluster:<id>. Returns ok=false when neither yields a known
// cluster.
func consoleClusterFromQuery(q url.Values, clusters []store.Cluster) (sc scope.Scope, clusterID, clusterName string, ok bool) {
	sc = scope.Parse(q.Get("scope"))
	if sc.ClusterID != "" {
		for _, c := range clusters {
			if c.ID == sc.ClusterID {
				return sc, c.ID, c.Name, true
			}
		}
		return sc, "", "", false
	}
	if name := q.Get("cluster"); name != "" {
		for _, c := range clusters {
			if c.Name == name {
				return scope.Scope{Kind: scope.Cluster, ClusterID: c.ID}, c.ID, c.Name, true
			}
		}
	}
	return sc, "", "", false
}

// consoleScriptSQL loads a saved script's SQL text for a console hand-off,
// respecting the viewer's visibility (GetVisibleScript). Returns "" for an
// absent/blank/invalid id or a script the viewer may not see.
func (s *Server) consoleScriptSQL(r *http.Request, idStr string) string {
	if idStr == "" {
		return ""
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		return ""
	}
	sc, found, err := s.conf.GetVisibleScript(r.Context(), id, viewerFromContext(r), groupFromContext(r))
	if err != nil || !found {
		return ""
	}
	return sc.SQLText
}

// renderConsoleBody re-renders the swap unit for the current request.
func (s *Server) renderConsoleBody(w http.ResponseWriter, r *http.Request) {
	vm, ok := s.buildConsoleVM(w, r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.ConsoleBody(vm).Render(r.Context(), w)
}

// shortHash renders a display-only abbreviation ("6c1d…e44"). NEVER use it as a
// lookup key — the multibyte ellipsis is not URL/attribute round-trip safe; use
// the full hex (Run.ID) for restore tokens and sessions.Get.
func shortHash(h []byte) string {
	s := hex.EncodeToString(h)
	if len(s) < 7 {
		return s
	}
	return s[:4] + "…" + s[len(s)-3:]
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// handleConsoleExport streams the caller's latest cached result FOR THIS
// CLUSTER as a CSV or SQL-INSERT download. The cluster comes from the ?scope=
// param, scoping the read so an export cannot return another cluster's run.
//
// A download is a fresh bulk egress of the literal T2 rows, so — unlike a
// paginated re-read of the same page — it is fail-closed re-gated on an active
// session grant and written to the audit log before any byte is streamed. A
// grant that has expired or been revoked since the run therefore cannot export
// the cached result, and no T2 egress is unaudited ("EVERY T2 READ APPEARS
// HERE — NO EXCEPTIONS").
func (s *Server) handleConsoleExport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	actor := actorFromContext(r)

	clusters, err := s.conf.ListClusters(ctx)
	if err != nil {
		http.Error(w, "load clusters", http.StatusInternalServerError)
		return
	}
	_, clusterID, clusterName, ok := consoleClusterFromQuery(r.URL.Query(), clusters)
	if !ok {
		http.NotFound(w, r)
		return
	}

	// Fail-closed grant re-check — no grant, no export, no bytes.
	if _, granted, err := s.grants.ActiveGrant(ctx, clusterID, clusterName, actor); err != nil {
		http.Error(w, "grant check", http.StatusInternalServerError)
		return
	} else if !granted {
		http.Error(w, "no active session grant on this cluster", http.StatusForbidden)
		return
	}

	run, ok := s.sessions.Latest(actor, clusterID)
	if !ok {
		http.Error(w, "no result to export", http.StatusNotFound)
		return
	}

	format := r.URL.Query().Get("format")
	var body, filename, ctype string
	switch format {
	case "sql":
		body, filename, ctype = console.SQLInserts(run.Result, "result"), "lynceus-result.sql", "application/sql"
	default:
		format = "csv"
		body, filename, ctype = console.CSV(run.Result), "lynceus-result.csv", "text/csv"
	}

	// Strict T2 audit BEFORE streaming any literal bytes — fail closed, mirroring
	// handleConsoleRun. This export egress is its own tier-2 audit event.
	serverID, _, _, _ := s.resolveConsoleTarget(ctx, clusterID, run.Node, run.Database)
	if _, err := s.conf.AppendAuditReturning(ctx, store.AuditEntry{
		Actor: actor, Action: "console.result.export", ServerID: serverID, DataTier: 2,
		Detail: map[string]any{
			"format":           format,
			"row_count":        run.Result.RowCount(),
			"statement_sha256": sha256Hex(run.SQL),
			"target_node":      run.Node,
			"target_database":  run.Database,
		},
	}); err != nil {
		http.Error(w, "audit failed — export withheld", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", ctype+"; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename="+filename)
	_, _ = w.Write([]byte(body))
}
