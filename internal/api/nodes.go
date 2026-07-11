package api

import (
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dobbo-ca/lynceus/internal/fleetview"
	"github.com/dobbo-ca/lynceus/internal/scope"
	"github.com/dobbo-ca/lynceus/web"
)

const nodeGroupsPerPage = 3

func (s *Server) handleNodes(w http.ResponseWriter, r *http.Request) {
	vm := s.buildShellView(r, "nodes")
	v := s.fetchNodes(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.NodesShellPage(vm, v).Render(r.Context(), w)
}

func (s *Server) handleNodesPartial(w http.ResponseWriter, r *http.Request) {
	v := s.fetchNodes(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.NodesBody(v).Render(r.Context(), w)
}

func (s *Server) fetchNodes(r *http.Request) web.NodesView {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	sortKey := r.URL.Query().Get("sort")
	if sortKey != "name" {
		sortKey = "health"
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 0 {
		page = 0
	}
	next := "name"
	if sortKey == "name" {
		next = "health"
	}
	qEsc := url.QueryEscape(q)

	view := web.NodesView{
		Query:     q,
		Sort:      sortKey,
		SortLabel: strings.ToUpper(sortKey),
		SortHref:  "/partial/nodes?sort=" + next + "&q=" + qEsc,
		PageHref:  "/nodes?sort=" + next + "&q=" + qEsc,
	}

	now := time.Now().UTC()
	groups, err := fleetview.ListNodeGroups(r.Context(), s.conf, s.stats, now.AddDate(0, 0, -1), now)
	if err != nil {
		return view
	}

	vms := make([]web.NodeGroupVM, 0, len(groups))
	for i := range groups {
		g := &groups[i]
		if !nodeGroupMatches(g, strings.ToLower(q)) {
			continue
		}
		vms = append(vms, nodeGroupVM(g))
	}
	// SORT: health puts clusters with open issues first (SevRank desc), else name.
	// SevRank is carried ON the VM (populated in nodeGroupVM), so sorting vms never
	// reaches into the fixed `groups` slice.
	sort.SliceStable(vms, func(i, j int) bool {
		if sortKey == "health" && vms[i].SevRank != vms[j].SevRank {
			return vms[i].SevRank > vms[j].SevRank
		}
		return vms[i].Name < vms[j].Name
	})

	if len(vms) == 0 {
		view.NoResults = true
		return view
	}

	pageCount := (len(vms) + nodeGroupsPerPage - 1) / nodeGroupsPerPage
	if page > pageCount-1 {
		page = pageCount - 1
	}
	start := page * nodeGroupsPerPage
	end := start + nodeGroupsPerPage
	if end > len(vms) {
		end = len(vms)
	}
	view.Groups = vms[start:end]
	view.ShowPager = pageCount > 1
	view.PagerLabel = "PAGE " + strconv.Itoa(page+1) + " / " + strconv.Itoa(pageCount)
	view.HasPrev = page > 0
	view.HasNext = page < pageCount-1
	view.PrevHref = "/partial/nodes?sort=" + sortKey + "&q=" + qEsc + "&page=" + strconv.Itoa(page-1)
	view.NextHref = "/partial/nodes?sort=" + sortKey + "&q=" + qEsc + "&page=" + strconv.Itoa(page+1)
	return view
}

func nodeGroupMatches(g *fleetview.NodeGroup, needle string) bool {
	if needle == "" {
		return true
	}
	if strings.Contains(strings.ToLower(g.Cluster.Name), needle) {
		return true
	}
	for i := range g.Nodes {
		if strings.Contains(strings.ToLower(g.Nodes[i].Instance.Name), needle) ||
			strings.Contains(strings.ToLower(g.Nodes[i].Role), needle) {
			return true
		}
	}
	return false
}

func nodeGroupVM(g *fleetview.NodeGroup) web.NodeGroupVM {
	rollup, rclass := nodeRollup(g)
	vm := web.NodeGroupVM{
		Name:        g.Cluster.Name,
		EngineIcon:  "eng-pg",
		EngineName:  "POSTGRES",
		Version:     g.Version,
		Rollup:      rollup,
		RollupClass: rclass,
		SevRank:     web.SevRank(g.CritOpen, g.WarnOpen, g.InfoOpen),
		ScopeHref:   string(web.ScopeHref(scope.Scope{Kind: scope.Cluster, ClusterID: g.Cluster.ID})),
	}
	for i := range g.Nodes {
		vm.Nodes = append(vm.Nodes, nodeRowVM(g, &g.Nodes[i]))
	}
	return vm
}

func nodeRowVM(g *fleetview.NodeGroup, n *fleetview.NodeRow) web.NodeRowVM {
	health, hclass := "● OK", "hl-ok"
	switch {
	case n.CritOpen > 0:
		health, hclass = "● CRIT", "hl-crit"
	case n.WarnOpen > 0:
		health, hclass = "● WARN", "hl-warn"
	}
	conns, pct := "— / —", "0%"
	if n.MaxConns > 0 {
		conns = strconv.FormatInt(n.ActiveConns, 10) + " / " + strconv.FormatInt(n.MaxConns, 10)
		p := int(float64(n.ActiveConns) / float64(n.MaxConns) * 100.0)
		if p > 100 {
			p = 100
		}
		pct = strconv.Itoa(p) + "%"
	} else if n.ActiveConns > 0 {
		conns = strconv.FormatInt(n.ActiveConns, 10) + " / —"
	}
	return web.NodeRowVM{
		Role:        n.Role,
		RoleClass:   roleClass(n.Role),
		Name:        n.Instance.Name,
		Version:     n.Version,
		Source:      "", // backend gap (ly-7ck.3) — template renders the neutral fallback
		CPU:         "—",
		Mem:         "—",
		Disk:        "—",
		IOWait:      "—",
		Conns:       conns,
		ConnsPct:    pct,
		Health:      health,
		HealthClass: hclass,
		ScopeHref:   string(web.ScopeHref(scope.Scope{Kind: scope.Node, ClusterID: g.Cluster.ID, NodeID: n.Instance.ID})),
	}
}

func nodeRollup(g *fleetview.NodeGroup) (text, cssClass string) {
	ok := 0
	for i := range g.Nodes {
		if g.Nodes[i].CritOpen == 0 && g.Nodes[i].WarnOpen == 0 {
			ok++
		}
	}
	parts := make([]string, 0, 3)
	if g.CritOpen > 0 {
		parts = append(parts, strconv.Itoa(g.CritOpen)+" CRIT")
	}
	if g.WarnOpen > 0 {
		parts = append(parts, strconv.Itoa(g.WarnOpen)+" WARN")
	}
	parts = append(parts, strconv.Itoa(ok)+" OK")
	clusterLabel := "HEALTHY"
	cssClass = "hl-ok"
	switch {
	case g.CritOpen > 0:
		clusterLabel, cssClass = "DEGRADED", "hl-crit"
	case g.WarnOpen > 0:
		clusterLabel, cssClass = "WARNING", "hl-warn"
	}
	return "NODE HEALTH " + strings.Join(parts, " · ") + " → CLUSTER " + clusterLabel, cssClass
}

func roleClass(role string) string {
	switch role {
	case "PRIMARY":
		return "role-primary"
	case "REPLICA":
		return "role-replica"
	case "POOLER":
		return "role-pooler"
	case "STANDBY":
		return "role-standby"
	default:
		return "role-unknown"
	}
}
