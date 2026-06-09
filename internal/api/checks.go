package api

import (
	"net/http"
	"time"

	"github.com/dobbo-ca/lynceus/web"
)

func (s *Server) handleChecksPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.ChecksPage(s.fetchChecks(r)).Render(r.Context(), w)
}

func (s *Server) handleChecksPartial(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.ChecksTable(s.fetchChecks(r)).Render(r.Context(), w)
}

func (s *Server) fetchChecks(r *http.Request) []web.ChecksRow {
	now := time.Now().UTC()
	since := now.AddDate(0, 0, -1)
	servers, err := s.stats.RecentServerIDs(r.Context(), since)
	if err != nil {
		return nil
	}
	var out []web.ChecksRow
	for _, srv := range servers {
		res, err := s.stats.LatestChecksResults(r.Context(), srv, since, now.Add(time.Minute))
		if err != nil {
			continue
		}
		for i := range res {
			c := &res[i]
			out = append(out, web.ChecksRow{
				Severity: c.Severity, Category: c.Category, CheckID: c.CheckID,
				Object: c.Object, Detail: c.Detail, Muted: c.Muted,
			})
		}
	}
	return out
}
