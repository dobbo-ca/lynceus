package api

import (
	"net/http"

	"github.com/dobbo-ca/lynceus/web"
)

// handleClusterScopeOverview renders the scoped cluster overview skeleton. No
// store calls: the body is inert placeholder markup (ly-ae6.7 shell retrofit).
func (s *Server) handleClusterScopeOverview(w http.ResponseWriter, r *http.Request) {
	sv := s.shellViewFor(r, "clusterdetail")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.ClusterOverviewSkeletonPage(sv).Render(r.Context(), w)
}
