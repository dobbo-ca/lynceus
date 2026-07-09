package api

import (
	"net/http"
	"time"

	"github.com/dobbo-ca/lynceus/internal/advisor"
	"github.com/dobbo-ca/lynceus/web"
)

func (s *Server) handleConfigAdvisorPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.ConfigAdvisorPage(s.fetchConfigAdvice(r)).Render(r.Context(), w)
}

func (s *Server) handleConfigAdvisorPartial(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.ConfigAdvisorTable(s.fetchConfigAdvice(r)).Render(r.Context(), w)
}

// fetchConfigAdvice discovers servers seen in the last 24h (via RecentServerIDs
// — settings exist without stored plans, so we do NOT gate on ListPlanKeys),
// loads the latest curated pg_settings per server, runs the pure ConfigAdvice
// fn, and maps each recommendation to a view-model. Errors degrade to nil rows.
func (s *Server) fetchConfigAdvice(r *http.Request) []web.ConfigAdvisorRow {
	now := time.Now().UTC()
	since := now.Add(-24 * time.Hour)
	servers, err := s.stats.RecentServerIDs(r.Context(), since)
	if err != nil {
		return nil
	}
	var in []advisor.ConfigSettingInput
	for _, srv := range servers {
		rows, err := s.stats.LatestSettings(r.Context(), srv, now)
		if err != nil {
			continue
		}
		for i := range rows {
			in = append(in, advisor.ConfigSettingInput{
				Name:   rows[i].Name,
				Value:  rows[i].Value,
				Unit:   rows[i].Unit,
				Source: rows[i].Source,
			})
		}
	}
	var out []web.ConfigAdvisorRow
	for _, rec := range advisor.ConfigAdvice(in) {
		out = append(out, web.ConfigAdvisorRow{
			Setting:   rec.Setting,
			Category:  string(rec.Category),
			Severity:  string(rec.Severity),
			Current:   rec.Current,
			Suggested: rec.Suggested,
			Detail:    rec.Detail,
		})
	}
	return out
}
