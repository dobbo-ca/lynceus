package api

import (
	"net/http"

	"github.com/dobbo-ca/lynceus/web"
)

// handleSearchDomains renders the full Search Domains page inside the design
// shell (fleet scope). Gated on the search-engine enable flags.
func (s *Server) handleSearchDomains(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.SearchEnabled() {
		http.NotFound(w, r)
		return
	}
	vm := s.buildShellView(r, "searchdomains")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.SearchDomainsPage(vm, s.fetchSearchDomains(r)).Render(r.Context(), w)
}

// handleSearchDomainsPartial renders just the Domains body fragment.
func (s *Server) handleSearchDomainsPartial(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.SearchEnabled() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.SearchDomainsBody(s.fetchSearchDomains(r)).Render(r.Context(), w)
}

// handleSearchNodes renders the full Nodes-by-role page inside the design shell.
func (s *Server) handleSearchNodes(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.SearchEnabled() {
		http.NotFound(w, r)
		return
	}
	vm := s.buildShellView(r, "searchnodes")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.SearchNodesPage(vm, s.fetchSearchNodes(r)).Render(r.Context(), w)
}

// handleSearchNodesPartial renders just the Nodes body fragment (SORT toggle target).
func (s *Server) handleSearchNodesPartial(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.SearchEnabled() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.SearchNodesBody(s.fetchSearchNodes(r)).Render(r.Context(), w)
}

// fetchSearchDomains builds the Domains view-model. The OpenSearch/ES collector,
// T1 wire types, and stats-store schema that would populate it are tracked as
// ly-wte (which depends on the collector generalization seam ly-h8x); until they
// land no domains are reported and the screen renders its empty state.
func (s *Server) fetchSearchDomains(_ *http.Request) web.SearchDomainsView {
	return web.SearchDomainsView{}
}

// fetchSearchNodes builds the Nodes view-model. Backend data is pending (ly-wte);
// the sort param is still parsed and echoed so the toggle round-trips today.
func (s *Server) fetchSearchNodes(r *http.Request) web.SearchNodesView {
	sort := r.URL.Query().Get("sort")
	if sort != "name" {
		sort = "heap"
	}
	nodes := []web.SearchNodeRow(nil) // backend ly-wte pending
	web.SortSearchNodes(nodes, sort)
	return web.SearchNodesView{Nodes: nodes, Sort: sort}
}
