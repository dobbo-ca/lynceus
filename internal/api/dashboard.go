package api

import (
	"net/http"
	"time"

	"github.com/dobbo-ca/lynceus/web"
)

// queriesSort reads the sort/dir params into the fleet Top Queries sort state.
// Nav carries the fleet routes; ly-ae6.3 refills them under scope.
func queriesSort(r *http.Request) web.QuerySort {
	return web.QuerySort{
		Col: q1(r, "sort", "total"), Dir: q1(r, "dir", "desc"),
		Nav: web.ScreenNav{Base: "/queries", Plan: "/plan"},
	}
}

// handleDashboard renders the legacy global Top Queries screen inside the shell.
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	sv := s.buildShellView(r, "topqueries")
	sort := queriesSort(r)
	rows := s.sortAndFilterQueries(s.fetchTop(r, 50), sort, r.URL.Query().Get("q"))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.QueriesPage(sv, sort, rows).Render(r.Context(), w)
}

// handleQueriesPartial renders just the table fragment, used by HTMX
// for in-place auto-refresh and sort/filter re-render.
func (s *Server) handleQueriesPartial(w http.ResponseWriter, r *http.Request) {
	sort := queriesSort(r)
	rows := s.sortAndFilterQueries(s.fetchTop(r, 50), sort, r.URL.Query().Get("q"))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.QueriesTable(sort, rows).Render(r.Context(), w)
}

func (s *Server) fetchTop(r *http.Request, limit int) []web.TopQuery {
	now := time.Now().UTC()
	rows, err := s.stats.TopQueriesByTotalTime(r.Context(),
		now.AddDate(0, 0, -30), now, limit)
	if err != nil {
		return nil
	}
	out := make([]web.TopQuery, 0, len(rows))
	for _, row := range rows {
		out = append(out, web.TopQuery{
			Fingerprint:     row.Fingerprint,
			NormalizedQuery: row.NormalizedQuery,
			Calls:           row.Calls,
			TotalTimeMs:     row.TotalTimeMs,
		})
	}
	counts := map[string]int{}
	for _, in := range s.fetchInsights(r) {
		counts[in.Fingerprint]++
	}
	for i := range out {
		out[i].InsightCount = counts[out[i].Fingerprint]
		out[i].MeanTimeMs = web.MeanMs(out[i].TotalTimeMs, out[i].Calls)
		out[i].CacheHitPct = -1
	}
	return out
}
