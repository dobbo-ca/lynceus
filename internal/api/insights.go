package api

import (
	"net/http"
	"time"

	"github.com/dobbo-ca/lynceus/internal/insight"
	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
	"github.com/dobbo-ca/lynceus/web"
)

// handleInsightsPage renders the full insights page.
func (s *Server) handleInsightsPage(w http.ResponseWriter, r *http.Request) {
	rows := s.fetchInsights(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.InsightsPage(rows).Render(r.Context(), w)
}

// handleInsightsPartial renders just the table fragment, used by HTMX for
// in-place auto-refresh.
func (s *Server) handleInsightsPartial(w http.ResponseWriter, r *http.Request) {
	rows := s.fetchInsights(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.InsightsTable(rows).Render(r.Context(), w)
}

// fetchInsights enumerates plan keys over the last 30 days, loads recent plans
// per key, runs the detection engine, and maps each Insight to a view-model.
// Errors degrade to nil rows (same convention as fetchTop, dashboard.go:29).
func (s *Server) fetchInsights(r *http.Request) []web.InsightRow {
	now := time.Now().UTC()
	since := now.AddDate(0, 0, -30) // same 30d window as fetchTop (dashboard.go:27)

	keys, err := s.stats.ListPlanKeys(r.Context(), since, now, 200)
	if err != nil {
		return nil
	}

	var out []web.InsightRow
	for _, k := range keys {
		plans, err := s.stats.TopPlansByQuery(r.Context(), k.ServerID, k.Fingerprint, since, now, 10)
		if err != nil {
			continue
		}
		qps := make([]*lynceusv1.QueryPlan, 0, len(plans))
		for _, p := range plans {
			qps = append(qps, p.Plan)
		}
		for _, in := range insight.DetectPlans(qps) {
			out = append(out, web.InsightRow{
				Kind:         string(in.Kind),
				Severity:     string(in.Severity),
				Fingerprint:  in.Fingerprint,
				Relation:     in.Relation,
				NodePath:     in.NodePath,
				RowsScanned:  in.RowsScanned,
				RowsReturned: in.RowsReturned,
				Detail:       in.Detail,
				ServerID:     k.ServerID,
			})
		}
	}
	return out
}
