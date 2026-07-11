// Package api is the Lynceus HTTP/SSR server. For the MVP it exposes
// a single JSON endpoint (top queries) and a dev-mode auth bypass.
// Real OIDC + SCIM are intentionally stubbed (501) and arrive in
// Milestone 5.
package api

import (
	"net/http"
	"strings"

	"github.com/dobbo-ca/lynceus/internal/console"
	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/dobbo-ca/lynceus/web"
)

// Config is the server's runtime configuration.
type Config struct {
	// DevAuth, when true, bypasses authentication entirely and treats
	// every request as authenticated as a static dev admin. Only safe
	// in development — gated by the LYNCEUS_DEV_AUTH env var.
	DevAuth bool

	// EnableOpensearch / EnableElasticsearch gate the Search vertical
	// (Domains + Nodes-by-role) UI. When both are false the /search/*
	// routes 404 and the fleet nav omits the SEARCH section. Per-tenant
	// config is M5+; these are process-level flags for now.
	EnableOpensearch    bool
	EnableElasticsearch bool

	// EnableRedis / EnableValkey gate the fleet-scope Cache section
	// (Clusters/Replicasets/Nodes). Sourced from LYNCEUS_ENABLE_REDIS /
	// LYNCEUS_ENABLE_VALKEY. Matches the design config model (README:87).
	EnableRedis  bool
	EnableValkey bool
}

// SearchEnabled reports whether the Search vertical UI should be served. The
// scoped nav (ly-ae6.3) reads the same predicate to decide whether to render
// the SEARCH nav section.
func (c Config) SearchEnabled() bool { return c.EnableOpensearch || c.EnableElasticsearch }

// CacheEnabled reports whether the Cache vertical should be visible/reachable.
func (c Config) CacheEnabled() bool { return c.EnableRedis || c.EnableValkey }

// Server bundles routes and dependencies.
type Server struct {
	cfg      Config
	stats    store.Stats
	conf     store.Config
	disc     *store.DiscoveredCapabilities
	exec     console.Executor
	grants   console.GrantReader
	sessions *console.Sessions
	mux      *http.ServeMux
}

// NewServer returns a fully wired Server. stats is the stats-DB store;
// conf is the config/metadata-DB store (used by the audit-log viewer).
func NewServer(cfg Config, stats store.Stats, conf store.Config) *Server {
	s := &Server{
		cfg:      cfg,
		stats:    stats,
		conf:     conf,
		disc:     store.NewDiscoveredCapabilities(conf.Pool()),
		exec:     console.StubExecutor{},
		grants:   console.StubGrantReader{},
		sessions: console.NewSessions(5),
		mux:      http.NewServeMux(),
	}
	s.routes()
	return s
}

// Handler returns the HTTP handler, with the auth middleware applied.
func (s *Server) Handler() http.Handler { return s.withAuth(s.mux) }

func (s *Server) routes() {
	s.mux.Handle("GET /static/", web.StaticHandler())
	s.mux.HandleFunc("GET /databases", s.handleClusters)
	s.mux.HandleFunc("GET /partial/databases", s.handleClustersPartial)
	s.mux.HandleFunc("GET /nodes", s.handleNodes)
	s.mux.HandleFunc("GET /partial/nodes", s.handleNodesPartial)
	s.mux.HandleFunc("GET /databases/all", s.handleDatabasesList)
	s.mux.HandleFunc("GET /partial/databases/all", s.handleDatabasesListPartial)
	s.mux.HandleFunc("GET /databases/{clusterID}", s.handleClusterOverview)
	s.mux.HandleFunc("GET /databases/{clusterID}/queries", s.handleClusterQueries)
	s.mux.HandleFunc("GET /databases/{clusterID}/insights", s.handleClusterInsights)
	s.mux.HandleFunc("GET /databases/{clusterID}/activity", s.handleClusterActivity)
	s.mux.HandleFunc("GET /databases/{clusterID}/settings", s.handleClusterSettings)
	s.mux.HandleFunc("GET /partial/databases/{clusterID}/query/{fingerprint}", s.handleClusterQueryDrilldown)
	s.mux.HandleFunc("GET /search/domains", s.handleSearchDomains)
	s.mux.HandleFunc("GET /partial/search/domains", s.handleSearchDomainsPartial)
	s.mux.HandleFunc("GET /search/nodes", s.handleSearchNodes)
	s.mux.HandleFunc("GET /partial/search/nodes", s.handleSearchNodesPartial)
	s.mux.HandleFunc("GET /{$}", s.handleFleet)          // root IS the fleet landing shell
	s.mux.HandleFunc("GET /fleet", s.handleFleet)        // hidden alias (old links/bookmarks)
	s.mux.HandleFunc("GET /queries", s.handleDashboard)  // legacy global top-queries (retrofit: ly-ae6.7)
	s.mux.HandleFunc("GET /partial/fleet", s.handleFleetPartial) // fleet dashboard body auto-poll (ly-ae6.4)
	s.mux.HandleFunc("GET /partial/scope-options", s.handleScopeOptions)
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
	s.mux.HandleFunc("GET /console", s.handleConsolePage)
	s.mux.HandleFunc("GET /partial/console", s.handleConsolePartial)
	s.mux.HandleFunc("POST /partial/console/run", s.handleConsoleRun)
	s.mux.HandleFunc("GET /console/export", s.handleConsoleExport)
	s.mux.HandleFunc("GET /cache/clusters", s.handleCacheClusters)
	s.mux.HandleFunc("GET /partial/cache/clusters", s.handleCacheClustersPartial)
	s.mux.HandleFunc("GET /cache/replicasets", s.handleCacheReplicasets)
	s.mux.HandleFunc("GET /partial/cache/replicasets", s.handleCacheReplicasetsPartial)
	s.mux.HandleFunc("GET /cache/nodes", s.handleCacheNodes)
	s.mux.HandleFunc("GET /partial/cache/nodes", s.handleCacheNodesPartial)
	s.mux.HandleFunc("GET /partial/add", s.handleAddComponent)
	s.mux.HandleFunc("GET /partial/modal/close", s.handleModalClose)
	s.mux.HandleFunc("GET /admin/provider-setup", s.handleProviderSetupPage)
	s.mux.HandleFunc("GET /partial/provider-setup", s.handleProviderSetupPartial)
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
		if strings.HasPrefix(r.URL.Path, "/static/") {
			next.ServeHTTP(w, r)
			return
		}
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
