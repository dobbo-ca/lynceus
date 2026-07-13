package api

import (
	"net/http"

	"github.com/dobbo-ca/lynceus/web"
)

func (s *Server) handleDatabasesList(w http.ResponseWriter, r *http.Request) {
	sv := s.shellViewFor(r, "databases")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.DatabasesListSkeletonPage(sv).Render(r.Context(), w)
}
