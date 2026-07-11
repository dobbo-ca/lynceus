package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/dobbo-ca/lynceus/internal/console"
	"github.com/dobbo-ca/lynceus/internal/scope"
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

// consoleResultVM is filled in Task 4/5.
func consoleResultVM(_ console.Run, _, _ int, _ string) web.ConsoleResultVM {
	return web.ConsoleResultVM{}
}

// consoleHistoryVM is filled in Task 4.
func consoleHistoryVM(_ []console.Run, _ string) []web.ConsoleHistoryRow {
	return nil
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

// handleConsoleRun is implemented in Task 4.
func (s *Server) handleConsoleRun(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}

// handleConsoleExport is implemented in Task 5.
func (s *Server) handleConsoleExport(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}
