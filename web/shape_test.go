package web

import (
	"strings"
	"testing"
)

// shapeCSS reads the embedded shape.css so assertions run against the exact
// bytes shipped to the browser.
func shapeCSS(t *testing.T) string {
	t.Helper()
	b, err := staticFS.ReadFile("static/css/shape.css")
	if err != nil {
		t.Fatalf("read shape.css: %v", err)
	}
	return string(b)
}

// The shape primitives must encode the design's shape language via tokens.
func TestShapeCSS_Primitives(t *testing.T) {
	css := shapeCSS(t)
	for _, want := range []string{
		".card",
		"border-radius: var(--radius)",       // 2px cards
		".badge",
		"border-radius: var(--radius-badge)", // 1px tiny badges
		"border: var(--border) solid",        // 1px borders via token
		".sev-sq",
		"width: 8px",
		"height: 8px",
		"border-radius: 0", // UNROUNDED severity squares
		".icon-btn",
		"width: 24px",
		"height: 24px",                  // 24px icon buttons
		".pop",
		"box-shadow: var(--shadow-pop)", // shadow ONLY on popovers
		".accent-picker",
		".accent-swatch",
		"data-accent='#2dd4bf'",
		"data-accent='#22d3ee'",
		"data-accent='#818cf8'",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("shape.css missing %q", want)
		}
	}
}

// Shadow policy: the ONLY box-shadow in the primitives is the popover shadow
// (dropdowns/modals). Any bare shadow is a shape-language conformance break.
func TestShapeCSS_ShadowPolicy(t *testing.T) {
	css := shapeCSS(t)
	for _, line := range strings.Split(css, "\n") {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, "box-shadow:") &&
			!strings.Contains(l, "var(--shadow-pop)") &&
			!strings.Contains(l, "none") {
			t.Errorf("non-conforming shadow in shape.css: %q (only var(--shadow-pop) or none allowed)", l)
		}
	}
}
