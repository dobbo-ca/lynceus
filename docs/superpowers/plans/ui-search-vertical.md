# Search Vertical (OpenSearch/ES) — Domains + Nodes-by-role Implementation Plan

> For agentic workers: execute with **superpowers:subagent-driven-development**. Each task
> is self-contained and ends with a green build/test and a commit. Work on a dedicated
> branch in this worktree (e.g. `git switch -c ui-search-vertical-$(openssl rand -hex 2)`);
> never commit on `main`. Do the tasks in order — Task 2 imports the view-models from Task 1,
> Task 3 imports the templ components from Task 2.

**Goal:** Ship the fleet-scoped Search vertical UI — a **Domains** screen (per-domain cards with
engine glyph, version, provider chip, GREEN/YELLOW/RED status + reason, a 5-cell stat strip, a
role summary, and a link to Nodes) and a **Nodes-by-role** screen (sortable HEAP/NAME table with
CLUSTER_MANAGER/DATA/INGEST/COORDINATING role chips and dedicated-manager 0-shard rendering),
both gated on `enableElasticsearch || enableOpensearch`.

**Architecture:** Follows the repo's SSR pattern exactly — view-model structs + pure logic in
`web/search.go` (package `web`), templ components `Search{Domains,Nodes}Page/Body` in
`web/search.templ`, token-based component classes in `web/static/css/search.css`, HTTP handlers
in `internal/api/search.go` registered in `internal/api/server.go`, and an engine-enable predicate
on `api.Config`. The screens are **fleet-scope list screens** (they appear only in the fleet-level
nav, like the Database Clusters/Nodes/Databases lists), so they do not require the low-level scoped
nav. Because the OpenSearch/ES collector + stats-store + T1 wire types are **not yet built**
(tracked as **ly-wte**, which depends on the collector generalization seam **ly-h8x**), the handlers
return an empty view-model today and the screens render an explicit empty state; the full card/table
rendering is exercised by `web`-package templ unit tests that construct the view-models directly.

**Tech Stack:** Go 1.26 · [a-h/templ](https://github.com/a-h/templ) (`make templ` regenerates the
committed `_templ.go`) · HTMX (self-hosted, `hx-get`/`outerHTML` swaps) · net/http `ServeMux` ·
design tokens as CSS custom properties (`web/static/css/tokens.css`, F1 / ly-ae6.1) · testing via
`net/http/httptest` + rendered-HTML assertions. **These search handlers never touch a store** —
`fetchSearch*` return empty views until the ly-wte backend lands — so the api gating test is a
DB-free white-box test (`package api`) that builds a `*Server` with **nil stores**, exercises the
real route table + `withAuth` + 404/200 gate through `Server.Handler()`, and therefore runs even
when Docker/testcontainers are unavailable. No DB container and no DB mock are needed on this path.

## Global Constraints

Copy these rules verbatim into your working memory; every task must honor them.

- **Privacy — T1 only.** Every field these screens render is T1 (normalized, literal-free): names,
  versions, role labels, counts, percentages, status enums, package-authored reason strings. **Never**
  introduce a field capable of carrying a monitored-datastore literal (a raw query DSL, slowlog line,
  document value, field value) into any struct on this path. There is **no** T2/audited reveal on these
  screens. The privacy backbone is enforced at the wire-contract layer (ly-wte / `internal/proto`); the
  templ side only displays what the handler hands it.
- **No external hosts.** All CSS/JS/fonts/SVG are self-hosted under `web/static/` and referenced at
  `/static/…`. Never add a CDN/font/script host. `web/layout_test.go:TestLayout_NoExternalHosts` guards
  this — do not regress it. The engine magnifier glyph is inlined as raw `<svg>` markup (no remote asset).
- **Tokens, not legacy.** Style with the F1 design tokens via `var(--…)` (see `web/static/css/tokens.css`:
  `--surface --line --line2 --text --mut --dim --faint --acc --acc2 --accdim --warnT --critT --infoT --ok`).
  Static structural styling is inline on the elements (mirrors `docs/design/Lynceus.dc.html`); the small set
  of **runtime-varying** colors (status, stat tone, dedicated-manager shards) live as classes in
  `web/static/css/search.css`. **Do not** reach for `legacy.css` component classes on these new screens.
- **templ regeneration.** After editing any `.templ`, run `make templ` and commit the regenerated
  `_templ.go` alongside the source. CI fails if generated code is out of sync.
- **No DB, no DB mock on this path.** The repo rule is "real Postgres via testcontainers, never a DB
  mock" for tests that hit the DB (see `internal/api/databases_test.go:newDBPool`). The search
  handlers **do not hit the DB** (they render empty views until ly-wte), so this plan's api test is
  DB-free by construction — it builds a `*Server` with nil stores and never opens a pool. That neither
  needs a container nor introduces a mock; it simply exercises the store-free code path directly. Do
  **not** add a fake `store.Config`/`store.Stats` — there is nothing to fake.

---

### Task 1: Search view-models + status/sort pure logic

Pure, templ-free Go: the structs the screens render plus the status-computation and node-sort helpers.
No templ, no DB — fully unit-testable in isolation. This is where the COMPARISON gaps "domain status
computation (GREEN/YELLOW/RED with reason)" and the Nodes "sortable HEAP/NAME + dedicated-manager 0-shard"
logic are satisfied.

**Files**
- Create: `web/search.go`
- Create: `web/search_test.go`

**Interfaces**

Produces (package `web`):
```go
type DomainStatus string
const (
    DomainGreen  DomainStatus = "GREEN"
    DomainYellow DomainStatus = "YELLOW"
    DomainRed    DomainStatus = "RED"
)
func (s DomainStatus) Class() string            // "sd-status--GREEN|YELLOW|RED"
func (s DomainStatus) Tone() string             // "ok" | "warn" | "crit"

type DomainStat struct {
    Label string // "INDICES"
    Value string // "14"
    Sub   string // "86 shards (P+R)"
    Tone  string // tone-* class suffix: "text" | "warn" | "crit" | "ok" | "info" | "dim" | "faint" | "mut"
}
type SearchDomainCard struct {
    Name         string           // "search-logs"
    Version      string           // "2.19"
    Provider     string           // "SELF-HOSTED"
    Status       DomainStatus
    StatusReason string           // "2 unassigned replica shards on os-data-2"
    Stats        []DomainStat     // 5 cells: STATUS, INDICES, NODES, JVM HEAP, SEARCH RATE
    RoleSummary  string           // "3× CLUSTER_MANAGER · 2× DATA+INGEST · 1× COORDINATING"
}
type SearchDomainsView struct {
    Domains []SearchDomainCard
}

type SearchNodeRow struct {
    Name             string   // "os-manager-1"
    Roles            []string // ["CLUSTER_MANAGER"] | ["DATA","INGEST"] | ["COORDINATING"]
    Version          string   // "2.19"
    Heap             string   // "38%"
    CPU              string   // "8%"
    Disk             string   // "31%"
    Shards           string   // "0"
    DedicatedManager bool     // only CLUSTER_MANAGER role → holds 0 shards, render shards dimmed
}
func (n SearchNodeRow) ShardsClass() string     // "sn-shards--zero" if DedicatedManager else "tone-mut"
type SearchNodesView struct {
    Nodes []SearchNodeRow
    Sort  string // "heap" (default) | "name"
}
func (v SearchNodesView) SortLabel() string     // "HEAP" | "NAME"
func (v SearchNodesView) NextSort() string      // the other sort key

func ComputeDomainStatus(health string, unassignedShards int, worstNode string) (DomainStatus, string)
func SortSearchNodes(nodes []SearchNodeRow, sort string) // in-place
func IsDedicatedManager(roles []string) bool
```

- [ ] **Step 1: Write the failing test.** Create `web/search_test.go`:

```go
package web

import "testing"

func TestComputeDomainStatus(t *testing.T) {
	cases := []struct {
		name       string
		health     string
		unassigned int
		worstNode  string
		wantStatus DomainStatus
		wantReason string
	}{
		{"red primaries", "red", 1, "", DomainRed, "1 unassigned primary shards"},
		{"yellow with node", "yellow", 2, "os-data-2", DomainYellow, "2 unassigned replica shards on os-data-2"},
		{"yellow no node", "yellow", 0, "", DomainYellow, "replica shards not fully allocated"},
		{"green", "green", 0, "", DomainGreen, "all shards assigned"},
		{"green upper-case input", "GREEN", 0, "", DomainGreen, "all shards assigned"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotStatus, gotReason := ComputeDomainStatus(c.health, c.unassigned, c.worstNode)
			if gotStatus != c.wantStatus {
				t.Errorf("status = %q, want %q", gotStatus, c.wantStatus)
			}
			if gotReason != c.wantReason {
				t.Errorf("reason = %q, want %q", gotReason, c.wantReason)
			}
		})
	}
}

func TestDomainStatus_ClassTone(t *testing.T) {
	for _, c := range []struct {
		s          DomainStatus
		wantClass  string
		wantTone   string
	}{
		{DomainGreen, "sd-status--GREEN", "ok"},
		{DomainYellow, "sd-status--YELLOW", "warn"},
		{DomainRed, "sd-status--RED", "crit"},
	} {
		if got := c.s.Class(); got != c.wantClass {
			t.Errorf("%s.Class() = %q, want %q", c.s, got, c.wantClass)
		}
		if got := c.s.Tone(); got != c.wantTone {
			t.Errorf("%s.Tone() = %q, want %q", c.s, got, c.wantTone)
		}
	}
}

func TestIsDedicatedManager(t *testing.T) {
	for _, c := range []struct {
		roles []string
		want  bool
	}{
		{[]string{"CLUSTER_MANAGER"}, true},
		{[]string{"CLUSTER_MANAGER", "DATA"}, false},
		{[]string{"DATA"}, false},
		{[]string{"COORDINATING"}, false},
		{nil, false},
	} {
		if got := IsDedicatedManager(c.roles); got != c.want {
			t.Errorf("IsDedicatedManager(%v) = %v, want %v", c.roles, got, c.want)
		}
	}
}

func TestSortSearchNodes(t *testing.T) {
	base := func() []SearchNodeRow {
		return []SearchNodeRow{
			{Name: "b", Heap: "20%"},
			{Name: "a", Heap: "80%"},
			{Name: "c", Heap: "50%"},
		}
	}
	byHeap := base()
	SortSearchNodes(byHeap, "heap")
	if got := []string{byHeap[0].Name, byHeap[1].Name, byHeap[2].Name}; got[0] != "a" || got[1] != "c" || got[2] != "b" {
		t.Errorf("heap sort order = %v, want [a c b]", got)
	}
	byName := base()
	SortSearchNodes(byName, "name")
	if got := []string{byName[0].Name, byName[1].Name, byName[2].Name}; got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Errorf("name sort order = %v, want [a b c]", got)
	}
}

func TestSearchNodesView_SortLabelNextSort(t *testing.T) {
	heap := SearchNodesView{Sort: "heap"}
	if heap.SortLabel() != "HEAP" || heap.NextSort() != "name" {
		t.Errorf("heap view: label=%q next=%q, want HEAP/name", heap.SortLabel(), heap.NextSort())
	}
	name := SearchNodesView{Sort: "name"}
	if name.SortLabel() != "NAME" || name.NextSort() != "heap" {
		t.Errorf("name view: label=%q next=%q, want NAME/heap", name.SortLabel(), name.NextSort())
	}
}

func TestSearchNodeRow_ShardsClass(t *testing.T) {
	ded := SearchNodeRow{DedicatedManager: true}
	if got := ded.ShardsClass(); got != "sn-shards--zero" {
		t.Errorf("dedicated ShardsClass() = %q, want sn-shards--zero", got)
	}
	data := SearchNodeRow{DedicatedManager: false}
	if got := data.ShardsClass(); got != "tone-mut" {
		t.Errorf("data ShardsClass() = %q, want tone-mut", got)
	}
}
```

- [ ] **Step 2: Run the test — expect FAIL (undefined symbols).**
  `go test ./web/ -run 'TestComputeDomainStatus|TestDomainStatus_ClassTone|TestIsDedicatedManager|TestSortSearchNodes|TestSearchNodesView_SortLabelNextSort|TestSearchNodeRow_ShardsClass'`
  Expected: compile error `undefined: ComputeDomainStatus` / `undefined: DomainGreen` etc.

- [ ] **Step 3: Implement.** Create `web/search.go`:

```go
package web

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// DomainStatus is the OpenSearch/Elasticsearch cluster-health rollup for a
// domain, surfaced as GREEN / YELLOW / RED. It is a T1 enum — never a literal.
type DomainStatus string

const (
	DomainGreen  DomainStatus = "GREEN"
	DomainYellow DomainStatus = "YELLOW"
	DomainRed    DomainStatus = "RED"
)

// Class returns the CSS class carrying this status's color (see search.css).
func (s DomainStatus) Class() string { return "sd-status--" + string(s) }

// Tone returns the tone-* class suffix used for the STATUS stat cell.
func (s DomainStatus) Tone() string {
	switch s {
	case DomainRed:
		return "crit"
	case DomainYellow:
		return "warn"
	default:
		return "ok"
	}
}

// DomainStat is one cell of a domain card's stat strip. Value/Sub are formatted
// T1 strings; Tone is a tone-* class suffix selecting a token color.
type DomainStat struct {
	Label string
	Value string
	Sub   string
	Tone  string
}

// SearchDomainCard is the view-model for one OpenSearch/ES domain. Every field
// is a structural identifier, a count/aggregate, an enum, or a package-authored
// reason string — no monitored-datastore literal.
type SearchDomainCard struct {
	Name         string
	Version      string
	Provider     string
	Status       DomainStatus
	StatusReason string
	Stats        []DomainStat
	RoleSummary  string
}

// SearchDomainsView is the Domains screen view-model.
type SearchDomainsView struct {
	Domains []SearchDomainCard
}

// SearchNodeRow is one row of the Nodes-by-role table.
type SearchNodeRow struct {
	Name             string
	Roles            []string
	Version          string
	Heap             string
	CPU              string
	Disk             string
	Shards           string
	DedicatedManager bool
}

// ShardsClass dims the shard count for a dedicated cluster-manager node (which
// holds no shards) and otherwise uses the muted metric color.
func (n SearchNodeRow) ShardsClass() string {
	if n.DedicatedManager {
		return "sn-shards--zero"
	}
	return "tone-mut"
}

// SearchNodesView is the Nodes screen view-model. Sort is "heap" (default) or
// "name"; the handler has already applied it to Nodes.
type SearchNodesView struct {
	Nodes []SearchNodeRow
	Sort  string
}

// SortLabel is the human label for the current sort.
func (v SearchNodesView) SortLabel() string {
	if v.Sort == "name" {
		return "NAME"
	}
	return "HEAP"
}

// NextSort returns the sort key the toggle should switch to.
func (v SearchNodesView) NextSort() string {
	if v.Sort == "name" {
		return "heap"
	}
	return "name"
}

// ComputeDomainStatus maps the raw cluster-health color (from _cluster/health.status:
// green|yellow|red) plus the unassigned-shard count into a display status and a
// package-authored, literal-free reason string.
func ComputeDomainStatus(health string, unassignedShards int, worstNode string) (DomainStatus, string) {
	switch strings.ToLower(strings.TrimSpace(health)) {
	case "red":
		return DomainRed, fmt.Sprintf("%d unassigned primary shards", unassignedShards)
	case "yellow":
		if unassignedShards > 0 && worstNode != "" {
			return DomainYellow, fmt.Sprintf("%d unassigned replica shards on %s", unassignedShards, worstNode)
		}
		return DomainYellow, "replica shards not fully allocated"
	default:
		return DomainGreen, "all shards assigned"
	}
}

// SortSearchNodes sorts nodes in place: by heap descending (default) or by name
// ascending. Heap is parsed leniently from its "NN%" string form.
func SortSearchNodes(nodes []SearchNodeRow, sort string) {
	if sort == "name" {
		slices.SortFunc(nodes, func(a, b SearchNodeRow) int { return strings.Compare(a.Name, b.Name) })
		return
	}
	slices.SortFunc(nodes, func(a, b SearchNodeRow) int { return heapPct(b.Heap) - heapPct(a.Heap) })
}

// heapPct parses "58%" (or " 0% ") into 58. Unparseable → 0.
func heapPct(s string) int {
	n, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimSpace(s), "%"))
	return n
}

// IsDedicatedManager reports whether a node's only role is CLUSTER_MANAGER, i.e.
// a dedicated manager that holds zero shards.
func IsDedicatedManager(roles []string) bool {
	return len(roles) == 1 && roles[0] == "CLUSTER_MANAGER"
}
```

- [ ] **Step 4: Run the test — expect PASS.**
  `go test ./web/ -run 'TestComputeDomainStatus|TestDomainStatus_ClassTone|TestIsDedicatedManager|TestSortSearchNodes|TestSearchNodesView_SortLabelNextSort|TestSearchNodeRow_ShardsClass'`
  Expected: `ok  github.com/dobbo-ca/lynceus/web`.

- [ ] **Step 5: Commit.**
  `git add web/search.go web/search_test.go`
  `git commit -m "feat(web): search vertical view-models + status/sort logic (ly-ae6.10)"`

---

### Task 2: Search screens (templ) + token component CSS

The two token-styled screens plus the runtime-color classes they need. Renders the Domains cards
(engine magnifier glyph, version, provider chip, status+reason, 5-cell stat strip, role summary,
NODES BY ROLE link, + ADD DOMAIN) and the Nodes-by-role table (role chips, heap/cpu/disk/shards,
dedicated-manager dim, SORT toggle, explanatory footer). Rendering is verified by `web`-package
unit tests that construct the view-models directly (the templ analog of `web/layout_test.go`).

**Files**
- Create: `web/static/css/search.css`
- Create: `web/search.templ`
- Create (generated by `make templ`, committed): `web/search_templ.go`
- Modify: `web/search_test.go` (append render tests)

**Interfaces**

Consumes (from Task 1): `SearchDomainsView`, `SearchDomainCard`, `DomainStat`, `DomainStatus.Class`,
`SearchNodesView`, `SearchNodeRow.ShardsClass`, `SearchNodesView.SortLabel/NextSort`.

Produces (package `web`, templ components):
```go
templ SearchDomainsPage(v SearchDomainsView)   // full page (@Layout) → #search-domains-body
templ SearchDomainsBody(v SearchDomainsView)   // HTMX fragment (no layout)
templ SearchNodesPage(v SearchNodesView)       // full page (@Layout) → #search-nodes-body
templ SearchNodesBody(v SearchNodesView)       // HTMX fragment (no layout), swapped by SORT toggle
```

- [ ] **Step 1: Write the failing render tests.** Append to `web/search_test.go`:

```go
import (
	"context"
	"strings"
	"testing"
)

func renderSearchDomains(t *testing.T, v SearchDomainsView) string {
	t.Helper()
	var sb strings.Builder
	if err := SearchDomainsBody(v).Render(context.Background(), &sb); err != nil {
		t.Fatalf("render domains: %v", err)
	}
	return sb.String()
}

func renderSearchNodes(t *testing.T, v SearchNodesView) string {
	t.Helper()
	var sb strings.Builder
	if err := SearchNodesBody(v).Render(context.Background(), &sb); err != nil {
		t.Fatalf("render nodes: %v", err)
	}
	return sb.String()
}

func sampleDomain() SearchDomainCard {
	return SearchDomainCard{
		Name: "search-logs", Version: "2.19", Provider: "SELF-HOSTED",
		Status: DomainYellow, StatusReason: "2 unassigned replica shards on os-data-2",
		RoleSummary: "3× CLUSTER_MANAGER · 2× DATA+INGEST · 1× COORDINATING",
		Stats: []DomainStat{
			{Label: "STATUS", Value: "YELLOW", Sub: "2 unassigned replica shards", Tone: "warn"},
			{Label: "INDICES", Value: "14", Sub: "86 shards (P+R)", Tone: "text"},
			{Label: "NODES", Value: "6", Sub: "3 mgr · 2 data+ingest · 1 coord", Tone: "text"},
			{Label: "JVM HEAP", Value: "58%", Sub: "fleet mean · GC healthy", Tone: "text"},
			{Label: "SEARCH RATE", Value: "1,840/s", Sub: "p95 latency 46 ms", Tone: "text"},
		},
	}
}

func TestSearchDomainsBody_parity(t *testing.T) {
	html := renderSearchDomains(t, SearchDomainsView{Domains: []SearchDomainCard{sampleDomain()}})
	for _, want := range []string{
		`/static/css/search.css`,                                  // token component CSS linked
		`<circle cx="10.5"`,                                       // inlined eng-os magnifier glyph
		"search-logs", "v2.19", "SELF-HOSTED",                     // header identity
		"sd-status--YELLOW", "[YELLOW]",                           // status class + label
		"2 unassigned replica shards on os-data-2",                // status reason
		"STATUS", "INDICES", "NODES", "JVM HEAP", "SEARCH RATE",   // stat labels
		"86 shards (P+R)", "1,840/s",                              // stat subs/values
		"tone-warn", "tone-text",                                  // stat tones
		"3× CLUSTER_MANAGER · 2× DATA+INGEST · 1× COORDINATING",   // role summary
		"NODES BY ROLE", `/search/nodes`,                          // link to Nodes
		"+ ADD DOMAIN", `/onboarding?kind=opensearch`,             // wizard hook (ly-ae6.12 contract)
		"LIVE",                                                    // live badge
	} {
		if !strings.Contains(html, want) {
			t.Errorf("domains body missing %q", want)
		}
	}
}

func TestSearchDomainsBody_empty(t *testing.T) {
	html := renderSearchDomains(t, SearchDomainsView{})
	if !strings.Contains(html, "No search domains monitored yet") {
		t.Errorf("expected empty state; got: %s", html)
	}
}

func sampleNodes() []SearchNodeRow {
	return []SearchNodeRow{
		{Name: "os-manager-1", Roles: []string{"CLUSTER_MANAGER"}, Version: "2.19", Heap: "38%", CPU: "8%", Disk: "31%", Shards: "0", DedicatedManager: true},
		{Name: "os-data-1", Roles: []string{"DATA", "INGEST"}, Version: "2.19", Heap: "61%", CPU: "42%", Disk: "54%", Shards: "42"},
		{Name: "os-coord-1", Roles: []string{"COORDINATING"}, Version: "2.19", Heap: "22%", CPU: "11%", Disk: "12%", Shards: "0"},
	}
}

func TestSearchNodesBody_parity(t *testing.T) {
	html := renderSearchNodes(t, SearchNodesView{Nodes: sampleNodes(), Sort: "heap"})
	for _, want := range []string{
		`/static/css/search.css`,
		"os-manager-1", "os-data-1", "os-coord-1",
		"CLUSTER_MANAGER", "DATA", "INGEST", "COORDINATING",       // role chips
		"38%", "61%", "42",                                        // heap + shards values
		"sn-shards--zero",                                          // dedicated-manager 0-shard dim
		"SORT: HEAP",                                               // sort control label
		`/partial/search/nodes?sort=name`,                         // toggle target
		"DEDICATED CLUSTER_MANAGER NODES HOLD NO SHARDS",          // explanatory footer
		"search-nodes-body",                                       // swap target id
	} {
		if !strings.Contains(html, want) {
			t.Errorf("nodes body missing %q", want)
		}
	}
}

func TestSearchNodesBody_sortLabelReflectsView(t *testing.T) {
	html := renderSearchNodes(t, SearchNodesView{Nodes: sampleNodes(), Sort: "name"})
	if !strings.Contains(html, "SORT: NAME") {
		t.Error("nodes body should show SORT: NAME when Sort==name")
	}
	if !strings.Contains(html, `/partial/search/nodes?sort=heap`) {
		t.Error("nodes body toggle should target sort=heap when Sort==name")
	}
}

func TestSearchNodesBody_empty(t *testing.T) {
	html := renderSearchNodes(t, SearchNodesView{Sort: "heap"})
	if !strings.Contains(html, "No search nodes monitored yet") {
		t.Errorf("expected empty state; got: %s", html)
	}
}

// TestSearchScreens_NoExternalHosts enforces the privacy-backbone rule on the
// SEARCH screens themselves. web/layout_test.go:TestLayout_NoExternalHosts only
// renders Layout, so it does NOT cover these fragments (or the inlined magnifier
// glyph); this test does. Every asset the screens reference must be relative
// (/static/…, /search/…) — no CDN host, no absolute URL.
func TestSearchScreens_NoExternalHosts(t *testing.T) {
	htmls := []string{
		renderSearchDomains(t, SearchDomainsView{Domains: []SearchDomainCard{sampleDomain()}}),
		renderSearchNodes(t, SearchNodesView{Nodes: sampleNodes(), Sort: "heap"}),
	}
	for _, html := range htmls {
		for _, bad := range []string{"unpkg.com", "googleapis.com", "gstatic.com", "cdn.jsdelivr.net", "http://", "https://"} {
			if strings.Contains(html, bad) {
				t.Errorf("search screen references external ref %q — assets must be self-hosted (privacy backbone)", bad)
			}
		}
	}
}
```

  Note: `web/search_test.go` from Task 1 declares `package web` and imports `testing`; add the
  `context` and `strings` imports shown above (Go will error on the duplicate `testing` import if
  you paste it twice — keep a single import block).

- [ ] **Step 2: Run the test — expect FAIL (undefined components).**
  `go test ./web/ -run 'TestSearchDomainsBody|TestSearchNodesBody|TestSearchScreens'`
  Expected: compile error `undefined: SearchDomainsBody` / `undefined: SearchNodesBody`.

- [ ] **Step 3a: Implement the component CSS.** Create `web/static/css/search.css`:

```css
/* Search vertical component styles — token-based (F1 / ly-ae6.1).
   Only runtime-varying colors live here as classes; static structural styling
   is inline on the elements (mirrors docs/design/Lynceus.dc.html search screens).
   NEVER hardcode a color — always reference a token from tokens.css. */
.tone-text  { color: var(--text); }
.tone-mut   { color: var(--mut); }
.tone-warn  { color: var(--warnT); }
.tone-crit  { color: var(--critT); }
.tone-ok    { color: var(--acc2); }
.tone-info  { color: var(--infoT); }
.tone-dim   { color: var(--dim); }
.tone-faint { color: var(--faint); }

.sd-status--GREEN  { color: var(--acc2); }
.sd-status--YELLOW { color: var(--warnT); }
.sd-status--RED    { color: var(--critT); }

.sn-shards--zero { color: var(--faint); }
```

- [ ] **Step 3b: Implement the screens.** Create `web/search.templ`:

```go
package web

// engOS renders the inlined magnifier glyph (the #eng-os slot from F1; inlined
// here so the screen is self-contained and does not depend on a global sprite
// sheet). currentColor makes it theme-aware.
templ engOS() {
	<svg width="12" height="12" viewBox="0 0 24 24" aria-hidden="true">
		<circle cx="10.5" cy="10.5" r="6" fill="none" stroke="currentColor" stroke-width="2"></circle>
		<line x1="15" y1="15" x2="20" y2="20" stroke="currentColor" stroke-width="2" stroke-linecap="round"></line>
	</svg>
}

// SearchDomainsPage is the full Domains page. The body fragment is swappable by
// HTMX; when the ly-ae6.2 scoped shell lands it swaps SearchDomainsBody directly.
templ SearchDomainsPage(v SearchDomainsView) {
	@Layout("Lynceus — search domains", "OpenSearch / Elasticsearch domains — a domain groups nodes by role") {
		@SearchDomainsBody(v)
	}
}

templ SearchDomainsBody(v SearchDomainsView) {
	<link rel="stylesheet" href="/static/css/search.css"/>
	<div id="search-domains-body" style="padding: 18px 22px 32px; display: flex; flex-direction: column; gap: 14px; max-width: 1400px; font-family: var(--font-ui);">
		<div style="display: flex; align-items: baseline; gap: 12px;">
			<span style="font-size: 17px; font-weight: 600; color: var(--text);">Domains</span>
			<span style="font-family: var(--font-mono); font-size: 10px; color: var(--acc); border: 1px solid var(--acc); padding: 0 5px; border-radius: var(--radius-badge);">LIVE</span>
			<span style="font-family: var(--font-mono); font-size: 10.5px; color: var(--faint); letter-spacing: .08em;">OPENSEARCH / ELASTICSEARCH — A DOMAIN GROUPS NODES BY ROLE</span>
			<span style="flex: 1;"></span>
			<a href={ templ.SafeURL("/onboarding?kind=opensearch") } style="font-family: var(--font-mono); font-size: 10.5px; color: var(--acc2); border: 1px solid var(--acc); padding: 4px 9px; border-radius: var(--radius); text-decoration: none;">+ ADD DOMAIN</a>
		</div>
		if len(v.Domains) == 0 {
			<p style="font-family: var(--font-mono); font-size: 11.5px; color: var(--dim);">No search domains monitored yet — enable an OpenSearch or Elasticsearch collector (backend ly-wte).</p>
		} else {
			for _, d := range v.Domains {
				<div style="border: 1px solid var(--line); border-radius: var(--radius); background: var(--surface);">
					<div style="padding: 10px 14px; border-bottom: 1px solid var(--line); display: flex; align-items: center; gap: 12px; font-family: var(--font-mono);">
						<span style="width: 20px; height: 20px; border: 1.5px solid var(--acc2); color: var(--acc2); display: flex; align-items: center; justify-content: center; border-radius: var(--radius); flex-shrink: 0;" title="OPENSEARCH">
							@engOS()
						</span>
						<span style="font-size: 13.5px; font-weight: 600; color: var(--text);">{ d.Name }</span>
						<span style="font-size: 10px; color: var(--infoT);">{ "v" + d.Version }</span>
						<span style="font-size: 9.5px; border: 1px solid var(--line); padding: 1px 6px; border-radius: var(--radius-badge); color: var(--infoT); letter-spacing: .06em;">{ d.Provider }</span>
						<span style="flex: 1;"></span>
						<span class={ d.Status.Class() } style="font-size: 10px;">[{ string(d.Status) }] { d.StatusReason }</span>
					</div>
					<div style="display: grid; grid-template-columns: repeat(5, 1fr);">
						for _, st := range d.Stats {
							<div style="padding: 10px 14px; border-right: 1px solid var(--line2); display: flex; flex-direction: column; gap: 2px;">
								<span style="font-family: var(--font-mono); font-size: 9.5px; letter-spacing: .1em; color: var(--faint);">{ st.Label }</span>
								<span class={ "tone-" + st.Tone } style="font-family: var(--font-mono); font-size: 17px; font-weight: 600; font-variant-numeric: tabular-nums;">{ st.Value }</span>
								<span style="font-size: 10.5px; color: var(--dim);">{ st.Sub }</span>
							</div>
						}
					</div>
					<div style="padding: 9px 14px; border-top: 1px solid var(--line2); display: flex; gap: 12px; align-items: center; font-family: var(--font-mono); font-size: 10px; color: var(--dim); letter-spacing: .04em;">
						<span>{ "ROLES: " + d.RoleSummary }</span>
						<span style="flex: 1;"></span>
						<a href={ templ.SafeURL("/search/nodes") } style="color: var(--acc2); text-decoration: none;">NODES BY ROLE →</a>
					</div>
				</div>
			}
		}
	</div>
}

// SearchNodesPage is the full Nodes-by-role page.
templ SearchNodesPage(v SearchNodesView) {
	@Layout("Lynceus — search nodes", "search node roles determine what each node does") {
		@SearchNodesBody(v)
	}
}

templ SearchNodesBody(v SearchNodesView) {
	<link rel="stylesheet" href="/static/css/search.css"/>
	<div id="search-nodes-body" style="padding: 18px 22px 32px; display: flex; flex-direction: column; gap: 14px; max-width: 1400px; font-family: var(--font-ui);">
		<div style="display: flex; align-items: baseline; gap: 12px;">
			<span style="font-size: 17px; font-weight: 600; color: var(--text);">Search Nodes</span>
			<span style="font-family: var(--font-mono); font-size: 10px; color: var(--acc); border: 1px solid var(--acc); padding: 0 5px; border-radius: var(--radius-badge);">LIVE</span>
			<span style="font-family: var(--font-mono); font-size: 10.5px; color: var(--faint); letter-spacing: .08em;">NODE ROLES DETERMINE WHAT EACH NODE DOES</span>
			<span style="flex: 1;"></span>
			<a
				href={ templ.SafeURL("/search/nodes?sort=" + v.NextSort()) }
				hx-get={ "/partial/search/nodes?sort=" + v.NextSort() }
				hx-target="#search-nodes-body"
				hx-swap="outerHTML"
				style="font-family: var(--font-mono); font-size: 10.5px; color: var(--dim); border: 1px solid var(--line); padding: 4px 9px; border-radius: var(--radius); text-decoration: none; cursor: pointer;"
			>{ "SORT: " + v.SortLabel() } ⇅</a>
		</div>
		if len(v.Nodes) == 0 {
			<p style="font-family: var(--font-mono); font-size: 11.5px; color: var(--dim);">No search nodes monitored yet — enable an OpenSearch or Elasticsearch collector (backend ly-wte).</p>
		} else {
			<div style="border: 1px solid var(--line); border-radius: var(--radius); background: var(--surface); overflow-x: auto;">
				<div style="min-width: 900px;">
					<div style="display: grid; grid-template-columns: 170px minmax(240px, 1fr) 64px 70px 70px 70px 70px; gap: 12px; padding: 8px 14px; border-bottom: 1px solid var(--line); font-family: var(--font-mono); font-size: 9.5px; letter-spacing: .1em; color: var(--faint);">
						<span>NODE</span><span>ROLES</span><span>VER</span>
						<span style="text-align: right;">HEAP</span><span style="text-align: right;">CPU</span>
						<span style="text-align: right;">DISK</span><span style="text-align: right;">SHARDS</span>
					</div>
					for _, n := range v.Nodes {
						<div style="display: grid; grid-template-columns: 170px minmax(240px, 1fr) 64px 70px 70px 70px 70px; gap: 12px; padding: 8px 14px; border-bottom: 1px solid var(--line2); align-items: center; font-variant-numeric: tabular-nums;">
							<span style="font-family: var(--font-mono); font-size: 11.5px; font-weight: 600; color: var(--text);">{ n.Name }</span>
							<div style="display: flex; gap: 5px; flex-wrap: wrap;">
								for _, r := range n.Roles {
									<span style="font-family: var(--font-mono); font-size: 9px; border: 1px solid var(--line); padding: 1px 6px; border-radius: var(--radius-badge); color: var(--infoT); letter-spacing: .06em;">{ r }</span>
								}
							</div>
							<span style="font-family: var(--font-mono); font-size: 10px; color: var(--dim);">{ "v" + n.Version }</span>
							<span class="tone-mut" style="font-family: var(--font-mono); font-size: 11.5px; text-align: right;">{ n.Heap }</span>
							<span class="tone-mut" style="font-family: var(--font-mono); font-size: 11.5px; text-align: right;">{ n.CPU }</span>
							<span class="tone-mut" style="font-family: var(--font-mono); font-size: 11.5px; text-align: right;">{ n.Disk }</span>
							<span class={ n.ShardsClass() } style="font-family: var(--font-mono); font-size: 11.5px; text-align: right;">{ n.Shards }</span>
						</div>
					}
				</div>
			</div>
			<div style="font-family: var(--font-mono); font-size: 10px; color: var(--faint); letter-spacing: .04em;">DEDICATED CLUSTER_MANAGER NODES HOLD NO SHARDS; DATA NODES CARRY THE INDEX WORKLOAD; COORDINATING NODES FAN OUT QUERIES.</div>
		}
	</div>
}
```

  Note on tokens: `--font-ui`, `--font-mono`, `--radius`, `--radius-badge` are F1 tokens. Confirm
  they exist in `web/static/css/tokens.css` before relying on the exact names; if a name differs,
  use the actual token (do not hardcode a fallback). `--radius` is 2px, `--radius-badge` is 1px.

- [ ] **Step 4: Regenerate templ + run the test — expect PASS.**
  `make templ` (regenerates `web/search_templ.go`), then
  `go test ./web/ -run 'TestSearchDomainsBody|TestSearchNodesBody|TestSearchScreens'`
  Expected: `ok  github.com/dobbo-ca/lynceus/web`. If a token name was wrong, the render still
  succeeds (unknown `var(--x)` is inert CSS) but fix it for visual parity.

- [ ] **Step 5: Confirm the whole web package still passes.**
  `go test ./web/`
  Expected: PASS. Note the coverage split: `TestLayout_NoExternalHosts` guards **only** the Layout
  output; the SEARCH screens' external-host guarantee is enforced by the **new**
  `TestSearchScreens_NoExternalHosts` (Step 1), which renders both fragments (including the inlined
  magnifier glyph) and asserts no CDN host / absolute URL appears.

- [ ] **Step 6: Commit.**
  `git add web/search.templ web/search_templ.go web/search_test.go web/static/css/search.css`
  `git commit -m "feat(web): search vertical Domains + Nodes-by-role screens (ly-ae6.10)"`

---

### Task 3: Engine-enable gating + handlers + routes

Wire the screens into the HTTP server behind the `enableElasticsearch || enableOpensearch` gate.
Adds the enable flags to `api.Config`, the four routes, and handlers that render the (currently
empty, backend ly-wte) view-models. Establishes the **integration contract** the scoped nav
(ly-ae6.3) consumes: the two routes + the `Config.SearchEnabled()` predicate.

**Files**
- Modify: `internal/api/server.go` (add fields to `Config`, register routes)
- Create: `internal/api/search.go`
- Create: `internal/api/search_test.go`
- Modify: `cmd/api/main.go` (read the two enable env vars into `Config`)

**Interfaces**

Produces (package `api`):
```go
// on Config:
EnableOpensearch    bool
EnableElasticsearch bool
func (c Config) SearchEnabled() bool  // EnableOpensearch || EnableElasticsearch

// handlers (all gate on SearchEnabled(); 404 when disabled):
func (s *Server) handleSearchDomains(w http.ResponseWriter, r *http.Request)
func (s *Server) handleSearchDomainsPartial(w http.ResponseWriter, r *http.Request)
func (s *Server) handleSearchNodes(w http.ResponseWriter, r *http.Request)
func (s *Server) handleSearchNodesPartial(w http.ResponseWriter, r *http.Request)
func (s *Server) fetchSearchDomains(r *http.Request) web.SearchDomainsView
func (s *Server) fetchSearchNodes(r *http.Request) web.SearchNodesView
```
Routes registered: `GET /search/domains`, `GET /partial/search/domains`, `GET /search/nodes`,
`GET /partial/search/nodes`.

Consumes (from Task 2): `web.SearchDomainsPage/Body`, `web.SearchNodesPage/Body`, `web.SearchNodesView`.

- [ ] **Step 1: Write the failing test.** Create `internal/api/search_test.go`. This is a **DB-free
  white-box test** (`package api`, so it can build a `*Server` with **nil stores**). The search
  handlers only read `s.cfg` and call `fetchSearch*` — which return store-free empty views — so this
  exercises the real route table, `withAuth`, the 404/200 gate, and full rendering **without a DB
  container**. It always runs (no `t.Skip`), so the RED/GREEN loop for gating survives a no-Docker
  environment. A `package api` test file coexists fine with the existing `package api_test` files in
  the same directory.

```go
package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestConfig_SearchEnabled is a pure predicate test — no server, no DB.
func TestConfig_SearchEnabled(t *testing.T) {
	for _, c := range []struct {
		os, es bool
		want   bool
	}{
		{false, false, false},
		{true, false, true},
		{false, true, true},
		{true, true, true},
	} {
		cfg := Config{EnableOpensearch: c.os, EnableElasticsearch: c.es}
		if got := cfg.SearchEnabled(); got != c.want {
			t.Errorf("Config{os:%v es:%v}.SearchEnabled() = %v, want %v", c.os, c.es, got, c.want)
		}
	}
}

// newSearchHandler builds a fully-routed handler backed by NIL stores. The
// search handlers never dereference s.stats/s.conf/s.disc (fetchSearch* return
// empty, store-free views), so gating + routing + rendering are exercised with
// zero DB. DevAuth is forced on so /search/* reaches the handler instead of the
// 401 auth wall.
func newSearchHandler(t *testing.T, cfg Config) http.Handler {
	t.Helper()
	cfg.DevAuth = true
	s := &Server{cfg: cfg, mux: http.NewServeMux()}
	s.routes()
	return s.Handler()
}

func TestSearchDomains_disabled404(t *testing.T) {
	h := newSearchHandler(t, Config{})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/search/domains", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("disabled /search/domains = %d, want 404", w.Code)
	}
}

func TestSearchNodes_disabled404(t *testing.T) {
	h := newSearchHandler(t, Config{})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/search/nodes", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("disabled /search/nodes = %d, want 404", w.Code)
	}
}

func TestSearchDomains_enabledEmptyState(t *testing.T) {
	h := newSearchHandler(t, Config{EnableOpensearch: true})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/search/domains", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("enabled /search/domains = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	got := strings.ToLower(w.Body.String())
	for _, want := range []string{"<!doctype html>", "domains", "opensearch", "no search domains monitored yet", "/static/css/search.css"} {
		if !strings.Contains(got, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

func TestSearchNodes_sortParamEchoed(t *testing.T) {
	h := newSearchHandler(t, Config{EnableElasticsearch: true})

	// full page, default sort → SORT: HEAP
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/search/nodes", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("/search/nodes = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "SORT: HEAP") {
		t.Errorf("default nodes page should show SORT: HEAP; got: %s", w.Body.String())
	}

	// partial with sort=name → SORT: NAME, no doctype
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, httptest.NewRequest(http.MethodGet, "/partial/search/nodes?sort=name", nil))
	b2 := w2.Body.String()
	if strings.Contains(strings.ToLower(b2), "<!doctype") {
		t.Error("partial must not contain <!doctype")
	}
	if !strings.Contains(b2, "SORT: NAME") {
		t.Errorf("partial should echo SORT: NAME; got: %s", b2)
	}
}
```

  Why not testcontainers here: `api.NewServer` normally requires real stores, but these handlers
  never touch them, so paying for two Postgres containers (and skipping when Docker is absent) would
  add zero coverage. Building `&Server{cfg: …, mux: http.NewServeMux()}` + `s.routes()` reproduces the
  exact registration + auth + gate path `NewServer` wires, minus the store-only `disc` field the search
  handlers don't read. This is white-box (not a mock): no fake `store.*` is introduced.

- [ ] **Step 2: Run the test — expect FAIL.**
  `go test ./internal/api/ -run 'TestConfig_SearchEnabled|TestSearchDomains|TestSearchNodes'`
  Expected: compile error — `api.Config` has no field `EnableOpensearch`/method `SearchEnabled`, and
  `Server` has no `handleSearch*` methods, so the package fails to build. (No `t.Skip`: this test has
  no Docker dependency, so once it compiles it runs to completion in CI and locally.)

- [ ] **Step 3a: Add the enable flags + predicate.** Edit `internal/api/server.go`, extend `Config`:

```go
// Config is the server's runtime configuration.
type Config struct {
	// DevAuth, when true, bypasses authentication entirely and treats
	// every request as authenticated as a static dev admin. Only safe
	// in development — gated by the LYNCEUS_DEV_AUTH env var.
	DevAuth bool

	// EnableOpensearch / EnableElasticsearch gate the Search vertical
	// (Domains + Nodes-by-role) UI. When both are false the /search/*
	// routes 404 and the ly-ae6.3 scoped nav omits the SEARCH section.
	// Per-tenant config is M5+; these are process-level flags for now.
	EnableOpensearch    bool
	EnableElasticsearch bool
}

// SearchEnabled reports whether the Search vertical UI should be served.
// ly-ae6.3 reads the same predicate to decide whether to render the SEARCH
// nav section.
func (c Config) SearchEnabled() bool { return c.EnableOpensearch || c.EnableElasticsearch }
```

- [ ] **Step 3b: Register the routes.** In `internal/api/server.go` `routes()`, add (next to the
  other page/partial pairs):

```go
	s.mux.HandleFunc("GET /search/domains", s.handleSearchDomains)
	s.mux.HandleFunc("GET /partial/search/domains", s.handleSearchDomainsPartial)
	s.mux.HandleFunc("GET /search/nodes", s.handleSearchNodes)
	s.mux.HandleFunc("GET /partial/search/nodes", s.handleSearchNodesPartial)
```

- [ ] **Step 3c: Implement the handlers.** Create `internal/api/search.go`:

```go
package api

import (
	"net/http"

	"github.com/dobbo-ca/lynceus/web"
)

// handleSearchDomains renders the full Search Domains page (fleet scope).
// Gated on the search-engine enable flags.
func (s *Server) handleSearchDomains(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.SearchEnabled() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.SearchDomainsPage(s.fetchSearchDomains(r)).Render(r.Context(), w)
}

// handleSearchDomainsPartial renders just the Domains body fragment.
func (s *Server) handleSearchDomainsPartial(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.SearchEnabled() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.SearchDomainsBody(s.fetchSearchDomains(r)).Render(r.Context(), w)
}

// handleSearchNodes renders the full Nodes-by-role page.
func (s *Server) handleSearchNodes(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.SearchEnabled() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.SearchNodesPage(s.fetchSearchNodes(r)).Render(r.Context(), w)
}

// handleSearchNodesPartial renders just the Nodes body fragment (SORT toggle target).
func (s *Server) handleSearchNodesPartial(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.SearchEnabled() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.SearchNodesBody(s.fetchSearchNodes(r)).Render(r.Context(), w)
}

// fetchSearchDomains builds the Domains view-model. The OpenSearch/ES collector,
// T1 wire types, and stats-store schema that would populate it are tracked as
// ly-wte (which depends on the collector generalization seam ly-h8x); until they
// land no domains are reported and the screen renders its empty state.
func (s *Server) fetchSearchDomains(_ *http.Request) web.SearchDomainsView {
	return web.SearchDomainsView{}
}

// fetchSearchNodes builds the Nodes view-model. Backend data is pending (ly-wte);
// the sort param is still parsed and echoed so the toggle round-trips today.
func (s *Server) fetchSearchNodes(r *http.Request) web.SearchNodesView {
	sort := r.URL.Query().Get("sort")
	if sort != "name" {
		sort = "heap"
	}
	nodes := []web.SearchNodeRow(nil) // backend ly-wte pending
	web.SortSearchNodes(nodes, sort)
	return web.SearchNodesView{Nodes: nodes, Sort: sort}
}
```

- [ ] **Step 3d: Wire the enable flags into the running binary.** Without this, both flags default
  `false`, so the routes 404 in the real app and the feature is unreachable outside tests. Edit
  `cmd/api/main.go`. Add the two env reads next to the existing `devAuth` line (~line 42):

```go
	devAuth := os.Getenv("LYNCEUS_DEV_AUTH") == "true"
	enableOpensearch := os.Getenv("LYNCEUS_ENABLE_OPENSEARCH") == "true"
	enableElasticsearch := os.Getenv("LYNCEUS_ENABLE_ELASTICSEARCH") == "true"
```

  and pass them into the `Config` literal in the existing `api.NewServer(...)` call (~line 65):

```go
	srv := api.NewServer(api.Config{
		DevAuth:             devAuth,
		EnableOpensearch:    enableOpensearch,
		EnableElasticsearch: enableElasticsearch,
	},
		store.NewStats(pool).WithReadPool(statsRO),
		store.NewConfig(configPool).WithReadPool(configRO))
```

  Keep the change surgical: two `os.Getenv` reads + two struct fields, no new logging or flags. (The
  env-var names are the process-level knobs until per-tenant config arrives in M5+.)

- [ ] **Step 4: Run the test — expect PASS.**
  `go test ./internal/api/ -run 'TestConfig_SearchEnabled|TestSearchDomains|TestSearchNodes'`
  Expected: PASS (runs regardless of Docker — no `t.Skip` on this path).

- [ ] **Step 5: Full build + package tests, then verify the wired binary end-to-end.**
  `go build ./...` then `go test ./internal/api/ ./web/` — expected PASS.
  Then exercise the real gate through `cmd/api` (Step 3d makes this executable):

```bash
# with the dev DBs up (make dev-up) and DSNs exported:
LYNCEUS_DEV_AUTH=true LYNCEUS_ENABLE_OPENSEARCH=true go run ./cmd/api &   # note the addr it logs
curl -s localhost:8080/search/domains | grep -o 'No search domains monitored yet'   # → prints the empty state
# now disabled: restart without the flag
LYNCEUS_DEV_AUTH=true go run ./cmd/api &
curl -s -o /dev/null -w '%{http_code}\n' localhost:8080/search/domains              # → 404
```

  (The curl step is optional but now backed by real wiring, not an untracked edit. If you skip it,
  the DB-free api test in Step 1 still fully covers the 404/200 gate.)

- [ ] **Step 6: Commit.**
  `git add internal/api/server.go internal/api/search.go internal/api/search_test.go cmd/api/main.go`
  `git commit -m "feat(api): gate + serve search vertical routes behind enable flags (ly-ae6.10)"`

---

## Integration contracts (built by other beads — do not implement here)

These are the seams other UI beads plug into; this plan **provides** them and maps the remaining
COMPARISON gaps to their owning beads.

- **Scoped nav (ly-ae6.3, depends ly-ae6.2).** The SEARCH nav section (`SEARCH → Domains, Nodes`)
  is rendered by the scoped sidebar, **only at fleet scope**, and only when `s.cfg.SearchEnabled()`
  is true. It links to `GET /search/domains` and `GET /search/nodes` (this plan's routes). ly-ae6.3
  consumes the `Config.SearchEnabled()` predicate added in Task 3.
- **Global shell / top bar (ly-ae6.2).** These screens wrap `@Layout` today; when the ly-ae6.2 shell
  replaces the layout internals, it swaps `SearchDomainsBody` / `SearchNodesBody` directly (the body
  fragments are the stable unit). The engine text-mark (OPENSEARCH) in the shell + a global `#eng-os`
  sprite sheet are ly-ae6.1/ly-ae6.2 concerns; this plan inlines the magnifier glyph so the screens
  do not depend on them.
- **Fleet dashboard Search card (ly-ae6.4, workstream D).** The fleet dashboard's SEARCH stat cell,
  the per-domain summary card, and the cross-engine Needs-Attention "unassigned shards" item are
  built by ly-ae6.4; they link to `GET /search/domains` (this plan's route) and may reuse the
  `web.SearchDomainCard` view-model.
- **+ ADD DOMAIN wizard (ly-ae6.12, workstream M).** The Domains header renders a `+ ADD DOMAIN`
  link to `/onboarding?kind=opensearch` (the TARGET_KIND=opensearch path). That route is built by
  ly-ae6.12; until then the link 404s — acceptable for a design-parity affordance.
- **Data source (ly-wte, depends ly-h8x).** The OpenSearch/ES collector reader, T1 wire messages +
  contract test, and vanilla-Postgres partitioned stats-store schema that populate
  `SearchDomainCard` / `SearchNodeRow` are tracked as ly-wte. This plan defines the exact view-model
  contract ly-wte must satisfy; the handlers return empty views until it lands.

---

## Deliberate parity deviations (intentional — not gaps)

These differ from `docs/design/Lynceus.dc.html` on purpose. Each is safe for the single-domain mock
and carries a forward pointer for the multi-domain iteration.

- **Nodes header has no domain prefix.** The prototype header reads `search-logs · NODE ROLES
  DETERMINE WHAT EACH NODE DOES` (`Lynceus.dc.html:579`); this plan renders just `NODE ROLES DETERMINE
  WHAT EACH NODE DOES`. The Nodes screen here is a **fleet-wide** list (all search nodes across all
  domains), not scoped to one domain, so hard-coding `search-logs ·` would be wrong once there is more
  than one domain. When multi-domain scoping lands (ly-ae6.2/ly-ae6.3), re-introduce the prefix as the
  selected-domain name, not a literal.
- **"NODES BY ROLE →" links to the unscoped `/search/nodes`.** The Domains card link targets the
  fleet-wide Nodes list rather than a per-domain view (`/search/nodes?domain=<name>`). This satisfies
  the COMPARISON "link to Nodes" gap for the one-domain mock; the multi-domain iteration should pass
  the domain as a query param and have `fetchSearchNodes` filter on it.
- **`search.css` is linked inside the body fragment, not in `<head>`.** `SearchDomainsBody` /
  `SearchNodesBody` each emit `<link rel="stylesheet" href="/static/css/search.css">` so the HTMX
  fragment is self-contained; the browser dedupes the repeated link and it is re-sent on each
  SORT-toggle `outerHTML` swap. This is the pragmatic choice **until ly-ae6.2 owns the shell** — at
  that point move the link into `web/layout.templ`'s `<head>` (alongside `tokens.css`/`legacy.css`)
  and drop it from the fragments, matching how the existing screens load their CSS.

---

## Self-Review

### Spec-coverage: COMPARISON.md `search-vertical` gaps → task/owner

| COMPARISON gap | Covered by |
|---|---|
| No proto T1 wire-contract types for search domain/node/shard/role metrics | **ly-wte** (backend; out of scope, referenced) |
| No collector support (`_cluster/health` / `_cluster/stats` / `_nodes/stats` / `_cat/shards`) | **ly-wte** (backend) |
| No stats-store schema for domains/nodes/roles/shards/heap/rate/status | **ly-wte** (backend) |
| No Domains screen (engine icon, version, provider chip, status+reason, 5-cell stat strip, role summary, link to Nodes) | **Task 2** (`SearchDomainsBody`) + **Task 1** (`SearchDomainCard`, `DomainStat`) + **Task 3** (route/handler) |
| No Nodes screen (sortable HEAP/NAME, role chips, heap/cpu/disk/shards, dedicated-manager 0-shard) | **Task 1** (`SortSearchNodes`, `IsDedicatedManager`, `ShardsClass`) + **Task 2** (`SearchNodesBody`) + **Task 3** (sort param) |
| No domain status computation (GREEN/YELLOW/RED with reason) | **Task 1** (`ComputeDomainStatus`, `DomainStatus.Class/Tone`) |
| No enable flags + per-vertical nav gating | **Task 3** (`Config.EnableOpensearch/EnableElasticsearch`, `SearchEnabled()`, route 404 gate, + `cmd/api/main.go` env wiring so the running binary honors the flags); nav-section render → **ly-ae6.3** (consumes `SearchEnabled()`) |
| No engine sprite `#eng-os` / OPENSEARCH text mark wired into shell | **Task 2** inlines the magnifier glyph in the Domains card (self-contained); global sprite/text-mark in shell → **ly-ae6.1/ly-ae6.2** |
| No fleet-dashboard Search integration (search cards, counts, footer link, cross-engine attention) | **ly-ae6.4** (provides route + reusable `SearchDomainCard`) |
| No + ADD DOMAIN onboarding wizard | **ly-ae6.12** (Task 2 renders the `+ ADD DOMAIN` link to `/onboarding?kind=opensearch`) |

### Spec-coverage: bead ly-ae6.10 acceptance criteria → task

| Acceptance criterion | Covered by |
|---|---|
| Domain → nodes-with-roles (cluster_manager / data / ingest / coordinating) | Task 1 (`SearchNodeRow.Roles`) + Task 2 (role chips) |
| Managers hold 0 shards (dedicated-manager rendering) | Task 1 (`IsDedicatedManager`, `ShardsClass`) + Task 2 (`sn-shards--zero`) + footer note |
| GREEN/YELLOW/RED status with reason | Task 1 (`ComputeDomainStatus`) + Task 2 (`sd-status--*`, `[STATUS] reason`) |
| Gated on `enableElasticsearch \|\| enableOpensearch` | Task 3 (`SearchEnabled()` + route 404 gate, both exercised DB-free in `TestSearchDomains_disabled404` / `TestSearchNodes_disabled404` / `TestConfig_SearchEnabled`) |

### Placeholder scan
No `TBD`, no "add X here", no "similar to Task N", no code step lacking real code, **no undefined test
helper** (the earlier `httptestNewServer` placeholder is gone — the api test now uses stdlib
`httptest.NewRecorder`/`httptest.NewRequest` only). Every referenced symbol is either defined in this
plan (`SearchDomainsView`, `SearchNodeRow`, `ComputeDomainStatus`, `SortSearchNodes`,
`IsDedicatedManager`, `Config.SearchEnabled`, the four handlers/routes, `SearchDomainsPage/Body`,
`SearchNodesPage/Body`, `engOS`, `search.css` classes, `newSearchHandler` test helper) or verified to
exist in the repo: `web.Layout` (`web/layout.templ`), `templ.SafeURL` (used in `web/databases.templ`),
`api.NewServer`/`api.Config`/`Server.cfg`/`Server.mux`/`Server.routes`/`Server.Handler`
(`internal/api/server.go`), `web.StaticHandler` registered in `routes()` (DB-free), `os.Getenv` +
existing `devAuth`/`api.NewServer(...)` call site (`cmd/api/main.go`), `make templ` target
(`Makefile`), design tokens `--surface --line --line2 --text --mut --dim --faint --acc --acc2 --warnT
--critT --infoT --radius --radius-badge --font-ui --font-mono` (`web/static/css/tokens.css`). The api
test is DB-free and does **not** use `newDBPool`/`store.*` — no testcontainers dependency on this path.

### Type-consistency check
- `DomainStatus` is a `string`-backed enum; `Class()`/`Tone()` return `string`; the templ uses
  `d.Status.Class()` (attribute expression → `class={string}`, matches `web/cluster_views.templ:82`)
  and `string(d.Status)` for the `[YELLOW]` label. ✓
- `SearchNodesView.Sort` is `"heap"|"name"`; the handler normalizes any non-`"name"` value to
  `"heap"`; `SortLabel()`/`NextSort()`/`SortSearchNodes` all switch on the same two values. ✓
- `web.SortSearchNodes(nodes []SearchNodeRow, sort string)` sorts in place and is called by
  `fetchSearchNodes` on a `[]web.SearchNodeRow` (nil today). Passing `nil` is safe (`slices.SortFunc`
  on nil is a no-op). ✓
- Handlers render `web.Search{Domains,Nodes}{Page,Body}` — all `templ.Component`s with
  `.Render(ctx, w)`, matching the `web.DatabasesPage(...).Render(r.Context(), w)` call shape in
  `internal/api/databases.go`. ✓
- `Config.SearchEnabled()` has a value receiver on `Config` (copied by value throughout `api`), and
  `Server.cfg` is a `Config` value — `s.cfg.SearchEnabled()` compiles. ✓
- DB-free white-box test: `&Server{cfg: …, mux: http.NewServeMux()}` + `s.routes()` is safe with
  `stats`/`conf`/`disc` nil because (a) `routes()` only registers handlers (no store deref at
  registration) and calls `web.StaticHandler()` (embedded FS, no DB), and (b) the four `handleSearch*`
  handlers read only `s.cfg` and call `fetchSearch*`, which build empty views without touching any
  store. `Server`'s fields are unexported, so the test must be `package api` (white-box) — which
  coexists with the existing `package api_test` files. ✓
- `cmd/api/main.go` change is purely additive: two `os.Getenv(...) == "true"` reads plus two named
  fields in the existing `api.Config{…}` literal; `EnableOpensearch`/`EnableElasticsearch` are the
  `bool` fields added to `Config` in Task 3, so it compiles once Task 3 lands. ✓
- `slices` + `strconv` + `strings` are stdlib and available on Go 1.26 (`go.mod`). ✓
