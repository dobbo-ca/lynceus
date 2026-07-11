package api

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dobbo-ca/lynceus/internal/fleetview"
	"github.com/dobbo-ca/lynceus/internal/scope"
	"github.com/dobbo-ca/lynceus/web"
)

// verticalShell builds the design shell view for a Database-vertical list screen,
// highlighting activeScreen in the scope-driven sidebar. Reuses the landed
// buildShellView (scope + range + picker) and only re-points the sidebar's active
// nav entry — no top-bar/nav is reinvented here.
func (s *Server) verticalShell(r *http.Request, activeScreen string) web.ShellView {
	vm := s.buildShellView(r, activeScreen)
	vm.Sidebar = web.Sidebar(vm.Scope, vm.ScopeLabel, web.DefaultEngines(), activeScreen)
	return vm
}

func (s *Server) handleClusters(w http.ResponseWriter, r *http.Request) {
	vm := s.verticalShell(r, "clusters")
	v := s.fetchClusters(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.ClustersShellPage(vm, v).Render(r.Context(), w)
}

func (s *Server) handleClustersPartial(w http.ResponseWriter, r *http.Request) {
	v := s.fetchClusters(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.ClustersBody(v).Render(r.Context(), w)
}

func (s *Server) fetchClusters(r *http.Request) web.ClustersView {
	sortKey := r.URL.Query().Get("sort")
	if sortKey != "name" {
		sortKey = "health"
	}
	next := "name"
	if sortKey == "name" {
		next = "health"
	}
	view := web.ClustersView{
		Sort:         sortKey,
		SortLabel:    strings.ToUpper(sortKey),
		SortHref:     "/partial/databases?sort=" + next,
		SortPageHref: "/databases?sort=" + next,
	}

	now := time.Now().UTC()
	sums, err := fleetview.ListClusterSummaries(r.Context(), s.conf, s.stats, now.AddDate(0, 0, -1), now)
	if err != nil {
		return view
	}

	rows := make([]web.ClusterListRow, 0, len(sums))
	for i := range sums {
		sum := &sums[i]
		var qps float64
		if n := len(sum.QPSBuckets); n > 0 {
			qps = float64(sum.QPSBuckets[n-1].Calls) / 3600.0
		}
		text, class := web.HealthLine(sum.CritOpen, sum.WarnOpen, sum.InfoOpen)
		rows = append(rows, web.ClusterListRow{
			Name:        sum.Cluster.Name,
			EngineIcon:  "eng-pg",
			EngineName:  "POSTGRES",
			Version:     sum.Version,
			Meta:        clusterMeta(sum.InstanceCount, sum.StreamCount),
			QPS:         groupThousands(int64(qps + 0.5)),
			HealthText:  text,
			HealthClass: class,
			SevRank:     web.SevRank(sum.CritOpen, sum.WarnOpen, sum.InfoOpen),
			ScopeHref:   string(web.ScopeHref(scope.Scope{Kind: scope.Cluster, ClusterID: sum.Cluster.ID})),
		})
	}
	sortRowsByKey(rows, sortKey)
	view.Rows = rows
	return view
}

// sortRowsByKey orders by SevRank desc (health) or Name asc.
func sortRowsByKey(rows []web.ClusterListRow, key string) {
	sort.SliceStable(rows, func(i, j int) bool {
		if key == "health" && rows[i].SevRank != rows[j].SevRank {
			return rows[i].SevRank > rows[j].SevRank
		}
		return rows[i].Name < rows[j].Name
	})
}

func clusterMeta(instances, streams int) string {
	return strconv.Itoa(instances) + " " + plural(instances, "INSTANCE", "INSTANCES") +
		" · " + strconv.Itoa(streams) + " " + plural(streams, "STREAM", "STREAMS")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// groupThousands renders n with comma thousands separators (e.g. 1284 -> "1,284").
func groupThousands(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	var out strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			out.WriteByte(',')
		}
		out.WriteRune(c)
	}
	if neg {
		return "-" + out.String()
	}
	return out.String()
}
