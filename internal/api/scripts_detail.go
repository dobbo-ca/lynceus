package api

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/dobbo-ca/lynceus/web"
)

// parseScriptID extracts and validates the {id} path value.
func parseScriptID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// handleScriptDetailPage renders the full script detail surface inside the
// design shell.
func (s *Server) handleScriptDetailPage(w http.ResponseWriter, r *http.Request) {
	id, ok := parseScriptID(r)
	if !ok {
		http.Error(w, "bad script id", http.StatusBadRequest)
		return
	}
	viewer := viewerFromContext(r)
	group := groupFromContext(r)
	// Visibility-gated read: GetVisibleScript returns found=false for a
	// missing OR non-visible id, so a non-owner (even an admin) gets a 404 on
	// someone else's PERSONAL script and never sees its SQL. Do NOT use the
	// ungated GetScript here.
	sc, found, err := s.conf.GetVisibleScript(r.Context(), id, viewer, group)
	if err != nil {
		http.Error(w, "load script", http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(w, "script not found", http.StatusNotFound)
		return
	}

	mine := sc.Owner == viewer
	vm := web.ScriptDetailVM{
		ID:          sc.ID,
		Name:        sc.Name,
		Description: sc.Description,
		SQLText:     sc.SQLText,
		Scope:       sc.Scope,
		ScopeColor:  web.ScriptScopeColor(sc.Scope),
		Owner:       sc.Owner,
		SavedAge:    web.RelativeAge(sc.CreatedAt, timeNow()),
		VisibleTo:   web.ScriptVisibleTo(sc.Scope, sc.Owner, sc.OwnerGroup, mine),
		Mine:        mine,
		ManagedBy:   sc.Owner,
		Run:         s.buildScriptRunVM(r, sc.ID),
	}
	if mine {
		vm.ScopeOptions = scopeOptions(sc.Scope)
	}
	shell := s.scriptsShell(r, "scriptdetail", "Lynceus — "+sc.Name)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.ScriptDetailPage(shell, vm).Render(r.Context(), w)
}

// scopeOptions builds the three owner scope-switch buttons, marking current.
func scopeOptions(current string) []web.ScriptScopeOption {
	out := make([]web.ScriptScopeOption, 0, 3)
	for _, sc := range []string{"GLOBAL", "TEAM", "PERSONAL"} {
		out = append(out, web.ScriptScopeOption{Label: sc, Active: sc == current})
	}
	return out
}

// scriptWriteStatus threads store sentinels to HTTP statuses.
func scriptWriteStatus(err error) int {
	switch {
	case errors.Is(err, store.ErrScriptForbidden):
		return http.StatusForbidden
	case errors.Is(err, store.ErrScriptNotFound):
		return http.StatusNotFound
	default:
		return http.StatusInternalServerError
	}
}

// handleScriptRunCard re-renders the RUN card fragment as the user searches,
// selects a target, and picks node/database. State is carried entirely in
// query params (?q= ?target= ?node= ?db=), keeping the flow stateless.
func (s *Server) handleScriptRunCard(w http.ResponseWriter, r *http.Request) {
	id, ok := parseScriptID(r)
	if !ok {
		http.Error(w, "bad script id", http.StatusBadRequest)
		return
	}
	vm := s.buildScriptRunVM(r, id)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.ScriptRunCard(vm).Render(r.Context(), w)
}

// buildScriptRunVM resolves the RUN card from the request's run-state params.
func (s *Server) buildScriptRunVM(r *http.Request, scriptID int64) web.ScriptRunVM {
	qv := r.URL.Query()
	q := qv.Get("q")
	target := qv.Get("target")

	vm := web.ScriptRunVM{
		ScriptID:       scriptID,
		TargetQuery:    q,
		SelectedTarget: target,
		Targets:        s.scriptTargetOptions(r, q, target),
		RunLabel:       "SEARCH & SELECT A TARGET",
		RunHint:        "THE RUN LANDS IN THE SQL CONSOLE — EVERY STATEMENT IS AUDITED.",
	}
	if target == "" {
		return vm
	}
	vm.Selected = true

	kind, cluster, fixed := parseTargetValue(target)
	nodes, dbs := s.clusterNodesAndDBs(r, cluster)

	nodeFixed, dbFixed := "", ""
	switch kind {
	case "node":
		nodeFixed, nodes = fixed, []string{fixed}
	case "db":
		dbFixed, dbs = fixed, []string{fixed}
	}

	node := chooseValue(qv.Get("node"), nodeFixed, nodes)
	db := chooseValue(qv.Get("db"), dbFixed, dbs)

	vm.NodeChips = runChips(target, "node", nodes, node, node, db)
	vm.DBChips = runChips(target, "db", dbs, db, node, db)

	if node != "" && db != "" {
		vm.RunReady = true
		vm.RunLabel = "RUN ON " + node + " · " + db + " →"
		// run=true == execute-intent. The console EXECUTES immediately when a
		// session grant is active on the target cluster and otherwise shows the
		// grant gate. That grant decision lives in the console (ly-ae6.8), which
		// owns live grant state; this surface always signals the intent to run.
		vm.RunHint = "HANDS OFF TO THE SQL CONSOLE TO RUN — IF A SESSION GRANT IS ACTIVE ON THIS CLUSTER IT EXECUTES IMMEDIATELY, OTHERWISE THE CONSOLE ASKS YOU TO REQUEST ONE FIRST. EVERY STATEMENT IS AUDITED."
		vm.RunHref = consoleHandoffURL(scriptID, cluster, node, db, true)
	} else {
		vm.RunLabel = "SELECT NODE & DATABASE TO RUN"
	}
	return vm
}

// scriptTargetOptions loads the fleet target index, filters by free-text q,
// and marks the option whose Value == selectedValue active.
func (s *Server) scriptTargetOptions(r *http.Request, q, selectedValue string) []web.ScriptTargetOption {
	targets, err := s.conf.ListScriptTargets(r.Context())
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []web.ScriptTargetOption
	add := func(label, kind, color, value string) {
		if seen[value] {
			return
		}
		seen[value] = true
		if q != "" && !contains(label, toLower(q)) && !contains(kind, toLower(q)) {
			return
		}
		out = append(out, web.ScriptTargetOption{
			Label: label, Kind: kind, KindColor: color, Value: value,
			Active: value == selectedValue,
		})
	}
	for _, tg := range targets {
		add(tg.Cluster, "CLUSTER", "var(--text)", "cluster|"+tg.Cluster+"|")
		add(tg.Node, "NODE", "var(--infoT)", "node|"+tg.Cluster+"|"+tg.Node)
		if tg.Database != "" {
			add(tg.Cluster+"/"+tg.Database, "DATABASE", "var(--mut)", "db|"+tg.Cluster+"|"+tg.Database)
		}
	}
	return out
}

// clusterNodesAndDBs returns the distinct node names and database names in a
// cluster from the fleet target index.
func (s *Server) clusterNodesAndDBs(r *http.Request, cluster string) (nodes, dbs []string) {
	targets, err := s.conf.ListScriptTargets(r.Context())
	if err != nil {
		return nil, nil
	}
	seenN, seenD := map[string]bool{}, map[string]bool{}
	for _, tg := range targets {
		if tg.Cluster != cluster {
			continue
		}
		if tg.Node != "" && !seenN[tg.Node] {
			seenN[tg.Node] = true
			nodes = append(nodes, tg.Node)
		}
		if tg.Database != "" && !seenD[tg.Database] {
			seenD[tg.Database] = true
			dbs = append(dbs, tg.Database)
		}
	}
	return nodes, dbs
}

// runChips builds node or database chips, each carrying the accumulated
// run-state (target + node + db) so a click advances the selection.
func runChips(target, dim string, values []string, active, curNode, curDB string) []web.ScriptTargetChip {
	out := make([]web.ScriptTargetChip, 0, len(values))
	for _, v := range values {
		node, db := curNode, curDB
		if dim == "node" {
			node = v
		} else {
			db = v
		}
		vals := url.Values{}
		vals.Set("target", target)
		if node != "" {
			vals.Set("node", node)
		}
		if db != "" {
			vals.Set("db", db)
		}
		out = append(out, web.ScriptTargetChip{Label: v, Value: vals.Encode(), Active: v == active})
	}
	return out
}

// parseTargetValue splits a "<kind>|<cluster>|<node-or-db>" target value.
func parseTargetValue(v string) (kind, cluster, fixed string) {
	parts := strings.SplitN(v, "|", 3)
	for len(parts) < 3 {
		parts = append(parts, "")
	}
	return parts[0], parts[1], parts[2]
}

// chooseValue returns fixed if set, else param when it is a member of list,
// else "".
func chooseValue(param, fixed string, list []string) string {
	if fixed != "" {
		return fixed
	}
	for _, v := range list {
		if v == param {
			return param
		}
	}
	return ""
}

// consoleHandoffURL builds the SQL-console hand-off URL (see the ly-ae6.8
// integration contract). run=false is a LOAD (preload SQL + preselect target,
// do NOT execute — the list/search LoadHref). run=true is the RUN card's
// execute-intent: it adds &run=1 so the console executes immediately when a
// session grant is active on the cluster, else shows the grant gate.
func consoleHandoffURL(scriptID int64, cluster, node, db string, run bool) string {
	vals := url.Values{}
	vals.Set("script", strconv.FormatInt(scriptID, 10))
	if cluster != "" {
		vals.Set("cluster", cluster)
	}
	if node != "" {
		vals.Set("node", node)
	}
	if db != "" {
		vals.Set("db", db)
	}
	if run {
		vals.Set("run", "1")
	}
	return "/console?" + vals.Encode()
}
