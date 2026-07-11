package api

import (
	"net/http"

	"github.com/dobbo-ca/lynceus/web"
)

// handleAccessPage renders the Access & Roles roadmap preview inside the design
// shell. The group rows are illustrative until RBAC (ly-8b0.4) + SCIM (ly-8b0.2)
// provide real data. T1-safe: no monitored-DB literal is rendered.
func (s *Server) handleAccessPage(w http.ResponseWriter, r *http.Request) {
	sv := s.buildShellView(r, "")
	vm := web.AccessRolesVM{
		Groups: []web.AccessGroupRow{
			{Name: "dba-oncall", Members: "4 users", T2Label: "T2: REVEAL", T2Kind: "reveal", Scope: "orders-prod only"},
			{Name: "platform", Members: "11 users", T2Label: "T2: NONE", T2Kind: "none", Scope: "fleet, read"},
			{Name: "security-audit", Members: "2 users", T2Label: "T2: AUDIT VIEW", T2Kind: "audit", Scope: "audit log only"},
		},
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.AccessRolesPage(sv, vm).Render(r.Context(), w)
}
