package web

import (
	"context"
	"strings"
	"testing"
)

func TestEngineSprites_definesSymbolsAndNoExternalHosts(t *testing.T) {
	var sb strings.Builder
	if err := EngineSprites().Render(context.Background(), &sb); err != nil {
		t.Fatalf("render: %v", err)
	}
	html := sb.String()
	for _, id := range []string{`id="eng-pg"`, `id="eng-os"`, `id="eng-vk"`} {
		if !strings.Contains(html, id) {
			t.Errorf("sprite set missing %s", id)
		}
	}
	if !strings.Contains(html, "currentColor") {
		t.Error("glyphs must stroke in currentColor to stay theme-aware")
	}
	for _, host := range []string{"http://", "https://", "unpkg.com", "googleapis.com"} {
		if strings.Contains(html, host) {
			t.Errorf("sprite references external host %q — must be self-hosted", host)
		}
	}
}
