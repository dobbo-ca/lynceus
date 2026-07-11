package api

import (
	"net/http"

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
