package api

import (
	"net/http"

	"github.com/dobbo-ca/lynceus/web"
)

func (s *Server) handleConnections(w http.ResponseWriter, r *http.Request) {
	sv := s.shellViewFor(r, "connections")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.ConnectionsSkeletonPage(sv).Render(r.Context(), w)
}
