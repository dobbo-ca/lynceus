// Package api is the Lynceus HTTP/SSR server. For the MVP it exposes
// a single JSON endpoint (top queries) and a dev-mode auth bypass.
// Real OIDC + SCIM are intentionally stubbed (501) and arrive in
// Milestone 5.
package api

import (
	"net/http"

	"github.com/dobbo-ca/lynceus/internal/store"
)

// Config is the server's runtime configuration.
type Config struct {
	// DevAuth, when true, bypasses authentication entirely and treats
	// every request as authenticated as a static dev admin. Only safe
	// in development — gated by the LYNCEUS_DEV_AUTH env var.
	DevAuth bool
}

// Server bundles routes and dependencies.
type Server struct {
	cfg   Config
	stats store.Stats
	conf  store.Config
	disc  *store.DiscoveredCapabilities
	mux   *http.ServeMux
}

// NewServer returns a fully wired Server. stats is the stats-DB store;
// conf is the config/metadata-DB store (used by the audit-log viewer).
func NewServer(cfg Config, stats store.Stats, conf store.Config) *Server {
	s := &Server{
		cfg:   cfg,
		stats: stats,
		conf:  conf,
		disc:  store.NewDiscoveredCapabilities(conf.Pool()),
		mux:   http.NewServeMux(),
	}
	s.routes()
	return s
}

// Handler returns the HTTP handler, with the auth middleware applied.
func (s *Server) Handler() http.Handler { return s.withAuth(s.mux) }

func (s *Server) routes() {
	s.mux.HandleFunc("GET /databases", s.handleDatabases)
	s.mux.HandleFunc("GET /partial/databases", s.handleDatabasesPartial)
	s.mux.HandleFunc("GET /databases/{clusterID}", s.handleClusterOverview)
	s.mux.HandleFunc("GET /databases/{clusterID}/queries", s.handleClusterQueries)
	s.mux.HandleFunc("GET /databases/{clusterID}/insights", s.handleClusterInsights)
	s.mux.HandleFunc("GET /databases/{clusterID}/activity", s.handleClusterActivity)
	s.mux.HandleFunc("GET /databases/{clusterID}/settings", s.handleClusterSettings)
	s.mux.HandleFunc("GET /partial/databases/{clusterID}/query/{fingerprint}", s.handleClusterQueryDrilldown)
	s.mux.HandleFunc("GET /", s.handleDashboard)
	s.mux.HandleFunc("GET /partial/queries", s.handleQueriesPartial)
	s.mux.HandleFunc("GET /insights", s.handleInsightsPage)
	s.mux.HandleFunc("GET /partial/insights", s.handleInsightsPartial)
	s.mux.HandleFunc("GET /audit", s.handleAuditPage)
	s.mux.HandleFunc("GET /partial/audit", s.handleAuditPartial)
	s.mux.HandleFunc("GET /plan", s.handlePlanPage)
	s.mux.HandleFunc("GET /partial/plan", s.handlePlanPartial)
	s.mux.HandleFunc("GET /index-advisor", s.handleIndexAdvisorPage)
	s.mux.HandleFunc("GET /partial/index-advisor", s.handleIndexAdvisorPartial)
	s.mux.HandleFunc("GET /vacuum-advisor", s.handleVacuumAdvisorPage)
	s.mux.HandleFunc("GET /partial/vacuum-advisor", s.handleVacuumAdvisorPartial)
	s.mux.HandleFunc("GET /config-advisor", s.handleConfigAdvisorPage)
	s.mux.HandleFunc("GET /partial/config-advisor", s.handleConfigAdvisorPartial)
	s.mux.HandleFunc("GET /waits", s.handleWaitsPage)
	s.mux.HandleFunc("GET /partial/waits", s.handleWaitsPartial)
	s.mux.HandleFunc("GET /checks", s.handleChecksPage)
	s.mux.HandleFunc("GET /partial/checks", s.handleChecksPartial)
	s.mux.HandleFunc("GET /api/queries/top", s.handleTopQueries)
	s.mux.HandleFunc("GET /api/servers/{id}/capabilities", s.handleCapabilityMatrix)
	s.mux.HandleFunc("POST /api/servers/{id}/capabilities/{cap}", s.handleCapabilityToggle)
	s.mux.HandleFunc("GET /api/servers/{id}/policy-snapshot", s.handlePolicySnapshot)
	s.mux.HandleFunc("GET /api/scim/v2/", s.notImplemented("SCIM"))
	s.mux.HandleFunc("GET /api/oidc/", s.notImplemented("OIDC"))
}

// withAuth is the simplest possible auth middleware for the MVP:
// when DevAuth is set, every request is allowed; otherwise no real
// authn is wired yet, so we refuse everything with 401. The 501s for
// SCIM/OIDC are emitted by their own handlers after auth passes.
func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.cfg.DevAuth {
			http.Error(w, "unauthorized (dev auth disabled and OIDC not yet implemented — see ly-8b0.1)", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) notImplemented(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, name+" not implemented yet (see Milestone 5)", http.StatusNotImplemented)
	}
}
