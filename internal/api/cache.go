package api

import (
	"net/http"
	"sort"

	"github.com/dobbo-ca/lynceus/web"
)

// handleCacheClusters serves the fleet-scope Cache › Clusters screen wrapped in
// the design Shell. Gated on CacheEnabled(): a non-cache deployment 404s here,
// so the routes are unreachable regardless of nav state (ly-ae6.3 owns the nav
// section). T1 only — the view carries counts/labels/health, never key identity.
func (s *Server) handleCacheClusters(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.CacheEnabled() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.CacheClustersPage(s.buildShellView(r), s.fetchCacheClusters()).Render(r.Context(), w)
}

func (s *Server) handleCacheClustersPartial(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.CacheEnabled() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.CacheClustersBody(s.fetchCacheClusters()).Render(r.Context(), w)
}

// fetchCacheClusters returns the cache-clusters view. Cache telemetry has no
// collector/ingestion/store yet (COMPARISON.md:378, ly-ae6.11 backend note),
// so the cluster list is empty and the screen renders its empty state. When the
// redisnorm ingest path lands, replace the nil slice with a fleetview query
// returning []web.CacheCluster.
func (s *Server) fetchCacheClusters() web.CacheClustersView {
	return web.CacheClustersView{Enabled: true, Clusters: nil}
}

func (s *Server) handleCacheReplicasets(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.CacheEnabled() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.CacheReplicasetsPage(s.buildShellView(r), s.fetchCacheReplicasets(r)).Render(r.Context(), w)
}

func (s *Server) handleCacheReplicasetsPartial(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.CacheEnabled() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.CacheReplicasetsBody(s.fetchCacheReplicasets(r)).Render(r.Context(), w)
}

// fetchCacheReplicasets returns the replicasets view. No cache store yet
// (ly-ae6.11 backend note); rows are empty and the screen renders its empty
// state. Sort is applied so it is correct the moment rows arrive.
func (s *Server) fetchCacheReplicasets(r *http.Request) web.CacheReplicasetsView {
	sortKey := r.URL.Query().Get("sort")
	if sortKey != "name" {
		sortKey = "health"
	}
	var rows []web.CacheReplicasetRow // future: fleetview.ListCacheReplicasets(...)
	sortCacheReplicasets(rows, sortKey)
	return web.CacheReplicasetsView{Enabled: true, Rows: rows, Sort: sortKey}
}

// sortCacheReplicasets sorts in place: "name" ascending, else "health"
// (worst SevRank first, ties broken by name).
func sortCacheReplicasets(rows []web.CacheReplicasetRow, key string) {
	switch key {
	case "name":
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	default:
		sort.SliceStable(rows, func(i, j int) bool {
			if rows[i].SevRank != rows[j].SevRank {
				return rows[i].SevRank > rows[j].SevRank
			}
			return rows[i].Name < rows[j].Name
		})
	}
}

func (s *Server) handleCacheNodes(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.CacheEnabled() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.CacheNodesPage(s.buildShellView(r), s.fetchCacheNodes(r)).Render(r.Context(), w)
}

func (s *Server) handleCacheNodesPartial(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.CacheEnabled() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.CacheNodesBody(s.fetchCacheNodes(r)).Render(r.Context(), w)
}

// fetchCacheNodes returns the nodes view (empty rows until backend lands).
func (s *Server) fetchCacheNodes(r *http.Request) web.CacheNodesView {
	sortKey := r.URL.Query().Get("sort")
	if sortKey != "name" {
		sortKey = "ops"
	}
	var rows []web.CacheNodeRow // future: fleetview.ListCacheNodes(...)
	sortCacheNodes(rows, sortKey)
	return web.CacheNodesView{Enabled: true, Rows: rows, Sort: sortKey}
}

// sortCacheNodes sorts in place: "name" ascending, else "ops"
// (highest OpsVal first, ties broken by name).
func sortCacheNodes(rows []web.CacheNodeRow, key string) {
	switch key {
	case "name":
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	default:
		sort.SliceStable(rows, func(i, j int) bool {
			if rows[i].OpsVal != rows[j].OpsVal {
				return rows[i].OpsVal > rows[j].OpsVal
			}
			return rows[i].Name < rows[j].Name
		})
	}
}
