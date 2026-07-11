package api

import (
	"net/http"

	"github.com/dobbo-ca/lynceus/web"
)

// handleProviderSetupPage renders the full Provider Setup admin page inside the
// design Shell. The ?provider= query pre-selects a guide (used by the wizard's
// deep-link). Content is static T1 setup guidance only — no store reads.
func (s *Server) handleProviderSetupPage(w http.ResponseWriter, r *http.Request) {
	shell := s.buildShellView(r)
	shell.Title = "Lynceus — provider setup"
	v := web.BuildProviderSetupView(web.ProviderID(r.URL.Query().Get("provider")))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.ProviderSetupPage(shell, v).Render(r.Context(), w)
}

// handleProviderSetupPartial renders just the #provider-setup-body fragment,
// re-rendered when a provider block button is chosen.
func (s *Server) handleProviderSetupPartial(w http.ResponseWriter, r *http.Request) {
	v := web.BuildProviderSetupView(web.ProviderID(r.URL.Query().Get("provider")))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.ProviderSetupBody(v).Render(r.Context(), w)
}
