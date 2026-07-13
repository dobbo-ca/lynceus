package api

import (
	"net/http"

	"github.com/dobbo-ca/lynceus/web"
)

func (s *Server) handleNodes(w http.ResponseWriter, r *http.Request) {
	sv := s.shellViewFor(r, "nodes")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.NodesSkeletonPage(sv).Render(r.Context(), w)
}
