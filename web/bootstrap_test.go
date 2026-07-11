package web

import (
	"strings"
	"testing"
)

func TestThemeBootstrap_Contents(t *testing.T) {
	for _, want := range []string{
		"window.Lynceus",
		"localStorage.getItem('lynceus.theme')",
		"localStorage.getItem('lynceus.accent')",
		"prefers-color-scheme: light",
		"resolveTheme",
		"applyAccent",
		"'#2dd4bf'", // teal preset present in the accent variant map
		"'#22d3ee'", // cyan
		"'#818cf8'", // indigo
	} {
		if !strings.Contains(themeBootstrapJS, want) {
			t.Errorf("themeBootstrapJS missing %q", want)
		}
	}
	// It must set data-theme and reference no external hosts.
	if !strings.Contains(themeBootstrapJS, "dataset.theme") {
		t.Error("bootstrap must set documentElement.dataset.theme")
	}
	for _, host := range []string{"http://", "https://", "//fonts.", "unpkg"} {
		if strings.Contains(themeBootstrapJS, host) {
			t.Errorf("bootstrap must be self-contained; found external reference %q", host)
		}
	}
}
