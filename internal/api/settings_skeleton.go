package api

import (
	"net/http"

	"github.com/dobbo-ca/lynceus/web"
)

func (s *Server) handleSettingsAccess(w http.ResponseWriter, r *http.Request) {
	sv := s.shellViewFor(r, "")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.SettingsAccessSkeletonPage(sv).Render(r.Context(), w)
}

func (s *Server) handleSettingsProviders(w http.ResponseWriter, r *http.Request) {
	sv := s.shellViewFor(r, "")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.SettingsProvidersSkeletonPage(sv).Render(r.Context(), w)
}

func (s *Server) handleSettingsCollectors(w http.ResponseWriter, r *http.Request) {
	sv := s.shellViewFor(r, "")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.SettingsCollectorsSkeletonPage(sv).Render(r.Context(), w)
}

func (s *Server) handleSettingsRetention(w http.ResponseWriter, r *http.Request) {
	sv := s.shellViewFor(r, "")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.SettingsRetentionSkeletonPage(sv).Render(r.Context(), w)
}

func (s *Server) handleSettingsGeneral(w http.ResponseWriter, r *http.Request) {
	sv := s.shellViewFor(r, "")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.SettingsGeneralSkeletonPage(sv).Render(r.Context(), w)
}
