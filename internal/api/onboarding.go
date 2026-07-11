package api

import (
	"net/http"

	"github.com/dobbo-ca/lynceus/web"
)

// handleAddComponent renders the + ADD wizard modal fragment for the given
// vertical (kind) and provider selection. HTMX swaps it into #modal-root, and
// provider chips re-fetch it (outerHTML swap of #add-modal).
func (s *Server) handleAddComponent(w http.ResponseWriter, r *http.Request) {
	kind := web.AddComponentKind(r.URL.Query().Get("kind"))
	provider := web.AddProvider(r.URL.Query().Get("provider"))
	v := web.BuildAddComponentView(kind, provider)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.AddComponentModal(v).Render(r.Context(), w)
}

// handleModalClose returns an empty body so an HTMX innerHTML swap clears the
// #modal-root container (closes any open modal).
func (s *Server) handleModalClose(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
}
