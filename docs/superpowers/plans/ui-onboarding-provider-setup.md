# Onboarding Wizard (+ ADD) + Provider Setup Guides Implementation Plan

> For agentic workers: execute this plan with the **superpowers:subagent-driven-development** skill. Each task is self-contained and TDD-ordered (failing test → run/expect FAIL → implement → run/expect PASS → commit). Do not skip the failing-test step. templ changes require `make templ` before `go build`/`go test`; the generated `_templ.go` files are committed and CI checks they are in sync.

**Goal:** Ship the `+ ADD` onboarding wizard modal (provider chips → collector-token step → copyable Kubernetes Deployment YAML using the real collector env contract → provider-specific step → "appears on first report") and the Provider Setup admin page (AWS/Azure/PlanetScale block buttons with selection-gated guides — AWS three-path + Terraform), built entirely on the F1 design tokens.

**Architecture:** Two new token-styled surfaces follow the existing `web` SSR/HTMX pattern — pure Go view-model builders in package `web` (`onboarding_vm.go`, `provider_setup_vm.go`) feed templ fragments (`onboarding.templ`, `provider_setup.templ`); thin `internal/api` handlers register HTMX routes (`/partial/add`, `/partial/modal/close`, `/admin/provider-setup`, `/partial/provider-setup`) and re-render fragments on chip/block selection. All content is static setup guidance and generated manifests with placeholder values only — no store reads, no T1/T2 database data flows through these screens — so the surfaces are unit-testable without a database, and the API handler tests reuse the existing testcontainer `setup()` harness only to satisfy `NewServer`.

**Tech Stack:** Go 1.x, `github.com/a-h/templ` (v0.3.1020), htmx (self-hosted), net/http `ServeMux`, design tokens in `web/static/css/tokens.css`. Module path `github.com/dobbo-ca/lynceus`.

## Global Constraints

Copy these rules into your working memory; every task must respect them.

- **Privacy — T1 only.** Only T1 (normalized, literal-free) data may render. These screens render NO database data at all — only static guidance text and generated manifests/policies with `<placeholder>` values. Never introduce a raw-literal field or a real query sample into any path here. There is a contract test enforcing T1 message purity at the wire layer; do not weaken it.
- **T2 discipline.** Nothing on these screens is T2. Do not add audited reveals, RBAC-gated literal views, or any `data_tier`>0 read. (Provider Setup is an *admin* page; RBAC admin-gating beyond the existing DevAuth gate is future work — note it, do not build it.)
- **No external hosts.** Never reference a CDN, external font, external script, or remote image. All CSS/JS/fonts are served from `/static/` and embedded via `web.StaticHandler()`. `TestLayout_NoExternalHosts` enforces this — keep it green.
- **Tokens, not legacy.** These are NEW screens: style them exclusively with the F1 design tokens (`var(--surface)`, `var(--line)`, `var(--font-mono)`, …) via inline styles matching the prototype. Do NOT use `web/static/css/legacy.css` component classes.
- **templ regen.** After editing any `*.templ`, run `make templ` to regenerate the committed `*_templ.go`. Never hand-edit generated files. Commit the `.templ` and its regenerated `_templ.go` together.
- **testcontainers, not mocks.** Integration/handler tests hit REAL Postgres via testcontainers (they `t.Skipf` when docker is unavailable). Do not add DB mocks. Pure view-model tests (package `web`) need no database.
- **Concurrent-session git hygiene.** Work on this session's own branch (topic + unique token). Verify `git branch --show-current` before every commit. `web/layout.templ` and `internal/api/server.go` are also touched by ly-ae6.2/ly-ae6.3; keep edits to them surgical and additive.

---

## Integration contract (dependencies — built elsewhere, referenced here)

- **ly-ae6.2 (top bar + scope model)** and **ly-ae6.13 (governance/user-menu/settings)** own `web/layout.templ`'s top bar and the user-menu ▸ ADMIN section. This plan **produces** the Provider Setup page at route `GET /admin/provider-setup`; those beads **consume** it by adding a user-menu ADMIN entry `{ Label: "Provider Setup", Href: "/admin/provider-setup" }`. This plan does not build the user menu. If the menu is not yet built when this lands, the page is still reachable by direct URL and by the wizard's "FULL SETUP GUIDE" deep-link.
  - **Reciprocal-task requirement (de-orphaning the deferral).** COMPARISON flags "No user-menu Admin section wiring in `web/layout.templ`" for both areas; this plan deliberately defers that host-surface wiring to ly-ae6.13. So the deferral is **not orphaned**, a reciprocal note was recorded on ly-ae6.13 when this plan was finalized (2026-07-10): it instructs ly-ae6.13 to add the ADMIN entry `{Label:"Provider Setup", Href:"/admin/provider-setup"}` and notes that until then the page is reachable by direct URL + the wizard FULL SETUP GUIDE deep-link. Implementer: verify it survived with `bd show ly-ae6.13` (look for the "RECIPROCAL (from ly-ae6.12 plan)" note); if it is missing, re-add it before closing this bead. The Provider Setup page ships with **no in-app navigation entry** until ly-ae6.2/ly-ae6.13 land — that is an accepted, explicitly-tracked gap, not a silent one.
- **ly-ae6.3 (scoped low-level nav) — intentionally NOT a dependency.** ly-ae6.3 owns the scoped left-nav that appears only when a scope is selected (queries/advisors/checks/schema/logs/capabilities). Neither surface this plan builds sits in that nav: the **+ ADD CLUSTER** entry point lives on the fleet-level Databases **list** page (`web/databases.templ`, Task 6), and **Provider Setup** is a standalone top-level `/admin/*` page reached from the user menu (ly-ae6.13), not from scoped nav. This omission from the dependency list is deliberate — recorded here so a reviewer knows ly-ae6.3 was considered and correctly excluded.
- **ly-8b0.8 (collector enrollment + scoped token issuance)** is the real backend for issuing the collector token. It is **not required** for this UI: the wizard instructs the operator to create the token out-of-band (user menu ▸ Collectors, roadmap) and store it as the k8s secret `lynceus-token`; the generated YAML references that secret via `secretKeyRef`. When ly-8b0.8 lands, a future bead can replace the manual step-1 instruction with a live "issue token" action. Dependency noted, not re-planned here.
- **Ingest self-registration on first report** (ingestion/store, tracked under M5) is the backend that makes step-4's "appears in this list once the collector first reports" literally true. This plan renders that as guidance copy only; no UI backend is added for it.
- **Collector env contract (verified, real).** `cmd/collector/main.go` reads `LYNCEUS_SERVER_ID`, `LYNCEUS_PG_DSN`, `LYNCEUS_INGESTION_URL` (all required; `main.go:407`) and `LYNCEUS_COLLECTOR_TOKEN` (`main.go:367`). The wizard's copyable Deployment YAML MUST emit these four real vars — reconciling away the prototype's illustrative `TARGET_KIND`/`TARGET_ENDPOINT`/`LYNCEUS_TOKEN` placeholders (COMPARISON gap). **Single-surface consistency decision (Task 3 guide reconciled too):** the AWS path-1 guide code block sits on the *same admin surface* as the wizard, so it must NOT contradict the wizard on the collector's DSN var. Task 3 therefore renames the prototype's `TARGET_ENDPOINT` to the real `LYNCEUS_PG_DSN` in the guide, and marks the remaining prototype-only pipeline hints (`SHIP_VIA`, `FIREHOSE_STREAM`) with an inline `# illustrative — NOT read by the collector; ingress is configured Terraform-side (step 5)` comment. The design-authoritative `LYNCEUS_DB_ROLE` env-placeholder *narrative* (per-environment role name) is preserved verbatim — that is directional setup documentation, intentionally distinct from the wizard's concrete manifest, and does not conflict with any real var.

---

### Task 1: Add-component wizard view-model + builder

Pure Go, no templ, no DB. Produces everything the modal fragment renders. The YAML uses the **real** collector env contract.

**Files**
- Create: `web/onboarding_vm.go`
- Create (test): `web/onboarding_vm_test.go`

**Interfaces**

Produces:
```go
package web

// AddComponentKind identifies which vertical's "+ ADD" wizard is open.
type AddComponentKind string

const (
	AddKindDatabase AddComponentKind = "database"
	AddKindSearch   AddComponentKind = "search"
	AddKindCache    AddComponentKind = "cache"
)

// AddProvider identifies the selected provider chip in the wizard.
type AddProvider string

const (
	ProviderSelf  AddProvider = "self"
	ProviderAWS   AddProvider = "aws"
	ProviderAzure AddProvider = "azure"
)

// ProviderChip is one selectable provider option in the wizard header.
type ProviderChip struct {
	ID       AddProvider
	Label    string // "SELF-HOSTED" / "AWS" / "AZURE"
	Selected bool
}

// AddComponentView is the full view-model for the + ADD wizard modal.
// All fields are static guidance or generated manifests with placeholder
// values only — no database data.
type AddComponentView struct {
	Kind          AddComponentKind
	Title         string // "ADD DATABASE CLUSTER"
	Noun          string // "CLUSTER" / "DOMAIN"
	Provider      AddProvider
	Chips         []ProviderChip
	YAML          string // copyable k8s Deployment manifest (real env contract)
	ProviderNote  string // step-3 provider-specific instruction
	ShowGuideLink bool   // true for AWS/Azure (deep-link into Provider Setup)
	GuideProvider string // provider-setup ?provider= value ("aws"/"azure"); "" if none
}

// BuildAddComponentView assembles the wizard view-model for a vertical +
// provider selection. Unknown kind falls back to database; unknown provider
// falls back to self-hosted.
func BuildAddComponentView(kind AddComponentKind, provider AddProvider) AddComponentView
```

Consumes: nothing (pure).

- [ ] **Step 1: Write the failing test.** Create `web/onboarding_vm_test.go`:
```go
package web

import (
	"strings"
	"testing"
)

func TestBuildAddComponentView_DatabaseSelf_realEnvContract(t *testing.T) {
	v := BuildAddComponentView(AddKindDatabase, ProviderSelf)

	if v.Title != "ADD DATABASE CLUSTER" {
		t.Errorf("Title = %q, want ADD DATABASE CLUSTER", v.Title)
	}
	if v.Noun != "CLUSTER" {
		t.Errorf("Noun = %q, want CLUSTER", v.Noun)
	}
	// The YAML must emit the REAL collector env contract (COMPARISON gap:
	// reconcile away the prototype TARGET_KIND/TARGET_ENDPOINT/LYNCEUS_TOKEN).
	for _, want := range []string{
		"LYNCEUS_SERVER_ID",
		"LYNCEUS_COLLECTOR_TOKEN",
		"LYNCEUS_INGESTION_URL",
		"LYNCEUS_PG_DSN",
		"kind: Deployment",
		"secretKeyRef",
	} {
		if !strings.Contains(v.YAML, want) {
			t.Errorf("YAML missing %q\n---\n%s", want, v.YAML)
		}
	}
	for _, bad := range []string{"TARGET_KIND", "TARGET_ENDPOINT", "LYNCEUS_TOKEN\n", "CLOUD_PROVIDER"} {
		if strings.Contains(v.YAML, bad) {
			t.Errorf("YAML still carries retired placeholder %q", bad)
		}
	}
	if v.ShowGuideLink {
		t.Error("self-hosted must not show the provider guide deep-link")
	}
	// chip selection reflects provider
	var selfSelected bool
	for _, c := range v.Chips {
		if c.ID == ProviderSelf {
			selfSelected = c.Selected
		}
	}
	if !selfSelected {
		t.Error("SELF-HOSTED chip must be marked Selected for ProviderSelf")
	}
}

func TestBuildAddComponentView_AWS_showsGuideAndRDSNote(t *testing.T) {
	v := BuildAddComponentView(AddKindDatabase, ProviderAWS)
	if !v.ShowGuideLink || v.GuideProvider != "aws" {
		t.Errorf("AWS must deep-link to provider guide aws; got ShowGuideLink=%v GuideProvider=%q", v.ShowGuideLink, v.GuideProvider)
	}
	for _, want := range []string{"AWS", "RDS", "IRSA"} {
		if !strings.Contains(v.ProviderNote, want) {
			t.Errorf("AWS ProviderNote missing %q: %q", want, v.ProviderNote)
		}
	}
}

func TestBuildAddComponentView_SearchDomainNoun(t *testing.T) {
	v := BuildAddComponentView(AddKindSearch, ProviderSelf)
	if v.Title != "ADD SEARCH DOMAIN" || v.Noun != "DOMAIN" {
		t.Errorf("search: Title=%q Noun=%q, want ADD SEARCH DOMAIN / DOMAIN", v.Title, v.Noun)
	}
}

func TestBuildAddComponentView_UnknownFallsBackToDatabaseSelf(t *testing.T) {
	v := BuildAddComponentView(AddComponentKind("bogus"), AddProvider("bogus"))
	if v.Title != "ADD DATABASE CLUSTER" || v.Provider != ProviderSelf {
		t.Errorf("fallback failed: Title=%q Provider=%q", v.Title, v.Provider)
	}
}
```

- [ ] **Step 2: Run it — expect FAIL (undefined symbols).**
```
go test ./web/ -run TestBuildAddComponentView
```
Expected: compile error `undefined: BuildAddComponentView` (and the types) — a red FAIL.

- [ ] **Step 3: Implement `web/onboarding_vm.go`.**
```go
package web

import "strings"

// AddComponentKind identifies which vertical's "+ ADD" wizard is open.
type AddComponentKind string

const (
	AddKindDatabase AddComponentKind = "database"
	AddKindSearch   AddComponentKind = "search"
	AddKindCache    AddComponentKind = "cache"
)

// AddProvider identifies the selected provider chip in the wizard.
type AddProvider string

const (
	ProviderSelf  AddProvider = "self"
	ProviderAWS   AddProvider = "aws"
	ProviderAzure AddProvider = "azure"
)

// ProviderChip is one selectable provider option in the wizard header.
type ProviderChip struct {
	ID       AddProvider
	Label    string
	Selected bool
}

// AddComponentView is the full view-model for the + ADD wizard modal.
type AddComponentView struct {
	Kind          AddComponentKind
	Title         string
	Noun          string
	Provider      AddProvider
	Chips         []ProviderChip
	YAML          string
	ProviderNote  string
	ShowGuideLink bool
	GuideProvider string
}

type addKindMeta struct {
	title string // "ADD DATABASE CLUSTER"
	noun  string // "CLUSTER" / "DOMAIN"
	dep   string // deployment name suffix
	dsn   string // LYNCEUS_PG_DSN placeholder
}

// addKinds. NOTE on the `dsn` field for search/cache: the DATABASE kind maps
// cleanly onto the real collector var LYNCEUS_PG_DSN (a Postgres DSN — the only
// endpoint var cmd/collector/main.go actually reads). The search/cache rows put
// an OpenSearch/Valkey URL in the SAME LYNCEUS_PG_DSN slot, which is
// semantically wrong for a Postgres-named var — those engines need their own
// endpoint env var, which the real collector does not yet define. This is
// harmless HERE because only the DATABASE "+ ADD" entry point is wired (Task 6);
// the search/cache entry points are deferred to their own vertical beads. Those
// beads MUST replace this placeholder DSN mapping with the correct per-engine
// endpoint var before wiring a search/cache "+ ADD" button — do not inherit this
// misleading manifest. (See the Task 1 caveat note and Self-Review deferral row.)
var addKinds = map[AddComponentKind]addKindMeta{
	AddKindDatabase: {title: "ADD DATABASE CLUSTER", noun: "CLUSTER", dep: "postgres", dsn: "postgres://$(LYNCEUS_DB_ROLE)@<primary-host>:5432/postgres"},
	AddKindSearch:   {title: "ADD SEARCH DOMAIN", noun: "DOMAIN", dep: "opensearch", dsn: "https://<domain-endpoint>:9200"}, // placeholder slot — see NOTE above
	AddKindCache:    {title: "ADD CACHE CLUSTER", noun: "CLUSTER", dep: "valkey", dsn: "valkey://<primary-host>:6379"},       // placeholder slot — see NOTE above
}

// BuildAddComponentView assembles the wizard view-model for a vertical +
// provider selection.
func BuildAddComponentView(kind AddComponentKind, provider AddProvider) AddComponentView {
	meta, ok := addKinds[kind]
	if !ok {
		kind, meta = AddKindDatabase, addKinds[AddKindDatabase]
	}
	switch provider {
	case ProviderSelf, ProviderAWS, ProviderAzure:
	default:
		provider = ProviderSelf
	}

	v := AddComponentView{
		Kind:     kind,
		Title:    meta.title,
		Noun:     meta.noun,
		Provider: provider,
		YAML:     addYAML(meta),
		Chips: []ProviderChip{
			{ID: ProviderSelf, Label: "SELF-HOSTED", Selected: provider == ProviderSelf},
			{ID: ProviderAWS, Label: "AWS", Selected: provider == ProviderAWS},
			{ID: ProviderAzure, Label: "AZURE", Selected: provider == ProviderAzure},
		},
	}
	switch provider {
	case ProviderAWS:
		v.ProviderNote = "3 · AWS — attach an IRSA role scoped strictly to RDS (rds:Describe* on lynceus=true-tagged ARNs; CloudWatch reads limited to the AWS/RDS namespace) so the collector can read provider metadata and cover endpoints it cannot query directly, like a Multi-AZ standby."
		v.ShowGuideLink = true
		v.GuideProvider = "aws"
	case ProviderAzure:
		v.ProviderNote = "3 · AZURE — grant the collector identity Monitoring Reader on the resource group so it can pull Azure Monitor metrics (covers the zone-redundant HA standby)."
		v.ShowGuideLink = true
		v.GuideProvider = "azure"
	default:
		v.ProviderNote = "3 · SELF-HOSTED — the collector connects straight to the endpoint in LYNCEUS_PG_DSN; no cloud role required."
	}
	return v
}

// addYAML renders the copyable Kubernetes Deployment using the real collector
// env contract (LYNCEUS_SERVER_ID / LYNCEUS_COLLECTOR_TOKEN /
// LYNCEUS_INGESTION_URL / LYNCEUS_PG_DSN — see cmd/collector/main.go).
func addYAML(m addKindMeta) string {
	lines := []string{
		"apiVersion: apps/v1",
		"kind: Deployment",
		"metadata:",
		"  name: lynceus-collector-" + m.dep,
		"  namespace: lynceus",
		"spec:",
		"  replicas: 1",
		"  selector:",
		"    matchLabels: { app: lynceus-collector-" + m.dep + " }",
		"  template:",
		"    metadata:",
		"      labels: { app: lynceus-collector-" + m.dep + " }",
		"    spec:",
		"      serviceAccountName: lynceus-collector",
		"      containers:",
		"        - name: collector",
		"          image: lynceus/collector:1.8",
		"          env:",
		"            - name: LYNCEUS_SERVER_ID",
		"              value: \"<cluster-name>\"",
		"            - name: LYNCEUS_COLLECTOR_TOKEN",
		"              valueFrom: { secretKeyRef: { name: lynceus-token, key: token } }",
		"            - name: LYNCEUS_INGESTION_URL",
		"              value: \"wss://ingest.<region>.lynceus.io/v1/collector\"",
		"            - name: LYNCEUS_PG_DSN",
		"              value: \"" + m.dsn + "\"",
	}
	return strings.Join(lines, "\n")
}
```

- [ ] **Step 4: Run it — expect PASS.**
```
go test ./web/ -run TestBuildAddComponentView
```
Expected: `ok  github.com/dobbo-ca/lynceus/web` — all four subtests green.

- [ ] **Step 5: Commit.**
```
git add web/onboarding_vm.go web/onboarding_vm_test.go
git commit -m "feat(web): add-component wizard view-model with real collector env contract (ly-ae6.12)"
```

---

### Task 2: Add-component wizard modal templ fragment

The HTMX-swappable modal overlay. Loaded into a `#modal-root` container by the "+ ADD" button; provider chips re-fetch the fragment to re-render YAML/note; ✕ closes by swapping in an empty fragment. Built with tokens only.

**Files**
- Create: `web/onboarding.templ`
- Regenerate: `web/onboarding_templ.go` (via `make templ`)
- Create (test): `web/onboarding_render_test.go`

**Interfaces**

Consumes: `AddComponentView` (Task 1).

Produces:
```go
templ AddComponentModal(v AddComponentView)   // full overlay, root id="add-modal"
```
HTMX contract emitted by the fragment:
- Chips: `hx-get="/partial/add?kind={Kind}&provider={ID}"` · `hx-target="#add-modal"` · `hx-swap="outerHTML"`.
- ✕ close: `hx-get="/partial/modal/close"` · `hx-target="#modal-root"` · `hx-swap="innerHTML"`.
- Copy button: `data-copy="add-yaml"` (wired by `onboarding.js`, Task 5); YAML block carries `id="add-yaml"`.
- Guide deep-link (AWS/Azure only): `hx-get="/partial/provider-setup?provider={GuideProvider}"` is NOT used here (it lives on a different page); instead it is a plain `<a href={ "/admin/provider-setup?provider=" + v.GuideProvider }>` full navigation.

- [ ] **Step 1: Write the failing render test.** Create `web/onboarding_render_test.go`:
```go
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
		`hx-target="#modal-root"`, // close swaps the #modal-root container (that element lives on the databases page — Task 6 — NOT in this fragment)
		`var(--surface)`,        // tokens, not legacy classes
		`ADD DATABASE CLUSTER`,  // title
		`SELF-HOSTED`, `AWS`, `AZURE`, // provider chips
		`id="add-yaml"`,         // copyable YAML block
		`data-copy="add-yaml"`,  // copy button hook
		`LYNCEUS_COLLECTOR_TOKEN`,
		`/partial/add?kind=database&amp;provider=aws`, // chip re-fetch (& is escaped in attr)
		`/partial/modal/close`,  // close route
		`/admin/provider-setup?provider=aws`, // guide deep-link (AWS)
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
```

- [ ] **Step 2: Run it — expect FAIL.**
```
make templ && go test ./web/ -run TestAddComponentModal
```
Expected: compile error `undefined: AddComponentModal` (templ hasn't been written yet, so `make templ` produces no symbol) — red FAIL. (If `make templ` errors on the not-yet-created file, that is also an acceptable red state; proceed to Step 3.)

- [ ] **Step 3: Implement `web/onboarding.templ`.**
```go
package web

import "fmt"

// AddComponentModal is the + ADD wizard, an HTMX-swappable overlay.
// Rendered into #modal-root by the "+ ADD" button; provider chips re-fetch
// this same fragment (outerHTML swap of #add-modal); ✕ clears #modal-root.
// Token-styled only (no legacy.css classes).
templ AddComponentModal(v AddComponentView) {
	<div id="add-modal" style="position: fixed; inset: 0; background: rgba(0,0,0,.55); z-index: 60; display: flex; align-items: center; justify-content: center; padding: 24px;">
		<div style="width: 660px; max-width: 100%; max-height: 84vh; overflow-y: auto; background: var(--surface); border: 1px solid var(--line); border-radius: var(--radius); box-shadow: 0 16px 48px rgba(0,0,0,.5); display: flex; flex-direction: column;">
			<div style="padding: 12px 16px; border-bottom: 1px solid var(--line); display: flex; align-items: center; gap: 12px; font-family: var(--font-mono);">
				<span style="font-size: 12.5px; font-weight: 600; letter-spacing: .06em;">{ v.Title }</span>
				<span style="flex: 1;"></span>
				<span
					hx-get="/partial/modal/close"
					hx-target="#modal-root"
					hx-swap="innerHTML"
					style="width: 24px; height: 24px; border: 1px solid var(--line); border-radius: var(--radius); display: flex; align-items: center; justify-content: center; color: var(--dim); font-size: 12px; cursor: pointer; user-select: none;"
				>✕</span>
			</div>
			<div style="padding: 14px 16px; display: flex; flex-direction: column; gap: 12px;">
				<div style="display: flex; gap: 8px; align-items: center;">
					<span style="font-family: var(--font-mono); font-size: 9.5px; letter-spacing: .1em; color: var(--faint); width: 76px;">PROVIDER</span>
					for _, c := range v.Chips {
						<span
							hx-get={ fmt.Sprintf("/partial/add?kind=%s&provider=%s", v.Kind, c.ID) }
							hx-target="#add-modal"
							hx-swap="outerHTML"
							style={ chipStyle(c.Selected) }
						>{ c.Label }</span>
					}
				</div>
				<div style="font-family: var(--font-mono); font-size: 10.5px; color: var(--mut); line-height: 1.7;">1 · CREATE A COLLECTOR TOKEN — SD MENU ▸ COLLECTORS · SCOPE: INGEST. STORE IT AS SECRET lynceus-token.</div>
				<div style="display: flex; flex-direction: column; gap: 6px;">
					<div style="display: flex; align-items: center; gap: 12px;">
						<span style="font-family: var(--font-mono); font-size: 10.5px; color: var(--mut);">2 · DEPLOY THE COLLECTOR INTO YOUR KUBERNETES CLUSTER:</span>
						<span style="flex: 1;"></span>
						<span data-copy="add-yaml" style="border: 1px solid var(--line); color: var(--mut); padding: 2px 8px; border-radius: var(--radius); cursor: pointer; user-select: none; font-family: var(--font-mono); font-size: 10px;">COPY</span>
					</div>
					<div id="add-yaml" style="background: var(--raised); border: 1px solid var(--line2); border-radius: var(--radius); padding: 10px 12px; font-family: var(--font-mono); font-size: 10.5px; line-height: 1.6; color: var(--text); white-space: pre; overflow-x: auto;">{ v.YAML }</div>
				</div>
				<div style="font-family: var(--font-mono); font-size: 10.5px; color: var(--mut); line-height: 1.7;">{ v.ProviderNote }</div>
				if v.ShowGuideLink {
					<a href={ templ.SafeURL("/admin/provider-setup?provider=" + v.GuideProvider) } style="font-family: var(--font-mono); font-size: 10.5px; letter-spacing: .04em;">FULL SETUP GUIDE — IAM / IDENTITY, QUERYSET MAPPING, VERIFICATION →</a>
				}
				<div style="font-family: var(--font-mono); font-size: 10.5px; color: var(--mut); line-height: 1.7;">{ fmt.Sprintf("4 · THE %s APPEARS IN THIS LIST ONCE THE COLLECTOR FIRST REPORTS (≈30S). HEALTH, VERSIONS AND PROVIDER METRICS FILL IN AUTOMATICALLY.", v.Noun) }</div>
			</div>
		</div>
	</div>
}

// chipStyle returns the inline style for a provider chip, selected or not.
func chipStyle(selected bool) string {
	if selected {
		return "padding: 4px 10px; border: 1px solid var(--acc); color: var(--acc2); background: var(--accbg); font-family: var(--font-mono); font-size: 10px; cursor: pointer; border-radius: var(--radius); user-select: none; letter-spacing: .06em;"
	}
	return "padding: 4px 10px; border: 1px solid var(--line); color: var(--dim); background: transparent; font-family: var(--font-mono); font-size: 10px; cursor: pointer; border-radius: var(--radius); user-select: none; letter-spacing: .06em;"
}
```
Note: this fragment does NOT contain an element with `id="modal-root"` — the `#modal-root` container itself is added on the databases page in Task 6. What this fragment DOES emit is the ✕ close button's `hx-target="#modal-root"` (an innerHTML swap that empties that container). The Task 2 render test therefore asserts the exact literal `hx-target="#modal-root"`, NOT `id="modal-root"` — asserting `id="modal-root"` here would fail because the substring prefix is `="#`, not `id="`. Do not add an `id="modal-root"` element to this fragment; the container belongs to the host page so the modal can clear itself.

- [ ] **Step 4: Regenerate + run — expect PASS.**
```
make templ && go test ./web/ -run TestAddComponentModal
```
Expected: `ok` — both subtests green.

- [ ] **Step 5: Commit.**
```
git add web/onboarding.templ web/onboarding_templ.go web/onboarding_render_test.go
git commit -m "feat(web): + ADD wizard modal templ fragment, token-styled HTMX (ly-ae6.12)"
```

---

### Task 3: Provider Setup view-model + guide data

Pure Go. Encodes the AWS three-path + Terraform guide, the Azure guide, and the PlanetScale guide, plus the block-button chooser and unselected state. Guide content is transcribed verbatim from the verified prototype (`docs/design/Lynceus.dc.html` `provDocs`, lines 2491-2521).

**Files**
- Create: `web/provider_setup_vm.go`
- Create (test): `web/provider_setup_vm_test.go`

**Interfaces**

Produces:
```go
package web

// ProviderID identifies a provider setup guide.
type ProviderID string

const (
	ProviderSetupAWS         ProviderID = "aws"
	ProviderSetupAzure       ProviderID = "azure"
	ProviderSetupPlanetScale ProviderID = "planetscale"
)

// ProviderBlock is one big block-button in the provider chooser.
type ProviderBlock struct {
	ID       ProviderID
	Label    string // "AWS" / "Azure" / "PlanetScale"
	Mark     string // text-mark chip: "AWS" / "AZ" / "PS"
	Sub      string // "CloudWatch" / "Azure Monitor" / "Prometheus"
	Selected bool
}

// GuideStep is one numbered step in a provider guide.
type GuideStep struct {
	N     string // "1"
	Title string
	Body  string
	Code  string // "" when the step has no code block
}

// ProviderGuide is a full provider setup guide.
type ProviderGuide struct {
	Intro string
	Steps []GuideStep
}

// ProviderSetupView is the full Provider Setup page view-model.
type ProviderSetupView struct {
	Blocks   []ProviderBlock
	Selected ProviderID     // "" => unselected (show the prompt)
	Guide    *ProviderGuide // nil when Selected == ""
}

// BuildProviderSetupView returns the page view-model for the given selection.
// An empty or unknown selection yields the unselected state (Guide == nil).
func BuildProviderSetupView(selected ProviderID) ProviderSetupView
```

Consumes: nothing (pure).

- [ ] **Step 1: Write the failing test.** Create `web/provider_setup_vm_test.go`:
```go
package web

import (
	"strings"
	"testing"
)

func TestBuildProviderSetupView_Unselected(t *testing.T) {
	v := BuildProviderSetupView("")
	if v.Selected != "" || v.Guide != nil {
		t.Errorf("unselected: Selected=%q Guide=%v, want empty/nil", v.Selected, v.Guide)
	}
	if len(v.Blocks) != 3 {
		t.Fatalf("blocks = %d, want 3", len(v.Blocks))
	}
	wantMarks := map[ProviderID]string{ProviderSetupAWS: "AWS", ProviderSetupAzure: "AZ", ProviderSetupPlanetScale: "PS"}
	for _, b := range v.Blocks {
		if wantMarks[b.ID] != b.Mark {
			t.Errorf("block %q mark = %q, want %q", b.ID, b.Mark, wantMarks[b.ID])
		}
		if b.Selected {
			t.Errorf("no block should be selected in the unselected state (%q)", b.ID)
		}
	}
}

func TestBuildProviderSetupView_AWS_threePathsAndTerraform(t *testing.T) {
	v := BuildProviderSetupView(ProviderSetupAWS)
	if v.Guide == nil {
		t.Fatal("aws guide is nil")
	}
	if len(v.Guide.Steps) != 6 {
		t.Fatalf("aws steps = %d, want 6", len(v.Guide.Steps))
	}
	// selected block flag
	var awsSel bool
	for _, b := range v.Blocks {
		if b.ID == ProviderSetupAWS {
			awsSel = b.Selected
		}
	}
	if !awsSel {
		t.Error("AWS block must be marked Selected")
	}
	titles := []string{
		"PATH 1 — DIRECT AGENT CONNECTION",
		"PATH 2 — RESOURCE API ACCESS (IAM, RDS-ONLY)",
		"PATH 3 — FIREHOSE INGESTION (CONTROLLED INGRESS)",
		"QUERYSET MAPPING",
		"TERRAFORM",
		"VERIFY",
	}
	for i, want := range titles {
		if v.Guide.Steps[i].Title != want {
			t.Errorf("step[%d].Title = %q, want %q", i, v.Guide.Steps[i].Title, want)
		}
	}
	joined := v.Guide.Intro
	for _, s := range v.Guide.Steps {
		joined += "\n" + s.Body + "\n" + s.Code
	}
	for _, want := range []string{
		"LYNCEUS_DB_ROLE",                       // path 1 env-placeholder role
		"pg_monitor",                            // required tier
		"pg_signal_backend",                     // maintenance tier
		`"aws:ResourceTag/lynceus": "true"`,     // path 2 RDS scoping
		`"cloudwatch:namespace": "AWS/RDS"`,     // path 2 namespace scope
		"Firehose",                              // path 3 controlled ingress
		"X-Lynceus-Tenant",                      // path 3 tenant header
		"aws_kinesis_firehose_delivery_stream",  // terraform
		"aws_cloudwatch_metric_stream",          // terraform metric stream
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("aws guide missing %q", want)
		}
	}
	// VERIFY step has no code block
	if v.Guide.Steps[5].Code != "" {
		t.Error("VERIFY step must have no code block")
	}
}

func TestBuildProviderSetupView_AzureAndPlanetScale(t *testing.T) {
	az := BuildProviderSetupView(ProviderSetupAzure)
	if az.Guide == nil || len(az.Guide.Steps) != 4 {
		t.Fatalf("azure steps = %v", az.Guide)
	}
	if !strings.Contains(az.Guide.Steps[0].Code, "Monitoring Reader") {
		t.Error("azure step 1 must grant Monitoring Reader")
	}
	ps := BuildProviderSetupView(ProviderSetupPlanetScale)
	if ps.Guide == nil || len(ps.Guide.Steps) != 4 {
		t.Fatalf("planetscale steps = %v", ps.Guide)
	}
	if !strings.Contains(ps.Guide.Steps[1].Code, "http_sd") {
		t.Error("planetscale step 2 must use http_sd service discovery")
	}
}

func TestBuildProviderSetupView_UnknownIsUnselected(t *testing.T) {
	v := BuildProviderSetupView(ProviderID("gcp"))
	if v.Selected != "" || v.Guide != nil {
		t.Error("unknown provider must render as unselected")
	}
}
```

- [ ] **Step 2: Run it — expect FAIL.**
```
go test ./web/ -run TestBuildProviderSetupView
```
Expected: compile error `undefined: BuildProviderSetupView` — red FAIL.

- [ ] **Step 3: Implement `web/provider_setup_vm.go`.** Transcribe the guide content verbatim from the prototype. Use raw string literals for multi-line code blocks (none of the code contains a backtick).
```go
package web

// ProviderID identifies a provider setup guide.
type ProviderID string

const (
	ProviderSetupAWS         ProviderID = "aws"
	ProviderSetupAzure       ProviderID = "azure"
	ProviderSetupPlanetScale ProviderID = "planetscale"
)

// ProviderBlock is one big block-button in the provider chooser.
type ProviderBlock struct {
	ID       ProviderID
	Label    string
	Mark     string
	Sub      string
	Selected bool
}

// GuideStep is one numbered step in a provider guide.
type GuideStep struct {
	N     string
	Title string
	Body  string
	Code  string
}

// ProviderGuide is a full provider setup guide.
type ProviderGuide struct {
	Intro string
	Steps []GuideStep
}

// ProviderSetupView is the full Provider Setup page view-model.
type ProviderSetupView struct {
	Blocks   []ProviderBlock
	Selected ProviderID
	Guide    *ProviderGuide
}

// BuildProviderSetupView returns the page view-model for the given selection.
func BuildProviderSetupView(selected ProviderID) ProviderSetupView {
	guide, ok := providerGuides[selected]
	if !ok {
		selected = ""
	}
	v := ProviderSetupView{
		Selected: selected,
		Blocks: []ProviderBlock{
			{ID: ProviderSetupAWS, Label: "AWS", Mark: "AWS", Sub: "CloudWatch", Selected: selected == ProviderSetupAWS},
			{ID: ProviderSetupAzure, Label: "Azure", Mark: "AZ", Sub: "Azure Monitor", Selected: selected == ProviderSetupAzure},
			{ID: ProviderSetupPlanetScale, Label: "PlanetScale", Mark: "PS", Sub: "Prometheus", Selected: selected == ProviderSetupPlanetScale},
		},
	}
	if selected != "" {
		g := guide
		v.Guide = &g
	}
	return v
}

var providerGuides = map[ProviderID]ProviderGuide{
	ProviderSetupAWS: {
		Intro: "Three data paths work together on AWS. Path 1: the agent (running in your Kubernetes cluster) connects directly to the database / search / cache endpoint and runs queries against it. Path 2: an IAM role lets the Lynceus role call the resource APIs for metadata (instance class, parameter groups, tags) — not logs or metrics. Path 3: CloudWatch ships metrics and logs about the resource back to Lynceus over a push pipeline.",
		Steps: []GuideStep{
			{N: "1", Title: "PATH 1 — DIRECT AGENT CONNECTION",
				Body: "The agent connects to the endpoint like any client and runs queries directly (pg_stat_*, cluster health APIs, INFO). The role name is never hardcoded — it comes from LYNCEUS_DB_ROLE so each environment can set its own. Grants are tiered: baseline is read-only monitoring; each optional tier unlocks more capabilities (visible on the Capabilities screen), up to owner-level.",
				Code: `# role name is an environment placeholder — set per environment
#   env: LYNCEUS_DB_ROLE=lynceus_monitor        (staging)
#   env: LYNCEUS_DB_ROLE=lynceus_monitor_prod   (production)

-- REQUIRED · baseline monitoring (read-only stats)
CREATE ROLE :"LYNCEUS_DB_ROLE" LOGIN PASSWORD :'LYNCEUS_DB_PASSWORD';
GRANT pg_monitor TO :"LYNCEUS_DB_ROLE";

-- OPTIONAL · extensions — enable trusted extensions on PG 13+
--   (pg_stat_statements, auto_explain)
GRANT CREATE ON DATABASE <db> TO :"LYNCEUS_DB_ROLE";

-- OPTIONAL · maintenance — cancel/terminate runaway backends
GRANT pg_signal_backend TO :"LYNCEUS_DB_ROLE";

-- OPTIONAL · owner-level — full control of the target database
--   (apply index advisor DDL, schema changes); grant deliberately
ALTER DATABASE <db> OWNER TO :"LYNCEUS_DB_ROLE";

# collector env — same deployment as the + ADD wizard.
# The real collector reads LYNCEUS_PG_DSN (see cmd/collector/main.go) — the
# wizard emits the identical var, so the two surfaces agree.
env:
  - { name: LYNCEUS_DB_ROLE,     value: "<role>" }      # per environment
  - { name: LYNCEUS_DB_PASSWORD, valueFrom: { secretKeyRef: { name: lynceus-db, key: password } } }
  - { name: LYNCEUS_PG_DSN,      value: "postgres://$(LYNCEUS_DB_ROLE)@<primary-host>:5432/postgres" }
  # SHIP_VIA / FIREHOSE_STREAM below are illustrative pipeline hints only — NOT
  # read by the collector; controlled ingress is configured Terraform-side (step 5).
  - { name: SHIP_VIA,            value: "firehose" }          # illustrative (step 3 / step 5)
  - { name: FIREHOSE_STREAM,     value: "lynceus-ingest" }    # illustrative`},
			{N: "2", Title: "PATH 2 — RESOURCE API ACCESS (IAM, RDS-ONLY)",
				Body: "A read-only role for control-plane metadata, limited strictly to RDS: rds:* actions are scoped to RDS ARNs and further gated on the lynceus=true resource tag; the CloudWatch read is restricted to the AWS/RDS namespace. Attach it to the collector service account via IRSA.",
				Code: `{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "RdsMetadataOnly",
      "Effect": "Allow",
      "Action": [
        "rds:DescribeDBInstances",
        "rds:DescribeDBClusters",
        "rds:DescribeDBParameters",
        "rds:ListTagsForResource"
      ],
      "Resource": [
        "arn:aws:rds:*:<acct>:db:*",
        "arn:aws:rds:*:<acct>:cluster:*",
        "arn:aws:rds:*:<acct>:pg:*"
      ],
      "Condition": { "StringEquals": { "aws:ResourceTag/lynceus": "true" } }
    },
    {
      "Sid": "CloudWatchRdsNamespaceOnly",
      "Effect": "Allow",
      "Action": ["cloudwatch:GetMetricData", "cloudwatch:ListMetrics"],
      "Resource": "*",
      "Condition": { "StringEquals": { "cloudwatch:namespace": "AWS/RDS" } }
    }
  ]
}`},
			{N: "3", Title: "PATH 3 — FIREHOSE INGESTION (CONTROLLED INGRESS)",
				Body: "All AWS-side data enters Lynceus through a Firehose delivery stream that you own — the agent writes to it, CloudWatch Metric Streams and log subscription filters feed it, and a single HTTP delivery leaves your account. That one stream is your ingress control point: buffer sizing, an optional Lambda transform to drop or redact records before they leave, include-filters per namespace, and IAM control over exactly which producers may write.",
				Code: `producers:   agent (k8s) ............ firehose:PutRecordBatch
             CW Metric Stream ....... include_filter: AWS/RDS
             CW Logs sub filter ..... /aws/rds/instance/*/postgresql
                       |
                       v
 stream:     Firehose "lynceus-ingest"        <- YOUR control point
 controls:   buffering 1-5 MB / 60 s
             Lambda transform (drop / redact before egress)
             IAM: only the collector role may PutRecord
                       |
                       v
 delivery:   https://ingest.<region>.lynceus.io/v1/ingest
 auth:       access_key = <ingest token>      # SD MENU / COLLECTORS
 tenant:     X-Lynceus-Tenant: <org-id>       # common attribute`},
			{N: "4", Title: "QUERYSET MAPPING",
				Body: "Whether pulled (path 2) or streamed (path 3), metrics are mapped onto fixed Lynceus series ids (ship_to) so they land in the right node and database views.",
				Code: `querysets:
  - id: rds-core
    provider: aws
    namespace: AWS/RDS
    discover: { by: tag, key: lynceus, value: "true" }
    period: 60s
    metrics:
      - { name: CPUUtilization,      ship_to: node.cpu }
      - { name: FreeableMemory,      ship_to: node.mem_free }
      - { name: FreeStorageSpace,    ship_to: node.disk_free }
      - { name: ReadIOPS,            ship_to: node.io.read }
      - { name: WriteIOPS,           ship_to: node.io.write }
      - { name: DatabaseConnections, ship_to: pg.connections }
      - { name: ReplicaLag,          ship_to: pg.replica_lag }`},
			{N: "5", Title: "TERRAFORM",
				Body: "The whole AWS side as Terraform — scoped IAM policy, metric stream limited to AWS/RDS, and the Firehose delivery with endpoint, auth key and tenant header.",
				Code: `resource "aws_iam_policy" "lynceus_rds_read" {
  name   = "LynceusRdsRead"
  policy = file("lynceus-rds-policy.json")   # step 2
}

resource "aws_iam_role" "lynceus_collector" {
  name               = "LynceusCollector"
  assume_role_policy = data.aws_iam_policy_document.eks_oidc.json
}

resource "aws_iam_role_policy_attachment" "lynceus" {
  role       = aws_iam_role.lynceus_collector.name
  policy_arn = aws_iam_policy.lynceus_rds_read.arn
}

resource "aws_cloudwatch_metric_stream" "lynceus" {
  name          = "lynceus-rds"
  role_arn      = aws_iam_role.metric_stream.arn
  firehose_arn  = aws_kinesis_firehose_delivery_stream.lynceus.arn
  output_format = "opentelemetry1.0"

  include_filter { namespace = "AWS/RDS" }   # RDS only
}

resource "aws_kinesis_firehose_delivery_stream" "lynceus" {
  name        = "lynceus-ingest"
  destination = "http_endpoint"

  http_endpoint_configuration {
    name               = "lynceus"
    url                = "https://ingest.${var.region}.lynceus.io/v1/ingest"
    access_key         = var.lynceus_ingest_token   # SD MENU / COLLECTORS
    buffering_size     = 4    # MB — ingress control
    buffering_interval = 60   # seconds

    request_configuration {
      common_attributes {
        name  = "X-Lynceus-Tenant"
        value = var.lynceus_tenant_id
      }
    }
  }
}

# only the collector role may write into the stream
resource "aws_iam_role_policy" "collector_put" {
  role = aws_iam_role.lynceus_collector.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["firehose:PutRecord", "firehose:PutRecordBatch"]
      Resource = aws_kinesis_firehose_delivery_stream.lynceus.arn
    }]
  })
}`},
			{N: "6", Title: "VERIFY",
				Body: `kubectl logs deploy/lynceus-collector for paths 1–2 ("queryset rds-core: N series shipped"); for path 3, the Firehose destination error rate should be 0 and the instance appears under Database ▸ Nodes within ~60s with source "CloudWatch". Multi-AZ standbys remain a blind spot until promoted.`,
				Code: ""},
		},
	},
	ProviderSetupAzure: {
		Intro: "Azure Flexible Server exposes metrics through Azure Monitor. The collector authenticates with a managed identity (or app registration) and normalizes metrics through the same queryset mechanism.",
		Steps: []GuideStep{
			{N: "1", Title: "GRANT MONITORING READER",
				Body: "Assign the collector identity Monitoring Reader on the resource group holding your Flexible Servers.",
				Code: `az role assignment create \
  --assignee <collector-client-id> \
  --role "Monitoring Reader" \
  --scope /subscriptions/<sub>/resourceGroups/<rg>`},
			{N: "2", Title: "COLLECTOR CREDENTIALS",
				Body: "Provide tenant, subscription and client ids as env vars, or use workload identity on AKS.",
				Code: `env:
  - { name: AZURE_TENANT_ID,       value: "<tenant>" }
  - { name: AZURE_SUBSCRIPTION_ID, value: "<sub>" }
  - { name: AZURE_CLIENT_ID,       value: "<client>" }`},
			{N: "3", Title: "CONFIGURE THE QUERYSET",
				Body: "Azure metric names differ from CloudWatch — the queryset normalizes them onto the same Lynceus series ids.",
				Code: `querysets:
  - id: azure-flex-core
    provider: azure
    resource_type: Microsoft.DBforPostgreSQL/flexibleServers
    period: 60s
    metrics:
      - { name: cpu_percent,        ship_to: node.cpu }
      - { name: memory_percent,     ship_to: node.mem }
      - { name: storage_percent,    ship_to: node.disk }
      - { name: iops,               ship_to: node.io.total }
      - { name: active_connections, ship_to: pg.connections }
      - { name: physical_replication_delay_in_seconds, ship_to: pg.replica_lag }`},
			{N: "4", Title: "VERIFY",
				Body: "A zone-redundant HA standby has no endpoint; its metrics arrive through the server resource, and Lynceus renders it as a standby with provider-only visibility.",
				Code: ""},
		},
	},
	ProviderSetupPlanetScale: {
		Intro: "PlanetScale ships data differently: an org-level Prometheus endpoint with API-driven service discovery. The collector scrapes it directly — no cloud IAM involved and no standby blind spot.",
		Steps: []GuideStep{
			{N: "1", Title: "CREATE A SERVICE TOKEN",
				Body: "In PlanetScale organization settings, create a service token and grant it read_metrics_endpoints. Store the id and token as the secret lynceus-pscale.",
				Code: ""},
			{N: "2", Title: "POINT THE COLLECTOR AT THE ORG",
				Body: "HTTP service discovery finds every Postgres branch in the org and refreshes the list every 10 minutes.",
				Code: `scrape:
  - job: planetscale-postgres
    http_sd:
      url: https://api.planetscale.com/v1/organizations/<org>/metrics
      auth: "token <TOKEN_ID>:<TOKEN>"
      refresh: 10m
    interval: 30s`},
			{N: "3", Title: "CONFIGURE THE QUERYSET",
				Body: "Prometheus series are relabeled into Lynceus series; primaries and replicas report individually.",
				Code: `querysets:
  - id: pscale-core
    provider: planetscale
    metrics:
      - { match: pscale_cpu_utilization,           ship_to: node.cpu }
      - { match: pscale_memory_utilization,        ship_to: node.mem }
      - { match: pscale_connections,               ship_to: pg.connections }
      - { match: pscale_replication_lag_seconds,   ship_to: pg.replica_lag }`},
			{N: "4", Title: "VERIFY",
				Body: "Each branch appears as its own cluster with per-instance nodes. There is no host shell — node metrics come exclusively from the metrics endpoint.",
				Code: ""},
		},
	},
}
```

- [ ] **Step 4: Run it — expect PASS.**
```
go test ./web/ -run TestBuildProviderSetupView
```
Expected: `ok` — all subtests green.

- [ ] **Step 5: Commit.**
```
git add web/provider_setup_vm.go web/provider_setup_vm_test.go
git commit -m "feat(web): provider setup guide data (AWS 3-path + Terraform, Azure, PlanetScale) (ly-ae6.12)"
```

---

### Task 4: Provider Setup page + body templ

Full page (`@Layout` wrapper) + swappable `#provider-setup-body` fragment. Block buttons re-fetch the body with `?provider=`. Token-styled.

**Files**
- Create: `web/provider_setup.templ`
- Regenerate: `web/provider_setup_templ.go` (via `make templ`)
- Create (test): `web/provider_setup_render_test.go`

**Interfaces**

Consumes: `ProviderSetupView` (Task 3).

Produces:
```go
templ ProviderSetupPage(v ProviderSetupView) // full page (@Layout)
templ ProviderSetupBody(v ProviderSetupView) // #provider-setup-body fragment
```
HTMX contract: each block button emits `hx-get="/partial/provider-setup?provider={ID}"` · `hx-target="#provider-setup-body"` · `hx-swap="outerHTML"`. Each guide step's copy button emits `data-copy="guide-code-{N}"`, and its code block carries `id="guide-code-{N}"`.

- [ ] **Step 1: Write the failing render test.** Create `web/provider_setup_render_test.go`:
```go
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
	if strings.Contains(html, "SELECT A PROVIDER") == false {
		t.Error("prompt missing")
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

func TestProviderSetupPage_wrapsLayout(t *testing.T) {
	var sb strings.Builder
	if err := ProviderSetupPage(BuildProviderSetupView("")).Render(context.Background(), &sb); err != nil {
		t.Fatalf("render: %v", err)
	}
	html := sb.String()
	if !strings.Contains(html, "<!DOCTYPE html>") || !strings.Contains(html, "Provider Setup") {
		t.Error("page must wrap Layout and carry the Provider Setup heading")
	}
}
```

- [ ] **Step 2: Run it — expect FAIL.**
```
make templ && go test ./web/ -run TestProviderSetup
```
Expected: compile error `undefined: ProviderSetupBody` / `ProviderSetupPage` — red FAIL.

- [ ] **Step 3: Implement `web/provider_setup.templ`.**
```go
package web

// ProviderSetupPage is the full admin page (user menu ▸ Admin ▸ Provider Setup).
templ ProviderSetupPage(v ProviderSetupView) {
	@Layout("Lynceus — provider setup", "how metrics ship into Lynceus, per provider") {
		<div id="provider-setup-root">
			@ProviderSetupBody(v)
		</div>
	}
}

// ProviderSetupBody is the swappable fragment: block-button chooser + the
// selection-gated guide. Block buttons re-fetch this fragment with ?provider=.
templ ProviderSetupBody(v ProviderSetupView) {
	<div id="provider-setup-body" style="padding: 18px 22px 32px; display: flex; flex-direction: column; gap: 14px; max-width: 1000px;">
		<div style="display: flex; align-items: center; gap: 12px;">
			<span style="font-size: 17px; font-weight: 600;">Provider Setup</span>
			<span style="font-family: var(--font-mono); font-size: 10.5px; color: var(--faint); letter-spacing: .08em;">HOW METRICS SHIP INTO LYNCEUS, PER PROVIDER</span>
		</div>
		<div style="display: grid; grid-template-columns: repeat(3, 1fr); gap: 14px;">
			for _, b := range v.Blocks {
				<div
					hx-get={ "/partial/provider-setup?provider=" + string(b.ID) }
					hx-target="#provider-setup-body"
					hx-swap="outerHTML"
					style={ blockStyle(b.Selected) }
				>
					<span style={ blockMarkStyle(b.Selected) }>{ b.Mark }</span>
					<span style={ blockLabelStyle(b.Selected) }>{ b.Label }</span>
					<span style="font-family: var(--font-mono); font-size: 9.5px; color: var(--faint); letter-spacing: .06em;">VIA { b.Sub }</span>
				</div>
			}
		</div>
		if v.Guide == nil {
			<div style="font-family: var(--font-mono); font-size: 10.5px; color: var(--faint); text-align: center; letter-spacing: .06em; padding: 8px 0;">SELECT A PROVIDER TO SEE ITS SETUP GUIDE</div>
		} else {
			<div style="border: 1px solid var(--line); border-radius: var(--radius); background: var(--surface); padding: 12px 14px; font-size: 12.5px; color: var(--mut); line-height: 1.7;">{ v.Guide.Intro }</div>
			for _, st := range v.Guide.Steps {
				<div style="border: 1px solid var(--line); border-radius: var(--radius); background: var(--surface);">
					<div style="padding: 8px 12px; border-bottom: 1px solid var(--line2); display: flex; align-items: center; gap: 12px; font-family: var(--font-mono); font-size: 10px; letter-spacing: .1em; color: var(--dim);">
						<span style="color: var(--acc2);">{ st.N }</span>
						<span>{ st.Title }</span>
						<span style="flex: 1;"></span>
						if st.Code != "" {
							<span data-copy={ "guide-code-" + st.N } style="border: 1px solid var(--line); color: var(--mut); padding: 2px 8px; border-radius: var(--radius); cursor: pointer; user-select: none; letter-spacing: 0;">COPY</span>
						}
					</div>
					<div style="padding: 10px 12px; font-size: 12.5px; color: var(--mut); line-height: 1.7;">{ st.Body }</div>
					if st.Code != "" {
						<div id={ "guide-code-" + st.N } style="margin: 0 12px 12px; background: var(--raised); border: 1px solid var(--line2); border-radius: var(--radius); padding: 10px 12px; font-family: var(--font-mono); font-size: 10.5px; line-height: 1.6; color: var(--text); white-space: pre; overflow-x: auto;">{ st.Code }</div>
					}
				</div>
			}
		}
	</div>
}

// blockStyle / blockMarkStyle / blockLabelStyle return inline styles for a
// provider block-button, selected or not.
func blockStyle(selected bool) string {
	base := "display: flex; flex-direction: column; align-items: center; gap: 10px; border-radius: var(--radius); padding: 22px 16px; cursor: pointer; user-select: none; "
	if selected {
		return base + "border: 1px solid var(--acc); background: var(--accbg);"
	}
	return base + "border: 1px solid var(--line); background: var(--surface);"
}

func blockMarkStyle(selected bool) string {
	c := "var(--dim)"
	if selected {
		c = "var(--acc2)"
	}
	return "width: 40px; height: 40px; border: 1.5px solid " + c + "; color: " + c + "; font-family: var(--font-mono); font-size: 12px; font-weight: 600; display: flex; align-items: center; justify-content: center; border-radius: var(--radius); letter-spacing: .04em;"
}

func blockLabelStyle(selected bool) string {
	c := "var(--mut)"
	if selected {
		c = "var(--acc2)"
	}
	return "font-family: var(--font-mono); font-size: 13px; font-weight: 600; color: " + c + ";"
}
```

- [ ] **Step 4: Regenerate + run — expect PASS.**
```
make templ && go test ./web/ -run TestProviderSetup
```
Expected: `ok` — all three tests green.

- [ ] **Step 5: Commit.**
```
git add web/provider_setup.templ web/provider_setup_templ.go web/provider_setup_render_test.go
git commit -m "feat(web): Provider Setup page + selection-gated guide fragment (ly-ae6.12)"
```

---

### Task 5: Copy-to-clipboard JS + layout wiring

One tiny self-hosted script wires every `[data-copy]` button (in the wizard and the guides) via event delegation, so it works on HTMX-swapped content. Referenced once from `web/layout.templ`.

**Files**
- Create: `web/static/js/onboarding.js`
- Modify: `web/layout.templ` (add one `<script>` line) + regenerate `web/layout_templ.go`
- Modify (test): `web/layout_test.go` (extend the self-hosted asset assertion)

**Interfaces**

Produces: a DOM behaviour — clicking an element with `data-copy="<id>"` copies the `textContent` of `#<id>` to the clipboard and flashes the label to `COPIED`. No Go interface.

- [ ] **Step 1: Extend the failing layout test.** In `web/layout_test.go`, add `onboarding.js` to the `TestLayout_SelfHostedAssets` want-list:
```go
	for _, want := range []string{
		`href="/static/css/tokens.css"`,
		`href="/static/css/legacy.css"`,
		`src="/static/js/htmx.min.js"`,
		`src="/static/js/theme.js"`,
		`src="/static/js/onboarding.js"`,
	} {
```

- [ ] **Step 2: Run it — expect FAIL.**
```
go test ./web/ -run TestLayout_SelfHostedAssets
```
Expected: `layout missing self-hosted asset ref "src=\"/static/js/onboarding.js\""` — red FAIL.

- [ ] **Step 3: Create `web/static/js/onboarding.js`.**
```js
// Copy-to-clipboard for onboarding wizard + provider guides.
// Event-delegated so it also covers HTMX-swapped content. No external hosts.
(function () {
  document.addEventListener('click', function (e) {
    var btn = e.target.closest('[data-copy]');
    if (!btn) return;
    var src = document.getElementById(btn.getAttribute('data-copy'));
    if (!src) return;
    var text = src.textContent;
    var done = function () {
      var prev = btn.textContent;
      btn.textContent = 'COPIED';
      setTimeout(function () { btn.textContent = prev; }, 1200);
    };
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(text).then(done, function () {});
    } else {
      var ta = document.createElement('textarea');
      ta.value = text;
      document.body.appendChild(ta);
      ta.select();
      try { document.execCommand('copy'); done(); } catch (err) {}
      document.body.removeChild(ta);
    }
  });
})();
```

- [ ] **Step 4: Wire it in `web/layout.templ`.** Add one line after the existing theme.js script:
```go
		<script src="/static/js/htmx.min.js" defer></script>
		<script src="/static/js/theme.js" defer></script>
		<script src="/static/js/onboarding.js" defer></script>
```

- [ ] **Step 5: Regenerate + run — expect PASS.** Confirm the existing no-external-hosts test still passes (the new script is same-origin `/static/`).
```
make templ && go test ./web/ -run TestLayout
```
Expected: `ok` — `TestLayout_SelfHostedAssets` and `TestLayout_NoExternalHosts` both green.

- [ ] **Step 6: Commit.**
```
git add web/static/js/onboarding.js web/layout.templ web/layout_templ.go web/layout_test.go
git commit -m "feat(web): self-hosted copy-to-clipboard for onboarding/provider guides (ly-ae6.12)"
```

---

### Task 6: API handlers, routes, + databases "+ ADD CLUSTER" entry point

Register the four HTMX routes, and add the `+ ADD CLUSTER` button plus the `#modal-root` container to the databases list page.

**Files**
- Create: `internal/api/onboarding.go`
- Create: `internal/api/provider_setup.go`
- Modify: `internal/api/server.go` (register routes)
- Modify: `web/databases.templ` (+ ADD CLUSTER button + `#modal-root`) + regenerate `web/databases_templ.go`
- Create (test): `internal/api/onboarding_test.go`
- Create (test): `internal/api/provider_setup_test.go`

**Interfaces**

Produces (handlers, methods on `*Server`):
```go
func (s *Server) handleAddComponent(w http.ResponseWriter, r *http.Request)     // GET /partial/add?kind=&provider=
func (s *Server) handleModalClose(w http.ResponseWriter, r *http.Request)       // GET /partial/modal/close -> empty
func (s *Server) handleProviderSetupPage(w http.ResponseWriter, r *http.Request)    // GET /admin/provider-setup?provider=
func (s *Server) handleProviderSetupPartial(w http.ResponseWriter, r *http.Request) // GET /partial/provider-setup?provider=
```
Consumes: `web.BuildAddComponentView`, `web.AddComponentModal`, `web.BuildProviderSetupView`, `web.ProviderSetupPage`, `web.ProviderSetupBody` (Tasks 1-4).

- [ ] **Step 1: Write the failing handler tests.** Create `internal/api/onboarding_test.go`:
```go
package api_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/api"
)

func TestAddComponentPartial_AWS(t *testing.T) {
	_, srv := setup(t, api.Config{DevAuth: true})
	resp, err := http.Get(srv.URL + "/partial/add?kind=database&provider=aws")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	html := string(body)
	for _, want := range []string{
		`id="add-modal"`,
		"LYNCEUS_COLLECTOR_TOKEN",
		"ADD DATABASE CLUSTER",
		"/admin/provider-setup?provider=aws",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("add partial missing %q", want)
		}
	}
}

func TestModalClose_returnsEmpty(t *testing.T) {
	_, srv := setup(t, api.Config{DevAuth: true})
	resp, err := http.Get(srv.URL + "/partial/modal/close")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if strings.TrimSpace(string(body)) != "" {
		t.Errorf("close body = %q, want empty", string(body))
	}
}
```
Create `internal/api/provider_setup_test.go`:
```go
package api_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/api"
)

func TestProviderSetupPage_unselected(t *testing.T) {
	_, srv := setup(t, api.Config{DevAuth: true})
	resp, err := http.Get(srv.URL + "/admin/provider-setup")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	html := string(body)
	for _, want := range []string{"<!DOCTYPE html>", "Provider Setup", "SELECT A PROVIDER TO SEE ITS SETUP GUIDE", "/partial/provider-setup?provider=aws"} {
		if !strings.Contains(html, want) {
			t.Errorf("page missing %q", want)
		}
	}
}

func TestProviderSetupPartial_AWS_firehose(t *testing.T) {
	_, srv := setup(t, api.Config{DevAuth: true})
	resp, err := http.Get(srv.URL + "/partial/provider-setup?provider=aws")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	html := string(body)
	for _, want := range []string{"PATH 3 — FIREHOSE INGESTION (CONTROLLED INGRESS)", "aws_kinesis_firehose_delivery_stream", "X-Lynceus-Tenant"} {
		if !strings.Contains(html, want) {
			t.Errorf("aws partial missing %q", want)
		}
	}
	// a fragment must NOT carry the full-page doctype
	if strings.Contains(html, "<!DOCTYPE html>") {
		t.Error("partial must be a bare fragment, not a full page")
	}
}

func TestProviderSetup_requiresDevAuth(t *testing.T) {
	_, srv := setup(t, api.Config{DevAuth: false})
	resp, err := http.Get(srv.URL + "/admin/provider-setup")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}
```

- [ ] **Step 2: Run them — expect FAIL.**
```
go test ./internal/api/ -run 'TestAddComponentPartial|TestModalClose|TestProviderSetup'
```
Expected: 404s (routes unregistered) → assertion failures, a red FAIL. (Tests `t.Skipf` if docker is unavailable — run where docker is present.)

- [ ] **Step 3: Implement `internal/api/onboarding.go`.**
```go
package api

import (
	"net/http"

	"github.com/dobbo-ca/lynceus/web"
)

// handleAddComponent renders the + ADD wizard modal fragment for the given
// vertical (kind) and provider selection. HTMX swaps it into #modal-root, and
// provider chips re-fetch it (outerHTML swap of #add-modal).
func (s *Server) handleAddComponent(w http.ResponseWriter, r *http.Request) {
	kind := web.AddComponentKind(r.URL.Query().Get("kind"))
	provider := web.AddProvider(r.URL.Query().Get("provider"))
	v := web.BuildAddComponentView(kind, provider)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.AddComponentModal(v).Render(r.Context(), w)
}

// handleModalClose returns an empty body so an HTMX innerHTML swap clears the
// #modal-root container (closes any open modal).
func (s *Server) handleModalClose(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
}
```

- [ ] **Step 4: Implement `internal/api/provider_setup.go`.**
```go
package api

import (
	"net/http"

	"github.com/dobbo-ca/lynceus/web"
)

// handleProviderSetupPage renders the full Provider Setup admin page. The
// ?provider= query pre-selects a guide (used by the wizard's deep-link).
func (s *Server) handleProviderSetupPage(w http.ResponseWriter, r *http.Request) {
	v := web.BuildProviderSetupView(web.ProviderID(r.URL.Query().Get("provider")))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.ProviderSetupPage(v).Render(r.Context(), w)
}

// handleProviderSetupPartial renders just the #provider-setup-body fragment,
// re-rendered when a provider block button is chosen.
func (s *Server) handleProviderSetupPartial(w http.ResponseWriter, r *http.Request) {
	v := web.BuildProviderSetupView(web.ProviderID(r.URL.Query().Get("provider")))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.ProviderSetupBody(v).Render(r.Context(), w)
}
```

- [ ] **Step 5: Register routes in `internal/api/server.go`.** Add inside `routes()`, after the checks routes (line ~76):
```go
	s.mux.HandleFunc("GET /partial/add", s.handleAddComponent)
	s.mux.HandleFunc("GET /partial/modal/close", s.handleModalClose)
	s.mux.HandleFunc("GET /admin/provider-setup", s.handleProviderSetupPage)
	s.mux.HandleFunc("GET /partial/provider-setup", s.handleProviderSetupPartial)
```

- [ ] **Step 6: Add the entry point to `web/databases.templ`.** Insert the button + modal container at the top of `DatabasesPage`'s children, before `@DatabasesControls(v)`:
```go
templ DatabasesPage(v DatabasesView) {
	@Layout("Lynceus — databases", "monitored databases") {
		<div style="display: flex; justify-content: flex-end; padding: 0 0 8px;">
			<div
				hx-get="/partial/add?kind=database&provider=self"
				hx-target="#modal-root"
				hx-swap="innerHTML"
				style="font-family: var(--font-mono); font-size: 10.5px; color: var(--acc2); border: 1px solid var(--acc); padding: 4px 9px; border-radius: var(--radius); cursor: pointer; user-select: none;"
			>+ ADD CLUSTER</div>
		</div>
		<div id="modal-root"></div>
		@DatabasesControls(v)
		<div id="databases-body">
			@DatabasesBody(v)
		</div>
	}
}
```

- [ ] **Step 7: Regenerate + run — expect PASS.**
```
make templ && go build ./... && go test ./internal/api/ -run 'TestAddComponentPartial|TestModalClose|TestProviderSetup'
```
Expected: `ok  github.com/dobbo-ca/lynceus/internal/api` — all handler tests green (given docker).

- [ ] **Step 8: Full regression + commit.**
```
go test ./web/... ./internal/api/...
git add internal/api/onboarding.go internal/api/provider_setup.go internal/api/server.go internal/api/onboarding_test.go internal/api/provider_setup_test.go web/databases.templ web/databases_templ.go
git commit -m "feat(api): wire onboarding wizard + Provider Setup routes and + ADD CLUSTER entry point (ly-ae6.12)"
```

---

## Self-Review

### Spec-coverage checklist — every COMPARISON gap → task

`docs/design/COMPARISON.md` §`onboarding-wizard` and §`provider-setup` gaps:

| COMPARISON gap | Covered by |
| --- | --- |
| No `+ ADD` wizard modal (provider chips SELF-HOSTED/AWS/AZURE, collector-token step, copyable k8s Deployment YAML, provider-specific step, first-report end state) | Task 1 (chips + YAML + note + steps in view-model) + Task 2 (modal fragment renders all four steps) |
| No `+ ADD CLUSTER` entry point on the Databases list page (`web/databases.templ`) | Task 6 Step 6 |
| No Provider Setup admin page (route/handler/templ) | Task 3 (data) + Task 4 (page/body templ) + Task 6 (route `GET /admin/provider-setup`) |
| No big-block AWS/Azure/PlanetScale chooser with selection-gated guide render | Task 3 (`Blocks`, `Selected`, `Guide` nil when unselected) + Task 4 (`ProviderSetupBody` prompt vs guide) |
| AWS three-path content: (1) direct agent tiered `LYNCEUS_DB_ROLE` grants, (2) RDS-scoped IAM (`aws:ResourceTag/lynceus`, `AWS/RDS` namespace), (3) Firehose ingress (endpoint + `<ingest token>` auth + `X-Lynceus-Tenant`) | Task 3 AWS steps 1-3 (asserted verbatim in `TestBuildProviderSetupView_AWS_threePathsAndTerraform`) |
| No Terraform variant for any guide | Task 3 AWS step 5 (`aws_kinesis_firehose_delivery_stream` etc.) |
| Azure + PlanetScale guides absent | Task 3 `ProviderSetupAzure` (4 steps) + `ProviderSetupPlanetScale` (4 steps) |
| No user-menu Admin section wiring in `web/layout.templ` (COMPARISON onboarding-wizard + provider-setup) | Out of scope here (host surface owned by ly-ae6.2/ly-ae6.13). De-orphaned, not silently dropped: a reciprocal note was recorded on ly-ae6.13 (2026-07-10) requiring the `{Label:"Provider Setup", Href:"/admin/provider-setup"}` ADMIN entry — see Integration contract. Page ships reachable by direct URL + wizard FULL SETUP GUIDE deep-link until that lands. |
| No user-menu ▸ Admin **shell** to navigate to Provider Setup (provider-setup line 398) | Same deferral as the row above — the avatar-dropdown ADMIN shell is ly-ae6.13's; reciprocal note filed so the entry is added when the shell is built. |
| Collector-token step has no real backend (only static ingest DevToken) | Dependency ly-8b0.8 noted in Integration contract; wizard references k8s secret `lynceus-token` — no backend needed for the UI |
| Ingest does not self-register a servers row on first report | Backend (M5) noted; rendered as guidance copy (step 4) only |
| Wizard YAML must emit the real collector env contract (`LYNCEUS_SERVER_ID`/`LYNCEUS_COLLECTOR_TOKEN`), reconciling away prototype `TARGET_KIND`/`TARGET_ENDPOINT`/`LYNCEUS_TOKEN` | Task 1 `addYAML` + `TestBuildAddComponentView_DatabaseSelf_realEnvContract` (asserts real vars present AND retired placeholders absent) |
| Whole web/ shell ignores the design system — onboarding UI must be built to tokens from scratch | Tasks 2 & 4 use only `var(--…)` inline styles; render tests assert `var(--surface)` and forbid `class="db-card"`/`class="filters"` |
| AWS/Azure wizard chips deep-link into the Provider Setup guide | Task 1 `ShowGuideLink`/`GuideProvider` + Task 2 `<a href="/admin/provider-setup?provider=…">` + Task 6 page honours `?provider=` |

Bead ly-ae6.12 acceptance (description): per-vertical modal (provider chips → collector token → copyable k8s Deployment YAML → provider step → self-register on first report) → Tasks 1-2, 6; Provider Setup admin page with big block buttons + AWS 3-path (tiered env-placeholder role grants / RDS-scoped IAM / Firehose ingress) + Terraform variant + Azure + PlanetScale → Tasks 3-4. (The modal is kind-parameterized so search/cache reuse it; only the DATABASE entry point is wired now — search/cache `+ ADD` entry points belong to their vertical beads, noted.)

### Placeholder scan

- No `TODO`, `TBD`, `similar to Task N`, or "add error handling" appears in any step. Every step ships literal code.
- Every referenced symbol is defined in this plan or verified in the repo: `Layout` (`web/layout.templ`), `templ.SafeURL` (templ builtin), `setup()`/`api.Config`/`api.NewServer` (`internal/api/server_test.go`, `server.go`), `store.NewStats`/`store.NewConfig` (used by `setup`), the collector env vars `LYNCEUS_SERVER_ID`/`LYNCEUS_COLLECTOR_TOKEN`/`LYNCEUS_INGESTION_URL`/`LYNCEUS_PG_DSN` (`cmd/collector/main.go:364-407`), and all tokens `--surface`/`--line`/`--line2`/`--raised`/`--acc`/`--acc2`/`--accbg`/`--dim`/`--faint`/`--mut`/`--text`/`--radius`/`--font-mono` (`web/static/css/tokens.css`).
- Guide code strings are transcribed verbatim from the verified prototype `provDocs` (`docs/design/Lynceus.dc.html:2491-2521`).

### Type-consistency check

- `handleAddComponent` reads `kind`/`provider` as `string`, casts to `web.AddComponentKind`/`web.AddProvider`; `BuildAddComponentView` validates and falls back — no invalid enum escapes.
- `handleProviderSetup*` cast the `provider` query string to `web.ProviderID`; `BuildProviderSetupView` maps unknown → unselected (`Guide == nil`).
- templ fragments consume exactly the structs the builders produce: `AddComponentModal(AddComponentView)`, `ProviderSetupPage/Body(ProviderSetupView)`. Chip `hx-get` uses `fmt.Sprintf("/partial/add?kind=%s&provider=%s", v.Kind, c.ID)` where `v.Kind` is `AddComponentKind` and `c.ID` is `AddProvider` (both `~string`, valid `%s` verbs). Block `hx-get` uses `string(b.ID)`.
- `ProviderSetupView.Guide` is `*ProviderGuide` (nil-able) so the unselected state is representable and the templ `if v.Guide == nil` branch is type-correct.
- No `float64`/`int64`/`string` mismatches: the only numeric-looking field, `GuideStep.N`, is a `string` ("1".."6") used directly in element ids (`"guide-code-" + st.N`).

### Adversarial-review resolutions

Every issue raised by adversarial review is resolved in-plan; each maps to a concrete edit above.

- **BLOCKING — Task 2 render test could never pass.** The old assertion `strings.Contains(html, `id="modal-root"`)` was unsatisfiable: the fragment emits the close button's `hx-target="#modal-root"` (prefix `="#`), never a literal `id="modal-root"`. **Fixed:** the Task 2 want-list now asserts `hx-target="#modal-root"`, and the misleading Task 2 note that claimed the substring satisfied the old assertion is rewritten to state plainly that the fragment contains no `id="modal-root"` element (the `#modal-root` container is added on the databases page in Task 6). Task 2 Step 4 now passes as written.
- **Design inconsistency — two admin surfaces contradicted on the collector env contract.** The wizard YAML drops `TARGET_ENDPOINT` for the real `LYNCEUS_PG_DSN`, yet the Task 3 AWS path-1 guide still set `TARGET_ENDPOINT`/`SHIP_VIA`/`FIREHOSE_STREAM`. **Fixed:** Task 3's guide code now renames `TARGET_ENDPOINT` → `LYNCEUS_PG_DSN` (both surfaces agree on the DSN var) and carries an inline `# illustrative — NOT read by the collector` comment on the two remaining pipeline hints; the Integration contract "Collector env contract" bullet records this single-surface-consistency decision. No test asserted `TARGET_ENDPOINT`, so the AWS guide test is unaffected.
- **ly-ae6.3 (scoped nav) silently omitted from Integration contract.** **Fixed:** a dedicated Integration-contract bullet now states ly-ae6.3 is *intentionally* not a dependency — the + ADD CLUSTER entry point lives on the fleet-level Databases list page and Provider Setup is a standalone `/admin/*` page, so neither sits in the scoped low-level nav.
- **Deferred user-menu wiring risked being orphaned.** **Fixed:** a reciprocal note was recorded on ly-ae6.13 (2026-07-10) requiring the `{Label:"Provider Setup", Href:"/admin/provider-setup"}` ADMIN entry; the Integration contract documents it and asks the implementer to verify it via `bd show ly-ae6.13` before close.
- **MINOR (semantics) — search/cache stuff a non-Postgres URL into `LYNCEUS_PG_DSN`.** **Fixed (flagged, correctly deferred):** the `addKinds` map now carries a NOTE that the OpenSearch/Valkey `dsn` values occupy the Postgres-only `LYNCEUS_PG_DSN` slot as placeholders, harmless because only the DATABASE entry point is wired; the search/cache vertical beads MUST supply the correct per-engine endpoint var before wiring their `+ ADD` buttons. Recorded in the deferral row below too.
- **MINOR (efficiency) — DB-free UI handler tests still spin up two testcontainers via `setup()`.** Acknowledged, not changed: it follows the established `internal/api` test pattern (there is no lighter `NewServer` path today), and the pure view-model + render tests (Tasks 1–4, package `web`) already give full DB-free coverage of the screens' logic — the four handler tests only assert route wiring. A lighter constructor is a reasonable future cleanup but out of scope for this bead.

### Deferral ledger (accepted, tracked gaps)

- **User-menu ADMIN entry** for Provider Setup → ly-ae6.13 (reciprocal note filed). Page reachable by URL + wizard deep-link meanwhile.
- **Search/cache `+ ADD` entry points** and their correct per-engine endpoint env var → their own vertical beads. The wizard is already kind-parameterized; only the DATABASE entry point + DSN mapping are wired/correct now.
- **Live "issue token" action** → ly-8b0.8 (manual step-1 instruction stands in).
- **Self-register on first report** → M5 ingestion/store (rendered as guidance copy only).
