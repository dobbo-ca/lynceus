package api

import (
	"net/http"

	"github.com/dobbo-ca/lynceus/web"
)

func (s *Server) handleAlerts(w http.ResponseWriter, r *http.Request) {
	sv := s.shellViewFor(r, "alerts")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.AlertsSkeletonPage(sv).Render(r.Context(), w)
}
