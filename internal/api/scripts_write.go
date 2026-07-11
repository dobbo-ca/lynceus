package api

import (
	"fmt"
	"net/http"

	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/dobbo-ca/lynceus/web"
)

// handleScriptCreate inserts a saved script from the console SAVE ▾ form
// (POST /scripts) and redirects to the new script's detail page. The console
// (ly-ae6.8) posts name/description/sql/scope; this is the CRUD create seam.
func (s *Server) handleScriptCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	scope := r.PostForm.Get("scope")
	if !store.ValidScriptScope(scope) {
		http.Error(w, "invalid scope", http.StatusBadRequest)
		return
	}
	viewer := viewerFromContext(r)
	group := ""
	if scope == "TEAM" {
		group = groupFromContext(r)
	}
	sc, err := s.conf.CreateScript(r.Context(), store.CreateScriptInput{
		Name:        r.PostForm.Get("name"),
		Description: r.PostForm.Get("description"),
		SQLText:     r.PostForm.Get("sql"),
		Scope:       scope,
		Owner:       viewer,
		OwnerGroup:  group,
	})
	if err != nil {
		http.Error(w, "create script", http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/scripts/%d", sc.ID), http.StatusSeeOther)
}

// handleScriptScopeChange changes a script's scope (owner or admin only) and
// re-renders the ACCESS card fragment. The store append-audits the change.
func (s *Server) handleScriptScopeChange(w http.ResponseWriter, r *http.Request) {
	id, ok := parseScriptID(r)
	if !ok {
		http.Error(w, "bad script id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	newScope := r.PostForm.Get("scope")
	sc, err := s.conf.SetScriptScope(r.Context(), id, newScope,
		viewerFromContext(r), s.isAdminFromContext(r))
	if err != nil {
		http.Error(w, "set scope", scriptWriteStatus(err))
		return
	}
	viewer := viewerFromContext(r)
	mine := sc.Owner == viewer
	vm := web.ScriptDetailVM{
		ID:         sc.ID,
		Scope:      sc.Scope,
		ScopeColor: web.ScriptScopeColor(sc.Scope),
		Owner:      sc.Owner,
		VisibleTo:  web.ScriptVisibleTo(sc.Scope, sc.Owner, sc.OwnerGroup, mine),
		Mine:       mine,
		ManagedBy:  sc.Owner,
	}
	if mine {
		vm.ScopeOptions = scopeOptions(sc.Scope)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.ScriptAccessCard(vm).Render(r.Context(), w)
}

// handleScriptDelete deletes a script (owner or admin only) and re-renders
// the list-table fragment so the row disappears in place.
func (s *Server) handleScriptDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := parseScriptID(r)
	if !ok {
		http.Error(w, "bad script id", http.StatusBadRequest)
		return
	}
	if err := s.conf.DeleteScript(r.Context(), id,
		viewerFromContext(r), s.isAdminFromContext(r)); err != nil {
		http.Error(w, "delete script", scriptWriteStatus(err))
		return
	}
	// Refresh the table for the HTMX swap.
	vm, err := s.savedScriptsVM(r)
	if err != nil {
		http.Error(w, "load scripts", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.SavedScriptsTable(vm).Render(r.Context(), w)
}
