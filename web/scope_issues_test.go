package web

import (
	"context"
	"strings"
	"testing"

	"github.com/a-h/templ"
)

func renderComponent(t *testing.T, c templ.Component) string {
	t.Helper()
	var sb strings.Builder
	if err := c.Render(context.Background(), &sb); err != nil {
		t.Fatalf("render: %v", err)
	}
	return sb.String()
}

func TestScopedIssues_listsIssues(t *testing.T) {
	vm := ScopedIssuesVM{
		ScopeKind: "CLUSTER", Count: 1, ShowServer: true,
		Issues: []ScopeIssueVM{{
			Severity: "crit", ID: "settings.fsync", Detail: "fsync is off",
			Server: "srv-a", Age: "10m", Href: "/checks",
		}},
	}
	got := renderComponent(t, ScopedIssues(vm))
	if !strings.Contains(got, "OPEN ISSUES ON THIS CLUSTER") {
		t.Error("missing scoped-issues header")
	}
	if !strings.Contains(got, "settings.fsync") || !strings.Contains(got, "sev-crit") {
		t.Errorf("missing issue row / crit modifier; got: %s", got)
	}
	if !strings.Contains(got, "srv-a") {
		t.Error("ShowServer true should render the server column")
	}
}

func TestScopedIssues_cleanStrip(t *testing.T) {
	got := renderComponent(t, ScopedIssues(ScopedIssuesVM{ScopeKind: "NODE", Count: 0}))
	if !strings.Contains(got, "NO OPEN CHECKS OR INSIGHTS ON THIS NODE") {
		t.Errorf("missing clean strip; got: %s", got)
	}
}

func TestNodeCards_scopeButtonHref(t *testing.T) {
	got := renderComponent(t, NodeCards([]ScopeNodeVM{{
		Role: "PRIMARY", RoleMod: "primary", Name: "node-1", Meta: "3 calls",
		Health: "OK", HealthMod: "ok", ScopeHref: "/nodes?scope=node%3Ac1%3An1",
	}}))
	if !strings.Contains(got, "scope-btn") || !strings.Contains(got, `href="/nodes?scope=node%3Ac1%3An1"`) {
		t.Errorf("node card missing ⌖ scope button href; got: %s", got)
	}
	if !strings.Contains(got, "node-role is-primary") {
		t.Error("missing role modifier class")
	}
}
