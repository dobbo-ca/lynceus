package web

import (
	"context"
	"strings"
	"testing"
)

func TestScreenHeader_RendersTitleAndBadges(t *testing.T) {
	h := ScreenHeader("Top Queries", []HeaderBadge{
		{Text: "LIVE", Kind: "live"},
		{Text: "T1 · NORMALIZED", Kind: "t1"},
	})
	var sb strings.Builder
	if err := h.Render(context.Background(), &sb); err != nil {
		t.Fatalf("render: %v", err)
	}
	html := sb.String()
	for _, want := range []string{
		`class="screen-hd"`, `class="screen-title"`, `Top Queries`,
		`badge--live`, `LIVE`, `badge--t1`, `T1 · NORMALIZED`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("ScreenHeader missing %q\n%s", want, html)
		}
	}
}

func TestEmptyState_RendersMessage(t *testing.T) {
	var sb strings.Builder
	_ = EmptyState("nothing here").Render(context.Background(), &sb)
	if !strings.Contains(sb.String(), "nothing here") || !strings.Contains(sb.String(), "empty-state") {
		t.Errorf("EmptyState wrong output: %s", sb.String())
	}
}
