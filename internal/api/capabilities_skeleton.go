package api

import (
	"net/http"

	"github.com/dobbo-ca/lynceus/web"
)

func (s *Server) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	sv := s.shellViewFor(r, "capabilities")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.CapabilitiesSkeletonPage(sv).Render(r.Context(), w)
}
