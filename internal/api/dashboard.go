package api

import (
	"net/http"
	"time"

	"github.com/dobbo-ca/lynceus/web"
)

// handleDashboard renders the full top-queries page.
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	rows := s.fetchTop(r, 50)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.QueriesPage(rows).Render(r.Context(), w)
}

// handleQueriesPartial renders just the table fragment, used by HTMX
// for in-place auto-refresh.
func (s *Server) handleQueriesPartial(w http.ResponseWriter, r *http.Request) {
	rows := s.fetchTop(r, 50)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.QueriesTable(rows).Render(r.Context(), w)
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
	return out
}
