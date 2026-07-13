package api

import (
	"net/http"

	"github.com/dobbo-ca/lynceus/web"
)

func (s *Server) handleLogInsights(w http.ResponseWriter, r *http.Request) {
	sv := s.shellViewFor(r, "loginsights")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.LogInsightsSkeletonPage(sv).Render(r.Context(), w)
}
