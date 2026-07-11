package api

import (
	"net/http"

	"github.com/dobbo-ca/lynceus/web"
)

// handleSettingsPage renders Settings › Appearance inside the design shell. The
// accent picker persists via the F1 setter (localStorage); server-side per-user
// persistence awaits the M5 users model.
func (s *Server) handleSettingsPage(w http.ResponseWriter, r *http.Request) {
	sv := s.buildShellView(r, "")
	vm := web.SettingsVM{
		Accents: []web.AccentSwatch{
			{Hex: "#2dd4bf", Name: "TEAL"},
			{Hex: "#22d3ee", Name: "CYAN"},
			{Hex: "#818cf8", Name: "INDIGO"},
		},
		RoadmapCards: []web.SettingsRoadmapCard{
			{Label: "ORGANIZATION"},
			{Label: "THEME DEFAULTS"},
			{Label: "INTEGRATIONS"},
		},
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.SettingsPage(sv, vm).Render(r.Context(), w)
}
