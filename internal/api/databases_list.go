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

func (s *Server) handleDatabasesList(w http.ResponseWriter, r *http.Request) {
	vm := s.buildShellView(r, "databases")
	v := s.fetchDatabasesList(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.DatabasesListShellPage(vm, v).Render(r.Context(), w)
}

func (s *Server) handleDatabasesListPartial(w http.ResponseWriter, r *http.Request) {
	v := s.fetchDatabasesList(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.DatabasesListBody(v).Render(r.Context(), w)
}

func (s *Server) fetchDatabasesList(r *http.Request) web.DatabasesListView {
	sortKey := r.URL.Query().Get("sort")
	if sortKey != "health" {
		sortKey = "name"
	}
	next := "health"
	if sortKey == "health" {
		next = "name"
	}
	view := web.DatabasesListView{
		Sort:      sortKey,
		SortLabel: strings.ToUpper(sortKey),
		SortHref:  "/partial/databases/all?sort=" + next,
		PageHref:  "/databases/all?sort=" + next,
	}

	now := time.Now().UTC()
	groups, err := fleetview.ListDatabaseGroups(r.Context(), s.conf, s.stats, now.AddDate(0, 0, -1), now)
	if err != nil {
		return view
	}

	totalDBs, totalClusters := 0, 0
	vms := make([]web.DatabaseGroupVM, 0, len(groups))
	for i := range groups {
		g := &groups[i]
		if len(g.Entries) == 0 {
			continue
		}
		totalClusters++
		totalDBs += len(g.Entries)
		text, class := web.HealthLine(g.CritOpen, g.WarnOpen, g.InfoOpen)
		gvm := web.DatabaseGroupVM{
			Name:        g.Cluster.Name,
			EngineIcon:  "eng-pg",
			EngineName:  "POSTGRES",
			Version:     g.Version,
			HealthText:  text,
			HealthClass: class,
			SevRank:     web.SevRank(g.CritOpen, g.WarnOpen, g.InfoOpen),
			ScopeHref:   string(web.ScopeHref(scope.Scope{Kind: scope.Cluster, ClusterID: g.Cluster.ID})),
		}
		for j := range g.Entries {
			e := &g.Entries[j]
			gvm.Entries = append(gvm.Entries, web.DatabaseEntryVM{
				Name:      e.Name,
				Qual:      g.Cluster.Name + "/" + e.Name,
				Size:      "—",
				QPS:       groupThousands(int64(e.QPS + 0.5)),
				Conns:     strconv.FormatInt(e.ActiveConns, 10),
				Cache:     "—",
				Tables:    "—",
				ScopeHref: string(web.ScopeHref(scope.Scope{Kind: scope.Database, ClusterID: g.Cluster.ID, Database: e.Name})),
			})
		}
		vms = append(vms, gvm)
	}
	sort.SliceStable(vms, func(i, j int) bool {
		if sortKey == "health" && vms[i].SevRank != vms[j].SevRank {
			return vms[i].SevRank > vms[j].SevRank
		}
		return vms[i].Name < vms[j].Name
	})

	view.Groups = vms
	view.CountLabel = strconv.Itoa(totalDBs) + " " + plural(totalDBs, "DATABASE", "DATABASES") +
		" ACROSS " + strconv.Itoa(totalClusters) + " " + plural(totalClusters, "CLUSTER", "CLUSTERS")
	return view
}
