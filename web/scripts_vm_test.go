package web

import (
	"testing"
	"time"
)

func TestRelativeAge(t *testing.T) {
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		age  time.Duration
		want string
	}{
		{30 * time.Second, "just now"},
		{5 * time.Minute, "5m ago"},
		{3 * time.Hour, "3h ago"},
		{50 * time.Hour, "2d ago"},
	}
	for _, c := range cases {
		if got := RelativeAge(now.Add(-c.age), now); got != c.want {
			t.Errorf("RelativeAge(-%s) = %q, want %q", c.age, got, c.want)
		}
	}
}

func TestScriptScopeColorAndVisibleTo(t *testing.T) {
	if ScriptScopeColor("GLOBAL") != "var(--acc2)" {
		t.Errorf("GLOBAL color = %q", ScriptScopeColor("GLOBAL"))
	}
	if ScriptScopeColor("TEAM") != "var(--infoT)" {
		t.Errorf("TEAM color = %q", ScriptScopeColor("TEAM"))
	}
	if ScriptScopeColor("PERSONAL") != "var(--warnT)" {
		t.Errorf("PERSONAL color = %q", ScriptScopeColor("PERSONAL"))
	}
	if got := ScriptVisibleTo("GLOBAL", "m.chen", "", false); got != "everyone in the org" {
		t.Errorf("GLOBAL visibleTo = %q", got)
	}
	if got := ScriptVisibleTo("TEAM", "j.alvarez", "dba-oncall", false); got != "group dba-oncall" {
		t.Errorf("TEAM visibleTo = %q", got)
	}
	if got := ScriptVisibleTo("PERSONAL", "s.dobson", "", true); got != "only you" {
		t.Errorf("PERSONAL mine visibleTo = %q", got)
	}
	if got := ScriptVisibleTo("PERSONAL", "s.dobson", "", false); got != "only s.dobson" {
		t.Errorf("PERSONAL theirs visibleTo = %q", got)
	}
}
