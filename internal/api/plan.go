package api

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/dobbo-ca/lynceus/web"
)

// handlePlanPage renders the full plan-visualization page for the
// (server, fingerprint) pair given in the query string.
func (s *Server) handlePlanPage(w http.ResponseWriter, r *http.Request) {
	sv := s.shellViewFor(r, "plans")
	vm := s.fetchPlan(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.PlanPage(sv, vm).Render(r.Context(), w)
}

// handlePlanPartial renders just the plan-view fragment, for HTMX in-place
// swaps.
func (s *Server) handlePlanPartial(w http.ResponseWriter, r *http.Request) {
	vm := s.fetchPlan(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.PlanView(vm).Render(r.Context(), w)
}

// fetchPlan loads up to 5 plan variants for (?server, ?fp), selects one via
// ?plan, decorates it (problem-node detection + ?node selection), and echoes
// the identifiers. A missing key / read error / zero rows yields an Empty PlanVM.
func (s *Server) fetchPlan(r *http.Request) web.PlanVM {
	q := r.URL.Query()
	serverID := q.Get("server")
	fp := q.Get("fp")
	now := time.Now().UTC()
	since := now.AddDate(0, 0, -30)

	plans, err := s.stats.TopPlansByQuery(r.Context(), serverID, fp, since, now, 5)
	if err != nil || len(plans) == 0 {
		return web.ToPlanVM(serverID, nil)
	}
	planIdx := atoiDefault(q.Get("plan"), 0)
	if planIdx < 0 || planIdx >= len(plans) {
		planIdx = 0
	}
	vm := web.ToPlanVM(serverID, plans[planIdx].Plan)
	vm.Fingerprint = fp
	vm.VariantIdx = planIdx
	for i := range plans {
		vm.Variants = append(vm.Variants, web.PlanVariant{
			FP:       shortFP(fp),
			Label:    fmt.Sprintf("variant %d", i+1),
			Selected: i == planIdx,
			Href:     fmt.Sprintf("/plan?server=%s&fp=%s&plan=%d", serverID, fp, i),
		})
	}
	web.DecoratePlan(&vm, atoiDefault(q.Get("node"), 0))
	return vm
}

func atoiDefault(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return def
}

func shortFP(fp string) string {
	if len(fp) > 8 {
		return fp[:8]
	}
	return fp
}
