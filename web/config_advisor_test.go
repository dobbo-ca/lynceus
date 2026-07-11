package web

import (
	"context"
	"strings"
	"testing"
)

func TestConfigAdvisorScreen_PerServerPickerAndGroupColumn(t *testing.T) {
	vm := ConfigAdvisorVM{
		Servers: []ConfigServerTab{
			{ID: "srv-1", Label: "srv-orders-primary", Sub: "3 findings", Selected: true},
			{ID: "srv-2", Label: "srv-orders-replica-1", Sub: "0 findings"},
		},
		ScopeName: "srv-orders-primary · CONFIG",
		Rows: []ConfigAdvisorRow{{
			Group: "MEMORY", Setting: "work_mem", SevClass: "warn",
			Current: "4MB", Suggested: "16MB", Detail: "disk sorts observed",
		}},
	}
	var sb strings.Builder
	_ = ConfigAdvisorScreen(vm).Render(context.Background(), &sb)
	html := sb.String()
	for _, want := range []string{
		"Config Advisor", "SETTINGS APPLY PER SERVER INSTANCE", "CURATED PG_SETTINGS ALLOWLIST",
		"srv-orders-primary", "3 findings", "chip--on",
		"GROUP", "SETTING", "CURRENT", "SUGGESTED", "RATIONALE",
		"MEMORY", "work_mem", "stripe-warn",
		"FREE-TEXT SETTINGS NEVER LEAVE THE SERVER",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("ConfigAdvisorScreen missing %q", want)
		}
	}
}
