package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/dobbo-ca/lynceus/web"
)

// handleClusterQueryDrilldownPage renders the full Query Drilldown screen inside
// the design shell for a (cluster, fingerprint).
func (s *Server) handleClusterQueryDrilldownPage(w http.ResponseWriter, r *http.Request) {
	sv := s.shellViewFor(r, "querydetail")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.QueryDrilldownPage(sv, s.fetchDrilldown(r)).Render(r.Context(), w)
}

// fetchDrilldown assembles the drilldown VM from the plan store + insight
// engine. Trend series and per-query wait attribution are tracked separately
// (ly-xqf.10/11, ly-xqf.1); this fills what the store already provides.
func (s *Server) fetchDrilldown(r *http.Request) web.DrilldownVM {
	clusterID := r.PathValue("clusterID")
	fp := r.PathValue("fingerprint")
	now := time.Now().UTC()
	since := now.AddDate(0, 0, -30)

	vm := web.DrilldownVM{
		ClusterID:   clusterID,
		Fingerprint: fp,
		Sample:      web.QuerySampleVM{Locked: true, Group: "dba-oncall"},
		Nav:         web.ScreenNav{Base: "/queries", Plan: "/plan"}, // fleet routes; ly-ae6.3 refills under scope
	}

	// Aggregate calls/total across the fingerprint (fleet or cluster window).
	var calls int64
	var total float64
	if tq, err := s.stats.TopQueriesByTotalTime(r.Context(), since, now, 200); err == nil {
		for _, q := range tq {
			if q.Fingerprint == fp {
				calls, total = q.Calls, q.TotalTimeMs
				vm.NormalizedQuery = q.NormalizedQuery
				break
			}
		}
	}

	// Plans: pick the first plan's server, count variants for the 4th stat.
	planCount := 0
	if keys, err := s.stats.ListPlanKeys(r.Context(), since, now, 200); err == nil {
		for _, k := range keys {
			if k.Fingerprint != fp {
				continue
			}
			vm.ServerID = k.ServerID
			vm.Sample.ServerID = k.ServerID
			if plans, err := s.stats.TopPlansByQuery(r.Context(), k.ServerID, fp, since, now, 10); err == nil && len(plans) > 0 {
				vm.HasPlan = true
				planCount += len(plans)
			}
		}
	}

	vm.Stats = []web.DrilldownStat{
		{Label: "CALLS", Value: fmt.Sprintf("%d", calls)},
		{Label: "TOTAL MS", Value: fmt.Sprintf("%.0f", total)},
		{Label: "MEAN MS", Value: fmt.Sprintf("%.1f", web.MeanMs(total, calls))},
		{Label: "PLAN VARIANTS", Value: fmt.Sprintf("%d", planCount)},
	}

	// Insights for this fingerprint, mapped to the drilldown shape.
	for _, in := range s.fetchInsights(r) {
		if in.Fingerprint != fp {
			continue
		}
		cls := web.SevClass(in.Severity)
		vm.Insights = append(vm.Insights, web.DrilldownInsight{
			KindLabel: web.KindLabel(in.Kind),
			Node:      in.NodePath,
			SevClass:  cls,
			Detail:    in.Detail,
			Rec:       web.RecommendationFor(in.Kind),
		})
	}
	return vm
}
