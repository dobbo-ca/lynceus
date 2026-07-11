package web

import (
	"context"
	"strings"
	"testing"
)

func renderLayout(t *testing.T) string {
	t.Helper()
	var sb strings.Builder
	if err := Layout("Test Title", "sub").Render(context.Background(), &sb); err != nil {
		t.Fatalf("render: %v", err)
	}
	return sb.String()
}

func TestLayout_NoExternalHosts(t *testing.T) {
	html := renderLayout(t)
	for _, host := range []string{"unpkg.com", "googleapis.com", "gstatic.com", "cdn.jsdelivr.net"} {
		if strings.Contains(html, host) {
			t.Errorf("layout references external host %q — assets must be self-hosted (privacy backbone)", host)
		}
	}
}

func TestLayout_SelfHostedAssets(t *testing.T) {
	html := renderLayout(t)
	for _, want := range []string{
		`href="/static/css/tokens.css"`,
		`href="/static/css/legacy.css"`,
		`src="/static/js/htmx.min.js"`,
		`src="/static/js/theme.js"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("layout missing self-hosted asset ref %q", want)
		}
	}
}

func TestLayout_DarkDefaultAndBootstrap(t *testing.T) {
	html := renderLayout(t)
	if !strings.Contains(html, `data-theme="dark"`) {
		t.Error(`layout <html> must default to data-theme="dark" (no-JS fallback)`)
	}
	// The no-flash bootstrap must be inlined verbatim.
	if !strings.Contains(html, "window.Lynceus") || !strings.Contains(html, "resolveTheme") {
		t.Error("layout is missing the inline theme bootstrap")
	}
}

// The inline <style> block must be gone — styling now lives in the served CSS.
func TestLayout_NoInlineStyleBlock(t *testing.T) {
	html := renderLayout(t)
	if strings.Contains(html, "system-ui, sans-serif") || strings.Contains(html, "#2b6cb0") {
		t.Error("layout still carries the old inline light stylesheet — it must move to tokens.css/legacy.css")
	}
}
