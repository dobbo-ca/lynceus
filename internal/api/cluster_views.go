package api

import (
	"net/http"
	"time"

	"github.com/dobbo-ca/lynceus/internal/fleetview"
	"github.com/dobbo-ca/lynceus/web"
)

// fetchClusterVM looks up the cluster by ID and returns its view-model.
// Returns false if the cluster is not found or on error.
func (s *Server) fetchClusterVM(r *http.Request) (web.OverviewVM, bool) {
	clusterID := r.PathValue("clusterID")
	now := time.Now().UTC()
	detail, found, err := fleetview.GetClusterDetail(r.Context(), s.conf, s.stats, clusterID, now.AddDate(0, 0, -1), now)
	if err != nil || !found {
		return web.OverviewVM{}, false
	}
	return toOverviewVM(&detail), true
}

// handleClusterQueries renders the cluster Queries view.
func (s *Server) handleClusterQueries(w http.ResponseWriter, r *http.Request) {
	vm, ok := s.fetchClusterVM(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.ClusterQueriesPage(vm).Render(r.Context(), w)
}

// handleClusterInsights renders the cluster Insights view.
func (s *Server) handleClusterInsights(w http.ResponseWriter, r *http.Request) {
	vm, ok := s.fetchClusterVM(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.ClusterInsightsPage(vm).Render(r.Context(), w)
}

// handleClusterActivity renders the cluster Activity & Waits view.
func (s *Server) handleClusterActivity(w http.ResponseWriter, r *http.Request) {
	vm, ok := s.fetchClusterVM(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.ClusterActivityPage(vm).Render(r.Context(), w)
}

// handleClusterSettings renders the cluster Settings view.
func (s *Server) handleClusterSettings(w http.ResponseWriter, r *http.Request) {
	vm, ok := s.fetchClusterVM(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.ClusterSettingsPage(vm).Render(r.Context(), w)
}
