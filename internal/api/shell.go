package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/a-h/templ"

	"github.com/dobbo-ca/lynceus/internal/scope"
	"github.com/dobbo-ca/lynceus/web"
)

// handleFleet serves the fleet-scope landing wrapped in the design shell: the
// top bar/nav come from the shell view (scoped by ?scope), the main body is the
// Fleet dashboard (ly-ae6.4). Both consume the shared ?range param.
func (s *Server) handleFleet(w http.ResponseWriter, r *http.Request) {
	// The /fleet landing highlights the fleet Overview at fleet scope; a scoped
	// ?scope= on this route highlights nothing (its dedicated screen is elsewhere).
	activeScreen := ""
	if scope.Parse(r.URL.Query().Get("scope")).IsFleet() {
		activeScreen = "fleet"
	}
	vm := s.buildShellView(r, activeScreen)
	fleet := s.fetchFleet(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.FleetShellPage(vm, fleet).Render(r.Context(), w)
}

// handleScopeOptions serves the searchable SCOPE picker option list (HTMX).
func (s *Server) handleScopeOptions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	active := scope.Parse(r.URL.Query().Get("scope-active"))
	opts := s.scopeOptions(r.Context(), q, active)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.ScopeOptionsList(opts, q).Render(r.Context(), w)
}

// buildShellView parses scope + range from the request, enumerates the picker
// options, resolves the active scope's display label, and assembles ShellView.
// activeScreen is the design screen id of the page being rendered — it drives
// both the sidebar's active highlight and the range control's on-screen hrefs
// (RangeOptions), so changing the range keeps the user on the current screen.
func (s *Server) buildShellView(r *http.Request, activeScreen string) web.ShellView {
	qv := r.URL.Query()
	active := scope.Parse(qv.Get("scope"))
	rng := web.ParseRange(qv.Get("range"))
	opts := s.scopeOptions(r.Context(), "", active)

	label := "FLEET"
	if !active.IsFleet() {
		label = resolveScopeLabel(opts, active)
	}

	return web.ShellView{
		Scope:      active,
		ScopeLabel: label,
		Scoped:     !active.IsFleet(),
		ClearHref:  templ.SafeURL("/"),
		LogoHref:   templ.SafeURL("/"),
		Range:      rng,
		Ranges:     web.RangeOptions(rng, active, activeScreen),
		PollSecs:   3,
		Options:    opts,
		Sidebar:    web.Sidebar(active, label, web.DefaultEngines(), activeScreen),
		// Static dev identity until OIDC (ly-8b0.1); dev-only, T1.
		User:  web.ShellUser{Name: "dev-admin", Group: "DBA-ONCALL", T2Granted: true},
		Title: "Lynceus — " + label,
	}
}

// scopeOptions enumerates scopeable entities from the config store: clusters
// (Depth 0), then each cluster's nodes and cluster-qualified databases (both
// Depth 1). A database is identified by cluster + name, so it is a CLUSTER-level
// entity, not a node child: distinct database names are collected across all of
// the cluster's instances and emitted ONCE under the cluster, after its nodes —
// matching the design's flat `pad: 1` placement (see docs/design/Lynceus.dc.html
// `scopes` array and README "Scope Model"). Consequence, by design: a database
// streamed by both a primary and a replica appears exactly once under its
// cluster (not indented beneath whichever node happened to report it first).
// Filtered case-insensitively over label + kind. Provider/engine search columns
// do not exist yet (see the plan's backend-gaps note); poolers are not modeled
// yet, so none are emitted.
func (s *Server) scopeOptions(ctx context.Context, q string, active scope.Scope) []web.ScopeOption {
	clusters, err := s.conf.ListClusters(ctx)
	if err != nil {
		return nil
	}
	activeKey := active.Encode()
	ql := strings.ToLower(strings.TrimSpace(q))

	var out []web.ScopeOption
	add := func(sc scope.Scope, label, kind string, depth int) {
		if ql != "" && !strings.Contains(strings.ToLower(label+" "+kind), ql) {
			return
		}
		key := sc.Encode()
		out = append(out, web.ScopeOption{
			Label:    label,
			Kind:     kind,
			Depth:    depth,
			ScopeKey: key,
			Href:     web.ScopeHref(sc),
			Current:  key == activeKey,
		})
	}

	for _, cl := range clusters {
		add(scope.Scope{Kind: scope.Cluster, ClusterID: cl.ID}, cl.Name, "CLUSTER", 0)

		instances, err := s.conf.ListInstances(ctx, cl.ID)
		if err != nil {
			continue
		}
		// Emit the cluster's nodes (Depth 1), collecting its distinct database
		// names as we go; then emit those databases once at the cluster level
		// (also Depth 1), in first-seen order.
		seenDB := map[string]bool{}
		var dbNames []string
		for _, in := range instances {
			add(scope.Scope{Kind: scope.Node, ClusterID: cl.ID, NodeID: in.ID},
				cl.Name+" / "+in.Name, "NODE", 1)

			streams, err := s.conf.ListServerStreams(ctx, in.ID)
			if err != nil {
				continue
			}
			for _, st := range streams {
				if st.DatabaseName == "" || seenDB[st.DatabaseName] {
					continue
				}
				seenDB[st.DatabaseName] = true
				dbNames = append(dbNames, st.DatabaseName)
			}
		}
		for _, db := range dbNames {
			add(scope.Scope{Kind: scope.Database, ClusterID: cl.ID, Database: db},
				cl.Name+"/"+db, "DATABASE", 1)
		}
	}
	return out
}

// resolveScopeLabel finds the display label for the active scope in the full
// (unfiltered) option list; falls back to "FLEET" if the entity is gone.
func resolveScopeLabel(opts []web.ScopeOption, active scope.Scope) string {
	enc := active.Encode()
	for _, o := range opts {
		if o.ScopeKey == enc {
			return o.Label
		}
	}
	return "FLEET"
}
