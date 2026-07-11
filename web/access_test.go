package web

import (
	"context"
	"strings"
	"testing"
)

func TestAccessRolesPage_previewAndGroups(t *testing.T) {
	vm := AccessRolesVM{Groups: []AccessGroupRow{
		{Name: "dba-oncall", Members: "4 users", T2Label: "T2: REVEAL", T2Kind: "reveal", Scope: "orders-prod only"},
		{Name: "security-audit", Members: "2 users", T2Label: "T2: AUDIT VIEW", T2Kind: "audit", Scope: "audit log only"},
	}}
	var sb strings.Builder
	if err := AccessRolesPage(ShellView{}, vm).Render(context.Background(), &sb); err != nil {
		t.Fatalf("render: %v", err)
	}
	html := sb.String()
	for _, want := range []string{
		`href="/static/css/governance.css"`,
		`class="badge badge--roadmap"`,
		"ROADMAP PREVIEW",
		"CONTINUE WITH OIDC SSO",
		"SCIM 2.0",
		"RBAC GROUPS → T2 RIGHTS",
		"dba-oncall", "T2: REVEAL",
		"t2-tag--reveal", "t2-tag--audit",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("access page missing %q", want)
		}
	}
}
