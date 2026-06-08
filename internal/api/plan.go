package api

import (
	"net/http"
	"time"

	"github.com/dobbo-ca/lynceus/web"
)

// handlePlanPage renders the full plan-visualization page for the
// (server, fingerprint) pair given in the query string.
func (s *Server) handlePlanPage(w http.ResponseWriter, r *http.Request) {
	vm := s.fetchPlan(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.PlanPage(vm).Render(r.Context(), w)
}

// handlePlanPartial renders just the plan-view fragment, for HTMX in-place
// swaps.
func (s *Server) handlePlanPartial(w http.ResponseWriter, r *http.Request) {
	vm := s.fetchPlan(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.PlanView(vm).Render(r.Context(), w)
}

// fetchPlan loads the most-recent stored plan for (?server, ?fp) and maps
// it to a view-model. A missing key, a read error, or zero rows all yield
// an Empty PlanVM that still echoes the requested identifiers, so the page
// can render its "no plan stored" branch.
func (s *Server) fetchPlan(r *http.Request) web.PlanVM {
	q := r.URL.Query()
	serverID := q.Get("server")
	fp := q.Get("fp")

	now := time.Now().UTC()
	since := now.AddDate(0, 0, -30) // same 30d window as fetchTop (dashboard.go:27)

	plans, err := s.stats.TopPlansByQuery(r.Context(), serverID, fp, since, now, 1)
	if err != nil || len(plans) == 0 {
		return web.ToPlanVM(serverID, nil) // Empty, identifiers preserved
	}
	return web.ToPlanVM(serverID, plans[0].Plan) // most-recent first
}
