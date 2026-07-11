package web

import (
	"context"
	"strings"
	"testing"
)

func renderProviderBody(t *testing.T, v ProviderSetupView) string {
	t.Helper()
	var sb strings.Builder
	if err := ProviderSetupBody(v).Render(context.Background(), &sb); err != nil {
		t.Fatalf("render: %v", err)
	}
	return sb.String()
}

func TestProviderSetupBody_Unselected_showsPromptAndBlocks(t *testing.T) {
	html := renderProviderBody(t, BuildProviderSetupView(""))
	for _, want := range []string{
		`id="provider-setup-body"`,
		`SELECT A PROVIDER TO SEE ITS SETUP GUIDE`,
		`/partial/provider-setup?provider=aws`,
		`/partial/provider-setup?provider=azure`,
		`/partial/provider-setup?provider=planetscale`,
		`var(--surface)`, // tokens
	} {
		if !strings.Contains(html, want) {
			t.Errorf("unselected body missing %q", want)
		}
	}
}

func TestProviderSetupBody_AWS_rendersStepsAndCopyHooks(t *testing.T) {
	html := renderProviderBody(t, BuildProviderSetupView(ProviderSetupAWS))
	for _, want := range []string{
		"PATH 3 — FIREHOSE INGESTION (CONTROLLED INGRESS)",
		"X-Lynceus-Tenant",
		`id="guide-code-1"`,
		`data-copy="guide-code-1"`,
		`id="guide-code-5"`, // terraform step has code
		"TERRAFORM",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("aws body missing %q", want)
		}
	}
	// VERIFY (step 6) has no code — its copy hook must be absent
	if strings.Contains(html, `data-copy="guide-code-6"`) {
		t.Error("VERIFY step has no code and must not render a copy button")
	}
}

func TestProviderSetupPage_wrapsShell(t *testing.T) {
	var sb strings.Builder
	if err := ProviderSetupPage(fleetShellView(), BuildProviderSetupView("")).Render(context.Background(), &sb); err != nil {
		t.Fatalf("render: %v", err)
	}
	html := sb.String()
	// Full page: renders inside the design Shell (DOCTYPE + top-bar chrome) and
	// carries the Provider Setup heading in the main body.
	for _, want := range []string{"<!doctype html>", "LYNCEUS", "Provider Setup", `id="provider-setup-body"`} {
		if !strings.Contains(html, want) {
			t.Errorf("page missing %q — must wrap the Shell and carry the Provider Setup body", want)
		}
	}
	// Shell is token-based; the page must not pull in legacy.css.
	if strings.Contains(html, "legacy.css") {
		t.Error("Provider Setup page must render inside the token-based Shell, not legacy Layout")
	}
}
