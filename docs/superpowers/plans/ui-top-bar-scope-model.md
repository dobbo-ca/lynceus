# Global Top Bar + Scope Model + Searchable SCOPE Picker Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the design-parity 48px global top bar and the fleet→cluster→node/pooler→database scope model — a self-contained scope value type, a searchable SCOPE picker, a `← FLEET` reset, a time-range control, a poll indicator, a theme toggle, a user menu, and a reusable `⌖` row scope button — as the shell foundation that gates the sidebar (ly-ae6.3), fleet dashboard (ly-ae6.4), and scoped screens.

**Architecture:** A new pure `internal/scope` package encodes the current scope as a URL-safe `scope` query param (`ParseScope`/`Encode`), decoupled from routing. A new tokens-based `Shell` full-page templ layout in `web/` renders the top bar and wraps a body, exposing a `Sidebar templ.Component` slot for ly-ae6.3 and delegating the main content to the caller. A single `internal/api/shell.go` handler serves the live `/fleet` landing plus a `/partial/scope-options` HTMX search endpoint, enumerating scopeable entities from the existing `store.Config` topology (clusters → instances/nodes → cluster-qualified databases).

**Tech Stack:** Go 1.x, `github.com/a-h/templ` v0.3.1020 (SSR components, regenerated with `make templ`), HTMX (self-hosted at `/static/js/htmx.min.js`), CSS custom-property design tokens from ly-ae6.1 (`web/static/css/tokens.css`), `github.com/jackc/pgx/v5` config store, testcontainers-backed integration tests against real Postgres.

## Global Constraints

Copied verbatim from the project spec and design handoff. Every task's requirements implicitly include this section.

- **Privacy — T1 only.** Every value the shell renders is T1 (normalized, literal-free): entity names, kind labels, counts, range labels. Never introduce a raw-literal / query-sample field into any type or template in this plan. The SCOPE picker searches entity/kind/provider/engine metadata only — never SQL text or literals.
- **No external hosts.** All CSS/JS/fonts/SVG are self-hosted under `web/static/` and referenced as `/static/...`. Never add a CDN/font/script host. There is a contract test (`TestLayout_NoExternalHosts`); the new `Shell` layout gets the equivalent (`TestShell_NoExternalHosts`).
- **Tokens, not legacy.** New markup uses design-token custom properties (`var(--acc)`, `var(--line)`, `var(--surface)`, …) via a new `web/static/css/shell.css`. New screens must NOT use `web/static/css/legacy.css`. The `Shell` head links `tokens.css` + `shell.css` only (never `legacy.css`).
- **templ regen.** Any change to a `web/*.templ` file requires `make templ` to regenerate the committed `web/*_templ.go`; CI checks generated code is in sync. Commit the `.templ` and its `_templ.go` together.
- **Testcontainers, no DB mocks.** Integration tests hit real Postgres via testcontainers (`tcpostgres.Run("postgres:16", …)`, `testpg.ReadyWait()`), following `internal/api/server_test.go` and `internal/fleetview/summary_test.go`. Never mock the database.
- **TDD.** Write the failing test first, watch it fail, implement the minimal code, watch it pass, commit.
- **Surgical.** Do not modify or "improve" the existing `web.Layout` or any existing screen. The `Shell` is a NEW parallel layout; migrating legacy pages onto it is ly-ae6.7's job, not this plan's.

**Branch:** All work happens on a dedicated branch created from `main` in this worktree (e.g. `git switch -c ui-top-bar-scope-model`). Never commit on `main`. Verify `git branch --show-current` before each commit.

**Module path:** `github.com/dobbo-ca/lynceus`. **Range default:** `24H`. **Valid ranges (ordered):** `15M 1H 24H 7D 30D`. **Poll cadence:** `3s`.

---

## Dependencies & integration contracts

**This bead depends on:** ly-ae6.1 (design tokens + fonts + theme mechanism) — DONE on this branch. The shell consumes:
- Tokens in `web/static/css/tokens.css` (`--bg --rail --surface --raised --line --line2 --text --mut --dim --faint --acc --acc2 --accbg --accdim --crit/--critT --warn/--warnT --info/--infoT --radius(2px) --radius-badge(1px) --shadow-pop --font-ui --font-mono`) and the existing `@keyframes pulse` (already defined at `tokens.css:61`).
- The theme JS API: `window.Lynceus.setTheme(...)`, `cycleTheme()`, `setAccent(...)`; the no-flash bootstrap `themeBootstrapTag()` in `web/bootstrap.go`; `web/static/js/theme.js`.
- `web/static/js/theme.js` header comment already names ly-ae6.2 as the top-bar toggle's caller — wire to `window.Lynceus.cycleTheme()`.

**This bead produces, for downstream beads:**
- `scope.Scope` value type + `web.ScopeHref(scope.Scope)` URL scheme (`/fleet?scope=<encoded>`) — the canonical way to set scope. **ly-ae6.4/.5/.6/.7** put `@web.ScopeButton(sc)` (the `⌖`) on their otherwise-inert rows; **ly-ae6.6** repoints `ScopeHref` targets to per-scope Overview routes and fills the scoped Overview "OPEN ISSUES ON THIS …" body.
- `web.ShellView.Sidebar templ.Component` slot — **ly-ae6.3** supplies the per-scope nav tree here (it consumes `ShellView.Scope` to pick the tree). Until then the shell renders `placeholderSidebar()`.
- `web.ShellView.Range` + `RangeOptions(...)` — the shared range param. **ly-ae6.4** (fleet dashboard) and other data screens read `?range=` and map the label to a duration window.
  - ⚠️ **Downstream base-path caveat (ly-ae6.6):** BOTH `ScopeHref` **and** `RangeOptions` hardcode the `/fleet` base path in this chrome-only bead — every range link is a full-page nav to `/fleet?...`. That is correct while `/fleet` is the only landing, but once **ly-ae6.6** introduces per-scope Overview routes it must repoint **both** helpers together (add a base-path parameter or a `web.ScopeBase(sc)` resolver and thread it through `ScopeHref` and `RangeOptions`). If only `ScopeHref` is repointed, changing the time range on a scoped screen silently navigates the user back to `/fleet`, dropping their page context. The `RangeOptions` code below carries the same reminder as an inline comment so the divergence can't be missed.
- The `/fleet` route + `Shell` wrapper render a minimal placeholder main body (`shellPlaceholderMain`). **ly-ae6.4** replaces that body with the real Fleet dashboard; this plan owns only the chrome.

**Documented backend gaps (NOT re-planned here — reference only):**
- **Poolers** have no `store.Config` model yet. The scope model includes `scope.Pooler` for the URL scheme and forward-compat, but the picker enumerates none until pooler topology lands (tracked under the fleet-topology epic **ly-99s** / **ly-99s.4** "organization UI + backend").
- **Search-placeholder divergence from pixel-truth (conscious):** the prototype's picker input reads `search cluster / instance / pooler…` (`docs/design/Lynceus.dc.html:120`). This plan uses `search cluster / node / database…` instead — a deliberate deviation, because the picker only enumerates what exists server-side today (clusters, nodes, cluster-qualified databases) and advertising `pooler` would promise results the store can't return. ("instance" in the prototype is the same entity this codebase calls a NODE.) Revisit to match the prototype text exactly once pooler topology lands (ly-99s). Reviewers comparing against the prototype should expect this one-line difference.
- **Poll ticker is cosmetic (not a live freshness readout).** The `POLL 3S · UPDATED Ns AGO` indicator ships as chrome only; `shell.js` free-runs the "Ns AGO" counter on a local 1s interval because this bead renders no live data. It gains real meaning when the data screens add HTMX polling (ly-ae6.4 fleet dashboard onward), at which point the counter should reset on each successful poll instead of free-running. Not re-planned here — visual parity only.
- **Provider (aws/azure) and engine metadata** are not columns on `store.Cluster`/`Instance`/`ServerStream` yet (multi-engine + provider awareness are later M2–M6 work). `ScopeOption` carries no provider/engine field yet; search matches label + kind only. When those columns exist, extend `scopeOptions` filtering — do not block on them now.

---

### Task 1: Scope value model (`internal/scope`)

Pure, dependency-free scope type: encode/parse the `scope` URL param, no store or templ imports. This is "the scope model core."

**Files:**
- Create: `internal/scope/scope.go`
- Test: `internal/scope/scope_test.go`

**Interfaces:**
- Consumes: nothing (leaf package).
- Produces:
  - `type Kind string` with consts `Fleet Kind = "fleet"`, `Cluster = "cluster"`, `Node = "node"`, `Pooler = "pooler"`, `Database = "database"`.
  - `type Scope struct { Kind Kind; ClusterID string; NodeID string; PoolerID string; Database string }`.
  - `func (s Scope) IsFleet() bool`
  - `func (s Scope) Encode() string` — URL-param form; fleet → `""`; `cluster:<clusterID>`; `node:<clusterID>:<nodeID>`; `pooler:<poolerID>`; `db:<clusterID>:<database>`.
  - `func Parse(raw string) Scope` — inverse of `Encode`; empty/unrecognized → `Scope{Kind: Fleet}`.

- [ ] **Step 1: Write the failing test**

Create `internal/scope/scope_test.go`:

```go
package scope_test

import (
	"testing"

	"github.com/dobbo-ca/lynceus/internal/scope"
)

func TestEncodeParse_roundTrip(t *testing.T) {
	cases := []scope.Scope{
		{Kind: scope.Cluster, ClusterID: "c-1"},
		{Kind: scope.Node, ClusterID: "c-1", NodeID: "n-1"},
		{Kind: scope.Pooler, PoolerID: "p-1"},
		{Kind: scope.Database, ClusterID: "c-1", Database: "orders"},
		{Kind: scope.Database, ClusterID: "c-1", Database: "weird:name"}, // colon in db name survives
	}
	for _, sc := range cases {
		got := scope.Parse(sc.Encode())
		if got != sc {
			t.Errorf("round-trip %+v -> %q -> %+v", sc, sc.Encode(), got)
		}
	}
}

func TestEncode_fleetIsEmpty(t *testing.T) {
	if enc := (scope.Scope{Kind: scope.Fleet}).Encode(); enc != "" {
		t.Errorf("fleet Encode() = %q, want empty", enc)
	}
	if enc := (scope.Scope{}).Encode(); enc != "" {
		t.Errorf("zero-value Encode() = %q, want empty", enc)
	}
}

func TestParse_emptyAndUnknownAreFleet(t *testing.T) {
	for _, raw := range []string{"", "garbage", "cluster", "db:only-one-part"} {
		if !scope.Parse(raw).IsFleet() {
			t.Errorf("Parse(%q) should be fleet", raw)
		}
	}
}

func TestIsFleet(t *testing.T) {
	if !(scope.Scope{}).IsFleet() {
		t.Error("zero value must be fleet")
	}
	if !(scope.Scope{Kind: scope.Fleet}).IsFleet() {
		t.Error("explicit fleet must be fleet")
	}
	if (scope.Scope{Kind: scope.Cluster, ClusterID: "x"}).IsFleet() {
		t.Error("cluster is not fleet")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/scope/`
Expected: FAIL — build error, `package github.com/dobbo-ca/lynceus/internal/scope is not in std` / `undefined: scope.Scope`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/scope/scope.go`:

```go
// Package scope models the current working scope of the UI shell:
// fleet -> cluster -> node/pooler -> database. A Scope round-trips through
// a single URL-safe "scope" query param (Encode/Parse) so the top-bar
// picker, the row ⌖ buttons, and deep links all agree. Databases are
// identified by cluster + name (the same name in two clusters is a
// different database), so the database form carries the cluster id.
package scope

import "strings"

// Kind is the scope level.
type Kind string

const (
	Fleet    Kind = "fleet"
	Cluster  Kind = "cluster"
	Node     Kind = "node"
	Pooler   Kind = "pooler"
	Database Kind = "database"
)

// Scope is the current working scope. The zero value is Fleet.
type Scope struct {
	Kind      Kind
	ClusterID string // cluster, node, database
	NodeID    string // node (a store Instance id)
	PoolerID  string // pooler (not yet modeled server-side; see ly-99s)
	Database  string // database name (database scope)
}

// IsFleet reports whether this is the fleet (root) scope.
func (s Scope) IsFleet() bool { return s.Kind == "" || s.Kind == Fleet }

// Encode returns the "scope" query-param form. Fleet -> "".
func (s Scope) Encode() string {
	switch s.Kind {
	case Cluster:
		return "cluster:" + s.ClusterID
	case Node:
		return "node:" + s.ClusterID + ":" + s.NodeID
	case Pooler:
		return "pooler:" + s.PoolerID
	case Database:
		return "db:" + s.ClusterID + ":" + s.Database
	default:
		return ""
	}
}

// Parse decodes an Encode() string. Empty or unrecognized input -> Fleet.
func Parse(raw string) Scope {
	if raw == "" {
		return Scope{Kind: Fleet}
	}
	key, val, ok := strings.Cut(raw, ":")
	if !ok {
		return Scope{Kind: Fleet}
	}
	switch key {
	case "cluster":
		return Scope{Kind: Cluster, ClusterID: val}
	case "node":
		cid, nid, ok := strings.Cut(val, ":")
		if !ok {
			return Scope{Kind: Fleet}
		}
		return Scope{Kind: Node, ClusterID: cid, NodeID: nid}
	case "pooler":
		return Scope{Kind: Pooler, PoolerID: val}
	case "db":
		cid, name, ok := strings.Cut(val, ":")
		if !ok {
			return Scope{Kind: Fleet}
		}
		return Scope{Kind: Database, ClusterID: cid, Database: name}
	default:
		return Scope{Kind: Fleet}
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/scope/`
Expected: PASS (`ok  github.com/dobbo-ca/lynceus/internal/scope`).

- [ ] **Step 5: Commit**

```bash
git branch --show-current   # confirm NOT main
git add internal/scope/scope.go internal/scope/scope_test.go
git commit -m "feat(scope): pure scope value model with URL encode/parse (ly-ae6.2)"
```

---

### Task 2: Shell view-models + URL helpers (`web/shell.go`)

Pure Go view-models and href builders that the templ components (Task 3) and handler (Task 4) both consume. No templ, no store — fast unit tests.

**Files:**
- Create: `web/shell.go`
- Test: `web/shell_helpers_test.go`

**Interfaces:**
- Consumes: `scope.Scope`, `scope.Scope.Encode()`, `scope.Scope.IsFleet()` (Task 1); `github.com/a-h/templ` (`templ.SafeURL`, `templ.Component`).
- Produces:
  - `const DefaultRange = "24H"`; `var ValidRanges = []string{"15M", "1H", "24H", "7D", "30D"}`.
  - `func ParseRange(raw string) string` — canonicalizes `?range`; unknown → `DefaultRange`.
  - `func ScopeHref(sc scope.Scope) templ.SafeURL` — `/fleet` for fleet, `/fleet?scope=<encoded>` otherwise.
  - `func RangeOptions(current string, sc scope.Scope) []RangeOption`.
  - `type RangeOption struct { Label string; Selected bool; Href templ.SafeURL }`.
  - `type ScopeOption struct { Label string; Kind string; Depth int; ScopeKey string; Href templ.SafeURL; Current bool }` (`Kind` ∈ `CLUSTER|NODE|POOLER|DATABASE`; `Depth` 0 = cluster, 1 = its children — nodes, poolers, and databases — matching the design's two-level `pad` indent; `ScopeKey` = `scope.Scope.Encode()`).
  - `type ShellUser struct { Name string; Group string; T2Granted bool }`.
  - `type ShellView struct { Scope scope.Scope; ScopeLabel string; Scoped bool; ClearHref templ.SafeURL; LogoHref templ.SafeURL; Range string; Ranges []RangeOption; PollSecs int; Options []ScopeOption; OptionsQuery string; User ShellUser; Sidebar templ.Component; Title string }`.

- [ ] **Step 1: Write the failing test**

Create `web/shell_helpers_test.go`:

```go
package web

import (
	"testing"

	"github.com/dobbo-ca/lynceus/internal/scope"
)

func TestParseRange(t *testing.T) {
	cases := map[string]string{
		"15m": "15M", "1H": "1H", "24h": "24H", "7D": "7D", "30D": "30D",
		"": DefaultRange, "bogus": DefaultRange,
	}
	for in, want := range cases {
		if got := ParseRange(in); got != want {
			t.Errorf("ParseRange(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestScopeHref(t *testing.T) {
	if got := ScopeHref(scope.Scope{Kind: scope.Fleet}); string(got) != "/fleet" {
		t.Errorf("fleet href = %q, want /fleet", got)
	}
	got := string(ScopeHref(scope.Scope{Kind: scope.Cluster, ClusterID: "c-1"}))
	if got != "/fleet?scope=cluster%3Ac-1" {
		t.Errorf("cluster href = %q, want /fleet?scope=cluster%%3Ac-1", got)
	}
}

func TestRangeOptions_selectedAndScopePreserved(t *testing.T) {
	sc := scope.Scope{Kind: scope.Cluster, ClusterID: "c-1"}
	opts := RangeOptions("1h", sc)
	if len(opts) != len(ValidRanges) {
		t.Fatalf("got %d options, want %d", len(opts), len(ValidRanges))
	}
	var sel int
	for _, o := range opts {
		if o.Selected {
			sel++
			if o.Label != "1H" {
				t.Errorf("selected label = %q, want 1H", o.Label)
			}
		}
		if !containsSubstr(string(o.Href), "scope=cluster%3Ac-1") {
			t.Errorf("href %q dropped the active scope", o.Href)
		}
	}
	if sel != 1 {
		t.Errorf("selected count = %d, want 1", sel)
	}
}

func TestRangeOptions_fleetHasNoScopeParam(t *testing.T) {
	for _, o := range RangeOptions("24H", scope.Scope{Kind: scope.Fleet}) {
		if containsSubstr(string(o.Href), "scope=") {
			t.Errorf("fleet range href %q must not carry a scope param", o.Href)
		}
	}
}

func containsSubstr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./web/ -run 'TestParseRange|TestScopeHref|TestRangeOptions'`
Expected: FAIL — `undefined: DefaultRange` / `undefined: ParseRange` / `undefined: ScopeHref`.

- [ ] **Step 3: Write minimal implementation**

Create `web/shell.go`:

```go
package web

import (
	"net/url"
	"strings"

	"github.com/a-h/templ"
	"github.com/dobbo-ca/lynceus/internal/scope"
)

// DefaultRange is used when the ?range param is absent or invalid.
const DefaultRange = "24H"

// ValidRanges is the ordered time-range segmented control (15M/1H/24H/7D/30D).
var ValidRanges = []string{"15M", "1H", "24H", "7D", "30D"}

// ParseRange canonicalizes the ?range param; unknown -> DefaultRange.
func ParseRange(raw string) string {
	up := strings.ToUpper(strings.TrimSpace(raw))
	for _, r := range ValidRanges {
		if up == r {
			return r
		}
	}
	return DefaultRange
}

// ScopeHref is the shell landing URL that sets the given scope: /fleet for
// fleet, /fleet?scope=<encoded> otherwise. This is the canonical scope-set URL
// shared by the picker, the ⌖ row buttons, and deep links. ly-ae6.6 will
// repoint these targets to the per-scope Overview routes; keep the encoding
// stable so every producer agrees.
func ScopeHref(sc scope.Scope) templ.SafeURL {
	if sc.IsFleet() {
		return templ.SafeURL("/fleet")
	}
	v := url.Values{"scope": {sc.Encode()}}
	return templ.SafeURL("/fleet?" + v.Encode())
}

// RangeOption is one segmented-control entry.
type RangeOption struct {
	Label    string
	Selected bool
	Href     templ.SafeURL
}

// RangeOptions builds the five range entries, preserving the active scope on
// each href and marking the selected one.
//
// NOTE (ly-ae6.6): like ScopeHref, this hardcodes the /fleet base path. When
// ly-ae6.6 adds per-scope Overview routes it MUST repoint this alongside
// ScopeHref (share one base-path resolver), or changing the range on a scoped
// screen will bounce the user back to /fleet and drop their page context.
func RangeOptions(current string, sc scope.Scope) []RangeOption {
	current = ParseRange(current)
	out := make([]RangeOption, 0, len(ValidRanges))
	for _, r := range ValidRanges {
		v := url.Values{"range": {r}}
		if !sc.IsFleet() {
			v.Set("scope", sc.Encode())
		}
		out = append(out, RangeOption{
			Label:    r,
			Selected: r == current,
			Href:     templ.SafeURL("/fleet?" + v.Encode()),
		})
	}
	return out
}

// ScopeOption is one row in the searchable SCOPE picker. Kind is
// CLUSTER|NODE|POOLER|DATABASE; Depth drives the indent — 0 for a cluster, 1
// for its children (nodes, poolers, and databases are all cluster-level, so
// they share the single indent, matching the design's flat pad-0/pad-1 layout);
// ScopeKey is scope.Scope.Encode() (used to mark the active option Current).
type ScopeOption struct {
	Label    string
	Kind     string
	Depth    int
	ScopeKey string
	Href     templ.SafeURL
	Current  bool
}

// ShellUser is the identity shown in the user menu. Until OIDC lands
// (ly-8b0.1) the handler supplies a static dev identity.
type ShellUser struct {
	Name      string
	Group     string
	T2Granted bool
}

// ShellView is the top-bar + shell view-model. Sidebar is the per-scope nav
// component supplied by ly-ae6.3; when nil the shell renders a placeholder.
type ShellView struct {
	Scope        scope.Scope
	ScopeLabel   string // "FLEET" or the resolved entity label
	Scoped       bool   // !Scope.IsFleet()
	ClearHref    templ.SafeURL
	LogoHref     templ.SafeURL
	Range        string
	Ranges       []RangeOption
	PollSecs     int
	Options      []ScopeOption
	OptionsQuery string
	User         ShellUser
	Sidebar      templ.Component
	Title        string
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./web/ -run 'TestParseRange|TestScopeHref|TestRangeOptions'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/shell.go web/shell_helpers_test.go
git commit -m "feat(web): shell view-models + range/scope URL helpers (ly-ae6.2)"
```

---

### Task 3: Top-bar shell templ components + CSS + JS (`web/shell.templ`)

The design-parity chrome: `Shell` full-page layout, `TopBar` (logo→Fleet, `← FLEET`, SCOPE picker, range control, poll indicator, theme toggle, user menu), the swappable `ScopeOptionsList`, and the reusable `ScopeButton` (`⌖`). Token styling in a new `shell.css`; a tiny `shell.js` for the theme glyph + poll ticker. Uses only tokens, never legacy.

**Files:**
- Create: `web/shell.templ` (and generated `web/shell_templ.go` via `make templ`)
- Create: `web/static/css/shell.css`
- Create: `web/static/js/shell.js`
- Test: `web/shell_test.go`

**Interfaces:**
- Consumes: `ShellView`, `ScopeOption`, `RangeOption`, `ShellUser`, `ScopeHref` (Task 2); `scope.Scope` (Task 1); `themeBootstrapTag()` (existing, `web/bootstrap.go`); tokens + `@keyframes pulse` (ly-ae6.1).
- Produces (templ components callable by handlers / other screens):
  - `templ Shell(vm ShellView) { children... }` — full HTML document; renders `TopBar`, a flex body with the sidebar slot + `{ children... }` as main.
  - `templ ScopeOptionsList(opts []ScopeOption, q string)` — the `<ul id="scope-options">` HTMX swap target.
  - `templ ScopeButton(sc scope.Scope)` — a 24px `⌖` anchor to `ScopeHref(sc)`, for other screens' inert rows.

- [ ] **Step 1: Write the failing test**

Create `web/shell_test.go`:

```go
package web

import (
	"context"
	"strings"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/scope"
)

func fleetShellView() ShellView {
	sc := scope.Scope{Kind: scope.Fleet}
	return ShellView{
		Scope:      sc,
		ScopeLabel: "FLEET",
		Scoped:     false,
		ClearHref:  "/fleet",
		LogoHref:   "/fleet",
		Range:      DefaultRange,
		Ranges:     RangeOptions(DefaultRange, sc),
		PollSecs:   3,
		Options: []ScopeOption{
			{Label: "orders-prod", Kind: "CLUSTER", Depth: 0, ScopeKey: "cluster:c-1", Href: "/fleet?scope=cluster%3Ac-1"},
		},
		User:  ShellUser{Name: "dev-admin", Group: "DBA-ONCALL", T2Granted: true},
		Title: "Lynceus — Fleet",
	}
}

func renderShell(t *testing.T, vm ShellView) string {
	t.Helper()
	var sb strings.Builder
	if err := Shell(vm).Render(context.Background(), &sb); err != nil {
		t.Fatalf("render shell: %v", err)
	}
	return sb.String()
}

func TestShell_NoExternalHosts(t *testing.T) {
	html := renderShell(t, fleetShellView())
	for _, host := range []string{"unpkg.com", "googleapis.com", "gstatic.com", "cdn.jsdelivr.net"} {
		if strings.Contains(html, host) {
			t.Errorf("shell references external host %q — assets must be self-hosted", host)
		}
	}
}

func TestShell_SelfHostedTokenAssetsNoLegacy(t *testing.T) {
	html := renderShell(t, fleetShellView())
	for _, want := range []string{
		`href="/static/css/tokens.css"`,
		`href="/static/css/shell.css"`,
		`src="/static/js/htmx.min.js"`,
		`src="/static/js/theme.js"`,
		`src="/static/js/shell.js"`,
		`data-theme="dark"`,
		"window.Lynceus",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("shell missing %q", want)
		}
	}
	if strings.Contains(html, "legacy.css") {
		t.Error("shell must NOT link legacy.css — new screens are token-based")
	}
}

func TestShell_TopBarChrome(t *testing.T) {
	html := renderShell(t, fleetShellView())
	for _, want := range []string{
		"LYNCEUS",              // wordmark
		"SCOPE:",               // picker button
		"FLEET",                // fleet chip label
		"15M", "1H", "24H", "7D", "30D", // range control
		"POLL",                 // poll indicator
		"id=\"theme-toggle\"",  // theme toggle
		"Audit Log",            // user menu governance item (live route)
		"GROUP: DBA-ONCALL",    // identity header
	} {
		if !strings.Contains(html, want) {
			t.Errorf("top bar missing %q", want)
		}
	}
	// Fleet scope: no ← FLEET reset present.
	if strings.Contains(html, "← FLEET") {
		t.Error("fleet scope must not show the ← FLEET reset")
	}
}

func TestShell_ScopedShowsResetAndAccentChip(t *testing.T) {
	sc := scope.Scope{Kind: scope.Cluster, ClusterID: "c-1"}
	vm := fleetShellView()
	vm.Scope, vm.Scoped, vm.ScopeLabel = sc, true, "orders-prod"
	vm.Ranges = RangeOptions(vm.Range, sc)
	html := renderShell(t, vm)
	if !strings.Contains(html, "← FLEET") {
		t.Error("scoped shell must show the ← FLEET reset")
	}
	if !strings.Contains(html, `data-scoped`) {
		t.Error("scoped chip must carry data-scoped for the accent style")
	}
	if !strings.Contains(html, "orders-prod") {
		t.Error("scoped chip must show the resolved scope label")
	}
}

func TestScopeOptionsList_rendersKindBadges(t *testing.T) {
	var sb strings.Builder
	opts := []ScopeOption{
		{Label: "orders-prod", Kind: "CLUSTER", Depth: 0, ScopeKey: "cluster:c-1", Href: "/fleet?scope=cluster%3Ac-1"},
		{Label: "orders-prod / node-1", Kind: "NODE", Depth: 1, ScopeKey: "node:c-1:n-1", Href: "/fleet?scope=node%3Ac-1%3An-1"},
		{Label: "orders-prod/orders", Kind: "DATABASE", Depth: 1, ScopeKey: "db:c-1:orders", Href: "/fleet?scope=db%3Ac-1%3Aorders"}, // databases are cluster-level (Depth 1), not nested under a node
	}
	if err := ScopeOptionsList(opts, "orders").Render(context.Background(), &sb); err != nil {
		t.Fatalf("render: %v", err)
	}
	html := sb.String()
	for _, want := range []string{`id="scope-options"`, "orders-prod / node-1", "CLUSTER", "NODE", "DATABASE", "orders-prod/orders"} {
		if !strings.Contains(html, want) {
			t.Errorf("options list missing %q", want)
		}
	}
}

func TestScopeOptionsList_emptyState(t *testing.T) {
	var sb strings.Builder
	if err := ScopeOptionsList(nil, "zzz").Render(context.Background(), &sb); err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(sb.String(), "NO COMPONENTS MATCH") {
		t.Error("empty options must render the NO COMPONENTS MATCH state")
	}
}

func TestScopeButton_linksToScopeHref(t *testing.T) {
	var sb strings.Builder
	sc := scope.Scope{Kind: scope.Node, ClusterID: "c-1", NodeID: "n-1"}
	if err := ScopeButton(sc).Render(context.Background(), &sb); err != nil {
		t.Fatalf("render: %v", err)
	}
	html := sb.String()
	if !strings.Contains(html, `href="/fleet?scope=node%3Ac-1%3An-1"`) {
		t.Errorf("scope button href wrong: %s", html)
	}
	if !strings.Contains(html, "⌖") {
		t.Error("scope button must render the ⌖ crosshair glyph")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./web/ -run 'TestShell|TestScopeOptionsList|TestScopeButton'`
Expected: FAIL — `undefined: Shell` / `undefined: ScopeOptionsList` / `undefined: ScopeButton`.

- [ ] **Step 3a: Write the CSS**

Create `web/static/css/shell.css`:

```css
/* Shell chrome (ly-ae6.2): 48px top bar + body layout. Tokens only — never
   legacy classes. Dropdowns use the only permitted shadow (--shadow-pop). */

.shell { display: flex; flex-direction: column; height: 100vh; }

.topbar {
  height: 48px; flex-shrink: 0; display: flex; align-items: center;
  gap: 14px; padding: 0 16px; position: relative; z-index: 30;
  border-bottom: var(--border) solid var(--line);
  font-family: var(--font-mono);
}

.topbar-logo { display: flex; align-items: center; gap: 8px; cursor: pointer; color: var(--text); }
.logo-mark {
  width: 20px; height: 20px; border: 1.5px solid var(--acc); border-radius: var(--radius);
  display: flex; align-items: center; justify-content: center;
  color: var(--acc); font-weight: 600; font-size: 11px;
}
.wordmark { font-weight: 600; font-size: 13px; letter-spacing: .04em; }

.fleet-reset {
  display: flex; align-items: center; gap: 6px; padding: 5px 10px;
  border: var(--border) solid var(--line); border-radius: var(--radius);
  font-size: 11px; color: var(--dim); cursor: pointer; user-select: none;
}
.fleet-reset:hover { color: var(--text); border-color: var(--dim); }

.scope { position: relative; }
.scope-chip {
  list-style: none; display: flex; align-items: center; gap: 8px;
  padding: 5px 10px; border: var(--border) solid var(--line);
  border-radius: var(--radius); font-size: 11.5px; cursor: pointer; user-select: none;
}
.scope-chip::-webkit-details-marker { display: none; }
.scope-chip .lbl { color: var(--dim); }
.scope-chip .val { color: var(--text); }
.scope[open] .scope-chip, .scope-chip[data-scoped] { border-color: var(--acc); }
.scope-chip[data-scoped] .val { color: var(--acc); }

.scope-panel {
  position: absolute; top: 34px; left: 0; min-width: 300px;
  background: var(--surface); border: var(--border) solid var(--line);
  border-radius: var(--radius); box-shadow: var(--shadow-pop); z-index: 40;
}
.scope-search-wrap { padding: 8px; border-bottom: var(--border) solid var(--line); }
.scope-search {
  width: 100%; box-sizing: border-box; background: var(--raised);
  border: var(--border) solid var(--line); border-radius: var(--radius);
  color: var(--text); font-family: var(--font-mono); font-size: 11px; padding: 6px 9px;
}
.scope-options { list-style: none; margin: 0; padding: 0; max-height: 320px; overflow-y: auto; }
.scope-opt {
  display: flex; align-items: center; gap: 12px; padding: 6px 12px;
  font-size: 11.5px; color: var(--text); text-decoration: none;
  border-bottom: var(--border) solid var(--line2);
}
.scope-opt[data-depth="1"] { padding-left: 26px; } /* cluster children: nodes, poolers, databases (design pad 1) */
.scope-opt:hover { background: var(--raised); }
.scope-opt[data-current] { background: var(--accbg); color: var(--acc); }
.scope-opt .lbl { flex: 1; }
.kind-badge {
  font-size: 9px; letter-spacing: .06em; color: var(--dim);
  border: var(--border) solid var(--line); border-radius: var(--radius-badge); padding: 0 5px;
}
.scope-empty { padding: 14px 12px; font-size: 10.5px; color: var(--faint); text-align: center; }

.range { display: flex; border: var(--border) solid var(--line); border-radius: var(--radius); overflow: hidden; font-size: 11px; }
.range-opt { padding: 5px 9px; color: var(--dim); text-decoration: none; border-right: var(--border) solid var(--line); }
.range-opt:last-child { border-right: 0; }
.range-opt:hover { color: var(--text); }
.range-opt.sel { color: var(--acc); background: var(--accbg); }

.spacer { flex: 1; }

.poll { font-size: 11px; color: var(--dim); white-space: nowrap; }
.poll-dot { color: var(--acc); animation: pulse 2s infinite; }

.icon-btn {
  width: 26px; height: 26px; border: var(--border) solid var(--line); border-radius: var(--radius);
  display: flex; align-items: center; justify-content: center;
  color: var(--dim); font-size: 12px; cursor: pointer; user-select: none; background: none;
}
.icon-btn:hover { color: var(--text); border-color: var(--dim); }

.user { position: relative; }
.user-chip { list-style: none; }
.user-chip::-webkit-details-marker { display: none; }
.user[data-t2] .user-chip { border-color: var(--acc); }
.user-panel {
  position: absolute; top: 34px; right: 0; width: 236px;
  background: var(--surface); border: var(--border) solid var(--line);
  border-radius: var(--radius); box-shadow: var(--shadow-pop); z-index: 40;
}
.user-id { padding: 10px 12px; border-bottom: var(--border) solid var(--line); display: flex; flex-direction: column; gap: 3px; }
.user-id .name { font-size: 12px; font-weight: 600; color: var(--text); }
.user-id .meta { font-size: 9.5px; color: var(--dim); letter-spacing: .06em; }
.menu-head { padding: 8px 12px 3px; font-size: 9.5px; letter-spacing: .12em; color: var(--faint); }
.menu-item {
  display: flex; justify-content: space-between; align-items: center; gap: 8px;
  padding: 6px 12px; font-size: 11.5px; color: var(--mut); text-decoration: none;
}
.menu-item:hover { background: var(--raised); color: var(--text); }
.menu-signout { border-top: var(--border) solid var(--line); margin-top: 5px; padding: 7px 12px 9px; }
.menu-signout .lbl { font-size: 11.5px; color: var(--dim); cursor: pointer; }
.menu-signout .lbl:hover { color: var(--critT); }
.badge-soon { font-size: 9px; border: var(--border) solid var(--line); border-radius: var(--radius-badge); padding: 0 4px; color: var(--faint); }

.shell-body { display: flex; flex: 1; min-height: 0; }
.sidebar {
  width: 208px; flex-shrink: 0; overflow-y: auto;
  border-right: var(--border) solid var(--line); background: var(--rail);
  padding: 12px 0 20px; font-family: var(--font-mono); font-size: 12px; color: var(--dim);
}
.sidebar-placeholder { padding: 12px 14px; font-size: 10px; letter-spacing: .12em; color: var(--faint); }
.main { flex: 1; overflow-y: auto; min-width: 0; }

.scope-btn {
  width: 24px; height: 24px; flex-shrink: 0; border: var(--border) solid var(--line);
  border-radius: var(--radius); display: inline-flex; align-items: center; justify-content: center;
  color: var(--dim); font-size: 12px; text-decoration: none;
}
.scope-btn:hover { color: var(--acc); border-color: var(--acc); }
```

- [ ] **Step 3b: Write the JS**

Create `web/static/js/shell.js`:

```javascript
// Shell interactivity (ly-ae6.2): theme-toggle glyph + poll ticker. Depends on
// window.Lynceus from the head bootstrap / theme.js. Self-contained, no external
// references (privacy backbone).
//
// COSMETIC-ONLY: the "UPDATED Ns AGO" ticker below is a visual placeholder — it
// counts 0..PollSecs on a local 1s interval and is NOT wired to any real data
// refresh (this chrome-only bead renders no live data). It becomes real when the
// data screens (ly-ae6.4 fleet dashboard onward) add HTMX polling on their body;
// at that point the ticker should reset on each successful poll (e.g. an
// htmx:afterSwap listener) rather than free-run. See the plan's backend-gaps note.
(function () {
  var doc = document.documentElement;
  function glyph() { return doc.dataset.theme === 'light' ? '☀' : '☾'; } // ☀ / ☾

  var toggle = document.getElementById('theme-toggle');
  if (toggle) {
    toggle.textContent = glyph();
    toggle.addEventListener('click', function () {
      if (window.Lynceus && window.Lynceus.cycleTheme) window.Lynceus.cycleTheme();
      toggle.textContent = glyph();
    });
  }

  var ago = document.querySelector('[data-updated-ago]');
  var host = document.querySelector('[data-poll-secs]');
  if (ago && host) {
    var span = parseInt(host.getAttribute('data-poll-secs'), 10) || 3;
    var n = 0;
    setInterval(function () {
      n = (n + 1) % (span + 1);
      ago.textContent = 'UPDATED ' + n + 'S AGO';
    }, 1000);
  }
})();
```

- [ ] **Step 3c: Write the templ components**

Create `web/shell.templ`:

```go
package web

import (
	"fmt"

	"github.com/dobbo-ca/lynceus/internal/scope"
)

// Shell is the design-parity full-page layout: 48px top bar + a body flex of
// the per-scope sidebar (ly-ae6.3 slot) and the main content ({ children... }).
// Token-based; never links legacy.css.
templ Shell(vm ShellView) {
	<!DOCTYPE html>
	<html lang="en" data-theme="dark">
		<head>
			<meta charset="UTF-8"/>
			<meta name="viewport" content="width=device-width, initial-scale=1"/>
			<title>{ vm.Title }</title>
			@templ.Raw(themeBootstrapTag())
			<link rel="stylesheet" href="/static/css/tokens.css"/>
			<link rel="stylesheet" href="/static/css/shell.css"/>
			<script src="/static/js/htmx.min.js" defer></script>
			<script src="/static/js/theme.js" defer></script>
			<script src="/static/js/shell.js" defer></script>
		</head>
		<body>
			<div class="shell">
				@TopBar(vm)
				<div class="shell-body">
					if vm.Sidebar != nil {
						@vm.Sidebar
					} else {
						@placeholderSidebar()
					}
					<main class="main">
						{ children... }
					</main>
				</div>
			</div>
		</body>
	</html>
}

// TopBar renders the 48px bar: logo→Fleet, ← FLEET reset (scoped only),
// SCOPE picker, range control, poll indicator, theme toggle, user menu.
templ TopBar(vm ShellView) {
	<div class="topbar">
		<a class="topbar-logo" href={ vm.LogoHref }>
			<span class="logo-mark">L</span>
			<span class="wordmark">LYNCEUS</span>
		</a>
		if vm.Scoped {
			<a class="fleet-reset" href={ vm.ClearHref } title="Back to fleet">← FLEET</a>
		}
		@scopePicker(vm)
		@rangeControl(vm)
		<span class="spacer"></span>
		@pollIndicator(vm)
		<button type="button" id="theme-toggle" class="icon-btn" title="Toggle theme" aria-label="Toggle theme">☾</button>
		@userMenu(vm)
	</div>
}

// scopePicker is a native <details> dropdown: a chip summary opening a search
// box (HTMX-filtered) over the pre-rendered option list. Degrades without JS —
// the full list is already server-rendered inside the panel.
//
// The search placeholder reads "search cluster / node / database…" — a conscious
// divergence from the prototype's "search cluster / instance / pooler…"
// (Lynceus.dc.html:120): it names only the entities actually enumerated today
// (poolers are not modeled yet; "instance" == our NODE). See backend-gaps note.
templ scopePicker(vm ShellView) {
	<details class="scope">
		<summary class="scope-chip" data-scoped?={ vm.Scoped }>
			<span class="lbl">SCOPE:</span>
			<span class="val">{ vm.ScopeLabel }</span>
			<span class="lbl">⌄</span>
		</summary>
		<div class="scope-panel">
			<div class="scope-search-wrap">
				<input
					class="scope-search"
					type="search"
					name="q"
					value={ vm.OptionsQuery }
					placeholder="search cluster / node / database…"
					autocomplete="off"
					hx-get="/partial/scope-options"
					hx-target="#scope-options"
					hx-swap="outerHTML"
					hx-trigger="keyup changed delay:200ms, search"
					hx-include="[name='scope-active']"
				/>
				<input type="hidden" name="scope-active" value={ vm.Scope.Encode() }/>
			</div>
			@ScopeOptionsList(vm.Options, vm.OptionsQuery)
		</div>
	</details>
}

// ScopeOptionsList is the swappable <ul> of picker options (HTMX target).
templ ScopeOptionsList(opts []ScopeOption, q string) {
	<ul id="scope-options" class="scope-options">
		if len(opts) == 0 {
			<li class="scope-empty">NO COMPONENTS MATCH</li>
		} else {
			for _, o := range opts {
				<li>
					<a
						class="scope-opt"
						href={ o.Href }
						data-depth={ fmt.Sprintf("%d", o.Depth) }
						data-current?={ o.Current }
					>
						<span class="lbl">{ o.Label }</span>
						<span class="kind-badge">{ o.Kind }</span>
					</a>
				</li>
			}
		}
	</ul>
}

// rangeControl renders the 15M/1H/24H/7D/30D segmented control as scope-
// preserving links; the selected one carries .sel.
templ rangeControl(vm ShellView) {
	<nav class="range" aria-label="Time range">
		for _, r := range vm.Ranges {
			<a class={ "range-opt", templ.KV("sel", r.Selected) } href={ r.Href }>{ r.Label }</a>
		}
	</nav>
}

// pollIndicator shows the pulsing dot + cadence; shell.js ticks the "UPDATED
// Ns AGO" text. NOTE: the tick is cosmetic in this bead (no live data refresh
// exists yet) — it becomes a real freshness readout when the data screens add
// polling (ly-ae6.4+). See the plan's backend-gaps note.
templ pollIndicator(vm ShellView) {
	<span class="poll" data-poll-secs={ fmt.Sprintf("%d", vm.PollSecs) }>
		<span class="poll-dot">●</span>
		{ fmt.Sprintf(" POLL %dS · ", vm.PollSecs) }
		<span data-updated-ago>UPDATED 0S AGO</span>
	</span>
}

// userMenu is a native <details> dropdown: identity header, GOVERNANCE and
// ADMIN sections, Sign out. Audit Log links to the live /audit route; the rest
// are SOON until their beads land (ly-ae6.13 governance, ly-8b0.1 auth).
templ userMenu(vm ShellView) {
	<details class="user" data-t2?={ vm.User.T2Granted }>
		<summary class="icon-btn user-chip" title="Account">{ userInitials(vm.User.Name) }</summary>
		<div class="user-panel">
			<div class="user-id">
				<span class="name">{ vm.User.Name }</span>
				<span class="meta">{ userMeta(vm.User) }</span>
			</div>
			<div class="menu-head">GOVERNANCE</div>
			<a class="menu-item" href="/audit"><span>Audit Log</span></a>
			<a class="menu-item" href="#"><span>Access &amp; Roles</span><span class="badge-soon">SOON</span></a>
			<div class="menu-head" style="border-top: var(--border) solid var(--line2); margin-top: 5px;">ADMIN</div>
			<a class="menu-item" href="#"><span>Provider Setup</span><span class="badge-soon">SOON</span></a>
			<a class="menu-item" href="#"><span>Collectors</span><span class="badge-soon">SOON</span></a>
			<a class="menu-item" href="#"><span>Data &amp; Retention</span><span class="badge-soon">SOON</span></a>
			<a class="menu-item" href="#"><span>Settings</span><span class="badge-soon">SOON</span></a>
			<div class="menu-signout"><span class="lbl">Sign out</span></div>
		</div>
	</details>
}

// placeholderSidebar is the ly-ae6.3 slot filler until the per-scope nav lands.
templ placeholderSidebar() {
	<aside class="sidebar">
		<div class="sidebar-placeholder">NAV · ly-ae6.3</div>
	</aside>
}

// ScopeButton is the reusable ⌖ crosshair: a 24px icon anchor that sets scope
// to sc. Other screens (ly-ae6.4/.5/.6) place it on otherwise-inert rows.
templ ScopeButton(sc scope.Scope) {
	<a class="scope-btn" href={ ScopeHref(sc) } title="Set scope" aria-label="Set scope to this component">⌖</a>
}
```

- [ ] **Step 3d: Add the templ helper functions**

Append to `web/shell.go`:

```go
// userInitials returns up to two uppercase initials from a username like
// "s.dobson" -> "SD" for the user chip.
func userInitials(name string) string {
	parts := strings.FieldsFunc(name, func(r rune) bool { return r == '.' || r == ' ' || r == '_' || r == '-' })
	var out []rune
	for _, p := range parts {
		if p == "" {
			continue
		}
		out = append(out, []rune(strings.ToUpper(p))[0])
		if len(out) == 2 {
			break
		}
	}
	if len(out) == 0 {
		return "?"
	}
	return string(out)
}

// userMeta is the identity sub-line: "GROUP: <group> · T2 GRANTED|T2 OFF".
func userMeta(u ShellUser) string {
	t2 := "T2 OFF"
	if u.T2Granted {
		t2 = "T2 GRANTED"
	}
	return "GROUP: " + strings.ToUpper(u.Group) + " · " + t2
}
```

- [ ] **Step 4a: Generate templ**

Run: `make templ`
Expected: PASS — `web/shell_templ.go` is (re)generated; `git status` shows it created.

- [ ] **Step 4b: Run the tests**

Run: `go test ./web/ -run 'TestShell|TestScopeOptionsList|TestScopeButton'`
Expected: PASS. Then run the full package: `go test ./web/` → PASS (existing `TestLayout_*` untouched).

- [ ] **Step 5: Commit**

```bash
git add web/shell.templ web/shell_templ.go web/shell.go web/shell_test.go \
        web/static/css/shell.css web/static/js/shell.js
git commit -m "feat(web): top-bar shell, SCOPE picker, range/poll/theme/user chrome, ⌖ button (ly-ae6.2)"
```

---

### Task 4: Scope-options enumeration + `/fleet` + `/partial/scope-options` handlers (`internal/api/shell.go`)

Wire the shell to a live route: parse scope+range from the request, enumerate scopeable entities from the config store, resolve the active scope's label, assemble `ShellView`, and serve the searchable-options partial. Register both routes.

**Files:**
- Create: `internal/api/shell.go`
- Modify: `internal/api/server.go:49-83` (add two routes in `routes()`)
- Create: `web/fleet.templ` (and generated `web/fleet_templ.go` via `make templ`) — the `FleetShellPage` wrapper + placeholder main
- Test: `internal/api/shell_test.go`

**Interfaces:**
- Consumes: `store.Config` methods `ListClusters(ctx) ([]store.Cluster, error)`, `ListInstances(ctx, clusterID string) ([]store.Instance, error)`, `ListServerStreams(ctx, instanceID string) ([]store.ServerStream, error)` (existing, `internal/store/config.go:22-24`); `store.Cluster{ID,Name}`, `store.Instance{ID,Name}`, `store.ServerStream{DatabaseName}` (existing, `internal/store/fleet.go`); `scope.Scope`, `scope.Parse`, `scope.Scope.Encode` (Task 1); `web.ShellView`, `web.ScopeOption`, `web.ScopeHref`, `web.ParseRange`, `web.RangeOptions`, `web.ShellUser` (Task 2); `web.Shell`, `web.ScopeOptionsList` (Task 3).
- Produces:
  - `web.FleetShellPage(vm web.ShellView)` templ (Task-4 templ file) — `Shell` wrapping the placeholder main.
  - `(*Server).handleFleet(w http.ResponseWriter, r *http.Request)` — `GET /fleet`.
  - `(*Server).handleScopeOptions(w http.ResponseWriter, r *http.Request)` — `GET /partial/scope-options`.
  - `(*Server).buildShellView(r *http.Request) web.ShellView` and `(*Server).scopeOptions(ctx context.Context, q string, active scope.Scope) []web.ScopeOption`.

- [ ] **Step 1: Write the failing test**

Create `internal/api/shell_test.go`:

```go
package api_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dobbo-ca/lynceus/internal/api"
	"github.com/dobbo-ca/lynceus/internal/scope"
	"github.com/dobbo-ca/lynceus/internal/store"
)

// seedTopology creates one cluster, one node, and one server stream carrying a
// database name, returning the cluster and instance for URL construction.
func seedTopology(t *testing.T, pool *pgxpool.Pool) (store.Cluster, store.Instance) {
	t.Helper()
	ctx := t.Context()
	cfg := store.NewConfig(pool)
	cl, err := cfg.CreateCluster(ctx, "orders-prod")
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}
	in, err := cfg.CreateInstance(ctx, cl.ID, "node-1")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO servers (id, name, database_name) VALUES ($1, $1, $2)`, "srv-a", "orders"); err != nil {
		t.Fatalf("seed server: %v", err)
	}
	if err := cfg.AssignServerToInstance(ctx, "srv-a", in.ID); err != nil {
		t.Fatalf("assign: %v", err)
	}
	return cl, in
}

func body(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

func TestFleet_devAuth_rendersTopBar(t *testing.T) {
	_, srv := setupAudit(t, api.Config{DevAuth: true})
	resp, err := http.Get(srv.URL + "/fleet")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	html := body(t, resp)
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	for _, want := range []string{"LYNCEUS", "SCOPE:", "FLEET", "POLL", "id=\"theme-toggle\"", "/static/css/shell.css"} {
		if !strings.Contains(html, want) {
			t.Errorf("/fleet missing %q", want)
		}
	}
}

func TestFleet_withoutDevAuth_401(t *testing.T) {
	_, srv := setupAudit(t, api.Config{DevAuth: false})
	resp, err := http.Get(srv.URL + "/fleet")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestScopeOptions_searchMatchesTopology(t *testing.T) {
	pool, srv := setupAudit(t, api.Config{DevAuth: true})
	seedTopology(t, pool)

	resp, err := http.Get(srv.URL + "/partial/scope-options?q=orders")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	html := body(t, resp)
	for _, want := range []string{
		`id="scope-options"`,
		"orders-prod",          // CLUSTER
		"orders-prod / node-1", // NODE
		"orders-prod/orders",   // DATABASE (cluster-qualified)
		"CLUSTER", "NODE", "DATABASE",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("options missing %q; body=%s", want, html)
		}
	}
}

func TestScopeOptions_noMatchEmptyState(t *testing.T) {
	pool, srv := setupAudit(t, api.Config{DevAuth: true})
	seedTopology(t, pool)
	resp, err := http.Get(srv.URL + "/partial/scope-options?q=zzzzz")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if !strings.Contains(body(t, resp), "NO COMPONENTS MATCH") {
		t.Error("expected empty-state marker")
	}
}

func TestFleet_clusterScope_resolvesLabelAndReset(t *testing.T) {
	pool, srv := setupAudit(t, api.Config{DevAuth: true})
	cl, _ := seedTopology(t, pool)
	enc := scope.Scope{Kind: scope.Cluster, ClusterID: cl.ID}.Encode()

	resp, err := http.Get(srv.URL + "/fleet?scope=" + enc)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	html := body(t, resp)
	if !strings.Contains(html, "orders-prod") {
		t.Error("scoped chip must show the resolved cluster label")
	}
	if !strings.Contains(html, "← FLEET") {
		t.Error("scoped shell must show the ← FLEET reset")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/api/ -run 'TestFleet|TestScopeOptions'`
Expected: FAIL — routes 404 (no `/fleet` / `/partial/scope-options`), and build errors for `undefined: (*Server).handleFleet` once referenced. (Initially the GETs return 404, so the assertions fail.)

- [ ] **Step 3a: Write the fleet page templ**

Create `web/fleet.templ`:

```go
package web

// FleetShellPage wraps the Shell around the fleet/scoped main body. The body is
// a minimal, clearly-marked seam: ly-ae6.4 replaces it with the real Fleet
// dashboard, and ly-ae6.6 supplies the scoped "OPEN ISSUES ON THIS …" overview.
templ FleetShellPage(vm ShellView) {
	@Shell(vm) {
		@shellPlaceholderMain(vm)
	}
}

// shellPlaceholderMain renders the current scope so the shell is exercisable
// end-to-end before the dashboard body exists.
templ shellPlaceholderMain(vm ShellView) {
	<div id="fleet-main" style="padding: 18px 22px; font-family: var(--font-mono); color: var(--dim); display: flex; flex-direction: column; gap: 8px;">
		<div style="font-size: 15px; color: var(--text);">{ vm.ScopeLabel }</div>
		<div style="font-size: 10.5px; letter-spacing: .08em; color: var(--faint);">
			{ scopeKindLabel(vm.Scope) } · RANGE { vm.Range }
		</div>
		<div style="font-size: 11px; color: var(--dim);">
			Dashboard body arrives with ly-ae6.4 (fleet) / ly-ae6.6 (scoped overview).
		</div>
	</div>
}
```

- [ ] **Step 3b: Add the `scopeKindLabel` helper**

Append to `web/shell.go`:

```go
// scopeKindLabel is the human label for the current scope kind, shown in the
// placeholder main until the real bodies land.
func scopeKindLabel(sc scope.Scope) string {
	switch sc.Kind {
	case scope.Cluster:
		return "CLUSTER SCOPE"
	case scope.Node:
		return "NODE SCOPE"
	case scope.Pooler:
		return "POOLER SCOPE"
	case scope.Database:
		return "DATABASE SCOPE"
	default:
		return "FLEET"
	}
}
```

Ensure `web/shell.go` imports `github.com/dobbo-ca/lynceus/internal/scope` (already imported from Task 2).

- [ ] **Step 3c: Write the handler**

Create `internal/api/shell.go`:

```go
package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/a-h/templ"

	"github.com/dobbo-ca/lynceus/internal/scope"
	"github.com/dobbo-ca/lynceus/web"
)

// handleFleet serves the /fleet landing wrapped in the design shell. A ?scope
// param scopes the top bar (the main body is a placeholder until ly-ae6.4/.6).
func (s *Server) handleFleet(w http.ResponseWriter, r *http.Request) {
	vm := s.buildShellView(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.FleetShellPage(vm).Render(r.Context(), w)
}

// handleScopeOptions serves the searchable SCOPE picker option list (HTMX).
func (s *Server) handleScopeOptions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	active := scope.Parse(r.URL.Query().Get("scope-active"))
	opts := s.scopeOptions(r.Context(), q, active)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.ScopeOptionsList(opts, q).Render(r.Context(), w)
}

// buildShellView parses scope + range from the request, enumerates the picker
// options, resolves the active scope's display label, and assembles ShellView.
func (s *Server) buildShellView(r *http.Request) web.ShellView {
	qv := r.URL.Query()
	active := scope.Parse(qv.Get("scope"))
	rng := web.ParseRange(qv.Get("range"))
	opts := s.scopeOptions(r.Context(), "", active)

	label := "FLEET"
	if !active.IsFleet() {
		label = resolveScopeLabel(opts, active)
	}
	return web.ShellView{
		Scope:      active,
		ScopeLabel: label,
		Scoped:     !active.IsFleet(),
		ClearHref:  templ.SafeURL("/fleet"),
		LogoHref:   templ.SafeURL("/fleet"),
		Range:      rng,
		Ranges:     web.RangeOptions(rng, active),
		PollSecs:   3,
		Options:    opts,
		// Static dev identity until OIDC (ly-8b0.1); dev-only, T1.
		User:  web.ShellUser{Name: "dev-admin", Group: "DBA-ONCALL", T2Granted: true},
		Title: "Lynceus — " + label,
	}
}

// scopeOptions enumerates scopeable entities from the config store: clusters
// (Depth 0), then each cluster's nodes and cluster-qualified databases (both
// Depth 1). A database is identified by cluster + name, so it is a CLUSTER-level
// entity, not a node child: distinct database names are collected across all of
// the cluster's instances and emitted ONCE under the cluster, after its nodes —
// matching the design's flat `pad: 1` placement (see docs/design/Lynceus.dc.html
// `scopes` array and README "Scope Model"). Consequence, by design: a database
// streamed by both a primary and a replica appears exactly once under its
// cluster (not indented beneath whichever node happened to report it first).
// Filtered case-insensitively over label + kind. Provider/engine search columns
// do not exist yet (see the plan's backend-gaps note); poolers are not modeled
// yet, so none are emitted.
func (s *Server) scopeOptions(ctx context.Context, q string, active scope.Scope) []web.ScopeOption {
	clusters, err := s.conf.ListClusters(ctx)
	if err != nil {
		return nil
	}
	activeKey := active.Encode()
	ql := strings.ToLower(strings.TrimSpace(q))

	var out []web.ScopeOption
	add := func(sc scope.Scope, label, kind string, depth int) {
		if ql != "" && !strings.Contains(strings.ToLower(label+" "+kind), ql) {
			return
		}
		key := sc.Encode()
		out = append(out, web.ScopeOption{
			Label:    label,
			Kind:     kind,
			Depth:    depth,
			ScopeKey: key,
			Href:     web.ScopeHref(sc),
			Current:  key == activeKey,
		})
	}

	for _, cl := range clusters {
		add(scope.Scope{Kind: scope.Cluster, ClusterID: cl.ID}, cl.Name, "CLUSTER", 0)

		instances, err := s.conf.ListInstances(ctx, cl.ID)
		if err != nil {
			continue
		}
		// Emit the cluster's nodes (Depth 1), collecting its distinct database
		// names as we go; then emit those databases once at the cluster level
		// (also Depth 1), in first-seen order.
		seenDB := map[string]bool{}
		var dbNames []string
		for _, in := range instances {
			add(scope.Scope{Kind: scope.Node, ClusterID: cl.ID, NodeID: in.ID},
				cl.Name+" / "+in.Name, "NODE", 1)

			streams, err := s.conf.ListServerStreams(ctx, in.ID)
			if err != nil {
				continue
			}
			for _, st := range streams {
				if st.DatabaseName == "" || seenDB[st.DatabaseName] {
					continue
				}
				seenDB[st.DatabaseName] = true
				dbNames = append(dbNames, st.DatabaseName)
			}
		}
		for _, db := range dbNames {
			add(scope.Scope{Kind: scope.Database, ClusterID: cl.ID, Database: db},
				cl.Name+"/"+db, "DATABASE", 1)
		}
	}
	return out
}

// resolveScopeLabel finds the display label for the active scope in the full
// (unfiltered) option list; falls back to "FLEET" if the entity is gone.
func resolveScopeLabel(opts []web.ScopeOption, active scope.Scope) string {
	enc := active.Encode()
	for _, o := range opts {
		if o.ScopeKey == enc {
			return o.Label
		}
	}
	return "FLEET"
}
```

- [ ] **Step 3d: Register the routes**

In `internal/api/server.go`, inside `routes()`, add after the `GET /` line (`internal/api/server.go:59`):

```go
	s.mux.HandleFunc("GET /fleet", s.handleFleet)
	s.mux.HandleFunc("GET /partial/scope-options", s.handleScopeOptions)
```

- [ ] **Step 4a: Generate templ**

Run: `make templ`
Expected: PASS — `web/fleet_templ.go` created.

- [ ] **Step 4b: Build + run the tests**

Run: `go build ./...`
Expected: PASS.

Run: `go test ./internal/api/ -run 'TestFleet|TestScopeOptions'`
Expected: PASS (containers start; if docker is unavailable the tests `t.Skip`).

- [ ] **Step 5: Commit**

```bash
git branch --show-current   # confirm NOT main
git add internal/api/shell.go internal/api/shell_test.go internal/api/server.go \
        web/fleet.templ web/fleet_templ.go web/shell.go
git commit -m "feat(api): /fleet shell landing + /partial/scope-options search over topology (ly-ae6.2)"
```

---

### Task 5: Full-suite verification + templ-sync gate

Confirm the whole repo builds, all tests pass, and generated templ is in sync (CI's gate).

**Files:** none (verification only).

- [ ] **Step 1: Regenerate templ and confirm no drift**

Run: `make templ && git status --porcelain web/`
Expected: no unstaged changes under `web/` (all `_templ.go` already committed). If any appear, `git add` + amend the relevant commit.

- [ ] **Step 2: Build everything**

Run: `go build ./...`
Expected: PASS, no output.

- [ ] **Step 3: Run the full test suite**

Run: `go test ./...`
Expected: PASS across `internal/scope`, `web`, `internal/api` (and unchanged packages). Container-backed tests skip cleanly if docker is unavailable.

- [ ] **Step 4: Run go vet**

Run: `go vet ./internal/scope/ ./web/ ./internal/api/`
Expected: PASS, no diagnostics.

- [ ] **Step 5: Commit any sync fixups (only if Step 1 produced changes)**

```bash
git add -A web/
git commit -m "chore(web): sync generated templ (ly-ae6.2)"
```

---

## Self-Review

### 1. Spec coverage — COMPARISON gaps → tasks

**COMPARISON `#### Global top bar` gaps:**
- No 48px top-bar shell → Task 3 (`Shell`/`TopBar`, `shell.css .topbar` height 48px).
- Design tokens absent/wrong (dark-default JetBrains Mono) → Task 3 (`shell.css` uses `var(--font-mono)`, tokens; `data-theme="dark"`). Reuses ly-ae6.1.
- No logo mark returning to Fleet → Task 3 (`.topbar-logo` → `vm.LogoHref` = `/fleet`) + Task 4 (`LogoHref`).
- No SCOPE picker button + searchable dropdown w/ kind badges → Task 3 (`scopePicker`, `ScopeOptionsList`, `.kind-badge`) + Task 4 (`/partial/scope-options`, `scopeOptions`).
- No search matching names+kinds (+provider/engine) → Task 4 (`scopeOptions` filters over label+kind; provider/engine documented as backend gap, not blocking).
- No back-to-FLEET reset + accent scoped chip → Task 3 (`.fleet-reset` shown when `vm.Scoped`; `.scope-chip[data-scoped]` accent) + Task 2 (`Scoped`, `ClearHref`).
- No time-range segmented control wired to a shared range param → Task 2 (`ParseRange`, `RangeOptions`, `ValidRanges`) + Task 3 (`rangeControl`).
- No poll indicator → Task 3 (`pollIndicator` + `shell.js` ticker; reuses existing `@keyframes pulse`).
- No theme toggle (dark/light/system, persisted) → Task 3 (`#theme-toggle` + `shell.js` → `window.Lynceus.cycleTheme()` from ly-ae6.1).
- No user button/menu (identity + GOVERNANCE + ADMIN + Sign out) → Task 3 (`userMenu`) + Task 2 (`ShellUser`) + Task 4 (dev identity).

**COMPARISON `#### Scope model core` gaps:**
- No 'current scope' concept / resolver / scoped URL scheme → Task 1 (`scope.Scope`, `Encode`/`Parse`) + Task 4 (`buildShellView`, `resolveScopeLabel`, `?scope=` scheme).
- No top-bar app shell (all controls) → Tasks 2/3/4.
- Nav not rebuilt per scope → OUT OF SCOPE — ly-ae6.3. Task 3 exposes `ShellView.Sidebar templ.Component` slot + `placeholderSidebar()`; documented in "Dependencies & integration contracts".
- No node/pooler/database scope; db identity cluster+name in routing → Task 1 (`Node`/`Pooler`/`Database` kinds; database form carries `ClusterID`) + Task 4 (enumerate nodes + cluster-qualified databases). Poolers documented as backend gap (ly-99s).
- No Fleet Overview surface for deep links to land → Task 4 (`/fleet` live landing + placeholder main). Real fleet body = ly-ae6.4; scoped overview = ly-ae6.6 (documented).
- Wrong scope-set interaction (rows are links; should be ⌖ on inert rows) → Task 3 (`ScopeButton` = the `⌖`, links to `ScopeHref`). Adoption on rows is ly-ae6.4/.5/.6/.7 (documented contract).
- Scoped Overview "OPEN ISSUES ON THIS …" absent → ly-ae6.6 (documented dependency; not this bead).
- Deep-link-to-explanation → mechanism (the `?scope=` param + `ScopeHref`) delivered here; concrete needs-attention deep links = ly-ae6.4/.6 (documented).
- Design tokens/shell styling absent → Task 3 (`shell.css`, tokens only).

**Bead ly-ae6.2 acceptance criteria (from `bd show`):**
- logo→Fleet ✓ T3/T4 · time-range 15M/1H/24H/7D/30D ✓ T2/T3 · poll indicator ✓ T3 · theme toggle ✓ T3 · user menu ✓ T3 · scope hierarchy fleet→cluster→node/pooler→database ✓ T1 · set via picker ✓ T3/T4 · set via row select-scope buttons (⌖) ✓ T3 (`ScopeButton`) · deep-links to explanations ✓ mechanism T1/T4 (targets deferred to ly-ae6.4/.6) · back-to-FLEET ✓ T3/T2.

### 2. Placeholder scan
No "TBD"/"add error handling"/"similar to Task N"/code-free code steps. Every code step contains complete, compilable code. The `shellPlaceholderMain` and `placeholderSidebar` are **product seams explicitly owned by ly-ae6.4/.6 and ly-ae6.3** (real, rendered content that names the current scope), not plan placeholders. The **poll ticker** (`shell.js` "UPDATED Ns AGO") is a labelled cosmetic placeholder — a self-contained visual counter, not a live freshness readout — documented in the backend-gaps note and in the `shell.js` / `pollIndicator` comments; it becomes real when data screens add polling (ly-ae6.4+). Provider/engine/pooler omissions and the search-placeholder wording are documented backend gaps / conscious divergences with owning epics, not hand-waves.

### 3. Type consistency
- `scope.Scope{Kind, ClusterID, NodeID, PoolerID, Database}` and `scope.Parse`/`Encode`/`IsFleet` — defined Task 1, consumed identically in Tasks 2 (`ScopeHref`, `RangeOptions`), 3 (`scopePicker` `vm.Scope.Encode()`, `ScopeButton(sc scope.Scope)`), 4 (`scope.Parse`, `scopeOptions`).
- `web.ScopeOption{Label, Kind, Depth, ScopeKey, Href, Current}` — defined Task 2; produced by Task 4 `scopeOptions` (all six fields set) and consumed by Task 3 `ScopeOptionsList` (`o.Label`, `o.Kind`, `o.Depth`, `o.Href`, `o.Current`). `ScopeKey` used by Task 4 `resolveScopeLabel`. Consistent.
- `web.ShellView` fields set in Task 4 `buildShellView` exactly match those read in Task 3 templ (`Scope, ScopeLabel, Scoped, ClearHref, LogoHref, Range, Ranges, PollSecs, Options, OptionsQuery, User, Sidebar, Title`). `OptionsQuery` is unset by `buildShellView` (zero "") and only echoed into the search input — consistent (initial full list uses empty query).
- `web.ScopeHref` returns `templ.SafeURL`; `RangeOption.Href`, `ScopeOption.Href`, `ShellView.ClearHref/LogoHref` all `templ.SafeURL`; templ `href={ … }` accepts `templ.SafeURL`. Consistent.
- Handler helper names stable: `handleFleet`, `handleScopeOptions`, `buildShellView`, `scopeOptions`, `resolveScopeLabel` — referenced identically in `server.go` route registration and within `shell.go`.
- Store method signatures used (`ListClusters`, `ListInstances`, `ListServerStreams`) and struct fields (`Cluster.ID/Name`, `Instance.ID/Name`, `ServerStream.DatabaseName`) verified against `internal/store/config.go` + `internal/store/fleet.go`.
- `themeBootstrapTag()` (package `web`, `web/bootstrap.go`) called from `Shell` — same package, verified. `@keyframes pulse` referenced by `.poll-dot` exists at `tokens.css:61`, so `shell.css` does not redefine it.

### 4. Adversarial-review resolutions (conscious decisions & downstream hand-offs)

Each item below was raised in adversarial review and is resolved in this plan; none is a silent gap.

1. **`RangeOptions` hardcodes the `/fleet` base path (issue 1) — FIXED via explicit hand-off.** `ScopeHref` and `RangeOptions` both build `/fleet?...` links, correct while `/fleet` is the only landing. The Dependencies section now flags a ⚠️ base-path caveat requiring **ly-ae6.6** to repoint BOTH helpers together (shared base-path resolver), and `RangeOptions`' own code comment repeats the warning. Downstream can no longer inherit the "range change bounces to /fleet" breakage silently. (Chose a documented hand-off + inline reminder over a speculative base-path param, per simplicity-first — the param has one value today.)
2. **Databases were emitted at Depth 2 under a node (issue 2) — FIXED to match design.** The prototype places databases at `pad: 1`, cluster-level siblings of nodes/poolers (`Lynceus.dc.html` `scopes` array; README "Scope Model": "Databases are identified by cluster + name"). `scopeOptions` now collects each cluster's distinct database names across all its instances and emits them once at **Depth 1** after the nodes; the `data-depth="2"` CSS rule is dropped and the `ScopeOption.Depth` doc updated. A primary+replica cluster therefore shows its shared database exactly once at cluster level — a conscious, documented dedup, no longer indented beneath whichever node reported it first.
3. **Poll ticker presented as live (issue 3) — FIXED via labelling.** The `UPDATED Ns AGO` counter is now explicitly documented as cosmetic-only (backend-gaps note + `shell.js` header comment + `pollIndicator` templ comment), with the concrete upgrade path (reset on `htmx:afterSwap` once ly-ae6.4+ data screens poll). No behavior change — it was always chrome; the plan no longer implies otherwise.
4. **Search placeholder diverges from the prototype (issue 4) — RESOLVED as a documented conscious divergence.** Prototype reads `search cluster / instance / pooler…` (`Lynceus.dc.html:120`); this plan uses `search cluster / node / database…` to name only entities actually enumerated (poolers unmodeled; "instance" == our NODE). Called out in the backend-gaps note and inline in `scopePicker`, with a revisit trigger (ly-99s) so pixel-parity reviewers expect the one-line difference.
