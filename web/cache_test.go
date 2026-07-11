package web

import (
	"context"
	"strings"
	"testing"

	"github.com/a-h/templ"
)

func renderBody(t *testing.T, c templ.Component) string {
	t.Helper()
	var sb strings.Builder
	if err := c.Render(context.Background(), &sb); err != nil {
		t.Fatalf("render: %v", err)
	}
	return sb.String()
}

func TestShell_LinksCacheStylesheet(t *testing.T) {
	html := renderShell(t, fleetShellView())
	if !strings.Contains(html, `href="/static/css/cache.css"`) {
		t.Error("shell must link /static/css/cache.css")
	}
}

func TestCacheEngineSprite_HasKeyGlyph(t *testing.T) {
	html := renderBody(t, cacheEngineSprite())
	if !strings.Contains(html, `id="eng-vk"`) {
		t.Error("sprite must define #eng-vk symbol")
	}
}
