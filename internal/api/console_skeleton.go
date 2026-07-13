package api

import (
	"net/http"

	"github.com/dobbo-ca/lynceus/web"
)

func (s *Server) handleConsole(w http.ResponseWriter, r *http.Request) {
	sv := s.shellViewFor(r, "console")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.ConsoleSkeletonPage(sv).Render(r.Context(), w)
}
