package web

import (
	"context"
	"strings"
	"testing"
)

func renderModal(t *testing.T, v AddComponentView) string {
	t.Helper()
	var sb strings.Builder
	if err := AddComponentModal(v).Render(context.Background(), &sb); err != nil {
		t.Fatalf("render: %v", err)
	}
	return sb.String()
}

func TestAddComponentModal_AWS_rendersTokensChipsYamlAndGuide(t *testing.T) {
	html := renderModal(t, BuildAddComponentView(AddKindDatabase, ProviderAWS))

	for _, want := range []string{
		`id="add-modal"`,
		`hx-target="#modal-root"`,     // close swaps the #modal-root container (added on the databases page — Task 6 — NOT in this fragment)
		`var(--surface)`,              // tokens, not legacy classes
		`ADD DATABASE CLUSTER`,        // title
		`SELF-HOSTED`, `AWS`, `AZURE`, // provider chips
		`id="add-yaml"`,        // copyable YAML block
		`data-copy="add-yaml"`, // copy button hook
		`LYNCEUS_COLLECTOR_TOKEN`,
		`/partial/add?kind=database&amp;provider=aws`, // chip re-fetch (& is escaped in attr)
		`/partial/modal/close`,                        // close route
		`/admin/provider-setup?provider=aws`,          // guide deep-link (AWS)
	} {
		if !strings.Contains(html, want) {
			t.Errorf("modal missing %q", want)
		}
	}
	// must not leak legacy component classes onto this new screen
	if strings.Contains(html, `class="db-card"`) || strings.Contains(html, `class="filters"`) {
		t.Error("wizard modal must be token-styled, not legacy classes")
	}
}

func TestAddComponentModal_Self_hidesGuideLink(t *testing.T) {
	html := renderModal(t, BuildAddComponentView(AddKindDatabase, ProviderSelf))
	if strings.Contains(html, "/admin/provider-setup?provider=") {
		t.Error("self-hosted wizard must not render the provider guide deep-link")
	}
}
