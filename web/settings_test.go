package web

import (
	"context"
	"strings"
	"testing"
)

func TestSettingsPage_accentPickerAndRoadmap(t *testing.T) {
	vm := SettingsVM{
		Accents:      []AccentSwatch{{Hex: "#2dd4bf", Name: "TEAL"}, {Hex: "#22d3ee", Name: "CYAN"}, {Hex: "#818cf8", Name: "INDIGO"}},
		RoadmapCards: []SettingsRoadmapCard{{Label: "ORGANIZATION"}, {Label: "THEME DEFAULTS"}, {Label: "INTEGRATIONS"}},
	}
	var sb strings.Builder
	if err := SettingsPage(ShellView{}, vm).Render(context.Background(), &sb); err != nil {
		t.Fatalf("render: %v", err)
	}
	html := sb.String()
	for _, want := range []string{
		`href="/static/css/governance.css"`,
		`src="/static/js/settings.js"`,
		"APPEARANCE — ACCENT COLOR",
		`data-accent="#2dd4bf"`, `data-accent="#22d3ee"`, `data-accent="#818cf8"`,
		">TEAL<", ">CYAN<", ">INDIGO<",
		"SAVED TO YOUR PROFILE",
		"ORGANIZATION", "THEME DEFAULTS", "INTEGRATIONS",
		`class="badge badge--roadmap"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("settings page missing %q", want)
		}
	}
}
