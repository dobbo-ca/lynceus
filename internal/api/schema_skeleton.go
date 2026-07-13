package api

import (
	"net/http"

	"github.com/dobbo-ca/lynceus/web"
)

func (s *Server) handleSchemaInventory(w http.ResponseWriter, r *http.Request) {
	sv := s.shellViewFor(r, "inventory")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.SchemaInventorySkeletonPage(sv).Render(r.Context(), w)
}

func (s *Server) handleSchemaTableGrowth(w http.ResponseWriter, r *http.Request) {
	sv := s.shellViewFor(r, "tablegrowth")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.SchemaTableGrowthSkeletonPage(sv).Render(r.Context(), w)
}

func (s *Server) handleSchemaIndexes(w http.ResponseWriter, r *http.Request) {
	sv := s.shellViewFor(r, "indexes")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.SchemaIndexesSkeletonPage(sv).Render(r.Context(), w)
}
