# Scoped Overview + Cluster/Node/Pooler Detail Implementation Plan

> For agentic workers: execute this plan with **superpowers:subagent-driven-development**. Each task is a self-contained TDD loop (write failing test → run → implement → run → commit). Do not batch tasks; land each behind a green test and a commit before starting the next.

**Bead:** ly-ae6.6 — "UI: Scoped Overview + Cluster/Node/Pooler detail" (P2). Parent epic ly-ae6. Depends on ly-ae6.2 (top bar + scope model) and ly-ae6.3 (scope-driven sidebar nav).

**Goal:** Rebuild the cluster-detail screen and add node- and pooler-scoped screens, each led by a reusable "OPEN ISSUES ON THIS &lt;SCOPE&gt;" list (or a green clean strip), using design tokens — replacing the current light-theme MVP Overview with the dark token-based cluster/node/pooler detail surfaces the design specifies.

**Architecture:** New cross-signal assembler `fleetview.ScopeIssues` unifies firing checks + insights for any server-id set into one ranked list; a reusable `web.ScopedIssues` templ component renders it on every scoped Overview. The cluster-detail screen (`web.OverviewPage`) gains a back link, health-rollup header, node cards with a ⌖ scope button, in-page OVERVIEW/QUERIES/INSIGHTS/ACTIVITY tabs (HTMX partials), dual QPS+latency SVG charts, and a connection-state bar. New `/databases/{clusterID}/nodes/{instanceID}` and `/databases/{clusterID}/poolers/{poolerID}` routes render node- and pooler-scoped Overviews from `fleetview.GetNodeDetail` and a `web.PoolerVM` contract. All new markup is token-driven via a new self-hosted `web/static/css/scope.css`; dynamic colors are discrete CSS modifier classes and dynamic geometry is SVG attributes (never templ dynamic `style={}`), so templ's CSS sanitizer never sees `var(--x)`.

**Tech Stack:** Go 1.x, templ (`github.com/a-h/templ`), HTMX (self-hosted), server-side rendering. Stores: `internal/store` (pgx). View assembly: `internal/fleetview`. HTTP: `internal/api` (`http.ServeMux`). Tests: `net/http/httptest` + rendered-HTML assertions for handlers; testcontainers-backed real Postgres for store/fleetview. Module path `github.com/dobbo-ca/lynceus`.

## Global Constraints

Copy these rules into every task's working memory. They are non-negotiable and CI-enforced where noted.

- **Privacy — T1 only.** Every component in this plan renders **T1 (normalized, literal-free)** data only. None of these screens is a T2 surface. Never read a raw query sample, never add a field capable of carrying a literal to any view-model on this path. Checks/insights `Detail` strings are already-normalized T1 text produced by the collector — render them as-is; do not enrich them with literals.
- **No external hosts.** Never add a CDN, external font, script, stylesheet, image, or `fetch`/XHR to an external origin. All CSS/JS/fonts are self-hosted under `web/static/` and served at `/static/`. There is a contract test `TestLayout_NoExternalHosts` — keep it green.
- **Tokens, not legacy.** New screens are built with design tokens (`var(--acc)`, `var(--surface)`, `var(--line)`, …) defined in `web/static/css/tokens.css`. Do **not** reuse `legacy.css` component classes on new screens. New token-based classes belong in `web/static/css/scope.css` (created in Task 1).
- **templ regen.** Any `.templ` edit requires `make templ` to regenerate the committed `*_templ.go`. CI checks the generated files are in sync — regenerate and commit them in the same commit as the `.templ` change.
- **Testcontainers, no DB mocks.** Tests hit **real Postgres** via each package's existing testcontainers helper — **use the one that already exists in that package, do not invent new names:** `internal/store` → `newPool(t)` (store_test.go); `internal/fleetview` → `newDB(t)` / `newStores(t)` (summary_test.go); `internal/api` → `newDBPool(t)` (databases_test.go). Apply schema with `store.ApplyConfigMigrations` / `store.ApplyStatsMigrations` (`newStores`/`setupOverview` already do). Never mock the database. Handler tests use `httptest` against a server wired to real stores (see `internal/api/overview_test.go` `setupOverview`).
- **Surgical changes.** Touch only what each task names. Do not retrofit adjacent screens (that is ly-ae6.7), do not rebuild the sidebar (ly-ae6.3), do not build the top bar (ly-ae6.2). Match existing code style.

## Integration contract with ly-ae6.2 / ly-ae6.3 (dependencies, built separately)

These screens live inside the scoped shell that ly-ae6.2 and ly-ae6.3 build. To keep ly-ae6.6 independently shippable and testable, this plan builds the **main-content** components and their routes now, wrapped in the existing `web.Layout` + placeholder `web.ClusterSidebar`. The integration points the shell owners must honor:

- **URL scheme (this plan defines it):** cluster scope `GET /databases/{clusterID}`; node scope `GET /databases/{clusterID}/nodes/{instanceID}`; pooler scope `GET /databases/{clusterID}/poolers/{poolerID}` (+ `/config`). The ⌖ scope buttons and deep-links produced here target these URLs. ly-ae6.2's scope picker must resolve a scope selection to the same URLs; ly-ae6.3's sidebar wraps these screens.
- **Scope identity comes from the URL path** (`clusterID`, `instanceID`, `poolerID`) — not from server-side scope state — so these handlers are pure functions of the request and testable in isolation. When ly-ae6.2 lands, the top-bar scope chip is derived from the same path values.
- **Sidebar ownership (transitional frame):** `web.ClusterSidebar` (with its out-of-spec "settings" item) is **ly-ae6.3's** to rebuild per scope. This plan does **not** modify the sidebar or the `/settings` route; on the cluster-detail screen it keeps the existing legacy frame (`overview-layout` + `overview-main` + `ClusterSidebar`, all `legacy.css`) and rebuilds only the `<main>` **content** with tokens + the in-page tabs. The NEW node/pooler screens (Tasks 5/6) instead use a clean `@Layout` + token `scope-screen` with no sidebar. **Consequence — state this explicitly:** until ly-ae6.3 lands, the flagship cluster screen is only half-tokenized (token content inside a legacy shell) and visually diverges from the fully-tokenized node/pooler screens. This is an accepted transitional state, not an oversight. Because the sidebar is kept, the cluster page body still contains the sidebar's mixed-case `Overview`/`Settings` link text; Task 4's "no Settings" assertion therefore targets the **in-page tab bar only** (asserts no `/tab/settings` tab exists), never the whole-page body. The "remove the vertical sidebar + `/settings` item" gap is owned by ly-ae6.3 (flagged in Self-Review).

- **Per-scope nav map (from the design prototype — the contract ly-ae6.3 must honor).** The prototype's scope-nav builder (`docs/design/Lynceus.dc.html` ~line 2386–2406) defines exactly which entries each scope exposes. This plan builds the **Overview** screen at each scope and the **pooler Config · pgbouncer** shell; every other entry points at a **shared screen owned by another bead**, surfaced by ly-ae6.3's nav — none are pooler/node-specific screens for ly-ae6.6 to build. The map (● = built in this plan; → owner otherwise):

  | Scope | Nav entries (design) | Ownership |
  |---|---|---|
  | CLUSTER | Overview ●, Nodes, Databases, Capabilities, Queries, Advisors(Index/Vacuum/Config·per-node), Activity, Console, Checks, Schema, Logs | Overview + in-page tabs ● (Task 2–4); Nodes/Databases = other ly-ae6 screens; Capabilities → ly-4ov; Advisors → ly-ae6.7; Checks/Schema/Logs/Console/Activity → their own beads; all wired by ly-ae6.3 |
  | NODE | Overview ●, **Config**, **Capabilities**, Queries, Advisors(no Config), Activity, Console, Checks, Logs | Overview ● (Task 5); **Config → existing `/config-advisor` screen (per-node scoping owned by ly-ae6.7)**; **Capabilities → existing capability screen (redesign ly-4ov)**; rest → their beads, nav-wired by ly-ae6.3 |
  | POOLER | Overview ●, **Config · pgbouncer** ●(shell), Activity→**Connections [SOON, T2]**, Saved Scripts, Checks, Logs | Overview ● + Config·pgbouncer shell ● (Task 6); Connections is a **roadmap/SOON T2 surface even in the design** (flag `'soon','t2'`) — not built by anyone yet; Saved Scripts → Saved-Scripts bead; Checks → `/checks` (existing); Logs → Logs bead; nav-wired by ly-ae6.3 |

  **Node "Config + Capabilities" acceptance (ly-ae6.6 criterion):** the node scope's Config and Capabilities entries are **existing shared screens** (`/config-advisor`, capability matrix), not new screens — the design points the node nav at them. This plan meets the criterion by (a) building the node **Overview** and (b) defining the node-scope URL/nav contract above so ly-ae6.3 can wire Config/Capabilities to those existing screens. It deliberately does **not** build throwaway node Config/Capabilities shells, because that would duplicate and conflict with ly-ae6.7 (config-advisor per-node) and ly-4ov (capabilities redesign). This is an explicit delegation with a named integration point, not a silent drop.

  **Pooler Activity/Checks/Logs:** likewise shared screens. **Connections (Activity)** is marked `SOON`/`T2` in the design prototype itself — a roadmap placeholder — so nothing is built at any scope; **Checks** reuses the existing `/checks` screen; **Logs** is owned by the Logs bead. ly-ae6.3 wires these nav entries. Task 6 builds only the two screens the design assigns to the POOLER level directly (Overview + Config · pgbouncer), both as token shells over the backend-absent `web.PoolerVM`.

## Backend dependency — pooler data model (genuinely missing, must be filed)

`grep -ri 'pgbouncer\|pooler' internal/` returns zero matches: there is **no POOLER topology role, no pgbouncer collector reader, and no pooler stats/settings store**. No existing bead tracks it (the node/pooler COMPARISON area cites only the closed ly-xnk.5 capability-matrix bead, which is unrelated). This plan therefore:
1. Defines the exact UI contract the pooler screens consume (`web.PoolerVM`, Task 6).
2. Renders the pooler Overview + Config screens as token-based shells that show a designed empty state ("pgbouncer stats appear once the collector reports") when the contract is unpopulated — which it always is today.
3. Flags in Self-Review and in the returned `backendDeps` that a backend bead must be filed: **"collector pgbouncer reader (SHOW POOLS/STATS/CLIENTS/LISTS) + POOLER topology role + pooler stats/settings store"**. Do not build that backend here.

---

### Task 1: Scope-issues assembler + reusable `ScopedIssues` component + `scope.css`

The centerpiece: one assembler and one component reused by cluster/node/pooler/database Overviews. "Every scoped Overview leads with an OPEN ISSUES ON THIS &lt;SCOPE&gt; list … or a green `● NO OPEN CHECKS OR INSIGHTS…` strip when clean" (README §Scope Model).

**Files**
- Create: `internal/fleetview/issues.go`
- Create: `internal/fleetview/issues_test.go`
- Create: `web/static/css/scope.css`
- Create: `web/scope_issues.templ` (+ generated `web/scope_issues_templ.go` via `make templ`)
- Create: `web/scope_issues_test.go`
- Modify: `web/layout.templ` (+ regenerated `web/layout_templ.go`) — add one `<link>` to `scope.css`
- Modify: `web/static_test.go` — assert `scope.css` is embedded & served (optional guard; see steps)

**Interfaces**

Produces (fleetview):
```go
// ScopeIssue is one open check or insight attributed to a scope's server set,
// normalized for the "OPEN ISSUES ON THIS <scope>" list.
type ScopeIssue struct {
	Kind     string // "check" | "insight"
	Severity string // normalized: "crit" | "warn" | "info"
	ID       string // display id: check_id, or "<kind> · <fingerprint>" for insights
	Detail   string // already-normalized T1 detail text
	Server   string // server_id the issue belongs to
	Ref      string // check_id (checks) or fingerprint (insights) — for deep links
	AgeMin   int    // whole minutes from EvaluatedAt/CapturedAt to `until`
}

func ScopeIssues(ctx context.Context, stats store.Stats, serverIDs []string, since, until time.Time) ([]ScopeIssue, error)
```

Produces (web):
```go
type ScopeIssueVM struct {
	Severity string        // "crit" | "warn" | "info"
	ID       string
	Detail   string
	Server   string
	Age      string        // "12m", "3h", "1d", or "—"
	Href     templ.SafeURL // deep-link target
}
type ScopedIssuesVM struct {
	ScopeKind  string // "CLUSTER" | "NODE" | "DATABASE" | "POOLER"
	Count      int
	Issues     []ScopeIssueVM
	ShowServer bool // false on node/pooler scope (single server) — hides SERVER column
}
templ ScopedIssues(vm ScopedIssuesVM)
```

Consumes: `store.Stats.LatestChecksResults(ctx, serverID, since, until) ([]store.ChecksResultRow, error)` and `store.Stats.TopInsightsForServers(ctx, serverIDs, since, until, limit) ([]store.InsightRow, error)` — both already on the interface. `store.ChecksResultRow{ServerID, EvaluatedAt, CheckID, Category, Severity, Status, Object, Detail, Muted, DataTier}`, `store.InsightRow{ServerID, CapturedAt, Kind, Severity, Fingerprint, Relation, Detail, …}`. Check statuses are `"firing"`/`"ok"`; check severities `"critical"|"warning"|"info"`; insight severities `"high"|"medium"|"low"`.

**Steps**

- [ ] **Step 1:** Write the failing assembler test `internal/fleetview/issues_test.go`. Seed one server, one firing critical check, one muted check, one non-firing (ok) check, and one insight; assert `ScopeIssues` returns exactly the firing check + the insight, crit-first, with normalized severities.
```go
package fleetview_test

import (
	"context"
	"testing"
	"time"

	"github.com/dobbo-ca/lynceus/internal/fleetview"
	"github.com/dobbo-ca/lynceus/internal/store"
)

func TestScopeIssues_firingChecksAndInsights(t *testing.T) {
	ctx := context.Background()
	statsPool := newDB(t) // existing fleetview testcontainers helper (summary_test.go)
	if err := store.ApplyStatsMigrations(ctx, statsPool); err != nil {
		t.Fatalf("stats migrate: %v", err)
	}
	stats := store.NewStats(statsPool)
	now := time.Now().UTC()
	since := now.AddDate(0, 0, -1)

	if err := stats.WriteChecksResults(ctx, []store.ChecksResultRow{
		{ServerID: "n1", EvaluatedAt: now.Add(-10 * time.Minute), CheckID: "settings.fsync",
			Category: "settings", Severity: "critical", Status: "firing", Object: "fsync", Detail: "fsync is off"},
		{ServerID: "n1", EvaluatedAt: now.Add(-10 * time.Minute), CheckID: "settings.muted",
			Category: "settings", Severity: "warning", Status: "firing", Object: "x", Detail: "muted one", Muted: true},
		{ServerID: "n1", EvaluatedAt: now.Add(-10 * time.Minute), CheckID: "settings.ok",
			Category: "settings", Severity: "info", Status: "ok", Object: "y", Detail: "all good"},
	}); err != nil {
		t.Fatalf("write checks: %v", err)
	}
	if err := stats.WriteInsights(ctx, []store.InsightRow{
		{ServerID: "n1", CapturedAt: now.Add(-5 * time.Minute), Kind: "slow_scan", Severity: "medium",
			Fingerprint: "fp-abc", Relation: "orders", Detail: "seq scan on orders"},
	}); err != nil {
		t.Fatalf("write insights: %v", err)
	}

	got, err := fleetview.ScopeIssues(ctx, stats, []string{"n1"}, since, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("ScopeIssues: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 issues (firing check + insight), got %d: %+v", len(got), got)
	}
	if got[0].Kind != "check" || got[0].Severity != "crit" || got[0].ID != "settings.fsync" {
		t.Errorf("issue[0] = %+v; want crit check settings.fsync first", got[0])
	}
	if got[1].Kind != "insight" || got[1].Severity != "warn" || got[1].Ref != "fp-abc" {
		t.Errorf("issue[1] = %+v; want warn insight fp-abc", got[1])
	}
}
```
Note on the pool helper (VERIFIED): `internal/fleetview` test package (`fleetview_test`) already provides `newDB(t) *pgxpool.Pool` and `newStores(t) (store.Config, store.Stats, *pgxpool.Pool)` in `internal/fleetview/summary_test.go`. Use `newDB(t)` for a stats-only pool (as above) and `newStores(t)` when both config and stats are needed (Task 5). Do **not** invent `newStatsPool`/`newConfigPool` — they do not exist in this package.

- [ ] **Step 2:** Run it — expect a compile failure (no `ScopeIssues`).
```
go test ./internal/fleetview/ -run TestScopeIssues_firingChecksAndInsights
```
Expected: FAIL — `undefined: fleetview.ScopeIssues`.

- [ ] **Step 3:** Implement `internal/fleetview/issues.go`.
```go
package fleetview

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/dobbo-ca/lynceus/internal/store"
)

// ScopeIssue is one open check or insight attributed to a scope's server set,
// normalized for the "OPEN ISSUES ON THIS <scope>" list. Kind is "check" or
// "insight"; Ref is the check_id or the query fingerprint used to deep-link.
type ScopeIssue struct {
	Kind     string
	Severity string
	ID       string
	Detail   string
	Server   string
	Ref      string
	AgeMin   int
}

// ScopeIssues assembles the open issues for a server-id set over [since, until):
// firing, non-muted check results plus all insights, normalized to the design's
// crit/warn/info vocabulary and sorted crit>warn>info then newest first.
func ScopeIssues(
	ctx context.Context, stats store.Stats, serverIDs []string, since, until time.Time,
) ([]ScopeIssue, error) {
	var out []ScopeIssue
	for _, sid := range serverIDs {
		checks, err := stats.LatestChecksResults(ctx, sid, since, until)
		if err != nil {
			return nil, err
		}
		for i := range checks {
			c := &checks[i]
			if c.Muted || c.Status != "firing" {
				continue
			}
			out = append(out, ScopeIssue{
				Kind:     "check",
				Severity: NormalizeSeverity(c.Severity),
				ID:       c.CheckID,
				Detail:   c.Detail,
				Server:   sid,
				Ref:      c.CheckID,
				AgeMin:   ageMinutes(until, c.EvaluatedAt),
			})
		}
	}
	insights, err := stats.TopInsightsForServers(ctx, serverIDs, since, until, 50)
	if err != nil {
		return nil, err
	}
	for i := range insights {
		in := &insights[i]
		out = append(out, ScopeIssue{
			Kind:     "insight",
			Severity: NormalizeSeverity(in.Severity),
			ID:       fmt.Sprintf("%s · %s", in.Kind, in.Fingerprint),
			Detail:   in.Detail,
			Server:   in.ServerID,
			Ref:      in.Fingerprint,
			AgeMin:   ageMinutes(until, in.CapturedAt),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if ri, rj := sevRank(out[i].Severity), sevRank(out[j].Severity); ri != rj {
			return ri < rj
		}
		return out[i].AgeMin < out[j].AgeMin
	})
	return out, nil
}

func ageMinutes(until, t time.Time) int {
	m := int(until.Sub(t).Minutes())
	if m < 0 {
		return 0
	}
	return m
}

// NormalizeSeverity maps the engines' severities (checks: critical/warning/info;
// insights: high/medium/low) onto the design's crit/warn/info vocabulary. Exported
// because the api layer reuses it for the RecentInsights view-model (Task 4).
func NormalizeSeverity(s string) string {
	switch strings.ToLower(s) {
	case "high", "critical", "crit":
		return "crit"
	case "medium", "warning", "warn":
		return "warn"
	default:
		return "info"
	}
}

func sevRank(s string) int {
	switch s {
	case "crit":
		return 0
	case "warn":
		return 1
	default:
		return 2
	}
}
```

- [ ] **Step 4:** Run — expect PASS.
```
go test ./internal/fleetview/ -run TestScopeIssues_firingChecksAndInsights
```
Expected: PASS.

- [ ] **Step 5:** Create `web/static/css/scope.css` — token-based classes for all ly-ae6.6 screens (used by Tasks 1–6). Discrete colors are modifier classes; no `var(--x)` ever appears in a templ dynamic `style={}`.
```css
/* scope.css — token-based styling for the ly-ae6.6 scoped Overview / cluster /
   node / pooler detail screens. References tokens.css custom properties only. */

.scope-screen { padding:18px 22px 32px; display:flex; flex-direction:column; gap:14px; max-width:1400px; }
.scope-head { display:flex; align-items:baseline; gap:12px; flex-wrap:wrap; }
.scope-back { font-family:var(--font-mono); font-size:11px; color:var(--acc); cursor:pointer; }
.scope-title { font-size:17px; font-weight:600; font-family:var(--font-mono); }
.scope-live { font-family:var(--font-mono); font-size:10px; color:var(--acc); border:var(--border) solid var(--acc); padding:0 5px; border-radius:var(--radius-badge); }
.scope-meta { font-family:var(--font-mono); font-size:10.5px; color:var(--faint); letter-spacing:.08em; }
.scope-spacer { flex:1; }
.scope-health { font-family:var(--font-mono); font-size:10px; letter-spacing:.04em; }
.scope-health.is-crit { color:var(--critT); }
.scope-health.is-warn { color:var(--warnT); }
.scope-health.is-info { color:var(--infoT); }
.scope-health.is-ok   { color:var(--ok); }

/* --- OPEN ISSUES ON THIS <scope> --- */
.issues-card { border:var(--border) solid var(--line); border-radius:var(--radius); background:var(--surface); }
.issues-head { padding:8px 12px; border-bottom:var(--border) solid var(--line); font-family:var(--font-mono); font-size:10.5px; letter-spacing:.1em; color:var(--dim); display:flex; gap:14px; align-items:center; }
.issues-count { font-size:9.5px; color:var(--faint); }
.issue-row { display:flex; align-items:center; gap:12px; padding:8px 12px; border-bottom:var(--border) solid var(--line2); font-size:12.5px; color:var(--text); text-decoration:none; }
.issue-row:hover { background:var(--raised); text-decoration:none; }
.issue-sev { width:8px; height:8px; flex-shrink:0; }
.issue-sev.sev-crit { background:var(--crit); }
.issue-sev.sev-warn { background:var(--warn); }
.issue-sev.sev-info { background:var(--info); }
.issue-id { font-family:var(--font-mono); font-size:12px; min-width:230px; color:var(--text); }
.issue-detail { color:var(--mut); overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
.issue-server { font-family:var(--font-mono); font-size:11px; color:var(--dim); flex-shrink:0; }
.issue-age { font-family:var(--font-mono); font-size:10px; color:var(--faint); width:30px; text-align:right; flex-shrink:0; }
.issues-clean { border:var(--border) solid var(--line); border-radius:var(--radius); background:var(--surface); padding:10px 12px; font-family:var(--font-mono); font-size:10.5px; color:var(--acc2); letter-spacing:.06em; }

/* --- node / component cards (cluster detail) --- */
.node-cards { display:grid; grid-template-columns:1fr 1fr; gap:14px; }
.node-card { border:var(--border) solid var(--line); border-radius:var(--radius); background:var(--surface); padding:11px 14px; display:flex; align-items:center; gap:12px; font-family:var(--font-mono); }
.node-role { font-size:9.5px; border:var(--border) solid var(--line); padding:1px 6px; border-radius:var(--radius-badge); width:58px; text-align:center; flex-shrink:0; color:var(--infoT); text-transform:uppercase; letter-spacing:.06em; }
.node-role.is-primary { color:var(--acc2); }
.node-role.is-replica { color:var(--infoT); }
.node-role.is-pooler  { color:var(--infoT); }
.node-body { display:flex; flex-direction:column; gap:2px; min-width:0; }
.node-name { font-size:12px; font-weight:600; }
.node-name .node-ver { font-size:9.5px; color:var(--dim); margin-left:8px; }
.node-meta { font-size:10px; color:var(--dim); overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
.node-src { font-size:9.5px; color:var(--faint); }
.node-health { font-size:10px; flex-shrink:0; }
.node-health.is-crit { color:var(--critT); }
.node-health.is-warn { color:var(--warnT); }
.node-health.is-info { color:var(--infoT); }
.node-health.is-ok   { color:var(--acc2); }
.scope-btn { width:24px; height:24px; border:var(--border) solid var(--line); border-radius:var(--radius); display:flex; align-items:center; justify-content:center; color:var(--acc2); font-size:13px; cursor:pointer; user-select:none; flex-shrink:0; text-decoration:none; }
.scope-btn:hover { border-color:var(--acc); background:var(--accdim); text-decoration:none; }

/* --- cluster in-page tabs --- */
.cluster-tabs { display:flex; border-bottom:var(--border) solid var(--line); font-family:var(--font-mono); font-size:11px; }
.cluster-tab { padding:7px 14px; cursor:pointer; user-select:none; letter-spacing:.08em; color:var(--mut); border-bottom:2px solid transparent; margin-bottom:-1px; background:none; border-top:none; border-left:none; border-right:none; }
.cluster-tab.is-active { color:var(--acc2); border-bottom-color:var(--acc); }

/* --- charts + conn bar --- */
.chart-grid { display:grid; grid-template-columns:1fr 1fr; gap:14px; }
.chart-card { border:var(--border) solid var(--line); border-radius:var(--radius); background:var(--surface); }
.chart-head { padding:8px 12px; border-bottom:var(--border) solid var(--line2); font-family:var(--font-mono); font-size:10px; letter-spacing:.1em; color:var(--dim); display:flex; justify-content:space-between; }
.chart-head .chart-val { color:var(--text); }
.chart-card svg { display:block; }
/* SVG mark COLORS via classes — var() does not resolve in SVG presentation
   attributes, so fill/stroke live here (geometry stays as attributes). */
.chart-gridline { stroke:var(--line2); stroke-width:1; }
.chart-area-qps { fill:var(--accdim); }
.chart-line-qps { fill:none; stroke:var(--acc); stroke-width:1.5; }
.chart-line-lat { fill:none; stroke:var(--chart-lwlock); stroke-width:1.5; }
.conn-seg.is-active { fill:var(--acc); }
.conn-seg.is-idle   { fill:var(--chart-cpu); }
.conn-seg.is-idletx { fill:var(--warn); }
.conn-seg.is-other  { fill:var(--chart-client); }
.conn-card { border:var(--border) solid var(--line); border-radius:var(--radius); background:var(--surface); padding:12px 14px; display:flex; flex-direction:column; gap:10px; }
.conn-label { font-family:var(--font-mono); font-size:10px; letter-spacing:.1em; color:var(--dim); }
.conn-legend { display:flex; gap:18px; font-family:var(--font-mono); font-size:10.5px; color:var(--mut); flex-wrap:wrap; }
.recent-card { border:var(--border) solid var(--line); border-radius:var(--radius); background:var(--surface); }
.recent-head { padding:8px 12px; border-bottom:var(--border) solid var(--line); font-family:var(--font-mono); font-size:10px; letter-spacing:.1em; color:var(--dim); }
.recent-row { display:flex; align-items:center; gap:12px; padding:8px 12px; border-bottom:var(--border) solid var(--line2); font-size:12.5px; color:var(--text); text-decoration:none; }
.recent-row:hover { background:var(--raised); text-decoration:none; }

/* --- pooler / node empty state --- */
.scope-empty { border:var(--border) solid var(--line); border-radius:var(--radius); background:var(--surface); padding:22px; font-family:var(--font-mono); font-size:11px; color:var(--faint); text-align:center; letter-spacing:.04em; }
```

- [ ] **Step 6:** Link `scope.css` in `web/layout.templ` `<head>`, right after the `legacy.css` link. Then regenerate.
```
<link rel="stylesheet" href="/static/css/legacy.css"/>
<link rel="stylesheet" href="/static/css/scope.css"/>
```
Run `make templ`. `web/static.go` already embeds the whole `static` dir via `//go:embed static`, so `scope.css` is served with no code change. Add a served-asset guard by appending one row to the existing table in `web/static_test.go` `TestStaticHandler_ServesThemeJSAndLegacyCSS`:
```go
	{"/static/css/scope.css", ".scope-screen"},
```
This keeps the self-hosted-asset contract honest for the new file. (`TestLayout_SelfHostedAssets` asserts only tokens/legacy/htmx/theme, so the extra `scope.css` link does not break it; `TestLayout_NoExternalHosts` stays green because `scope.css` is a `/static/…` local ref.)

- [ ] **Step 7:** Write the failing component test `web/scope_issues_test.go` — render `ScopedIssues` in both the issues and clean states.
```go
package web_test

import (
	"context"
	"strings"
	"testing"

	"github.com/a-h/templ"

	"github.com/dobbo-ca/lynceus/web"
)

// renderStr renders a templ.Component to a string (mirrors web/layout_test.go's
// strings.Builder idiom). templ.Component.Render takes an io.Writer, which
// *strings.Builder satisfies.
func renderStr(t *testing.T, c templ.Component) string {
	t.Helper()
	var sb strings.Builder
	if err := c.Render(context.Background(), &sb); err != nil {
		t.Fatalf("render: %v", err)
	}
	return sb.String()
}

func TestScopedIssues_listsIssues(t *testing.T) {
	vm := web.ScopedIssuesVM{
		ScopeKind: "CLUSTER", Count: 1, ShowServer: true,
		Issues: []web.ScopeIssueVM{{
			Severity: "crit", ID: "settings.fsync", Detail: "fsync is off",
			Server: "srv-a", Age: "10m", Href: "/checks",
		}},
	}
	got := renderStr(t, web.ScopedIssues(vm))
	if !strings.Contains(got, "OPEN ISSUES ON THIS CLUSTER") {
		t.Error("missing scoped-issues header")
	}
	if !strings.Contains(got, "settings.fsync") || !strings.Contains(got, "sev-crit") {
		t.Errorf("missing issue row / crit modifier; got: %s", got)
	}
}

func TestScopedIssues_cleanStrip(t *testing.T) {
	got := renderStr(t, web.ScopedIssues(web.ScopedIssuesVM{ScopeKind: "NODE", Count: 0}))
	if !strings.Contains(got, "NO OPEN CHECKS OR INSIGHTS ON THIS NODE") {
		t.Errorf("missing clean strip; got: %s", got)
	}
}
```
Note (VERIFIED): `web/layout_test.go` renders with `Component.Render(context.Background(), &sb)` where `sb` is a `strings.Builder` — the exact idiom `renderStr` uses. `templ.Component` is the return type of every `templ Xxx(...)` function, so `web.ScopedIssues(vm)` is a `templ.Component`.

- [ ] **Step 8:** Run — expect compile failure (`undefined: web.ScopedIssues`).
```
go test ./web/ -run TestScopedIssues
```
Expected: FAIL.

- [ ] **Step 9:** Implement `web/scope_issues.templ`.
```go
package web

// ScopeIssueVM is one row in a scoped "OPEN ISSUES" list.
type ScopeIssueVM struct {
	Severity string // "crit" | "warn" | "info"
	ID       string
	Detail   string
	Server   string
	Age      string
	Href     templ.SafeURL
}

// ScopedIssuesVM is the full model for the OPEN ISSUES ON THIS <scope> card.
type ScopedIssuesVM struct {
	ScopeKind  string // "CLUSTER" | "NODE" | "DATABASE" | "POOLER"
	Count      int
	Issues     []ScopeIssueVM
	ShowServer bool
}

// ScopedIssues renders the "OPEN ISSUES ON THIS <scope>" list, or a green clean
// strip when there are none. Rows are anchors deep-linking to the explanation.
templ ScopedIssues(vm ScopedIssuesVM) {
	if vm.Count == 0 {
		<div class="issues-clean">
			{ "● NO OPEN CHECKS OR INSIGHTS ON THIS " + vm.ScopeKind }
		</div>
	} else {
		<div class="issues-card">
			<div class="issues-head">
				{ "OPEN ISSUES ON THIS " + vm.ScopeKind }
				<span class="scope-spacer"></span>
				<span class="issues-count">{ intToStr(vm.Count) } OPEN</span>
			</div>
			for i := range vm.Issues {
				<a class="issue-row" href={ vm.Issues[i].Href }>
					<span class={ "issue-sev sev-" + vm.Issues[i].Severity }></span>
					<span class="issue-id">{ vm.Issues[i].ID }</span>
					<span class="issue-detail">{ vm.Issues[i].Detail }</span>
					<span class="scope-spacer"></span>
					if vm.ShowServer {
						<span class="issue-server">{ vm.Issues[i].Server }</span>
					}
					<span class="issue-age">{ vm.Issues[i].Age }</span>
				</a>
			}
		</div>
	}
}
```
Add a shared small helper if `intToStr` does not already exist in `package web` (search first: `grep -rn "func intToStr" web/`). If absent, create `web/format.go`:
```go
package web

import "strconv"

// intToStr formats an int for templ text nodes without fmt in the template.
func intToStr(n int) string { return strconv.Itoa(n) }
```

- [ ] **Step 10:** Regenerate and run.
```
make templ && go test ./web/ -run TestScopedIssues
```
Expected: PASS.

- [ ] **Step 11:** Commit.
```
git add internal/fleetview/issues.go internal/fleetview/issues_test.go web/static/css/scope.css web/scope_issues.templ web/scope_issues_templ.go web/scope_issues_test.go web/layout.templ web/layout_templ.go web/static_test.go web/format.go
git commit -m "ly-ae6.6: scope-issues assembler + ScopedIssues component + scope.css"
```

---

### Task 2: Cluster-detail header (back link, meta, health rollup) + wire ScopedIssues

Rebuild the top of the cluster Overview: back link, `LIVE` badge, cluster meta, and a `[HEALTH] n CRIT · n WARN` rollup line, followed immediately by the ScopedIssues card. Gaps closed: "No back link", "No cluster health indicator / DB→NODE→CLUSTER rollup line", "Scoped Overview does NOT lead with an OPEN ISSUES ON THIS CLUSTER list".

**Files**
- Modify: `web/overview.templ` (+ regenerated `_templ.go`) — new header + ScopedIssues; keep tokens
- Modify: `internal/api/overview.go` — extend `toOverviewVM` to build header + scoped-issues fields
- Modify: `web/overview.templ` `OverviewVM` struct — add fields
- Modify: `internal/api/overview_test.go` — assert new header + issues render

**Interfaces**

Produces — extend `web.OverviewVM` (existing struct in `web/overview.templ`) with:
```go
Meta       string           // "3 PG INSTANCES + 1 POOLER / STREAMING / PG 16.3" (best-effort; see note)
HealthLine string           // "[DEGRADED] 1 CRIT · 4 WARN" / "[HEALTHY] 0 OPEN"
HealthMod  string           // "crit" | "warn" | "info" | "ok"  → CSS modifier
Issues     ScopedIssuesVM   // OPEN ISSUES ON THIS CLUSTER
```
(Keep all existing `OverviewVM` fields.)

Consumes: `fleetview.ScopeIssues` (Task 1); `fleetview.ClusterDetail` (existing, from `GetClusterDetail`). Health rollup is derived in the handler from the assembled issues (crit/warn/info counts).

Note on `Meta`: the design's meta string ("3 PG INSTANCES + 1 POOLER / …") needs instance counts, streaming/topology, and version — of which only instance count is currently modeled (`len(d.Instances)`), and pooler/version/topology are not (backend gaps). Build `Meta` from what exists: `fmt.Sprintf("%d PG INSTANCES / %d DATABASES", len(d.Instances), d.StreamCount)`. Do not invent version/pooler/topology text. This is a deliberate, honest partial; richer meta arrives with the fleet-topology backend (epic ly-99s).

**Steps**

- [ ] **Step 1:** Add a handler test to `internal/api/overview_test.go` asserting the new header + issues. Extend `setupOverview` to also seed a firing check on `ov-srv-a` (so the ScopedIssues card renders), then:
```go
func TestOverviewPage_headerAndScopedIssues(t *testing.T) {
	srv, clusterID, _ := setupOverview(t)
	resp, err := http.Get(srv.URL + "/databases/" + clusterID)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	b := readBody(t, resp)
	if !strings.Contains(b, "scope-back") {
		t.Error("missing back link")
	}
	if !strings.Contains(b, "OPEN ISSUES ON THIS CLUSTER") && !strings.Contains(b, "NO OPEN CHECKS OR INSIGHTS ON THIS CLUSTER") {
		t.Error("missing scoped-issues card (neither issues nor clean strip)")
	}
	if !strings.Contains(b, "scope-health") {
		t.Error("missing health rollup line")
	}
}
```
To make the issues branch deterministic, add a firing check seed inside `setupOverview` after the insights seed:
```go
	if err := stats.WriteChecksResults(ctx, []store.ChecksResultRow{
		{ServerID: "ov-srv-a", EvaluatedAt: now.Add(-15 * time.Minute), CheckID: "settings.fsync",
			Category: "settings", Severity: "critical", Status: "firing", Object: "fsync", Detail: "fsync is off"},
	}); err != nil {
		t.Fatalf("seed checks: %v", err)
	}
```

- [ ] **Step 2:** Run — expect FAIL (`scope-back`/`scope-health` not in output; possibly compile error once you reference new fields).
```
go test ./internal/api/ -run TestOverviewPage_headerAndScopedIssues
```
Expected: FAIL.

- [ ] **Step 3:** Add the new fields to `OverviewVM` in `web/overview.templ` (append to the struct):
```go
	Meta       string
	HealthLine string
	HealthMod  string
	Issues     ScopedIssuesVM
```

- [ ] **Step 4:** Replace the header block of `templ OverviewPage` (the `<main class="overview-main">` opening through the `<div class="tiles">` — specifically the `<h2 id="overview">{ vm.Name }</h2>` line) with the token-based header + ScopedIssues. Keep the sidebar wrapper and the rest (tiles/topology/queries/insights) intact for now; Task 4 replaces tiles/topology with charts/tabs. New header:
```go
			<main class="overview-main">
				<div class="scope-head">
					<a class="scope-back" href="/databases">← CLUSTERS</a>
					<span class="scope-title">{ vm.Name }</span>
					<span class="scope-live">LIVE</span>
					<span class="scope-meta">{ vm.Meta }</span>
					<span class="scope-spacer"></span>
					<span class={ "scope-health is-" + vm.HealthMod }>{ vm.HealthLine }</span>
				</div>
				@ScopedIssues(vm.Issues)
```
(Leave the `<div class="tiles">…</div>`, `@OverviewTopology`, `@OverviewQueries`, `@OverviewInsights`, and Facts sections below untouched in this task; Task 4 restructures them.)

- [ ] **Step 5:** Extend `toOverviewVM` in `internal/api/overview.go` to compute the header + issues. `toOverviewVM` has no `ctx`/stores, so the scoped issues are assembled in `handleClusterOverview` (which has `s.conf`/`s.stats`) and passed **in** as a new parameter. Change the signature to accept `issues`, keep the existing body verbatim, but convert its trailing `return web.OverviewVM{ … }` into a named `vm := web.OverviewVM{ … }` (same field list, unchanged) and set the four new fields before returning:
```go
func toOverviewVM(d *fleetview.ClusterDetail, issues web.ScopedIssuesVM) web.OverviewVM {
	// ...UNCHANGED: the existing local computations (qps, insightFPs, queries,
	// insights, instances) stay exactly as they are today...
	vm := web.OverviewVM{ /* the existing field list, verbatim: ClusterID, Name,
		QPS, AvgLatencyMs, ActiveConns, TopWait, InsightCount, StreamCount,
		Sparkline, Instances, Queries, Insights */ }
	vm.Issues = issues
	vm.Meta = fmt.Sprintf("%d PG INSTANCES / %d DATABASES", len(d.Instances), d.StreamCount)
	vm.HealthLine, vm.HealthMod = rollup(issues)
	return vm
}

// rollup renders "[DEGRADED] n CRIT · n WARN" style summaries from the issues.
func rollup(issues web.ScopedIssuesVM) (line, mod string) {
	var crit, warn, info int
	for _, is := range issues.Issues {
		switch is.Severity {
		case "crit":
			crit++
		case "warn":
			warn++
		default:
			info++
		}
	}
	switch {
	case crit > 0:
		return fmt.Sprintf("[DEGRADED] %d CRIT · %d WARN", crit, warn), "crit"
	case warn > 0:
		return fmt.Sprintf("[WARNING] %d WARN · %d INFO", warn, info), "warn"
	case info > 0:
		return fmt.Sprintf("[HEALTHY] %d INFO", info), "info"
	default:
		return "[HEALTHY] 0 OPEN", "ok"
	}
}
```
Update `handleClusterOverview` to build the issues VM and call the new signature:
```go
func (s *Server) handleClusterOverview(w http.ResponseWriter, r *http.Request) {
	clusterID := r.PathValue("clusterID")
	now := time.Now().UTC()
	since := now.AddDate(0, 0, -1)
	detail, found, err := fleetview.GetClusterDetail(r.Context(), s.conf, s.stats, clusterID, since, now)
	if err != nil || !found {
		http.NotFound(w, r)
		return
	}
	issues := s.scopeIssuesVM(r, "CLUSTER", true, clusterID, "", since, now)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.OverviewPage(toOverviewVM(&detail, issues)).Render(r.Context(), w)
}
```
Add a shared helper `scopeIssuesVM` in `internal/api/overview.go` (reused by node scope in Task 5). It resolves the server set, calls `fleetview.ScopeIssues`, and maps to `web.ScopedIssuesVM` including deep-link `Href`:
```go
// scopeIssuesVM assembles the ScopedIssues view-model for a cluster (instanceID
// == "") or a single node (instanceID != ""). kind is the display scope label.
func (s *Server) scopeIssuesVM(r *http.Request, kind string, showServer bool, clusterID, instanceID string, since, until time.Time) web.ScopedIssuesVM {
	var serverIDs []string
	if instanceID != "" {
		serverIDs, _ = s.conf.ServerIDsForInstance(r.Context(), instanceID)
	} else {
		serverIDs, _ = s.conf.ServerIDsForCluster(r.Context(), clusterID)
	}
	issues, _ := fleetview.ScopeIssues(r.Context(), s.stats, serverIDs, since, until)
	vm := web.ScopedIssuesVM{ScopeKind: kind, ShowServer: showServer, Count: len(issues)}
	for i := range issues {
		vm.Issues = append(vm.Issues, web.ScopeIssueVM{
			Severity: issues[i].Severity,
			ID:       issues[i].ID,
			Detail:   issues[i].Detail,
			Server:   issues[i].Server,
			Age:      humanizeAge(issues[i].AgeMin),
			Href:     issueHref(clusterID, &issues[i]),
		})
	}
	return vm
}

// issueHref deep-links an issue to its explanation: insights → the cluster query
// drilldown for the fingerprint; checks → the checks screen. (Check-expand deep
// targeting is owned by the Checks bead; a plain /checks link is the interim.)
func issueHref(clusterID string, is *fleetview.ScopeIssue) templ.SafeURL {
	if is.Kind == "insight" {
		return templ.SafeURL("/databases/" + clusterID + "/queries#drill-" + is.Ref)
	}
	return templ.SafeURL("/checks")
}

func humanizeAge(min int) string {
	switch {
	case min <= 0:
		return "—"
	case min < 60:
		return fmt.Sprintf("%dm", min)
	case min < 60*24:
		return fmt.Sprintf("%dh", min/60)
	default:
		return fmt.Sprintf("%dd", min/(60*24))
	}
}
```
Imports (VERIFIED): `internal/api/overview.go` currently imports only `net/http`, `time`, `fleetview`, `store`, `web` — it does **not** import `fmt`. This task's new code (`Meta`, `rollup`, `humanizeAge`) uses `fmt.Sprintf` and `issueHref` uses `templ.SafeURL`, so **add** both `"fmt"` and `"github.com/a-h/templ"` to the import block. Update the other `toOverviewVM` caller in `internal/api/cluster_views.go` (`fetchClusterVM`) to pass an empty issues VM (those non-Overview pages don't lead with it): `return toOverviewVM(&detail, web.ScopedIssuesVM{ScopeKind: "CLUSTER"}), true`.

- [ ] **Step 6:** Regenerate templ, run the focused test then the package.
```
make templ && go test ./internal/api/ -run TestOverviewPage
```
Expected: PASS (both the new test and the existing `TestOverviewPage_returns200WithContent`, since `my-cluster`/`Overview`/`SELECT` still render).

- [ ] **Step 7:** Commit.
```
git add web/overview.templ web/overview_templ.go internal/api/overview.go internal/api/cluster_views.go internal/api/overview_test.go
git commit -m "ly-ae6.6: cluster-detail header (back link, meta, health rollup) + scoped issues"
```

---

### Task 3: Node cards with ⌖ scope button + per-node role/version/source/health

Replace the generic `OverviewTopology` "Topology" cards with design node cards: role badge, name (+ per-node version), a source line, a health dot, and a ⌖ button that sets scope to the node (`/databases/{clusterID}/nodes/{instanceID}`). Gap closed: "Node cards lack the ⌖ scope button (and are framed as generic 'Topology' cards without per-node version/health/source)".

**Files**
- Modify: `web/overview.templ` (`OverviewInstance` struct + rewrite `OverviewTopology` → `NodeCards`) (+ regen)
- Modify: `internal/api/overview.go` (`toOverviewVM` instance mapping)
- Modify: `internal/api/overview_test.go` — assert ⌖ link + role badge render

**Interfaces**

Produces — extend `web.OverviewInstance` with:
```go
	InstanceID string // for the ⌖ node-scope link
	ClusterID  string // for the ⌖ node-scope link
	Version    string // per-node version, "" until backend models it (epic ly-99s)
	Source     string // data-source line, "" until backend models it
	Health     string // "OK" | "WARN" | "CRIT" — derived from the node's firing checks
	HealthMod  string // "ok" | "warn" | "crit"
	Meta1      string // "<calls> calls · <conns> active conns" (from existing rollup)
```
(Keep existing `Name, Role, Databases, Calls, ActiveConns`.)

New component signature: `templ NodeCards(insts []OverviewInstance)` (replaces `OverviewTopology`). Update the call site in `OverviewPage`.

Consumes: existing `fleetview.InstanceTopo` (Instance + Calls + ActiveConns). Per-node health is derived from `fleetview.ScopeIssues` scoped to that instance's server set; Version/Source are backend-absent (render "—" / omit).

**Steps**

- [ ] **Step 1:** Add to `internal/api/overview_test.go`:
```go
func TestOverviewPage_nodeCardsHaveScopeButton(t *testing.T) {
	srv, clusterID, _ := setupOverview(t)
	resp, _ := http.Get(srv.URL + "/databases/" + clusterID)
	defer func() { _ = resp.Body.Close() }()
	b := readBody(t, resp)
	if !strings.Contains(b, "/nodes/") || !strings.Contains(b, "scope-btn") {
		t.Errorf("node card missing ⌖ scope link to /nodes/; body: %s", b)
	}
	if !strings.Contains(b, "node-role") {
		t.Error("node card missing role badge")
	}
}
```

- [ ] **Step 2:** Run — expect FAIL.
```
go test ./internal/api/ -run TestOverviewPage_nodeCardsHaveScopeButton
```
Expected: FAIL.

- [ ] **Step 3:** In `web/overview.templ`, extend `OverviewInstance` (add the fields above) and replace `templ OverviewTopology` with:
```go
// NodeCards renders one card per instance: role badge, name + per-node version,
// a data-source line, health dot, and a ⌖ button that sets scope to the node.
templ NodeCards(insts []OverviewInstance) {
	<section id="topology">
		if len(insts) == 0 {
			<p class="scope-empty">NO NODES LINKED YET</p>
		} else {
			<div class="node-cards">
				for i := range insts {
					<div class="node-card">
						<span class={ "node-role is-" + insts[i].Role }>{ insts[i].Role }</span>
						<div class="node-body">
							<span class="node-name">
								{ insts[i].Name }
								if insts[i].Version != "" {
									<span class="node-ver">v{ insts[i].Version }</span>
								}
							</span>
							if insts[i].Meta1 != "" {
								<span class="node-meta">{ insts[i].Meta1 }</span>
							}
							if insts[i].Source != "" {
								<span class="node-src">{ insts[i].Source }</span>
							}
						</div>
						<span class="scope-spacer"></span>
						<span class={ "node-health is-" + insts[i].HealthMod }>● { insts[i].Health }</span>
						<a
							class="scope-btn"
							title="Set scope to this node"
							href={ templ.SafeURL("/databases/" + insts[i].ClusterID + "/nodes/" + insts[i].InstanceID) }
						>⌖</a>
					</div>
				}
			</div>
		}
	</section>
}
```
Update the call in `OverviewPage`: replace `@OverviewTopology(vm.Instances)` with `@NodeCards(vm.Instances)`.

- [ ] **Step 4:** In `internal/api/overview.go` `toOverviewVM`, enrich the instance mapping. Per-node health needs the node's firing checks — resolve them from the detail's per-instance server sets. `fleetview.InstanceTopo` already carries `Instance` and rolled `Calls`/`ActiveConns`; add health via a per-instance issue count. Since `toOverviewVM` has no ctx/store, pass a `map[string]string` (instanceID→healthMod) computed in the handler, OR compute health from the already-assembled cluster issues by matching `issue.Server` to the instance's server ids. Simpler and store-free: pass the per-instance health map from the handler. Two concrete edits to `toOverviewVM`: (a) widen the signature to `func toOverviewVM(d *fleetview.ClusterDetail, issues web.ScopedIssuesVM, nodeHealth map[string]string) web.OverviewVM` (this supersedes Task 2's 2-arg signature); (b) inside the **existing** instance loop, replace the current `instances = append(instances, web.OverviewInstance{Name, Role, Databases, Calls, ActiveConns})` call with the enriched append below (the loop's `inst`, `dbs` locals are unchanged):
```go
	instances = append(instances, web.OverviewInstance{
		Name:        inst.Instance.Name,
		Role:        roleClass(inst.Instance.Role),
		Databases:   dbs,
		Calls:       inst.Calls,
		ActiveConns: inst.ActiveConns,
		InstanceID:  inst.Instance.ID,
		ClusterID:   d.Cluster.ID,
		Health:      healthLabel(nodeHealth[inst.Instance.ID]),
		HealthMod:   emptyToOK(nodeHealth[inst.Instance.ID]),
		Meta1:       fmt.Sprintf("%d calls · %d active conns", inst.Calls, inst.ActiveConns),
	})

// roleClass normalizes store roles to the css/display vocabulary. store.Instance.Role
// is "primary" | "replica" | "unknown" (defaults to "unknown" until ly-99s.3 fills
// it). Map "unknown" to "replica" so the chip + is-<role> class always resolve to a
// styled value (scope.css only defines is-primary / is-replica / is-pooler).
func roleClass(role string) string {
	switch role {
	case "primary", "replica":
		return role
	default:
		return "replica"
	}
}
func emptyToOK(mod string) string {
	if mod == "" {
		return "ok"
	}
	return mod
}
func healthLabel(mod string) string {
	switch mod {
	case "crit":
		return "CRIT"
	case "warn":
		return "WARN"
	case "info":
		return "INFO" // pairs with .node-health.is-info in scope.css
	default:
		return "OK"
	}
}
```
In `handleClusterOverview`, build `nodeHealth` by scoping issues per instance:
```go
	nodeHealth := map[string]string{}
	for i := range detail.Instances {
		inst := detail.Instances[i].Instance
		ids, _ := s.conf.ServerIDsForInstance(r.Context(), inst.ID)
		nis, _ := fleetview.ScopeIssues(r.Context(), s.stats, ids, since, now)
		nodeHealth[inst.ID] = worstSeverity(nis)
	}
	_ = web.OverviewPage(toOverviewVM(&detail, issues, nodeHealth)).Render(r.Context(), w)
```
Add `worstSeverity`:
```go
func worstSeverity(issues []fleetview.ScopeIssue) string {
	mod := ""
	for i := range issues {
		switch issues[i].Severity {
		case "crit":
			return "crit"
		case "warn":
			mod = "warn"
		case "info":
			if mod == "" {
				mod = "info"
			}
		}
	}
	return mod
}
```
Update `fetchClusterVM` in `cluster_views.go` to pass `nil` for `nodeHealth`.

- [ ] **Step 5:** Regenerate + run.
```
make templ && go test ./internal/api/ -run TestOverviewPage
```
Expected: PASS.

- [ ] **Step 6:** Commit.
```
git add web/overview.templ web/overview_templ.go internal/api/overview.go internal/api/cluster_views.go internal/api/overview_test.go
git commit -m "ly-ae6.6: node cards with ⌖ scope button + per-node role/health"
```

---

### Task 4: Cluster in-page tabs + QPS/latency charts + connection-state bar + recent insights

Replace the MVP tiles/single-sparkline with the design's cluster-detail body: horizontal OVERVIEW/QUERIES/INSIGHTS/ACTIVITY tabs (HTMX-swapped), dual QPS + mean-latency SVG charts, a connection-state stacked bar, and a recent-insights list. No "settings" tab. Gaps closed: "Only a single hardcoded QPS sparkline — no QPS+latency charts", "Activity view has no connection-state bar", the in-page tab structure.

**Files**
- Create: store method `LatencyBucketsForServers` — `internal/store/rollup.go` (+ interface in `internal/store/stats.go`) + `internal/store/rollup_test.go` case
- Create: `internal/fleetview/connstate.go` + test — connection-state breakdown across a server set
- Modify: `web/overview.templ` — chart card + conn bar + tabs components (+ regen)
- Modify: `internal/api/overview.go` — chart point helpers, conn-state mapping, `OverviewVM` fields
- Create: `internal/api/cluster_tabs.go` — `handleClusterTab` partial handler
- Modify: `internal/api/server.go` — register `GET /partial/databases/{clusterID}/tab/{tab}`
- Modify/Create: `internal/api/overview_test.go` / `cluster_tabs_test.go` — assert charts, conn bar, tab partial

**Interfaces**

Produces (store):
```go
type LatencyBucket struct {
	BucketStart time.Time
	MeanMs      float64
}
// on Stats interface:
LatencyBucketsForServers(ctx context.Context, serverIDs []string, since, until time.Time) ([]LatencyBucket, error)
```
Produces (fleetview):
```go
type ConnState struct {
	State string // "active" | "idle" | "idle in transaction" | ...
	Count int64
}
func ConnStates(ctx context.Context, stats store.Stats, serverIDs []string, since, until time.Time) ([]ConnState, error)
```
Produces — extend `web.OverviewVM`:
```go
	QPSLine     string // SVG polyline points, viewBox 0 0 560 118
	QPSArea     string // SVG polygon points (area under QPS)
	LatLine     string // SVG polyline points for mean latency
	QPSVal      string // header value e.g. "1,284"
	LatVal      string // header value e.g. "3.6"
	ConnLabel   string // "CONNECTIONS BY STATE · <active>/<sum>"
	ConnStates  []ConnStateVM
	RecentInsights []RecentInsightVM
```
```go
type ConnStateVM struct { Label string; Count int64; X, W float64; FillClass string } // FillClass = discrete CSS modifier (is-active/is-idle/is-idletx/is-other); color set in scope.css, NOT via a var() SVG attr
type RecentInsightVM struct { Severity, Label, FP, Detail string; Href templ.SafeURL }
```
**SVG color rule (important):** CSS custom properties (`var(--x)`) do **not** resolve inside SVG *presentation attributes* (`fill="…"`, `stroke="…"`) — only inside a CSS rule or `style`. So every chart/bar **color** is applied via a CSS class defined in `scope.css` (which references the tokens); only **geometry** (points, x/width) stays as SVG attributes. This matches the plan's stated principle ("dynamic colors are discrete CSS modifier classes; dynamic geometry is SVG attributes") and keeps `var(--x)` out of templ's `style={}` sanitizer entirely.

Consumes: `store.QPSBucketsForServers` (existing), new `LatencyBucketsForServers`, new `fleetview.ConnStates` (built on existing `store.TopActivityBucketsByState`).

**Steps**

- [ ] **Step 1:** Failing store test — add to `internal/store/rollup_test.go` (or the nearest existing rollup test file):
```go
func TestLatencyBucketsForServers(t *testing.T) {
	ctx := context.Background()
	pool := newPool(t) // store_test helper (store_test.go); NOT newDBPool
	if err := store.ApplyStatsMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := store.NewStats(pool)
	now := time.Now().UTC().Truncate(time.Hour)
	if err := s.WriteQueryStats(ctx, []store.QueryStat{
		{ServerID: "l1", CollectedAt: now.Add(-90 * time.Minute), Fingerprint: "f", NormalizedQuery: "q", Calls: 10, TotalTimeMs: 100},
		{ServerID: "l1", CollectedAt: now.Add(-30 * time.Minute), Fingerprint: "f", NormalizedQuery: "q", Calls: 5, TotalTimeMs: 100},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	got, err := s.LatencyBucketsForServers(ctx, []string{"l1"}, now.Add(-2*time.Hour), now.Add(time.Minute))
	if err != nil {
		t.Fatalf("LatencyBucketsForServers: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 buckets, got %d", len(got))
	}
	if got[0].MeanMs != 10 || got[1].MeanMs != 20 { // 100/10=10, 100/5=20
		t.Errorf("mean latencies = %v, want [10 20]", got)
	}
}
```
VERIFIED: `internal/store/rollup_test.go` is package `store_test` and uses `newPool(t)` (defined in `internal/store/store_test.go`). Use that exact package + helper; do not introduce `newDBPool` (that name lives only in `internal/api`).

- [ ] **Step 2:** Run — expect FAIL (`undefined: LatencyBucketsForServers`).
```
go test ./internal/store/ -run TestLatencyBucketsForServers
```
Expected: FAIL.

- [ ] **Step 3:** Implement `LatencyBucketsForServers` in `internal/store/rollup.go` (mirror `QPSBucketsForServers`):
```go
// LatencyBucket is the call-weighted mean latency for a server set in one bucket.
type LatencyBucket struct {
	BucketStart time.Time
	MeanMs      float64
}

// LatencyBucketsForServers returns hourly call-weighted mean-latency buckets for
// the server_id set in [since, until), oldest first — the data behind the
// cluster mean-latency chart. Buckets with zero calls are skipped.
func (s *pgxStats) LatencyBucketsForServers(
	ctx context.Context, serverIDs []string, since, until time.Time,
) ([]LatencyBucket, error) {
	rows, err := s.ro.Query(ctx,
		`SELECT date_trunc('hour', collected_at) AS bucket,
		        SUM(total_time_ms) / NULLIF(SUM(calls), 0) AS mean_ms
		   FROM query_stats
		  WHERE server_id = ANY($1)
		    AND collected_at >= $2 AND collected_at < $3
		    AND data_tier = 1
		  GROUP BY bucket
		 HAVING SUM(calls) > 0
		  ORDER BY bucket ASC`,
		serverIDs, since, until,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LatencyBucket
	for rows.Next() {
		var b LatencyBucket
		if err := rows.Scan(&b.BucketStart, &b.MeanMs); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
```
Add to the `Stats` interface in `internal/store/stats.go` (next to `QPSBucketsForServers`):
```go
	LatencyBucketsForServers(ctx context.Context, serverIDs []string, since, until time.Time) ([]LatencyBucket, error)
```

- [ ] **Step 4:** Run — expect PASS.
```
go test ./internal/store/ -run TestLatencyBucketsForServers
```
Expected: PASS.

- [ ] **Step 5:** Failing fleetview test — `internal/fleetview/connstate_test.go`:
```go
func TestConnStates_sumsLatestBucketPerState(t *testing.T) {
	ctx := context.Background()
	pool := newDB(t) // fleetview_test helper (summary_test.go); NOT newStatsPool
	if err := store.ApplyStatsMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := store.NewStats(pool)
	now := time.Now().UTC().Truncate(time.Hour)
	if err := s.WriteActivityBuckets(ctx, []store.ActivityBucket{
		{ServerID: "c1", Database: "d", State: "active", BucketStart: now, BucketSeconds: 60, SampleCount: 1, CountMax: 4, DataTier: 1},
		{ServerID: "c1", Database: "d", State: "idle", BucketStart: now, BucketSeconds: 60, SampleCount: 1, CountMax: 7, DataTier: 1},
		{ServerID: "c2", Database: "d", State: "active", BucketStart: now, BucketSeconds: 60, SampleCount: 1, CountMax: 2, DataTier: 1},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	got, err := fleetview.ConnStates(ctx, s, []string{"c1", "c2"}, now.Add(-time.Hour), now.Add(time.Minute))
	if err != nil {
		t.Fatalf("ConnStates: %v", err)
	}
	// active should sum to 6 across servers; idle 7.
	byState := map[string]int64{}
	for _, cs := range got {
		byState[cs.State] = cs.Count
	}
	if byState["active"] != 6 || byState["idle"] != 7 {
		t.Errorf("got %+v; want active=6 idle=7", byState)
	}
}
```

- [ ] **Step 6:** Run — expect FAIL. `go test ./internal/fleetview/ -run TestConnStates_sumsLatestBucketPerState` → FAIL.

- [ ] **Step 7:** Implement `internal/fleetview/connstate.go`:
```go
package fleetview

import (
	"context"
	"time"

	"github.com/dobbo-ca/lynceus/internal/store"
)

// ConnState is one connection-state total across a server set (latest bucket).
type ConnState struct {
	State string
	Count int64
}

// ConnStates returns the connection-state breakdown for a server set, summing
// each server's most-recent bucket's count_max per state. Order: active, idle,
// idle in transaction, then any remaining states as encountered.
func ConnStates(
	ctx context.Context, stats store.Stats, serverIDs []string, since, until time.Time,
) ([]ConnState, error) {
	totals := map[string]int64{}
	for _, sid := range serverIDs {
		buckets, err := stats.TopActivityBucketsByState(ctx, sid, since, until, 5000)
		if err != nil {
			return nil, err
		}
		var latest time.Time
		for i := range buckets {
			if buckets[i].BucketStart.After(latest) {
				latest = buckets[i].BucketStart
			}
		}
		for i := range buckets {
			if buckets[i].BucketStart.Equal(latest) {
				totals[buckets[i].State] += buckets[i].CountMax
			}
		}
	}
	order := []string{"active", "idle", "idle in transaction"}
	var out []ConnState
	seen := map[string]bool{}
	for _, st := range order {
		if v, ok := totals[st]; ok {
			out = append(out, ConnState{State: st, Count: v})
			seen[st] = true
		}
	}
	for st, v := range totals {
		if !seen[st] {
			out = append(out, ConnState{State: st, Count: v})
		}
	}
	return out, nil
}
```
Note: `TopActivityBucketsByState` is defined on `*pgxStats` but not on the `Stats` interface. Add it to the `Stats` interface in `internal/store/stats.go` if absent (search `grep -n TopActivityBucketsByState internal/store/stats.go`); it must be interface-visible for `fleetview.ConnStates(stats store.Stats, …)`.

- [ ] **Step 8:** Run — expect PASS (add the interface method if the compile fails, then rerun).
```
go test ./internal/fleetview/ -run TestConnStates_sumsLatestBucketPerState
```
Expected: PASS.

- [ ] **Step 9:** Add chart + conn-bar + tabs components to `web/overview.templ`. First the chart card and conn bar and tabs helpers:
```go
type ConnStateVM struct {
	Label     string
	Count     int64
	X         float64 // segment x within a 100-wide bar (computed in handler)
	W         float64 // segment width
	FillClass string  // discrete CSS modifier: "is-active" | "is-idle" | "is-idletx" | "is-other" (color in scope.css)
}
type RecentInsightVM struct {
	Severity string
	Label    string
	FP       string
	Detail   string
	Href     templ.SafeURL
}

// ClusterCharts renders the QPS + mean-latency dual chart cards. All colors come
// from scope.css classes (var() does not resolve in SVG presentation attributes);
// only geometry (points) is a dynamic attribute.
templ ClusterCharts(vm OverviewVM) {
	<div class="chart-grid">
		<div class="chart-card">
			<div class="chart-head"><span>QUERIES / SEC</span><span class="chart-val">{ vm.QPSVal }</span></div>
			<svg width="100%" height="118" viewBox="0 0 560 118" preserveAspectRatio="none">
				<line x1="0" y1="30" x2="560" y2="30" class="chart-gridline"></line>
				<line x1="0" y1="60" x2="560" y2="60" class="chart-gridline"></line>
				<line x1="0" y1="90" x2="560" y2="90" class="chart-gridline"></line>
				if vm.QPSArea != "" {
					<polygon points={ vm.QPSArea } class="chart-area-qps"></polygon>
				}
				if vm.QPSLine != "" {
					<polyline points={ vm.QPSLine } class="chart-line-qps"></polyline>
				}
			</svg>
		</div>
		<div class="chart-card">
			<div class="chart-head"><span>MEAN LATENCY (MS)</span><span class="chart-val">{ vm.LatVal }</span></div>
			<svg width="100%" height="118" viewBox="0 0 560 118" preserveAspectRatio="none">
				<line x1="0" y1="30" x2="560" y2="30" class="chart-gridline"></line>
				<line x1="0" y1="60" x2="560" y2="60" class="chart-gridline"></line>
				<line x1="0" y1="90" x2="560" y2="90" class="chart-gridline"></line>
				if vm.LatLine != "" {
					<polyline points={ vm.LatLine } class="chart-line-lat"></polyline>
				}
			</svg>
		</div>
	</div>
}

// ConnBar renders the connection-state stacked bar + legend. Segment color is a
// discrete class (conn-seg is-active/is-idle/is-idletx/is-other); only x/width
// are dynamic attributes.
templ ConnBar(vm OverviewVM) {
	<div class="conn-card">
		<div class="conn-label">{ vm.ConnLabel }</div>
		<svg width="100%" height="14" viewBox="0 0 100 14" preserveAspectRatio="none">
			for i := range vm.ConnStates {
				<rect x={ floatToStr(vm.ConnStates[i].X) } y="0" width={ floatToStr(vm.ConnStates[i].W) } height="14" class={ "conn-seg " + vm.ConnStates[i].FillClass }></rect>
			}
		</svg>
		<div class="conn-legend">
			for i := range vm.ConnStates {
				<span>{ vm.ConnStates[i].Label } · { int64ToStr(vm.ConnStates[i].Count) }</span>
			}
		</div>
	</div>
}

// ClusterTabsWrap is the full-page wrapper: the whole tabs+body block lives
// inside #cluster-tabs-wrap. Every tab button targets this wrapper's innerHTML,
// so a click re-renders BOTH the tab bar (moving the is-active underline) and
// the body — fixing the stuck-active-tab defect. On first paint the wrapper is
// rendered with active="overview".
templ ClusterTabsWrap(vm OverviewVM, active string) {
	<div id="cluster-tabs-wrap">
		@ClusterTabsInner(vm, active)
	</div>
}

// ClusterTabsInner is the swap payload: the tab bar (with `active` marked) plus
// the matching tab body. handleClusterTab returns exactly this so the active
// indicator moves on every swap. Exported so the api package can render it.
templ ClusterTabsInner(vm OverviewVM, active string) {
	<div class="cluster-tabs">
		@clusterTab(vm.ClusterID, "overview", active, "OVERVIEW")
		@clusterTab(vm.ClusterID, "queries", active, "QUERIES")
		@clusterTab(vm.ClusterID, "insights", active, "INSIGHTS")
		@clusterTab(vm.ClusterID, "activity", active, "ACTIVITY")
	</div>
	<div id="cluster-tab-body">
		@ClusterTabBody(active, vm)
	</div>
}
templ clusterTab(clusterID, key, active, label string) {
	<button
		class={ "cluster-tab", templ.KV("is-active", key == active) }
		hx-get={ "/partial/databases/" + clusterID + "/tab/" + key }
		hx-target="#cluster-tabs-wrap"
		hx-swap="innerHTML"
	>{ label }</button>
}

// ClusterTabBody renders the body of one in-page tab. There is intentionally no
// "settings" case — the design's tab set is OVERVIEW/QUERIES/INSIGHTS/ACTIVITY.
templ ClusterTabBody(tab string, vm OverviewVM) {
	switch tab {
		case "queries":
			@OverviewQueries(vm)
		case "insights":
			@OverviewInsights(vm.Insights)
		case "activity":
			<div class="scope-empty">Wait-event history lives in ACTIVITY → Wait Events; live sessions are Tier 2 (Connections).</div>
		default:
			@ClusterCharts(vm)
			@ConnBar(vm)
			@RecentInsights(vm.RecentInsights)
	}
}

// RecentInsights renders the recent-insights list on the cluster overview tab.
templ RecentInsights(items []RecentInsightVM) {
	<div class="recent-card">
		<div class="recent-head">RECENT INSIGHTS</div>
		if len(items) == 0 {
			<div class="issue-row"><span class="issue-detail">No insights in the last 24 hours.</span></div>
		} else {
			for i := range items {
				<a class="recent-row" href={ items[i].Href }>
					<span class={ "issue-sev sev-" + items[i].Severity }></span>
					<span class="issue-id">{ items[i].Label }</span>
					<span class="issue-server">{ items[i].FP }</span>
					<span class="issue-detail">{ items[i].Detail }</span>
				</a>
			}
		}
	</div>
}
```
Add float/int helpers to `web/format.go`:
```go
func floatToStr(f float64) string { return strconv.FormatFloat(f, 'f', 1, 64) }
func int64ToStr(n int64) string   { return strconv.FormatInt(n, 10) }
```
Now restructure `OverviewPage`'s `<main>` body. **Keep the transitional legacy frame** (`overview-layout` + `@ClusterSidebar(vm.ClusterID, "overview")` + `overview-main`) — the sidebar rebuild is ly-ae6.3's (see Integration Contract). Inside `<main class="overview-main">`, the body becomes exactly the token header (from Task 2), the scoped-issues card, the node cards, then the tabs wrapper — nothing else. `ClusterTabsWrap` renders the full OVERVIEW tab inline on first paint (so the page is complete with no JS), and each tab click swaps `#cluster-tabs-wrap` innerHTML with `ClusterTabsInner`, re-rendering the tab bar (moving the active underline) plus the new body. Final `<main>` content:
```go
			<main class="overview-main">
				<div class="scope-head">
					<a class="scope-back" href="/databases">← CLUSTERS</a>
					<span class="scope-title">{ vm.Name }</span>
					<span class="scope-live">LIVE</span>
					<span class="scope-meta">{ vm.Meta }</span>
					<span class="scope-spacer"></span>
					<span class={ "scope-health is-" + vm.HealthMod }>{ vm.HealthLine }</span>
				</div>
				@ScopedIssues(vm.Issues)
				@NodeCards(vm.Instances)
				@ClusterTabsWrap(vm, "overview")
			</main>
```
Delete the now-unused `<div class="tiles">…</div>` block, the standalone `@OverviewQueries(vm)`/`@OverviewInsights(vm.Insights)` calls, and the `<section id="facts">…</section>` block from `OverviewPage`. **Keep the `OverviewQueries`/`OverviewInsights` templ definitions** — they are still referenced by `ClusterTabBody` (QUERIES/INSIGHTS tabs) and by `ClusterQueriesPage`/`ClusterInsightsPage` in `web/cluster_views.templ`. (`OverviewTopology` was already replaced by `NodeCards` in Task 3 and has no other callers — VERIFIED via grep.) Note `web/overview.templ`'s `import "fmt"` stays valid: `OverviewQueries` still uses `fmt.Sprintf`. The `vm.Sparkline` field/`sparklinePoints` become unused by the template but remain populated by `toOverviewVM`; leave them (removing them is out of scope and other cluster pages may reference the VM).

- [ ] **Step 10:** Add chart-point + conn mapping helpers and populate the new `OverviewVM` fields in `internal/api/overview.go`. Add fields to the struct (Task 4 interface list) and in `toOverviewVM`:
```go
	vm.QPSLine, vm.QPSArea = polylineArea(qpsValues(d.QPSBuckets), 560, 118)
	// vm.QPS already holds queries/SEC (last hourly bucket Calls ÷ 3600, set at
	// the top of toOverviewVM). The header label is "QUERIES / SEC", so render
	// the per-second value directly — do NOT multiply by 3600 (that would print
	// calls-per-hour under a /sec label).
	vm.QPSVal = fmt.Sprintf("%.1f", vm.QPS)
	// latency + conn states are store-backed → set in the handler (need ctx).
```
Latency/conn/recent need `ctx` + stores, so they are filled by a shared `enrichClusterBody` helper used by both `handleClusterOverview` and `handleClusterTab` (Step 11). It resolves the cluster's server set **once** and reuses it for latency and conn (no double lookup — this replaces the undefined `serverIDsForCluster`/`serverIDsForClusterCached` names from the earlier draft):
```go
// enrichClusterBody fills the OVERVIEW-tab body fields (mean-latency chart,
// connection-state bar, recent insights) that need ctx + stores. Shared by the
// full page and the tab partial. serverIDs is resolved once and reused.
func (s *Server) enrichClusterBody(
	r *http.Request, clusterID string, since, until time.Time,
	detail *fleetview.ClusterDetail, vm web.OverviewVM,
) web.OverviewVM {
	serverIDs, _ := s.conf.ServerIDsForCluster(r.Context(), clusterID)
	latBuckets, _ := s.stats.LatencyBucketsForServers(r.Context(), serverIDs, since, until)
	vm.LatLine, _ = polylineArea(latValues(latBuckets), 560, 118)
	if n := len(latBuckets); n > 0 {
		vm.LatVal = fmt.Sprintf("%.1f", latBuckets[n-1].MeanMs)
	}
	conns, _ := fleetview.ConnStates(r.Context(), s.stats, serverIDs, since, until)
	vm.ConnStates, vm.ConnLabel = connStatesVM(conns)
	vm.RecentInsights = recentInsightsVM(clusterID, detail.Insights)
	return vm
}
```
The final `handleClusterOverview` (this is the complete Task-2→3→4 version — it supersedes the drafts in Task 2 Step 5 and Task 3 Step 4):
```go
func (s *Server) handleClusterOverview(w http.ResponseWriter, r *http.Request) {
	clusterID := r.PathValue("clusterID")
	now := time.Now().UTC()
	since := now.AddDate(0, 0, -1)
	detail, found, err := fleetview.GetClusterDetail(r.Context(), s.conf, s.stats, clusterID, since, now)
	if err != nil || !found {
		http.NotFound(w, r)
		return
	}
	issues := s.scopeIssuesVM(r, "CLUSTER", true, clusterID, "", since, now)
	nodeHealth := map[string]string{}
	for i := range detail.Instances {
		inst := detail.Instances[i].Instance
		ids, _ := s.conf.ServerIDsForInstance(r.Context(), inst.ID)
		nis, _ := fleetview.ScopeIssues(r.Context(), s.stats, ids, since, now)
		nodeHealth[inst.ID] = worstSeverity(nis)
	}
	vm := toOverviewVM(&detail, issues, nodeHealth)
	vm = s.enrichClusterBody(r, clusterID, since, now, &detail, vm)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.OverviewPage(vm).Render(r.Context(), w)
}
```
Add helpers:
```go
func qpsValues(bs []store.QPSBucket) []float64 {
	out := make([]float64, len(bs))
	for i := range bs {
		out[i] = float64(bs[i].Calls)
	}
	return out
}
func latValues(bs []store.LatencyBucket) []float64 {
	out := make([]float64, len(bs))
	for i := range bs {
		out[i] = bs[i].MeanMs
	}
	return out
}

// polylineArea maps values to an SVG polyline (line) and a closed polygon (area)
// over a w×h viewBox, 8px top/bottom padding. Returns "","" for <2 points.
func polylineArea(vals []float64, w, h float64) (line, area string) {
	if len(vals) < 2 {
		return "", ""
	}
	minV, maxV := vals[0], vals[0]
	for _, v := range vals[1:] {
		if v < minV {
			minV = v
		}
		if v > maxV {
			maxV = v
		}
	}
	pad := 8.0
	n := len(vals)
	pts := make([]string, n)
	for i, v := range vals {
		x := float64(i) * w / float64(n-1)
		var y float64
		if maxV == minV {
			y = h / 2
		} else {
			y = (h - pad) - ((v-minV)/(maxV-minV))*(h-2*pad)
		}
		pts[i] = fmt.Sprintf("%.1f,%.1f", x, y)
	}
	line = strings.Join(pts, " ")
	area = fmt.Sprintf("0,%.0f %s %.0f,%.0f", h, line, w, h)
	return line, area
}

// connStatesVM maps fleetview conn states to view-model segments (x/width over a
// 100-wide bar) with token fill colors, and a summary label.
func connStatesVM(states []fleetview.ConnState) ([]web.ConnStateVM, string) {
	var total int64
	var active int64
	for _, s := range states {
		total += s.Count
		if s.State == "active" {
			active = s.Count
		}
	}
	if total == 0 {
		return nil, "CONNECTIONS BY STATE · NO SAMPLES"
	}
	var x float64
	out := make([]web.ConnStateVM, 0, len(states))
	for _, s := range states {
		w := float64(s.Count) / float64(total) * 100
		out = append(out, web.ConnStateVM{
			Label: s.State, Count: s.Count, X: x, W: w, FillClass: connSegClass(s.State),
		})
		x += w
	}
	return out, fmt.Sprintf("CONNECTIONS BY STATE · %d / %d", active, total)
}

// connSegClass maps a connection state to its scope.css color modifier class.
func connSegClass(state string) string {
	switch state {
	case "active":
		return "is-active"
	case "idle":
		return "is-idle"
	case "idle in transaction":
		return "is-idletx"
	default:
		return "is-other"
	}
}
func recentInsightsVM(clusterID string, insights []store.InsightRow) []web.RecentInsightVM {
	out := make([]web.RecentInsightVM, 0, len(insights))
	for i := range insights {
		in := &insights[i]
		out = append(out, web.RecentInsightVM{
			Severity: fleetview.NormalizeSeverity(in.Severity), // exported in Task 1
			Label:    in.Kind,
			FP:       in.Fingerprint,
			Detail:   in.Detail,
			Href:     templ.SafeURL("/databases/" + clusterID + "/queries#drill-" + in.Fingerprint),
		})
	}
	return out
}
```
Note: `fleetview.NormalizeSeverity` is already exported (Task 1 Step 3 defines it exported), so `recentInsightsVM` calls it directly — there is no rename to do here and no `NormalizeSeverityExported` placeholder. `recentInsightsVM` uses `templ.SafeURL`, so the `"github.com/a-h/templ"` import added in Task 2 covers it. `enrichClusterBody` resolves `serverIDs` once via `s.conf.ServerIDsForCluster` and reuses it for both latency and conn — no separate helper and no double lookup.

- [ ] **Step 11:** Register the tab partial route in `internal/api/server.go` routes():
```go
	s.mux.HandleFunc("GET /partial/databases/{clusterID}/tab/{tab}", s.handleClusterTab)
```
Create `internal/api/cluster_tabs.go`:
```go
package api

import (
	"net/http"
	"time"

	"github.com/dobbo-ca/lynceus/internal/fleetview"
	"github.com/dobbo-ca/lynceus/web"
)

// handleClusterTab renders one in-page cluster tab as an HTMX fragment. It
// returns ClusterTabsInner (tab bar + body), NOT just the body, so the swap
// re-renders the tab bar with the clicked tab marked is-active — otherwise the
// active underline would stay stuck on OVERVIEW. The tab buttons target
// #cluster-tabs-wrap, so returning the inner content (no outer wrapper div)
// replaces the wrapper's contents exactly.
func (s *Server) handleClusterTab(w http.ResponseWriter, r *http.Request) {
	clusterID := r.PathValue("clusterID")
	tab := r.PathValue("tab")
	now := time.Now().UTC()
	since := now.AddDate(0, 0, -1)
	detail, found, err := fleetview.GetClusterDetail(r.Context(), s.conf, s.stats, clusterID, since, now)
	if err != nil || !found {
		http.NotFound(w, r)
		return
	}
	vm := toOverviewVM(&detail, web.ScopedIssuesVM{ScopeKind: "CLUSTER"}, nil)
	vm = s.enrichClusterBody(r, clusterID, since, now, &detail, vm)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.ClusterTabsInner(vm, tab).Render(r.Context(), w)
}
```
`enrichClusterBody` (defined in Step 10) is the single shared enrich path — no duplicate latency/conn code between the two handlers.

- [ ] **Step 12:** Tests — assert charts + conn bar render on the page and the tab partial returns a fragment. Add to `internal/api/overview_test.go`:
```go
func TestOverviewPage_chartsAndConnBar(t *testing.T) {
	srv, clusterID, _ := setupOverview(t)
	resp, _ := http.Get(srv.URL + "/databases/" + clusterID)
	defer func() { _ = resp.Body.Close() }()
	b := readBody(t, resp)
	if !strings.Contains(b, "MEAN LATENCY") || !strings.Contains(b, "QUERIES / SEC") {
		t.Error("missing dual charts")
	}
	if !strings.Contains(b, "CONNECTIONS BY STATE") {
		t.Error("missing connection-state bar")
	}
	if !strings.Contains(b, "cluster-tabs") {
		t.Error("missing in-page tabs")
	}
	// The sidebar is kept transitionally (ly-ae6.3 owns its removal) and renders
	// a mixed-case "Settings" link, so we must NOT assert on the whole-page body.
	// Assert instead that the in-page tab bar has no Settings tab: no tab button
	// deep-links to /tab/settings.
	if strings.Contains(b, "/tab/settings") {
		t.Error("out-of-spec Settings tab present in cluster in-page tab bar")
	}
}

func TestClusterTab_partialFragment(t *testing.T) {
	srv, clusterID, _ := setupOverview(t)
	resp, _ := http.Get(srv.URL + "/partial/databases/" + clusterID + "/tab/queries")
	defer func() { _ = resp.Body.Close() }()
	b := readBody(t, resp)
	if strings.Contains(strings.ToLower(b), "<!doctype") {
		t.Error("tab partial returned a full document")
	}
	if !strings.Contains(b, "SELECT") {
		t.Errorf("queries tab missing query row; body: %s", b)
	}
	// Regression guard for the stuck-active-tab bug: the partial must re-render
	// the tab bar (not just the body) with an is-active tab, so the underline
	// moves to the clicked tab. handleClusterTab returns ClusterTabsInner.
	if !strings.Contains(b, "cluster-tabs") || !strings.Contains(b, "is-active") {
		t.Errorf("tab partial did not re-render the tab bar with an active tab; body: %s", b)
	}
}
```

- [ ] **Step 13:** Update the existing legacy assertion that this task's restructure breaks, then regenerate + run. **Required edit — `TestOverviewPage_returns200WithContent` in `internal/api/overview_test.go`:** it currently asserts `strings.Contains(b, "SELECT")`. After this task the queries table is **no longer on the initial page** — it moved behind the QUERIES tab, which renders only in `ClusterTabBody`'s `"queries"` case (loaded on demand / via the tab partial). The default OVERVIEW tab body renders `ClusterCharts`+`ConnBar`+`RecentInsights`, none of which contain `SELECT`. So the `SELECT` assertion **will fail** and must be replaced. (The old plan draft's claim that "SELECT appears in the default tab body via OverviewQueries" was wrong — `OverviewQueries` renders only in the QUERIES tab.) Replace the `SELECT` block with a check for the new structure; keep the doctype + `my-cluster` checks. The `Overview` check still passes because the kept sidebar renders an "Overview" link — leave it:
```go
	// Queries moved behind the QUERIES tab (loaded on demand); the initial
	// overview body now leads with the scoped-issues card + in-page tabs.
	// SELECT is asserted in the queries-tab partial (TestClusterTab_partialFragment).
	if !strings.Contains(b, "cluster-tabs") {
		t.Error("body missing cluster in-page tab bar")
	}
```
Then:
```
make templ && go test ./internal/store/... ./internal/fleetview/... ./internal/api/... ./web/...
```
Expected: PASS across all four packages, including the edited `TestOverviewPage_returns200WithContent`.

- [ ] **Step 14:** Commit.
```
git add internal/store/rollup.go internal/store/stats.go internal/store/rollup_test.go internal/fleetview/connstate.go internal/fleetview/connstate_test.go web/overview.templ web/overview_templ.go web/format.go internal/api/overview.go internal/api/cluster_tabs.go internal/api/server.go internal/api/overview_test.go
git commit -m "ly-ae6.6: cluster in-page tabs + QPS/latency charts + conn-state bar + recent insights"
```
(`scope.css` already carries the `.chart-*` / `.conn-seg` color classes from Task 1 Step 5 — nothing to re-add here. `web/format.go` gains `floatToStr`/`int64ToStr` in this task.)

---

### Task 5: Node-scope Overview screen + route

Add the node-scoped Overview: a back link to the cluster, node identity (role, name, version/source when modeled), the "OPEN ISSUES ON THIS NODE" card, and a single node stat card. Gap closed: "No node-scoped screen set (Overview…) rendered under a NODE: scope nav; no /node routes exist." Node Config/Capabilities nav entries point at existing screens (ly-ae6.7 / caps redesign) — not built here.

**Files**
- Create: `internal/fleetview/node.go` + `internal/fleetview/node_test.go` — `GetNodeDetail`
- Create: `web/node.templ` (+ regen) — `NodeOverviewPage` / `NodeOverviewView`
- Create: `internal/api/node.go` — `handleNodeOverview`
- Modify: `internal/api/server.go` — register `GET /databases/{clusterID}/nodes/{instanceID}`
- Create: `internal/api/node_test.go`

**Interfaces**

Produces (fleetview):
```go
type NodeDetail struct {
	Cluster     store.Cluster
	Instance    store.Instance
	ServerIDs   []string
	StreamCount int
	Calls       int64
	ActiveConns int64
	Found       bool
}
func GetNodeDetail(ctx context.Context, cfg store.Config, stats store.Stats, clusterID, instanceID string, since, until time.Time) (NodeDetail, bool, error)
```
Produces (web):
```go
type NodeVM struct {
	ClusterID   string
	ClusterName string
	InstanceID  string
	Name        string
	Role        string // "primary" | "replica"
	Version     string // "" until backend
	Source      string // "" until backend
	HealthLine  string
	HealthMod   string
	ActiveConns int64
	Calls       int64
	StreamCount int
	Issues      ScopedIssuesVM
}
templ NodeOverviewPage(vm NodeVM)
```

Consumes: `store.Config.ListInstances`, `store.Config.ServerIDsForInstance`, `store.Stats.ThroughputForServers`, `store.Stats.ActivitySummaryForServers`, `fleetview.ScopeIssues`.

**Steps**

- [ ] **Step 1:** Failing fleetview test `internal/fleetview/node_test.go`: seed cluster+instance+2 servers with throughput, assert `GetNodeDetail` returns the instance, its 2 server ids, and rolled calls.
```go
func TestGetNodeDetail(t *testing.T) {
	ctx := context.Background()
	// newStores (summary_test.go) spins up separate config + stats DBs and
	// applies BOTH migration sets; it returns (Config, Stats, configPool).
	cfg, stats, cfgPool := newStores(t)
	for _, id := range []string{"nd-a", "nd-b"} {
		if _, err := cfgPool.Exec(ctx, `INSERT INTO servers (id, name) VALUES ($1,$1)`, id); err != nil {
			t.Fatalf("seed server %s: %v", id, err)
		}
	}
	cl, _ := cfg.CreateCluster(ctx, "c")
	in, _ := cfg.CreateInstance(ctx, cl.ID, "primary") // "primary" is the instance NAME; Role defaults to "unknown"
	_ = cfg.AssignServerToInstance(ctx, "nd-a", in.ID)
	_ = cfg.AssignServerToInstance(ctx, "nd-b", in.ID)
	now := time.Now().UTC()
	_ = stats.WriteQueryStats(ctx, []store.QueryStat{{ServerID: "nd-a", CollectedAt: now.Add(-time.Minute), Fingerprint: "f", NormalizedQuery: "q", Calls: 3, TotalTimeMs: 9}})
	got, found, err := fleetview.GetNodeDetail(ctx, cfg, stats, cl.ID, in.ID, now.AddDate(0, 0, -1), now.Add(time.Minute))
	if err != nil || !found {
		t.Fatalf("GetNodeDetail found=%v err=%v", found, err)
	}
	if got.Instance.ID != in.ID || len(got.ServerIDs) != 2 || got.Calls != 3 {
		t.Errorf("got %+v", got)
	}
}
```

- [ ] **Step 2:** Run — FAIL. `go test ./internal/fleetview/ -run TestGetNodeDetail`.

- [ ] **Step 3:** Implement `internal/fleetview/node.go`:
```go
package fleetview

import (
	"context"
	"time"

	"github.com/dobbo-ca/lynceus/internal/store"
)

// NodeDetail is the node-scope Overview view-model for one instance.
type NodeDetail struct {
	Cluster     store.Cluster
	Instance    store.Instance
	ServerIDs   []string
	StreamCount int
	Calls       int64
	ActiveConns int64
}

// GetNodeDetail assembles a single node's Overview data. found=false (no error)
// when clusterID or instanceID is unknown / not in that cluster.
func GetNodeDetail(
	ctx context.Context, cfg store.Config, stats store.Stats,
	clusterID, instanceID string, since, until time.Time,
) (NodeDetail, bool, error) {
	clusters, err := cfg.ListClusters(ctx)
	if err != nil {
		return NodeDetail{}, false, err
	}
	var cluster store.Cluster
	var okC bool
	for _, cl := range clusters {
		if cl.ID == clusterID {
			cluster, okC = cl, true
			break
		}
	}
	if !okC {
		return NodeDetail{}, false, nil
	}
	instances, err := cfg.ListInstances(ctx, clusterID)
	if err != nil {
		return NodeDetail{}, false, err
	}
	var inst store.Instance
	var okI bool
	for _, in := range instances {
		if in.ID == instanceID {
			inst, okI = in, true
			break
		}
	}
	if !okI {
		return NodeDetail{}, false, nil
	}
	serverIDs, err := cfg.ServerIDsForInstance(ctx, instanceID)
	if err != nil {
		return NodeDetail{}, false, err
	}
	d := NodeDetail{Cluster: cluster, Instance: inst, ServerIDs: serverIDs, StreamCount: len(serverIDs)}
	if len(serverIDs) == 0 {
		return d, true, nil
	}
	tp, err := stats.ThroughputForServers(ctx, serverIDs, since, until)
	if err != nil {
		return NodeDetail{}, false, err
	}
	d.Calls = tp.Calls
	act, err := stats.ActivitySummaryForServers(ctx, serverIDs, since, until)
	if err != nil {
		return NodeDetail{}, false, err
	}
	d.ActiveConns = act.ActiveConns
	return d, true, nil
}
```

- [ ] **Step 4:** Run — PASS. `go test ./internal/fleetview/ -run TestGetNodeDetail`.

- [ ] **Step 5:** Create `web/node.templ`:
```go
package web

// NodeVM is the node-scope Overview view-model.
type NodeVM struct {
	ClusterID   string
	ClusterName string
	InstanceID  string
	Name        string
	Role        string
	Version     string
	Source      string
	HealthLine  string
	HealthMod   string
	ActiveConns int64
	Calls       int64
	StreamCount int
	Issues      ScopedIssuesVM
}

// NodeOverviewPage renders the full node-scope Overview page.
templ NodeOverviewPage(vm NodeVM) {
	@Layout("Lynceus — "+vm.Name, "node overview") {
		<div class="scope-screen">
			<div class="scope-head">
				<a class="scope-back" href={ templ.SafeURL("/databases/" + vm.ClusterID) }>← { vm.ClusterName }</a>
				<span class={ "node-role is-" + vm.Role }>{ vm.Role }</span>
				<span class="scope-title">{ vm.Name }</span>
				<span class="scope-live">LIVE</span>
				if vm.Version != "" {
					<span class="scope-meta">v{ vm.Version }</span>
				}
				if vm.Source != "" {
					<span class="scope-meta">{ vm.Source }</span>
				}
				<span class="scope-spacer"></span>
				<span class={ "scope-health is-" + vm.HealthMod }>{ vm.HealthLine }</span>
			</div>
			@ScopedIssues(vm.Issues)
			<div class="node-card">
				<div class="node-body">
					<span class="node-name">{ vm.Name }</span>
					<span class="node-meta">
						{ int64ToStr(vm.Calls) } calls · { int64ToStr(vm.ActiveConns) } active conns · { intToStr(vm.StreamCount) } databases
					</span>
				</div>
			</div>
		</div>
	}
}
```

- [ ] **Step 6:** Create `internal/api/node.go`:
```go
package api

import (
	"net/http"
	"time"

	"github.com/dobbo-ca/lynceus/internal/fleetview"
	"github.com/dobbo-ca/lynceus/web"
)

// handleNodeOverview renders the node-scope Overview page.
func (s *Server) handleNodeOverview(w http.ResponseWriter, r *http.Request) {
	clusterID := r.PathValue("clusterID")
	instanceID := r.PathValue("instanceID")
	now := time.Now().UTC()
	since := now.AddDate(0, 0, -1)
	d, found, err := fleetview.GetNodeDetail(r.Context(), s.conf, s.stats, clusterID, instanceID, since, now)
	if err != nil || !found {
		http.NotFound(w, r)
		return
	}
	issues := s.scopeIssuesVM(r, "NODE", false, clusterID, instanceID, since, now)
	line, mod := rollup(issues)
	vm := web.NodeVM{
		ClusterID:   clusterID,
		ClusterName: d.Cluster.Name,
		InstanceID:  instanceID,
		Name:        d.Instance.Name,
		Role:        roleClass(d.Instance.Role),
		HealthLine:  line,
		HealthMod:   mod,
		ActiveConns: d.ActiveConns,
		Calls:       d.Calls,
		StreamCount: d.StreamCount,
		Issues:      issues,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.NodeOverviewPage(vm).Render(r.Context(), w)
}
```
This handler reuses `s.scopeIssuesVM`, `rollup`, and `roleClass` from `internal/api/overview.go` (same package) and uses no `fmt` of its own — hence no `fmt` import.

- [ ] **Step 7:** Register the route in `internal/api/server.go`:
```go
	s.mux.HandleFunc("GET /databases/{clusterID}/nodes/{instanceID}", s.handleNodeOverview)
```

- [ ] **Step 8:** Create `internal/api/node_test.go`:
```go
package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dobbo-ca/lynceus/internal/api"
	"github.com/dobbo-ca/lynceus/internal/store"
)

// setupNode mirrors setupOverview (overview_test.go, same api_test package) but
// also returns the instance ID for the node-scope routes. Reuses newDBPool
// (databases_test.go) and readBody (overview_test.go).
func setupNode(t *testing.T) (srv *httptest.Server, clusterID, instanceID string) {
	t.Helper()
	ctx := context.Background()
	configPool := newDBPool(t)
	statsPool := newDBPool(t)
	if err := store.ApplyConfigMigrations(ctx, configPool); err != nil {
		t.Fatalf("config migrate: %v", err)
	}
	if err := store.ApplyStatsMigrations(ctx, statsPool); err != nil {
		t.Fatalf("stats migrate: %v", err)
	}
	cfg := store.NewConfig(configPool)
	stats := store.NewStats(statsPool)
	for _, id := range []string{"nd-srv-a", "nd-srv-b"} {
		if _, err := configPool.Exec(ctx, `INSERT INTO servers (id, name) VALUES ($1, $1)`, id); err != nil {
			t.Fatalf("seed server %s: %v", id, err)
		}
	}
	cl, err := cfg.CreateCluster(ctx, "my-cluster")
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}
	inst, err := cfg.CreateInstance(ctx, cl.ID, "primary")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	for _, id := range []string{"nd-srv-a", "nd-srv-b"} {
		if err := cfg.AssignServerToInstance(ctx, id, inst.ID); err != nil {
			t.Fatalf("assign %s: %v", id, err)
		}
	}
	now := time.Now().UTC()
	if err := stats.WriteQueryStats(ctx, []store.QueryStat{
		{ServerID: "nd-srv-a", CollectedAt: now.Add(-10 * time.Minute), Fingerprint: "fp-nd",
			NormalizedQuery: "SELECT $1", Calls: 12, TotalTimeMs: 24},
	}); err != nil {
		t.Fatalf("seed stats: %v", err)
	}
	httpSrv := httptest.NewServer(api.NewServer(api.Config{DevAuth: true}, stats, cfg).Handler())
	t.Cleanup(httpSrv.Close)
	return httpSrv, cl.ID, inst.ID
}

func TestNodeOverview_returns200(t *testing.T) {
	srv, clusterID, instanceID := setupNode(t)
	resp, err := http.Get(srv.URL + "/databases/" + clusterID + "/nodes/" + instanceID)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	b := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, b)
	}
	if !strings.Contains(b, "scope-back") {
		t.Error("missing back link to cluster")
	}
	if !strings.Contains(b, "OPEN ISSUES ON THIS NODE") && !strings.Contains(b, "NO OPEN CHECKS OR INSIGHTS ON THIS NODE") {
		t.Error("missing node scoped-issues card")
	}
}

func TestNodeOverview_unknownNode_404(t *testing.T) {
	srv, clusterID, _ := setupNode(t)
	resp, _ := http.Get(srv.URL + "/databases/" + clusterID + "/nodes/does-not-exist")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status=%d, want 404", resp.StatusCode)
	}
}
```
`setupNode` is defined in full above (do not also alter `setupOverview`, whose 3-value signature the existing cluster tests depend on). It seeds no checks/insights, so the node Overview shows the green "NO OPEN CHECKS OR INSIGHTS ON THIS NODE" clean strip — which `TestNodeOverview_returns200` accepts via its `||` assertion.

- [ ] **Step 9:** Regenerate + run.
```
make templ && go test ./internal/fleetview/... ./internal/api/... ./web/...
```
Expected: PASS.

- [ ] **Step 10:** Commit.
```
git add internal/fleetview/node.go internal/fleetview/node_test.go web/node.templ web/node_templ.go internal/api/node.go internal/api/node_test.go internal/api/server.go
git commit -m "ly-ae6.6: node-scope Overview screen + /databases/{id}/nodes/{instanceID} route"
```

---

### Task 6: Pooler-scope Overview + Config-pgbouncer shells + routes (backend-dependent)

Add the pooler-scoped Overview and Config-pgbouncer screens as token-based shells wired to a `web.PoolerVM`. The pooler data model is absent (see "Backend dependency" above), so both screens render a designed empty state until the collector reports.

**The design's full POOLER-scope screen set (prototype ~line 2396–2400) is: Overview / Config · pgbouncer / Activity→Connections [SOON, T2] / Checks / Logs.** This task does NOT build all five — and does not silently drop the other three. Per the Per-scope nav map in the Integration Contract:
- **Overview ● + Config · pgbouncer ● (built here):** these are the only two screens the design assigns to the POOLER level directly (`['nodes','Overview'],['configadvisor','Config · pgbouncer']`). Built as token shells over `web.PoolerVM`.
- **Activity → Connections [SOON, T2] (not built by anyone):** the prototype itself flags this entry `'soon','t2'` — a roadmap placeholder. It is a T2 live-connections surface with no backend and no owning bead yet; building it is out of scope for ly-ae6.6 and for the pooler backend bead's first cut. Explicitly deferred, not dropped.
- **Checks (delegated → existing `/checks`):** the Checks screen is shared across all scopes and owned by the Checks bead; ly-ae6.3's nav wires the pooler Checks entry to it. Not rebuilt here.
- **Logs (delegated → Logs bead):** no `/logs` route exists yet; the Logs screen is owned by the Logs bead and nav-wired by ly-ae6.3. Not built here.

Gap closed (UI shells): the two POOLER-level screens + their `/poolers/...` routes. The remaining three are shared/roadmap screens delegated with named owners above; backend model + pgbouncer reader remain a filed dependency (Task 7).

**Files**
- Create: `web/pooler.templ` (+ regen) — `PoolerOverviewPage`, `PoolerConfigPage`
- Create: `internal/api/pooler.go` — `handlePoolerOverview`, `handlePoolerConfig`
- Modify: `internal/api/server.go` — register the two routes
- Create: `internal/api/pooler_test.go`

**Interfaces**

Produces (web) — the exact contract the backend must populate later:
```go
type PoolerSettingVM struct {
	Key   string
	Value string
	Group string // "pgbouncer" group label
}
type PoolerVM struct {
	ClusterID   string
	ClusterName string
	PoolerID    string
	Name        string
	Version     string // e.g. "pgbouncer 1.22" — "" until backend
	Mode        string // "txn" | "session" | "" until backend
	Issues      ScopedIssuesVM
	Settings    []PoolerSettingVM // empty until backend pgbouncer reader lands
	Backed      bool              // false today (no pooler data model)
}
templ PoolerOverviewPage(vm PoolerVM)
templ PoolerConfigPage(vm PoolerVM)
```

Consumes: `store.Config.ListClusters` (to validate/resolve the cluster + name and 404 unknown clusters). No pooler resolution exists; the handler renders the shell for any `{poolerID}` under a known cluster, with `Backed=false`. `Issues` is an empty (clean-strip) `ScopedIssuesVM` today.

**Steps**

- [ ] **Step 1:** Create `internal/api/pooler_test.go`:
```go
package api_test

import (
	"net/http"
	"strings"
	"testing"
)

func TestPoolerOverview_rendersEmptyStateShell(t *testing.T) {
	srv, clusterID, _ := setupOverview(t)
	resp, err := http.Get(srv.URL + "/databases/" + clusterID + "/poolers/pgb-01")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	b := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, b)
	}
	if !strings.Contains(b, "pgb-01") {
		t.Error("missing pooler name")
	}
	if !strings.Contains(b, "NO OPEN CHECKS OR INSIGHTS ON THIS POOLER") {
		t.Error("missing pooler clean strip")
	}
}

func TestPoolerConfig_rendersEmptyState(t *testing.T) {
	srv, clusterID, _ := setupOverview(t)
	resp, _ := http.Get(srv.URL + "/databases/" + clusterID + "/poolers/pgb-01/config")
	defer func() { _ = resp.Body.Close() }()
	b := readBody(t, resp)
	if !strings.Contains(b, "pgbouncer") || !strings.Contains(strings.ToUpper(b), "COLLECTOR") {
		t.Errorf("config empty-state missing pgbouncer/collector copy; body: %s", b)
	}
}

func TestPoolerOverview_unknownCluster_404(t *testing.T) {
	srv, _, _ := setupOverview(t)
	resp, _ := http.Get(srv.URL + "/databases/nope/poolers/pgb-01")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status=%d, want 404", resp.StatusCode)
	}
}
```

- [ ] **Step 2:** Run — FAIL (routes/handlers/components missing).
```
go test ./internal/api/ -run TestPooler
```
Expected: FAIL.

- [ ] **Step 3:** Create `web/pooler.templ`:
```go
package web

type PoolerSettingVM struct {
	Key   string
	Value string
	Group string
}
type PoolerVM struct {
	ClusterID   string
	ClusterName string
	PoolerID    string
	Name        string
	Version     string
	Mode        string
	Issues      ScopedIssuesVM
	Settings    []PoolerSettingVM
	Backed      bool
}

// poolerHead renders the shared pooler screen header.
templ poolerHead(vm PoolerVM) {
	<div class="scope-head">
		<a class="scope-back" href={ templ.SafeURL("/databases/" + vm.ClusterID) }>← { vm.ClusterName }</a>
		<span class="node-role is-pooler">POOLER</span>
		<span class="scope-title">{ vm.Name }</span>
		<span class="scope-live">LIVE</span>
		if vm.Version != "" {
			<span class="scope-meta">{ vm.Version }</span>
		}
		if vm.Mode != "" {
			<span class="scope-meta">{ vm.Mode } mode</span>
		}
	</div>
}

// PoolerOverviewPage renders the pooler-scope Overview.
templ PoolerOverviewPage(vm PoolerVM) {
	@Layout("Lynceus — "+vm.Name, "pooler overview") {
		<div class="scope-screen">
			@poolerHead(vm)
			@ScopedIssues(vm.Issues)
			if !vm.Backed {
				<div class="scope-empty">PGBOUNCER STATS APPEAR ONCE THE COLLECTOR REPORTS SHOW POOLS/STATS/CLIENTS/LISTS FOR THIS POOLER.</div>
			}
		</div>
	}
}

// PoolerConfigPage renders the pooler Config · pgbouncer screen.
templ PoolerConfigPage(vm PoolerVM) {
	@Layout("Lynceus — "+vm.Name+" config", "pgbouncer config") {
		<div class="scope-screen">
			@poolerHead(vm)
			if len(vm.Settings) == 0 {
				<div class="scope-empty">PGBOUNCER CONFIG APPEARS ONCE THE COLLECTOR REPORTS THIS POOLER TO LYNCEUS.</div>
			} else {
				<div class="issues-card">
					<div class="issues-head">PGBOUNCER SETTINGS</div>
					for i := range vm.Settings {
						<div class="issue-row">
							<span class="issue-id">{ vm.Settings[i].Key }</span>
							<span class="issue-detail">{ vm.Settings[i].Value }</span>
						</div>
					}
				</div>
			}
		</div>
	}
}
```

- [ ] **Step 4:** Create `internal/api/pooler.go`:
```go
package api

import (
	"net/http"

	"github.com/dobbo-ca/lynceus/web"
)

// poolerVM resolves the cluster (404 unknown) and builds the shell view-model.
// Pooler data is not modeled yet (see ly-ae6.6 plan backend dependency): the
// screen renders an empty state with a clean scoped-issues strip.
func (s *Server) poolerVM(r *http.Request) (web.PoolerVM, bool) {
	clusterID := r.PathValue("clusterID")
	poolerID := r.PathValue("poolerID")
	clusters, err := s.conf.ListClusters(r.Context())
	if err != nil {
		return web.PoolerVM{}, false
	}
	var name string
	var found bool
	for _, cl := range clusters {
		if cl.ID == clusterID {
			name, found = cl.Name, true
			break
		}
	}
	if !found {
		return web.PoolerVM{}, false
	}
	return web.PoolerVM{
		ClusterID:   clusterID,
		ClusterName: name,
		PoolerID:    poolerID,
		Name:        poolerID,
		Issues:      web.ScopedIssuesVM{ScopeKind: "POOLER", Count: 0},
		Backed:      false,
	}, true
}

func (s *Server) handlePoolerOverview(w http.ResponseWriter, r *http.Request) {
	vm, ok := s.poolerVM(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.PoolerOverviewPage(vm).Render(r.Context(), w)
}

func (s *Server) handlePoolerConfig(w http.ResponseWriter, r *http.Request) {
	vm, ok := s.poolerVM(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.PoolerConfigPage(vm).Render(r.Context(), w)
}
```

- [ ] **Step 5:** Register routes in `internal/api/server.go`:
```go
	s.mux.HandleFunc("GET /databases/{clusterID}/poolers/{poolerID}", s.handlePoolerOverview)
	s.mux.HandleFunc("GET /databases/{clusterID}/poolers/{poolerID}/config", s.handlePoolerConfig)
```

- [ ] **Step 6:** Regenerate + run.
```
make templ && go test ./internal/api/ -run TestPooler ./web/...
```
Expected: PASS.

- [ ] **Step 7:** Commit.
```
git add web/pooler.templ web/pooler_templ.go internal/api/pooler.go internal/api/pooler_test.go internal/api/server.go
git commit -m "ly-ae6.6: pooler-scope Overview + Config-pgbouncer shells + routes (backend-dependent)"
```

---

### Task 7: Full-suite verification + docs/beads hand-off

- [ ] **Step 1:** Build + full test run.
```
go build ./... && go test ./...
```
Expected: PASS across store, fleetview, api, web.

- [ ] **Step 2:** Confirm templ generated files are in sync (CI guard).
```
make templ && git diff --exit-code -- 'web/*_templ.go'
```
Expected: no diff (regenerated files already committed).

- [ ] **Step 3:** Confirm the no-external-hosts contract still passes (Layout gained one self-hosted `scope.css` link only).
```
go test ./web/ -run TestLayout_NoExternalHosts
```
Expected: PASS.

- [ ] **Step 4:** Update the bead: move ly-ae6.6 from `needs-plan` → `ready-impl` after the plan is committed, per the Feature Work Lifecycle.
```
bd label remove ly-ae6.6 needs-plan && bd label add ly-ae6.6 ready-impl
bd note ly-ae6.6 "Plan at docs/superpowers/plans/ui-scoped-overview-detail.md. Backend dep: file a bead for collector pgbouncer reader + POOLER topology role + pooler stats/settings store (pooler screens are empty-state shells until then)."
```
(Do not close; implementation follows.)

- [ ] **Step 5:** Commit any doc/bead artifacts that belong in-tree (the plan file itself).
```
git add docs/superpowers/plans/ui-scoped-overview-detail.md
git commit -m "ly-ae6.6: implementation plan for scoped Overview + cluster/node/pooler detail"
```

---

## Self-Review

### Spec-coverage checklist — COMPARISON gaps (mandatory)

**#### Cluster detail + node cards — impl: present, parity: partial**
- [x] No back link (← FLEET / back to clusters) → **Task 2** (`.scope-back` "← CLUSTERS").
- [x] No cluster health indicator / DB→NODE→CLUSTER rollup line in header → **Task 2** (`HealthLine`/`HealthMod` + `rollup`, e.g. `[DEGRADED] 1 CRIT · 4 WARN`).
- [~] Header **meta** line below design parity → **partial (honest).** Design meta is `3 PG INSTANCES + 1 POOLER / STREAMING / PG 16.3`; Task 2 renders `N PG INSTANCES / N DATABASES` from what the backend models today. Pooler count, streaming/topology, and version are **not modeled** (no pooler role; version is backend-absent — epic ly-99s). Not invented; upgraded when the fleet-topology backend lands.
- [x] Scoped Overview does NOT lead with OPEN ISSUES ON THIS CLUSTER nor green clean strip → **Task 1** (`ScopedIssues`) wired in **Task 2**.
- [x] Node cards lack ⌖ scope button; generic Topology without per-node version/health/source → **Task 3** (`NodeCards` with `.scope-btn` link + role/health; version/source fields present, populated when backend models them — noted).
- [~] Vertical sidebar not horizontal OVERVIEW/QUERIES/INSIGHTS/ACTIVITY tabs; out-of-spec settings tab → **half-closed by design.** **Task 4 adds** the horizontal in-page tab bar (`ClusterTabsWrap`/`ClusterTabsInner`, tabs OVERVIEW/QUERIES/INSIGHTS/ACTIVITY, no Settings tab; the tab partial re-renders the bar so the active underline moves; test asserts no `/tab/settings` entry). **The vertical `ClusterSidebar` (incl. its `/settings` link) is deliberately KEPT** as a transitional frame — its removal/per-scope rebuild is **ly-ae6.3's** (Integration Contract). Because the sidebar remains, the page body still contains the sidebar's "Settings" text, so the "no Settings" test is scoped to the tab bar only, not the whole body. Honest status: tab bar ✅ this bead; sidebar+`/settings` removal ⏳ ly-ae6.3.
- [x] Only a single hardcoded QPS sparkline — no QPS+latency charts → **Task 4** (`ClusterCharts` dual SVG + `LatencyBucketsForServers`; QPS header value renders queries/**sec** to match its label, not calls/hour).
- [x] Activity view has no connection-state bar → **Task 4** (`ConnBar` + `fleetview.ConnStates`, on the OVERVIEW tab body per the design's cluster-overview layout).
- [x] Zero design-token adoption (font/theme/accent/radii) → all new markup uses `scope.css` token classes; no `#2b6cb0`, no `system-ui`; dark-default via tokens.css.

**#### Node & Pooler scope screens — impl: absent, parity: none**
- [x] Pooler absent from data model / no pgbouncer reader → **out of UI scope**; contract defined (`PoolerVM`, Task 6) + backend dependency filed (Task 7 note, `backendDeps`).
- [x] No node-scoped screen set (Overview) under NODE: scope; no /node routes → **Task 5** (`/databases/{clusterID}/nodes/{instanceID}`, `NodeOverviewPage`). **Design node-scope set = Overview / Config / Capabilities (+ shared Queries/Advisors/Activity/Console/Checks/Logs).** Built here: **Overview**. **Config → existing `/config-advisor` screen; Capabilities → existing capability screen** — both are shared screens the design's node nav points at (prototype line 2393), owned by ly-ae6.7 (per-node config) / ly-4ov (caps redesign); the Integration Contract's Per-scope nav map names the exact URLs ly-ae6.3 wires. Explicit delegation with a named integration point, not a silent drop; deliberately no throwaway node Config/Capabilities shell (would conflict with ly-ae6.7/ly-4ov).
- [~] No pooler-scoped screen set under POOLER:; no /pooler routes → **partially built + explicitly delegated.** **Design pooler set = Overview / Config·pgbouncer / Activity→Connections[SOON,T2] / Checks / Logs** (prototype line 2396–2400). **Built here (Task 6):** Overview + Config·pgbouncer shells (`/poolers/{poolerID}` + `/config`, empty-state). **Deferred/delegated:** Connections is `soon`/`t2` in the design itself (no backend, no owner) — deferred; Checks → existing `/checks`; Logs → Logs bead; both nav-wired by ly-ae6.3. Enumerated in Task 6 intro + Per-scope nav map.
- [x] No scope-nav framework / scope picker / back-to-FLEET → **dependency ly-ae6.2/ly-ae6.3**; this plan defines the URL scheme + ⌖ setters and consumes scope from the path (Integration Contract section).
- [x] No OPEN ISSUES ON THIS NODE/POOLER component → **Task 1** `ScopedIssues` reused at node (Task 5) and pooler (Task 6) scope.
- [x] config-advisor has no PGBOUNCER group (pooler Config backing) → **backend/ly-ae6.7 dependency**; Task 6 renders the Config shell + empty state and the `PoolerSettingVM` contract to fill.
- [x] Node data-source line not modeled → `NodeVM.Source` / `OverviewInstance.Source` fields defined, rendered when present, "" today (epic ly-99s) — noted, not invented.
- [x] Existing ClusterSidebar diverges / no tokens → **ly-ae6.3** owns the sidebar rebuild; not modified here (flagged below).

### Bead acceptance-criteria coverage (ly-ae6.6 description)
- [x] "Scoped Overview leading with OPEN ISSUES ON THIS (or green no-open strip)" → Task 1 + wired Tasks 2/5/6.
- [x] "node cards with select-scope" → Task 3.
- [x] "tabs" → Task 4 (OVERVIEW/QUERIES/INSIGHTS/ACTIVITY; active-tab indicator moves on swap).
- [~] "Config + Capabilities per scope" → **met via delegation + one built shell.** In the design, node-scope Config/Capabilities are **existing shared screens** (`/config-advisor`, capability matrix) the scoped nav points at — not new ly-ae6.6 screens; this plan defines the node-scope URL/nav contract (Per-scope nav map) so ly-ae6.3 wires them, and does not build throwaway shells that would conflict with ly-ae6.7/ly-4ov. Pooler **Config·pgbouncer** IS built here as a token shell (Task 6). Node Config/Capabilities screens themselves = ly-ae6.7 / ly-4ov. Honest status: contract + nav-wiring defined here; the node Config/Capabilities screens ship with their owning beads.
- [x] "pooler pgbouncer views" → Task 6 (Overview + Config·pgbouncer shells, `web.PoolerVM` contract-ready; live data behind the filed backend dependency).

### Out-of-scope, explicitly delegated (not left silently undone)
- Sidebar per-scope rebuild + removal of the `/settings` sidebar item/route → **ly-ae6.3**. Task 4 does not add a Settings tab to the new in-page tab bar (and tests assert no `/tab/settings` entry); it does NOT touch the kept legacy `ClusterSidebar` or its `/settings` link — those are ly-ae6.3's to remove. **Transitional state (stated explicitly):** until ly-ae6.3 lands, the cluster-detail screen is a token-based `<main>` inside the legacy `overview-layout`+`ClusterSidebar` frame, so it is only half-tokenized and visually diverges from the fully-tokenized node/pooler screens. Accepted, not an oversight.
- Top-bar SCOPE picker, ← FLEET control, searchable scope search → **ly-ae6.2**.
- Node Config (config-advisor per-node) + Capabilities screen tokenization → **ly-ae6.7 / ly-4ov caps redesign**.
- Pooler data model + pgbouncer collector reader + pooler stats/settings store → **new backend bead to file** (Task 7 note; `backendDeps`).
- Checks-screen deep-link "expand this check" target → **Checks bead**; interim `issueHref` links to `/checks` (insights link to the query drilldown anchor, which exists).

### Placeholder scan
No "TBD", "similar to Task N", or code-free steps. Every step gives a runnable command with an explicit expected FAIL/PASS. The only `// ...` markers in the plan (Task 2 Step 5 and Task 3 Step 4, both editing the pre-existing `toOverviewVM`) are explicit **"keep the existing function body unchanged, add these lines"** edit-markers over code that is already in `internal/api/overview.go` — not logic gaps; every NEW function/templ/CSS is written out in full. Where a value is genuinely backend-absent (node version/source, pooler settings, richer cluster meta), the field is defined in the view-model and rendered conditionally (`if x != ""`), never faked.

Previously-flagged placeholders — all eliminated (verified against the real code):
- Test pool helpers now use the **actual** package helpers: `internal/fleetview` → `newDB(t)` / `newStores(t)` (summary_test.go); `internal/store` → `newPool(t)` (store_test.go); `internal/api` → `newDBPool(t)` (databases_test.go). The invented `newStatsPool`/`newConfigPool`/`newDBPool`-in-store names are gone. `setupNode` is spelled out in full.
- `serverIDsForCluster(...)` / `serverIDsForClusterCached` (undefined) removed — replaced by `enrichClusterBody`, which resolves the set once via `s.conf.ServerIDsForCluster` and reuses it.
- `fleetview.NormalizeSeverityExported` placeholder removed — `NormalizeSeverity` is exported directly in Task 1 and called as-is.
- `_ = fmt.Sprint` dead line + its `fmt` import removed from `internal/api/node.go`.
- Task 4 Step 9's contradictory "Wait —" two-structure block collapsed into one: `ClusterTabsWrap`(page) → `ClusterTabsInner`(swap payload) → `ClusterTabBody`.

Design-fidelity fixes folded in (were review findings):
- `.node-role` gets `text-transform:uppercase` (design shows PRIMARY/REPLICA chips); `.node-health.is-info` colour added so info-severity node dots aren't uncolored; `healthLabel` returns INFO for the info mod.
- QPS header value renders `vm.QPS` (queries/sec) to match the "QUERIES / SEC" label — no ×3600.
- Cluster tab partial returns `ClusterTabsInner` (tab bar + body) so the active-tab underline moves; regression-tested.
- The legacy `TestOverviewPage_returns200WithContent` `SELECT` assertion is explicitly rewritten (queries moved behind the QUERIES tab); `TestOverviewPage_chartsAndConnBar`'s Settings check targets the tab bar (`/tab/settings`), not the whole body (which still contains the kept sidebar's "Settings").

### Type-consistency check
- `fleetview.ScopeIssue{Kind,Severity,ID,Detail,Server,Ref,AgeMin}` → mapped to `web.ScopeIssueVM{Severity,ID,Detail,Server,Age,Href}` in `s.scopeIssuesVM` (age via `humanizeAge int→string`, href via `issueHref`). Consistent.
- `store.ChecksResultRow.Status == "firing"` and `.Muted` filter; `.EvaluatedAt` for age. `store.InsightRow.CapturedAt` for age, `.Fingerprint` for `Ref`. Verified against `internal/store/checks_results.go` and `internal/store/insights.go`.
- `NormalizeSeverity` (exported in **Task 1**, `internal/fleetview/issues.go`) maps checks `critical|warning|info` and insights `high|medium|low` → `crit|warn|info`. Used by both the assembler and `recentInsightsVM` (no rename step, no `NormalizeSeverityExported` shim).
- New `store.LatencyBucket{BucketStart,MeanMs}` + `LatencyBucketsForServers` added to the `Stats` interface; `TopActivityBucketsByState` promoted to the `Stats` interface for `fleetview.ConnStates`. `polylineArea` consumes `[]float64` from `qpsValues`/`latValues`; returns SVG-attribute strings (not CSS) — no templ sanitization risk.
- `web.OverviewVM` additions (`Meta,HealthLine,HealthMod,Issues,QPSLine,QPSArea,LatLine,QPSVal,LatVal,ConnLabel,ConnStates,RecentInsights`) are all consumed by `OverviewPage`/`ClusterCharts`/`ConnBar`/`RecentInsights`. `toOverviewVM` final signature is `(d *fleetview.ClusterDetail, issues web.ScopedIssuesVM, nodeHealth map[string]string)`; **all three callers** pass it consistently: `handleClusterOverview` (real issues + nodeHealth), `handleClusterTab` (empty issues + `nil` nodeHealth), and `fetchClusterVM` in `cluster_views.go` (empty issues + `nil`). Latency/conn/recent are filled after mapping by the shared `enrichClusterBody`; the page uses `ClusterTabsWrap(vm,"overview")` and the partial returns `ClusterTabsInner(vm, tab)` (same VM shape).
- `web.NodeVM` / `web.PoolerVM` fields all consumed by `NodeOverviewPage` / `PoolerOverviewPage` / `PoolerConfigPage`. `int64ToStr`/`intToStr`/`floatToStr` live in `web/format.go`.
- Routes registered in `internal/api/server.go` match the handler method names and the URL scheme in the Integration Contract.
