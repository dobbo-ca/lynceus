package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/dobbo-ca/lynceus/internal/advisor"
	"github.com/dobbo-ca/lynceus/web"
)

func (s *Server) handleVacuumAdvisorPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.VacuumAdvisorPage(s.fetchVacuumAdvice(r)).Render(r.Context(), w)
}

func (s *Server) handleVacuumAdvisorPartial(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.VacuumAdvisorTable(s.fetchVacuumAdvice(r)).Render(r.Context(), w)
}

// fetchVacuumAdvice enumerates servers seen in plan keys over the last 30 days
// (same server-discovery pattern as fetchIndexAdvice, index_advisor.go:29),
// loads the latest table stats per server, runs the pure advisor, and maps each
// recommendation to a view-model. Errors degrade to nil rows. The Freezing /
// wraparound view is out of scope here (ly-u4t.26 — needs relfrozenxid age).
func (s *Server) fetchVacuumAdvice(r *http.Request) []web.VacuumAdvisorRow {
	now := time.Now().UTC()
	since := now.AddDate(0, 0, -30)
	keys, err := s.stats.ListPlanKeys(r.Context(), since, now, 200)
	if err != nil {
		return nil
	}
	servers := map[string]bool{}
	for _, k := range keys {
		servers[k.ServerID] = true
	}
	var in []advisor.TableVacuumInfo
	for srv := range servers {
		for _, t := range latestTableStats(r, s, srv, now) {
			in = append(in, advisor.TableVacuumInfo{
				Relation:         t.ObjectName,
				LiveTuples:       t.LiveTuples,
				DeadTuples:       t.DeadTuples,
				NModSinceAnalyze: t.NModSinceAnalyze,
				LastVacuum:       t.LastVacuum,
				LastAutovacuum:   t.LastAutovacuum,
			})
		}
	}
	var out []web.VacuumAdvisorRow
	for _, rec := range advisor.VacuumAdvice(in, now) {
		out = append(out, web.VacuumAdvisorRow{
			Relation: rec.Relation,
			Category: string(rec.Category),
			Severity: string(rec.Severity),
			DeadPct:  fmt.Sprintf("%.0f%%", rec.DeadRatio*100),
			Detail:   rec.Detail,
		})
	}
	return out
}
