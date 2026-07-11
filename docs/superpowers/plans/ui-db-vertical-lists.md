# Database Vertical Lists (Clusters / Nodes / Databases + Blind Spots) Implementation Plan

> For agentic workers: execute this plan with **superpowers:subagent-driven-development** — one subagent per Task, each Task is self-contained (failing test → run → implement → run → commit). Do the steps in order; do not skip the "run the failing test" step.

**Bead:** ly-ae6.5 (P2, depends ly-ae6.3). Parent epic ly-ae6 (UI design parity).

**Goal:** Build the three Database-vertical list screens — **Clusters**, **Nodes** (with dimmed `◌ BLIND SPOT` standbys, provider chips, data-source lines, per-node versions, conns/max_connections bars, DB→NODE→CLUSTER rollups), and **Databases** (cluster-qualified identity, per-database metrics) — as design-token templ/HTMX screens driven by real fleetview view-models.

**Architecture:** Each screen follows the repo's `XxxPage(vm)` (full page in `@Layout`) + `XxxBody(vm)` (HTMX-swappable fragment) pattern; handlers in `internal/api` assemble view-models from `internal/fleetview` roll-ups over the two stores (`store.Config` topology + `store.Stats` metrics). All rendering uses the ly-ae6.1 CSS custom-property tokens via a new `web/static/css/verticals.css` class layer plus an inlined engine-icon SVG sprite — **no** legacy classes, no inline `var(--x)` emitted from Go. Metrics the backend does not yet collect (host CPU/MEM/DISK/IO, provider identity, blind-spot detection, per-database size/cache/tables) are first-class view-model fields rendered as `—` today, each tied to an explicit backend dependency.

**Tech Stack:** Go 1.22+ (`net/http.ServeMux` method+wildcard routing), `templ` v0.3.1020 (`make templ` regenerates `_templ.go`, committed, CI-checked), HTMX (self-hosted `/static/js/htmx.min.js`), CSS custom-property tokens (`web/static/css/tokens.css`). Tests: `go test` with `testcontainers` real Postgres for handler/fleetview integration, plus in-memory templ-render unit tests (`web` package).

## Global Constraints

Copied verbatim — these are project-wide rules every Task must honor:

- **Privacy T1/T2.** Only T1 (normalized, literal-free) data renders on these screens. All three screens are T1-only — there is no T2 reveal, no query sample, no literal path here. Never introduce a raw-literal field into a T1 path. Every store read below already filters `data_tier = 1`.
- **No external hosts.** Never add a CDN/font/script/style host. All assets self-hosted under `web/static/` and referenced as `/static/...`. There is a contract test `TestLayout_NoExternalHosts` (web/layout_test.go) that must stay green.
- **Tokens, not legacy.** New screens are built with design tokens (`var(--x)` from `tokens.css`) via the new `verticals.css` class layer. Do **not** use `legacy.css` component classes (`.db-card`, `.filters`, `.badge`, `.cards`) on these screens.
- **templ regen.** Any `.templ` edit requires `make templ` to regenerate the committed `_templ.go`; CI checks they are in sync. Commit the generated files.
- **testcontainers.** Integration tests hit real Postgres via testcontainers (`internal/testpg`, `tcpostgres`), never a DB mock. Unit-test view-model/handler logic with `httptest` + rendered-HTML assertions (see `web/layout_test.go`, `internal/api/databases_test.go`).

**Dependency on the scope shell (ly-ae6.2 + ly-ae6.3).** These screens live inside the scoped app shell that ly-ae6.2 (top bar + scope model + searchable SCOPE picker) and ly-ae6.3 (scope-driven sidebar nav) build separately. Those are **not** built here. This plan ships the three screens rendered inside the existing `@Layout` and defines the integration contract the shell consumes (Task 5): the route scheme, the `?sort=`/`?q=`/`?page=` params, the DATABASE-section nav entries, and the per-row `⌖` scope-set `href` + `data-scope` encoding. When ly-ae6.3 lands, the `XxxPage` wrappers get re-parented under the scoped shell; the `XxxBody` fragments, view-models, and handlers are unchanged.

---

### Task 1: Shared foundations — engine sprite, verticals CSS, severity/health roll-up

Produces the reusable pieces every screen needs: the inline engine-icon SVG sprite, the `verticals.css` token class layer, the `fleetview` open-check severity roll-up (`CritOpen/WarnOpen/InfoOpen` + per-cluster `Version`), and the `web` presentation helpers (`HealthLine`, `SevRank`, `nextSort`).

**Files:**
- create `web/engine_sprite.templ` — `templ EngineSprite()`
- create `web/static/css/verticals.css` — token class layer for all three screens
- create `web/health.go` — `HealthLine`, `SevRank`, `nextSort` (package `web`)
- create `web/health_test.go` — unit tests for the helpers
- modify `web/layout.templ` — add the `verticals.css` stylesheet `<link>`
- modify `web/layout_test.go` — extend `TestLayout_SelfHostedAssets` want-list
- modify `internal/fleetview/summary.go` — add `CritOpen/WarnOpen/InfoOpen/Version` to `ClusterSummary`; add `rollupOpenChecks`, `settingsForServer`, `formatServerVersion` helpers; populate in `ListClusterSummaries`
- modify `internal/fleetview/summary_test.go` (external `package fleetview_test`) — assert the new roll-up fields
- create `internal/fleetview/version_internal_test.go` (internal `package fleetview`) — pure-unit `formatServerVersion` table test (no DB)

**Interfaces:**

Produces (package `fleetview`, extending the existing struct):
```go
type ClusterSummary struct {
	Cluster       store.Cluster
	InstanceCount int
	StreamCount   int
	Calls         int64
	AvgLatencyMs  float64
	QPSBuckets    []store.QPSBucket
	ActiveConns   int64
	TopWait       string
	InsightCount  int
	CritOpen      int    // NEW: firing, non-muted latest checks with severity=critical, rolled up across the cluster's servers
	WarnOpen      int    // NEW: severity=warning
	InfoOpen      int    // NEW: severity=info
	Version       string // NEW: display "major.minor" (e.g. "16.3") derived from pg_settings server_version_num of the cluster's first server stream; "" if unknown
}

// rollupOpenChecks tallies firing, non-muted latest check results by severity across serverIDs.
func rollupOpenChecks(ctx context.Context, stats store.Stats, serverIDs []string, since, until time.Time) (crit, warn, info int, err error)

// settingsForServer reads LatestSettings for serverID and extracts the display
// version (from server_version_num) + max_connections.
func settingsForServer(ctx context.Context, stats store.Stats, serverID string, asOf time.Time) (version string, maxConns int64, err error)

// formatServerVersion turns a pg_settings server_version_num integer into "major.minor".
func formatServerVersion(raw string) string
```

Consumes (existing, verified): `store.Stats.LatestChecksResults(ctx, serverID, since, until) ([]store.ChecksResultRow, error)` where `ChecksResultRow{Severity string /* "critical"|"warning"|"info" */, Status string /* "firing"|"ok" */, Muted bool}`; `store.Stats.LatestSettings(ctx, serverID, asOf) ([]store.SettingRow, error)` where `SettingRow{Name, Value string}`.

**Version-chip data source — LOAD-BEARING (fixes the review's HIGH bug).** The collector allowlist (`internal/collector/settings_reader.go`, `settingsAllowlist`) ships **`server_version_num`** (an integer GUC, e.g. `160003`) and **`max_connections`** — it does **NOT** ship the free-form `server_version` string. Reading `server_version` would leave `Version` empty in production and silently omit every version chip (the COMPARISON `Database › Clusters` "version chip" gap). Therefore `settingsForServer` reads `server_version_num` and `formatServerVersion` renders the display `major.minor` (`160003 → "16.3"`). This keeps the privacy allowlist boundary intact (no new GUC added — no human allowlist review needed) and, critically, the fleetview roll-up test (Step 9) seeds the **real** collector field `server_version_num`, so the test exercises the production data path rather than a synthetic `server_version` row. Lynceus's supported baseline is PG 12+ (`internal/caps/probes.go` enforces `server_version_num >= 120000`), where the encoding is `major*10000 + minor`, so integer division/modulo by 10000 is exact.

Produces (package `web`):
```go
// HealthLine renders the cluster/database health rollup label + its CSS color class from
// open-check severity counts. Mirrors the prototype's healthLine strings.
func HealthLine(crit, warn, info int) (text, cssClass string)   // cssClass ∈ {"hl-crit","hl-warn","hl-info","hl-ok"}

// SevRank orders rows for the HEALTH sort (crit first): 2=crit, 1=warn, 0=clean/info.
func SevRank(crit, warn, info int) int

// nextSort returns the other sort key ("health" <-> "name").
func nextSort(cur string) string
```

**Steps:**

- [ ] **Step 1:** Write the failing helper test. Create `web/health_test.go`:
```go
package web

import "testing"

func TestHealthLine(t *testing.T) {
	cases := []struct {
		crit, warn, info    int
		wantText, wantClass string
	}{
		{1, 4, 0, "[DEGRADED] 1 CRIT · 4 WARN", "hl-crit"},
		{0, 2, 1, "[WARNING] 2 WARN", "hl-warn"},
		{0, 0, 3, "[HEALTHY] 3 INFO", "hl-info"},
		{0, 0, 0, "[HEALTHY] 0 OPEN", "hl-ok"},
	}
	for _, c := range cases {
		gotText, gotClass := HealthLine(c.crit, c.warn, c.info)
		if gotText != c.wantText || gotClass != c.wantClass {
			t.Errorf("HealthLine(%d,%d,%d) = %q/%q, want %q/%q",
				c.crit, c.warn, c.info, gotText, gotClass, c.wantText, c.wantClass)
		}
	}
}

func TestSevRankAndNextSort(t *testing.T) {
	if SevRank(1, 0, 0) != 2 || SevRank(0, 1, 9) != 1 || SevRank(0, 0, 5) != 0 {
		t.Fatal("SevRank ordering wrong")
	}
	if nextSort("health") != "name" || nextSort("name") != "health" || nextSort("") != "name" {
		t.Fatal("nextSort toggle wrong")
	}
}
```

- [ ] **Step 2:** Run it, expect FAIL (undefined `HealthLine`/`SevRank`/`nextSort`):
```
go test ./web/ -run 'TestHealthLine|TestSevRankAndNextSort'
```
Expected: build error `undefined: HealthLine` (compile failure counts as the failing state).

- [ ] **Step 3:** Implement `web/health.go`:
```go
package web

import "fmt"

// HealthLine renders the design's cluster/database health rollup label and its
// CSS color class (defined in verticals.css) from open-check severity counts.
func HealthLine(crit, warn, info int) (text, cssClass string) {
	switch {
	case crit > 0:
		return fmt.Sprintf("[DEGRADED] %d CRIT · %d WARN", crit, warn), "hl-crit"
	case warn > 0:
		return fmt.Sprintf("[WARNING] %d WARN", warn), "hl-warn"
	case info > 0:
		return fmt.Sprintf("[HEALTHY] %d INFO", info), "hl-info"
	default:
		return "[HEALTHY] 0 OPEN", "hl-ok"
	}
}

// SevRank orders rows for the HEALTH sort (crit-first).
func SevRank(crit, warn, info int) int {
	switch {
	case crit > 0:
		return 2
	case warn > 0:
		return 1
	default:
		return 0
	}
}

// nextSort toggles between the two sort keys. Anything not "name" is treated
// as "health", so the default flips to "name".
func nextSort(cur string) string {
	if cur == "name" {
		return "health"
	}
	return "name"
}
```

- [ ] **Step 4:** Run it, expect PASS:
```
go test ./web/ -run 'TestHealthLine|TestSevRankAndNextSort'
```
Expected: `ok  github.com/dobbo-ca/lynceus/web`.

- [ ] **Step 5:** Create the engine sprite component `web/engine_sprite.templ` (exact glyphs from `docs/design/Lynceus.dc.html:52-66`):
```go
package web

// EngineSprite renders the hidden inline SVG symbol set referenced by
// <use href="#eng-pg|#eng-os|#eng-vk"> on the engine lists. Original glyphs in
// currentColor so they theme automatically (no official logos — PRODUCT_INTENT §9).
templ EngineSprite() {
	<svg width="0" height="0" style="position:absolute" aria-hidden="true">
		<symbol id="eng-pg" viewBox="0 0 24 24">
			<path d="M5 6 v12 a7 3 0 0 0 14 0 V6" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"></path>
			<ellipse cx="12" cy="6" rx="7" ry="3" fill="none" stroke="currentColor" stroke-width="2"></ellipse>
			<path d="M5 12 a7 3 0 0 0 14 0" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"></path>
		</symbol>
		<symbol id="eng-os" viewBox="0 0 24 24">
			<circle cx="10.5" cy="10.5" r="6" fill="none" stroke="currentColor" stroke-width="2"></circle>
			<line x1="15" y1="15" x2="20" y2="20" stroke="currentColor" stroke-width="2" stroke-linecap="round"></line>
		</symbol>
		<symbol id="eng-vk" viewBox="0 0 24 24">
			<circle cx="8" cy="12" r="4" fill="none" stroke="currentColor" stroke-width="2"></circle>
			<line x1="12" y1="12" x2="20" y2="12" stroke="currentColor" stroke-width="2" stroke-linecap="round"></line>
			<line x1="17" y1="12" x2="17" y2="15.5" stroke="currentColor" stroke-width="2" stroke-linecap="round"></line>
			<line x1="20" y1="12" x2="20" y2="14" stroke="currentColor" stroke-width="2" stroke-linecap="round"></line>
		</symbol>
	</svg>
}
```

- [ ] **Step 6:** Create `web/static/css/verticals.css` (token class layer — every color is `var(--x)` from tokens.css; the only inline style Go emits is the conns-bar `width:NN%`):
```css
/* Database-vertical list screens (Clusters / Nodes / Databases).
   Structural layout + token color classes. Colors resolve to tokens.css vars. */

.vlist { padding:18px 22px 32px; display:flex; flex-direction:column; gap:14px; max-width:1400px; }
.vlist-head { display:flex; align-items:center; gap:12px; flex-wrap:wrap; }
.vlist-title { font-size:17px; font-weight:600; color:var(--text); }
.badge-live { font-family:var(--font-mono); font-size:10px; color:var(--acc); border:var(--border) solid var(--acc); padding:0 5px; border-radius:var(--radius-badge); }
.vlist-strap { font-family:var(--font-mono); font-size:10.5px; color:var(--faint); letter-spacing:.08em; }
.vlist-spacer { flex:1; }
.vlist-btn { font-family:var(--font-mono); font-size:10.5px; color:var(--dim); border:var(--border) solid var(--line); padding:4px 9px; border-radius:var(--radius); cursor:pointer; user-select:none; background:none; text-decoration:none; white-space:nowrap; }
.vlist-btn:hover { color:var(--text); }
.vlist-btn--acc { color:var(--acc2); border-color:var(--acc); }
.vlist-btn--acc:hover { background:var(--accdim); }
.vlist-search { background:var(--surface); border:var(--border) solid var(--line); border-radius:var(--radius); color:var(--text); font-family:var(--font-mono); font-size:11px; padding:5px 10px; width:250px; }

.vpanel { border:var(--border) solid var(--line); border-radius:var(--radius); background:var(--surface); }

.eng-mark { width:20px; height:20px; border:1.5px solid var(--acc2); color:var(--acc2); display:flex; align-items:center; justify-content:center; border-radius:var(--radius); flex-shrink:0; }

.scope-btn { width:24px; height:24px; border:var(--border) solid var(--line); border-radius:var(--radius); display:flex; align-items:center; justify-content:center; color:var(--acc2); font-size:13px; cursor:pointer; user-select:none; flex-shrink:0; text-decoration:none; }
.scope-btn:hover { border-color:var(--acc); background:var(--accdim); }

.hl-crit { color:var(--critT); }
.hl-warn { color:var(--warnT); }
.hl-info { color:var(--ok); }
.hl-ok   { color:var(--ok); }

/* ---- clusters flat list ---- */
.cl-row { padding:10px 14px; border-bottom:var(--border) solid var(--line2); display:flex; align-items:center; gap:12px; font-family:var(--font-mono); }
.cl-row:last-child { border-bottom:none; }
.cl-name { font-size:13px; font-weight:600; min-width:150px; color:var(--text); }
.cl-ver { font-size:10px; color:var(--acc2); }
.cl-meta { font-size:10px; color:var(--faint); letter-spacing:.06em; }
.cl-qps { font-size:10.5px; color:var(--dim); }
.cl-health { font-size:10px; min-width:170px; text-align:right; }

/* ---- group header (nodes + databases) ---- */
.grp-head { padding:10px 14px; border-bottom:var(--border) solid var(--line); display:flex; align-items:center; gap:12px; font-family:var(--font-mono); flex-wrap:wrap; }
.grp-name { font-size:13.5px; font-weight:600; color:var(--text); }
.grp-ver { font-size:10px; color:var(--acc2); }
.grp-provider { font-size:9.5px; border:var(--border) solid var(--line); padding:1px 6px; border-radius:var(--radius-badge); color:var(--infoT); letter-spacing:.06em; cursor:help; }
.grp-rollup { font-size:9.5px; letter-spacing:.04em; }

/* ---- nodes table ---- */
.tbl-scroll { overflow-x:auto; }
.nodes-grid { min-width:1020px; }
.nodes-hd, .nodes-row { display:grid; grid-template-columns:74px minmax(240px,1fr) 64px 64px 64px 76px 210px 110px 34px; gap:12px; padding:7px 14px; border-bottom:var(--border) solid var(--line2); font-family:var(--font-mono); }
.nodes-hd { font-size:9.5px; letter-spacing:.1em; color:var(--faint); }
.nodes-row { padding:8px 14px; align-items:center; font-variant-numeric:tabular-nums; }
.nodes-row:last-child { border-bottom:none; }
.num-r { text-align:right; }
.role-chip { font-family:var(--font-mono); font-size:9px; border:var(--border) solid var(--line); padding:1px 4px; border-radius:var(--radius-badge); text-align:center; letter-spacing:.04em; }
.role-primary { color:var(--acc2); }
.role-replica, .role-pooler, .role-unknown { color:var(--infoT); }
.role-standby { color:var(--faint); }
.node-id { display:flex; flex-direction:column; gap:1px; min-width:0; }
.node-idline { display:flex; gap:8px; align-items:baseline; }
.node-name { font-family:var(--font-mono); font-size:11.5px; font-weight:600; color:var(--text); }
.node-name--blind { color:var(--dim); }
.node-nodever { font-family:var(--font-mono); font-size:9.5px; color:var(--dim); }
.node-src { font-family:var(--font-mono); font-size:9.5px; color:var(--faint); overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
.node-metric { font-family:var(--font-mono); font-size:11.5px; color:var(--mut); }
.conns-wrap { display:flex; align-items:center; gap:8px; }
.conns-bar { flex:1; height:8px; background:var(--raised); border-radius:var(--radius-badge); }
.conns-fill { height:8px; background:var(--acc); }
.conns-num { font-family:var(--font-mono); font-size:10.5px; color:var(--mut); white-space:nowrap; }
.node-health { font-family:var(--font-mono); font-size:10px; text-align:right; }

/* ---- databases table ---- */
.dbs-grid { min-width:960px; }
.dbs-hd, .dbs-row { display:grid; grid-template-columns:minmax(220px,1fr) 90px 90px 80px 80px 80px 34px; gap:12px; padding:7px 14px; border-bottom:var(--border) solid var(--line2); font-family:var(--font-mono); }
.dbs-hd { font-size:9.5px; letter-spacing:.1em; color:var(--faint); }
.dbs-row { padding:8px 14px; align-items:center; font-variant-numeric:tabular-nums; }
.dbs-row:last-child { border-bottom:none; }
.db-id { display:flex; flex-direction:column; gap:1px; min-width:0; }
.db-name { font-family:var(--font-mono); font-size:12px; font-weight:600; color:var(--text); }
.db-qual { font-family:var(--font-mono); font-size:9.5px; color:var(--faint); }
.db-metric { font-family:var(--font-mono); font-size:11.5px; color:var(--mut); }
.db-qps { color:var(--text); }
.info-strip { border:var(--border) solid var(--info); border-radius:var(--radius); background:var(--infobg); padding:8px 12px; font-family:var(--font-mono); font-size:10.5px; color:var(--infoT); }
.info-strip .prose { font-family:var(--font-ui); font-size:12px; color:var(--mut); }

/* ---- pager + empty ---- */
.vpager { display:flex; align-items:center; gap:8px; font-family:var(--font-mono); font-size:10px; color:var(--dim); letter-spacing:.04em; }
.vpager a { cursor:pointer; border:var(--border) solid var(--line); padding:2px 8px; border-radius:var(--radius); color:var(--dim); user-select:none; text-decoration:none; }
.vpager a:hover { border-color:var(--dim); }
.vpager a.is-disabled { color:var(--faint); pointer-events:none; }
.vpager .hint { color:var(--faint); }
.vempty { border:var(--border) solid var(--line); border-radius:var(--radius); background:var(--surface); padding:22px; font-family:var(--font-mono); font-size:10.5px; color:var(--faint); text-align:center; letter-spacing:.06em; }
```

- [ ] **Step 7:** Wire the stylesheet into the shared layout. In `web/layout.templ`, add after the `legacy.css` link (line 27):
```go
			<link rel="stylesheet" href="/static/css/verticals.css"/>
```
Then extend `web/layout_test.go` `TestLayout_SelfHostedAssets` want-list to include `` `href="/static/css/verticals.css"` ``.

- [ ] **Step 8:** Extend `fleetview.ClusterSummary` and populate the roll-up. In `internal/fleetview/summary.go`, add the four fields to the struct (as in Interfaces above), add the two helpers, and populate inside `ListClusterSummaries` after the `InsightCount` block (before `out = append`):
```go
		if sum.CritOpen, sum.WarnOpen, sum.InfoOpen, err = rollupOpenChecks(ctx, stats, serverIDs, since, until); err != nil {
			return nil, err
		}
		if sum.Version, _, err = settingsForServer(ctx, stats, serverIDs[0], until); err != nil {
			return nil, err
		}
```
Add the helpers to the same file. Task 1's new code (`rollupOpenChecks`, `settingsForServer`, `formatServerVersion`) needs only the `"strconv"` import added to `internal/fleetview/summary.go` — do **not** add `"strings"` yet (it would be unused-and-not-imported... i.e. `imported and not used`); `"strings"` is added in Task 3 when `ListNodeGroups` (which calls `strings.ToUpper`) lands:
```go
// rollupOpenChecks tallies firing, non-muted latest check results by severity
// across the server set. Severity strings are the stored check vocab
// ("critical"/"warning"/"info"); Status "firing" (not "ok") and !Muted == open.
func rollupOpenChecks(ctx context.Context, stats store.Stats, serverIDs []string, since, until time.Time) (crit, warn, info int, err error) {
	for _, sid := range serverIDs {
		rows, err := stats.LatestChecksResults(ctx, sid, since, until)
		if err != nil {
			return 0, 0, 0, err
		}
		for i := range rows {
			r := &rows[i]
			if r.Muted || r.Status != "firing" {
				continue
			}
			switch r.Severity {
			case "critical":
				crit++
			case "warning":
				warn++
			default:
				info++
			}
		}
	}
	return crit, warn, info, nil
}

// settingsForServer extracts the display version + max_connections from the
// server's latest curated pg_settings. Both source GUCs (server_version_num,
// max_connections) are in the collector allowlist; either may be absent
// (returns "" / 0) on a stream that has not reported settings yet.
//
// NOTE: version is derived from server_version_num (the integer GUC the
// collector actually ships), NOT the free-form server_version string — which is
// deliberately NOT allowlisted. See the "Version-chip data source" note in
// Interfaces above.
func settingsForServer(ctx context.Context, stats store.Stats, serverID string, asOf time.Time) (version string, maxConns int64, err error) {
	rows, err := stats.LatestSettings(ctx, serverID, asOf)
	if err != nil {
		return "", 0, err
	}
	for i := range rows {
		switch rows[i].Name {
		case "server_version_num":
			version = formatServerVersion(rows[i].Value)
		case "max_connections":
			if n, perr := strconv.ParseInt(rows[i].Value, 10, 64); perr == nil {
				maxConns = n
			}
		}
	}
	return version, maxConns, nil
}

// formatServerVersion turns a pg_settings server_version_num integer (e.g.
// "160003") into the display "major.minor" ("16.3"). Lynceus's supported
// baseline is PG 12+, where the encoding is major*10000 + minor, so integer
// division/modulo by 10000 is exact. Returns "" for a blank/unparseable value.
func formatServerVersion(raw string) string {
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return ""
	}
	return strconv.Itoa(n/10000) + "." + strconv.Itoa(n%10000)
}
```

- [ ] **Step 9a:** Add a pure-unit test for the version formatter. `formatServerVersion` is unexported in package `fleetview`, and `summary_test.go` is the *external* `package fleetview_test` (it calls `fleetview.ListClusterSummaries`), so it cannot reach the unexported helper. Create a new **internal**-package test file `internal/fleetview/version_internal_test.go` with `package fleetview` (Go allows both `fleetview` and `fleetview_test` test files in one dir); it needs no DB, so it runs even without docker:
```go
package fleetview

import "testing"

func TestFormatServerVersion(t *testing.T) {
	cases := map[string]string{"160003": "16.3", "150007": "15.7", "120000": "12.0", "": "", "garbage": ""}
	for in, want := range cases {
		if got := formatServerVersion(in); got != want {
			t.Errorf("formatServerVersion(%q) = %q, want %q", in, got, want)
		}
	}
}
```

- [ ] **Step 9b:** Add the fleetview roll-up integration test to `internal/fleetview/summary_test.go` (external `package fleetview_test`; reuse the `newStores(t)` helper already defined there — same pattern as `TestListClusterSummaries_rollsUpAcrossStreams`). Seed one cluster/instance/server (`srv-test`), write two firing checks (one critical, one warning) via `stats.WriteChecksResults`, and — critically — the **real collector field `server_version_num`** (plus `max_connections`) via `stats.WriteSettings`, then assert `ListClusterSummaries` returns `CritOpen==1, WarnOpen==1, Version=="16.3"`. Seeding `server_version_num` (not `server_version`) is what makes this test exercise the production data path — the collector ships `server_version_num`, so a green test here means the chip renders in production. Assertion core:
```go
	sums, err := ListClusterSummaries(ctx, cfg, stats, now.Add(-time.Hour), now.Add(time.Hour))
	if err != nil { t.Fatalf("summaries: %v", err) }
	got := sums[0]
	if got.CritOpen != 1 || got.WarnOpen != 1 {
		t.Fatalf("severity rollup = crit %d warn %d, want 1/1", got.CritOpen, got.WarnOpen)
	}
	if got.Version != "16.3" {
		t.Fatalf("version = %q, want 16.3 (derived from server_version_num=160003)", got.Version)
	}
```
Seed the checks with `store.ChecksResultRow{ServerID:"srv-test", EvaluatedAt:now, CheckID:"settings.fsync", Category:"settings", Severity:"critical", Status:"firing", Object:"server", DataTier:1}` and a second `warning`/`firing` row; seed settings with `store.SettingRow{ServerID:"srv-test", CollectedAt:now, Name:"server_version_num", Value:"160003", DataTier:1}` and a second row `Name:"max_connections", Value:"200", DataTier:1`. (`SettingRow.DataTier` is `int16`; the untyped `1` constant assigns cleanly.)

- [ ] **Step 10:** Regenerate templ and run the affected tests:
```
make templ
go build ./...
go test ./web/ ./internal/fleetview/
```
Expected: PASS (fleetview integration test skips if docker unavailable — that is acceptable per repo convention).

- [ ] **Step 11:** Commit:
```
git add web/engine_sprite.templ web/engine_sprite_templ.go web/static/css/verticals.css web/health.go web/health_test.go web/layout.templ web/layout_templ.go web/layout_test.go internal/fleetview/summary.go internal/fleetview/summary_test.go internal/fleetview/version_internal_test.go
git commit -m "feat(ui): shared foundations for db vertical lists — engine sprite, verticals.css, severity rollup (ly-ae6.5)"
```

---

### Task 2: Clusters list (retrofit `/databases`)

Replaces the current cards/list `DatabasesView` with the design's flat sortable **Clusters** list: engine-icon chip, name, `v<version>` chip, faint meta, QPS, colored `[HEALTH]` rollup line, and a per-row `⌖` scope button (rows are **not** clickable). Adds the `Clusters` LIVE header + strap, `SORT: HEALTH/NAME` toggle, and `+ ADD CLUSTER` (onboarding link). Removes the sparkline SVG and the Cards/List toggle.

**Files:**
- delete `web/databases.templ` + `web/databases_templ.go`; create `web/clusters.templ` (+ generated `web/clusters_templ.go`)
- delete `internal/api/databases.go`; create `internal/api/clusters.go` (Clusters handlers) and `internal/api/sparkline.go` (move `sparklinePoints`, still used by `internal/api/overview.go`)
- delete `internal/api/databases_test.go`; create `internal/api/clusters_test.go`
- create `internal/api/vertical_helpers_test.go` (shared test scaffolding: `newDBPool`, `newVerticalFleet`, `getBody`). **`newDBPool` MUST be preserved here** — the deleted `databases_test.go` currently owns it, and existing `internal/api/cluster_views_test.go` + `internal/api/overview_test.go` call it; migrate it verbatim or those tests stop compiling. `newVerticalFleet`/`getBody` are new and reused by Tasks 3/4/5.
- create `web/clusters_test.go` (templ-render unit test)
- modify `internal/api/server.go` (swap the two `/databases` routes to the Clusters handlers — **required in this Task** to keep the tree building; see Step 5)

**Interfaces:**

Produces (package `web`):
```go
type ClusterListRow struct {
	ClusterID   string
	Name        string
	EngineIcon  string // "eng-pg"
	EngineName  string // "POSTGRES" (title tooltip on the chip)
	Version     string // "16.3"; "" -> version chip omitted
	Meta        string // "3 INSTANCES · 4 STREAMS"
	QPS         string // "1,284" (thousands-grouped)
	HealthText  string // "[DEGRADED] 1 CRIT · 4 WARN"
	HealthClass string // "hl-crit" | "hl-warn" | "hl-info" | "hl-ok"
	SevRank     int    // for HEALTH sort
	ScopeHref   string // "/databases/<id>" — scope-set landing (cluster Overview)
	ScopeTarget string // "cluster:<id>" — data-scope attr the shell reads
}

type ClustersView struct {
	Rows          []ClusterListRow
	Sort          string // "health" | "name"
	SortLabel     string // "HEALTH" | "NAME"
	SortHref      string // "/partial/databases?sort=<next>"
	SortPageHref  string // "/databases?sort=<next>" (no-JS fallback)
	AddHref       string // "/onboarding?vertical=database"
}

templ ClustersPage(v ClustersView)
templ ClustersBody(v ClustersView)   // root: <div id="clusters-screen" class="vlist"> ... </div>
```

Consumes: `fleetview.ListClusterSummaries` (now carrying `CritOpen/WarnOpen/InfoOpen/Version`); `web.HealthLine`, `web.SevRank`.

**Steps:**

- [ ] **Step 1:** Write the failing templ-render unit test `web/clusters_test.go`:
```go
package web

import (
	"context"
	"strings"
	"testing"
)

func renderClusters(t *testing.T, v ClustersView) string {
	t.Helper()
	var sb strings.Builder
	if err := ClustersBody(v).Render(context.Background(), &sb); err != nil {
		t.Fatalf("render: %v", err)
	}
	return sb.String()
}

func TestClustersBody_RowAnatomy(t *testing.T) {
	html := renderClusters(t, ClustersView{Rows: []ClusterListRow{{
		ClusterID: "c1", Name: "orders-prod", EngineIcon: "eng-pg", EngineName: "POSTGRES",
		Version: "16.3", Meta: "3 INSTANCES · 4 STREAMS", QPS: "1,284",
		HealthText: "[DEGRADED] 1 CRIT · 4 WARN", HealthClass: "hl-crit", SevRank: 2,
		ScopeHref: "/databases/c1", ScopeTarget: "cluster:c1",
	}}})
	for _, want := range []string{
		`id="clusters-screen"`,
		`href="#eng-pg"`,            // engine sprite chip
		`class="cl-name"`, `orders-prod`,
		`v16.3`,                     // version chip
		`3 INSTANCES · 4 STREAMS`,   // faint meta
		`1,284 QPS`,
		`[DEGRADED] 1 CRIT · 4 WARN`,
		`hl-crit`,
		`class="scope-btn"`, `href="/databases/c1"`, `data-scope="cluster:c1"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("ClustersBody missing %q", want)
		}
	}
	// Rows must NOT be whole-row links, and no sparkline/cards remain.
	if strings.Contains(html, `<polyline`) || strings.Contains(html, `class="db-card"`) {
		t.Error("ClustersBody still renders a sparkline or legacy card")
	}
}

func TestClustersBody_VersionOmittedWhenBlank(t *testing.T) {
	html := renderClusters(t, ClustersView{Rows: []ClusterListRow{{
		ClusterID: "c1", Name: "x", EngineIcon: "eng-pg", HealthClass: "hl-ok",
	}}})
	if strings.Contains(html, `class="cl-ver"`) {
		t.Error("version chip must be omitted when Version is empty")
	}
}
```

- [ ] **Step 2:** Run it, expect FAIL (undefined `ClustersView`/`ClustersBody`):
```
go test ./web/ -run TestClustersBody
```
Expected: compile failure `undefined: ClustersBody`.

- [ ] **Step 3:** Delete the old screen and create `web/clusters.templ`:
```go
package web

// ClusterListRow / ClustersView: see plan Task 2 Interfaces.
type ClusterListRow struct {
	ClusterID   string
	Name        string
	EngineIcon  string
	EngineName  string
	Version     string
	Meta        string
	QPS         string
	HealthText  string
	HealthClass string
	SevRank     int
	ScopeHref   string
	ScopeTarget string
}

type ClustersView struct {
	Rows         []ClusterListRow
	Sort         string
	SortLabel    string
	SortHref     string
	SortPageHref string
	AddHref      string
}

templ ClustersPage(v ClustersView) {
	@Layout("Lynceus — Clusters", "database vertical · clusters") {
		@EngineSprite()
		@ClustersBody(v)
	}
}

templ ClustersBody(v ClustersView) {
	<div id="clusters-screen" class="vlist">
		<div class="vlist-head">
			<span class="vlist-title">Clusters</span>
			<span class="badge-live">LIVE</span>
			<span class="vlist-strap">A CLUSTER GROUPS A PRIMARY WITH ITS REPLICAS AND POOLERS</span>
			<span class="vlist-spacer"></span>
			<a
				class="vlist-btn"
				href={ templ.SafeURL(v.SortPageHref) }
				hx-get={ v.SortHref }
				hx-target="#clusters-screen"
				hx-swap="outerHTML"
			>SORT: { v.SortLabel } ⇅</a>
			<a class="vlist-btn vlist-btn--acc" href={ templ.SafeURL(v.AddHref) } title="Deploy a collector for a new cluster">+ ADD CLUSTER</a>
		</div>
		<div class="vpanel">
			if len(v.Rows) == 0 {
				<div class="vempty">NO CLUSTERS MONITORED YET — RUN A COLLECTOR AND CHECK BACK</div>
			} else {
				for _, c := range v.Rows {
					<div class="cl-row">
						<span class="eng-mark" title={ c.EngineName }>
							<svg width="12" height="12" viewBox="0 0 24 24"><use href={ "#" + c.EngineIcon }></use></svg>
						</span>
						<span class="cl-name">{ c.Name }</span>
						if c.Version != "" {
							<span class="cl-ver">{ "v" + c.Version }</span>
						}
						<span class="cl-meta">{ c.Meta }</span>
						<span class="vlist-spacer"></span>
						<span class="cl-qps">{ c.QPS } QPS</span>
						<span class={ "cl-health " + c.HealthClass }>{ c.HealthText }</span>
						<a class="scope-btn" href={ templ.SafeURL(c.ScopeHref) } data-scope={ c.ScopeTarget } title="Set scope to this cluster">⌖</a>
					</div>
				}
			}
		</div>
	</div>
}
```

- [ ] **Step 4:** Move `sparklinePoints` to `internal/api/sparkline.go` (verbatim from the old `databases.go`, plus its imports `fmt`, `math`, `strings`, and the `store` import for `store.QPSBucket`), then create `internal/api/clusters.go`:
```go
package api

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dobbo-ca/lynceus/internal/fleetview"
	"github.com/dobbo-ca/lynceus/web"
)

func (s *Server) handleClusters(w http.ResponseWriter, r *http.Request) {
	v := s.fetchClusters(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.ClustersPage(v).Render(r.Context(), w)
}

func (s *Server) handleClustersPartial(w http.ResponseWriter, r *http.Request) {
	v := s.fetchClusters(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.ClustersBody(v).Render(r.Context(), w)
}

func (s *Server) fetchClusters(r *http.Request) web.ClustersView {
	sortKey := r.URL.Query().Get("sort")
	if sortKey != "name" {
		sortKey = "health"
	}
	next := "name"
	if sortKey == "name" {
		next = "health"
	}
	view := web.ClustersView{
		Sort:         sortKey,
		SortLabel:    strings.ToUpper(sortKey),
		SortHref:     "/partial/databases?sort=" + next,
		SortPageHref: "/databases?sort=" + next,
		AddHref:      "/onboarding?vertical=database",
	}

	now := time.Now().UTC()
	sums, err := fleetview.ListClusterSummaries(r.Context(), s.conf, s.stats, now.AddDate(0, 0, -1), now)
	if err != nil {
		return view
	}

	rows := make([]web.ClusterListRow, 0, len(sums))
	for i := range sums {
		sum := &sums[i]
		var qps float64
		if n := len(sum.QPSBuckets); n > 0 {
			qps = float64(sum.QPSBuckets[n-1].Calls) / 3600.0
		}
		text, class := web.HealthLine(sum.CritOpen, sum.WarnOpen, sum.InfoOpen)
		rows = append(rows, web.ClusterListRow{
			ClusterID:   sum.Cluster.ID,
			Name:        sum.Cluster.Name,
			EngineIcon:  "eng-pg",
			EngineName:  "POSTGRES",
			Version:     sum.Version,
			Meta:        clusterMeta(sum.InstanceCount, sum.StreamCount),
			QPS:         groupThousands(int64(qps + 0.5)),
			HealthText:  text,
			HealthClass: class,
			SevRank:     web.SevRank(sum.CritOpen, sum.WarnOpen, sum.InfoOpen),
			ScopeHref:   "/databases/" + sum.Cluster.ID,
			ScopeTarget: "cluster:" + sum.Cluster.ID,
		})
	}
	sortRowsByKey(rows, sortKey)
	view.Rows = rows
	return view
}

// sortRowsByKey orders by SevRank desc (health) or Name asc.
func sortRowsByKey(rows []web.ClusterListRow, key string) {
	sort.SliceStable(rows, func(i, j int) bool {
		if key == "health" && rows[i].SevRank != rows[j].SevRank {
			return rows[i].SevRank > rows[j].SevRank
		}
		return rows[i].Name < rows[j].Name
	})
}

func clusterMeta(instances, streams int) string {
	return strconv.Itoa(instances) + " " + plural(instances, "INSTANCE", "INSTANCES") +
		" · " + strconv.Itoa(streams) + " " + plural(streams, "STREAM", "STREAMS")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// groupThousands renders n with comma thousands separators (e.g. 1284 -> "1,284").
func groupThousands(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	var out strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			out.WriteByte(',')
		}
		out.WriteRune(c)
	}
	if neg {
		return "-" + out.String()
	}
	return out.String()
}
```

- [ ] **Step 5:** Create the shared test scaffolding `internal/api/vertical_helpers_test.go`. `newDBPool`, `newVerticalFleet`, and `getBody` are migrated verbatim/adapted from the deleted `databases_test.go` and are reused by the Task 3/4/5 tests (do not re-declare them there):
```go
package api_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/dobbo-ca/lynceus/internal/api"
	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/dobbo-ca/lynceus/internal/testpg"
)

// newDBPool starts a fresh postgres:16 container and returns a connected pool.
// Skips the test if docker/testcontainers are unavailable.
func newDBPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	c, err := tcpostgres.Run(ctx, "postgres:16",
		tcpostgres.WithDatabase("lynceus_test"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		testpg.ReadyWait(),
	)
	if err != nil {
		t.Skipf("docker/testcontainers unavailable: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(c) })
	url, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// newVerticalFleet migrates the two stores and starts an httptest server with
// the real API handler wired, returning everything the per-screen setups need
// to seed. Seeding after NewServer is fine — handlers read the DB per request.
func newVerticalFleet(t *testing.T) (srv *httptest.Server, cfg store.Config, stats store.Stats, configPool, statsPool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	configPool = newDBPool(t)
	statsPool = newDBPool(t)
	if err := store.ApplyConfigMigrations(ctx, configPool); err != nil {
		t.Fatalf("config migrate: %v", err)
	}
	if err := store.ApplyStatsMigrations(ctx, statsPool); err != nil {
		t.Fatalf("stats migrate: %v", err)
	}
	cfg = store.NewConfig(configPool)
	stats = store.NewStats(statsPool)
	srv = httptest.NewServer(api.NewServer(api.Config{DevAuth: true}, stats, cfg).Handler())
	t.Cleanup(srv.Close)
	return srv, cfg, stats, configPool, statsPool
}

// getBody GETs url and returns the response body as a string.
func getBody(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}
```

- [ ] **Step 6:** Write the handler integration test `internal/api/clusters_test.go` with `setupClusters` and the HEALTH-sort assertion. `setupClusters` seeds two clusters — `aaa-clean` (no open checks) and `zzz-degraded` (one firing critical check) — so that HEALTH sort must reorder them ahead of the alphabetical order:
```go
package api_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dobbo-ca/lynceus/internal/store"
)

// setupClusters seeds two clusters: "aaa-clean" (healthy) and "zzz-degraded"
// (one firing critical check). Alphabetically aaa precedes zzz, so a correct
// HEALTH sort (degraded-first) must invert that order.
func setupClusters(t *testing.T) *httptest.Server {
	t.Helper()
	ctx := context.Background()
	srv, cfg, stats, configPool, _ := newVerticalFleet(t)
	now := time.Now().UTC()

	seed := func(clusterName, serverID string, degraded bool) {
		if _, err := configPool.Exec(ctx, `INSERT INTO servers (id, name) VALUES ($1, $1)`, serverID); err != nil {
			t.Fatalf("seed server %s: %v", serverID, err)
		}
		cl, err := cfg.CreateCluster(ctx, clusterName)
		if err != nil {
			t.Fatalf("CreateCluster: %v", err)
		}
		inst, err := cfg.CreateInstance(ctx, cl.ID, "primary")
		if err != nil {
			t.Fatalf("CreateInstance: %v", err)
		}
		if err := cfg.AssignServerToInstance(ctx, serverID, inst.ID); err != nil {
			t.Fatalf("AssignServerToInstance: %v", err)
		}
		if err := stats.WriteQueryStats(ctx, []store.QueryStat{
			{ServerID: serverID, CollectedAt: now.Add(-time.Hour), Fingerprint: "fp-1",
				NormalizedQuery: "SELECT $1", Calls: 3600, TotalTimeMs: 720},
		}); err != nil {
			t.Fatalf("seed query stats: %v", err)
		}
		if degraded {
			if err := stats.WriteChecksResults(ctx, []store.ChecksResultRow{{
				ServerID: serverID, EvaluatedAt: now, CheckID: "settings.fsync",
				Category: "settings", Severity: "critical", Status: "firing",
				Object: "server", DataTier: 1,
			}}); err != nil {
				t.Fatalf("seed check: %v", err)
			}
		}
	}
	seed("aaa-clean", "srv-clean", false)
	seed("zzz-degraded", "srv-degraded", true)
	return srv
}

func TestClusters_HealthSortAndRowAnatomy(t *testing.T) {
	srv := setupClusters(t)
	html := getBody(t, srv.URL+"/databases?sort=health")
	if !strings.Contains(html, `id="clusters-screen"`) || !strings.Contains(html, "SORT: HEALTH") {
		t.Fatal("clusters screen header missing")
	}
	if !strings.Contains(html, "[DEGRADED]") || !strings.Contains(html, `data-scope="cluster:`) {
		t.Fatal("degraded health line or scope button missing")
	}
	// HEALTH sort must place the degraded cluster before the clean one, inverting
	// the alphabetical (zzz after aaa) order.
	if strings.Index(html, "zzz-degraded") > strings.Index(html, "aaa-clean") {
		t.Fatal("HEALTH sort did not put the degraded cluster first")
	}
}
```

- [ ] **Step 7:** Swap the `/databases` routes to the Clusters handlers — **required now** so the tree builds after `databases.go` is deleted (do not defer to Task 5). In `internal/api/server.go` `routes()`, replace the two existing lines:
```go
	s.mux.HandleFunc("GET /databases", s.handleDatabases)          // was: handleDatabases
	s.mux.HandleFunc("GET /partial/databases", s.handleDatabasesPartial)
```
with:
```go
	s.mux.HandleFunc("GET /databases", s.handleClusters)
	s.mux.HandleFunc("GET /partial/databases", s.handleClustersPartial)
```
Leave `GET /databases/{clusterID}` and its sub-routes untouched (ly-ae6.6 owns cluster detail). Task 5 only *verifies* these routes; the swap happens here.

- [ ] **Step 8:** Run the tests, expect PASS (build must be green — `databases.go`/`databases_test.go` are gone and their route users are rewired):
```
make templ
go build ./...
go test ./web/ -run TestClustersBody
go test ./internal/api/ -run TestClusters
```

- [ ] **Step 9:** Commit (self-contained, building tree):
```
git add web/clusters.templ web/clusters_templ.go web/clusters_test.go \
        internal/api/clusters.go internal/api/sparkline.go internal/api/clusters_test.go \
        internal/api/vertical_helpers_test.go internal/api/server.go
git rm web/databases.templ web/databases_templ.go internal/api/databases.go internal/api/databases_test.go
git commit -m "feat(ui): Database › Clusters flat sortable list with health rollup + scope buttons (ly-ae6.5)"
```

---

### Task 3: Nodes list (`/databases/nodes`) with blind spots, providers, data sources

New screen. Cluster group headers (engine chip, name, `v<version>`, provider chip + ⓘ tooltip, DB→NODE→CLUSTER rollup line, `⌖`), and per-node rows (role chip, name + per-node version, data-source line, CPU/MEM/DISK/IO WAIT, conns/max_connections bar, health, `⌖`). RDS Multi-AZ / Azure HA standbys render dimmed as `◌ BLIND SPOT`. Search across cluster/node/provider metadata, `SORT: HEALTH/NAME` (issues first), pagination (3 groups/page).

**Backend dependencies (documented, not built here):** host metrics (CPU/MEM/DISK/IO WAIT) have no store/collector/ingestion path; provider identity + data-source + blind-spot detection are untracked in a per-node model — the tracked backend bead for provider awareness is **ly-7ck.3** (external managed Postgres support). Until those land, `CPU/Mem/Disk/IOWait` render `—`, `Provider`/`Source` are empty (chip/line omitted), and `BlindSpot` is always false from the handler. The view-model carries every field so the shell renders correctly the moment the data exists; the blind-spot rendering path is verified with a synthetic view-model row in the web unit test.

**Files:**
- create `web/nodes.templ` (+ `web/nodes_templ.go`)
- create `web/nodes_test.go`
- create `internal/api/nodes.go`
- create `internal/api/nodes_test.go`
- modify `internal/fleetview/summary.go` — add `NodeGroup`/`NodeRow` + `ListNodeGroups`
- modify `internal/fleetview/summary_test.go` — `ListNodeGroups` case

**Interfaces:**

Produces (package `fleetview`):
```go
type NodeGroup struct {
	Cluster  store.Cluster
	Version  string // cluster's representative display version (from server_version_num)
	CritOpen int    // cluster-level open-check counts (for the rollup line)
	WarnOpen int
	InfoOpen int
	Nodes    []NodeRow
}

// NodeRow is one instance (node). Host metrics / provider / data-source / blind-spot
// are backend gaps (see Task 3 note) — fields present, zero/empty until collected.
type NodeRow struct {
	Instance    store.Instance
	Role        string // upper-cased instance role: PRIMARY | REPLICA | UNKNOWN
	Version     string // per-node display version from server_version_num (may differ on rolling upgrades)
	ActiveConns int64
	MaxConns    int64  // pg_settings max_connections; 0 unknown
	CritOpen    int    // per-node (per-instance) open-check counts
	WarnOpen    int
	InfoOpen    int
}

func ListNodeGroups(ctx context.Context, cfg store.Config, stats store.Stats, since, until time.Time) ([]NodeGroup, error)
```

Produces (package `web`):
```go
type NodeGroupVM struct {
	ClusterID    string
	Name         string
	EngineIcon   string // "eng-pg"
	EngineName   string // "POSTGRES"
	Version      string
	Provider     string // "" -> chip omitted
	ProviderNote string // ⓘ tooltip
	Rollup       string // "NODE HEALTH 1 CRIT · 1 WARN · 2 OK → CLUSTER DEGRADED"
	RollupClass  string // "hl-crit"|"hl-warn"|"hl-ok"
	SevRank      int    // cluster-level crit-first rank, for the HEALTH sort (mirrors ClusterListRow.SevRank / DatabaseGroupVM.SevRank)
	ScopeHref    string
	ScopeTarget  string
	Nodes        []NodeRowVM
}

type NodeRowVM struct {
	Role        string // "PRIMARY" | "REPLICA" | "POOLER" | "STANDBY" | "UNKNOWN"
	RoleClass   string // "role-primary" | "role-replica" | "role-pooler" | "role-standby" | "role-unknown"
	Name        string
	NameBlind   bool   // dim the name when BlindSpot
	Version     string // "" -> version tag omitted
	Source      string // data-source line; "" -> "source unknown"
	CPU         string // "—" when unknown
	Mem         string
	Disk        string
	IOWait      string
	Conns       string // "87 / 200" or "12 / —"
	ConnsPct    string // "44%" (bar width); "0%" when max unknown
	Health      string // "● CRIT" | "● WARN" | "● OK" | "◌ BLIND SPOT"
	HealthClass string // "hl-crit"|"hl-warn"|"hl-ok"|"hl-warn"(blind)
	BlindSpot   bool
	ScopeHref   string
	ScopeTarget string
}

type NodesView struct {
	Groups     []NodeGroupVM
	Query      string
	Sort       string
	SortLabel  string
	SortHref   string // "/partial/databases/nodes?sort=<next>&q=<q>"
	PageHref   string // "/databases/nodes?sort=<next>&q=<q>" (no-JS)
	PrevHref   string
	NextHref   string
	HasPrev    bool
	HasNext    bool
	ShowPager  bool
	PagerLabel string // "PAGE 1 / 2"
	NoResults  bool
}

templ NodesPage(v NodesView)
templ NodesBody(v NodesView)   // root: <div id="nodes-screen" class="vlist"> ... </div>
```

Consumes: `cfg.ListClusters`, `cfg.ListInstances`, `cfg.ServerIDsForCluster`, `cfg.ServerIDsForInstance`, `stats.ActivitySummaryForServers`, `fleetview.rollupOpenChecks`, `fleetview.settingsForServer`.

**Steps:**

- [ ] **Step 1:** Write the failing web unit test `web/nodes_test.go` covering the grid, the conns bar, and the **blind-spot** dim path:
```go
package web

import (
	"context"
	"strings"
	"testing"
)

func renderNodes(t *testing.T, v NodesView) string {
	t.Helper()
	var sb strings.Builder
	if err := NodesBody(v).Render(context.Background(), &sb); err != nil {
		t.Fatalf("render: %v", err)
	}
	return sb.String()
}

func TestNodesBody_GroupAndRow(t *testing.T) {
	html := renderNodes(t, NodesView{Groups: []NodeGroupVM{{
		ClusterID: "c1", Name: "orders-prod", EngineIcon: "eng-pg", EngineName: "POSTGRES",
		Version: "16.3", Provider: "SELF-HOSTED", ProviderNote: "Collector on host",
		Rollup: "NODE HEALTH 1 CRIT · 2 OK → CLUSTER DEGRADED", RollupClass: "hl-crit",
		ScopeHref: "/databases/c1", ScopeTarget: "cluster:c1",
		Nodes: []NodeRowVM{{
			Role: "PRIMARY", RoleClass: "role-primary", Name: "srv-orders-primary",
			Version: "16.3", Source: "collector on host · node + pg stats",
			CPU: "—", Mem: "—", Disk: "—", IOWait: "—",
			Conns: "87 / 200", ConnsPct: "44%", Health: "● CRIT", HealthClass: "hl-crit",
			ScopeHref: "/databases/c1", ScopeTarget: "node:orders-prod/srv-orders-primary",
		}},
	}}})
	for _, want := range []string{
		`id="nodes-screen"`,
		`href="#eng-pg"`, `orders-prod`, `v16.3`,
		`SELF-HOSTED`, `⌖`,
		`NODE HEALTH 1 CRIT · 2 OK → CLUSTER DEGRADED`,
		`class="tbl-scroll"`, `class="nodes-grid"`, // horizontal-scroll wrapper + wide grid (min-width lives in verticals.css, not the HTML)
		`role-primary`, `srv-orders-primary`,
		`collector on host · node + pg stats`,
		`87 / 200`, `width:44%`,
		`● CRIT`,
		`data-scope="node:orders-prod/srv-orders-primary"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("NodesBody missing %q", want)
		}
	}
}

func TestNodesBody_BlindSpotDimmed(t *testing.T) {
	html := renderNodes(t, NodesView{Groups: []NodeGroupVM{{
		Name: "analytics-stage", EngineIcon: "eng-pg", Provider: "AWS RDS · MULTI-AZ",
		Nodes: []NodeRowVM{{
			Role: "STANDBY", RoleClass: "role-standby", Name: "analytics-standby-az2",
			NameBlind: true, Source: "no endpoint — CloudWatch instance metrics only",
			CPU: "—", Mem: "—", Disk: "—", IOWait: "—", Conns: "— / —", ConnsPct: "0%",
			Health: "◌ BLIND SPOT", HealthClass: "hl-warn", BlindSpot: true,
		}},
	}}})
	if !strings.Contains(html, "◌ BLIND SPOT") {
		t.Fatal("blind-spot health text missing")
	}
	if !strings.Contains(html, "node-name--blind") {
		t.Fatal("blind-spot node name is not dimmed")
	}
	if !strings.Contains(html, "no endpoint — CloudWatch instance metrics only") {
		t.Fatal("blind-spot data-source line missing")
	}
}
```

- [ ] **Step 2:** Run it, expect FAIL (undefined `NodesView`/`NodesBody`):
```
go test ./web/ -run TestNodesBody
```

- [ ] **Step 3:** Create `web/nodes.templ`. Declare the two structs (as in Interfaces), then:
```go
templ NodesPage(v NodesView) {
	@Layout("Lynceus — Nodes", "database vertical · nodes") {
		@EngineSprite()
		@NodesBody(v)
	}
}

templ NodesBody(v NodesView) {
	<div id="nodes-screen" class="vlist">
		<div class="vlist-head">
			<span class="vlist-title">Nodes</span>
			<span class="badge-live">LIVE</span>
			<span class="vlist-strap">HEALTH ROLLS UP: DATABASE → NODE → CLUSTER</span>
			<span class="vlist-spacer"></span>
			<form hx-get="/partial/databases/nodes" hx-target="#nodes-screen" hx-swap="outerHTML" hx-trigger="keyup changed delay:300ms from:input[name='q'], submit" style="display:contents">
				<input type="hidden" name="sort" value={ v.Sort }/>
				<input class="vlist-search" type="text" name="q" value={ v.Query } placeholder="search cluster / node / provider…"/>
			</form>
			<a class="vlist-btn" href={ templ.SafeURL(v.PageHref) } hx-get={ v.SortHref } hx-target="#nodes-screen" hx-swap="outerHTML">SORT: { v.SortLabel } ⇅</a>
		</div>
		if v.NoResults {
			<div class="vempty">NO CLUSTERS OR NODES MATCH</div>
		}
		for _, g := range v.Groups {
			<div class="vpanel">
				<div class="grp-head">
					<span class="eng-mark" title={ g.EngineName }>
						<svg width="12" height="12" viewBox="0 0 24 24"><use href={ "#" + g.EngineIcon }></use></svg>
					</span>
					<span class="grp-name">{ g.Name }</span>
					if g.Version != "" {
						<span class="grp-ver">{ "v" + g.Version }</span>
					}
					if g.Provider != "" {
						<span class="grp-provider" title={ g.ProviderNote }>{ g.Provider } ⓘ</span>
					}
					<span class="vlist-spacer"></span>
					<span class={ "grp-rollup " + g.RollupClass }>{ g.Rollup }</span>
					<a class="scope-btn" href={ templ.SafeURL(g.ScopeHref) } data-scope={ g.ScopeTarget } title="Set scope to this cluster">⌖</a>
				</div>
				<div class="tbl-scroll">
					<div class="nodes-grid">
						<div class="nodes-hd">
							<span>ROLE</span><span>NODE</span><span class="num-r">CPU</span><span class="num-r">MEM</span><span class="num-r">DISK</span><span class="num-r">IO WAIT</span><span>CONNS / MAX_CONNECTIONS</span><span class="num-r">HEALTH</span><span></span>
						</div>
						for _, n := range g.Nodes {
							<div class="nodes-row">
								<span class={ "role-chip " + n.RoleClass }>{ n.Role }</span>
								<div class="node-id">
									<span class="node-idline">
										<span class={ classIf("node-name", "node-name--blind", n.NameBlind) }>{ n.Name }</span>
										if n.Version != "" {
											<span class="node-nodever">{ "v" + n.Version }</span>
										}
									</span>
									<span class="node-src">{ nodeSource(n.Source) }</span>
								</div>
								<span class="node-metric num-r">{ n.CPU }</span>
								<span class="node-metric num-r">{ n.Mem }</span>
								<span class="node-metric num-r">{ n.Disk }</span>
								<span class="node-metric num-r">{ n.IOWait }</span>
								<div class="conns-wrap">
									<div class="conns-bar"><div class="conns-fill" style={ "width:" + n.ConnsPct }></div></div>
									<span class="conns-num">{ n.Conns }</span>
								</div>
								<span class={ "node-health " + n.HealthClass }>{ n.Health }</span>
								<a class="scope-btn" href={ templ.SafeURL(n.ScopeHref) } data-scope={ n.ScopeTarget } title="Set scope to this node">⌖</a>
							</div>
						}
					</div>
				</div>
			</div>
		}
		if v.ShowPager {
			<div class="vpager">
				<span>{ v.PagerLabel }</span>
				<a class={ classIf("", "is-disabled", !v.HasPrev) } href={ templ.SafeURL(v.PrevHref) } hx-get={ v.PrevHref } hx-target="#nodes-screen" hx-swap="outerHTML">‹ PREV</a>
				<a class={ classIf("", "is-disabled", !v.HasNext) } href={ templ.SafeURL(v.NextHref) } hx-get={ v.NextHref } hx-target="#nodes-screen" hx-swap="outerHTML">NEXT ›</a>
				<span class="vlist-spacer"></span>
				<span class="hint">SORT: HEALTH PUTS CLUSTERS WITH OPEN ISSUES FIRST</span>
			</div>
		}
	</div>
}

// nodeSource renders the data-source line, defaulting to a neutral note when the
// provider/data-source model has not populated it yet (backend gap ly-7ck.3).
func nodeSource(src string) string {
	if src == "" {
		return "source unknown — collector reporting"
	}
	return src
}

// classIf builds a class attribute string, appending extra only when on. Uses
// plain string concatenation — the SAME pattern already generated elsewhere in
// this repo (web/overview.templ, web/cluster_views.templ do `class={ "x-" + s }`).
// Deliberately NOT templ.KV: templ.KV has ZERO usages in web/*.templ, so this
// keeps the conditional-class path on a proven mechanism.
func classIf(base, extra string, on bool) string {
	if !on {
		return base
	}
	if base == "" {
		return extra
	}
	return base + " " + extra
}
```
**Note on the conns bar `style={ "width:" + n.ConnsPct }` (verified, not speculative).** templ compiles a dynamic `style` attribute to `templruntime.SanitizeStyleAttributeValues(expr)` (generator.go:1337). For a `string` value it calls `safehtml.SanitizeStyleValue`, which passes ASCII declarations like `width:44%` through unchanged and appends a trailing `;`, so the rendered attribute is exactly `style="width:44%;"`. The web unit test asserts the substring `width:44%`, which matches. This is a real, verified templ feature (confirmed against the vendored templ v0.3.1020 runtime in the module cache) — it does not depend on any repo-local precedent. Do NOT switch to a `--w` custom property: `safehtml.SanitizeCSSProperty` rejects unknown/custom property names, so a `--w` style would be dropped.

- [ ] **Step 4:** Add `ListNodeGroups` to `internal/fleetview/summary.go`:
```go
// ListNodeGroups returns one group per cluster with its instances as node rows.
// Host metrics / provider / blind-spot fields are left zero (backend gaps); conns
// come from the activity store and max_connections/version from pg_settings.
func ListNodeGroups(ctx context.Context, cfg store.Config, stats store.Stats, since, until time.Time) ([]NodeGroup, error) {
	clusters, err := cfg.ListClusters(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]NodeGroup, 0, len(clusters))
	for _, cl := range clusters {
		clusterIDs, err := cfg.ServerIDsForCluster(ctx, cl.ID)
		if err != nil {
			return nil, err
		}
		crit, warn, info, err := rollupOpenChecks(ctx, stats, clusterIDs, since, until)
		if err != nil {
			return nil, err
		}
		g := NodeGroup{Cluster: cl, CritOpen: crit, WarnOpen: warn, InfoOpen: info}
		instances, err := cfg.ListInstances(ctx, cl.ID)
		if err != nil {
			return nil, err
		}
		for _, inst := range instances {
			ids, err := cfg.ServerIDsForInstance(ctx, inst.ID)
			if err != nil {
				return nil, err
			}
			row := NodeRow{Instance: inst, Role: strings.ToUpper(inst.Role)}
			if len(ids) > 0 {
				act, err := stats.ActivitySummaryForServers(ctx, ids, since, until)
				if err != nil {
					return nil, err
				}
				row.ActiveConns = act.ActiveConns
				if row.Version, row.MaxConns, err = settingsForServer(ctx, stats, ids[0], until); err != nil {
					return nil, err
				}
				if row.CritOpen, row.WarnOpen, row.InfoOpen, err = rollupOpenChecks(ctx, stats, ids, since, until); err != nil {
					return nil, err
				}
				if g.Version == "" {
					g.Version = row.Version
				}
			}
			g.Nodes = append(g.Nodes, row)
		}
		out = append(out, g)
	}
	return out, nil
}
```

- [ ] **Step 4b:** Add the fleetview `ListNodeGroups` roll-up test to `internal/fleetview/summary_test.go` (external `package fleetview_test`; reuse `newStores(t)` already defined there). Seed one cluster with a primary carrying `server_version_num`/`max_connections`/activity, assert the group version and the node's real conns/version:
```go
func TestListNodeGroups_rollsUpNodesAndSettings(t *testing.T) {
	cfg, stats, configPool := newStores(t)
	ctx := context.Background()

	if _, err := configPool.Exec(ctx, `INSERT INTO servers (id, name) VALUES ($1, $1)`, "srv-p"); err != nil {
		t.Fatalf("seed server: %v", err)
	}
	cl, err := cfg.CreateCluster(ctx, "orders-prod")
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}
	inst, err := cfg.CreateInstance(ctx, cl.ID, "node-a")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	if _, err := configPool.Exec(ctx, `UPDATE instance SET role='primary' WHERE id=$1`, inst.ID); err != nil {
		t.Fatalf("set role: %v", err)
	}
	if err := cfg.AssignServerToInstance(ctx, "srv-p", inst.ID); err != nil {
		t.Fatalf("assign: %v", err)
	}

	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	if err := stats.WriteSettings(ctx, []store.SettingRow{
		{ServerID: "srv-p", CollectedAt: now, Name: "server_version_num", Value: "160003", DataTier: 1},
		{ServerID: "srv-p", CollectedAt: now, Name: "max_connections", Value: "200", DataTier: 1},
	}); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	if err := stats.WriteActivityBuckets(ctx, []store.ActivityBucket{
		{ServerID: "srv-p", Database: "orders", State: "active",
			BucketStart: now, BucketSeconds: 60, SampleCount: 6, CountSum: 87, CountMax: 87},
	}); err != nil {
		t.Fatalf("seed activity: %v", err)
	}

	since, until := now.Add(-time.Hour), now.Add(time.Hour)
	groups, err := fleetview.ListNodeGroups(ctx, cfg, stats, since, until)
	if err != nil {
		t.Fatalf("ListNodeGroups: %v", err)
	}
	if len(groups) != 1 || len(groups[0].Nodes) != 1 {
		t.Fatalf("groups/nodes = %d/%v, want 1/1", len(groups), len(groups))
	}
	g, n := groups[0], groups[0].Nodes[0]
	if g.Version != "16.3" {
		t.Fatalf("group version = %q, want 16.3", g.Version)
	}
	// ListNodeGroups upper-cases the instance role (Role: strings.ToUpper(inst.Role)).
	if n.Role != "PRIMARY" || n.Version != "16.3" || n.MaxConns != 200 || n.ActiveConns != 87 {
		t.Fatalf("node = role %q ver %q conns %d/%d, want PRIMARY/16.3/87/200",
			n.Role, n.Version, n.ActiveConns, n.MaxConns)
	}
}
```

- [ ] **Step 5:** Create `internal/api/nodes.go` — the handler assembles `NodesView` (search, health/name sort with issues-first, pagination of 3 groups/page, presentation mapping):
```go
package api

import (
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dobbo-ca/lynceus/internal/fleetview"
	"github.com/dobbo-ca/lynceus/web"
)

const nodeGroupsPerPage = 3

func (s *Server) handleNodes(w http.ResponseWriter, r *http.Request) {
	v := s.fetchNodes(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.NodesPage(v).Render(r.Context(), w)
}

func (s *Server) handleNodesPartial(w http.ResponseWriter, r *http.Request) {
	v := s.fetchNodes(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.NodesBody(v).Render(r.Context(), w)
}

func (s *Server) fetchNodes(r *http.Request) web.NodesView {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	sortKey := r.URL.Query().Get("sort")
	if sortKey != "name" {
		sortKey = "health"
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 0 {
		page = 0
	}
	next := "name"
	if sortKey == "name" {
		next = "health"
	}
	qEsc := url.QueryEscape(q)

	view := web.NodesView{
		Query:     q,
		Sort:      sortKey,
		SortLabel: strings.ToUpper(sortKey),
		SortHref:  "/partial/databases/nodes?sort=" + next + "&q=" + qEsc,
		PageHref:  "/databases/nodes?sort=" + next + "&q=" + qEsc,
	}

	now := time.Now().UTC()
	groups, err := fleetview.ListNodeGroups(r.Context(), s.conf, s.stats, now.AddDate(0, 0, -1), now)
	if err != nil {
		return view
	}

	vms := make([]web.NodeGroupVM, 0, len(groups))
	for i := range groups {
		g := &groups[i]
		if !nodeGroupMatches(g, strings.ToLower(q)) {
			continue
		}
		vms = append(vms, nodeGroupVM(g))
	}
	// SORT: health puts clusters with open issues first (SevRank desc), else name.
	// SevRank is carried ON the VM (populated in nodeGroupVM) — never reach into
	// `groups[i]` here: the search filter above drops non-matching groups, and
	// sort.SliceStable swaps `vms` elements while `groups` stays fixed, so `vms[i]`
	// and `groups[i]` are NOT the same cluster. Sorting on vms[i].SevRank is the
	// only correct source (matches the Clusters and Databases sorts).
	sort.SliceStable(vms, func(i, j int) bool {
		if sortKey == "health" && vms[i].SevRank != vms[j].SevRank {
			return vms[i].SevRank > vms[j].SevRank
		}
		return vms[i].Name < vms[j].Name
	})

	if len(vms) == 0 {
		view.NoResults = true
		return view
	}

	pageCount := (len(vms) + nodeGroupsPerPage - 1) / nodeGroupsPerPage
	if page > pageCount-1 {
		page = pageCount - 1
	}
	start := page * nodeGroupsPerPage
	end := start + nodeGroupsPerPage
	if end > len(vms) {
		end = len(vms)
	}
	view.Groups = vms[start:end]
	view.ShowPager = pageCount > 1
	view.PagerLabel = "PAGE " + strconv.Itoa(page+1) + " / " + strconv.Itoa(pageCount)
	view.HasPrev = page > 0
	view.HasNext = page < pageCount-1
	view.PrevHref = "/partial/databases/nodes?sort=" + sortKey + "&q=" + qEsc + "&page=" + strconv.Itoa(page-1)
	view.NextHref = "/partial/databases/nodes?sort=" + sortKey + "&q=" + qEsc + "&page=" + strconv.Itoa(page+1)
	return view
}

func nodeGroupMatches(g *fleetview.NodeGroup, needle string) bool {
	if needle == "" {
		return true
	}
	if strings.Contains(strings.ToLower(g.Cluster.Name), needle) {
		return true
	}
	for i := range g.Nodes {
		if strings.Contains(strings.ToLower(g.Nodes[i].Instance.Name), needle) ||
			strings.Contains(strings.ToLower(g.Nodes[i].Role), needle) {
			return true
		}
	}
	return false
}

func nodeGroupVM(g *fleetview.NodeGroup) web.NodeGroupVM {
	rollup, rclass := nodeRollup(g)
	vm := web.NodeGroupVM{
		ClusterID:   g.Cluster.ID,
		Name:        g.Cluster.Name,
		EngineIcon:  "eng-pg",
		EngineName:  "POSTGRES",
		Version:     g.Version,
		Rollup:      rollup,
		RollupClass: rclass,
		SevRank:     web.SevRank(g.CritOpen, g.WarnOpen, g.InfoOpen),
		ScopeHref:   "/databases/" + g.Cluster.ID,
		ScopeTarget: "cluster:" + g.Cluster.ID,
	}
	for i := range g.Nodes {
		vm.Nodes = append(vm.Nodes, nodeRowVM(g, &g.Nodes[i]))
	}
	return vm
}

func nodeRowVM(g *fleetview.NodeGroup, n *fleetview.NodeRow) web.NodeRowVM {
	health, hclass := "● OK", "hl-ok"
	switch {
	case n.CritOpen > 0:
		health, hclass = "● CRIT", "hl-crit"
	case n.WarnOpen > 0:
		health, hclass = "● WARN", "hl-warn"
	}
	conns, pct := "— / —", "0%"
	if n.MaxConns > 0 {
		conns = strconv.FormatInt(n.ActiveConns, 10) + " / " + strconv.FormatInt(n.MaxConns, 10)
		p := int(float64(n.ActiveConns) / float64(n.MaxConns) * 100.0)
		if p > 100 {
			p = 100
		}
		pct = strconv.Itoa(p) + "%"
	} else if n.ActiveConns > 0 {
		conns = strconv.FormatInt(n.ActiveConns, 10) + " / —"
	}
	return web.NodeRowVM{
		Role:        n.Role,
		RoleClass:   roleClass(n.Role),
		Name:        n.Instance.Name,
		Version:     n.Version,
		Source:      "", // backend gap (ly-7ck.3) — template renders the neutral fallback
		CPU:         "—",
		Mem:         "—",
		Disk:        "—",
		IOWait:      "—",
		Conns:       conns,
		ConnsPct:    pct,
		Health:      health,
		HealthClass: hclass,
		ScopeHref:   "/databases/" + g.Cluster.ID,
		ScopeTarget: "node:" + g.Cluster.Name + "/" + n.Instance.Name,
	}
}

func nodeRollup(g *fleetview.NodeGroup) (text, cssClass string) {
	ok := 0
	for i := range g.Nodes {
		if g.Nodes[i].CritOpen == 0 && g.Nodes[i].WarnOpen == 0 {
			ok++
		}
	}
	parts := make([]string, 0, 3)
	if g.CritOpen > 0 {
		parts = append(parts, strconv.Itoa(g.CritOpen)+" CRIT")
	}
	if g.WarnOpen > 0 {
		parts = append(parts, strconv.Itoa(g.WarnOpen)+" WARN")
	}
	parts = append(parts, strconv.Itoa(ok)+" OK")
	clusterLabel := "HEALTHY"
	cssClass = "hl-ok"
	switch {
	case g.CritOpen > 0:
		clusterLabel, cssClass = "DEGRADED", "hl-crit"
	case g.WarnOpen > 0:
		clusterLabel, cssClass = "WARNING", "hl-warn"
	}
	return "NODE HEALTH " + strings.Join(parts, " · ") + " → CLUSTER " + clusterLabel, cssClass
}

func roleClass(role string) string {
	switch role {
	case "PRIMARY":
		return "role-primary"
	case "REPLICA":
		return "role-replica"
	case "POOLER":
		return "role-pooler"
	case "STANDBY":
		return "role-standby"
	default:
		return "role-unknown"
	}
}
```

- [ ] **Step 6:** Write `internal/api/nodes_test.go` (testcontainers). `setupNodes` seeds two clusters so the test covers row anatomy AND the HEALTH multi-group ordering the sort bug would have broken:
  - `orders-prod` (healthy, SevRank 0): a `primary` instance (role set directly — `CreateInstance` defaults role to `unknown`, and there is no store role-setter yet; fleet role sync is ly-99s.3) + a `replica`; the primary's server carries `server_version_num=160003`, `max_connections=200`, and an active-connections activity bucket (`CountMax=87`).
  - `zzz-payments` (critical, SevRank 2): one instance whose server has a firing **critical** check.
  - `mmm-billing` (warning, SevRank 1): one instance whose server has a firing **warning** check.

  **Three groups with distinct severities is deliberate.** A correct HEALTH sort must render `zzz-payments` (crit) → `mmm-billing` (warn) → `orders-prod` (clean), which differs from BOTH creation order (`orders-prod, zzz-payments, mmm-billing`) and name order (`mmm-billing, orders-prod, zzz-payments`). The old bug read `SevRank` from the fixed `groups[i]` while `sort.SliceStable` permuted `vms`; with only two rows a single swap happens to line up and the bug hides, but across three rows the post-swap misalignment produces a wrong order — so this 3-way assertion actually fails on the buggy code and passes only on the `vms[i].SevRank` fix. (`newVerticalFleet`, `getBody`, `newDBPool` come from `internal/api/vertical_helpers_test.go`, Task 2.)
```go
package api_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dobbo-ca/lynceus/internal/store"
)

func setupNodes(t *testing.T) *httptest.Server {
	t.Helper()
	ctx := context.Background()
	srv, cfg, stats, configPool, _ := newVerticalFleet(t)
	now := time.Now().UTC()

	// --- orders-prod (healthy, rich metrics) ---
	for _, id := range []string{"srv-orders-primary", "srv-orders-replica"} {
		if _, err := configPool.Exec(ctx, `INSERT INTO servers (id, name) VALUES ($1, $1)`, id); err != nil {
			t.Fatalf("seed server %s: %v", id, err)
		}
	}
	cl, err := cfg.CreateCluster(ctx, "orders-prod")
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}
	primary, err := cfg.CreateInstance(ctx, cl.ID, "srv-orders-primary")
	if err != nil {
		t.Fatalf("CreateInstance primary: %v", err)
	}
	replica, err := cfg.CreateInstance(ctx, cl.ID, "srv-orders-replica")
	if err != nil {
		t.Fatalf("CreateInstance replica: %v", err)
	}
	// CreateInstance defaults role to 'unknown'; set the real roles directly.
	if _, err := configPool.Exec(ctx, `UPDATE instance SET role='primary' WHERE id=$1`, primary.ID); err != nil {
		t.Fatalf("set primary role: %v", err)
	}
	if _, err := configPool.Exec(ctx, `UPDATE instance SET role='replica' WHERE id=$1`, replica.ID); err != nil {
		t.Fatalf("set replica role: %v", err)
	}
	if err := cfg.AssignServerToInstance(ctx, "srv-orders-primary", primary.ID); err != nil {
		t.Fatalf("assign primary: %v", err)
	}
	if err := cfg.AssignServerToInstance(ctx, "srv-orders-replica", replica.ID); err != nil {
		t.Fatalf("assign replica: %v", err)
	}
	if err := stats.WriteSettings(ctx, []store.SettingRow{
		{ServerID: "srv-orders-primary", CollectedAt: now, Name: "server_version_num", Value: "160003", DataTier: 1},
		{ServerID: "srv-orders-primary", CollectedAt: now, Name: "max_connections", Value: "200", DataTier: 1},
	}); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	if err := stats.WriteActivityBuckets(ctx, []store.ActivityBucket{
		{ServerID: "srv-orders-primary", Database: "orders", State: "active",
			BucketStart: now, BucketSeconds: 60, SampleCount: 6, CountSum: 87, CountMax: 87},
	}); err != nil {
		t.Fatalf("seed activity: %v", err)
	}

	// --- two degraded clusters with distinct severities ---
	seedDegraded := func(clusterName, serverID, severity string) {
		if _, err := configPool.Exec(ctx, `INSERT INTO servers (id, name) VALUES ($1, $1)`, serverID); err != nil {
			t.Fatalf("seed server %s: %v", serverID, err)
		}
		dcl, err := cfg.CreateCluster(ctx, clusterName)
		if err != nil {
			t.Fatalf("CreateCluster %s: %v", clusterName, err)
		}
		dinst, err := cfg.CreateInstance(ctx, dcl.ID, serverID)
		if err != nil {
			t.Fatalf("CreateInstance %s: %v", clusterName, err)
		}
		if err := cfg.AssignServerToInstance(ctx, serverID, dinst.ID); err != nil {
			t.Fatalf("assign %s: %v", serverID, err)
		}
		if err := stats.WriteChecksResults(ctx, []store.ChecksResultRow{{
			ServerID: serverID, EvaluatedAt: now, CheckID: "settings.fsync",
			Category: "settings", Severity: severity, Status: "firing", Object: "server", DataTier: 1,
		}}); err != nil {
			t.Fatalf("seed check %s: %v", clusterName, err)
		}
	}
	seedDegraded("zzz-payments", "srv-payments", "critical") // SevRank 2
	seedDegraded("mmm-billing", "srv-billing", "warning")    // SevRank 1
	return srv
}

func TestNodes_GroupRowsSearchAndHealthSort(t *testing.T) {
	srv := setupNodes(t)
	html := getBody(t, srv.URL + "/databases/nodes") // default sort=health

	// Row anatomy on the healthy cluster.
	for _, want := range []string{
		`id="nodes-screen"`, "orders-prod", "v16.3", // version chip (from server_version_num — the real collector field)
		"NODE HEALTH", "role-primary", "role-replica",
		"87 / 200",                 // conns / max_connections
		`data-scope="node:orders-prod/srv-orders-primary"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("nodes page missing %q", want)
		}
	}

	// HEALTH sort (default): crit → warn → clean, i.e. zzz-payments (crit) before
	// mmm-billing (warn) before orders-prod (clean). This differs from creation
	// order AND name order, and requires 3 groups to reliably expose the old
	// groups[i]-indexed sort bug (a 2-group case can pass on the buggy code).
	iCrit := strings.Index(html, "zzz-payments")
	iWarn := strings.Index(html, "mmm-billing")
	iClean := strings.Index(html, "orders-prod")
	if !(iCrit < iWarn && iWarn < iClean) {
		t.Fatalf("HEALTH sort order wrong: crit@%d warn@%d clean@%d (want crit<warn<clean)", iCrit, iWarn, iClean)
	}

	// Empty search state.
	miss := getBody(t, srv.URL + "/databases/nodes?q=no-such-cluster")
	if !strings.Contains(miss, "NO CLUSTERS OR NODES MATCH") {
		t.Fatal("empty-search state missing")
	}
}
```

- [ ] **Step 7:** Register the routes locally so the test can hit them (Task 5 finalizes, but add now to run this task's test). In `internal/api/server.go` `routes()`:
```go
	s.mux.HandleFunc("GET /databases/nodes", s.handleNodes)
	s.mux.HandleFunc("GET /partial/databases/nodes", s.handleNodesPartial)
```

- [ ] **Step 8:** Regenerate + test:
```
make templ
go build ./...
go test ./web/ -run TestNodesBody
go test ./internal/fleetview/ -run TestListNodeGroups
go test ./internal/api/ -run TestNodes
```
Expected: PASS.

- [ ] **Step 9:** Commit:
```
git add web/nodes.templ web/nodes_templ.go web/nodes_test.go internal/api/nodes.go internal/api/nodes_test.go internal/fleetview/summary.go internal/fleetview/summary_test.go internal/api/server.go
git commit -m "feat(ui): Database › Nodes list — group rollups, blind-spot rows, provider chips, conns bars (ly-ae6.5)"
```

---

### Task 4: Databases list (`/databases/databases`) — cluster-qualified individual databases

New screen. Individual databases grouped by cluster (group header: engine chip, name, version, cluster health line, `⌖`), each database row showing `name`, cluster-qualified identity sub-line (`orders-prod/orders`), SIZE / QPS / CONNS / CACHE / TABLES, and a `⌖` scope button. Info strip: identity is cluster+name; stats never merge across clusters.

**Backend dependencies (documented):** per-database SIZE, CACHE-hit %, and TABLES have no store query today (COMPARISON db-databases). QPS + CONNS are real (query/activity stores). SIZE/TABLES relate to schema/table-stats beads **ly-xqf.6** (table growth: `pg_relation_size`/TOAST) and **ly-xqf.7** (per-table indexes); CACHE-hit needs a per-database `pg_stat_database` blks_hit ratio not yet collected. Those three columns render `—` with the fields present for later population.

**Row-existence dependency — `servers.database_name` (LOAD-BEARING for THIS screen, flag it like the metric gaps).** A database row exists only for a `ServerStream` whose `database_name` is non-empty: `ListDatabaseGroups` skips streams where `st.DatabaseName == ""` (grouping is by cluster + database_name), and the handler skips clusters with zero entries. The `servers.database_name` column is **nullable** (`COALESCE(s.database_name,'')` in `store.ResolveServer`) and is NOT written by any path in the current tree — it is expected to be populated at **collector enrollment / stream registration**, owned by bead **ly-8b0.8** (M5: Collector enrollment + scoped token issuance). **Production consequence to state plainly:** until ly-8b0.8 (or an equivalent enrollment path) sets `database_name`, the Databases screen renders **zero rows** even though clusters/nodes are healthy — this screen is dark until enrollment stamps the database name, exactly the way SIZE/CACHE/TABLES are dark until their collectors land. The integration test (Step 6) seeds `database_name` directly via SQL to exercise the populated path; the empty-fleet route test (Task 5) exercises the zero-rows path. Do not treat a passing test as proof the screen shows data in production — that requires ly-8b0.8.

**Files:**
- create `web/databases_list.templ` (+ `web/databases_list_templ.go`)
- create `web/databases_list_test.go`
- create `internal/api/databases_list.go`
- create `internal/api/databases_list_test.go`
- modify `internal/fleetview/summary.go` — add `DatabaseGroup`/`DatabaseEntry` + `ListDatabaseGroups`
- modify `internal/fleetview/summary_test.go` — `ListDatabaseGroups` case

**Interfaces:**

Produces (package `fleetview`):
```go
type DatabaseGroup struct {
	Cluster  store.Cluster
	Version  string
	CritOpen int
	WarnOpen int
	InfoOpen int
	Entries  []DatabaseEntry
}

// DatabaseEntry is one database (cluster+name identity). SizeBytes/CacheHitPct/
// TableCount are backend gaps (ly-xqf.6/ly-xqf.7 + pg_stat_database) — 0 today.
type DatabaseEntry struct {
	Name        string // database_name from the server stream
	QPS         float64
	ActiveConns int64
	SizeBytes   int64
	CacheHitPct float64
	TableCount  int
}

func ListDatabaseGroups(ctx context.Context, cfg store.Config, stats store.Stats, since, until time.Time) ([]DatabaseGroup, error)
```

Produces (package `web`):
```go
type DatabaseEntryVM struct {
	Name        string
	Qual        string // "orders-prod/orders"
	Size        string // "—" until collected
	QPS         string
	Conns       string
	Cache       string // "—"
	Tables      string // "—"
	ScopeHref   string
	ScopeTarget string
}
type DatabaseGroupVM struct {
	ClusterID   string
	Name        string
	EngineIcon  string
	EngineName  string
	Version     string
	HealthText  string
	HealthClass string
	SevRank     int
	ScopeHref   string
	ScopeTarget string
	Entries     []DatabaseEntryVM
}
type DatabasesListView struct {
	Groups     []DatabaseGroupVM
	CountLabel string // "7 DATABASES ACROSS 5 CLUSTERS"
	Sort       string
	SortLabel  string
	SortHref   string
	PageHref   string
}

templ DatabasesListPage(v DatabasesListView)
templ DatabasesListBody(v DatabasesListView)   // root: <div id="databases-screen" class="vlist"> ... </div>
```

Consumes: `cfg.ListClusters`, `cfg.ListInstances`, `cfg.ListServerStreams`, `stats.QPSBucketsForServers`, `stats.ActivitySummaryForServers`, `fleetview.rollupOpenChecks`, `fleetview.settingsForServer`.

**Steps:**

- [ ] **Step 1:** Write the failing web unit test `web/databases_list_test.go`:
```go
package web

import (
	"context"
	"strings"
	"testing"
)

func TestDatabasesListBody_QualifiedIdentityAndInfoStrip(t *testing.T) {
	var sb strings.Builder
	v := DatabasesListView{
		CountLabel: "2 DATABASES ACROSS 1 CLUSTER", Sort: "name", SortLabel: "NAME",
		Groups: []DatabaseGroupVM{{
			ClusterID: "c1", Name: "orders-prod", EngineIcon: "eng-pg", EngineName: "POSTGRES",
			Version: "16.3", HealthText: "[DEGRADED] 1 CRIT · 4 WARN", HealthClass: "hl-crit",
			ScopeHref: "/databases/c1", ScopeTarget: "cluster:c1",
			Entries: []DatabaseEntryVM{{
				Name: "orders", Qual: "orders-prod/orders", Size: "—", QPS: "1,102",
				Conns: "64", Cache: "—", Tables: "—",
				ScopeHref: "/databases/c1", ScopeTarget: "db:orders-prod/orders",
			}},
		}},
	}
	if err := DatabasesListBody(v).Render(context.Background(), &sb); err != nil {
		t.Fatalf("render: %v", err)
	}
	html := sb.String()
	for _, want := range []string{
		`id="databases-screen"`,
		`2 DATABASES ACROSS 1 CLUSTER`,
		`A DATABASE IS IDENTIFIED BY CLUSTER + NAME`, // info strip
		`class="info-strip"`,
		`orders-prod/orders`,                          // qualified identity sub-line
		`class="db-name"`, `>orders<`,
		`1,102`, `data-scope="db:orders-prod/orders"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("DatabasesListBody missing %q", want)
		}
	}
}
```

- [ ] **Step 2:** Run it, expect FAIL:
```
go test ./web/ -run TestDatabasesListBody
```

- [ ] **Step 3:** Create `web/databases_list.templ` (structs as in Interfaces), then:
```go
templ DatabasesListPage(v DatabasesListView) {
	@Layout("Lynceus — Databases", "database vertical · databases") {
		@EngineSprite()
		@DatabasesListBody(v)
	}
}

templ DatabasesListBody(v DatabasesListView) {
	<div id="databases-screen" class="vlist">
		<div class="vlist-head">
			<span class="vlist-title">Databases</span>
			<span class="badge-live">LIVE</span>
			<span class="vlist-strap">{ v.CountLabel }</span>
			<span class="vlist-spacer"></span>
			<a class="vlist-btn" href={ templ.SafeURL(v.PageHref) } hx-get={ v.SortHref } hx-target="#databases-screen" hx-swap="outerHTML">SORT: { v.SortLabel } ⇅</a>
		</div>
		<div class="info-strip">◈ A DATABASE IS IDENTIFIED BY CLUSTER + NAME — <span class="prose">orders-prod/orders and analytics-stage/orders share a name but are unrelated databases; stats, insights and history are never merged across clusters.</span></div>
		for _, g := range v.Groups {
			<div class="vpanel">
				<div class="grp-head">
					<span class="eng-mark" title={ g.EngineName }>
						<svg width="12" height="12" viewBox="0 0 24 24"><use href={ "#" + g.EngineIcon }></use></svg>
					</span>
					<span class="grp-name">{ g.Name }</span>
					if g.Version != "" {
						<span class="grp-ver">{ "v" + g.Version }</span>
					}
					<span class="vlist-spacer"></span>
					<span class={ "grp-rollup " + g.HealthClass }>{ g.HealthText }</span>
					<a class="scope-btn" href={ templ.SafeURL(g.ScopeHref) } data-scope={ g.ScopeTarget } title="Set scope to this cluster">⌖</a>
				</div>
				<div class="tbl-scroll">
					<div class="dbs-grid">
						<div class="dbs-hd">
							<span>DATABASE</span><span class="num-r">SIZE</span><span class="num-r">QPS</span><span class="num-r">CONNS</span><span class="num-r">CACHE</span><span class="num-r">TABLES</span><span></span>
						</div>
						for _, d := range g.Entries {
							<div class="dbs-row">
								<div class="db-id">
									<span class="db-name">{ d.Name }</span>
									<span class="db-qual">{ d.Qual }</span>
								</div>
								<span class="db-metric num-r">{ d.Size }</span>
								<span class="db-metric db-qps num-r">{ d.QPS }</span>
								<span class="db-metric num-r">{ d.Conns }</span>
								<span class="db-metric num-r">{ d.Cache }</span>
								<span class="db-metric num-r">{ d.Tables }</span>
								<a class="scope-btn" href={ templ.SafeURL(d.ScopeHref) } data-scope={ d.ScopeTarget } title="Set scope to this database">⌖</a>
							</div>
						}
					</div>
				</div>
			</div>
		}
	</div>
}
```

- [ ] **Step 4:** Add `ListDatabaseGroups` to `internal/fleetview/summary.go`:
```go
// ListDatabaseGroups returns one group per cluster, each listing that cluster's
// individual databases (identified by cluster + database_name). QPS/conns come
// from the stats store; size/cache/table columns are backend gaps.
func ListDatabaseGroups(ctx context.Context, cfg store.Config, stats store.Stats, since, until time.Time) ([]DatabaseGroup, error) {
	clusters, err := cfg.ListClusters(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]DatabaseGroup, 0, len(clusters))
	for _, cl := range clusters {
		clusterIDs, err := cfg.ServerIDsForCluster(ctx, cl.ID)
		if err != nil {
			return nil, err
		}
		crit, warn, info, err := rollupOpenChecks(ctx, stats, clusterIDs, since, until)
		if err != nil {
			return nil, err
		}
		g := DatabaseGroup{Cluster: cl, CritOpen: crit, WarnOpen: warn, InfoOpen: info}
		if len(clusterIDs) > 0 {
			if g.Version, _, err = settingsForServer(ctx, stats, clusterIDs[0], until); err != nil {
				return nil, err
			}
		}
		// Collect stream serverIDs per database name within this cluster.
		byDB := map[string][]string{}
		order := []string{}
		instances, err := cfg.ListInstances(ctx, cl.ID)
		if err != nil {
			return nil, err
		}
		for _, inst := range instances {
			streams, err := cfg.ListServerStreams(ctx, inst.ID)
			if err != nil {
				return nil, err
			}
			for _, st := range streams {
				if st.DatabaseName == "" {
					continue
				}
				if _, seen := byDB[st.DatabaseName]; !seen {
					order = append(order, st.DatabaseName)
				}
				byDB[st.DatabaseName] = append(byDB[st.DatabaseName], st.ServerID)
			}
		}
		for _, name := range order {
			ids := byDB[name]
			e := DatabaseEntry{Name: name}
			buckets, err := stats.QPSBucketsForServers(ctx, ids, since, until)
			if err != nil {
				return nil, err
			}
			if n := len(buckets); n > 0 {
				e.QPS = float64(buckets[n-1].Calls) / 3600.0
			}
			act, err := stats.ActivitySummaryForServers(ctx, ids, since, until)
			if err != nil {
				return nil, err
			}
			e.ActiveConns = act.ActiveConns
			g.Entries = append(g.Entries, e)
		}
		out = append(out, g)
	}
	return out, nil
}
```

- [ ] **Step 4b:** Add the fleetview `ListDatabaseGroups` test to `internal/fleetview/summary_test.go` (external `package fleetview_test`; reuse `newStores(t)`). Seed one cluster, two streams with distinct `database_name`s, and assert both databases surface — plus that a stream with an EMPTY `database_name` is skipped (the production dark-screen behavior documented above):
```go
func TestListDatabaseGroups_perDatabaseAndSkipsBlankName(t *testing.T) {
	cfg, stats, configPool := newStores(t)
	ctx := context.Background()

	cl, err := cfg.CreateCluster(ctx, "orders-prod")
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}
	inst, err := cfg.CreateInstance(ctx, cl.ID, "node-a")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	// Two named streams + one with a blank database_name (must be skipped).
	seed := func(serverID, dbName string) {
		if _, err := configPool.Exec(ctx, `INSERT INTO servers (id, name) VALUES ($1, $1)`, serverID); err != nil {
			t.Fatalf("seed server %s: %v", serverID, err)
		}
		if err := cfg.AssignServerToInstance(ctx, serverID, inst.ID); err != nil {
			t.Fatalf("assign %s: %v", serverID, err)
		}
		if dbName != "" {
			if _, err := configPool.Exec(ctx, `UPDATE servers SET database_name=$1 WHERE id=$2`, dbName, serverID); err != nil {
				t.Fatalf("set database_name: %v", err)
			}
		}
	}
	seed("srv-orders", "orders")
	seed("srv-billing", "billing")
	seed("srv-blank", "") // blank database_name — no row expected

	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	groups, err := fleetview.ListDatabaseGroups(ctx, cfg, stats, now.Add(-time.Hour), now.Add(time.Hour))
	if err != nil {
		t.Fatalf("ListDatabaseGroups: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("groups = %d, want 1", len(groups))
	}
	names := map[string]bool{}
	for _, e := range groups[0].Entries {
		names[e.Name] = true
	}
	if len(names) != 2 || !names["orders"] || !names["billing"] {
		t.Fatalf("database names = %v, want {orders,billing} (blank-name stream skipped)", names)
	}
}
```

- [ ] **Step 5:** Create `internal/api/databases_list.go`:
```go
package api

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dobbo-ca/lynceus/internal/fleetview"
	"github.com/dobbo-ca/lynceus/web"
)

func (s *Server) handleDatabasesList(w http.ResponseWriter, r *http.Request) {
	v := s.fetchDatabasesList(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.DatabasesListPage(v).Render(r.Context(), w)
}

func (s *Server) handleDatabasesListPartial(w http.ResponseWriter, r *http.Request) {
	v := s.fetchDatabasesList(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.DatabasesListBody(v).Render(r.Context(), w)
}

func (s *Server) fetchDatabasesList(r *http.Request) web.DatabasesListView {
	sortKey := r.URL.Query().Get("sort")
	if sortKey != "health" {
		sortKey = "name"
	}
	next := "health"
	if sortKey == "health" {
		next = "name"
	}
	view := web.DatabasesListView{
		Sort:      sortKey,
		SortLabel: strings.ToUpper(sortKey),
		SortHref:  "/partial/databases/databases?sort=" + next,
		PageHref:  "/databases/databases?sort=" + next,
	}

	now := time.Now().UTC()
	groups, err := fleetview.ListDatabaseGroups(r.Context(), s.conf, s.stats, now.AddDate(0, 0, -1), now)
	if err != nil {
		return view
	}

	totalDBs, totalClusters := 0, 0
	vms := make([]web.DatabaseGroupVM, 0, len(groups))
	for i := range groups {
		g := &groups[i]
		if len(g.Entries) == 0 {
			continue
		}
		totalClusters++
		totalDBs += len(g.Entries)
		text, class := web.HealthLine(g.CritOpen, g.WarnOpen, g.InfoOpen)
		gvm := web.DatabaseGroupVM{
			ClusterID:   g.Cluster.ID,
			Name:        g.Cluster.Name,
			EngineIcon:  "eng-pg",
			EngineName:  "POSTGRES",
			Version:     g.Version,
			HealthText:  text,
			HealthClass: class,
			SevRank:     web.SevRank(g.CritOpen, g.WarnOpen, g.InfoOpen),
			ScopeHref:   "/databases/" + g.Cluster.ID,
			ScopeTarget: "cluster:" + g.Cluster.ID,
		}
		for j := range g.Entries {
			e := &g.Entries[j]
			gvm.Entries = append(gvm.Entries, web.DatabaseEntryVM{
				Name:        e.Name,
				Qual:        g.Cluster.Name + "/" + e.Name,
				Size:        "—",
				QPS:         groupThousands(int64(e.QPS + 0.5)),
				Conns:       strconv.FormatInt(e.ActiveConns, 10),
				Cache:       "—",
				Tables:      "—",
				ScopeHref:   "/databases/" + g.Cluster.ID,
				ScopeTarget: "db:" + g.Cluster.Name + "/" + e.Name,
			})
		}
		vms = append(vms, gvm)
	}
	sort.SliceStable(vms, func(i, j int) bool {
		if sortKey == "health" && vms[i].SevRank != vms[j].SevRank {
			return vms[i].SevRank > vms[j].SevRank
		}
		return vms[i].Name < vms[j].Name
	})

	view.Groups = vms
	view.CountLabel = strconv.Itoa(totalDBs) + " " + plural(totalDBs, "DATABASE", "DATABASES") +
		" ACROSS " + strconv.Itoa(totalClusters) + " " + plural(totalClusters, "CLUSTER", "CLUSTERS")
	return view
}
```

- [ ] **Step 6:** Write `internal/api/databases_list_test.go` (testcontainers). `setupDatabasesScreen` seeds one cluster/instance with two streams whose `database_name`s are `orders` and `billing` (stamped directly — in production this is written at enrollment, ly-8b0.8), plus query stats so each database has a QPS. (`newVerticalFleet`/`getBody` from `internal/api/vertical_helpers_test.go`, Task 2.)
```go
package api_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dobbo-ca/lynceus/internal/store"
)

func setupDatabasesScreen(t *testing.T) *httptest.Server {
	t.Helper()
	ctx := context.Background()
	srv, cfg, stats, configPool, _ := newVerticalFleet(t)
	now := time.Now().UTC()

	cl, err := cfg.CreateCluster(ctx, "orders-prod")
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}
	inst, err := cfg.CreateInstance(ctx, cl.ID, "node-a")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	for _, d := range []struct{ serverID, dbName string }{
		{"srv-orders", "orders"},
		{"srv-billing", "billing"},
	} {
		if _, err := configPool.Exec(ctx, `INSERT INTO servers (id, name) VALUES ($1, $1)`, d.serverID); err != nil {
			t.Fatalf("seed server %s: %v", d.serverID, err)
		}
		if err := cfg.AssignServerToInstance(ctx, d.serverID, inst.ID); err != nil {
			t.Fatalf("assign %s: %v", d.serverID, err)
		}
		// Stamp the (nullable) database_name — the field ListDatabaseGroups groups
		// by. Direct SQL stands in for the enrollment path (ly-8b0.8).
		if _, err := configPool.Exec(ctx, `UPDATE servers SET database_name=$1 WHERE id=$2`, d.dbName, d.serverID); err != nil {
			t.Fatalf("set database_name %s: %v", d.serverID, err)
		}
		if err := stats.WriteQueryStats(ctx, []store.QueryStat{
			{ServerID: d.serverID, CollectedAt: now.Add(-time.Hour), Fingerprint: "fp-" + d.dbName,
				NormalizedQuery: "SELECT $1", Calls: 3600, TotalTimeMs: 720},
		}); err != nil {
			t.Fatalf("seed query stats %s: %v", d.serverID, err)
		}
	}
	return srv
}

func TestDatabasesList_QualifiedRowsAndCount(t *testing.T) {
	srv := setupDatabasesScreen(t)
	html := getBody(t, srv.URL + "/databases/databases")
	for _, want := range []string{
		`id="databases-screen"`,
		"2 DATABASES ACROSS 1 CLUSTER",
		"A DATABASE IS IDENTIFIED BY CLUSTER + NAME", // info strip
		"orders-prod/orders", "orders-prod/billing", // cluster-qualified identities
		`data-scope="db:orders-prod/orders"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("databases page missing %q", want)
		}
	}
}
```

- [ ] **Step 7:** Register routes locally (finalized in Task 5). In `server.go` `routes()`:
```go
	s.mux.HandleFunc("GET /databases/databases", s.handleDatabasesList)
	s.mux.HandleFunc("GET /partial/databases/databases", s.handleDatabasesListPartial)
```

- [ ] **Step 8:** Regenerate + test:
```
make templ
go build ./...
go test ./web/ -run TestDatabasesListBody
go test ./internal/fleetview/ -run TestListDatabaseGroups
go test ./internal/api/ -run TestDatabasesList
```
Expected: PASS.

- [ ] **Step 9:** Commit:
```
git add web/databases_list.templ web/databases_list_templ.go web/databases_list_test.go internal/api/databases_list.go internal/api/databases_list_test.go internal/fleetview/summary.go internal/fleetview/summary_test.go internal/api/server.go
git commit -m "feat(ui): Database › Databases list — cluster-qualified per-database rows + identity info strip (ly-ae6.5)"
```

---

### Task 5: Route wiring, scope-shell integration contract, full verification

Finalizes route registration, records the integration contract for ly-ae6.2/ly-ae6.3, and runs the whole affected suite + templ-sync + build.

**Files:**
- `internal/api/server.go` (verify all six routes — the edits happened in Tasks 2/3/4)
- create `internal/api/vertical_routes_test.go` (route-presence / empty-state test for the three pages + `setupEmptyFleet`)
- create `docs/design/integration-db-vertical-lists.md` (contract notes — short)

**Interfaces (integration contract — what ly-ae6.2/ly-ae6.3 consume):**

Route scheme (Go 1.22 ServeMux; literal segments outrank the `/databases/{clusterID}` wildcard, so no conflict):

| Screen | Page route | HTMX fragment route | Nav label (DATABASE section) |
|--------|-----------|---------------------|------------------------------|
| Clusters | `GET /databases` | `GET /partial/databases` | Clusters |
| Nodes | `GET /databases/nodes` | `GET /partial/databases/nodes` | Nodes |
| Databases | `GET /databases/databases` | `GET /partial/databases/databases` | Databases |

Query params (stable, shell may deep-link with them): `?sort=health|name` (Clusters, Databases), plus `?q=<text>&page=<n>` (Nodes). Fragments swap `outerHTML` on `#clusters-screen` / `#nodes-screen` / `#databases-screen`.

Per-row scope-set encoding (the `⌖` button): rendered as an `<a class="scope-btn" href={ScopeHref} data-scope={ScopeTarget}>`. `ScopeHref` is the current best landing (`/databases/<clusterID>` cluster Overview) so the button works pre-shell and no-JS; `data-scope` carries the canonical target for the ly-ae6.2 top-bar scope state to adopt:
- cluster: `cluster:<clusterID>`
- node: `node:<clusterName>/<nodeName>`
- database: `db:<clusterName>/<dbName>`

When ly-ae6.2 lands, its client hook reads `data-scope` on click to set scope and redirect to the resource Overview (node/db Overviews are ly-ae6.6); until then the `href` cluster-Overview fallback applies. Rows themselves remain inert (no whole-row `<a>`), satisfying the design's "scope only via `⌖`" rule.

`+ ADD CLUSTER` links to `/onboarding?vertical=database` — the entry point ly-ae6.12 (onboarding wizard) implements. It 404s until then; that is the documented stub.

**Steps:**

- [ ] **Step 1:** Verify `internal/api/server.go` `routes()` now has exactly these six lines. The first two were swapped in Task 2 Step 7; Tasks 3 Step 7 and 4 Step 7 added the four sub-routes. This step only confirms the final state (no edit expected unless a prior task was skipped):
```go
	s.mux.HandleFunc("GET /databases", s.handleClusters)
	s.mux.HandleFunc("GET /partial/databases", s.handleClustersPartial)
	s.mux.HandleFunc("GET /databases/nodes", s.handleNodes)
	s.mux.HandleFunc("GET /partial/databases/nodes", s.handleNodesPartial)
	s.mux.HandleFunc("GET /databases/databases", s.handleDatabasesList)
	s.mux.HandleFunc("GET /partial/databases/databases", s.handleDatabasesListPartial)
```
Leave `GET /databases/{clusterID}` and its sub-routes untouched (ly-ae6.6 owns cluster detail).

- [ ] **Step 2:** Add the route-presence / empty-state test in a **new** file `internal/api/vertical_routes_test.go` (a new file avoids editing `server_test.go`'s import block). `setupEmptyFleet` runs migrations only (no seed data), so it exercises the empty states: Clusters shows the `vempty` panel, Nodes/Databases render the header with zero groups. It reuses `newVerticalFleet`/`getBody` from `internal/api/vertical_helpers_test.go` (Task 2):
```go
package api_test

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// setupEmptyFleet migrates both stores and starts the server with no seeded
// clusters — the zero-rows path (and the servers.database_name dark-screen path
// for Databases; see Task 4 backend note).
func setupEmptyFleet(t *testing.T) *httptest.Server {
	t.Helper()
	srv, _, _, _, _ := newVerticalFleet(t)
	return srv
}

func TestRoutes_DatabaseVerticalScreens(t *testing.T) {
	srv := setupEmptyFleet(t)
	cases := []struct{ path, wantID string }{
		{"/databases", `id="clusters-screen"`},
		{"/databases/nodes", `id="nodes-screen"`},
		{"/databases/databases", `id="databases-screen"`},
	}
	for _, c := range cases {
		html := getBody(t, srv.URL + c.path)
		if !strings.Contains(html, c.wantID) {
			t.Errorf("GET %s: missing %q", c.path, c.wantID)
		}
	}
}
```

- [ ] **Step 3:** Write the short contract doc `docs/design/integration-db-vertical-lists.md` capturing the route table, params, and `data-scope` encoding above (so ly-ae6.2/ly-ae6.3 workers can wire the nav + scope picker without re-reading this plan).

- [ ] **Step 4:** Full verification:
```
make templ
git diff --exit-code -- 'web/*_templ.go'   # templ output committed & in sync
go build ./...
go vet ./internal/api/... ./internal/fleetview/... ./web/...
go test ./web/ ./internal/api/ ./internal/fleetview/
```
Expected: build clean, `git diff --exit-code` returns 0 (no uncommitted generated drift), all tests PASS (integration tests skip cleanly if docker is unavailable).

- [ ] **Step 5:** Commit:
```
git add internal/api/server.go internal/api/vertical_routes_test.go docs/design/integration-db-vertical-lists.md
git commit -m "feat(ui): wire Database-vertical list routes + scope-shell integration contract (ly-ae6.5)"
```

- [ ] **Step 6:** Update the bead labels for hand-off (per the Feature Work Lifecycle in CLAUDE.md — do NOT commit/push beyond the above unless asked):
```
bd label remove ly-ae6.5 needs-plan && bd label add ly-ae6.5 ready-impl
bd note ly-ae6.5 "Plan at docs/superpowers/plans/ui-db-vertical-lists.md. Depends on scope shell ly-ae6.2/ly-ae6.3 (integration contract in docs/design/integration-db-vertical-lists.md). Backend gaps flagged: host metrics + provider/blind-spot (ly-7ck.3); per-db size/cache/tables (ly-xqf.6/ly-xqf.7). Version chip derives from pg_settings server_version_num (server_version is NOT allowlisted). Databases screen shows rows only once servers.database_name is populated at enrollment (ly-8b0.8) — dark until then."
```

---

## Self-Review

### Spec-coverage checklist (every COMPARISON gap + bead criterion → task)

**Bead ly-ae6.5 description:** Clusters (sortable, engine/version/provider chips) → **Task 2**; Nodes (cluster groups, DB→NODE→CLUSTER rollup, data-source lines, per-node versions, dimmed BLIND SPOT for RDS Multi-AZ/Azure HA standby) → **Task 3**; Databases (cluster-qualified identity) → **Task 4**.

**COMPARISON `Database › Clusters list` (9 gaps):**
- design-system styling (mono/tokens/2px/1px/dark-light) → T1 (verticals.css) + T2
- flat sortable list, SORT HEALTH/NAME, no Cards/List toggle → T2 (`ClustersView.Sort`, `sortRowsByKey`)
- engine icon chip + `#eng-pg` sprite → T1 (`EngineSprite`) + T2 (`eng-mark`)
- version chip → T1/T2 (`Version` via `settingsForServer` reading **`server_version_num`** — the field the collector actually allowlists — then `formatServerVersion` → `major.minor`; NOT the un-allowlisted `server_version` string; the fleetview roll-up test seeds the real `server_version_num` field so a green test means the chip renders in production)
- faint meta + `Clusters` LIVE header/strap → T2 (`.vlist-strap`, `.cl-meta`)
- health rollup line (CRIT/WARN/INFO colored) → T1 (`rollupOpenChecks`, `HealthLine`) + T2
- per-row `⌖`, rows not clickable → T2 (`ScopeHref`/`ScopeTarget`), contract T5
- `+ ADD CLUSTER` wizard → T2 stub link `/onboarding?vertical=database` (wizard is ly-ae6.12; documented)
- sparkline removed → T2 (removed; `sparklinePoints` retained only for `overview.go`; test asserts no `<polyline`)

**COMPARISON `Database › Nodes` (12 gaps):**
- no Nodes screen (route/templ/handler/nav) → T3 + T5
- pagination 3 groups/page → T3 (`nodeGroupsPerPage`)
- search cluster/node/provider/engine → T3 (`nodeGroupMatches`)
- SORT HEALTH/NAME issues-first → T3 (SevRank sort)
- per-cluster group header (engine chip, version, provider badge+tooltip, DB→NODE→CLUSTER rollup, `⌖`) → T3 (`NodeGroupVM`, `nodeRollup`)
- Instance model lacks engine/version/provider/data-source/max_connections/health/blind-spot → T3 (all fields on `NodeRow`/`NodeRowVM`; version+max_connections real via pg_settings; role real; provider/source/blind-spot backend-gap documented ly-7ck.3)
- no host metrics CPU/MEM/DISK/IOWAIT → T3 render `—` + backend-gap note
- no provider/CloudWatch/Azure metadata → T3 provider chip omitted when empty; ly-7ck.3
- no dimmed BLIND SPOT rendering → T3 (`BlindSpot`/`node-name--blind`/`◌ BLIND SPOT`; unit-tested with synthetic row)
- no conns-vs-max bar → T3 (`conns-bar`/`conns-fill`, `MaxConns`)
- design system not adopted → T1/T3 (tokens)
- (per-node version differences on rolling upgrades) → T3 (`NodeRow.Version` per instance)

**COMPARISON `Database › Databases` (9 gaps):**
- shows clusters not databases / no per-database row → T4 (`DatabaseEntry` from `ServerStream.DatabaseName`; **row-existence depends on `servers.database_name` being populated at enrollment — ly-8b0.8; screen is dark until then**, documented in Task 4 backend note)
- grouping by cluster w/ header → T4 (`DatabaseGroupVM`)
- cluster-qualified identity sub-line → T4 (`Qual = clusterName/dbName`)
- SIZE/QPS/CONNS/CACHE/TABLES columns → T4 (all fields; QPS/CONNS real; SIZE/CACHE/TABLES `—` + ly-xqf.6/ly-xqf.7 note)
- scope `⌖` per row/group, no clickable links → T4 + T5
- info strip identity=cluster+name → T4 (`.info-strip`)
- design system → T1/T4 (tokens)
- global shell absent → out-of-scope here; T5 integration contract (ly-ae6.2/ae6.3)
- no per-database view-model/store query → T4 (`ListDatabaseGroups` new; minimal, reuses existing stores)

**COMPARISON `Provider awareness & blind spots` (8 gaps, backend bead ly-7ck.3):**
- provider identity in data model → backend gap (ly-7ck.3); VM fields present T3
- provider chip (AWS/AZ/PS) + ⓘ tooltip → T3 (`grp-provider`, `ProviderNote`)
- blind-spot node state dimmed `◌` + "provider metrics only" source → T3 (mechanism + unit test)
- per-node data-source line → T3 (`Source`/`nodeSource`)
- observable replica vs unobservable standby → T3 (`role-standby`, `BlindSpot`)
- Provider Setup admin page → out-of-scope (ly-ae6.12)
- add-component onboarding wizard → out-of-scope (ly-ae6.12; `+ ADD` link stub)
- no dedicated Nodes view → T3 (core deliverable)

**Explicitly out of scope (with owner):** top bar + scope state + SCOPE picker (ly-ae6.2); sidebar nav rebuild (ly-ae6.3); node/database Overview landings (ly-ae6.6); onboarding/add wizard + Provider Setup (ly-ae6.12); cluster detail screen (ly-ae6.6).

### Placeholder scan
No `TBD`, no "add error handling", no "similar to Task N", no code step without real code. Every test in the plan ships a full code block (helpers included): the shared scaffolding `newDBPool`/`newVerticalFleet`/`getBody` (Task 2 Step 5), `setupClusters` (Task 2 Step 6), `setupNodes` (Task 3 Step 6), `setupDatabasesScreen` (Task 4 Step 6), `setupEmptyFleet` (Task 5 Step 2), and the fleetview `TestListNodeGroups`/`TestListDatabaseGroups`/`TestFormatServerVersion` cases — none are left as prose. Every referenced type/function is either defined in a Task above (`ClusterListRow`, `NodeGroupVM` incl. `SevRank`, `HealthLine`, `ListNodeGroups`, `nodeRollup`, `groupThousands`, `classIf`, `formatServerVersion`, …) or verified to exist in the repo: `fleetview.ListClusterSummaries`, `store.Config.{ListClusters,ListInstances,ListServerStreams,ServerIDsForCluster,ServerIDsForInstance,CreateCluster,CreateInstance,AssignServerToInstance}`, `store.Stats.{ActivitySummaryForServers,QPSBucketsForServers,LatestChecksResults,LatestSettings,WriteChecksResults,WriteSettings,WriteActivityBuckets,WriteQueryStats}`, `store.{Cluster,Instance,ServerStream,ChecksResultRow,SettingRow,QPSBucket,ActivityBucket,QueryStat}`, `web.Layout`, `templ.SafeURL`. The plan deliberately does **not** use `templ.KV` (zero usages in `web/*.templ`); conditional classes go through the `classIf` string helper instead. The only intentional runtime stub is `+ ADD CLUSTER` → `/onboarding` (ly-ae6.12), documented in Task 5.

### Type-consistency check
- Severity vocab is consistent: `store.ChecksResultRow.Severity ∈ {"critical","warning","info"}`, `.Status ∈ {"firing","ok"}` (verified in `internal/checks/checks.go`), matched exactly in `rollupOpenChecks`.
- `HealthLine`/`SevRank` take `(crit, warn, info int)` and are called with `sum.CritOpen/WarnOpen/InfoOpen` (int) everywhere — consistent. HEALTH sort reads `SevRank` **off the sorted VM** in all three screens (`ClusterListRow.SevRank`, `DatabaseGroupVM.SevRank`, and — fixed in this revision — `NodeGroupVM.SevRank`); no sort comparator reaches into a separate, unsorted `groups` slice.
- Version: `settingsForServer` reads `server_version_num` (the allowlisted integer GUC) and `formatServerVersion` renders `major.minor`; verified against `internal/collector/settings_reader.go` (allowlist contains `server_version_num`, not `server_version`) and `internal/caps/probes.go` (PG 12+ baseline → `major*10000+minor` encoding). `max_connections` parses with `strconv.ParseInt(...,10,64)` → `int64`, matching `NodeRow.MaxConns int64` and the `strconv.FormatInt` render path.
- `ScopeHref` is always passed through `templ.SafeURL`; `data-scope` is a plain string attribute — no `templ.SafeURL` misuse.
- QPS is computed as `float64` (last bucket calls / 3600) then rendered via `groupThousands(int64(qps+0.5))` (Clusters, Databases) — consistent integer render, comma-grouped, matching the design's `1,284`.
- Dynamic `class={ ... }` uses ONLY plain string concatenation — the pattern already generated in this repo (`web/overview.templ:144`, `web/cluster_views.templ:82` do `class={ "badge-role badge-role--" + role }`). Multi-class values are `"base " + x`; conditional classes go through the `classIf(base, extra, on bool) string` helper. `templ.KV` is deliberately **avoided** (zero usages in `web/*.templ` — verified). The single dynamic `style` (`style={ "width:" + n.ConnsPct }`) is verified against the vendored templ v0.3.1020 runtime: the generator emits `templruntime.SanitizeStyleAttributeValues`, which for a string calls `safehtml.SanitizeStyleValue` — `width:44%` passes through unchanged and gains a trailing `;`, rendering `style="width:44%;"`. Do NOT substitute a `--w` custom property: `safehtml.SanitizeCSSProperty` drops unknown property names.
- Route literals outrank the `/databases/{clusterID}` wildcard in Go 1.22 ServeMux, so `/databases/nodes` and `/databases/databases` register without conflict (no panic); verified against the existing `server.go` wildcard usage.
