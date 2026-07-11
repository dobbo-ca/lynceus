package api

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/dobbo-ca/lynceus/web"
)

// timeNow is the clock used by the Saved Scripts handlers. A package var so
// tests can pin it if they need deterministic relative ages.
var timeNow = time.Now

// viewerFromContext is the principal whose visibility + ownership the Saved
// Scripts surface resolves against. Every Saved Scripts handler only ever
// executes under DevAuth (withAuth 401s all non-static requests otherwise —
// see server.go), so the X-Dev-Actor impersonation header below is inherently
// a dev-only convenience: it lets tests exercise non-owner / non-visible
// paths without wiring real auth. Real OIDC actor resolution replaces this
// whole helper in Milestone 5 (see actorFromContext).
func viewerFromContext(r *http.Request) string {
	if a := r.Header.Get("X-Dev-Actor"); a != "" {
		return a
	}
	return actorFromContext(r)
}

// groupFromContext is the viewer's group for TEAM-scope visibility. Under
// DevAuth this is the fixed dba-oncall group the design references; real
// group membership arrives with OIDC/SCIM (Milestone 5).
func groupFromContext(_ *http.Request) string { return "dba-oncall" }

// isAdminFromContext reports whether the viewer may change access / delete
// scripts they do not own. Under DevAuth the dev principal is an admin, unless
// the dev-only X-Dev-Admin: false header drops the privilege — which is how
// tests reach the handler-level 403 (owner-or-admin) branch. Real RBAC
// arrives in Milestone 5.
func (s *Server) isAdminFromContext(r *http.Request) bool {
	if !s.cfg.DevAuth {
		return false
	}
	return r.Header.Get("X-Dev-Admin") != "false"
}

// scriptsShell builds the design shell view for a Saved Scripts screen,
// highlighting the given nav screen key ("scripts" or "scriptdetail") in the
// per-scope sidebar and setting the page title.
func (s *Server) scriptsShell(r *http.Request, activeScreen, title string) web.ShellView {
	shell := s.buildShellView(r)
	shell.Sidebar = web.Sidebar(shell.Scope, shell.ScopeLabel, web.DefaultEngines(), activeScreen)
	shell.Title = title
	return shell
}

// handleSavedScriptsPage renders the full Saved Scripts list surface inside
// the design shell.
func (s *Server) handleSavedScriptsPage(w http.ResponseWriter, r *http.Request) {
	vm, err := s.savedScriptsVM(r)
	if err != nil {
		http.Error(w, "load scripts", http.StatusInternalServerError)
		return
	}
	shell := s.scriptsShell(r, "scripts", "Lynceus — Saved Scripts")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.SavedScriptsPage(shell, vm).Render(r.Context(), w)
}

// handleSavedScriptsTable renders just the table fragment for HTMX filtering.
func (s *Server) handleSavedScriptsTable(w http.ResponseWriter, r *http.Request) {
	vm, err := s.savedScriptsVM(r)
	if err != nil {
		http.Error(w, "load scripts", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.SavedScriptsTable(vm).Render(r.Context(), w)
}

// savedScriptsVM loads the viewer's visible scripts, applies the free-text
// filter, and maps them to the list view-model. It returns the store error so
// a config-DB outage surfaces as a 500 (not a silent empty "no scripts
// match" list) — the caller decides the HTTP status.
func (s *Server) savedScriptsVM(r *http.Request) (web.SavedScriptsVM, error) {
	viewer := viewerFromContext(r)
	group := groupFromContext(r)
	q := r.URL.Query().Get("q")

	scripts, err := s.conf.ListVisibleScripts(r.Context(), viewer, group)
	if err != nil {
		return web.SavedScriptsVM{}, fmt.Errorf("list visible scripts: %w", err)
	}
	now := timeNow()
	rows := make([]web.SavedScriptRow, 0, len(scripts))
	for i := range scripts {
		sc := scripts[i]
		if q != "" && !scriptMatches(sc, q) {
			continue
		}
		mine := sc.Owner == viewer
		rows = append(rows, web.SavedScriptRow{
			ID:          sc.ID,
			Name:        sc.Name,
			Description: sc.Description,
			Scope:       sc.Scope,
			ScopeColor:  web.ScriptScopeColor(sc.Scope),
			VisibleTo:   web.ScriptVisibleTo(sc.Scope, sc.Owner, sc.OwnerGroup, mine),
			Owner:       sc.Owner,
			SavedAge:    web.RelativeAge(sc.CreatedAt, now),
			Mine:        mine,
			DetailHref:  fmt.Sprintf("/scripts/%d", sc.ID),
			LoadHref:    fmt.Sprintf("/console?script=%d", sc.ID),
		})
	}
	return web.SavedScriptsVM{
		Query:   q,
		SubLine: scriptsSubLine(len(scripts)),
		Count:   len(rows),
		Rows:    rows,
	}, nil
}

func scriptsSubLine(total int) string {
	return fmt.Sprintf("%d SCRIPTS · GLOBAL — EVERYONE · TEAM — DBA-ONCALL · PERSONAL — OWNER ONLY", total)
}

// scriptMatches is the free-text filter: case-insensitive substring over
// name, description, and scope.
func scriptMatches(sc store.SavedScript, q string) bool {
	q = toLower(q)
	return contains(sc.Name, q) || contains(sc.Description, q) || contains(sc.Scope, q)
}

func toLower(s string) string { return strings.ToLower(s) }
func contains(hay, needleLower string) bool {
	return strings.Contains(strings.ToLower(hay), needleLower)
}
