package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func navCSSBody(t *testing.T) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/static/css/nav.css", nil)
	rec := httptest.NewRecorder()
	StaticHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET nav.css = %d, want 200", rec.Code)
	}
	return rec.Body.String()
}

func TestStaticHandler_ServesNavCSS(t *testing.T) {
	body := navCSSBody(t)
	for _, want := range []string{
		".ln-nav",              // rail container
		".ln-nav-head",         // section header
		".ln-nav-item--active", // active item
		".ln-nav-badge",        // SOON/T2 badge
		"width: 208px",         // design rail width
		"var(--font-mono)",     // mono font
		"var(--accbg)",         // active tinted bg
	} {
		if !strings.Contains(body, want) {
			t.Errorf("nav.css missing %q", want)
		}
	}
}

func TestNavCSS_TokensNotHardcoded(t *testing.T) {
	body := navCSSBody(t)
	for _, banned := range []string{"#2b6cb0", "system-ui", "#0c1118", "#2dd4bf"} {
		if strings.Contains(body, banned) {
			t.Errorf("nav.css hardcodes %q — use design tokens (var(--x))", banned)
		}
	}
}

// Guards against legacy.css bare 'nav a'/'nav' rules leaking font-size:0.9rem
// and margin-right:1rem onto the sidebar anchors (see Task 4 analysis).
func TestNavCSS_NeutralizesLegacyNavBleed(t *testing.T) {
	body := navCSSBody(t)
	for _, want := range []string{
		".ln-nav a",       // anchor-level reset that outranks bare `nav a`
		"font-size: 12px", // explicit item font-size (not the legacy 0.9rem)
		"margin: 0",       // neutralize legacy nav/nav a margins
	} {
		if !strings.Contains(body, want) {
			t.Errorf("nav.css missing legacy-bleed guard %q", want)
		}
	}
}
