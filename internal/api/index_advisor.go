package api

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/dobbo-ca/lynceus/internal/advisor"
	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/dobbo-ca/lynceus/web"
)

func (s *Server) handleIndexAdvisorPage(w http.ResponseWriter, r *http.Request) {
	sv := s.shellViewFor(r, "indexadvisor")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.IndexAdvisorPage(sv, s.fetchIndexAdvice(r)).Render(r.Context(), w)
}

func (s *Server) handleIndexAdvisorPartial(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.IndexAdvisorTable(s.fetchIndexAdvice(r)).Render(r.Context(), w)
}

// fetchIndexAdvice enumerates plan keys over the last 30 days, loads recent
// plans per key plus the latest table stats per server, runs the pure advisor,
// and maps each recommendation to a view-model. Errors degrade to nil rows
// (same convention as fetchInsights, insights.go:30).
func (s *Server) fetchIndexAdvice(r *http.Request) []web.IndexAdvisorRow {
	now := time.Now().UTC()
	since := now.AddDate(0, 0, -30)
	keys, err := s.stats.ListPlanKeys(r.Context(), since, now, 200)
	if err != nil {
		return nil
	}
	var plans []*lynceusv1.QueryPlan
	tables := map[string]advisor.TableInfo{}
	servers := map[string]bool{}
	for _, k := range keys {
		ps, err := s.stats.TopPlansByQuery(r.Context(), k.ServerID, k.Fingerprint, since, now, 10)
		if err != nil {
			continue
		}
		for _, p := range ps {
			plans = append(plans, p.Plan)
		}
		servers[k.ServerID] = true
	}
	for srv := range servers {
		tsList := latestTableStats(r, s, srv, now)
		for i := range tsList {
			ts := &tsList[i]
			ti := tables[ts.ObjectName]
			ti.TotalBytes = ts.TotalBytes
			ti.SeqScans = ts.SeqScan
			tables[ts.ObjectName] = ti
		}
	}
	var out []web.IndexAdvisorRow
	for _, rec := range advisor.RecommendIndexes(plans, tables) {
		evfp := ""
		if len(rec.Fingerprints) > 0 {
			evfp = rec.Fingerprints[0] // evidence fingerprint is available today (index.go:59)
		}
		out = append(out, web.IndexAdvisorRow{
			Relation:   rec.Relation,
			Columns:    strings.Join(rec.Columns, ", "),
			QueryCount: rec.QueryCount,
			SizePretty: prettyBytes(rec.TotalBytes),
			SeqScans:   rec.SeqScans,
			Rationale:  rec.Rationale,
			DDL:        fmt.Sprintf("CREATE INDEX ON %s (%s)", rec.Relation, strings.Join(rec.Columns, ", ")),
			BenefitPct: 0,    // ly-u4t.13 will quantify benefit; bar hidden until then
			EvidenceFP: evfp, // populated now → EVIDENCE link renders
			// scope crumb Cluster/Database/Server + ClusterID fill when scope resolves (ly-u4t.12/ly-ae6.2)
			Nav: web.ScreenNav{Base: "/index-advisor", Plan: "/plan"},
		})
	}
	return out
}

// latestTableStats is a thin wrapper so a missing reader degrades to empty.
func latestTableStats(r *http.Request, s *Server, serverID string, asOf time.Time) []store.TableStatRow {
	rows, err := s.stats.LatestTableStats(r.Context(), serverID, asOf)
	if err != nil {
		return nil
	}
	return rows
}

// prettyBytes humanizes a byte count (1024-based) for the table-size column.
func prettyBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
