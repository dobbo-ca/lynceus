# Design-Parity Retrofit of Existing Scoped Screens Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Retrofit the seven already-built scoped screens — Top Queries, Query Drilldown/Insights, Index/Vacuum/Config Advisors, Wait Events, Checks, Query Plan, and Audit Log — from generic MVP HTML tables onto the F1 design-token system with the exact prototype markup, badges, severity striping, and interactions.

**Architecture:** Each screen keeps the existing `XxxPage(vm)` (wrapped in `@Layout`) + `XxxTable/XxxView` HTMX fragment pattern in package `web`, and its `internal/api` handler + `fetchXxx` view-model mapper. The retrofit rewrites each `.templ` to token-based markup driven by a shared component/CSS layer (`web/components.templ`, `web/design.go`, `web/static/css/screens.css`), extends the view-model structs with the fields the design needs (computing what the store already has, referencing tracked backend beads for what it does not), and adds sorting/filtering/expand/mute interactions via HTMX query params. No screen depends on JS beyond self-hosted htmx.

**Tech Stack:** Go 1.23, `templ` (a-h/templ, regen via `make templ`), HTMX (self-hosted `/static/js/htmx.min.js`), CSS custom-property tokens (`web/static/css/tokens.css` from ly-ae6.1), `net/http.ServeMux` routing, `httptest` + rendered-HTML string assertions for unit tests, testcontainers for any store-touching integration test.

## Global Constraints

Every task's requirements implicitly include this section. Values copied verbatim from the design handoff and repo `CLAUDE.md`.

- **Privacy T1/T2:** Only T1 (normalized, literal-free) data renders unless a surface is explicitly T2. The **only** T2 surface in this plan is the Query Drilldown "RAW SAMPLE — TIER 2" reveal panel and the Top Queries `SAMPLE` column badge — both render a **locked gate only**; no literal value is ever placed in a view-model field. Never add a raw-sample/raw-text field to any T1 view-model. The T2 reveal action targets an audited endpoint tracked by `ly-8b0.6` / `ly-u4t.21` and is **out of scope here** — render the gate, not the sample.
- **No external hosts:** Never reference a CDN/font/script host. All CSS/JS/fonts/SVG live under `web/static/` and load from `/static/...`. Contract test `web/layout_test.go::TestLayout_NoExternalHosts` enforces this.
- **Tokens, not legacy:** New/retrofitted markup is built with design tokens — either token CSS classes defined in `web/static/css/screens.css` or `var(--x)` inline — **never** the pre-design `legacy.css` component classes. `legacy.css` stays linked only for the not-yet-retrofitted screens (databases, overview, cluster_views).
- **templ regen:** Any `.templ` edit must be followed by `make templ` to regenerate the committed `_templ.go`; CI checks the generated files are in sync. Commit the `.templ` and its `_templ.go` together.
- **testcontainers:** Integration tests hit real Postgres via testcontainers (`internal/store`, `internal/api` patterns) — never mock the DB. View-model/handler/templ logic is unit-tested with `httptest` + rendered-HTML assertions (see `web/layout_test.go`, `internal/api/*_test.go`).
- **Shape/type language:** 2px radius (1px on tiny badges), 1px borders, no shadows except dropdowns/modals. JetBrains Mono for ALL data/labels/badges (labels 9.5–10.5px letter-spaced, values `font-variant-numeric: tabular-nums`); Work Sans for UI prose (13px). Severity squares/stripes are unrounded; row severity is a 3px `border-left` stripe.

## Scope-Shell Integration Contract (ly-ae6.2 + ly-ae6.3)

This bead (`ly-ae6.7`) **depends on `ly-ae6.3`** (scope-driven sidebar nav) which **depends on `ly-ae6.2`** (top bar + scope model). Those beads build the shell separately. This plan does **not** rebuild the shell and does **not** edit the `layout.templ` `<nav>` (owned by ly-ae6.3). The integration seam:

- **Scope caption:** Screens that show a scope context line (Wait Events, Vacuum Advisor, Config Advisor per-server, Checks) read a `ScopeLabel string` field on their view-model. Today the handler fills it from the `?server=` / `{clusterID}` route value; after ly-ae6.2 lands it is filled from resolved scope state. The field is the contract — ly-ae6.2 sets it, this plan renders it.
- **Scoped routes:** Screen bodies are exposed as standalone templ components (`QueriesScreen`, `QueryDrilldownScreen`, `InsightsScreen`, `IndexAdvisorScreen`, `VacuumAdvisorScreen`, `ConfigAdvisorScreen`, `WaitsScreen`, `ChecksScreen`, `PlanScreen`, `AuditScreen`) that render the `data-screen-label` body **without** `@Layout`. The existing `XxxPage` wraps `@Layout{ @XxxScreen(vm) }` for standalone testing today; ly-ae6.3 re-mounts the same `XxxScreen` component inside the scoped shell. Nav gating rules ("waits/insights/checks never appear at fleet scope", "Alerts soon sub-item") are ly-ae6.3's responsibility — noted, not implemented here.
- **Deep-link contract:** Insight rows and needs-attention items link to the Query Drilldown at `GET /databases/{clusterID}/query/{fingerprint}` (full page added in Task 3). Checks deep-link uses `?expand={checkID}` (Task 9). Index-advisor EVIDENCE and drilldown VIEW PLAN link to `GET /plan?server={id}&fp={fp}` (existing route, retrofitted Task 10).
- **In-component link base paths (no fleet literals in shared helpers):** Every retrofitted screen's *page-navigation* links — column-sort hrefs, filter/severity/kind chip hrefs, per-server Config tabs, Checks `?expand=` hrefs, drilldown ← back / VIEW PLAN, and every EVIDENCE/drilldown fleet fallback — derive from a `Nav ScreenNav` value **carried on the view-model**, NOT from a hardcoded fleet literal inside a `web/design.go` helper. `ScreenNav{ Base, Plan string }` is defined once in Task 1. The fleet handlers in THIS plan fill it with today's fleet routes (`Base` = `/`, `/insights`, `/config-advisor`, `/checks`; `Plan` = `/plan`); when ly-ae6.3 re-mounts the same `XxxScreen` under `/databases/{clusterID}/…` it refills `Nav` with the scoped prefix, so no page-navigation link silently drops scope and ly-ae6.3 does **not** have to edit `design.go`. Scope-aware drilldown/evidence links already prefer the row's `ClusterID` when it is set (scoped path); the `Nav.Plan` field only supplies the fleet fallback used when `ClusterID == ""`. **Screen-owned fragment routes** — the `hx-get` poll/swap targets and the Checks `POST /partial/checks/mute` action endpoint — are intentionally left as literals: they refresh the same fragment in place and are re-registered wholesale by ly-ae6.3 when it re-mounts the screen, so they carry no scope to drop.

## Tracked Backend Dependencies (do NOT re-plan)

Fields the design shows that the store does not yet provide are tracked elsewhere; this plan defines the exact VM field the UI needs and renders a graceful `—`/empty state until the bead lands:

| Design element | VM field added | Tracked bead | Render-until-landed |
|---|---|---|---|
| Top Queries ROWS, CACHE hit % | `TopQuery.Rows`, `TopQuery.CacheHitPct` | ly-58w.8, ly-xqf.3 | `—` when `Rows==0` / `CacheHitPct<0` |
| Top Queries + drilldown TREND spark, calls/min & mean-time series | `TopQuery.SparkPoints`, `DrilldownVM.CallsTrend/CallsArea/MeanTrend` | ly-xqf.10, ly-xqf.11 | omit polyline (grid lines only) when `""` |
| Per-query wait breakdown | `DrilldownVM.Waits` | ly-u4t.22, ly-xqf.1 | empty-state caption when `nil` |
| T2 raw query sample reveal | `QuerySampleVM.Locked` (no literal field) | ly-8b0.6, ly-u4t.21 | locked gate only |
| Index-advisor CREATE INDEX DDL, EST. BENEFIT % | `IndexAdvisorRow.DDL`, `IndexAdvisorRow.BenefitPct` | ly-u4t.13 | derive DDL from relation+columns; `BenefitPct=0` hides bar fill |
| Index-advisor scope crumb (Cluster ▸ Database ▸ Server) | `IndexAdvisorRow.Cluster/Database/Server/ClusterID` | ly-u4t.12 | crumb collapses to `—`; **EVIDENCE fp is NOT deferred** — populated today from `advisor.IndexRecommendation.Fingerprints[0]` (index.go:59) |
| Vacuum-advisor BLOAT/FREEZING bars | `VacuumAdvisorVM` panel slices | ly-u4t.16, ly-u4t.17, ly-u4t.26 | empty panel when slice `nil` |
| Checks 24h history sparkline | `ChecksRow.History []HistCell` | ly-u4t.25 | empty strip when `nil` |
| Checks mute persistence (NOT deferred) | store `SetMute`/`ClearMute`/`ListMutes` | — (shipped) | `SetMute`+`ListMutes` already on the `store.Stats` interface (stats.go:49-50); `ClearMute` widened onto the interface in Task 9. The MUTE toggle **writes real state** and `fetchChecks` overlays `ListMutes` so re-render reflects it. ly-u4t.20 tracks alert *routing*, not mute storage. |
| Audit TARGET column | `AuditRow.Target` (extracted from Detail JSON) | ly-8b0.3 | `—` when unextractable |

---

### Task 1: Shared screen design layer (helpers + components + screens.css)

Establishes the token-based building blocks every later task reuses: severity mapping helpers, reusable header/badge templ components, and the `screens.css` component stylesheet. Written once here; referenced by exact name in Tasks 2–11.

**Files:**
- Create: `web/design.go` (pure Go helpers)
- Create: `web/design_test.go` (unit tests for helpers)
- Create: `web/components.templ` (shared templ components)
- Create: `web/components_test.go` (rendered-HTML tests)
- Create: `web/static/css/screens.css` (token component classes)
- Modify: `web/layout.templ:26-27` (add `screens.css` `<link>`)
- Modify: `web/static_test.go` (assert screens.css is embedded/served)

**Interfaces:**
- Produces: `func SevClass(sev string) string` — maps any severity vocab to `"crit"|"warn"|"info"`. `crit`: `critical|crit|high|error|fatal`; `warn`: `warn|warning|medium|mod`; `info`: everything else (incl. `info|low|notice`). Case-insensitive.
- Produces: `func SevLabel(sev string) string` — returns `"CRIT"|"WARN"|"INFO"` for the mapped class.
- Produces: `func SevChartVar(class string) string` — returns the CSS color token name for a class: `crit`→`var(--crit)`, `warn`→`var(--warn)`, `info`→`var(--info)`.
- Produces: `func MeanMs(totalMs float64, calls int64) float64` — `0` when `calls<=0`, else `totalMs/float64(calls)`.
- Produces: `func RecommendationFor(kind string) string` — package-authored, literal-free remediation guidance keyed by `insight.Kind` string; `""` for unknown kinds.
- Produces: `func KindLabel(kind string) string` — human label for an `insight.Kind` string (e.g. `slow_scan`→`SLOW SEQ SCAN`); falls back to upper-snake of the input.
- Produces: struct `ScreenNav{ Base string; Plan string }` — the base routes a screen's page-navigation links resolve against (see the Scope-Shell Integration Contract). `Base` is the screen's own full-page route; `Plan` is the plan/drilldown route used as the fleet fallback when a row has no `ClusterID`. Carried on each screen's view-model / sort / filter struct; fleet handlers fill it, ly-ae6.3 refills it under scope. Helpers read it — they never hardcode a fleet literal.
- Produces: templ `HeaderBadge` struct `{ Text string; Kind string }` where `Kind ∈ {"live","t1","t2","scope"}`.
- Produces: templ `component ScreenHeader(title string, badges []HeaderBadge)` — renders the `.screen-hd` title row.
- Produces: templ `component EmptyState(msg string)` — token-styled empty row (`.empty-state`).
- Produces CSS classes (in `screens.css`): `.screen .screen-hd .screen-title .badge .badge--live .badge--t1 .badge--t2 .badge--scope .panel .panel-hd .scroll-x .grid-hd .grid-row .sev-crit .sev-warn .sev-info .stripe-crit .stripe-warn .stripe-info .bar .bar-fill .chip .chip--on .filter-input .icon-btn .empty-state .num-r`.

- [ ] **Step 1: Write the failing helper tests**

Create `web/design_test.go`:

```go
package web

import "testing"

func TestSevClass(t *testing.T) {
	cases := map[string]string{
		"critical": "crit", "CRIT": "crit", "high": "crit", "error": "crit",
		"warning": "warn", "medium": "warn", "WARN": "warn",
		"info": "info", "low": "info", "notice": "info", "": "info", "weird": "info",
	}
	for in, want := range cases {
		if got := SevClass(in); got != want {
			t.Errorf("SevClass(%q)=%q want %q", in, got, want)
		}
	}
}

func TestSevLabel(t *testing.T) {
	if got := SevLabel("high"); got != "CRIT" {
		t.Errorf("SevLabel(high)=%q want CRIT", got)
	}
	if got := SevLabel("low"); got != "INFO" {
		t.Errorf("SevLabel(low)=%q want INFO", got)
	}
}

func TestMeanMs(t *testing.T) {
	if got := MeanMs(100, 0); got != 0 {
		t.Errorf("MeanMs div-by-zero guard: got %v want 0", got)
	}
	if got := MeanMs(100, 4); got != 25 {
		t.Errorf("MeanMs(100,4)=%v want 25", got)
	}
}

func TestRecommendationFor(t *testing.T) {
	if RecommendationFor("slow_scan") == "" {
		t.Error("slow_scan should have a recommendation")
	}
	if RecommendationFor("no_such_kind") != "" {
		t.Error("unknown kind should return empty string")
	}
}

func TestKindLabel(t *testing.T) {
	if got := KindLabel("slow_scan"); got != "SLOW SEQ SCAN" {
		t.Errorf("KindLabel(slow_scan)=%q want SLOW SEQ SCAN", got)
	}
	if got := KindLabel("mystery_kind"); got != "MYSTERY KIND" {
		t.Errorf("KindLabel fallback=%q want MYSTERY KIND", got)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./web/ -run 'TestSevClass|TestSevLabel|TestMeanMs|TestRecommendationFor|TestKindLabel' -v`
Expected: FAIL — build error `undefined: SevClass` (and the rest).

- [ ] **Step 3: Implement the helpers**

Create `web/design.go`:

```go
package web

import "strings"

// ScreenNav carries the base routes a retrofitted screen's page-navigation
// links resolve against. THIS plan's fleet handlers fill it with fleet routes;
// ly-ae6.3 refills it with the scoped "/databases/{clusterID}/…" prefix when it
// re-mounts the screen, so no in-component page link hardcodes (and silently
// drops) scope. Base is the screen's own full-page route; Plan is the
// plan/drilldown route used as the fleet fallback when a row has no ClusterID.
type ScreenNav struct {
	Base string
	Plan string
}

// SevClass maps any engine severity vocabulary (insight low/medium/high,
// checks/advisor severity strings) onto the design's three token roles.
func SevClass(sev string) string {
	switch strings.ToLower(strings.TrimSpace(sev)) {
	case "critical", "crit", "high", "error", "fatal":
		return "crit"
	case "warn", "warning", "medium", "mod", "moderate":
		return "warn"
	default:
		return "info"
	}
}

// SevLabel returns the uppercase CRIT/WARN/INFO label for a severity.
func SevLabel(sev string) string {
	switch SevClass(sev) {
	case "crit":
		return "CRIT"
	case "warn":
		return "WARN"
	default:
		return "INFO"
	}
}

// SevChartVar returns the CSS color token for a severity class.
func SevChartVar(class string) string {
	switch class {
	case "crit":
		return "var(--crit)"
	case "warn":
		return "var(--warn)"
	default:
		return "var(--info)"
	}
}

// MeanMs is total time / calls, guarding division by zero.
func MeanMs(totalMs float64, calls int64) float64 {
	if calls <= 0 {
		return 0
	}
	return totalMs / float64(calls)
}

// recommendations is the package-authored, literal-free remediation guidance
// per insight kind. Keys mirror internal/insight.Kind string values.
var recommendations = map[string]string{
	"slow_scan":            "Add an index covering the filtered columns; a seq scan reads every row.",
	"disk_sort":            "Raise work_mem for this workload or add an index matching the sort key.",
	"disk_spill":           "Raise work_mem; the node spilled its hash/sort to disk.",
	"hash_batches":         "Raise work_mem so the hash fits in one batch.",
	"inefficient_index":    "The index is scanned but most rows are discarded — reconsider its column order.",
	"mis_estimate":         "Run ANALYZE; the planner's row estimate is far from actual.",
	"stale_stats":          "Statistics are stale — ANALYZE the relation so estimates track reality.",
	"large_offset":         "Replace OFFSET pagination with keyset (WHERE id > last) pagination.",
	"lossy_bitmap":         "The bitmap heap scan went lossy — raise work_mem or narrow the predicate.",
	"nested_loop":          "A nested loop over many rows is costly — check join estimates and indexes.",
	"wrong_index_order_by": "The chosen index does not match the ORDER BY — add a matching composite index.",
}

// RecommendationFor returns literal-free guidance for an insight kind, or "".
func RecommendationFor(kind string) string { return recommendations[kind] }

// KindLabel humanizes an insight kind for display. Known kinds get a curated
// label; unknown kinds fall back to UPPER SNAKE with underscores as spaces.
func KindLabel(kind string) string {
	known := map[string]string{
		"slow_scan":            "SLOW SEQ SCAN",
		"disk_sort":            "DISK SORT",
		"disk_spill":           "DISK SPILL",
		"hash_batches":         "HASH BATCHES",
		"inefficient_index":    "INEFFICIENT INDEX",
		"mis_estimate":         "MIS-ESTIMATE",
		"stale_stats":          "STALE STATS",
		"large_offset":         "LARGE OFFSET",
		"lossy_bitmap":         "LOSSY BITMAP",
		"nested_loop":          "NESTED LOOP",
		"wrong_index_order_by": "WRONG INDEX FOR ORDER BY",
	}
	if l, ok := known[kind]; ok {
		return l
	}
	return strings.ToUpper(strings.ReplaceAll(kind, "_", " "))
}
```

- [ ] **Step 4: Run to verify helpers pass**

Run: `go test ./web/ -run 'TestSevClass|TestSevLabel|TestMeanMs|TestRecommendationFor|TestKindLabel' -v`
Expected: PASS (all five).

- [ ] **Step 5: Write the failing component test**

Create `web/components_test.go`:

```go
package web

import (
	"context"
	"strings"
	"testing"
)

func renderComp(t *testing.T, c interface{ Render(context.Context, *strings.Builder) error }) string {
	t.Helper()
	var sb strings.Builder
	if err := c.Render(context.Background(), &sb); err != nil {
		t.Fatalf("render: %v", err)
	}
	return sb.String()
}

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
```

- [ ] **Step 6: Run to verify it fails**

Run: `go test ./web/ -run 'TestScreenHeader|TestEmptyState' -v`
Expected: FAIL — build error `undefined: ScreenHeader` / `HeaderBadge` / `EmptyState`.

- [ ] **Step 7: Implement the shared components**

Create `web/components.templ`:

```go
package web

// HeaderBadge is one pill in a screen header. Kind selects the token color:
// "live" (accent), "t1" (faint/line), "t2" (warn), "scope" (muted bordered).
type HeaderBadge struct {
	Text string
	Kind string
}

// ScreenHeader renders the title + badge row shared by every retrofitted
// screen (Lynceus.dc.html:860-866 pattern).
templ ScreenHeader(title string, badges []HeaderBadge) {
	<div class="screen-hd">
		<span class="screen-title">{ title }</span>
		for _, b := range badges {
			<span class={ "badge", "badge--" + b.Kind }>{ b.Text }</span>
		}
		<span style="flex:1;"></span>
	</div>
}

// EmptyState is the token-styled "no data" row used by every fragment.
templ EmptyState(msg string) {
	<div class="empty-state">{ msg }</div>
}
```

Create `web/static/css/screens.css`:

```css
/* Lynceus retrofitted-screen component layer — token-driven (ly-ae6.7).
   Colors come only from tokens.css custom properties; never hardcode hex. */

.screen { padding:18px 22px 32px; display:flex; flex-direction:column; gap:14px; max-width:1400px; }
.screen-hd { display:flex; align-items:center; gap:12px; }
.screen-title { font-size:17px; font-weight:600; color:var(--text); }

.badge { font-family:var(--font-mono); font-size:10px; padding:0 5px; border-radius:var(--radius-badge);
         border:var(--border) solid var(--line); color:var(--faint); }
.badge--live  { color:var(--acc);   border-color:var(--acc); }
.badge--t1    { color:var(--faint); border-color:var(--line); }
.badge--t2    { color:var(--warnT); border-color:var(--warn); }
.badge--scope { color:var(--mut);   border-color:var(--line); letter-spacing:.06em; padding:2px 8px; }

.panel { border:var(--border) solid var(--line); border-radius:var(--radius); background:var(--surface); }
.panel-hd { padding:8px 12px; border-bottom:var(--border) solid var(--line);
            font-family:var(--font-mono); font-size:10px; letter-spacing:.1em; color:var(--dim); }
.scroll-x { overflow-x:auto; }

.grid-hd { font-family:var(--font-mono); font-size:9.5px; letter-spacing:.1em; color:var(--faint);
           padding:8px 12px; border-bottom:var(--border) solid var(--line); align-items:center; }
.grid-row { padding:7px 12px; border-bottom:var(--border) solid var(--line2); align-items:center;
            font-variant-numeric:tabular-nums; }
.grid-row:hover { background:var(--raised); }

.sev-crit { color:var(--critT); } .sev-warn { color:var(--warnT); } .sev-info { color:var(--infoT); }
.stripe-crit { border-left:3px solid var(--crit); }
.stripe-warn { border-left:3px solid var(--warn); }
.stripe-info { border-left:3px solid var(--info); }

.bar { flex:1; height:10px; background:var(--raised); border-radius:var(--radius-badge); }
.bar-fill { height:10px; background:var(--acc); border-radius:var(--radius-badge); }

.chip { font-family:var(--font-mono); font-size:10.5px; padding:3px 9px; border:var(--border) solid var(--line);
        color:var(--dim); background:transparent; border-radius:var(--radius); cursor:pointer; user-select:none; }
.chip--on { color:var(--acc2); border-color:var(--acc); background:var(--accbg); }

.filter-input { background:var(--surface); border:var(--border) solid var(--line); border-radius:var(--radius);
                color:var(--text); font-family:var(--font-mono); font-size:11.5px; padding:6px 10px; width:280px; }

.icon-btn { width:24px; height:24px; display:inline-flex; align-items:center; justify-content:center;
            border:var(--border) solid var(--line); border-radius:var(--radius); color:var(--dim);
            background:transparent; cursor:pointer; }
.icon-btn:hover { color:var(--acc); border-color:var(--acc); }

.empty-state { padding:24px 12px; font-family:var(--font-mono); font-size:11px; color:var(--faint); text-align:center; }
.num-r { text-align:right; }
.mono-lbl { font-family:var(--font-mono); letter-spacing:.08em; color:var(--faint); }
```

Add the `<link>` in `web/layout.templ` immediately after the `legacy.css` link (line 27):

```html
			<link rel="stylesheet" href="/static/css/tokens.css"/>
			<link rel="stylesheet" href="/static/css/legacy.css"/>
			<link rel="stylesheet" href="/static/css/screens.css"/>
```

- [ ] **Step 8: Regenerate templ and run component + layout tests**

Run: `make templ && go test ./web/ -run 'TestScreenHeader|TestEmptyState|TestLayout' -v`
Expected: PASS (component tests + existing layout tests still green — the new `<link>` does not add an external host).

- [ ] **Step 9: Add screens.css to the served-assets test**

In `web/static_test.go`, extend the embedded-asset assertion list to include `css/screens.css`. Find the slice of expected static paths and add `"css/screens.css"` (mirror the existing `css/tokens.css` entry).

- [ ] **Step 10: Run static test**

Run: `go test ./web/ -run 'TestStatic' -v`
Expected: PASS — `/static/css/screens.css` is embedded and served.

- [ ] **Step 11: Commit**

```bash
git add web/design.go web/design_test.go web/components.templ web/components_templ.go web/components_test.go web/static/css/screens.css web/layout.templ web/layout_templ.go web/static_test.go
git commit -m "feat(ui): shared token-based screen design layer (helpers, header/badge components, screens.css) [ly-ae6.7]"
```

---

### Task 2: Top Queries screen retrofit

Covers COMPARISON `#### Queries screen` gaps: token design system; single scope-only Top Queries screen; MEAN/ROWS/CACHE/TREND/SAMPLE columns; column sort + SQL/fingerprint filter; per-fingerprint insight ▲ count; T2 SAMPLE column + caption. (Drilldown and per-query T2 reveal are Task 3.)

**Files:**
- Modify: `web/layout.templ:8-16` (extend `TopQuery` struct)
- Modify: `web/queries.templ` (full rewrite to token grid + `QueriesScreen`)
- Modify: `internal/api/queries.go` (add sort/filter/insight-count to `fetchTop`)
- Modify: `internal/api/dashboard.go` (the `/` handler and its `fetchTop`; verify name)
- Test: `web/queries_test.go` (create)
- Test: `internal/api/queries_sort_test.go` (create)

**Interfaces:**
- Consumes: `ScreenHeader`, `HeaderBadge`, `EmptyState`, `SevClass`, `MeanMs` (Task 1); `store.Stats.TopQueriesByTotalTime(ctx, since, until, limit) ([]store.TopQuery, error)` (existing); `fetchInsights(r) []web.InsightRow` (existing, `internal/api/insights.go:30`).
- Produces: extended `web.TopQuery`:
```go
type TopQuery struct {
	Fingerprint     string
	NormalizedQuery string
	Calls           int64
	TotalTimeMs     float64
	MeanTimeMs      float64 // computed via MeanMs
	Rows            int64   // 0 until ly-58w.8; render "—"
	CacheHitPct     float64 // <0 unknown (ly-xqf.3); render "—"
	InsightCount    int     // per-fingerprint, computed from fetchInsights
	ClusterID       string  // drilldown link target; "" at fleet scope
	SparkPoints     string  // SVG polyline points; "" hides sparkline (ly-xqf.10)
}
```
- Produces: templ `QueriesScreen(sort QuerySort, rows []TopQuery)` and `QueriesTable(sort QuerySort, rows []TopQuery)`; struct `QuerySort{ Col string; Dir string }` (Col ∈ `calls|total|mean|rows|hit`, Dir ∈ `asc|desc`).
- Produces: `func (s *Server) sortAndFilterQueries(rows []web.TopQuery, q QuerySort, filter string) []web.TopQuery` in `internal/api/queries.go`; `type QuerySort = web.QuerySort` alias reused by the handler.

- [ ] **Step 1: Write the failing view-model + sort test**

Create `internal/api/queries_sort_test.go`:

```go
package api

import (
	"testing"

	"github.com/dobbo-ca/lynceus/web"
)

func rowsFixture() []web.TopQuery {
	return []web.TopQuery{
		{Fingerprint: "aaa", NormalizedQuery: "select * from orders", Calls: 10, TotalTimeMs: 100, MeanTimeMs: 10},
		{Fingerprint: "bbb", NormalizedQuery: "update users set x=$1", Calls: 5, TotalTimeMs: 200, MeanTimeMs: 40},
		{Fingerprint: "ccc", NormalizedQuery: "select * from items", Calls: 30, TotalTimeMs: 60, MeanTimeMs: 2},
	}
}

func TestSortAndFilter_SortByMeanDesc(t *testing.T) {
	s := &Server{}
	got := s.sortAndFilterQueries(rowsFixture(), web.QuerySort{Col: "mean", Dir: "desc"}, "")
	if got[0].Fingerprint != "bbb" || got[2].Fingerprint != "ccc" {
		t.Errorf("mean desc order wrong: %v", []string{got[0].Fingerprint, got[1].Fingerprint, got[2].Fingerprint})
	}
}

func TestSortAndFilter_FilterBySQLSubstring(t *testing.T) {
	s := &Server{}
	got := s.sortAndFilterQueries(rowsFixture(), web.QuerySort{Col: "total", Dir: "desc"}, "orders")
	if len(got) != 1 || got[0].Fingerprint != "aaa" {
		t.Errorf("filter should keep only the orders row, got %d rows", len(got))
	}
}

func TestSortAndFilter_FilterByFingerprint(t *testing.T) {
	s := &Server{}
	got := s.sortAndFilterQueries(rowsFixture(), web.QuerySort{Col: "calls", Dir: "asc"}, "ccc")
	if len(got) != 1 || got[0].Fingerprint != "ccc" {
		t.Errorf("fingerprint filter failed, got %d rows", len(got))
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/api/ -run TestSortAndFilter -v`
Expected: FAIL — `undefined: Server.sortAndFilterQueries`, `undefined: web.QuerySort`, `web.TopQuery has no field MeanTimeMs`.

- [ ] **Step 3: Extend the TopQuery struct and add QuerySort**

In `web/layout.templ`, replace the `TopQuery` struct (lines 8-16) with the extended version (fields listed in Interfaces above), and add `QuerySort` above it:

```go
// QuerySort is the active column-sort state for the Top Queries table.
// Nav carries the page-navigation base paths (Scope-Shell Integration
// Contract); the fleet handler fills it, ly-ae6.3 refills it under scope.
type QuerySort struct {
	Col string // "calls" | "total" | "mean" | "rows" | "hit"
	Dir string // "asc" | "desc"
	Nav ScreenNav
}

// TopQuery is the view-model for one row in the top-queries table.
type TopQuery struct {
	Fingerprint     string
	NormalizedQuery string
	Calls           int64
	TotalTimeMs     float64
	MeanTimeMs      float64
	Rows            int64
	CacheHitPct     float64
	InsightCount    int
	ClusterID       string
	SparkPoints     string
}
```

- [ ] **Step 4: Implement sortAndFilterQueries**

In `internal/api/queries.go`, add (and add `"sort"`, `"strings"` to imports; keep the existing `topQueryDTO`/`handleTopQueries`):

```go
// sortAndFilterQueries applies a case-insensitive substring filter over
// fingerprint+normalized SQL, then a stable sort by the chosen column.
// Default sort is total-time descending (matches the store's own order).
func (s *Server) sortAndFilterQueries(rows []web.TopQuery, q web.QuerySort, filter string) []web.TopQuery {
	out := rows[:0:0]
	f := strings.ToLower(strings.TrimSpace(filter))
	for _, r := range rows {
		if f == "" || strings.Contains(strings.ToLower(r.Fingerprint), f) ||
			strings.Contains(strings.ToLower(r.NormalizedQuery), f) {
			out = append(out, r)
		}
	}
	less := func(i, j int) bool {
		a, b := out[i], out[j]
		switch q.Col {
		case "calls":
			return a.Calls < b.Calls
		case "mean":
			return a.MeanTimeMs < b.MeanTimeMs
		case "rows":
			return a.Rows < b.Rows
		case "hit":
			return a.CacheHitPct < b.CacheHitPct
		default: // "total"
			return a.TotalTimeMs < b.TotalTimeMs
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if q.Dir == "asc" {
			return less(i, j)
		}
		return less(j, i)
	})
	return out
}
```

- [ ] **Step 5: Run the sort tests**

Run: `go test ./internal/api/ -run TestSortAndFilter -v`
Expected: PASS (all three).

- [ ] **Step 6: Write the failing templ render test**

Create `web/queries_test.go`:

```go
package web

import (
	"context"
	"strings"
	"testing"
)

func TestQueriesScreen_HeadersAndT2Column(t *testing.T) {
	rows := []TopQuery{{
		Fingerprint: "3f2a", NormalizedQuery: "select * from orders where id=$1",
		Calls: 1200, TotalTimeMs: 4800, MeanTimeMs: 4, Rows: 0, CacheHitPct: -1,
		InsightCount: 2, ClusterID: "orders-prod",
	}}
	var sb strings.Builder
	_ = QueriesScreen(QuerySort{Col: "total", Dir: "desc"}, rows).Render(context.Background(), &sb)
	html := sb.String()
	for _, want := range []string{
		"Top Queries", "badge--live", "FINGERPRINT", "NORMALIZED QUERY",
		"CALLS", "TOTAL", "MEAN", "ROWS", "CACHE", "TREND", "SAMPLE",
		"3f2a", "2▲",         // insight count triangle
		"T2 ◈",              // T2 sample-column badge
		"MAY CONTAIN LITERALS", // T2 caption
		"/databases/orders-prod/query/3f2a", // drilldown link
	} {
		if !strings.Contains(html, want) {
			t.Errorf("QueriesScreen missing %q", want)
		}
	}
	if strings.Contains(html, "system-ui") || strings.Contains(html, "#2b6cb0") {
		t.Error("QueriesScreen must not use legacy light styling")
	}
}

func TestQueriesScreen_UnknownMetricsRenderDash(t *testing.T) {
	rows := []TopQuery{{Fingerprint: "x", NormalizedQuery: "q", Calls: 1, TotalTimeMs: 1, MeanTimeMs: 1, Rows: 0, CacheHitPct: -1}}
	var sb strings.Builder
	_ = QueriesTable(QuerySort{Col: "total", Dir: "desc"}, rows).Render(context.Background(), &sb)
	if !strings.Contains(sb.String(), "—") {
		t.Error("unknown ROWS/CACHE should render em-dash")
	}
}
```

- [ ] **Step 7: Run to verify it fails**

Run: `go test ./web/ -run TestQueriesScreen -v`
Expected: FAIL — `undefined: QueriesScreen`.

- [ ] **Step 8: Rewrite queries.templ**

Replace the entire body of `web/queries.templ` with:

```go
package web

import "fmt"

// queriesGrid is the shared grid-template-columns for header + rows.
const queriesGrid = "grid-template-columns:84px minmax(280px,1fr) 92px 72px 84px 60px 68px 132px 58px;gap:10px;"

// QueriesPage wraps the screen in the layout for standalone rendering; the
// scoped shell (ly-ae6.3) re-mounts QueriesScreen directly.
templ QueriesPage(sort QuerySort, rows []TopQuery) {
	@Layout("Lynceus — top queries", "top queries by total time") {
		@QueriesScreen(sort, rows)
	}
}

// QueriesScreen is the full Top Queries body without layout.
templ QueriesScreen(sort QuerySort, rows []TopQuery) {
	<div class="screen" data-screen-label="Top Queries">
		@ScreenHeader("Top Queries", []HeaderBadge{
			{Text: "LIVE", Kind: "live"},
			{Text: "T1 · NORMALIZED FINGERPRINTS", Kind: "t1"},
		})
		<input class="filter-input" name="q" placeholder="filter by SQL or fingerprint"
			hx-get="/partial/queries" hx-target="#queries-table" hx-swap="outerHTML"
			hx-trigger="keyup changed delay:300ms" hx-include="[name='sort'],[name='dir']"/>
		@QueriesTable(sort, rows)
		<div class="mono-lbl" style="font-size:10px;">SAMPLE COLUMN IS TIER 2 (MAY CONTAIN LITERALS) — GATED PER SERVER, EVERY READ AUDITED. OPEN A QUERY TO REQUEST A REVEAL.</div>
	</div>
}

templ QueriesTable(sort QuerySort, rows []TopQuery) {
	<div id="queries-table" hx-get="/partial/queries" hx-trigger="every 10s" hx-swap="outerHTML">
		if len(rows) == 0 {
			@EmptyState("No data yet — start a collector and check back.")
		} else {
			<div class="panel scroll-x">
				<div style="min-width:1080px;">
					<div class="grid-hd" style={ "display:grid;" + queriesGrid }>
						<span>FINGERPRINT</span><span>NORMALIZED QUERY</span>
						@sortHead("CALLS", "calls", sort)
						@sortHead("TOTAL", "total", sort)
						@sortHead("MEAN", "mean", sort)
						@sortHead("ROWS", "rows", sort)
						@sortHead("CACHE", "hit", sort)
						<span>TREND</span><span>SAMPLE</span>
					</div>
					for _, r := range rows {
						<div class="grid-row" style={ "display:grid;" + queriesGrid }>
							<span class="mono" style="font-size:11px;color:var(--dim);">{ r.Fingerprint }</span>
							<span style="display:flex;align-items:center;gap:8px;min-width:0;">
								<span class="mono" style="font-size:11.5px;color:var(--text);overflow:hidden;text-overflow:ellipsis;white-space:nowrap;">{ r.NormalizedQuery }</span>
								if r.InsightCount > 0 {
									<a class="badge badge--t2" style="flex-shrink:0;" href={ templ.SafeURL(drilldownHref(r, sort.Nav)) }>{ fmt.Sprintf("%d▲", r.InsightCount) }</a>
								}
							</span>
							<span class="mono num-r" style="color:var(--mut);">{ fmt.Sprintf("%d", r.Calls) }</span>
							<span class="mono num-r" style="color:var(--mut);">{ fmt.Sprintf("%.0f", r.TotalTimeMs) }</span>
							<span class="mono num-r" style="color:var(--text);">{ fmt.Sprintf("%.1f", r.MeanTimeMs) }</span>
							<span class="mono num-r" style="color:var(--mut);">{ dashInt(r.Rows) }</span>
							<span class="mono num-r" style="color:var(--mut);">{ dashPct(r.CacheHitPct) }</span>
							<span>
								if r.SparkPoints != "" {
									<svg width="120" height="24" viewBox="0 0 120 24"><polyline points={ r.SparkPoints } fill="none" stroke="var(--acc)" stroke-width="1.2"></polyline></svg>
								}
							</span>
							<a class="badge" style="text-align:center;" href={ templ.SafeURL(drilldownHref(r, sort.Nav)) }>T2 ◈</a>
						</div>
					}
				</div>
			</div>
		}
	</div>
}

// sortHead renders a clickable column header carrying its sort arrow.
templ sortHead(label, col string, sort QuerySort) {
	<a class="num-r mono" style="cursor:pointer;color:var(--faint);" href={ templ.SafeURL(sortHref(col, sort)) }>{ label } { sortArrow(col, sort) }</a>
}
```

- [ ] **Step 9: Add the small render helpers**

Change the import block at the top of `web/design.go` from `import "strings"` to:

```go
import (
	"fmt"
	"strings"
)
```

Then append these five funcs to `web/design.go`:

```go
// drilldownHref builds the drilldown URL for a query row. A set ClusterID
// (scoped) yields the scoped drilldown page; empty ClusterID (fleet scope)
// falls back to nav.Plan so the link is never dead and never hardcodes a
// fleet literal in this helper.
func drilldownHref(r TopQuery, nav ScreenNav) string {
	if r.ClusterID == "" {
		return nav.Plan + "?fp=" + r.Fingerprint
	}
	return "/databases/" + r.ClusterID + "/query/" + r.Fingerprint
}

// sortHref toggles direction when the same column is re-picked. The base path
// comes from cur.Nav (fleet "/" today; scoped queries route under ly-ae6.3).
func sortHref(col string, cur QuerySort) string {
	dir := "desc"
	if cur.Col == col && cur.Dir == "desc" {
		dir = "asc"
	}
	return cur.Nav.Base + "?sort=" + col + "&dir=" + dir
}

// sortArrow shows ▼/▲ on the active column, "" otherwise.
func sortArrow(col string, cur QuerySort) string {
	if cur.Col != col {
		return ""
	}
	if cur.Dir == "asc" {
		return "▲"
	}
	return "▼"
}

// dashInt renders 0 as an em-dash (metric not yet collected).
func dashInt(n int64) string {
	if n == 0 {
		return "—"
	}
	return fmt.Sprintf("%d", n)
}

// dashPct renders a negative cache-hit ratio as an em-dash.
func dashPct(p float64) string {
	if p < 0 {
		return "—"
	}
	return fmt.Sprintf("%.0f%%", p)
}
```

- [ ] **Step 10: Wire sort/filter into the dashboard handler**

In `internal/api/dashboard.go`, locate `handleDashboard` + its `fetchTop`. Update the page handler to parse `sort`/`dir`/`q` params, build `web.QuerySort`, compute `MeanTimeMs`, populate `InsightCount` from a fingerprint→count map built off `fetchInsights(r)`, and call `sortAndFilterQueries`. Concretely, replace the body that renders `web.QueriesPage(rows)` with:

The existing signature is `fetchTop(r *http.Request, limit int) []web.TopQuery` (dashboard.go:25) — **pass the limit**; do not call `s.fetchTop(r)` (won't compile). Both handlers build the same `sort` (with `Nav`) and reuse `sortAndFilterQueries`:

```go
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	sort := web.QuerySort{
		Col: q1(r, "sort", "total"), Dir: q1(r, "dir", "desc"),
		Nav: web.ScreenNav{Base: "/", Plan: "/plan"}, // fleet routes; ly-ae6.3 refills under scope
	}
	rows := s.sortAndFilterQueries(s.fetchTop(r, 50), sort, r.URL.Query().Get("q"))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.QueriesPage(sort, rows).Render(r.Context(), w)
}

func (s *Server) handleQueriesPartial(w http.ResponseWriter, r *http.Request) {
	sort := web.QuerySort{
		Col: q1(r, "sort", "total"), Dir: q1(r, "dir", "desc"),
		Nav: web.ScreenNav{Base: "/", Plan: "/plan"},
	}
	rows := s.sortAndFilterQueries(s.fetchTop(r, 50), sort, r.URL.Query().Get("q"))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.QueriesTable(sort, rows).Render(r.Context(), w)
}
```

In `fetchTop`, after mapping each store row, set `MeanTimeMs: web.MeanMs(row.TotalTimeMs, row.Calls)`, `CacheHitPct: -1`, and after the loop populate insight counts:

```go
counts := map[string]int{}
for _, in := range s.fetchInsights(r) {
	counts[in.Fingerprint]++
}
for i := range out {
	out[i].InsightCount = counts[out[i].Fingerprint]
	out[i].MeanTimeMs = web.MeanMs(out[i].TotalTimeMs, out[i].Calls)
	out[i].CacheHitPct = -1
}
```

Add helper `q1` (in `dashboard.go` or a shared `internal/api/params.go`):

```go
// q1 returns query param key or def when absent/empty.
func q1(r *http.Request, key, def string) string {
	if v := r.URL.Query().Get(key); v != "" {
		return v
	}
	return def
}
```

- [ ] **Step 11: Regenerate templ and run all queries tests**

Run: `make templ && go test ./web/ -run TestQueriesScreen -v && go test ./internal/api/ -run 'TestSortAndFilter|TestDashboard' -v`
Expected: PASS. If `internal/api` has an existing `dashboard_test.go` asserting old markup, update its assertions to the new grid (`FINGERPRINT`, `data-screen-label="Top Queries"`).

- [ ] **Step 12: Commit**

```bash
git add web/queries.templ web/queries_templ.go web/queries_test.go web/layout.templ web/layout_templ.go web/design.go internal/api/queries.go internal/api/dashboard.go internal/api/queries_sort_test.go
git commit -m "feat(ui): retrofit Top Queries to token grid with sort/filter, mean/rows/cache/trend/sample columns, insight counts [ly-ae6.7]"
```

---

### Task 3: Query Drilldown screen (the insight's explanation) + T2 reveal gate

Covers COMPARISON `#### Queries screen` gaps: "Drilldown is an inline accordion … not the full drilldown screen: no 4-stat grid, no calls/min + mean-time trend charts, no wait-mix, no per-insight recommendation text"; "No T2 query-sample reveal + audit-on-read". Also `#### Insights` gap: "an insight's explanation IS the query drilldown". Replaces the inline accordion at `web/overview.templ:183-208` with a full drilldown screen reachable at `GET /databases/{clusterID}/query/{fingerprint}`.

**Files:**
- Create: `web/drilldown_vm.go` (view-model structs — pure Go, T2-safe)
- Create: `web/drilldown.templ` (`QueryDrilldownScreen`, `QueryDrilldownPage`)
- Create: `internal/api/drilldown.go` (`handleClusterQueryDrilldownPage`, `fetchDrilldown`)
- Modify: `internal/api/server.go:58` (add full-page route)
- Test: `web/drilldown_test.go` (create)
- Test: `internal/api/drilldown_test.go` (create)

**Interfaces:**
- Consumes: `ScreenHeader`, `HeaderBadge`, `EmptyState`, `SevClass`, `KindLabel`, `RecommendationFor`, `MeanMs` (Task 1); `fetchInsights(r) []web.InsightRow` (existing); `store.Stats.TopPlansByQuery(ctx, serverID, fp, since, until, limit)` and `TopQueriesByTotalTime` (existing).
- Produces:
```go
// web/drilldown_vm.go
type DrilldownStat struct{ Label, Value string }
type DrilldownInsight struct {
	KindLabel string
	Node      string
	SevClass  string // "crit"|"warn"|"info"
	Detail    string
	Rec       string
}
type DrilldownWait struct {
	Label    string
	Pct      string
	WidthPct int
	ColorVar string
}
// QuerySampleVM is the T2 gate. It NEVER carries a literal — Locked is the
// only state this plan renders; the reveal endpoint (ly-8b0.6) is out of scope.
type QuerySampleVM struct {
	Locked   bool
	Group    string // reveal-rights group label (identifier, not a literal)
	ServerID string
}
type DrilldownVM struct {
	ClusterID       string
	ServerID        string
	Fingerprint     string
	NormalizedQuery string
	HasPlan         bool
	Stats           []DrilldownStat
	Insights        []DrilldownInsight
	Waits           []DrilldownWait
	Sample          QuerySampleVM
	CallsTrend      string // "" until ly-xqf.10
	CallsArea       string
	MeanTrend       string
	Nav             ScreenNav // ← back + VIEW PLAN base paths (fleet default; ly-ae6.3 refills)
}
```
- Produces: `func (s *Server) fetchDrilldown(r *http.Request) web.DrilldownVM` reading `{clusterID}` + `{fingerprint}` path values.

- [ ] **Step 1: Write the failing VM/handler test**

Create `internal/api/drilldown_test.go`. **There is no `store.Stats` fake in this repo** — every `internal/api` test is `package api_test` and runs against a real testcontainer Postgres via `setup()` / `seedStats()` (`internal/api/server_test.go`). Do **not** invent an `emptyStats{}` double. The drilldown handler renders its static shell (fingerprint, ← back link, the T2 RAW SAMPLE gate, `data-screen-label`) from the path values even with **no** seeded plans/insights, so a real-but-empty stats DB exercises the route end-to-end in the repo's standard black-box style:

```go
package api_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/api"
)

func TestDrilldownPage_RendersScreenForFingerprint(t *testing.T) {
	_, srv := setup(t, api.Config{DevAuth: true})
	resp, err := http.Get(srv.URL + "/databases/orders-prod/query/3f2a")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	for _, want := range []string{"3f2a", "← TOP QUERIES", "RAW SAMPLE — TIER 2", `data-screen-label="Query drilldown"`} {
		if !strings.Contains(string(body), want) {
			t.Errorf("drilldown page missing %q", want)
		}
	}
	if strings.Contains(string(body), "system-ui") {
		t.Error("drilldown must not use legacy styling")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/api/ -run TestDrilldownPage -v`
Expected: FAIL — HTTP **404** (`status = 404, want 200`): the `GET /databases/{clusterID}/query/{fingerprint}` full-page route is not registered until Step 6. (It compiles — the test never references the not-yet-written handler by name.)

- [ ] **Step 3: Implement the view-model file**

Create `web/drilldown_vm.go` with the structs from Interfaces above (package `web`, no imports needed).

- [ ] **Step 4: Implement the drilldown templ**

Create `web/drilldown.templ`:

```go
package web

templ QueryDrilldownPage(vm DrilldownVM) {
	@Layout("Lynceus — query drilldown", "query drilldown") {
		@QueryDrilldownScreen(vm)
	}
}

templ QueryDrilldownScreen(vm DrilldownVM) {
	<div class="screen" data-screen-label="Query drilldown">
		<div class="screen-hd">
			<a class="mono" style="font-size:11px;" href={ templ.SafeURL(vm.Nav.Base) }>← TOP QUERIES</a>
			<span class="mono" style="font-size:16px;font-weight:600;">{ vm.Fingerprint }</span>
			<span class="badge badge--t1">T1 · NORMALIZED</span>
			<span style="flex:1;"></span>
			if vm.HasPlan {
				<a class="badge badge--live" style="padding:4px 9px;" href={ templ.SafeURL(vm.Nav.Plan + "?server=" + vm.ServerID + "&fp=" + vm.Fingerprint) }>VIEW PLAN →</a>
			}
		</div>
		<div class="panel mono" style="padding:12px 14px;font-size:12.5px;line-height:1.7;color:var(--text);white-space:pre-wrap;">{ vm.NormalizedQuery }</div>
		<div class="panel" style="display:grid;grid-template-columns:repeat(4,1fr);">
			for _, st := range vm.Stats {
				<div style="padding:10px 14px;border-right:1px solid var(--line2);display:flex;flex-direction:column;gap:2px;">
					<span class="mono-lbl" style="font-size:9.5px;letter-spacing:.1em;">{ st.Label }</span>
					<span class="mono" style="font-size:19px;font-weight:600;font-variant-numeric:tabular-nums;">{ st.Value }</span>
				</div>
			}
		</div>
		<div style="display:grid;grid-template-columns:1fr 1fr;gap:14px;">
			@trendPanel("CALLS / MIN", vm.CallsArea, vm.CallsTrend, "var(--acc)")
			@trendPanel("MEAN TIME", "", vm.MeanTrend, "var(--chart-lwlock)")
		</div>
		<div style="display:grid;grid-template-columns:5fr 4fr;gap:14px;align-items:start;">
			<div class="panel">
				<div class="panel-hd">DETECTED INSIGHTS · { intToStr(len(vm.Insights)) }</div>
				if len(vm.Insights) == 0 {
					@EmptyState("NO ANTI-PATTERNS DETECTED — NICE.")
				}
				for _, qi := range vm.Insights {
					<div class={ "stripe-" + qi.SevClass } style="padding:9px 12px;border-bottom:1px solid var(--line2);display:flex;flex-direction:column;gap:3px;">
						<div style="display:flex;gap:10px;align-items:center;">
							<span class={ "mono", "sev-" + qi.SevClass } style="font-size:11.5px;">{ qi.KindLabel }</span>
							<span class="mono" style="font-size:10px;color:var(--faint);">{ qi.Node }</span>
						</div>
						<span style="font-size:12px;color:var(--mut);">{ qi.Detail }</span>
						if qi.Rec != "" {
							<span style="font-size:12px;color:var(--text);">→ { qi.Rec }</span>
						}
					</div>
				}
			</div>
			<div style="display:flex;flex-direction:column;gap:14px;">
				<div class="panel" style="padding:12px 14px;display:flex;flex-direction:column;gap:8px;">
					<div class="panel-hd" style="border:0;padding:0;">WAIT BREAKDOWN</div>
					if len(vm.Waits) == 0 {
						<span class="mono-lbl" style="font-size:10px;">PER-QUERY WAIT ATTRIBUTION NOT YET COLLECTED (ly-xqf.1)</span>
					}
					for _, wt := range vm.Waits {
						<div style="display:flex;align-items:center;gap:10px;">
							<span class="mono" style="font-size:10.5px;color:var(--mut);width:140px;flex-shrink:0;">{ wt.Label }</span>
							<div class="bar"><div class="bar-fill" style={ "width:" + intToStr(wt.WidthPct) + "%;background:" + wt.ColorVar + ";" }></div></div>
							<span class="mono" style="font-size:10.5px;color:var(--dim);width:34px;text-align:right;">{ wt.Pct }</span>
						</div>
					}
				</div>
				@sampleGate(vm.Sample)
			</div>
		</div>
	</div>
}

// trendPanel draws the grid-lined chart card; the polyline is drawn only when
// series points exist (ly-xqf.10/11).
templ trendPanel(label, area, line, stroke string) {
	<div class="panel">
		<div class="panel-hd">{ label }</div>
		<svg width="100%" height="96" viewBox="0 0 480 96" preserveAspectRatio="none" style="display:block;">
			<line x1="0" y1="32" x2="480" y2="32" stroke="var(--line2)"></line>
			<line x1="0" y1="64" x2="480" y2="64" stroke="var(--line2)"></line>
			if area != "" {
				<polygon points={ area } fill="var(--accdim)"></polygon>
			}
			if line != "" {
				<polyline points={ line } fill="none" stroke={ stroke } stroke-width="1.5"></polyline>
			}
		</svg>
	</div>
}

// sampleGate renders the T2 RAW SAMPLE panel. It only ever renders the LOCKED
// state — no literal value is present in the VM. The reveal action posts to the
// audited T2 endpoint tracked by ly-8b0.6 (not wired here).
templ sampleGate(sm QuerySampleVM) {
	<div class="panel" style="border-color:var(--warn);">
		<div class="panel-hd" style="color:var(--warnT);display:flex;gap:8px;align-items:center;">
			◈ RAW SAMPLE — TIER 2
			<span style="flex:1;"></span>
			<span style="color:var(--faint);font-size:9.5px;">EVERY ACCESS IS LOGGED</span>
		</div>
		<div style="padding:16px 14px;display:flex;flex-direction:column;gap:10px;align-items:flex-start;">
			<span style="font-size:12px;color:var(--mut);line-height:1.6;">Raw samples may contain literal values and never leave this server's T2 store. Your group <span class="mono" style="font-size:11px;">{ sm.Group }</span> has reveal rights on <span class="mono" style="font-size:11px;">{ sm.ServerID }</span>.</span>
			<button class="badge badge--t2" style="padding:5px 12px;cursor:pointer;" hx-post={ "/api/t2/reveal?server=" + sm.ServerID } hx-swap="none" disabled?={ true }>REVEAL SAMPLE (AUDITED)</button>
		</div>
	</div>
}
```

- [ ] **Step 5: Add intToStr helper**

In `web/design.go`, append:

```go
// intToStr avoids a fmt import churn in templ files for plain ints.
func intToStr(n int) string { return fmt.Sprintf("%d", n) }
```

- [ ] **Step 6: Implement the handler + route**

Create `internal/api/drilldown.go`:

```go
package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/dobbo-ca/lynceus/web"
)

func (s *Server) handleClusterQueryDrilldownPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.QueryDrilldownPage(s.fetchDrilldown(r)).Render(r.Context(), w)
}

// fetchDrilldown assembles the drilldown VM from the plan store + insight
// engine. Trend series and per-query wait attribution are tracked separately
// (ly-xqf.10/11, ly-xqf.1); this fills what the store already provides.
func (s *Server) fetchDrilldown(r *http.Request) web.DrilldownVM {
	clusterID := r.PathValue("clusterID")
	fp := r.PathValue("fingerprint")
	now := time.Now().UTC()
	since := now.AddDate(0, 0, -30)

	vm := web.DrilldownVM{
		ClusterID:   clusterID,
		Fingerprint: fp,
		Sample:      web.QuerySampleVM{Locked: true, Group: "dba-oncall"},
		Nav:         web.ScreenNav{Base: "/", Plan: "/plan"}, // fleet routes; ly-ae6.3 refills under scope
	}

	// Aggregate calls/total across the fingerprint (fleet or cluster window).
	var calls int64
	var total float64
	if tq, err := s.stats.TopQueriesByTotalTime(r.Context(), since, now, 200); err == nil {
		for _, q := range tq {
			if q.Fingerprint == fp {
				calls, total = q.Calls, q.TotalTimeMs
				vm.NormalizedQuery = q.NormalizedQuery
				break
			}
		}
	}

	// Plans: pick the first plan's server, count variants for the 4th stat.
	planCount := 0
	if keys, err := s.stats.ListPlanKeys(r.Context(), since, now, 200); err == nil {
		for _, k := range keys {
			if k.Fingerprint != fp {
				continue
			}
			vm.ServerID = k.ServerID
			vm.Sample.ServerID = k.ServerID
			if plans, err := s.stats.TopPlansByQuery(r.Context(), k.ServerID, fp, since, now, 10); err == nil && len(plans) > 0 {
				vm.HasPlan = true
				planCount += len(plans)
			}
		}
	}

	vm.Stats = []web.DrilldownStat{
		{Label: "CALLS", Value: fmt.Sprintf("%d", calls)},
		{Label: "TOTAL MS", Value: fmt.Sprintf("%.0f", total)},
		{Label: "MEAN MS", Value: fmt.Sprintf("%.1f", web.MeanMs(total, calls))},
		{Label: "PLAN VARIANTS", Value: fmt.Sprintf("%d", planCount)},
	}

	// Insights for this fingerprint, mapped to the drilldown shape.
	for _, in := range s.fetchInsights(r) {
		if in.Fingerprint != fp {
			continue
		}
		cls := web.SevClass(in.Severity)
		vm.Insights = append(vm.Insights, web.DrilldownInsight{
			KindLabel: web.KindLabel(in.Kind),
			Node:      in.NodePath,
			SevClass:  cls,
			Detail:    in.Detail,
			Rec:       web.RecommendationFor(in.Kind),
		})
	}
	return vm
}
```

Register the new full-page route in `internal/api/server.go` after line 58:

```go
	s.mux.HandleFunc("GET /databases/{clusterID}/query/{fingerprint}", s.handleClusterQueryDrilldownPage)
```

- [ ] **Step 6b: Replace the inline overview accordion (closes COMPARISON `#### Queries` "inline accordion, not the full drilldown" + "two divergent query surfaces" + hardcoded 'slow scan' badge)**

This bead's queries retrofit **replaces** the inline accordion at `web/overview.templ` `OverviewQueries` — it does not leave a second, un-tokenized scoped queries surface behind. Route consolidation (folding `/databases/{clusterID}/queries` into the single Top Queries screen) stays ly-ae6.3's; the surrounding Overview page chrome (tiles/facts/topology) stays ly-ae6.6's. Do all three:

1. **Retrofit `OverviewQueries` to a token grid + deep-link.** Replace the whole `templ OverviewQueries` in `web/overview.templ` (`fmt` is already imported there) with:

```go
const overviewQGrid = "grid-template-columns:minmax(280px,1fr) 72px 72px 84px;gap:10px;"

// OverviewQueries renders the cluster's most-expensive queries as a token grid.
// Each row deep-links to the full Query Drilldown page (this task); the old
// inline #drill- accordion and the hardcoded "slow scan" badge are gone — the
// real HasInsight flag drives a ▲ marker.
templ OverviewQueries(vm OverviewVM) {
	<section id="queries" class="overview-section">
		<div class="panel">
			<div class="panel-hd">MOST EXPENSIVE QUERIES · LAST 24H</div>
			if len(vm.Queries) == 0 {
				@EmptyState("No query stats in the last 24 hours.")
			} else {
				<div class="grid-hd" style={ "display:grid;" + overviewQGrid }>
					<span>QUERY</span><span class="num-r">CALLS</span><span class="num-r">MEAN</span><span class="num-r">TOTAL</span>
				</div>
				for i := range vm.Queries {
					<a class="grid-row" style={ "display:grid;" + overviewQGrid + "text-decoration:none;" } href={ templ.SafeURL("/databases/" + vm.ClusterID + "/query/" + vm.Queries[i].Fingerprint) }>
						<span style="display:flex;align-items:center;gap:8px;min-width:0;">
							<span class="mono" style="font-size:11.5px;color:var(--text);overflow:hidden;text-overflow:ellipsis;white-space:nowrap;">{ vm.Queries[i].NormalizedQuery }</span>
							if vm.Queries[i].HasInsight {
								<span class="badge badge--t2" style="flex-shrink:0;">▲</span>
							}
						</span>
						<span class="mono num-r" style="color:var(--mut);">{ fmt.Sprintf("%d", vm.Queries[i].Calls) }</span>
						<span class="mono num-r" style="color:var(--text);">{ fmt.Sprintf("%.1f", vm.Queries[i].MeanMs) }</span>
						<span class="mono num-r" style="color:var(--mut);">{ fmt.Sprintf("%.0f", vm.Queries[i].TotalMs) }</span>
					</a>
				}
			}
		</div>
	</section>
}
```

2. **Delete the inline-accordion machinery** (its only job was the pre-drilldown-page popover, and it is the sole remaining caller of the now two-pane `PlanView` — Task 10):
   - Delete the `templ QueryDrilldown(plan PlanVM, insight *OverviewInsight)` component in `web/overview.templ` (lines ~212-225).
   - Delete `handleClusterQueryDrilldown` in `internal/api/overview.go` (lines ~25-55). Keep `toOverviewInsight` (still used by `toOverviewVM`).
   - Remove the route registration `s.mux.HandleFunc("GET /partial/databases/{clusterID}/query/{fingerprint}", s.handleClusterQueryDrilldown)` from `internal/api/server.go:58`.

3. **Replace the accordion test.** `internal/api/overview_test.go::TestQueryDrilldown_returnsFragment` (line ~137) hits the now-deleted `/partial/databases/{clusterID}/query/{fingerprint}` route — delete it and add a black-box test that the Overview page deep-links instead:

```go
// The cluster Overview "most expensive queries" table deep-links each row to the
// full Query Drilldown page (the old inline #drill- accordion was removed).
func TestClusterOverview_queriesDeepLinkToDrilldown(t *testing.T) {
	srv, clusterID, fp := setupOverview(t)
	resp, err := http.Get(srv.URL + "/databases/" + clusterID)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if want := "/databases/" + clusterID + "/query/" + fp; !strings.Contains(string(body), want) {
		t.Errorf("overview queries table missing deep-link %q", want)
	}
}
```

(`setupOverview` already returns `(srv, clusterID, fp)` — `internal/api/overview_test.go:19`. Ensure `io` is imported in that test file.)

- [ ] **Step 7: Regenerate templ and run tests**

Run: `make templ && go test ./web/ -run TestQueryDrilldown -v && go test ./internal/api/ -run TestDrilldownPage -v`
Expected: PASS. Add `web/drilldown_test.go`:

```go
package web

import (
	"context"
	"strings"
	"testing"
)

func TestQueryDrilldownScreen_StructureAndT2Gate(t *testing.T) {
	vm := DrilldownVM{
		ClusterID: "orders-prod", ServerID: "srv-1", Fingerprint: "3f2a", HasPlan: true,
		NormalizedQuery: "select * from orders where id=$1",
		Stats:           []DrilldownStat{{Label: "CALLS", Value: "1200"}},
		Insights:        []DrilldownInsight{{KindLabel: "SLOW SEQ SCAN", Node: "Seq Scan", SevClass: "crit", Detail: "scanned 1M rows", Rec: "add an index"}},
		Sample:          QuerySampleVM{Locked: true, Group: "dba-oncall", ServerID: "srv-1"},
	}
	var sb strings.Builder
	_ = QueryDrilldownScreen(vm).Render(context.Background(), &sb)
	html := sb.String()
	for _, want := range []string{
		"← TOP QUERIES", "VIEW PLAN →", "CALLS", "DETECTED INSIGHTS", "SLOW SEQ SCAN",
		"stripe-crit", "→ add an index", "WAIT BREAKDOWN", "RAW SAMPLE — TIER 2",
		"REVEAL SAMPLE (AUDITED)", "dba-oncall",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("drilldown missing %q", want)
		}
	}
}
```

- [ ] **Step 8: Verify no literal leaks into the VM (privacy guard test)**

Add to `web/drilldown_test.go`:

```go
func TestQuerySampleVM_HasNoLiteralField(t *testing.T) {
	// Compile-time contract: QuerySampleVM must never gain a raw-sample string.
	// This test documents intent; if a literal field is added it should be a T2
	// audited path, not a plain VM field. Kept as a review anchor.
	sm := QuerySampleVM{Locked: true}
	var sb strings.Builder
	_ = sampleGate(sm).Render(context.Background(), &sb)
	if strings.Contains(sb.String(), "SELECT") && !strings.Contains(sb.String(), "Raw samples may contain") {
		t.Error("sampleGate must not render an actual SQL literal in the locked state")
	}
}
```

Run: `go test ./web/ -run 'TestQueryDrilldown|TestQuerySampleVM' -v`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add web/drilldown_vm.go web/drilldown.templ web/drilldown_templ.go web/drilldown_test.go web/design.go web/overview.templ web/overview_templ.go internal/api/drilldown.go internal/api/drilldown_test.go internal/api/overview.go internal/api/overview_test.go internal/api/server.go
git commit -m "feat(ui): full Query Drilldown screen + retrofit overview queries to token deep-links, remove inline accordion (4-stat grid, trend charts, wait breakdown, recommendations, T2 reveal gate) [ly-ae6.7]"
```

---

### Task 4: Insights screen retrofit (filter chips, severity stripes, deep-link to drilldown)

Covers COMPARISON `#### Insights / query drilldown` gaps: token design system; CRIT/WARN/INFO mapping (engine emits low/medium/high); severity + kind filter chips; severity left-stripe rows; node/server context column; each insight row deep-links `fp →` to the drilldown (Task 3); recommendation text.

**Files:**
- Modify: `web/insights.templ` (full rewrite to `InsightsScreen`)
- Modify: `internal/api/insights.go` (add filter params + ClusterID/Rec/SevClass mapping)
- Test: `web/insights_test.go` (create)
- Test: `internal/api/insights_test.go` (extend — file exists)

**Interfaces:**
- Consumes: `ScreenHeader`, `HeaderBadge`, `EmptyState`, `SevClass`, `SevLabel`, `KindLabel`, `RecommendationFor`, `drilldownHref` (Tasks 1–2).
- Produces: extended `web.InsightRow` (add `ClusterID string`); templ `InsightsScreen(f InsightFilter, rows []InsightRow)` and `InsightsTable(f InsightFilter, rows []InsightRow)`; struct `InsightFilter{ Sev string; Kind string }` (empty = all).
- Produces: `func (s *Server) filterInsights(rows []web.InsightRow, f web.InsightFilter) []web.InsightRow` in `internal/api/insights.go`.

- [ ] **Step 1: Write the failing filter test**

`filterInsights` is an **unexported** method with an unexported receiver, so it can only be exercised white-box — from a `package api` file. The existing `internal/api/insights_test.go` is `package api_test` (black-box) and **cannot** call `s.filterInsights` on `&Server{}`. Create a **new** file `internal/api/insights_filter_test.go` with `package api` (Go allows `package api` and `package api_test` test files to coexist in the same dir — `internal/api/static_test.go` already declares `package api`):

```go
package api

import (
	"testing"

	"github.com/dobbo-ca/lynceus/web"
)

func TestFilterInsights_BySeverityAndKind(t *testing.T) {
	s := &Server{} // filterInsights is pure — no stats/conf touched
	rows := []web.InsightRow{
		{Kind: "slow_scan", Severity: "high", Fingerprint: "a"},
		{Kind: "disk_sort", Severity: "low", Fingerprint: "b"},
		{Kind: "slow_scan", Severity: "low", Fingerprint: "c"},
	}
	// Severity filter uses the mapped class: "crit" keeps high.
	if got := s.filterInsights(rows, web.InsightFilter{Sev: "crit"}); len(got) != 1 || got[0].Fingerprint != "a" {
		t.Errorf("crit sev filter wrong: %d rows", len(got))
	}
	if got := s.filterInsights(rows, web.InsightFilter{Kind: "slow_scan"}); len(got) != 2 {
		t.Errorf("kind filter wrong: %d rows", len(got))
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/api/ -run TestFilterInsights -v`
Expected: FAIL — `undefined: Server.filterInsights`, `undefined: web.InsightFilter`.

- [ ] **Step 3: Add InsightFilter + ClusterID and the filter func**

In `web/insights.templ`, add above `InsightRow`:

```go
// InsightFilter narrows the insights list. Empty fields impose no constraint.
// Nav carries the page-navigation base paths (fleet default; ly-ae6.3 refills).
type InsightFilter struct {
	Sev  string // mapped severity class: "crit"|"warn"|"info"|""
	Kind string // insight kind string, "" = all
	Nav  ScreenNav
}
```

Add `ClusterID string` to the `InsightRow` struct (after `ServerID`).

In `internal/api/insights.go`, add:

```go
// filterInsights keeps rows matching the mapped-severity and kind filter.
func (s *Server) filterInsights(rows []web.InsightRow, f web.InsightFilter) []web.InsightRow {
	out := rows[:0:0]
	for _, r := range rows {
		if f.Sev != "" && web.SevClass(r.Severity) != f.Sev {
			continue
		}
		if f.Kind != "" && r.Kind != f.Kind {
			continue
		}
		out = append(out, r)
	}
	return out
}
```

- [ ] **Step 4: Run the filter test**

Run: `go test ./internal/api/ -run TestFilterInsights -v`
Expected: PASS.

- [ ] **Step 5: Write the failing templ test**

Create `web/insights_test.go`:

```go
package web

import (
	"context"
	"strings"
	"testing"
)

func TestInsightsScreen_ChipsStripesAndDeepLink(t *testing.T) {
	rows := []InsightRow{{
		Kind: "slow_scan", Severity: "high", Fingerprint: "3f2a",
		Relation: "orders", NodePath: "Seq Scan(orders)", Detail: "scanned 1M rows",
		ServerID: "srv-1", ClusterID: "orders-prod",
	}}
	var sb strings.Builder
	_ = InsightsScreen(InsightFilter{}, rows).Render(context.Background(), &sb)
	html := sb.String()
	for _, want := range []string{
		"Query Insights", "SEVERITY", "KIND", "chip",
		"SLOW SEQ SCAN", "stripe-crit", "Seq Scan(orders)",
		"scanned 1M rows", "→ Add an index",
		"/databases/orders-prod/query/3f2a", // deep-link
	} {
		if !strings.Contains(html, want) {
			t.Errorf("InsightsScreen missing %q", want)
		}
	}
}
```

- [ ] **Step 6: Run to verify it fails**

Run: `go test ./web/ -run TestInsightsScreen -v`
Expected: FAIL — `undefined: InsightsScreen`.

- [ ] **Step 7: Rewrite insights.templ**

Replace the templ portion (keep the struct edits from Step 3) of `web/insights.templ`:

```go
// insightSevChips / insightKindChips are the fixed filter vocabularies.
var insightSevChips = []string{"crit", "warn", "info"}
var insightKindChips = []string{"slow_scan", "disk_sort", "hash_batches", "mis_estimate", "stale_stats", "nested_loop"}

templ InsightsPage(f InsightFilter, rows []InsightRow) {
	@Layout("Lynceus — insights", "query plan insights from the detection engine") {
		@InsightsScreen(f, rows)
	}
}

templ InsightsScreen(f InsightFilter, rows []InsightRow) {
	<div class="screen" data-screen-label="Query Insights">
		@ScreenHeader("Query Insights", []HeaderBadge{{Text: "LIVE", Kind: "live"}})
		<div style="display:flex;gap:6px;flex-wrap:wrap;align-items:center;">
			<span class="mono-lbl" style="font-size:9.5px;letter-spacing:.1em;">SEVERITY</span>
			for _, sv := range insightSevChips {
				<a class={ chipClass(f.Sev == sv) } href={ templ.SafeURL(insightChipHref("sev", sv, f)) }>{ SevLabel(sv) }</a>
			}
			<span style="width:14px;"></span>
			<span class="mono-lbl" style="font-size:9.5px;letter-spacing:.1em;">KIND</span>
			for _, kd := range insightKindChips {
				<a class={ chipClass(f.Kind == kd) } href={ templ.SafeURL(insightChipHref("kind", kd, f)) }>{ KindLabel(kd) }</a>
			}
		</div>
		@InsightsTable(f, rows)
	</div>
}

templ InsightsTable(f InsightFilter, rows []InsightRow) {
	<div id="insights-table" hx-get="/partial/insights" hx-trigger="every 10s" hx-swap="outerHTML">
		<div class="panel">
			if len(rows) == 0 {
				@EmptyState("NO INSIGHTS MATCH THIS FILTER — NICE.")
			}
			for _, r := range rows {
				<div class={ "stripe-" + SevClass(r.Severity) } style="padding:10px 14px;border-bottom:1px solid var(--line2);display:flex;flex-direction:column;gap:4px;">
					<div style="display:flex;gap:12px;align-items:center;">
						<span class={ "mono", "sev-" + SevClass(r.Severity) } style="font-size:12px;font-weight:600;">{ KindLabel(r.Kind) }</span>
						<span class="mono" style="font-size:10.5px;color:var(--faint);">{ r.NodePath }</span>
						<span style="flex:1;"></span>
						<a class="mono" style="font-size:10.5px;" href={ templ.SafeURL(insightDrilldownHref(r, f.Nav)) }>{ r.Fingerprint } →</a>
					</div>
					<span style="font-size:12.5px;color:var(--mut);">{ r.Detail }</span>
					if RecommendationFor(r.Kind) != "" {
						<span style="font-size:12.5px;color:var(--text);">→ { RecommendationFor(r.Kind) }</span>
					}
				</div>
			}
		</div>
	</div>
}
```

- [ ] **Step 8: Add chip/href helpers**

In `web/design.go`, append:

```go
// chipClass returns the token chip class, "on" variant when selected.
func chipClass(on bool) string {
	if on {
		return "chip chip--on"
	}
	return "chip"
}

// insightChipHref toggles one facet of the insights filter, preserving the
// other. Base comes from f.Nav (fleet "/insights" today; scoped route under
// ly-ae6.3) — never a hardcoded literal.
func insightChipHref(facet, val string, f InsightFilter) string {
	sev, kind := f.Sev, f.Kind
	if facet == "sev" {
		if sev == val {
			sev = ""
		} else {
			sev = val
		}
	} else {
		if kind == val {
			kind = ""
		} else {
			kind = val
		}
	}
	return f.Nav.Base + "?sev=" + sev + "&kind=" + kind
}

// insightDrilldownHref links an insight row to its query drilldown. Scoped rows
// (ClusterID set) use the scoped drilldown page; fleet rows fall back to
// nav.Plan (no hardcoded fleet literal in this helper).
func insightDrilldownHref(r InsightRow, nav ScreenNav) string {
	if r.ClusterID == "" {
		return nav.Plan + "?server=" + r.ServerID + "&fp=" + r.Fingerprint
	}
	return "/databases/" + r.ClusterID + "/query/" + r.Fingerprint
}
```

- [ ] **Step 9: Wire filter params into the handler**

In `internal/api/insights.go`, update `handleInsightsPage`/`handleInsightsPartial` to parse `sev`/`kind` and pass `web.InsightFilter`:

```go
func (s *Server) handleInsightsPage(w http.ResponseWriter, r *http.Request) {
	f := web.InsightFilter{
		Sev: r.URL.Query().Get("sev"), Kind: r.URL.Query().Get("kind"),
		Nav: web.ScreenNav{Base: "/insights", Plan: "/plan"}, // fleet routes; ly-ae6.3 refills under scope
	}
	rows := s.filterInsights(s.fetchInsights(r), f)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.InsightsPage(f, rows).Render(r.Context(), w)
}

func (s *Server) handleInsightsPartial(w http.ResponseWriter, r *http.Request) {
	f := web.InsightFilter{
		Sev: r.URL.Query().Get("sev"), Kind: r.URL.Query().Get("kind"),
		Nav: web.ScreenNav{Base: "/insights", Plan: "/plan"}, // fleet routes; ly-ae6.3 refills under scope
	}
	rows := s.filterInsights(s.fetchInsights(r), f)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.InsightsTable(f, rows).Render(r.Context(), w)
}
```

`fetchInsights` already maps rows; leave `ClusterID` empty at fleet scope (ly-ae6.2 fills it under cluster scope).

- [ ] **Step 9b: Retrofit the scoped `OverviewInsights` fragment (closes COMPARISON `#### Insights` "rows are not clickable/deep-linkable" + "un-tokenized scoped insights surface")**

The cluster Overview embeds `@OverviewInsights(vm.Insights)` — the second, un-tokenized insights surface the review flags. Tokenize it and make each row deep-link to the full drilldown (an insight's explanation IS the drilldown — PRODUCT_INTENT). The row needs the cluster id, so widen the component signature. Replace `templ OverviewInsights` in `web/overview.templ`:

```go
// OverviewInsights renders the cluster's insights as token rows, each
// deep-linking to the full Query Drilldown. Severity maps low/medium/high →
// crit/warn/info via SevClass/SevLabel (Task 1).
templ OverviewInsights(clusterID string, items []OverviewInsight) {
	<section id="insights" class="overview-section">
		<div class="panel">
			<div class="panel-hd">INSIGHTS · LAST 24H</div>
			if len(items) == 0 {
				@EmptyState("No insights in the last 24 hours.")
			}
			for i := range items {
				<a class={ "grid-row", "stripe-" + SevClass(items[i].Severity) } style="display:flex;align-items:center;gap:12px;text-decoration:none;padding:8px 12px;" href={ templ.SafeURL("/databases/" + clusterID + "/query/" + items[i].Fingerprint) }>
					<span class={ "mono", "sev-" + SevClass(items[i].Severity) } style="font-size:10px;font-weight:700;width:40px;">{ SevLabel(items[i].Severity) }</span>
					<span class="mono" style="font-size:11.5px;color:var(--acc2);">{ items[i].Relation }</span>
					<span style="font-size:12px;color:var(--mut);flex:1;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;">{ items[i].Detail }</span>
					<span class="mono" style="font-size:10.5px;color:var(--faint);">{ items[i].Fingerprint } →</span>
				</a>
			}
		</div>
	</section>
}
```

Update the caller in `templ OverviewPage`: `@OverviewInsights(vm.Insights)` → `@OverviewInsights(vm.ClusterID, vm.Insights)`. Update any `internal/api/overview_test.go` assertion that expects the old `<table><th>Severity</th>` insights markup to the new token rows (`stripe-`, the deep-link href). The surrounding Overview chrome (tiles/facts/topology) stays legacy — ly-ae6.6.

- [ ] **Step 10: Regenerate templ and run all insights tests**

Run: `make templ && go test ./web/ -run TestInsightsScreen -v && go test ./internal/api/ -run 'TestFilterInsights|TestInsights' -v`
Expected: PASS. Update any existing `insights_test.go` assertion that expects the old `<th>Severity</th>` table markup to the new grid.

- [ ] **Step 11: Commit**

```bash
git add web/insights.templ web/insights_templ.go web/insights_test.go web/design.go web/overview.templ web/overview_templ.go internal/api/insights.go internal/api/insights_test.go internal/api/insights_filter_test.go internal/api/overview_test.go
git commit -m "feat(ui): retrofit Insights to severity/kind chips, CRIT/WARN/INFO stripes, drilldown deep-links; tokenize scoped overview insights [ly-ae6.7]"
```

---

### Task 5: Index Advisor retrofit

Covers COMPARISON `#### Advisors` index gaps: token design; scope context caption; CREATE INDEX DDL string; EST. BENEFIT bar + %; expandable WHY rationale; EVIDENCE → query-drilldown link; evidence-based caption.

**Files:**
- Modify: `web/index_advisor.templ` (full rewrite to `IndexAdvisorScreen`)
- Modify: `internal/api/index_advisor.go` (populate DDL, BenefitPct, EvidenceFP, ClusterID)
- Test: `web/index_advisor_test.go` (create)

**Interfaces:**
- Consumes: `ScreenHeader`, `HeaderBadge`, `EmptyState` (Task 1); `drilldownHref` pattern (Task 2).
- Produces: extended `web.IndexAdvisorRow`:
```go
type IndexAdvisorRow struct {
	Relation   string
	Columns    string
	QueryCount int
	SizePretty string
	SeqScans   int64
	Rationale  string
	DDL        string // "CREATE INDEX ON <rel> (<cols>)" — derived; ly-u4t.13 refines
	BenefitPct int    // 0 hides the bar fill until ly-u4t.13
	EvidenceFP string // fingerprint for the EVIDENCE → drilldown link; populated NOW from IndexRecommendation.Fingerprints[0]
	ClusterID  string
	Cluster    string // scope crumb label
	Database   string
	Server     string
	Nav        ScreenNav // EVIDENCE fleet-fallback base; ly-ae6.3 refills under scope
}
```
- Produces: templ `IndexAdvisorScreen(rows []IndexAdvisorRow)`, `IndexAdvisorTable(rows []IndexAdvisorRow)`.

- [ ] **Step 1: Write the failing templ test**

Create `web/index_advisor_test.go`:

```go
package web

import (
	"context"
	"strings"
	"testing"
)

func TestIndexAdvisorScreen_DDLBenefitEvidence(t *testing.T) {
	rows := []IndexAdvisorRow{{
		Relation: "orders", Columns: "customer_id, created_at", QueryCount: 12,
		SizePretty: "500 MB", SeqScans: 900, Rationale: "seq scans on filtered columns",
		DDL: "CREATE INDEX ON orders (customer_id, created_at)", BenefitPct: 64,
		EvidenceFP: "3f2a", ClusterID: "orders-prod", Cluster: "orders-prod", Database: "orders", Server: "srv-1",
	}}
	var sb strings.Builder
	_ = IndexAdvisorScreen(rows).Render(context.Background(), &sb)
	html := sb.String()
	for _, want := range []string{
		"Index Advisor", "badge--live", "CREATE INDEX ON orders (customer_id, created_at)",
		"EST. BENEFIT", "64%", "EVIDENCE", "WHY",
		"/databases/orders-prod/query/3f2a", "orders-prod ▸ orders ▸ srv-1",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("IndexAdvisorScreen missing %q", want)
		}
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./web/ -run TestIndexAdvisorScreen -v`
Expected: FAIL — `undefined: IndexAdvisorScreen`.

- [ ] **Step 3: Rewrite index_advisor.templ**

Replace `web/index_advisor.templ` (struct + templ):

```go
package web

import "fmt"

type IndexAdvisorRow struct {
	Relation   string
	Columns    string
	QueryCount int
	SizePretty string
	SeqScans   int64
	Rationale  string
	DDL        string
	BenefitPct int
	EvidenceFP string
	ClusterID  string
	Cluster    string
	Database   string
	Server     string
	Nav        ScreenNav // EVIDENCE fleet fallback base (Nav.Plan); ly-ae6.3 refills under scope
}

templ IndexAdvisorPage(rows []IndexAdvisorRow) {
	@Layout("Lynceus — index advisor", "missing-index suggestions from plan evidence") {
		@IndexAdvisorScreen(rows)
	}
}

templ IndexAdvisorScreen(rows []IndexAdvisorRow) {
	<div class="screen" data-screen-label="Index Advisor">
		@ScreenHeader("Index Advisor", []HeaderBadge{
			{Text: "LIVE", Kind: "live"},
			{Text: "EVIDENCE-BASED — NO HYPOTHETICAL-INDEX EXTENSION REQUIRED", Kind: "t1"},
		})
		<div hx-get="/partial/index-advisor" hx-trigger="every 30s" hx-target="#idx-list" hx-swap="outerHTML"></div>
		@IndexAdvisorTable(rows)
	</div>
}

templ IndexAdvisorTable(rows []IndexAdvisorRow) {
	<div id="idx-list" style="display:flex;flex-direction:column;gap:14px;">
		if len(rows) == 0 {
			@EmptyState("No index suggestions — no seq-scanned filters in the last 30 days.")
		}
		for i, rec := range rows {
			<div class="panel">
				<div style="padding:11px 14px;display:flex;flex-direction:column;gap:9px;">
					<div style="display:flex;align-items:center;gap:12px;">
						<span class="mono" style="font-size:11px;color:var(--faint);">{ fmt.Sprintf("%02d", i+1) }</span>
						<span class="mono" style="font-size:12.5px;font-weight:600;color:var(--acc2);">{ rec.DDL }</span>
					</div>
					<div class="mono" style="display:flex;align-items:center;gap:20px;font-size:11px;color:var(--dim);flex-wrap:wrap;">
						<span class="badge" style="color:var(--mut);">{ rec.Cluster } ▸ { rec.Database } ▸ { rec.Server }</span>
						<span>TABLE <span style="color:var(--text);">{ rec.Relation }</span></span>
						<span>SIZE <span style="color:var(--text);">{ rec.SizePretty }</span> ({ fmt.Sprintf("%d seq scans", rec.SeqScans) })</span>
						if rec.EvidenceFP != "" {
							<span>EVIDENCE <a href={ templ.SafeURL(idxEvidenceHref(rec)) }>{ rec.EvidenceFP } →</a></span>
						}
						<span style="flex:1;"></span>
						<a style="cursor:pointer;color:var(--mut);" href={ templ.SafeURL(fmt.Sprintf("#why-%d", i)) }>WHY ▾</a>
					</div>
					<div style="display:flex;align-items:center;gap:10px;">
						<span class="mono" style="font-size:10px;color:var(--faint);width:110px;">EST. BENEFIT</span>
						<div class="bar"><div class="bar-fill" style={ fmt.Sprintf("width:%d%%;", rec.BenefitPct) }></div></div>
						<span class="mono" style="font-size:11.5px;color:var(--acc2);width:42px;text-align:right;">{ dashPctInt(rec.BenefitPct) }</span>
					</div>
					<details id={ fmt.Sprintf("why-%d", i) }>
						<summary class="mono" style="font-size:10px;color:var(--faint);cursor:pointer;">WHY</summary>
						<div style="font-size:12.5px;color:var(--mut);line-height:1.65;border-left:3px solid var(--acc);padding:8px 12px;background:var(--accbg);">{ rec.Rationale }</div>
					</details>
				</div>
			</div>
		}
	</div>
}
```

- [ ] **Step 4: Add idxEvidenceHref + dashPctInt helpers**

In `web/design.go`, append:

```go
// idxEvidenceHref links an index recommendation's evidence to the drilldown.
// Scoped rows use the scoped drilldown page; fleet rows fall back to r.Nav.Plan
// (no hardcoded fleet literal in this helper).
func idxEvidenceHref(r IndexAdvisorRow) string {
	if r.ClusterID == "" {
		return r.Nav.Plan + "?server=" + r.Server + "&fp=" + r.EvidenceFP
	}
	return "/databases/" + r.ClusterID + "/query/" + r.EvidenceFP
}

// dashPctInt renders 0% benefit as an em-dash (not yet quantified).
func dashPctInt(p int) string {
	if p <= 0 {
		return "—"
	}
	return fmt.Sprintf("%d%%", p)
}
```

- [ ] **Step 5: Populate the new fields in the handler**

In `internal/api/index_advisor.go`, inside the `for _, rec := range advisor.RecommendIndexes(...)` loop, extend the appended row. **`advisor.IndexRecommendation` already carries `Fingerprints []string` (index.go:59)** — populate `EvidenceFP` from it **now** so the `EVIDENCE → drilldown` link renders (the templ only shows the link `if rec.EvidenceFP != ""`; leaving it empty is the very gap COMPARISON flags). Only the scope crumb (`Cluster`/`Database`/`ClusterID`) awaits ly-u4t.12:

```go
		evfp := ""
		if len(rec.Fingerprints) > 0 {
			evfp = rec.Fingerprints[0] // evidence fingerprint is available TODAY (index.go:59)
		}
		out = append(out, web.IndexAdvisorRow{
			Relation:   rec.Relation,
			Columns:    strings.Join(rec.Columns, ", "),
			QueryCount: rec.QueryCount,
			SizePretty: prettyBytes(rec.TotalBytes),
			SeqScans:   rec.SeqScans,
			Rationale:  rec.Rationale,
			DDL:        fmt.Sprintf("CREATE INDEX ON %s (%s)", rec.Relation, strings.Join(rec.Columns, ", ")),
			BenefitPct: 0,    // ly-u4t.13 will quantify benefit; bar hidden until then
			EvidenceFP: evfp, // populated now → EVIDENCE link renders
			Nav:        web.ScreenNav{Base: "/index-advisor", Plan: "/plan"}, // fleet fallback; ly-ae6.3 refills under scope
			Server:     "",   // scope crumb Cluster/Database/Server + ClusterID fill when scope resolves (ly-u4t.12/ly-ae6.2)
		})
```

Ensure `"fmt"` is imported (it already is). The EVIDENCE link now renders at fleet scope as `/plan?server=&fp=<evfp>` (the `?server=` fills once per-server scope resolves); under cluster scope ly-ae6.3 sets `ClusterID` so the scoped drilldown path is used.

- [ ] **Step 6: Regenerate templ and run tests**

Run: `make templ && go test ./web/ -run TestIndexAdvisorScreen -v && go test ./internal/api/ -run TestIndexAdvisor -v`
Expected: PASS. Update existing `index_advisor_test.go` (internal/api) assertions from `<th>Relation</th>` table to the new card markup (`data-screen-label="Index Advisor"`, `CREATE INDEX`).

- [ ] **Step 7: Commit**

```bash
git add web/index_advisor.templ web/index_advisor_templ.go web/index_advisor_test.go web/design.go internal/api/index_advisor.go internal/api/index_advisor_test.go
git commit -m "feat(ui): retrofit Index Advisor to DDL cards with benefit bars, WHY expander, evidence links [ly-ae6.7]"
```

---

### Task 6: Vacuum Advisor retrofit (BLOAT / PERFORMANCE / ACTIVITY / FREEZING panels)

Covers COMPARISON `#### Advisors` vacuum gap: "renders one flat table instead of the 4 hi-fi panels: BLOAT dead-tuple bars, PERFORMANCE, ACTIVITY (last autovacuum/analyze), and FREEZING wraparound bars with freeze_max_age tick". All four panels are built from store data the handler already fetches (`latestTableStats`, `LatestFreezeAges`) plus the existing `advisor.VacuumAdvice` findings (only its `performance` category feeds the PERFORMANCE panel; the FREEZING panel is computed directly from `LatestFreezeAges`, so `advisor.FreezeAdvice` is deliberately not called) — no new store method.

**Files:**
- Modify: `web/vacuum_advisor.templ` (full rewrite to `VacuumAdvisorScreen` + `VacuumAdvisorVM`)
- Modify: `internal/api/vacuum_advisor.go` (build the 4-panel VM)
- Test: `web/vacuum_advisor_test.go` (create)

**Interfaces:**
- Consumes: `ScreenHeader`, `HeaderBadge`, `EmptyState` (Task 1); `store.TableStatRow{ObjectName, LiveTuples, DeadTuples, NModSinceAnalyze, LastVacuum, LastAutovacuum, TotalBytes}` and `store.FreezeAgeRow{FQN, XIDAge, MXIDAge}` (existing).
- Produces:
```go
type VacBloatRow struct {
	Relation     string
	PctLabel     string
	WidthPct     int
	ColorVar     string // var(--crit)|var(--warn)|var(--acc)
	Dead         string // "12,340 dead"
	Wasted       string // "48 MB" or ""
}
type VacPerfRow struct{ Label, Value, Detail string }
type VacActivityRow struct {
	Relation     string
	Last         string // "3h ago" | "never"
	LastColorVar string
	Analyze      string
}
type VacFreezeRow struct {
	Name     string
	Kind     string // "xid" | "mxid"
	AgeLabel string // "182M"
	PctLabel string // "91%"
	WidthPct int    // vs 260M hard limit
	ColorVar string
}
type VacuumAdvisorVM struct {
	ScopeLabel string
	Bloat      []VacBloatRow
	Perf       []VacPerfRow
	Activity   []VacActivityRow
	Freeze     []VacFreezeRow
}
```
- Produces: templ `VacuumAdvisorScreen(vm VacuumAdvisorVM)`, `VacuumAdvisorView(vm VacuumAdvisorVM)` (the swap fragment).

- [ ] **Step 1: Write the failing templ test**

Create `web/vacuum_advisor_test.go`:

```go
package web

import (
	"context"
	"strings"
	"testing"
)

func TestVacuumAdvisorScreen_FourPanels(t *testing.T) {
	vm := VacuumAdvisorVM{
		ScopeLabel: "orders-prod ▸ db orders ▸ srv-orders-primary",
		Bloat:      []VacBloatRow{{Relation: "orders", PctLabel: "37%", WidthPct: 37, ColorVar: "var(--warn)", Dead: "12,340 dead", Wasted: "48 MB"}},
		Perf:       []VacPerfRow{{Label: "autovacuum lag", Value: "2 tables", Detail: "dead tuples exceed threshold"}},
		Activity:   []VacActivityRow{{Relation: "orders", Last: "3h ago", LastColorVar: "var(--dim)", Analyze: "1d ago"}},
		Freeze:     []VacFreezeRow{{Name: "orders", Kind: "xid", AgeLabel: "182M", PctLabel: "91%", WidthPct: 70, ColorVar: "var(--warn)"}},
	}
	var sb strings.Builder
	_ = VacuumAdvisorScreen(vm).Render(context.Background(), &sb)
	html := sb.String()
	for _, want := range []string{
		"Vacuum Advisor", "BLOAT — DEAD TUPLE SHARE", "PERFORMANCE",
		"ACTIVITY — LAST AUTOVACUUM / ANALYZE", "FREEZING — WRAPAROUND RISK",
		"orders", "37%", "12,340 dead", "3h ago", "182M",
		"orders-prod ▸ db orders ▸ srv-orders-primary",
		"AUTOVACUUM_FREEZE_MAX_AGE",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("VacuumAdvisorScreen missing %q", want)
		}
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./web/ -run TestVacuumAdvisorScreen -v`
Expected: FAIL — `undefined: VacuumAdvisorVM`.

- [ ] **Step 3: Rewrite vacuum_advisor.templ**

Replace `web/vacuum_advisor.templ`:

```go
package web

templ VacuumAdvisorPage(vm VacuumAdvisorVM) {
	@Layout("Lynceus — vacuum advisor", "bloat / stats-freshness / autovacuum-lag findings") {
		@VacuumAdvisorScreen(vm)
	}
}

templ VacuumAdvisorScreen(vm VacuumAdvisorVM) {
	<div class="screen" data-screen-label="Vacuum Advisor">
		@ScreenHeader("Vacuum Advisor", []HeaderBadge{
			{Text: "LIVE", Kind: "live"},
			{Text: vm.ScopeLabel, Kind: "scope"},
			{Text: "BLOAT / PERFORMANCE / ACTIVITY / FREEZING", Kind: "t1"},
		})
		<div hx-get="/partial/vacuum-advisor" hx-trigger="every 30s" hx-target="#vac-view" hx-swap="outerHTML"></div>
		@VacuumAdvisorView(vm)
	</div>
}

templ VacuumAdvisorView(vm VacuumAdvisorVM) {
	<div id="vac-view" style="display:grid;grid-template-columns:1fr 1fr;gap:14px;align-items:start;">
		<div style="display:flex;flex-direction:column;gap:14px;">
			<div class="panel">
				<div class="panel-hd">BLOAT — DEAD TUPLE SHARE</div>
				if len(vm.Bloat) == 0 {
					@EmptyState("No bloat findings — tables are healthy.")
				}
				for _, b := range vm.Bloat {
					<div style="display:flex;align-items:center;gap:10px;padding:8px 12px;border-bottom:1px solid var(--line2);">
						<span class="mono" style="font-size:11.5px;width:170px;flex-shrink:0;">{ b.Relation }</span>
						<div class="bar"><div class="bar-fill" style={ "width:" + intToStr(b.WidthPct) + "%;background:" + b.ColorVar + ";" }></div></div>
						<span class="mono" style={ "font-size:11px;width:34px;text-align:right;color:" + b.ColorVar + ";" }>{ b.PctLabel }</span>
						<span class="mono" style="font-size:10.5px;color:var(--dim);width:120px;text-align:right;">{ b.Dead } · { b.Wasted }</span>
					</div>
				}
			</div>
			<div class="panel">
				<div class="panel-hd">PERFORMANCE</div>
				if len(vm.Perf) == 0 {
					@EmptyState("No autovacuum-lag findings.")
				}
				for _, p := range vm.Perf {
					<div style="padding:9px 12px;border-bottom:1px solid var(--line2);display:flex;flex-direction:column;gap:3px;">
						<div style="display:flex;justify-content:space-between;gap:10px;">
							<span class="mono" style="font-size:11.5px;">{ p.Label }</span>
							<span class="mono" style="font-size:11px;color:var(--acc2);">{ p.Value }</span>
						</div>
						<span style="font-size:12px;color:var(--mut);">{ p.Detail }</span>
					</div>
				}
			</div>
			<div class="panel">
				<div class="panel-hd">ACTIVITY — LAST AUTOVACUUM / ANALYZE</div>
				for _, v := range vm.Activity {
					<div class="mono" style="display:flex;align-items:center;gap:10px;padding:7px 12px;border-bottom:1px solid var(--line2);font-size:11.5px;">
						<span style="width:170px;">{ v.Relation }</span>
						<span style={ "width:90px;color:" + v.LastColorVar + ";" }>{ v.Last }</span>
						<span style="color:var(--dim);">analyze { v.Analyze }</span>
					</div>
				}
			</div>
		</div>
		<div class="panel">
			<div class="panel-hd" style="display:flex;justify-content:space-between;">
				<span>FREEZING — WRAPAROUND RISK</span><span>VS AUTOVACUUM_FREEZE_MAX_AGE (PER-SERVER)</span>
			</div>
			if len(vm.Freeze) == 0 {
				@EmptyState("No freeze-age data collected (ly-u4t.26).")
			}
			for _, f := range vm.Freeze {
				<div style="padding:10px 12px;border-bottom:1px solid var(--line2);display:flex;flex-direction:column;gap:5px;">
					<div class="mono" style="display:flex;justify-content:space-between;font-size:11.5px;">
						<span>{ f.Name } <span style="color:var(--faint);font-size:9.5px;">{ f.Kind }</span></span>
						<span style={ "color:" + f.ColorVar + ";" }>{ f.AgeLabel } · { f.PctLabel }</span>
					</div>
					<div style="height:12px;background:var(--raised);border-radius:1px;position:relative;">
						<div style={ "width:" + intToStr(f.WidthPct) + "%;height:12px;background:" + f.ColorVar + ";" }></div>
						<div style="position:absolute;left:76.9%;top:-2px;width:1px;height:16px;background:var(--dim);" title="autovacuum_freeze_max_age"></div>
					</div>
				</div>
			}
			<div class="mono-lbl" style="padding:9px 12px;font-size:10px;line-height:1.6;">TICK = EACH TABLE'S AUTOVACUUM_FREEZE_MAX_AGE (COLLECTED PER SERVER); BAR FULL WIDTH ≈ 1.3× THAT — THE HARD-WRAPAROUND GUARD.</div>
		</div>
	</div>
}
```

- [ ] **Step 4: Rebuild the handler VM**

Rewrite `internal/api/vacuum_advisor.go`'s `fetchVacuumAdvice` to return `web.VacuumAdvisorVM` (and update the two handlers to render `VacuumAdvisorPage`/`VacuumAdvisorView`). Add imports `"fmt"`, `"time"` (already), and the mapping:

```go
func (s *Server) handleVacuumAdvisorPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.VacuumAdvisorPage(s.fetchVacuumAdvice(r)).Render(r.Context(), w)
}

func (s *Server) handleVacuumAdvisorPartial(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.VacuumAdvisorView(s.fetchVacuumAdvice(r)).Render(r.Context(), w)
}

func (s *Server) fetchVacuumAdvice(r *http.Request) web.VacuumAdvisorVM {
	now := time.Now().UTC()
	since := now.AddDate(0, 0, -30)
	vm := web.VacuumAdvisorVM{ScopeLabel: r.URL.Query().Get("server")}
	keys, err := s.stats.ListPlanKeys(r.Context(), since, now, 200)
	if err != nil {
		return vm
	}
	servers := map[string]bool{}
	for _, k := range keys {
		servers[k.ServerID] = true
	}
	var tvi []advisor.TableVacuumInfo
	for srv := range servers {
		for _, ts := range latestTableStats(r, s, srv, now) {
			total := ts.LiveTuples + ts.DeadTuples
			pct := 0
			if total > 0 {
				pct = int(float64(ts.DeadTuples) / float64(total) * 100)
			}
			vm.Bloat = append(vm.Bloat, web.VacBloatRow{
				Relation: ts.ObjectName, PctLabel: fmt.Sprintf("%d%%", pct), WidthPct: pct,
				ColorVar: bloatColor(pct), Dead: fmt.Sprintf("%d dead", ts.DeadTuples),
				Wasted: prettyBytes(ts.TotalBytes),
			})
			vm.Activity = append(vm.Activity, web.VacActivityRow{
				Relation: ts.ObjectName, Last: agoLabel(ts.LastAutovacuum, now),
				LastColorVar: agoColor(ts.LastAutovacuum, now), Analyze: agoLabel(ts.LastVacuum, now),
			})
			tvi = append(tvi, advisor.TableVacuumInfo{
				Relation: ts.ObjectName, LiveTuples: ts.LiveTuples, DeadTuples: ts.DeadTuples,
				NModSinceAnalyze: ts.NModSinceAnalyze, LastVacuum: ts.LastVacuum, LastAutovacuum: ts.LastAutovacuum,
			})
		}
		if fz, err := s.stats.LatestFreezeAges(r.Context(), srv, now); err == nil {
			for _, f := range fz {
				// Per-row freeze budget from the collected GUC (freeze_ages.go:24);
				// fall back to PG's 200M default if unset. The hard-wraparound guard
				// is ~1.3× the freeze tick, which puts the tick at 76.9% of the bar
				// (the fixed marker position in the templ).
				freezeMax := f.AutovacuumFreezeMaxAge
				if freezeMax <= 0 {
					freezeMax = 200000000
				}
				hardLimit := freezeMax * 13 / 10
				pct := int(float64(f.XIDAge) / float64(hardLimit) * 100)
				vm.Freeze = append(vm.Freeze, web.VacFreezeRow{
					Name: f.FQN, Kind: "xid", AgeLabel: millions(f.XIDAge),
					PctLabel: fmt.Sprintf("%d%%", int(float64(f.XIDAge)/float64(freezeMax)*100)),
					WidthPct: pct, ColorVar: freezeColor(f.XIDAge, freezeMax),
				})
			}
		}
	}
	// VacuumAdvice returns bloat / performance / activity recs; the BLOAT and
	// ACTIVITY panels are already built from raw table stats above, so route ONLY
	// the performance (stale-stats → ANALYZE) category into the PERFORMANCE panel —
	// do not lump every category in. (FreezeAdvice is intentionally not called:
	// the FREEZING panel is driven directly from LatestFreezeAges above.)
	for _, rec := range advisor.VacuumAdvice(tvi, now) {
		if rec.Category != advisor.CatPerformance {
			continue
		}
		vm.Perf = append(vm.Perf, web.VacPerfRow{Label: rec.Relation, Value: string(rec.Category), Detail: rec.Detail})
	}
	return vm
}

func bloatColor(pct int) string {
	switch {
	case pct >= 40:
		return "var(--crit)"
	case pct >= 20:
		return "var(--warn)"
	default:
		return "var(--acc)"
	}
}

// freezeColor grades XID age against the per-row freeze budget: crit at/over the
// freeze tick, warn at 75% of it, else accent. Thresholds are relative to
// freezeMax (freeze_ages.go AutovacuumFreezeMaxAge), never hardcoded.
func freezeColor(age, freezeMax int64) string {
	switch {
	case age >= freezeMax:
		return "var(--crit)"
	case age >= freezeMax*3/4:
		return "var(--warn)"
	default:
		return "var(--acc)"
	}
}

func millions(n int64) string { return fmt.Sprintf("%dM", n/1000000) }

func agoLabel(t, now time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := now.Sub(t)
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func agoColor(t, now time.Time) string {
	if t.IsZero() || now.Sub(t) > 7*24*time.Hour {
		return "var(--critT)"
	}
	return "var(--dim)"
}
```

Verify field names on `store.TableStatRow` (`ObjectName`, `LiveTuples`, `DeadTuples`, `NModSinceAnalyze`, `LastVacuum`, `LastAutovacuum`, `TotalBytes`) and `store.FreezeAgeRow` (`FQN`, `XIDAge`, `MXIDAge`, `AutovacuumFreezeMaxAge`) with `grep -n "type TableStatRow\|type FreezeAgeRow" internal/store/*.go` before writing; adjust names to match exactly. Delete the old flat-table `fetchVacuumAdvice` body entirely — there is **no** leftover `freezes []advisor.TableFreezeInfo` accumulation (it was dead — never passed to `FreezeAdvice`) and no hardcoded 200M/260M constants.

- [ ] **Step 5: Regenerate templ and run tests**

Run: `make templ && go test ./web/ -run TestVacuumAdvisorScreen -v && go test ./internal/api/ -run TestVacuum -v`
Expected: PASS. Rewrite existing `internal/api/vacuum_advisor_test.go` expectations from the flat table VM to the new `VacuumAdvisorVM` (assert `vm.Bloat`/`vm.Freeze` are populated for seeded stats).

- [ ] **Step 6: Commit**

```bash
git add web/vacuum_advisor.templ web/vacuum_advisor_templ.go web/vacuum_advisor_test.go internal/api/vacuum_advisor.go internal/api/vacuum_advisor_test.go
git commit -m "feat(ui): retrofit Vacuum Advisor to BLOAT/PERFORMANCE/ACTIVITY/FREEZING panels [ly-ae6.7]"
```

---

### Task 7: Config Advisor retrofit (per-server picker + GROUP column + severity stripe + allowlist badge/footer)

Covers COMPARISON `#### Advisors` config gaps: "Config Advisor is NOT per-node … design shows a per-server picker (cfgServers) and 'SETTINGS APPLY PER SERVER INSTANCE'"; "UI missing severity left-stripe, GROUP column styling, T1/curated-allowlist badge, per-server picker, and allowlist footer note".

**Files:**
- Modify: `web/config_advisor.templ` (rewrite to `ConfigAdvisorScreen` + `ConfigAdvisorVM`)
- Modify: `internal/api/config_advisor.go` (build per-server tabs; run `ConfigAdvice` per server)
- Test: `web/config_advisor_test.go` (create)

**Interfaces:**
- Consumes: `ScreenHeader`, `HeaderBadge`, `EmptyState`, `SevClass` (Task 1); `store.Stats.RecentServerIDs`, `LatestSettings`, `advisor.ConfigAdvice([]advisor.ConfigSettingInput) []advisor.ConfigRecommendation` (existing).
- Produces:
```go
type ConfigServerTab struct {
	ID       string
	Label    string
	Sub      string // "N findings"
	Selected bool
}
type ConfigAdvisorRow struct {
	Group     string // category (GROUP column)
	Setting   string
	SevClass  string // "crit"|"warn"|"info"
	Current   string
	Suggested string
	Detail    string
}
type ConfigAdvisorVM struct {
	Servers   []ConfigServerTab
	ScopeName string // "<selected server> · CONFIG"
	Rows      []ConfigAdvisorRow
	Nav       ScreenNav // per-server tab base path (fleet default; ly-ae6.3 refills)
}
```
- Produces: templ `ConfigAdvisorScreen(vm ConfigAdvisorVM)`, `ConfigAdvisorTable(vm ConfigAdvisorVM)`.

- [ ] **Step 1: Write the failing templ test**

Create `web/config_advisor_test.go`:

```go
package web

import (
	"context"
	"strings"
	"testing"
)

func TestConfigAdvisorScreen_PerServerPickerAndGroupColumn(t *testing.T) {
	vm := ConfigAdvisorVM{
		Servers: []ConfigServerTab{
			{ID: "srv-1", Label: "srv-orders-primary", Sub: "3 findings", Selected: true},
			{ID: "srv-2", Label: "srv-orders-replica-1", Sub: "0 findings"},
		},
		ScopeName: "srv-orders-primary · CONFIG",
		Rows: []ConfigAdvisorRow{{
			Group: "MEMORY", Setting: "work_mem", SevClass: "warn",
			Current: "4MB", Suggested: "16MB", Detail: "disk sorts observed",
		}},
	}
	var sb strings.Builder
	_ = ConfigAdvisorScreen(vm).Render(context.Background(), &sb)
	html := sb.String()
	for _, want := range []string{
		"Config Advisor", "SETTINGS APPLY PER SERVER INSTANCE", "CURATED PG_SETTINGS ALLOWLIST",
		"srv-orders-primary", "3 findings", "chip--on",
		"GROUP", "SETTING", "CURRENT", "SUGGESTED", "RATIONALE",
		"MEMORY", "work_mem", "stripe-warn",
		"FREE-TEXT SETTINGS NEVER LEAVE THE SERVER",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("ConfigAdvisorScreen missing %q", want)
		}
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./web/ -run TestConfigAdvisorScreen -v`
Expected: FAIL — `undefined: ConfigAdvisorVM`.

- [ ] **Step 3: Rewrite config_advisor.templ**

Replace `web/config_advisor.templ`:

```go
package web

const cfgGrid = "grid-template-columns:120px 220px 110px 110px 1fr;gap:10px;"

templ ConfigAdvisorPage(vm ConfigAdvisorVM) {
	@Layout("Lynceus — config advisor", "settings-derived tuning recommendations") {
		@ConfigAdvisorScreen(vm)
	}
}

templ ConfigAdvisorScreen(vm ConfigAdvisorVM) {
	<div class="screen" data-screen-label="Config Advisor">
		@ScreenHeader("Config Advisor", []HeaderBadge{
			{Text: "LIVE", Kind: "live"},
			{Text: "T1 · VALUES FROM A CURATED PG_SETTINGS ALLOWLIST", Kind: "t1"},
			{Text: "SETTINGS APPLY PER SERVER INSTANCE", Kind: "scope"},
		})
		<div style="display:flex;gap:8px;flex-wrap:wrap;">
			for _, cs := range vm.Servers {
				<a class={ chipClass(cs.Selected) } style="display:flex;flex-direction:column;gap:2px;padding:6px 12px;" href={ templ.SafeURL(vm.Nav.Base + "?server=" + cs.ID) }>
					<span style="font-size:11.5px;font-weight:600;">{ cs.Label }</span>
					<span style="font-size:9px;color:var(--faint);letter-spacing:.06em;">{ cs.Sub }</span>
				</a>
			}
		</div>
		@ConfigAdvisorTable(vm)
		<div class="mono-lbl" style="font-size:10px;">ONLY ALLOWLISTED GUC VALUES (NUMBERS / BOOLS / ENUMS) ARE COLLECTED — FREE-TEXT SETTINGS NEVER LEAVE THE SERVER.</div>
	</div>
}

templ ConfigAdvisorTable(vm ConfigAdvisorVM) {
	<div id="config-table" class="panel" hx-get="/partial/config-advisor" hx-trigger="every 30s" hx-swap="outerHTML">
		<div class="panel-hd">{ vm.ScopeName }</div>
		<div class="grid-hd" style={ "display:grid;" + cfgGrid }>
			<span>GROUP</span><span>SETTING</span><span>CURRENT</span><span>SUGGESTED</span><span>RATIONALE</span>
		</div>
		if len(vm.Rows) == 0 {
			@EmptyState("No config findings — settings look well-tuned.")
		}
		for _, c := range vm.Rows {
			<div class={ "stripe-" + c.SevClass } style={ "display:grid;" + cfgGrid + "padding:8px 12px;border-bottom:1px solid var(--line2);align-items:baseline;" }>
				<span class="mono-lbl" style="font-size:9.5px;">{ c.Group }</span>
				<span class="mono" style="font-size:11.5px;">{ c.Setting }</span>
				<span class="mono" style="font-size:11.5px;color:var(--critT);">{ c.Current }</span>
				<span class="mono" style="font-size:11.5px;color:var(--acc2);">{ c.Suggested }</span>
				<span style="font-size:12px;color:var(--mut);line-height:1.5;">{ c.Detail }</span>
			</div>
		}
	</div>
}
```

- [ ] **Step 4: Rebuild the handler to per-server**

Rewrite `internal/api/config_advisor.go`'s `fetchConfigAdvice` to return `web.ConfigAdvisorVM`, running `advisor.ConfigAdvice` **per server** and selecting the `?server=` tab (default: first):

```go
func (s *Server) handleConfigAdvisorPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.ConfigAdvisorPage(s.fetchConfigAdvice(r)).Render(r.Context(), w)
}

func (s *Server) handleConfigAdvisorPartial(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.ConfigAdvisorTable(s.fetchConfigAdvice(r)).Render(r.Context(), w)
}

func (s *Server) fetchConfigAdvice(r *http.Request) web.ConfigAdvisorVM {
	now := time.Now().UTC()
	since := now.Add(-24 * time.Hour)
	vm := web.ConfigAdvisorVM{Nav: web.ScreenNav{Base: "/config-advisor"}} // fleet route; ly-ae6.3 refills under scope
	servers, err := s.stats.RecentServerIDs(r.Context(), since)
	if err != nil || len(servers) == 0 {
		return vm
	}
	sel := r.URL.Query().Get("server")
	if sel == "" {
		sel = servers[0]
	}
	for _, srv := range servers {
		var in []advisor.ConfigSettingInput
		if rows, err := s.stats.LatestSettings(r.Context(), srv, now); err == nil {
			for i := range rows {
				in = append(in, advisor.ConfigSettingInput{
					Name: rows[i].Name, Value: rows[i].Value, Unit: rows[i].Unit, Source: rows[i].Source,
				})
			}
		}
		recs := advisor.ConfigAdvice(in)
		vm.Servers = append(vm.Servers, web.ConfigServerTab{
			ID: srv, Label: srv, Sub: fmt.Sprintf("%d findings", len(recs)), Selected: srv == sel,
		})
		if srv == sel {
			vm.ScopeName = srv + " · CONFIG"
			for _, rec := range recs {
				vm.Rows = append(vm.Rows, web.ConfigAdvisorRow{
					Group: string(rec.Category), Setting: rec.Setting, SevClass: web.SevClass(string(rec.Severity)),
					Current: rec.Current, Suggested: rec.Suggested, Detail: rec.Detail,
				})
			}
		}
	}
	return vm
}
```

Add `"fmt"` to imports. Verify `advisor.ConfigRecommendation` field names (`Category`, `Setting`, `Severity`, `Current`, `Suggested`, `Detail`) with `grep -n "type ConfigRecommendation" internal/advisor/*.go`; adjust if different.

- [ ] **Step 5: Regenerate templ and run tests**

Run: `make templ && go test ./web/ -run TestConfigAdvisorScreen -v && go test ./internal/api/ -run TestConfig -v`
Expected: PASS. Update existing `internal/api/config_advisor_test.go` to assert `vm.Servers`/`vm.Rows` for the selected server rather than the old flat slice.

- [ ] **Step 6: Commit**

```bash
git add web/config_advisor.templ web/config_advisor_templ.go web/config_advisor_test.go internal/api/config_advisor.go internal/api/config_advisor_test.go
git commit -m "feat(ui): retrofit Config Advisor to per-server picker with GROUP column, severity stripes, allowlist badge [ly-ae6.7]"
```

---

### Task 8: Wait Events retrofit (per-class legend + per-server mix bars + histogram frame)

Covers COMPARISON `#### Activity / Waits` gaps: "Wait Events screen is a bare table, not the designed stacked histogram (24 buckets/60m) + per-class colored legend (avg per class) + per-server mix bars + LIVE badge + scope caption"; design tokens/mono/tabular-nums. Legend + mix bars are built from the existing `WaitEventHistogram` aggregate; the per-time-bucket stacked histogram needs bucketed wait data tracked by **ly-u4t.22** and renders a soon-state frame until it lands. (Connection-state bar and live-activity T2 view are cluster-detail/SQL-console scope — ly-ae6.6 / ly-ae6.8 — not this bead.)

**Files:**
- Modify: `web/waits.templ` (rewrite to `WaitsScreen` + `WaitsVM`)
- Modify: `internal/api/waits.go` (build the VM; add `waitColorVar`)
- Test: `web/waits_test.go` (create)

**Interfaces:**
- Consumes: `ScreenHeader`, `HeaderBadge`, `EmptyState`, `intToStr` (Task 1); `store.WaitEventCount{WaitEventType, WaitEvent, Total, Buckets}` + `store.Stats.WaitEventHistogram` (existing); `waitClass` (existing, `internal/api/waits.go:50`).
- Produces:
```go
type WaitLegend struct{ Key, Avg, ColorVar string }
type WaitSeg struct{ HeightPct int; ColorVar string }
type WaitBucket struct{ Title string; Segs []WaitSeg }
type WaitMixSeg struct{ Key string; WidthPct int; ColorVar string }
type WaitServerMix struct {
	Name        string
	TopClass    string
	TopColorVar string
	Mix         []WaitMixSeg
}
type WaitsVM struct {
	ServerID   string // the poll/refresh key (?server=) — the server id, NEVER the display label
	ScopeLabel string // human caption; today == ServerID, but ly-ae6.2 fills a resolved label
	Legend     []WaitLegend
	Buckets    []WaitBucket // empty until ly-u4t.22 → renders soon-state
	Servers    []WaitServerMix
}
```
- Produces: templ `WaitsScreen(vm WaitsVM)`, `WaitsView(vm WaitsVM)`; helper `func waitColorVar(class string) string` in `internal/api/waits.go`.

- [ ] **Step 1: Write the failing templ test**

Create `web/waits_test.go`:

```go
package web

import (
	"context"
	"strings"
	"testing"
)

func TestWaitsScreen_LegendMixAndCaption(t *testing.T) {
	vm := WaitsVM{
		ScopeLabel: "SRV-ORDERS-PRIMARY",
		Legend:     []WaitLegend{{Key: "IO / DataFileRead", Avg: "12", ColorVar: "var(--chart-io)"}},
		Servers: []WaitServerMix{{
			Name: "srv-orders-primary", TopClass: "IO / DataFileRead", TopColorVar: "var(--chart-io)",
			Mix: []WaitMixSeg{{Key: "IO / DataFileRead", WidthPct: 60, ColorVar: "var(--chart-io)"}},
		}},
	}
	var sb strings.Builder
	_ = WaitsScreen(vm).Render(context.Background(), &sb)
	html := sb.String()
	for _, want := range []string{
		"Wait Events", "badge--live", "SAMPLED FROM PG_STAT_ACTIVITY · ON-CPU PRESERVED",
		"IO / DataFileRead", "avg 12", "var(--chart-io)", "srv-orders-primary",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("WaitsScreen missing %q", want)
		}
	}
}

func TestWaitsScreen_HistogramSoonStateWhenNoBuckets(t *testing.T) {
	var sb strings.Builder
	_ = WaitsView(WaitsVM{ScopeLabel: "srv-1"}).Render(context.Background(), &sb)
	if !strings.Contains(sb.String(), "ly-u4t.22") {
		t.Error("empty histogram should cite the bucketed-data bead")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./web/ -run TestWaitsScreen -v`
Expected: FAIL — `undefined: WaitsVM`.

- [ ] **Step 3: Rewrite waits.templ**

Replace `web/waits.templ`:

```go
package web

templ WaitsPage(vm WaitsVM) {
	@Layout("Lynceus — wait events", "sampled wait-event breakdown") {
		@WaitsScreen(vm)
	}
}

templ WaitsScreen(vm WaitsVM) {
	<div class="screen" data-screen-label="Wait Events">
		@ScreenHeader("Wait Events", []HeaderBadge{
			{Text: "LIVE", Kind: "live"},
			{Text: vm.ScopeLabel + " · SAMPLED FROM PG_STAT_ACTIVITY · ON-CPU PRESERVED", Kind: "scope"},
		})
		<div hx-get={ "/partial/waits?server=" + vm.ServerID } hx-trigger="every 30s" hx-target="#waits-view" hx-swap="outerHTML"></div>
		@WaitsView(vm)
	</div>
}

templ WaitsView(vm WaitsVM) {
	<div id="waits-view" style="display:flex;flex-direction:column;gap:14px;">
		<div class="panel">
			<div class="panel-hd" style="display:flex;gap:16px;flex-wrap:wrap;color:var(--mut);">
				for _, l := range vm.Legend {
					<span><span style={ "color:" + l.ColorVar + ";" }>■</span> { l.Key } · avg { l.Avg }</span>
				}
			</div>
			if len(vm.Buckets) == 0 {
				<div class="mono-lbl" style="padding:24px 12px;font-size:10.5px;text-align:center;">PER-BUCKET WAIT HISTORY NOT YET COLLECTED (ly-u4t.22) — LEGEND AND PER-SERVER MIX SHOWN BELOW.</div>
			} else {
				<div style="padding:14px 12px 6px;display:flex;align-items:flex-end;gap:3px;height:190px;">
					for _, b := range vm.Buckets {
						<div title={ b.Title } style="flex:1;display:flex;flex-direction:column-reverse;gap:1px;height:100%;justify-content:flex-start;">
							for _, sg := range b.Segs {
								<div style={ "height:" + intToStr(sg.HeightPct) + "%;background:" + sg.ColorVar + ";border-radius:1px;" }></div>
							}
						</div>
					}
				</div>
				<div class="mono-lbl" style="padding:0 12px 10px;display:flex;justify-content:space-between;font-size:9.5px;">
					<span>-60m</span><span>-45m</span><span>-30m</span><span>-15m</span><span>now</span>
				</div>
			}
		</div>
		<div style="display:grid;grid-template-columns:repeat(2,1fr);gap:14px;">
			if len(vm.Servers) == 0 {
				@EmptyState("No sampled wait events for this scope in the window.")
			}
			for _, ws := range vm.Servers {
				<div class="panel" style="padding:10px 12px;display:flex;flex-direction:column;gap:6px;">
					<div class="mono" style="display:flex;justify-content:space-between;">
						<span style="font-size:11.5px;font-weight:600;">{ ws.Name }</span>
						<span style="font-size:10.5px;color:var(--dim);">top: <span style={ "color:" + ws.TopColorVar + ";" }>{ ws.TopClass }</span></span>
					</div>
					<div style="display:flex;height:8px;border-radius:1px;overflow:hidden;gap:1px;">
						for _, m := range ws.Mix {
							<div title={ m.Key } style={ "width:" + intToStr(m.WidthPct) + "%;background:" + m.ColorVar + ";" }></div>
						}
					</div>
				</div>
			}
		</div>
	</div>
}
```

- [ ] **Step 4: Rebuild the handler VM + color map**

Rewrite `internal/api/waits.go` handlers + `fetchWaits` to produce `web.WaitsVM`:

```go
func (s *Server) handleWaitsPage(w http.ResponseWriter, r *http.Request) {
	server := r.URL.Query().Get("server")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.WaitsPage(s.fetchWaits(r, server)).Render(r.Context(), w)
}

func (s *Server) handleWaitsPartial(w http.ResponseWriter, r *http.Request) {
	server := r.URL.Query().Get("server")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.WaitsView(s.fetchWaits(r, server)).Render(r.Context(), w)
}

func (s *Server) fetchWaits(r *http.Request, server string) web.WaitsVM {
	// ServerID is the poll key; ScopeLabel is the caption. They coincide today
	// (label defaults to the id) but ly-ae6.2 replaces ScopeLabel with a resolved
	// human label — keep ServerID separate so the ?server= poll never breaks.
	vm := web.WaitsVM{ServerID: server, ScopeLabel: server}
	if server == "" {
		return vm
	}
	now := time.Now().UTC()
	since := now.AddDate(0, 0, -7)
	counts, err := s.stats.WaitEventHistogram(r.Context(), server, since, now)
	if err != nil || len(counts) == 0 {
		return vm
	}
	var total int64
	for _, c := range counts {
		total += c.Total
	}
	var mix []web.WaitMixSeg
	topClass, topColor := "", "var(--chart-cpu)"
	var topN int64
	for _, c := range counts {
		class := waitClass(c)
		color := waitColorVar(class)
		avg := int64(0)
		if c.Buckets > 0 {
			avg = c.Total / c.Buckets
		}
		vm.Legend = append(vm.Legend, web.WaitLegend{Key: class, Avg: fmt.Sprintf("%d", avg), ColorVar: color})
		w := 0
		if total > 0 {
			w = int(float64(c.Total) / float64(total) * 100)
		}
		mix = append(mix, web.WaitMixSeg{Key: class, WidthPct: w, ColorVar: color})
		if c.Total > topN {
			topN, topClass, topColor = c.Total, class, color
		}
	}
	vm.Servers = []web.WaitServerMix{{Name: server, TopClass: topClass, TopColorVar: topColor, Mix: mix}}
	return vm
}

// waitColorVar maps a wait class to its chart token color.
func waitColorVar(class string) string {
	switch {
	case class == "CPU":
		return "var(--chart-cpu)"
	case strings.HasPrefix(class, "IO"):
		return "var(--chart-io)"
	case strings.HasPrefix(class, "LWLock"):
		return "var(--chart-lwlock)"
	case strings.HasPrefix(class, "Lock"):
		return "var(--chart-lock)"
	case strings.HasPrefix(class, "Client"):
		return "var(--chart-client)"
	default:
		return "var(--chart-cpu)"
	}
}
```

Add `"strings"` to imports (keep `"fmt"`, `"time"`, `"github.com/dobbo-ca/lynceus/internal/store"`, `"github.com/dobbo-ca/lynceus/web"`). Keep the existing `waitClass` func; drop the now-unused `pct` helper only if nothing else references it (grep first — it may be used by other handlers; leave it if so).

- [ ] **Step 5: Regenerate templ and run tests**

Run: `make templ && go test ./web/ -run TestWaitsScreen -v && go test ./internal/api/ -run TestWaits -v`
Expected: PASS. Rewrite `internal/api/waits_test.go` expectations from the old `[]web.WaitRow` to `web.WaitsVM` (assert `vm.Legend`/`vm.Servers` populated for seeded histogram).

- [ ] **Step 6: Commit**

```bash
git add web/waits.templ web/waits_templ.go web/waits_test.go internal/api/waits.go internal/api/waits_test.go
git commit -m "feat(ui): retrofit Wait Events to per-class legend, per-server mix bars, tokenized histogram frame [ly-ae6.7]"
```

---

### Task 9: Checks & Alerts retrofit (summary header, SERVER/FIRST SEEN columns, expandable history, MUTE toggle, expand deep-link)

Covers COMPARISON `#### Checks & Alerts` gaps: token design + LIVE badge + 'N FIRING · SETTINGS/VACUUM/…' summary header + 3px severity stripe; expandable rows with 'HISTORY · LAST 24H · CATEGORY' 24-cell sparkline; missing SERVER + FIRST SEEN columns (store persists `ServerID`+`EvaluatedAt`, VM drops them); MUTE/MUTED toggle button; expanded-check deep-link via `?expand={checkID}`.

**Files:**
- Modify: `internal/store/stats.go` (widen `Stats` interface with `ClearMute` — Step 0)
- Modify: `web/checks.templ` (rewrite to `ChecksScreen` + `ChecksVM`)
- Modify: `internal/api/checks.go` (firing-only filter, ServerID/FirstSeen/Expanded/Summary, ListMutes overlay, SetMute/ClearMute toggle)
- Modify: `internal/api/server.go` (register `POST /partial/checks/mute`)
- Test: `web/checks_test.go` (create)

**Interfaces:**
- Consumes: `ScreenHeader`, `HeaderBadge`, `EmptyState`, `SevClass`, `SevLabel` (Task 1); `store.ChecksResultRow{ServerID, EvaluatedAt, CheckID, Category, Severity, Status, Object, Detail, Muted}` (note `Status` — checks_results.go:19) + `store.Stats.LatestChecksResults`, `RecentServerIDs`, `ListMutes`, `SetMute` (all already on the interface, stats.go:48-50), and `ClearMute` (exists on `*pgxStats` at checks_results.go:131 — widened onto the interface in Step 0).
- Produces:
```go
type HistCell struct{ ColorVar, Title string }
type ChecksRow struct {
	Severity  string
	Category  string
	CheckID   string
	Object    string
	Detail    string
	ServerID  string
	FirstSeen string   // pre-formatted, e.g. "3h ago"
	Muted     bool
	Expanded  bool
	History   []HistCell // 24 cells; nil until ly-u4t.25 → soon-state
	Nav       ScreenNav  // ?expand= base path (fleet default; ly-ae6.3 refills)
}
type ChecksVM struct {
	Summary string // "7 FIRING · SETTINGS / VACUUM / WRAPAROUND"
	Rows    []ChecksRow
}
```
- Produces: templ `ChecksScreen(vm ChecksVM)`, `ChecksTable(vm ChecksVM)`.

- [ ] **Step 0: Expose `ClearMute` on the `store.Stats` interface**

The MUTE toggle needs to both mute and un-mute. `SetMute` and `ListMutes` are already on the interface (stats.go:49-50); `ClearMute` exists on the concrete `*pgxStats` (checks_results.go:131) but is **not** on the interface. Add it to the `Stats interface` block in `internal/store/stats.go`, right after `ListMutes` (line 50):

```go
	ClearMute(ctx context.Context, serverID, checkID, object string) error
```

This is a pure interface widening — `*pgxStats` already satisfies it, so `var _ Stats = (*pgxStats)(nil)` still holds and nothing else implements `store.Stats` (every consumer passes `store.NewStats(pool)`; there is no hand-written fake). Verify: `go build ./internal/store/... ./internal/api/... ./internal/checks/... ./internal/ingest/...` stays green.

- [ ] **Step 1: Write the failing templ test**

Create `web/checks_test.go`:

```go
package web

import (
	"context"
	"strings"
	"testing"
)

func TestChecksScreen_ColumnsSummaryStripeAndExpand(t *testing.T) {
	vm := ChecksVM{
		Summary: "2 FIRING · VACUUM / SETTINGS",
		Rows: []ChecksRow{
			{Severity: "high", Category: "vacuum", CheckID: "vacuum.wraparound", Object: "public.orders",
				Detail: "xid age high", ServerID: "srv-orders-primary", FirstSeen: "3h ago", Expanded: true,
				History: []HistCell{{ColorVar: "var(--crit)", Title: "-24h"}}},
			{Severity: "low", Category: "settings", CheckID: "settings.work_mem", Object: "cluster",
				Detail: "low work_mem", ServerID: "srv-orders-primary", FirstSeen: "1d ago", Muted: true},
		},
	}
	var sb strings.Builder
	_ = ChecksScreen(vm).Render(context.Background(), &sb)
	html := sb.String()
	for _, want := range []string{
		"Checks", "badge--live", "2 FIRING · VACUUM / SETTINGS",
		"SEVERITY", "CHECK", "OBJECT", "DETAIL", "SERVER", "FIRST SEEN",
		"vacuum.wraparound", "srv-orders-primary", "3h ago", "stripe-crit",
		"HISTORY · LAST 24H", "MUTE", "MUTED",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("ChecksScreen missing %q", want)
		}
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./web/ -run TestChecksScreen -v`
Expected: FAIL — `undefined: ChecksVM`.

- [ ] **Step 3: Rewrite checks.templ**

Replace `web/checks.templ`:

```go
package web

const checksGrid = "grid-template-columns:88px 230px 190px 1fr 150px 76px 60px;gap:10px;"

templ ChecksPage(vm ChecksVM) {
	@Layout("Lynceus — checks", "scheduled health checks + alerts") {
		@ChecksScreen(vm)
	}
}

templ ChecksScreen(vm ChecksVM) {
	<div class="screen" data-screen-label="Checks">
		@ScreenHeader("Checks", []HeaderBadge{
			{Text: "LIVE", Kind: "live"},
			{Text: vm.Summary, Kind: "scope"},
		})
		<div hx-get="/partial/checks" hx-trigger="every 30s" hx-target="#checks-table" hx-swap="outerHTML"></div>
		@ChecksTable(vm)
	</div>
}

templ ChecksTable(vm ChecksVM) {
	<div id="checks-table" class="panel">
		<div class="grid-hd" style={ "display:grid;" + checksGrid }>
			<span>SEVERITY</span><span>CHECK</span><span>OBJECT</span><span>DETAIL</span><span>SERVER</span><span>FIRST SEEN</span><span></span>
		</div>
		if len(vm.Rows) == 0 {
			@EmptyState("No firing checks — all monitored servers healthy.")
		}
		for _, c := range vm.Rows {
			<div style={ "border-bottom:1px solid var(--line2);opacity:" + mutedOpacity(c.Muted) + ";" }>
				<a class={ "grid-row", "stripe-" + SevClass(c.Severity) } style={ "display:grid;" + checksGrid + "cursor:pointer;text-decoration:none;" } href={ templ.SafeURL(checkExpandHref(c)) }>
					<span class={ "mono", "sev-" + SevClass(c.Severity) } style="font-size:10px;font-weight:700;">{ SevLabel(c.Severity) }</span>
					<span class="mono" style="font-size:11.5px;color:var(--text);">{ c.CheckID }</span>
					<span class="mono" style="font-size:11px;color:var(--mut);overflow:hidden;text-overflow:ellipsis;white-space:nowrap;">{ c.Object }</span>
					<span style="font-size:12px;color:var(--mut);overflow:hidden;text-overflow:ellipsis;white-space:nowrap;">{ c.Detail }</span>
					<span class="mono" style="font-size:11px;color:var(--dim);">{ c.ServerID }</span>
					<span class="mono" style="font-size:10.5px;color:var(--faint);">{ c.FirstSeen }</span>
					<button class="badge" style="cursor:pointer;" hx-post={ checkMuteHref(c) } hx-target="#checks-table" hx-swap="outerHTML">{ muteLabel(c.Muted) }</button>
				</a>
				if c.Expanded {
					<div style="padding:8px 12px 12px 103px;display:flex;flex-direction:column;gap:6px;">
						<span class="mono-lbl" style="font-size:9.5px;">HISTORY · LAST 24H · CATEGORY: { c.Category }</span>
						if len(c.History) == 0 {
							<span class="mono-lbl" style="font-size:10px;">PER-HOUR HISTORY NOT YET COLLECTED (ly-u4t.25)</span>
						} else {
							<div style="display:flex;gap:2px;max-width:560px;">
								for _, h := range c.History {
									<div style={ "flex:1;height:16px;border-radius:1px;background:" + h.ColorVar + ";" } title={ h.Title }></div>
								}
							</div>
						}
					</div>
				}
			</div>
		}
	</div>
}
```

- [ ] **Step 4: Add mute/expand/opacity helpers**

In `web/design.go`, append:

```go
func mutedOpacity(m bool) string {
	if m {
		return ".5"
	}
	return "1"
}

func muteLabel(m bool) string {
	if m {
		return "MUTED"
	}
	return "MUTE"
}

// checkExpandHref toggles the expanded row; re-clicking the open row collapses
// it. Base comes from c.Nav (fleet "/checks" today; scoped route under ly-ae6.3).
func checkExpandHref(c ChecksRow) string {
	if c.Expanded {
		return c.Nav.Base
	}
	return c.Nav.Base + "?expand=" + c.CheckID
}

// checkMuteHref points the mute toggle at the mute endpoint.
func checkMuteHref(c ChecksRow) string {
	return "/partial/checks/mute?server=" + c.ServerID + "&check=" + c.CheckID + "&object=" + c.Object
}
```

- [ ] **Step 5: Populate the new VM fields in the handler**

Rewrite `internal/api/checks.go` handlers + `fetchChecks` to build `web.ChecksVM`:

```go
func (s *Server) handleChecksPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.ChecksPage(s.fetchChecks(r)).Render(r.Context(), w)
}

func (s *Server) handleChecksPartial(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.ChecksTable(s.fetchChecks(r)).Render(r.Context(), w)
}

func (s *Server) fetchChecks(r *http.Request) web.ChecksVM {
	now := time.Now().UTC()
	since := now.AddDate(0, 0, -1)
	expand := r.URL.Query().Get("expand")
	nav := web.ScreenNav{Base: "/checks"} // fleet route; ly-ae6.3 refills under scope
	servers, err := s.stats.RecentServerIDs(r.Context(), since)
	if err != nil {
		return web.ChecksVM{}
	}
	var rows []web.ChecksRow
	cats := map[string]bool{}
	for _, srv := range servers {
		res, err := s.stats.LatestChecksResults(r.Context(), srv, since, now.Add(time.Minute))
		if err != nil {
			continue
		}
		// Overlay live mutes. LatestChecksResults.Muted only reflects mutes as of
		// the last scheduler run, but the MUTE toggle writes check_mutes directly
		// via SetMute — so a just-toggled mute must be read back from ListMutes for
		// the re-render to reflect it. Key by (check_id, object); an object=""
		// mute suppresses every object of that check.
		muted := map[string]bool{}
		checkWide := map[string]bool{}
		if ms, err := s.stats.ListMutes(r.Context(), srv); err == nil {
			for _, m := range ms {
				if m.Object == "" {
					checkWide[m.CheckID] = true
				} else {
					muted[m.CheckID+"\x00"+m.Object] = true
				}
			}
		}
		for i := range res {
			c := &res[i]
			// LatestChecksResults returns the latest result per (check, object)
			// across ALL statuses (ok/firing). Only FIRING checks belong on this
			// screen — filter before counting so "N FIRING" is accurate and the
			// "all healthy" empty state is reachable. (checks.StatusFiring="firing".)
			if c.Status != "firing" {
				continue
			}
			isMuted := c.Muted || checkWide[c.CheckID] || muted[c.CheckID+"\x00"+c.Object]
			cats[strings.ToUpper(c.Category)] = true
			rows = append(rows, web.ChecksRow{
				Severity: c.Severity, Category: c.Category, CheckID: c.CheckID,
				Object: c.Object, Detail: c.Detail, ServerID: c.ServerID,
				FirstSeen: agoLabel(c.EvaluatedAt, now), Muted: isMuted,
				Expanded: c.CheckID == expand, Nav: nav,
			})
		}
	}
	return web.ChecksVM{Summary: checksSummary(len(rows), cats), Rows: rows}
}

// checksSummary builds the "N FIRING · CAT / CAT" header line.
func checksSummary(n int, cats map[string]bool) string {
	if n == 0 {
		return "0 FIRING"
	}
	var cs []string
	for c := range cats {
		cs = append(cs, c)
	}
	sort.Strings(cs)
	return fmt.Sprintf("%d FIRING · %s", n, strings.Join(cs, " / "))
}
```

Add imports `"fmt"`, `"sort"`, `"strings"`. `agoLabel` is defined in `internal/api/vacuum_advisor.go` (Task 6) — same package, reuse it.

- [ ] **Step 6: Wire the MUTE toggle to real SetMute/ClearMute state**

The mute store methods exist and are on the interface (Step 0) — there is **no** no-op stub and **no** conditional. `handleChecksMute` toggles: if a mute already exists for (check, object) it clears it, else it sets a 24h mute, then re-renders the table. Because `fetchChecks` now overlays `ListMutes`, the button flips MUTE↔MUTED on the same round-trip and survives re-render:

```go
func (s *Server) handleChecksMute(w http.ResponseWriter, r *http.Request) {
	server := r.URL.Query().Get("server")
	check := r.URL.Query().Get("check")
	object := r.URL.Query().Get("object")

	muted := false
	if ms, err := s.stats.ListMutes(r.Context(), server); err == nil {
		for _, m := range ms {
			if m.CheckID == check && m.Object == object {
				muted = true
				break
			}
		}
	}
	if muted {
		_ = s.stats.ClearMute(r.Context(), server, check, object)
	} else {
		_ = s.stats.SetMute(r.Context(), server, check, object, time.Now().Add(24*time.Hour), "muted from checks UI")
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.ChecksTable(s.fetchChecks(r)).Render(r.Context(), w)
}
```

Register in `server.go`: `s.mux.HandleFunc("POST /partial/checks/mute", s.handleChecksMute)`. `checkMuteHref` (Step 4) already targets this route. ly-u4t.20 tracks alert *routing/notification*, not mute storage — mute persistence ships here.

- [ ] **Step 6b: Integration-test the mute round-trip**

Add a black-box test in `internal/api/checks_test.go` (`package api_test`) that seeds a firing check + settings, POSTs the mute endpoint, and asserts the re-rendered table shows `MUTED` (and a second POST flips it back to `MUTE`). This exercises SetMute→ListMutes overlay→ClearMute end-to-end against real Postgres via `setup()`/`seedStats()`. Seed a firing `ChecksResultRow` with `store.NewStats(pool).WriteChecksResults(...)` (Status `"firing"`), then:

```go
resp, _ := http.Post(srv.URL+"/partial/checks/mute?server=srv&check=vacuum.wraparound&object=public.orders", "", nil)
body, _ := io.ReadAll(resp.Body)
if !strings.Contains(string(body), "MUTED") { t.Fatal("first POST should mute") }
```

- [ ] **Step 7: Regenerate templ and run tests**

Run: `make templ && go test ./web/ -run TestChecksScreen -v && go test ./internal/api/ -run TestChecks -v`
Expected: PASS. Rewrite `internal/api/checks_test.go` expectations to `web.ChecksVM` (assert `vm.Summary` and `vm.Rows[i].ServerID`/`FirstSeen` populated).

- [ ] **Step 8: Commit**

```bash
git add internal/store/stats.go web/checks.templ web/checks_templ.go web/checks_test.go web/design.go internal/api/checks.go internal/api/server.go internal/api/checks_test.go
git commit -m "feat(ui): retrofit Checks to summary header, SERVER/FIRST-SEEN columns, expandable 24h history, firing-only filter, real SetMute/ClearMute toggle, expand deep-link [ly-ae6.7]"
```

---

### Task 10: Query Plan visualization retrofit (two-pane, node-detail, problem detection, variant tabs)

Covers COMPARISON `#### Query plan visualization` gaps: two-pane 3fr/2fr plan-tree | NODE-DETAIL grid; NODE DETAIL pane (EST/ACTUAL ROWS, TOTAL COST, LOOPS, extra info, problem note); click-to-select with `?node=`; problem-node detection (est vs act ratio) with red stripe + PROBLEM NODE badge + color-coded est→act; plan-variant tabs; LIVE / T1 header badges; design tokens. (Extra proto node attrs — Workers/Buckets/Batches/Memory — are tracked by ly-u4t.6; render the attrs `PlanNodeVM` already carries.)

**Files:**
- Modify: `web/plan_vm.go` (add Idx/Problem/EstColorVar; add `PlanVariant`; add `DecoratePlan`)
- Modify: `web/plan.templ` (rewrite `PlanView` to two-pane)
- Modify: `internal/api/plan.go` (load variants; parse `?plan`/`?node`; decorate)
- Test: `web/plan_vm_test.go` (extend — file exists)
- Test: `web/plan_test.go` (create rendered-HTML test)

**Interfaces:**
- Consumes: `ScreenHeader`, `HeaderBadge`, `EmptyState`, `intToStr` (Task 1); `store.Stats.TopPlansByQuery` (existing).
- Produces: extended `PlanNodeVM` (add `Idx int`, `Problem bool`, `EstColorVar string`); new:
```go
type PlanVariant struct {
	FP       string
	Label    string // "seen 12× · 4.2ms"
	Selected bool
	Href     string
}
```
- Produces: extended `PlanVM` (add `Variants []PlanVariant`, `SelectedIdx int`, `Selected *PlanNodeVM`); `func DecoratePlan(vm *PlanVM, selectedIdx int)`.
- Produces: templ `PlanScreen(vm PlanVM)`, rewritten `PlanView(vm PlanVM)`.

- [ ] **Step 1: Write the failing decorate test**

Add to `web/plan_vm_test.go`:

```go
func TestDecoratePlan_FlagsProblemNodeAndSelects(t *testing.T) {
	root := &PlanNodeVM{NodeType: "Nested Loop", PlanRows: 10, ActualRows: 10}
	child := &PlanNodeVM{NodeType: "Seq Scan", Relation: "orders", PlanRows: 5, ActualRows: 5000}
	root.Children = []*PlanNodeVM{child}
	vm := PlanVM{Root: root}
	flatten(root, &vm.Flat)
	DecoratePlan(&vm, 1)
	if vm.Flat[0].Idx != 0 || vm.Flat[1].Idx != 1 {
		t.Fatalf("Idx not assigned: %d %d", vm.Flat[0].Idx, vm.Flat[1].Idx)
	}
	if !vm.Flat[1].Problem {
		t.Error("Seq Scan 5→5000 (1000x) should be flagged a problem node")
	}
	if vm.Flat[0].Problem {
		t.Error("Nested Loop 10→10 should not be a problem node")
	}
	if vm.Selected == nil || vm.Selected.NodeType != "Seq Scan" {
		t.Error("selected node should be Flat[1]")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./web/ -run TestDecoratePlan -v`
Expected: FAIL — `undefined: DecoratePlan`, `PlanNodeVM has no field Idx`.

- [ ] **Step 3: Extend plan_vm.go**

Add fields `Idx int`, `Problem bool`, `EstColorVar string` to `PlanNodeVM`; add `Variants []PlanVariant`, `SelectedIdx int`, `Selected *PlanNodeVM` to `PlanVM`; add the `PlanVariant` struct; and append `DecoratePlan`:

```go
// DecoratePlan assigns flat indices, flags misestimated nodes (actual vs plan
// rows off by >10x in either direction), colors the est→act, and selects the
// node at selectedIdx (clamped). Call after ToPlanVM builds Root+Flat.
func DecoratePlan(vm *PlanVM, selectedIdx int) {
	for i, n := range vm.Flat {
		n.Idx = i
		ratio := misestimateRatio(n.PlanRows, n.ActualRows)
		if ratio >= 10 {
			n.Problem = true
			n.EstColorVar = "var(--critT)"
		} else {
			n.EstColorVar = "var(--dim)"
		}
	}
	if len(vm.Flat) == 0 {
		return
	}
	if selectedIdx < 0 || selectedIdx >= len(vm.Flat) {
		selectedIdx = 0
	}
	vm.SelectedIdx = selectedIdx
	vm.Selected = vm.Flat[selectedIdx]
}

// misestimateRatio is the larger of plan/actual and actual/plan (guarding zero).
func misestimateRatio(plan, actual int64) float64 {
	p, a := float64(plan), float64(actual)
	if p < 1 {
		p = 1
	}
	if a < 1 {
		a = 1
	}
	if a > p {
		return a / p
	}
	return p / a
}
```

- [ ] **Step 4: Run the decorate test**

Run: `go test ./web/ -run TestDecoratePlan -v`
Expected: PASS.

- [ ] **Step 5: Rewrite plan.templ to two-pane**

Replace the templ portion of `web/plan.templ`:

```go
package web

import "fmt"

templ PlanPage(vm PlanVM) {
	@Layout("Lynceus — query plan", "most recent normalized plan") {
		@PlanScreen(vm)
	}
}

templ PlanScreen(vm PlanVM) {
	<div class="screen" data-screen-label="Plans">
		@ScreenHeader("Plans", []HeaderBadge{
			{Text: "LIVE", Kind: "live"},
			{Text: "T1 · CONDITIONS NORMALIZED, NO LITERALS", Kind: "t1"},
		})
		@PlanView(vm)
	</div>
}

templ PlanView(vm PlanVM) {
	<div id="plan-view">
		if vm.Empty {
			@EmptyState("No plan stored for this server and fingerprint.")
		} else {
			if len(vm.Variants) > 1 {
				<div style="display:flex;gap:8px;flex-wrap:wrap;margin-bottom:14px;">
					for _, pt := range vm.Variants {
						<a class={ chipClass(pt.Selected) } href={ templ.SafeURL(pt.Href) }>{ pt.FP } · { pt.Label }</a>
					}
				</div>
			}
			<div style="display:grid;grid-template-columns:3fr 2fr;gap:14px;align-items:start;">
				<div class="panel scroll-x">
					<div class="panel-hd">PLAN TREE — CLICK A NODE</div>
					<div style="padding:10px 12px;display:flex;flex-direction:column;gap:4px;min-width:560px;">
						for _, n := range vm.Flat {
							<a class={ planNodeStripe(n) } style={ planNodeStyle(n) } href={ templ.SafeURL(planNodeHref(vm, n.Idx)) }>
								<span class="mono" style={ "font-size:12px;font-weight:600;color:" + planTypeColor(n) + ";" }>{ n.NodeType }</span>
								if n.Relation != "" {
									<span class="mono" style="font-size:11.5px;color:var(--acc2);">{ n.Relation }</span>
								}
								<span style="flex:1;"></span>
								<span class="mono" style="font-size:10.5px;color:var(--dim);">est <span style={ "color:" + n.EstColorVar + ";" }>{ fmt.Sprintf("%d", n.PlanRows) }</span> → act <span style={ "color:" + n.EstColorVar + ";" }>{ fmt.Sprintf("%d", n.ActualRows) }</span></span>
								<span class="mono" style="font-size:10.5px;color:var(--faint);">cost { FmtCost(n.TotalCost) } · loops { fmt.Sprintf("%d", n.ActualLoops) }</span>
							</a>
						}
					</div>
				</div>
				<div class="panel">
					<div class="panel-hd">NODE DETAIL</div>
					if vm.Selected != nil {
						@planNodeDetail(vm.Selected)
					}
				</div>
			</div>
		}
	</div>
}

templ planNodeDetail(n *PlanNodeVM) {
	<div style="padding:12px 14px;display:flex;flex-direction:column;gap:9px;">
		<div style="display:flex;gap:8px;align-items:center;">
			<span class="mono" style="font-size:13px;font-weight:600;">{ n.NodeType }</span>
			if n.Relation != "" {
				<span class="mono" style="font-size:12px;color:var(--acc2);">{ n.Relation }</span>
			}
			if n.Problem {
				<span class="badge" style="color:var(--critT);border-color:var(--crit);">PROBLEM NODE</span>
			}
		</div>
		<div style="display:grid;grid-template-columns:max-content 1fr;gap:4px 16px;">
			<span class="mono-lbl" style="font-size:10.5px;">EST ROWS</span><span class="mono" style="font-variant-numeric:tabular-nums;">{ fmt.Sprintf("%d", n.PlanRows) }</span>
			<span class="mono-lbl" style="font-size:10.5px;">ACTUAL ROWS</span><span class="mono" style={ "font-variant-numeric:tabular-nums;color:" + n.EstColorVar + ";" }>{ fmt.Sprintf("%d", n.ActualRows) }</span>
			<span class="mono-lbl" style="font-size:10.5px;">TOTAL COST</span><span class="mono">{ FmtCost(n.TotalCost) }</span>
			<span class="mono-lbl" style="font-size:10.5px;">LOOPS</span><span class="mono">{ fmt.Sprintf("%d", n.ActualLoops) }</span>
		</div>
		if n.Condition != "" || n.JoinType != "" || n.ScanDirection != "" {
			<div class="mono" style="font-size:11px;color:var(--mut);background:var(--raised);border:1px solid var(--line2);padding:7px 9px;border-radius:1px;line-height:1.6;">
				if n.JoinType != "" {
					<div>{ n.JoinType } join</div>
				}
				if n.ScanDirection != "" {
					<div>scan { n.ScanDirection }</div>
				}
				if n.Condition != "" {
					<div>cond { n.Condition }</div>
				}
			</div>
		}
		if n.Problem {
			<div style="font-size:12px;color:var(--mut);border-left:3px solid var(--crit);padding:6px 10px;background:var(--critbg);line-height:1.6;">Estimated and actual row counts differ by more than 10× — the planner is mis-estimating this node. Consider ANALYZE or extended statistics.</div>
		}
	</div>
}
```

**Delete** the existing recursive `templ PlanTreeNode(n *PlanNodeVM)` in `web/plan.templ` (the two-pane flat list replaces it) and any test referencing it. After this rewrite the **only** callers of `PlanView` are `handlePlanPage` and `handlePlanPartial` (internal/api/plan.go), and **both** route through the decorating `fetchPlan` (Step 7) so `PlanView` always receives a decorated VM (`Flat`/`Selected` populated). The third caller — the overview accordion `QueryDrilldown` — was removed in Task 3 Step 6b, so there is no undecorated `PlanView` call left to break. Confirm with `grep -rn "PlanView\|PlanTreeNode" web/ internal/api/ | grep -v _templ.go` after the edit: only `plan.templ`, `plan.go`, and the plan tests should match.

- [ ] **Step 6: Add plan node render helpers**

In `web/design.go`, append:

```go
func planNodeStripe(n *PlanNodeVM) string {
	if n.Problem {
		return "stripe-crit"
	}
	return ""
}

func planNodeStyle(n *PlanNodeVM) string {
	return fmt.Sprintf("margin-left:%dpx;border:1px solid var(--line);background:var(--surface);border-radius:2px;padding:7px 10px;display:flex;align-items:center;gap:10px;text-decoration:none;", n.Depth*18)
}

func planTypeColor(n *PlanNodeVM) string {
	if n.Problem {
		return "var(--critT)"
	}
	return "var(--text)"
}

func planNodeHref(vm PlanVM, idx int) string {
	return fmt.Sprintf("/plan?server=%s&fp=%s&plan=%d&node=%d", vm.ServerID, vm.Fingerprint, vm.SelectedIdx, idx)
}
```

Note: `planNodeHref` uses `vm.SelectedIdx` to keep the current variant; adjust to a `plan` param if you track variant index separately. Keep it simple: variant selection uses `?plan=<i>` set by the variant tab's `Href`.

- [ ] **Step 7: Load variants + parse params in the handler**

Rewrite `internal/api/plan.go`'s `fetchPlan` to load up to 5 variants, select via `?plan`, decorate with `?node`:

```go
func (s *Server) fetchPlan(r *http.Request) web.PlanVM {
	q := r.URL.Query()
	serverID := q.Get("server")
	fp := q.Get("fp")
	now := time.Now().UTC()
	since := now.AddDate(0, 0, -30)

	plans, err := s.stats.TopPlansByQuery(r.Context(), serverID, fp, since, now, 5)
	if err != nil || len(plans) == 0 {
		return web.ToPlanVM(serverID, nil)
	}
	planIdx := atoiDefault(q.Get("plan"), 0)
	if planIdx < 0 || planIdx >= len(plans) {
		planIdx = 0
	}
	vm := web.ToPlanVM(serverID, plans[planIdx].Plan)
	vm.Fingerprint = fp
	for i, p := range plans {
		vm.Variants = append(vm.Variants, web.PlanVariant{
			FP:       shortFP(fp),
			Label:    fmt.Sprintf("variant %d", i+1),
			Selected: i == planIdx,
			Href:     fmt.Sprintf("/plan?server=%s&fp=%s&plan=%d", serverID, fp, i),
		})
	}
	web.DecoratePlan(&vm, atoiDefault(q.Get("node"), 0))
	return vm
}

func atoiDefault(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return def
}

func shortFP(fp string) string {
	if len(fp) > 8 {
		return fp[:8]
	}
	return fp
}
```

Add imports `"fmt"`, `"strconv"`. `QueryPlanRow` has a `.Plan` field (existing). If `plans[i].Plan` carries a per-variant seen-count/mean-time, use them for `Label`; otherwise `variant N` is honest until ly-xqf.11 adds those.

- [ ] **Step 8: Write the render test, regen, run**

Create `web/plan_test.go`:

```go
package web

import (
	"context"
	"strings"
	"testing"
)

func TestPlanScreen_TwoPaneWithProblemNode(t *testing.T) {
	root := &PlanNodeVM{NodeType: "Seq Scan", Relation: "orders", PlanRows: 5, ActualRows: 5000, TotalCost: 1234}
	vm := PlanVM{ServerID: "srv-1", Fingerprint: "3f2affff", Root: root}
	flatten(root, &vm.Flat)
	DecoratePlan(&vm, 0)
	var sb strings.Builder
	_ = PlanScreen(vm).Render(context.Background(), &sb)
	html := sb.String()
	for _, want := range []string{
		"Plans", "badge--live", "PLAN TREE — CLICK A NODE", "NODE DETAIL",
		"PROBLEM NODE", "EST ROWS", "ACTUAL ROWS", "stripe-crit", "orders",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("PlanScreen missing %q", want)
		}
	}
}
```

Run: `make templ && go test ./web/ -run 'TestDecoratePlan|TestPlanScreen|TestToPlanVM' -v && go test ./internal/api/ -run TestPlan -v`
Expected: PASS. Update `internal/api/plan_test.go` if it asserts the old single-table markup.

- [ ] **Step 9: Commit**

```bash
git add web/plan_vm.go web/plan_vm_test.go web/plan.templ web/plan_templ.go web/plan_test.go web/design.go internal/api/plan.go internal/api/plan_test.go
git commit -m "feat(ui): retrofit Query Plan to two-pane tree|node-detail with problem detection and variant tabs [ly-ae6.7]"
```

---

### Task 11: Audit Log retrofit (hash-chain header, TARGET/HASH columns, tier badge, T2 amber striping, action color)

Covers COMPARISON `#### Governance: Audit Log` gaps: verified-tip header banner (VerifyChain) + per-row HASH column (store fetches prev/row hash but VM discards); TARGET column (extract from Detail JSON); tier badge + T2 amber border-left stripe + bg; action color-coding; design tokens + LIVE badge + tamper-evident subtitle. Placement under the user-menu GOVERNANCE section is ly-ae6.2's nav concern — noted, not implemented here. (Keep the existing filter form — it is an intentional bonus beyond the design.)

**Files:**
- Modify: `internal/store/config.go:15-31` (add `VerifyChain` to the `Config` interface — impl already exists at `config.go:284`)
- Modify: `web/audit.templ` (rewrite to `AuditScreen` + `AuditVM` with new columns + banner)
- Modify: `internal/api/audit.go` (extract Target, hashes, verify chain)
- Test: `web/audit_test.go` (create)
- Test: `internal/api/audit_test.go` (extend — file exists)

**Interfaces:**
- Consumes: `ScreenHeader`, `HeaderBadge`, `EmptyState` (Task 1); `store.AuditRecord{ID, Actor, Action, ServerID, DataTier, Detail []byte, At, PrevHash, RowHash}` + `store.Config.ListAudit` (existing); `store.Config.VerifyChain(ctx, since, until) (int, string, error)` (**newly exposed on the interface**).
- Produces: extended `web.AuditRow`:
```go
type AuditRow struct {
	ID             int64
	Actor          string
	Action         string
	ServerID       string
	DataTier       int16
	Detail         string
	At             string
	Target         string // extracted from Detail JSON; "—" if none
	HashShort      string // "c4b7…2ef"
	ActionColorVar string // T2 read actions warn-colored, mutations accent
	StripeVar      string // "var(--warn)" for T2, "var(--line2)" otherwise
	BgVar          string // "var(--warnbg)" for T2, "transparent" otherwise
	TierLabel      string // "T1" | "T2"
}
type AuditVM struct {
	Filter        AuditFilterValues
	Rows          []AuditRow
	ChainVerified bool
	TipShort      string
	Count         int
}
```
- Produces: templ `AuditScreen(vm AuditVM)`, `AuditTable(vm AuditVM)` (fragment); keep `AuditFilterForm(f AuditFilterValues)` unchanged.

- [ ] **Step 1: Expose VerifyChain on the Config interface**

In `internal/store/config.go`, add to the `Config interface` block (after `ListAudit`, line 17):

```go
	VerifyChain(ctx context.Context, since, until time.Time) (int, string, error)
```

Ensure `"time"` is imported in `config.go` (it is). This is a pure interface widening — the method already exists on `*pgxConfig` (line 284), so `var _ Config = (*pgxConfig)(nil)` still holds.

- [ ] **Step 2: Verify it still builds**

Run: `go build ./internal/store/...`
Expected: PASS (no error — the concrete type already satisfies the widened interface).

- [ ] **Step 3: Write the failing templ test**

Create `web/audit_test.go`:

```go
package web

import (
	"context"
	"strings"
	"testing"
)

func TestAuditScreen_BannerColumnsAndT2Stripe(t *testing.T) {
	vm := AuditVM{
		Filter:        AuditFilterValues{},
		ChainVerified: true, TipShort: "c4b7…2ef", Count: 42,
		Rows: []AuditRow{{
			ID: 7, Actor: "chris", Action: "t2.read.query_sample", ServerID: "srv-1",
			DataTier: 2, At: "2026-07-10T12:00:00Z", Target: "public.orders",
			HashShort: "9a1f…be2", ActionColorVar: "var(--warnT)",
			StripeVar: "var(--warn)", BgVar: "var(--warnbg)", TierLabel: "T2",
		}},
	}
	var sb strings.Builder
	_ = AuditScreen(vm).Render(context.Background(), &sb)
	html := sb.String()
	for _, want := range []string{
		"Audit Log", "badge--live", "HASH CHAIN VERIFIED", "c4b7…2ef", "42 EVENTS",
		"TIMESTAMP", "ACTOR", "ACTION", "TARGET", "SERVER", "TIER", "HASH",
		"t2.read.query_sample", "public.orders", "9a1f…be2",
		"var(--warnbg)", "T2",
		"TAMPER-EVIDENT",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("AuditScreen missing %q", want)
		}
	}
}
```

- [ ] **Step 4: Run to verify it fails**

Run: `go test ./web/ -run TestAuditScreen -v`
Expected: FAIL — `undefined: AuditVM`.

- [ ] **Step 5: Rewrite audit.templ table + add screen/banner (keep the filter form)**

In `web/audit.templ`: keep the `AuditFilterValues` struct and `AuditFilterForm`. Replace `AuditRow`, `AuditPage`, and `AuditTable` with:

```go
const auditGrid = "grid-template-columns:168px 110px 210px 1fr 150px 44px 84px;gap:10px;"

// AuditRow — see Interfaces block for full field list.
type AuditRow struct {
	ID             int64
	Actor          string
	Action         string
	ServerID       string
	DataTier       int16
	Detail         string
	At             string
	Target         string
	HashShort      string
	ActionColorVar string
	StripeVar      string
	BgVar          string
	TierLabel      string
}

type AuditVM struct {
	Filter        AuditFilterValues
	Rows          []AuditRow
	ChainVerified bool
	TipShort      string
	Count         int
}

templ AuditPage(vm AuditVM) {
	@Layout("Lynceus — audit log", "tamper-evident audit log") {
		@AuditScreen(vm)
	}
}

templ AuditScreen(vm AuditVM) {
	<div class="screen" data-screen-label="Audit Log">
		<div class="screen-hd">
			<span class="screen-title">Audit Log</span>
			<span class="badge badge--live">LIVE</span>
			<span style="flex:1;"></span>
			if vm.ChainVerified {
				<span class="badge badge--live" style="padding:4px 10px;background:var(--accbg);">✓ HASH CHAIN VERIFIED · TIP { vm.TipShort } · { intToStr(vm.Count) } EVENTS</span>
			} else {
				<span class="badge badge--t2" style="padding:4px 10px;">✗ HASH CHAIN BROKEN — SEE OPS</span>
			}
		</div>
		<div class="mono-lbl" style="font-size:10px;">TAMPER-EVIDENT: EACH EVENT'S HASH COVERS THE PREVIOUS. EVERY T2 READ APPEARS HERE — NO EXCEPTIONS.</div>
		@AuditFilterForm(vm.Filter)
		@AuditTable(vm)
	</div>
}

templ AuditTable(vm AuditVM) {
	<div id="audit-table" class="panel">
		<div class="grid-hd" style={ "display:grid;" + auditGrid }>
			<span>TIMESTAMP</span><span>ACTOR</span><span>ACTION</span><span>TARGET</span><span>SERVER</span><span>TIER</span><span>HASH</span>
		</div>
		if len(vm.Rows) == 0 {
			@EmptyState("No audit entries match the current filter.")
		}
		for _, a := range vm.Rows {
			<div class="mono" style={ "display:grid;" + auditGrid + "padding:7px 12px;border-bottom:1px solid var(--line2);border-left:3px solid " + a.StripeVar + ";background:" + a.BgVar + ";align-items:center;" }>
				<span style="font-size:10.5px;color:var(--dim);font-variant-numeric:tabular-nums;">{ a.At }</span>
				<span style="font-size:11px;">{ a.Actor }</span>
				<span style={ "font-size:11px;color:" + a.ActionColorVar + ";" }>{ a.Action }</span>
				<span style="font-size:11px;color:var(--mut);overflow:hidden;text-overflow:ellipsis;white-space:nowrap;">{ a.Target }</span>
				<span style="font-size:10.5px;color:var(--dim);">{ a.ServerID }</span>
				<span class="badge" style="text-align:center;">{ a.TierLabel }</span>
				<span style="font-size:10.5px;color:var(--faint);">{ a.HashShort }</span>
			</div>
		}
	</div>
}
```

- [ ] **Step 6: Build Target/hash/color in the handler**

Rewrite `internal/api/audit.go`'s `fetchAudit` to return `web.AuditVM` (and update both handlers). Add imports `"encoding/hex"`, `"encoding/json"`:

```go
func (s *Server) handleAuditPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.AuditPage(s.fetchAudit(r)).Render(r.Context(), w)
}

func (s *Server) handleAuditPartial(w http.ResponseWriter, r *http.Request) {
	vm := s.fetchAudit(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.AuditTable(vm).Render(r.Context(), w)
}

func (s *Server) fetchAudit(r *http.Request) web.AuditVM {
	q := r.URL.Query()
	values := web.AuditFilterValues{
		Actor: q.Get("actor"), Action: q.Get("action"), ServerID: q.Get("server"),
		Since: q.Get("since"), Until: q.Get("until"), Tier: q.Get("tier"),
	}
	filter := store.AuditFilter{Actor: values.Actor, Action: values.Action, ServerID: values.ServerID, Limit: 200}
	if t, err := time.Parse(dateLayout, values.Since); err == nil {
		filter.Since = t
	}
	if t, err := time.Parse(dateLayout, values.Until); err == nil {
		filter.Until = t.Add(24*time.Hour - time.Nanosecond)
	}
	if n, err := strconv.Atoi(values.Tier); err == nil && (n == 1 || n == 2) {
		tier := int16(n)
		filter.Tier = &tier
	}

	recs, err := s.conf.ListAudit(r.Context(), filter)
	vm := web.AuditVM{Filter: values}
	if err != nil {
		return vm
	}
	for i := range recs {
		rec := &recs[i]
		t2 := rec.DataTier == 2
		vm.Rows = append(vm.Rows, web.AuditRow{
			ID: rec.ID, Actor: rec.Actor, Action: rec.Action, ServerID: rec.ServerID,
			DataTier: rec.DataTier, Detail: string(rec.Detail),
			At:             rec.At.UTC().Format(time.RFC3339),
			Target:         auditTarget(rec.Detail),
			HashShort:      shortHash(rec.RowHash),
			ActionColorVar: actionColor(rec.Action, t2),
			StripeVar:      ternary(t2, "var(--warn)", "var(--line2)"),
			BgVar:          ternary(t2, "var(--warnbg)", "transparent"),
			TierLabel:      ternary(t2, "T2", "T1"),
		})
	}
	vm.Count = len(vm.Rows)
	if len(recs) > 0 {
		vm.TipShort = shortHash(recs[0].RowHash) // ListAudit is id DESC → newest first
	}
	// VerifyChain returns -1 when the chain is INTACT (config.go:272); any value
	// >= 0 is the 0-based ordinal of the first tampered row. Do NOT write
	// `bad == 0` — ordinal 0 is a REAL tamper at the very first row, and an
	// intact chain is -1. Use `bad < 0`.
	if bad, _, err := s.conf.VerifyChain(r.Context(), time.Time{}, time.Time{}); err == nil {
		vm.ChainVerified = bad < 0
	}
	return vm
}

// auditTarget pulls the acted-upon object out of the canonical Detail JSON,
// trying "target", then "object", then "relation". "—" when absent/unparseable.
func auditTarget(detail []byte) string {
	if len(detail) == 0 {
		return "—"
	}
	var m map[string]any
	if err := json.Unmarshal(detail, &m); err != nil {
		return "—"
	}
	for _, k := range []string{"target", "object", "relation"} {
		if v, ok := m[k].(string); ok && v != "" {
			return v
		}
	}
	return "—"
}

// shortHash renders a 32-byte hash as "c4b7…2ef".
func shortHash(h []byte) string {
	if len(h) == 0 {
		return "—"
	}
	s := hex.EncodeToString(h)
	if len(s) < 7 {
		return s
	}
	return s[:4] + "…" + s[len(s)-3:]
}

// actionColor: T2 read actions are warn-toned; write/mutation actions accent.
func actionColor(action string, t2 bool) string {
	if t2 || strings.HasPrefix(action, "t2.") {
		return "var(--warnT)"
	}
	if strings.Contains(action, "execute") || strings.Contains(action, "grant") {
		return "var(--acc2)"
	}
	return "var(--text)"
}

func ternary(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}
```

Add `"strings"` to imports (keep `"strconv"`, `"time"`, `store`, `web`).

- [ ] **Step 7: Regenerate templ and run tests**

Run: `make templ && go test ./web/ -run TestAuditScreen -v && go test ./internal/api/ -run TestAudit -v && go test ./internal/store/ -run TestVerifyChain -v`
Expected: PASS. `internal/api/audit_test.go` is `package api_test` and drives the **real** config store via `setupAudit()` + `seedAudit()` (server_test.go) — there is **no** `store.Config` fake to extend (and do **not** add a `VerifyChain` stub returning `0`, which would falsely read as *tampered at row 0*). Just update its assertions from the old flat `[]web.AuditRow` markup to the new `AuditVM` screen: `seedAudit`'s rows are appended via `AppendAuditReturning`, so they carry a valid hash chain and the page must render `HASH CHAIN VERIFIED` (VerifyChain returns -1 → `ChainVerified == true`), the `TARGET`/`HASH`/`TIER` columns, and the amber T2 stripe on the tier-2 (`viewed.t2`) row.

- [ ] **Step 8: Commit**

```bash
git add internal/store/config.go web/audit.templ web/audit_templ.go web/audit_test.go internal/api/audit.go internal/api/audit_test.go
git commit -m "feat(ui): retrofit Audit Log to hash-chain banner, TARGET/HASH columns, tier badge, T2 amber striping [ly-ae6.7]"
```

---

## Self-Review

### 0. Adversarial-review resolutions (revision 2, 2026-07-10)

Every item from the adversarial review is resolved **in place** above:

- **Checks MUTE mutates real state (was: no-op stub / never-changes-on-render).** Step 0 widens `store.Stats` with `ClearMute` (already on `*pgxStats`, checks_results.go:131). `handleChecksMute` (Step 6) toggles `SetMute`↔`ClearMute` for real; `fetchChecks` (Step 5) overlays `ListMutes` onto each row's `Muted` so a just-toggled mute flips MUTE↔MUTED on the same round-trip (fixing the `checks_results.muted`-vs-`check_mutes` divergence). Step 6b integration-tests the round-trip.
- **Index EVIDENCE → drilldown link now renders (was: `EvidenceFP` left empty).** Task 5 Step 5 populates `EvidenceFP` from `advisor.IndexRecommendation.Fingerprints[0]` (index.go:59), which exists today, so the `if rec.EvidenceFP != ""` link is reached.
- **Checks firing-only semantics (was: counted/rendered all statuses).** `fetchChecks` filters `c.Status != "firing"` before counting/appending (checks.StatusFiring="firing"), so "N FIRING" is accurate and the "all healthy" empty state is reachable.
- **Scoped queries/insights surface no longer stale + un-tokenized.** Task 3 Step 6b retrofits `OverviewQueries` to a token grid + drilldown deep-link and **removes** the inline `QueryDrilldown` accordion (+ handler + `/partial/…/query/…` route); Task 4 Step 9b retrofits `OverviewInsights`. Route consolidation stays ly-ae6.3's; the Overview page chrome stays ly-ae6.6's.
- **No `emptyStats` placeholder.** Task 3 Step 1 is now a `package api_test` integration test using `setup()` + `http.Get` (the repo has no `store.Stats` fake). Task 4's white-box `filterInsights` test moves to a **new** `package api` file (`insights_filter_test.go`); the `api_test` files can't call unexported methods.
- **Audit `VerifyChain` correctness.** Task 11 uses `vm.ChainVerified = bad < 0` (VerifyChain returns **-1** when intact; `0` is a tamper at row 0). The false "fake returning `(0,\"\",nil)`" hedge is removed.
- **`fetchTop` compiles.** Task 2 Step 10 passes the required limit: `s.fetchTop(r, 50)`.
- **No hardcoded fleet paths in shared helpers.** `ScreenNav{Base, Plan}` (Task 1) is carried on each screen VM; `sortHref`, `insightChipHref`, `checkExpandHref`, the Config per-server tab, and the drilldown/evidence fleet fallbacks all read it. Fleet handlers set it; ly-ae6.3 refills it under scope without touching `design.go`.
- **Waits poll key ≠ display label.** `WaitsVM.ServerID` (the `?server=` key) is separate from `ScopeLabel` (the caption ly-ae6.2 will humanize).
- **Vacuum cleanups.** Dead `freezes`/`FreezeAdvice` accumulation removed; FREEZING bars use per-row `AutovacuumFreezeMaxAge` (no hardcoded 200M/260M); the PERFORMANCE panel takes only `advisor.CatPerformance` recs, not every category.

### 1. Spec coverage — every COMPARISON gap mapped to a task

Legend: **T#** = task in this plan; **→bead** = tracked backend/shell dependency, rendered as graceful empty/soon state or handled by ly-ae6.2/ly-ae6.3 (this plan provides the seam).

**`#### Queries screen`**
- Token design system → T1+T2 · Single scope-only Top Queries → T2 global surface tokenized; the **scoped** `OverviewQueries` fragment tokenized + deep-linked and its inline accordion removed → T3 Step 6b; route consolidation (dedup the two surfaces) →ly-ae6.3 · MEAN/ROWS/CACHE/TREND/SAMPLE + FINGERPRINT cols → T2 · Column sort + SQL filter → T2 · Real N-triangle insight count → T2 (replaces hardcoded 'slow scan' badge on both global T2 and scoped T3 Step 6b) · Full drilldown (4-stat/trends/wait-mix/recommendation) → T3 · T2 sample reveal+audit → T3 gate (→ly-8b0.6) · Scope-driven shell → seam (→ly-ae6.2/6.3)

**`#### Insights / query drilldown`**
- Design system → T1+T4 (global `/insights`) + T4 Step 9b (scoped `OverviewInsights` tokenized) · Deep-link to drilldown → T3+T4 (both the global insights table and the scoped `OverviewInsights` rows deep-link; fleet needs-attention list →ly-ae6.4) · Rows clickable/deep-linkable → T4 + T4 Step 9b · Insights-at-fleet scope-model violation → nav gating (→ly-ae6.3) · Scope shell → seam (→ly-ae6.2/6.3) · Engine type-icon + version chip → depends on per-node version model (→ly-7ck.3/ly-ae6.5; noted out-of-scope for retrofit) · Server/node bubble-up context → T4 renders `NodePath`; full server column arrives with cluster scope (→ly-ae6.2) · Severity vocab low/medium/high→CRIT/WARN/INFO → T1 `SevClass`/`SevLabel` + T4

**`#### Advisors (Index/Vacuum/Config)`**
- Design-system adoption → T5/T6/T7 · Scope context chip → T5/T6/T7 scope caption (nav placement →ly-ae6.3) · Config per-node picker → T7 · Index DDL/benefit/why/evidence → T5 (DDL + **EVIDENCE fp link populated NOW** from `Fingerprints[0]`; benefit% →ly-u4t.13; scope crumb →ly-u4t.12) · Vacuum 4 panels (BLOAT/PERF/ACTIVITY/FREEZING) → T6 (FREEZING uses per-row `AutovacuumFreezeMaxAge`; PERFORMANCE panel takes only `CatPerformance` recs; freeze data breadth →ly-u4t.26) · Config severity stripe/GROUP/allowlist badge/picker/footer → T7

**`#### Activity / Waits`**
- Wait histogram + legend(avg) + per-server mix + LIVE + scope caption → T8 (per-time-bucket stack →ly-u4t.22 soon-state; legend+mix are real) · /waits global-nav scope violation → nav gating (→ly-ae6.3) · Cluster ACTIVITY connection-state bar → cluster detail (→ly-ae6.6) · TopActivityBucketsByState render → cluster detail (→ly-ae6.6) · Cluster wait histogram → `WaitsView` is reusable; cluster mount →ly-ae6.6 · Connections/Live-Activity T2 view → SQL console (→ly-ae6.8) · Design tokens → T8

**`#### Checks & Alerts`**
- Tokens + LIVE + 'N FIRING' summary + 3px stripe → T9 (summary counts **firing-only** rows via the Status filter) · Expandable rows + 24-cell history → T9 (per-hour history →ly-u4t.25 soon-state) · SERVER + FIRST SEEN cols → T9 · MUTE/MUTED toggle → T9 **mutates real state** (SetMute/ClearMute on the interface, ListMutes overlay; ly-u4t.20 is alert *routing*, not mute storage) · Expanded-check deep-link → T9 `?expand=` · Nav not scoped → nav gating (→ly-ae6.3) · Alerts 'soon' sub-item → nav (→ly-ae6.3)

**`#### Query plan visualization`**
- Two-pane 3fr/2fr → T10 · NODE DETAIL pane → T10 · Click-to-select (`selNode`) → T10 `?node=` · Problem-node detection + red stripe + badge + color est→act → T10 · Plan-variant tabs → T10 (`?plan=`; seen-count/mean labels →ly-xqf.11) · LIVE/T1 badges → T10 · Scope shell → seam (→ly-ae6.2/6.3) · Extra node attrs (Workers/Buckets/Batches/Memory) → T10 renders VM-carried attrs; rest →ly-u4t.6 · Design system → T1+T10

**`#### Governance: Audit Log`**
- Hash chain UI + verified-tip banner + HASH col → T11 (VerifyChain exposed on interface) · TARGET col → T11 (extract from Detail JSON; →ly-8b0.3 for structured target) · Tier badge + T2 amber stripe/bg → T11 · Action color-coding → T11 · Placement under user-menu GOVERNANCE → nav (→ly-ae6.2) · Design system + LIVE + tamper-evident subtitle → T11 · Filter-form bonus kept → T11

**Bead ly-ae6.7 acceptance** ("retrofit queries/insights/advisors/waits/checks/plan/audit to the token system + scope context"): queries→T2+T3 · insights→T4 · advisors→T5+T6+T7 · waits→T8 · checks→T9 · plan→T10 · audit→T11 · token system→T1 (consumed by all) · scope context→`ScopeLabel` seam + Scope-Shell Integration Contract. **No gap unmapped.**

### 2. Placeholder scan

- No "TBD"/"implement later"/"add error handling"/"handle edge cases"/"similar to Task N" appear in any step — every code step carries complete code.
- Every referenced symbol is defined in a task or verified to exist in the repo: `ScreenNav` struct (T1) + `ScreenHeader`/`HeaderBadge`/`EmptyState`/`SevClass`/`SevLabel`/`SevChartVar`/`MeanMs`/`RecommendationFor`/`KindLabel`/`intToStr`/`chipClass`/`drilldownHref`/`dashInt`/`dashPct`/`dashPctInt`/`idxEvidenceHref`/`insightChipHref`/`insightDrilldownHref`/`mutedOpacity`/`muteLabel`/`checkExpandHref`/`checkMuteHref`/`planNodeStripe`/`planNodeStyle`/`planTypeColor`/`planNodeHref` (all Task 1–10, `web/design.go`). Nav-aware helpers `drilldownHref(r, nav)`, `insightDrilldownHref(r, nav)`, `idxEvidenceHref(r)` (reads `r.Nav`), `sortHref`/`insightChipHref`/`checkExpandHref` (read the `Nav` on the sort/filter/row struct) — no fleet literal is baked into any helper. `sortArrow`/`sortHead` (T2); `DecoratePlan`/`misestimateRatio` (T10); `FmtCost`/`flatten`/`ToPlanVM` (existing `web/plan_vm.go`); `prettyBytes`/`latestTableStats` (existing `internal/api/index_advisor.go`); `waitClass` (existing `internal/api/waits.go`); `agoLabel`/`agoColor` (T6, reused T9). Store methods on `store.Stats`: `TopQueriesByTotalTime`/`TopPlansByQuery`/`ListPlanKeys`/`WaitEventHistogram`/`RecentServerIDs`/`LatestChecksResults`/`LatestTableStats`/`LatestFreezeAges`/`LatestSettings`/`ListMutes`/`SetMute` (all existing, stats.go) + `ClearMute` (widened onto the interface in T9 Step 0; impl at checks_results.go:131); `advisor.IndexRecommendation.Fingerprints` (index.go:59); on `store.Config`: `ListAudit` + `VerifyChain` (widened in T11 Step 1; impl at config.go:284).
- Soon-state placeholders in the **UI** (empty histogram, empty history strip, `—` metrics) are explicit design states citing the exact tracked bead — not plan placeholders. No conditional/optional "if a store write exists" branch remains: the Checks mute path (T9) is unconditional real state, and the drilldown test (T3) is an unconditional integration test — no `emptyStats`/fake hedges anywhere.
- Each task ends with a real `git add … && git commit` naming the exact files including the generated `_templ.go`.

### 3. Type-consistency check

- `SevClass(string) string` always returns one of `"crit"|"warn"|"info"`; every consumer composes it as `"stripe-"+cls`, `"sev-"+cls`, or `"badge--"+kind` — matching the classes defined in `screens.css` (T1). No mismatch.
- `web.QuerySort{Col string; Dir string; Nav ScreenNav}` — identical field names/types in the struct (T2, `web/layout.templ`), the templ params (`QueriesScreen`/`QueriesTable`/`sortHead`), and the handler (`sortAndFilterQueries`, `handleDashboard`/`handleQueriesPartial` both set `Nav`). The handler references it as `web.QuerySort`; no local alias drift. `sortAndFilterQueries` ignores `Nav` (sorts on `Col`/`Dir`), so adding the field is inert to the sort logic.
- `web.InsightFilter{Sev string; Kind string; Nav ScreenNav}` — same in struct (T4), templ, and `filterInsights`/handlers; `filterInsights` reads only `Sev`/`Kind`.
- `ScreenNav{Base, Plan string}` (T1) is carried as a `Nav` field on `QuerySort`, `InsightFilter`, `ConfigAdvisorVM`, `ChecksRow`, `DrilldownVM`, and `IndexAdvisorRow`. Every helper that builds a page-navigation link reads that `Nav` (never a literal); every fleet handler sets it (`Base`=`/`,`/insights`,`/config-advisor`,`/checks`; `Plan`=`/plan`). Field names/types match at each producer (handler) and consumer (helper/templ). Web-test fixtures that omit `Nav` still compile (zero value) and only exercise scoped `ClusterID` links, so their assertions are unaffected.
- `intToStr(int) string` (T3) vs `dashInt(int64) string` (T2) — distinct names, distinct arg types; no accidental reuse. `dashPct(float64)` vs `dashPctInt(int)` — distinct by suffix and type; benefit% is `int`, cache-hit% is `float64`, called with the matching field type.
- `agoLabel(t, now time.Time) string` / `agoColor(t, now time.Time) string` — defined once (T6, package `api`) and called with `time.Time` args in both T6 and T9; single definition, no redefinition.
- `PlanNodeVM` field additions (`Idx int`, `Problem bool`, `EstColorVar string`) and `PlanVM` additions (`Variants []PlanVariant`, `SelectedIdx int`, `Selected *PlanNodeVM`) are referenced with the same names in `DecoratePlan` (T10 `plan_vm.go`), the templ (`PlanView`/`planNodeDetail`), and the render helpers (`planNodeStripe`/`planNodeStyle`/`planTypeColor`/`planNodeHref`). `PlanVariant{FP, Label, Selected, Href}` fields match between the handler population and the templ `for _, pt := range vm.Variants`.
- `web.ChecksVM{Summary string; Rows []ChecksRow}` and `ChecksRow{… ServerID, FirstSeen, Expanded, History, Nav …}` — same names in struct (T9), templ, `fetchChecks`, and the test. `fetchChecks` filters `c.Status != "firing"` (store `ChecksResultRow.Status`, checks_results.go:19) and overlays `ListMutes` into `Muted` before appending; `checksSummary(len(rows), cats)` therefore counts firing-only rows.
- `store.Stats.ClearMute(ctx, serverID, checkID, object string) error` — the signature added to the interface (T9 Step 0) exactly matches the existing `*pgxStats` method (checks_results.go:131); `SetMute`/`ListMutes` are already on the interface. `handleChecksMute` calls all three; no other type implements `store.Stats` (every consumer passes `store.NewStats(pool)`), so the widening cannot break a mock.
- `web.WaitsVM{ServerID string; ScopeLabel string; …}` — `ServerID` is the `?server=` poll key (templ `hx-get`), `ScopeLabel` is the caption; `fetchWaits` sets both. They coincide today but are distinct fields, so ly-ae6.2 humanizing `ScopeLabel` cannot break the poll.
- `web.AuditVM`/`AuditRow` field names (`Target`, `HashShort`, `ActionColorVar`, `StripeVar`, `BgVar`, `TierLabel`, `ChainVerified`, `TipShort`, `Count`) identical across struct (T11 `audit.templ`), templ (`AuditScreen`/`AuditTable`), and `fetchAudit`.
- `store.Config.VerifyChain(ctx, since, until) (int, string, error)` — the signature added to the interface (T11 step 1) exactly matches the existing `*pgxConfig` method (`internal/store/config.go:284`); the handler destructures it as `bad, _, err` and sets `vm.ChainVerified = bad < 0` (VerifyChain returns **-1** when intact; `0` is a first-row tamper — not "verified").
- Every retrofitted page keeps the established `XxxPage(vm)` (wraps `@Layout`) + `XxxScreen(vm)`/`XxxTable|View(vm)` fragment split, so `internal/api` handler render calls (`web.XxxPage(...)` / `web.XxxTable(...)`) resolve to the exact templ component names introduced in each task.

### Post-implementation full-suite gate

After Task 11, run the whole suite once to catch cross-task drift and confirm templ sync:

```bash
make templ
git diff --exit-code -- '*_templ.go'   # must be clean: generated files committed and in sync
go build ./...
go test ./web/... ./internal/api/... ./internal/store/...
```

Expected: build clean, `git diff --exit-code` returns 0 (no uncommitted templ regen), all tests PASS, and `web/layout_test.go::TestLayout_NoExternalHosts` still green (screens.css added no external host).

