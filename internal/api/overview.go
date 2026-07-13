package api

import (
	"net/http"
	"time"

	"github.com/dobbo-ca/lynceus/internal/fleetview"
	"github.com/dobbo-ca/lynceus/internal/scope"
	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/dobbo-ca/lynceus/web"
)

// handleClusterOverview renders the full cluster Overview page.
func (s *Server) handleClusterOverview(w http.ResponseWriter, r *http.Request) {
	clusterID := r.PathValue("clusterID")
	now := time.Now().UTC()
	detail, found, err := fleetview.GetClusterDetail(r.Context(), s.conf, s.stats, clusterID, now.AddDate(0, 0, -1), now)
	if err != nil || !found {
		http.NotFound(w, r)
		return
	}
	// Scope the Shell to this cluster so the sidebar renders the cluster nav
	// (Overview active) and the top bar reflects the cluster scope.
	q := r.URL.Query()
	q.Set("scope", scope.Scope{Kind: scope.Cluster, ClusterID: clusterID}.Encode())
	rs := r.Clone(r.Context())
	rs.URL.RawQuery = q.Encode()
	sv := s.shellViewFor(rs, "clusterdetail")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.OverviewPage(sv, toOverviewVM(&detail)).Render(r.Context(), w)
}

// handleClusterQueryDrilldown renders the plan tree + insight fragment for one
// fingerprint in a cluster. HTMX fragment — no full HTML document.
func (s *Server) handleClusterQueryDrilldown(w http.ResponseWriter, r *http.Request) {
	clusterID := r.PathValue("clusterID")
	fp := r.PathValue("fingerprint")
	now := time.Now().UTC()

	serverIDs, _ := s.conf.ServerIDsForCluster(r.Context(), clusterID)

	planVM := web.PlanVM{Empty: true}
	for _, sid := range serverIDs {
		rows, err := s.stats.TopPlansByQuery(r.Context(), sid, fp, now.AddDate(0, 0, -1), now, 1)
		if err == nil && len(rows) > 0 {
			planVM = web.ToPlanVM(sid, rows[0].Plan)
			break
		}
	}

	insights, _ := s.stats.TopInsightsForServers(r.Context(), serverIDs, now.AddDate(0, 0, -1), now, 50)
	var match *web.OverviewInsight
	for i := range insights {
		if insights[i].Fingerprint == fp {
			v := toOverviewInsight(&insights[i])
			match = &v
			break
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.QueryDrilldown(planVM, match).Render(r.Context(), w)
}

// toOverviewVM maps a ClusterDetail to the templ view-model.
func toOverviewVM(d *fleetview.ClusterDetail) web.OverviewVM {
	var qps float64
	if n := len(d.QPSBuckets); n > 0 {
		qps = float64(d.QPSBuckets[n-1].Calls) / 3600.0
	}

	insightFPs := make(map[string]struct{}, len(d.Insights))
	for i := range d.Insights {
		insightFPs[d.Insights[i].Fingerprint] = struct{}{}
	}

	queries := make([]web.OverviewQuery, 0, len(d.TopQueries))
	for i := range d.TopQueries {
		q := d.TopQueries[i]
		var mean float64
		if q.Calls > 0 {
			mean = q.TotalTimeMs / float64(q.Calls)
		}
		_, hasInsight := insightFPs[q.Fingerprint]
		queries = append(queries, web.OverviewQuery{
			Fingerprint:     q.Fingerprint,
			NormalizedQuery: q.NormalizedQuery,
			Calls:           q.Calls,
			MeanMs:          mean,
			TotalMs:         q.TotalTimeMs,
			HasInsight:      hasInsight,
		})
	}

	insights := make([]web.OverviewInsight, 0, len(d.Insights))
	for i := range d.Insights {
		insights = append(insights, toOverviewInsight(&d.Insights[i]))
	}

	instances := make([]web.OverviewInstance, 0, len(d.Instances))
	for i := range d.Instances {
		inst := d.Instances[i]
		dbs := make([]string, 0, len(inst.Streams))
		for j := range inst.Streams {
			if db := inst.Streams[j].DatabaseName; db != "" {
				dbs = append(dbs, db)
			}
		}
		instances = append(instances, web.OverviewInstance{
			Name:        inst.Instance.Name,
			Role:        inst.Instance.Role,
			Databases:   dbs,
			Calls:       inst.Calls,
			ActiveConns: inst.ActiveConns,
		})
	}

	return web.OverviewVM{
		ClusterID:    d.Cluster.ID,
		Name:         d.Cluster.Name,
		QPS:          qps,
		AvgLatencyMs: d.AvgLatencyMs,
		ActiveConns:  d.ActiveConns,
		TopWait:      d.TopWait,
		InsightCount: d.InsightCount,
		StreamCount:  d.StreamCount,
		Sparkline:    sparklinePoints(d.QPSBuckets),
		Instances:    instances,
		Queries:      queries,
		Insights:     insights,
	}
}

// toOverviewInsight maps a store.InsightRow to the templ view-model.
func toOverviewInsight(r *store.InsightRow) web.OverviewInsight {
	return web.OverviewInsight{
		Severity:    r.Severity,
		Relation:    r.Relation,
		Detail:      r.Detail,
		Fingerprint: r.Fingerprint,
	}
}
