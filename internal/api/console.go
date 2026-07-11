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
	vm, ok := s.buildConsoleVM(w, r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	sh := s.buildShellView(r)
	sh.Sidebar = web.Sidebar(sh.Scope, sh.ScopeLabel, web.DefaultEngines(), "console")
	vm.Shell = sh
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

	sc := scope.Parse(q.Get("scope"))
	clusterID := sc.ClusterID
	if clusterID == "" {
		return web.ConsoleVM{}, false
	}

	clusters, err := s.conf.ListClusters(ctx)
	if err != nil {
		return web.ConsoleVM{}, false
	}
	var clusterName string
	for _, c := range clusters {
		if c.ID == clusterID {
			clusterName = c.Name
			break
		}
	}
	if clusterName == "" {
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
		// Result + history are wired from the (actor, cluster) session cache.
		// The restore-aware version of this block lands in Task 5.
		page, pageSize := consolePage(r), consolePageSize(r)
		if run, ok := s.sessions.Latest(actor, clusterID); ok {
			vm.HasResult = true
			vm.Result = consoleResultVM(run, page, pageSize, scopeEnc)
			vm.Editor.SQL = run.SQL
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

// consoleResultVM renders the cached run's full result as page 0. Real
// pagination (prev/next math, pagesize chips, copy guard) lands in Task 5.
func consoleResultVM(run console.Run, _, _ int, scopeEnc string) web.ConsoleResultVM {
	res := run.Result
	total := res.RowCount()
	base := consoleChipHref("/partial/console", scopeEnc, run.Node, run.Database)
	return web.ConsoleResultVM{
		Columns:    res.Columns,
		Rows:       res.Rows,
		TotalRows:  total,
		DurationMs: res.DurationMs,
		Hash:       run.ShortHash,
		PageLabel:  fmt.Sprintf("ROWS %d–%d OF %d", min1(total), total, total),
		PrevHref:   base,
		NextHref:   base,
		CsvHref:    consoleExportHref(scopeEnc, "csv"),
		SqlHref:    consoleExportHref(scopeEnc, "sql"),
	}
}

func min1(n int) int {
	if n == 0 {
		return 0
	}
	return 1
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

// consolePageSize is finalized in Task 5 (cookie persistence); default 25 now.
func consolePageSize(r *http.Request) int {
	if n, err := strconv.Atoi(r.URL.Query().Get("pagesize")); err == nil && consoleValidPageSize(n) {
		return n
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
	sql := strings.TrimSpace(r.FormValue("sql"))
	serverID, _, ok, err := s.resolveConsoleTarget(ctx, clusterID, node, db)
	if err != nil {
		http.Error(w, "resolve target", http.StatusInternalServerError)
		return
	}
	if !ok || sql == "" {
		// Target not fully resolved or empty statement — re-render (RUN inert).
		s.renderConsoleBody(w, r)
		return
	}

	res, err := s.exec.Execute(ctx, console.Statement{
		ClusterID: clusterID, ClusterName: clusterName, Node: node, Database: db,
		ServerID: serverID, SQL: sql, RowLimit: consoleRowLimit, TimeoutSecs: consoleTimeoutSecs, Actor: actor,
	})
	if err != nil {
		http.Error(w, "execute", http.StatusInternalServerError)
		return
	}

	// Strict audit BEFORE returning results — fail closed. Detail is a T2
	// governance artifact (statement text is permitted in the audit_log).
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
		// Fail closed: the run happened but could not be recorded — withhold results.
		http.Error(w, "audit failed — result withheld", http.StatusInternalServerError)
		return
	}

	// ID is the FULL hex (URL-safe lookup key for restore/Get); ShortHash is
	// display-only. Scope the cache entry to (actor, clusterID).
	s.sessions.Append(actor, clusterID, console.Run{
		ID: hex.EncodeToString(rec.RowHash), ShortHash: shortHash(rec.RowHash), ClusterID: clusterID,
		At: rec.At, SQL: sql, Node: node, Database: db,
		Result: res, DurationMs: res.DurationMs,
	})

	// The freshly-cached run drives the re-render; carry the selection so the
	// picker stays resolved (scope is already in the request URL).
	r2 := r.Clone(ctx)
	q := r2.URL.Query()
	q.Set("node", node)
	q.Set("db", db)
	r2.URL.RawQuery = q.Encode()
	s.renderConsoleBody(w, r2)
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

// handleConsoleExport is implemented in Task 5.
func (s *Server) handleConsoleExport(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}
