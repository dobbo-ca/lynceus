package api

import (
	"fmt"
	"net/http"

	"github.com/dobbo-ca/lynceus/web"
)

// handleScriptSearch renders the console's saved-script search dropdown:
// focus (empty q) browses every visible script; typing filters by name /
// description / scope. Cross-scope: GLOBAL + TEAM(viewer group) + own
// PERSONAL, per ListVisibleScripts.
func (s *Server) handleScriptSearch(w http.ResponseWriter, r *http.Request) {
	viewer := viewerFromContext(r)
	group := groupFromContext(r)
	q := r.URL.Query().Get("q")

	scripts, err := s.conf.ListVisibleScripts(r.Context(), viewer, group)
	if err != nil {
		http.Error(w, "search scripts", http.StatusInternalServerError)
		return
	}
	var items []web.ScriptSearchItem
	for i := range scripts {
		sc := scripts[i]
		if q != "" && !scriptMatches(sc, q) {
			continue
		}
		items = append(items, web.ScriptSearchItem{
			Name:        sc.Name,
			Description: sc.Description,
			Scope:       sc.Scope,
			ScopeColor:  web.ScriptScopeColor(sc.Scope),
			LoadHref:    fmt.Sprintf("/console?script=%d", sc.ID),
		})
	}
	vm := web.ScriptSearchVM{Items: items, Empty: len(items) == 0}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.ScriptSearchResults(vm).Render(r.Context(), w)
}
