package api

import (
	"net/http"

	"github.com/dobbo-ca/lynceus/web"
)

func (s *Server) handleScripts(w http.ResponseWriter, r *http.Request) {
	sv := s.shellViewFor(r, "scripts")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.ScriptsSkeletonPage(sv).Render(r.Context(), w)
}
