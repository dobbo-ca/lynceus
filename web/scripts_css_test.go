package web

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/scope"
)

func TestScriptsCSS_usesTokensNoExternalHosts(t *testing.T) {
	body, err := os.ReadFile("static/css/scripts.css")
	if err != nil {
		t.Fatalf("read scripts.css: %v", err)
	}
	css := string(body)
	if !strings.Contains(css, "var(--") {
		t.Error("scripts.css must be token-styled (no var(--...) references found)")
	}
	for _, host := range []string{"http://", "https://", "unpkg.com", "googleapis.com", "cdn."} {
		if strings.Contains(css, host) {
			t.Errorf("scripts.css references external host %q — assets must be self-hosted", host)
		}
	}
}

// The Saved Scripts screens render inside the design Shell (not the legacy
// Layout), so the Shell head must link scripts.css for them to style.
func TestShell_LinksScriptsCSS(t *testing.T) {
	var sb strings.Builder
	vm := ShellView{
		Scope:   scope.Scope{},
		Title:   "Test",
		Sidebar: Sidebar(scope.Scope{}, "FLEET", DefaultEngines(), "scripts"),
	}
	if err := Shell(vm).Render(context.Background(), &sb); err != nil {
		t.Fatalf("render shell: %v", err)
	}
	if !strings.Contains(sb.String(), `href="/static/css/scripts.css"`) {
		t.Error("shell is missing the scripts.css link")
	}
}
