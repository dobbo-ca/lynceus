# Fleet Dashboard Implementation Plan

> For agentic workers: execute this with the **superpowers:subagent-driven-development** skill — dispatch each Task to a fresh subagent, TDD-style (failing test → run → implement → run → commit), reviewing between tasks.

**Bead:** ly-ae6.4 — "UI: Fleet dashboard — triage strips, needs-attention, problem-only cards, healthy/unhealthy" (P1).
Depends on **ly-ae6.2** (top bar + scope model) and **ly-ae6.3** (scope-driven nav). Those build the shell; this bead builds the fleet-scope OVERVIEW screen that lives inside it. Until the shell lands, the fleet screen renders inside the existing `web.Layout`; the scope integration is via a documented URL contract (see Global Constraints → Scope integration contract) that the shell will consume without changes here.

**Goal:** Replace the placeholder root page with a design-parity Fleet dashboard — engine-neutral stat strips, a computed cross-engine Needs-Attention list whose rows deep-link to the offending resource's explanation, problem-only cluster cards (healthy ones collapsed behind a footer link), and an all-clear healthy state — driven by a new `fleetview` severity/health rollup over checks + insights.

**Architecture:** `internal/fleetview` gains `BuildFleetView(...)`, a cross-signal aggregator that rolls per-cluster metrics (reusing `ListClusterSummaries`) together with open-check and insight severity counts and a fleet-wide Needs-Attention list. `internal/api/fleet.go` maps that domain view-model onto `web.FleetView` (formatting ages, sev→CSS-class, and building scope-aware deep-link hrefs) and serves it at `GET /` (plus `GET /partial/fleet` for HTMX refresh). `web/fleet.templ` renders the screen with design tokens; `web/sprites.templ` supplies the inline engine-icon sprite.

**Tech Stack:** Go 1.x, `github.com/a-h/templ` v0.3.1020 (SSR), HTMX (self-hosted), `net/http` (`ServeMux`), pgx v5 stores, testcontainers-go (real Postgres for `fleetview`/store tests), `httptest` + rendered-HTML assertions for handler/view tests.

## Global Constraints

Copy these into every task's working context. They are non-negotiable and enforced by tests/CI:

- **Privacy (T1 only).** Everything the fleet dashboard renders is T1 (normalized, literal-free): counts, severities, structural check/insight ids, normalized detail strings, cluster/instance names, fingerprints. Never add a raw-sample/raw-text field to any fleet path. `internal/store` reads already filter `data_tier = 1`. Handler tests assert no literal leaks in rendered HTML.
- **No external hosts.** All CSS/JS/fonts/SVG are self-hosted under `web/static/` and referenced at `/static/...`. Never add a CDN/font/script host. The contract test `web.TestLayout_NoExternalHosts` must stay green; new templ output must not introduce external hosts either.
- **Tokens, not legacy.** This is a NEW screen: style it with design tokens (`var(--surface)`, `var(--line)`, `var(--critT)`, `var(--acc2)`, `var(--font-mono)`, …) defined in `web/static/css/tokens.css`. Do NOT use `web/static/css/legacy.css` component classes. Dynamic colors go through the small fleet utility classes defined in Task 3's inline `<style>` (templ sanitizes *dynamic* `style=` expressions, so never put a Go expression in a `style` attribute — use a class instead; literal `style="…var(--x)…"` attributes are emitted verbatim and are fine).
- **templ regen.** Any `.templ` edit requires `make templ` to regenerate the committed `*_templ.go`. CI checks the generated files are in sync — commit them together with the `.templ` source.
- **testcontainers, no DB mocks.** `internal/fleetview` and `internal/store` tests hit real Postgres via testcontainers (`tcpostgres.Run("postgres:16", …)`, `testpg.ReadyWait()`), spinning up *two* databases (config + stats) as production does. Never mock the database.
- **Scope integration contract (ly-ae6.2 / ly-ae6.3).** This screen is the fleet-scope landing. It does not build the shell; it depends on it and integrates via:
  - **Landing route:** the fleet dashboard is served at `GET /`. ly-ae6.3's fleet-scope nav must point its `OVERVIEW ▸ Fleet` entry at `/`.
  - **Deep-link scheme:** rows and cards carry a `?scope=<serverID|clusterID>` query param plus the target screen path (e.g. `/checks?scope=<serverID>&check=<checkID>`, `/vacuum-advisor?scope=<serverID>`, `/databases/<clusterID>/insights?scope=<serverID>&fp=<fingerprint>`, `/databases/<clusterID>?scope=<clusterID>`). ly-ae6.2's scope shell reads `?scope=` to set active scope; today's handlers ignore the unknown param, so links degrade gracefully to navigation. Do NOT implement scope-state consumption here — only emit the contract.
  - **Time-range param.** The fleet screen *consumes* a shared `?range=` param — canonical values `15m|1h|24h|7d|30d` (default `24h`), matching ly-ae6.2's top-bar segmented control (`15M/1H/24H/7D/30D`). `fetchFleet.fleetRange` maps it to the `since` window and the header `RANGE` label, and it is preserved (alongside `?sort=`) on the auto-poll and SORT-toggle URLs so a range/sort choice survives the 30s refresh. ly-ae6.2 owns the control *widget* that sets the param; this screen owns *consuming* it — the two agree on the value set above. Until the shell lands, `?range=` is reachable only by direct URL, but the header already follows it.

## Backend data notes (read before Task 2)

- **Severity/health rollup is genuinely missing** and is the cross-signal concern this bead owns — it is built in `internal/fleetview` (Task 2), not re-planned elsewhere. It reuses existing store reads: `LatestChecksResults`, `TopInsightsForServers`, and `ListClusterSummaries`.
- **Version / provider are not in the store today.** `store.Cluster` has only `ID, Name, CreatedAt`. The card view-model carries `Version`, `Provider`, `ProviderName` fields that render conditionally (chip hidden when empty), so the card *anatomy* reaches parity now and lights up when data arrives. Provider ingestion is tracked by **ly-99s.5** (cloud/managed ingestion — PlanetScale/AWS/Azure); cluster version is derivable from `pg_settings` `server_version` (collected under ly-u4t.24) but wiring it per-cluster is out of scope here — leave `Version`/`Provider` empty and note the dependency. Do not add a store column in this bead.
- **P95 latency is unavailable.** The stats store exposes summed calls + `SUM(total_time_ms)/SUM(calls)` (mean), not a percentile. The card metric uses mean latency under the label `LATENCY MS` (honest substitute for the design's `P95 MS`); a true P95 needs a percentile aggregation in the stats store — out of scope, noted in Self-Review.
- **Engine is Postgres-only today; stat strips are engine-neutral and gated per engine.** All clusters render as `POSTGRESQL` + `#eng-pg`. `fetchFleet` builds `Row1`/`Row2` as slices gated by two **default-off** constants, `enableSearch`/`enableCache` (Postgres-only today; the real per-engine enable source is a fleet-config concern wired *with* the verticals in ly-ae6.10 / ly-ae6.11). With both off, `Row1` is a single `DATABASES` cell and each `Row2` severity sub reads `"%d db"` (no `· 0 search · 0 cache` noise); flipping a gate on appends its `SEARCH`/`CACHE` cell and `· N search`/`· N cache` sub. The Search/Cache **cards** are out of scope (ly-ae6.10 / ly-ae6.11); only the gate + neutral cells live here. This is the whole of the "engine gate" — no dead `EnableSearch`/`EnableCache` struct field is added to `web.FleetView`/`fleetview.FleetView` (that would be speculative config for a vertical that does not exist yet; the gate lives where it is used, in `fetchFleet`).
- **Info advisories are surfaced but do not degrade health (explicit design decision).** The fleet is "all-clear" (`Healthy`) only when `len(Attention)==0` — i.e. no open check/insight of *any* band. Info-band items DO appear in Needs-Attention and DO make the fleet non-all-clear, but they do NOT degrade a cluster's health: `deriveHealth` returns `HEALTHY` for an info-only cluster, so its card is excluded by the problem-only filter (`Crit>0||Warn>0`) and it is counted in "N HEALTHY DB CLUSTERS NOT SHOWN". Consequence: an info-only cluster's Needs-Attention row can deep-link to a cluster the same screen reports as healthy-and-hidden. This is intentional — info is an advisory, not a health regression; the row exists so the advisory is discoverable and its deep-link still resolves. The Needs-Attention header sums only `n CRIT / n WARN` (matching the prototype), so info rows are listed but not counted there.

---

### Task 1: Engine-icon sprite partial

Adds the inline SVG symbol set (`#eng-pg`, `#eng-os`, `#eng-vk`) as a reusable templ component so cards can reference engine glyphs via `<use href="#eng-pg">`. Original glyphs in `currentColor` (theme-aware) — no external/licensed logos.

**Files**
- Create: `web/sprites.templ`
- Regenerated (commit): `web/sprites_templ.go`
- Create test: `web/sprites_test.go`

**Interfaces**
- Produces: `templ EngineSprites()` — renders one hidden `<svg>` containing three `<symbol>` elements. No parameters.

Steps:

- [ ] **Step 1: Write the failing test.** Create `web/sprites_test.go`:
```go
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
```

- [ ] **Step 2: Run it — expect FAIL (undefined: EngineSprites).**
```
go test ./web/ -run TestEngineSprites
```
Expected: compile error `undefined: EngineSprites`.

- [ ] **Step 3: Implement `web/sprites.templ`** (glyphs copied verbatim from `docs/design/Lynceus.dc.html` lines 51–66):
```go
package web

// EngineSprites renders the hidden inline SVG symbol set referenced by engine
// icons via <use href="#eng-pg|#eng-os|#eng-vk">. Original glyphs stroked in
// currentColor so they follow the active theme's accent. Render once per page,
// outside any HTMX swap target, so <use> refs survive body swaps.
templ EngineSprites() {
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

- [ ] **Step 4: Regenerate + run — expect PASS.**
```
make templ
go test ./web/ -run TestEngineSprites
```
Expected: `ok`. Also run `go build ./...` to confirm the generated file compiles.

- [ ] **Step 5: Commit.**
```
git add web/sprites.templ web/sprites_templ.go web/sprites_test.go
git commit -m "feat(web): engine-icon sprite partial (#eng-pg/os/vk) for fleet cards (ly-ae6.4)"
```

---

### Task 2: fleetview severity/health rollup + Needs-Attention assembly

Adds the cross-signal aggregator that produces everything the dashboard needs beyond raw metrics: per-cluster CRIT/WARN/INFO counts + derived health, a fleet-wide Needs-Attention list (checks + insights, sorted crit→warn→info then newest), fleet totals, and the row-1 count subs.

**Files**
- Create: `internal/fleetview/dashboard.go`
- Create test: `internal/fleetview/dashboard_test.go`

**Interfaces**

Consumes (all exist — verified in `internal/store`):
- `store.Config.ListClusters(ctx) ([]store.Cluster, error)` — `Cluster{ID, Name string; CreatedAt time.Time}`.
- `store.Config.ListInstances(ctx, clusterID string) ([]store.Instance, error)` — `Instance{ID, ClusterID, Name, Role string; …}`.
- `store.Config.ListServerStreams(ctx, instanceID string) ([]store.ServerStream, error)` — `ServerStream{ServerID, Name, InstanceID, DatabaseName string; …}`.
- `store.Config.ServerIDsForCluster(ctx, clusterID string) ([]string, error)`.
- `store.Stats.LatestChecksResults(ctx, serverID string, since, until time.Time) ([]store.ChecksResultRow, error)` — `ChecksResultRow{ServerID string; EvaluatedAt time.Time; CheckID, Category, Severity, Status, Object, Detail string; Muted bool; DataTier int16}`. Severity ∈ {`info`,`warning`,`critical`}; Status ∈ {`ok`,`firing`}.
- `store.Stats.TopInsightsForServers(ctx, serverIDs []string, since, until time.Time, limit int) ([]store.InsightRow, error)` — `InsightRow{ServerID string; CapturedAt time.Time; Kind, Severity, Fingerprint, Relation, NodePath string; …; Detail string}`. Severity ∈ {`low`,`medium`,`high`}.
- `fleetview.ListClusterSummaries(ctx, cfg store.Config, stats store.Stats, since, until time.Time) ([]ClusterSummary, error)` — reused for metrics (`ClusterSummary{Cluster store.Cluster; InstanceCount, StreamCount int; QPSBuckets []store.QPSBucket; AvgLatencyMs float64; ActiveConns int64; TopWait string}`).

Produces:
```go
// Sev is the normalized 3-band severity used across the dashboard.
type Sev string

const (
	SevCrit Sev = "crit"
	SevWarn Sev = "warn"
	SevInfo Sev = "info"
)

// AttentionItem is one open check or insight surfaced in the fleet
// Needs-Attention list. All fields are T1 (structural id, counts, normalized
// detail). ServerID/ClusterID/Fingerprint feed the deep-link the handler builds.
type AttentionItem struct {
	Kind        string // "check" | "insight"
	ID          string // check_id, or "insight: <kind>"
	Detail      string
	Sev         Sev
	ServerID    string
	ServerName  string // node (instance) display name; falls back to ServerID
	ClusterID   string
	Category    string // check category (e.g. "vacuum"); "" for insights
	CheckID     string // "" for insights
	Fingerprint string // insight fingerprint; "" for checks
	At          time.Time
}

// FleetCluster is one cluster's dashboard roll-up: metrics + severity + health.
type FleetCluster struct {
	ClusterID    string
	Name         string
	Version      string // "" until a version source is wired (see backend notes)
	Provider     string // "" | "AWS" | "AZ" (ly-99s.5); chip hidden when ""
	ProviderName string // tooltip text, e.g. "AWS RDS · Multi-AZ"
	Engine       string // "POSTGRESQL"
	EngineIcon   string // "eng-pg"
	Health       string // "DEGRADED" | "WARNING" | "HEALTHY"
	HealthSev    Sev    // drives health-text color
	Crit         int
	Warn         int
	Info         int
	QPS          float64 // latest hourly bucket calls / 3600
	LatencyMs    float64 // mean latency (P95 unavailable — see backend notes)
	ActiveConns  int64
	TopWait      string
}

// FleetView is the whole dashboard domain model (pre-presentation).
type FleetView struct {
	Clusters      []FleetCluster  // all clusters (unfiltered, unsorted); presentation filters/sorts
	Attention     []AttentionItem // sorted crit→warn→info, then newest-first
	OpenCrit      int
	OpenWarn      int
	OpenInfo      int
	ClusterCount  int
	NodeCount     int // instances across the fleet
	DatabaseCount int // server streams across the fleet
	Healthy       bool // true iff Attention is empty (no open checks or insights anywhere)
}

func BuildFleetView(ctx context.Context, cfg store.Config, stats store.Stats, since, until time.Time) (FleetView, error)
```

Steps:

- [ ] **Step 1: Write the failing test.** Create `internal/fleetview/dashboard_test.go` (reuses the package's existing `newStores` helper from `summary_test.go`):
```go
package fleetview_test

import (
	"context"
	"testing"
	"time"

	"github.com/dobbo-ca/lynceus/internal/fleetview"
	"github.com/dobbo-ca/lynceus/internal/store"
)

func TestBuildFleetView_rollsUpSeverityHealthAndAttention(t *testing.T) {
	cfg, stats, configPool := newStores(t)
	ctx := context.Background()

	// two servers under one cluster/instance
	for _, id := range []string{"fv-srv-a", "fv-srv-b"} {
		if _, err := configPool.Exec(ctx, `INSERT INTO servers (id, name) VALUES ($1, $1)`, id); err != nil {
			t.Fatalf("seed server %s: %v", id, err)
		}
	}
	cl, err := cfg.CreateCluster(ctx, "orders-prod")
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}
	inst, err := cfg.CreateInstance(ctx, cl.ID, "srv-orders-primary")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	for _, id := range []string{"fv-srv-a", "fv-srv-b"} {
		if err := cfg.AssignServerToInstance(ctx, id, inst.ID); err != nil {
			t.Fatalf("assign %s: %v", id, err)
		}
	}

	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	// one firing critical check + one firing warning check (+ a muted one that must be ignored)
	if err := stats.WriteChecksResults(ctx, []store.ChecksResultRow{
		{ServerID: "fv-srv-a", EvaluatedAt: now.Add(-2 * time.Hour), CheckID: "settings.fsync",
			Category: "settings", Severity: "critical", Status: "firing", Object: "fsync",
			Detail: "fsync = off — a crash can lose committed transactions"},
		{ServerID: "fv-srv-a", EvaluatedAt: now.Add(-3 * time.Hour), CheckID: "vacuum.xmin_horizon",
			Category: "vacuum", Severity: "warning", Status: "firing", Object: "orders",
			Detail: "oldest xmin age 260M exceeds 200M"},
		{ServerID: "fv-srv-b", EvaluatedAt: now.Add(-1 * time.Hour), CheckID: "settings.work_mem",
			Category: "settings", Severity: "warning", Status: "firing", Object: "work_mem",
			Detail: "muted noise", Muted: true},
	}); err != nil {
		t.Fatalf("seed checks: %v", err)
	}
	// one high insight
	if err := stats.WriteInsights(ctx, []store.InsightRow{
		{ServerID: "fv-srv-a", CapturedAt: now.Add(-4 * time.Hour), Kind: "slow_scan", Severity: "high",
			Fingerprint: "f41b7d09", Relation: "orders_audit", NodePath: "Seq Scan(orders_audit)",
			RowsReturned: 1, RowsScanned: 1200000, Selectivity: 0.0000008,
			Detail: "Seq Scan on orders_audit reads 1.2M rows to return 1"},
	}); err != nil {
		t.Fatalf("seed insights: %v", err)
	}

	fv, err := fleetview.BuildFleetView(ctx, cfg, stats, now.Add(-24*time.Hour), now.Add(time.Minute))
	if err != nil {
		t.Fatalf("BuildFleetView: %v", err)
	}

	if fv.Healthy {
		t.Error("fleet has open issues; Healthy must be false")
	}
	// crit = critical check (1) + high insight (1); warn = warning check (1); muted excluded.
	if fv.OpenCrit != 2 || fv.OpenWarn != 1 || fv.OpenInfo != 0 {
		t.Fatalf("fleet totals = %d/%d/%d, want 2/1/0", fv.OpenCrit, fv.OpenWarn, fv.OpenInfo)
	}
	if fv.ClusterCount != 1 || fv.NodeCount != 1 || fv.DatabaseCount != 2 {
		t.Fatalf("counts clusters/nodes/dbs = %d/%d/%d, want 1/1/2", fv.ClusterCount, fv.NodeCount, fv.DatabaseCount)
	}
	if len(fv.Clusters) != 1 {
		t.Fatalf("clusters = %d, want 1", len(fv.Clusters))
	}
	c := fv.Clusters[0]
	if c.Crit != 2 || c.Warn != 1 || c.Info != 0 {
		t.Fatalf("cluster counts = %d/%d/%d, want 2/1/0", c.Crit, c.Warn, c.Info)
	}
	if c.Health != "DEGRADED" || c.HealthSev != fleetview.SevCrit {
		t.Fatalf("health = %q/%v, want DEGRADED/crit", c.Health, c.HealthSev)
	}
	if c.Engine != "POSTGRESQL" || c.EngineIcon != "eng-pg" {
		t.Fatalf("engine = %q/%q, want POSTGRESQL/eng-pg", c.Engine, c.EngineIcon)
	}
	// Needs-Attention: 3 items (muted excluded), sorted crit first then newest.
	if len(fv.Attention) != 3 {
		t.Fatalf("attention = %d, want 3", len(fv.Attention))
	}
	if fv.Attention[0].Sev != fleetview.SevCrit || fv.Attention[0].ID != "settings.fsync" {
		t.Fatalf("first attention = %+v, want crit settings.fsync", fv.Attention[0])
	}
	if fv.Attention[0].ServerName != "srv-orders-primary" {
		t.Fatalf("server name = %q, want srv-orders-primary", fv.Attention[0].ServerName)
	}
	// the insight is also crit-band and must carry its fingerprint + kind id.
	var sawInsight bool
	for _, a := range fv.Attention {
		if a.Kind == "insight" {
			sawInsight = true
			if a.ID != "insight: slow_scan" || a.Fingerprint != "f41b7d09" || a.ClusterID != cl.ID {
				t.Fatalf("insight item wrong: %+v", a)
			}
		}
	}
	if !sawInsight {
		t.Error("insight not surfaced in Needs-Attention")
	}
}

func TestBuildFleetView_healthyWhenNoOpenIssues(t *testing.T) {
	cfg, stats, configPool := newStores(t)
	ctx := context.Background()
	if _, err := configPool.Exec(ctx, `INSERT INTO servers (id, name) VALUES ('hv-srv','hv-srv')`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cl, err := cfg.CreateCluster(ctx, "quiet")
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}
	inst, _ := cfg.CreateInstance(ctx, cl.ID, "primary")
	if err := cfg.AssignServerToInstance(ctx, "hv-srv", inst.ID); err != nil {
		t.Fatalf("assign: %v", err)
	}
	now := time.Now().UTC()
	fv, err := fleetview.BuildFleetView(ctx, cfg, stats, now.Add(-24*time.Hour), now.Add(time.Minute))
	if err != nil {
		t.Fatalf("BuildFleetView: %v", err)
	}
	if !fv.Healthy || len(fv.Attention) != 0 {
		t.Fatalf("no open issues -> Healthy=true, empty attention; got healthy=%v n=%d", fv.Healthy, len(fv.Attention))
	}
	if fv.OpenCrit+fv.OpenWarn+fv.OpenInfo != 0 {
		t.Fatalf("healthy fleet must zero all severity totals")
	}
	if len(fv.Clusters) != 1 || fv.Clusters[0].Health != "HEALTHY" || fv.Clusters[0].HealthSev != fleetview.SevInfo {
		t.Fatalf("healthy cluster mislabeled: %+v", fv.Clusters)
	}
}
```

- [ ] **Step 2: Run it — expect FAIL.**
```
go test ./internal/fleetview/ -run TestBuildFleetView
```
Expected: compile error `undefined: fleetview.BuildFleetView` (and `SevCrit`/`SevInfo`).

- [ ] **Step 3: Implement `internal/fleetview/dashboard.go`:**
```go
package fleetview

import (
	"context"
	"sort"
	"time"

	"github.com/dobbo-ca/lynceus/internal/store"
)

// Sev is the normalized 3-band severity used across the dashboard.
type Sev string

const (
	SevCrit Sev = "crit"
	SevWarn Sev = "warn"
	SevInfo Sev = "info"
)

// normSev folds the checks vocabulary {info,warning,critical} and the insights
// vocabulary {low,medium,high} onto the three dashboard bands. Unknown -> info.
func normSev(raw string) Sev {
	switch raw {
	case "critical", "high":
		return SevCrit
	case "warning", "medium":
		return SevWarn
	default:
		return SevInfo
	}
}

func sevRank(s Sev) int {
	switch s {
	case SevCrit:
		return 0
	case SevWarn:
		return 1
	default:
		return 2
	}
}

// AttentionItem is one open check or insight surfaced in the fleet
// Needs-Attention list. All fields are T1.
type AttentionItem struct {
	Kind        string
	ID          string
	Detail      string
	Sev         Sev
	ServerID    string
	ServerName  string
	ClusterID   string
	Category    string
	CheckID     string
	Fingerprint string
	At          time.Time
}

// FleetCluster is one cluster's dashboard roll-up.
type FleetCluster struct {
	ClusterID    string
	Name         string
	Version      string
	Provider     string
	ProviderName string
	Engine       string
	EngineIcon   string
	Health       string
	HealthSev    Sev
	Crit         int
	Warn         int
	Info         int
	QPS          float64
	LatencyMs    float64
	ActiveConns  int64
	TopWait      string
}

// FleetView is the whole dashboard domain model (pre-presentation).
type FleetView struct {
	Clusters      []FleetCluster
	Attention     []AttentionItem
	OpenCrit      int
	OpenWarn      int
	OpenInfo      int
	ClusterCount  int
	NodeCount     int
	DatabaseCount int
	Healthy       bool
}

// BuildFleetView assembles the fleet dashboard: per-cluster metrics (reusing
// ListClusterSummaries) fused with open-check + insight severity roll-ups and a
// fleet-wide, severity-sorted Needs-Attention list. Only T1 data is read.
func BuildFleetView(
	ctx context.Context, cfg store.Config, stats store.Stats, since, until time.Time,
) (FleetView, error) {
	summaries, err := ListClusterSummaries(ctx, cfg, stats, since, until)
	if err != nil {
		return FleetView{}, err
	}

	var fv FleetView
	fv.ClusterCount = len(summaries)

	for i := range summaries {
		sum := &summaries[i]
		fv.NodeCount += sum.InstanceCount
		fv.DatabaseCount += sum.StreamCount

		serverIDs, err := cfg.ServerIDsForCluster(ctx, sum.Cluster.ID)
		if err != nil {
			return FleetView{}, err
		}
		instances, err := cfg.ListInstances(ctx, sum.Cluster.ID)
		if err != nil {
			return FleetView{}, err
		}
		// serverID -> node (instance) display name
		nameByServer := map[string]string{}
		for j := range instances {
			streams, err := cfg.ListServerStreams(ctx, instances[j].ID)
			if err != nil {
				return FleetView{}, err
			}
			for k := range streams {
				nameByServer[streams[k].ServerID] = instances[j].Name
			}
		}
		nameOf := func(sid string) string {
			if n := nameByServer[sid]; n != "" {
				return n
			}
			return sid
		}

		fc := FleetCluster{
			ClusterID:   sum.Cluster.ID,
			Name:        sum.Cluster.Name,
			Engine:      "POSTGRESQL",
			EngineIcon:  "eng-pg",
			LatencyMs:   sum.AvgLatencyMs,
			ActiveConns: sum.ActiveConns,
			TopWait:     sum.TopWait,
		}
		if n := len(sum.QPSBuckets); n > 0 {
			fc.QPS = float64(sum.QPSBuckets[n-1].Calls) / 3600.0
		}

		// open checks (firing, not muted)
		for _, sid := range serverIDs {
			checks, err := stats.LatestChecksResults(ctx, sid, since, until)
			if err != nil {
				return FleetView{}, err
			}
			for c := range checks {
				ch := &checks[c]
				if ch.Status != "firing" || ch.Muted {
					continue
				}
				sev := normSev(ch.Severity)
				bump(&fc, sev)
				fv.Attention = append(fv.Attention, AttentionItem{
					Kind: "check", ID: ch.CheckID, Detail: ch.Detail, Sev: sev,
					ServerID: sid, ServerName: nameOf(sid), ClusterID: sum.Cluster.ID,
					Category: ch.Category, CheckID: ch.CheckID, At: ch.EvaluatedAt,
				})
			}
		}
		// insights (already T1-filtered by the store)
		insights, err := stats.TopInsightsForServers(ctx, serverIDs, since, until, 50)
		if err != nil {
			return FleetView{}, err
		}
		for r := range insights {
			in := &insights[r]
			sev := normSev(in.Severity)
			bump(&fc, sev)
			fv.Attention = append(fv.Attention, AttentionItem{
				Kind: "insight", ID: "insight: " + in.Kind, Detail: in.Detail, Sev: sev,
				ServerID: in.ServerID, ServerName: nameOf(in.ServerID), ClusterID: sum.Cluster.ID,
				Fingerprint: in.Fingerprint, At: in.CapturedAt,
			})
		}

		fc.Health, fc.HealthSev = deriveHealth(fc.Crit, fc.Warn)
		fv.OpenCrit += fc.Crit
		fv.OpenWarn += fc.Warn
		fv.OpenInfo += fc.Info
		fv.Clusters = append(fv.Clusters, fc)
	}

	sort.SliceStable(fv.Attention, func(a, b int) bool {
		ra, rb := sevRank(fv.Attention[a].Sev), sevRank(fv.Attention[b].Sev)
		if ra != rb {
			return ra < rb
		}
		return fv.Attention[a].At.After(fv.Attention[b].At)
	})
	fv.Healthy = len(fv.Attention) == 0
	return fv, nil
}

func bump(fc *FleetCluster, sev Sev) {
	switch sev {
	case SevCrit:
		fc.Crit++
	case SevWarn:
		fc.Warn++
	default:
		fc.Info++
	}
}

// deriveHealth: any crit -> DEGRADED; else any warn -> WARNING; else HEALTHY.
// Info-only clusters are HEALTHY (info advisories don't degrade health).
func deriveHealth(crit, warn int) (string, Sev) {
	switch {
	case crit > 0:
		return "DEGRADED", SevCrit
	case warn > 0:
		return "WARNING", SevWarn
	default:
		return "HEALTHY", SevInfo
	}
}
```

- [ ] **Step 4: Run it — expect PASS.**
```
go test ./internal/fleetview/ -run TestBuildFleetView
go build ./...
```
Expected: `ok github.com/dobbo-ca/lynceus/internal/fleetview`.

- [ ] **Step 5: Commit.**
```
git add internal/fleetview/dashboard.go internal/fleetview/dashboard_test.go
git commit -m "feat(fleetview): fleet severity/health rollup + needs-attention assembly (ly-ae6.4)"
```

---

### Task 3: Fleet view-models + `fleet.templ` (stat strips, needs-attention, problem-only cards, healthy state)

Renders the screen with tokens: header (title + LIVE + engine-count summary + RANGE + SORT toggle), two stat strips, the Needs-Attention card OR all-clear panel, problem-only cluster cards, and the hidden-healthy footer link.

**Files**
- Create: `web/fleet.go` (view-model structs — pure `package web`, no store/fleetview import)
- Create: `web/fleet.templ`
- Regenerated (commit): `web/fleet_templ.go`
- Create test: `web/fleet_test.go`

**Interfaces**

Produces (view-models):
```go
// FleetStat is one stat-strip cell. ValueClass is a fleet color utility class
// (e.g. "fl-crit"); "" -> default text color.
type FleetStat struct {
	Label      string
	Value      string
	Sub        string
	ValueClass string
}

// FleetAttentionRow is one Needs-Attention row (already formatted + linked).
type FleetAttentionRow struct {
	SevClass string // "fl-sq-crit" | "fl-sq-warn" | "fl-sq-info"
	ID       string
	Detail   string
	Server   string
	Age      string // "2d" | "4h" | "18m"
	Href     string // scope-aware deep link
}

// FleetClusterCard is one problem-only cluster card (already formatted).
type FleetClusterCard struct {
	Name         string
	Version      string // "" -> version chip hidden
	Provider     string // "" -> provider chip hidden
	ProviderName string
	Engine       string
	EngineIcon   string
	Health       string
	HealthClass  string // "fl-crit" | "fl-warn" | "fl-ok"
	QPS          string
	LatencyMs    string
	Conns        string
	TopWait      string
	Crit         int
	Warn         int
	Info         int
	Href         string
}

// FleetLink is a labeled navigation link. Used by the all-clear panel
// (per-vertical "healthy" links) and the hidden-healthy footer.
type FleetLink struct {
	Label string
	Href  string
}

// FleetView is the presentation view-model for the whole dashboard.
type FleetView struct {
	Row1          []FleetStat // engine-neutral counts (DATABASES [+SEARCH +CACHE])
	Row2          []FleetStat // OPEN CRITICAL / WARN / INFO
	Attention     []FleetAttentionRow
	AttnCrit      int // header "n CRIT"
	AttnWarn      int // header "n WARN"
	Cards         []FleetClusterCard // problem-only, already sorted
	HealthyLinks  []FleetLink        // all-clear panel per-vertical links ("N DATABASE CLUSTERS HEALTHY →")
	HiddenLinks   []FleetLink        // footer "N HEALTHY DB CLUSTERS NOT SHOWN →" (+ per-engine ALL links when enabled)
	Healthy       bool               // all-clear: no open checks/insights of any band
	LoadError     bool               // BuildFleetView failed — render an error panel, never a false all-clear
	RangeLabel    string             // header label, e.g. "24H"
	Range         string             // canonical range param ("24h") echoed into poll/toggle URLs
	Sort          string             // "health" | "name" (echoed into toggle + poll URL)
	EngineSummary string             // e.g. "5 DB CLUSTERS / RANGE 24H"
}
```

Rendered by:
```go
templ FleetPage(v FleetView)  // full page: @Layout + sprites + inline <style> + #fleet-body + poll trigger
templ FleetBody(v FleetView)  // swappable body fragment (id="fleet-body")
```

Steps:

- [ ] **Step 1: Write the failing test.** Create `web/fleet_test.go`:
```go
package web

import (
	"context"
	"strings"
	"testing"
)

func renderFleetBody(t *testing.T, v FleetView) string {
	t.Helper()
	var sb strings.Builder
	if err := FleetBody(v).Render(context.Background(), &sb); err != nil {
		t.Fatalf("render: %v", err)
	}
	return sb.String()
}

func unhealthyFixture() FleetView {
	return FleetView{
		Row1: []FleetStat{{Label: "DATABASES", Value: "1", Sub: "clusters · 1 nodes · 2 databases"}},
		Row2: []FleetStat{
			{Label: "OPEN CRITICAL", Value: "2", Sub: "2 db", ValueClass: "fl-crit"},
			{Label: "OPEN WARN", Value: "1", Sub: "1 db", ValueClass: "fl-warn"},
			{Label: "OPEN INFO", Value: "0", Sub: "0 db", ValueClass: "fl-info"},
		},
		Attention: []FleetAttentionRow{
			{SevClass: "fl-sq-crit", ID: "settings.fsync", Detail: "fsync = off", Server: "srv-orders-primary", Age: "2h", Href: "/checks?scope=fv-srv-a&check=settings.fsync"},
		},
		AttnCrit: 2, AttnWarn: 1,
		Cards: []FleetClusterCard{
			{Name: "orders-prod", Version: "16.3", Engine: "POSTGRESQL", EngineIcon: "eng-pg",
				Health: "DEGRADED", HealthClass: "fl-crit", QPS: "1,284", LatencyMs: "18.2",
				Conns: "87", TopWait: "IO/DataFileRead", Crit: 2, Warn: 1, Info: 0, Href: "/databases/cl-1?scope=cl-1"},
		},
		HiddenLinks:   []FleetLink{{Label: "3 HEALTHY DB CLUSTERS NOT SHOWN →", Href: "/databases"}},
			RangeLabel:    "24H", Range: "24h", Sort: "health",
		EngineSummary: "1 DB CLUSTERS / RANGE 24H",
	}
}

func TestFleetBody_unhealthyRendersStripsAttentionAndCards(t *testing.T) {
	html := renderFleetBody(t, unhealthyFixture())
	for _, want := range []string{
		`id="fleet-body"`,
		"DATABASES", "OPEN CRITICAL", "OPEN WARN", "OPEN INFO",
		"NEEDS ATTENTION",
		"settings.fsync", "srv-orders-primary", "2h",
		`href="/checks?scope=fv-srv-a&amp;check=settings.fsync"`,
		"orders-prod", "v16.3", "[DEGRADED]", "POSTGRESQL", "#eng-pg",
		"2 CRIT", "1 WARN", "0 INFO",
		"3 HEALTHY DB CLUSTERS NOT SHOWN",
			`hx-get="/partial/fleet?sort=health&amp;range=24h"`, // auto-poll preserves sort+range
			`hx-get="/partial/fleet?sort=name&amp;range=24h"`,   // SORT toggle flips mode, keeps range
		"var(--", // tokens, not legacy
	} {
		if !strings.Contains(html, want) {
			t.Errorf("unhealthy fleet body missing %q", want)
		}
	}
	// explicitly-removed noise must NOT appear
	for _, forbidden := range []string{"<polyline", "components", "class=\"db-card\""} {
		if strings.Contains(html, forbidden) {
			t.Errorf("fleet card must not contain removed noise %q", forbidden)
		}
	}
}

func TestFleetBody_healthyShowsAllClearNoCards(t *testing.T) {
	v := FleetView{
		Row1: []FleetStat{{Label: "DATABASES", Value: "1", Sub: "clusters · 1 nodes · 1 databases"}},
		Row2: []FleetStat{
			{Label: "OPEN CRITICAL", Value: "0", Sub: "all clear", ValueClass: "fl-acc2"},
			{Label: "OPEN WARN", Value: "0", Sub: "no checks firing"},
			{Label: "OPEN INFO", Value: "0", Sub: "no advisories"},
		},
		Healthy: true, RangeLabel: "24H", Range: "24h", Sort: "health", EngineSummary: "1 DB CLUSTERS / RANGE 24H",
			HealthyLinks: []FleetLink{{Label: "1 DATABASE CLUSTER HEALTHY →", Href: "/databases"}},
	}
	html := renderFleetBody(t, v)
	if !strings.Contains(html, "ALL CLEAR") || !strings.Contains(html, "NO OPEN CHECKS OR INSIGHTS ACROSS ANY ENGINE") {
		t.Error("healthy fleet must show the all-clear panel")
	}
	if !strings.Contains(html, "DATABASE CLUSTER HEALTHY") || !strings.Contains(html, `href="/databases"`) {
		t.Error("all-clear panel must carry the per-vertical DB healthy link")
	}
	if strings.Contains(html, "NEEDS ATTENTION") {
		t.Error("healthy fleet must not show the Needs-Attention card")
	}
}
```

- [ ] **Step 2: Run it — expect FAIL.**
```
go test ./web/ -run TestFleetBody
```
Expected: compile errors (`undefined: FleetView`, `FleetBody`, field types).

- [ ] **Step 3a: Implement `web/fleet.go`** (structs only — the exact definitions from the Interfaces block above, verbatim, in `package web`).

- [ ] **Step 3b: Implement `web/fleet.templ`.** Structural styling is literal inline (emitted verbatim); dynamic colors go through the fleet utility classes defined in the page-level `<style>`. `@EngineSprites()` and the `<style>` sit in `FleetPage` (outside `#fleet-body`) so `<use>` refs and classes survive HTMX body swaps.
```go
package web

import "fmt"

// FleetPage renders the full fleet dashboard (fleet-scope OVERVIEW landing).
// Sprites + component styles live outside #fleet-body so HTMX body swaps keep
// their <use> refs and classes. The 30s poll trigger deliberately does NOT live
// here — it sits inside #fleet-body (see FleetBody) so each swap re-emits it
// with the live ?sort/?range and a user's SORT/range choice survives the poll.
templ FleetPage(v FleetView) {
	@Layout("Lynceus — Fleet", "fleet dashboard") {
		@EngineSprites()
		@fleetStyles()
		@FleetBody(v)
	}
}

// fleetStyles is the fleet-only component stylesheet: color utilities for the
// dynamic severity/health bits (templ sanitizes dynamic style= expressions, so
// dynamic colors must be class-driven). Token-based; no legacy classes.
templ fleetStyles() {
	<style>
		.fl-crit{color:var(--critT)} .fl-warn{color:var(--warnT)} .fl-info{color:var(--infoT)}
		.fl-ok{color:var(--ok)} .fl-acc2{color:var(--acc2)}
		.fl-sq-crit{background:var(--crit)} .fl-sq-warn{background:var(--warn)} .fl-sq-info{background:var(--info)}
		.fl-attn-row:hover{background:var(--raised)}
		.fl-card:hover{border-color:var(--dim)}
	</style>
}

// FleetBody is the swappable dashboard fragment.
templ FleetBody(v FleetView) {
	<div id="fleet-body" style="padding:18px 22px 32px; display:flex; flex-direction:column; gap:14px; max-width:1400px;">
		<!-- auto-refresh (30s): rendered INSIDE #fleet-body so every swap re-emits it with the live sort+range; a SORT/range choice survives the poll -->
		<p hx-get={ fleetPartialURL(v.Sort, v.Range) } hx-trigger="every 30s" hx-target="#fleet-body" hx-swap="outerHTML" style="display:none"></p>
		<!-- header -->
		<div style="display:flex; align-items:baseline; gap:12px;">
			<span style="font-size:17px; font-weight:600;">Fleet</span>
			<span style="font-family:var(--font-mono); font-size:10px; color:var(--acc); border:1px solid var(--acc); padding:0 5px; border-radius:1px;">LIVE</span>
			<span style="font-family:var(--font-mono); font-size:10.5px; color:var(--faint); letter-spacing:.08em;">{ v.EngineSummary }</span>
			<span style="flex:1;"></span>
			<a href={ templ.SafeURL("/?sort=" + fleetOtherSort(v.Sort) + "&range=" + v.Range) } hx-get={ fleetPartialURL(fleetOtherSort(v.Sort), v.Range) } hx-target="#fleet-body" hx-swap="outerHTML" style="font-family:var(--font-mono); font-size:10.5px; color:var(--dim); border:1px solid var(--line); padding:4px 9px; border-radius:2px; text-decoration:none;">SORT: { fleetSortLabel(v.Sort) } ⇅</a>
		</div>
		if !v.LoadError {
		<!-- stat strip row 1 -->
		<div style="display:flex; border:1px solid var(--line); border-radius:2px; background:var(--surface);">
			for _, s := range v.Row1 {
				@fleetStatCell(s)
			}
		</div>
		<!-- stat strip row 2 -->
		<div style="display:flex; border:1px solid var(--line); border-radius:2px; background:var(--surface);">
			for _, s := range v.Row2 {
				@fleetStatCell(s)
			}
		</div>
		}
		<!-- load-error / needs-attention / all-clear -->
		if v.LoadError {
			<div style="border:1px solid var(--crit); border-radius:2px; background:var(--critbg); padding:20px 22px; display:flex; flex-direction:column; gap:6px;">
				<span class="fl-crit" style="font-family:var(--font-mono); font-size:11px; letter-spacing:.08em;">FLEET DATA UNAVAILABLE — RETRYING…</span>
				<span style="font-size:11.5px; color:var(--mut);">Could not load fleet checks and insights. This view auto-refreshes; it is deliberately NOT shown as an all-clear.</span>
			</div>
		} else if v.Healthy {
			<div style="border:1px solid var(--line); border-radius:2px; background:var(--surface); padding:24px 22px; display:flex; flex-direction:column; gap:14px; align-items:center;">
				<div style="display:flex; gap:10px; align-items:center;">
					<span style="color:var(--acc);">●</span>
					<span class="fl-acc2" style="font-family:var(--font-mono); font-size:11px; letter-spacing:.08em;">ALL CLEAR — NO OPEN CHECKS OR INSIGHTS ACROSS ANY ENGINE</span>
				</div>
				if len(v.HealthyLinks) > 0 {
					<div style="display:flex; gap:20px; flex-wrap:wrap; justify-content:center;">
						for _, l := range v.HealthyLinks {
							<a href={ templ.SafeURL(l.Href) } style="font-family:var(--font-mono); font-size:10.5px; letter-spacing:.04em;">{ l.Label }</a>
						}
					</div>
				}
			</div>
		} else {
			<div style="border:1px solid var(--line); border-radius:2px; background:var(--surface);">
				<div style="padding:8px 12px; border-bottom:1px solid var(--line); font-family:var(--font-mono); font-size:10.5px; letter-spacing:.1em; color:var(--dim); display:flex; gap:14px; align-items:center;">
					NEEDS ATTENTION
					<span class="fl-crit">{ fmt.Sprintf("%d CRIT", v.AttnCrit) }</span>
					<span class="fl-warn">{ fmt.Sprintf("%d WARN", v.AttnWarn) }</span>
					<span style="flex:1;"></span>
					<span style="font-size:9.5px; color:var(--faint);">TOP OPEN CHECKS + INSIGHTS, ALL ENGINES</span>
				</div>
				for _, a := range v.Attention {
					@fleetAttentionRow(a)
				}
			</div>
		}
		<!-- problem-only cluster cards -->
		if len(v.Cards) > 0 {
			<div style="display:grid; grid-template-columns:1fr 1fr; gap:14px;">
				for _, c := range v.Cards {
					@fleetClusterCard(c)
				}
			</div>
		}
		<!-- hidden-healthy footer links -->
		if len(v.HiddenLinks) > 0 {
			<div style="display:flex; gap:20px; flex-wrap:wrap; font-family:var(--font-mono); font-size:10px; letter-spacing:.04em;">
				for _, l := range v.HiddenLinks {
					<a href={ templ.SafeURL(l.Href) }>{ l.Label }</a>
				}
			</div>
		}
	</div>
}

templ fleetStatCell(s FleetStat) {
	<div style="flex:1; padding:10px 14px; border-right:1px solid var(--line2); display:flex; flex-direction:column; gap:2px;">
		<span style="font-family:var(--font-mono); font-size:9.5px; letter-spacing:.1em; color:var(--faint);">{ s.Label }</span>
		<span class={ s.ValueClass } style="font-family:var(--font-mono); font-size:19px; font-weight:600; font-variant-numeric:tabular-nums;">{ s.Value }</span>
		<span style="font-size:10.5px; color:var(--dim);">{ s.Sub }</span>
	</div>
}

templ fleetAttentionRow(a FleetAttentionRow) {
	<a href={ templ.SafeURL(a.Href) } class="fl-attn-row" style="display:flex; align-items:center; gap:12px; padding:8px 12px; border-bottom:1px solid var(--line2); font-size:12.5px; text-decoration:none; color:inherit;">
		<span class={ a.SevClass } style="width:8px; height:8px; flex-shrink:0;"></span>
		<span style="font-family:var(--font-mono); font-size:12px; min-width:230px; color:var(--text);">{ a.ID }</span>
		<span style="color:var(--mut); overflow:hidden; text-overflow:ellipsis; white-space:nowrap;">{ a.Detail }</span>
		<span style="flex:1;"></span>
		<span style="font-family:var(--font-mono); font-size:11px; color:var(--dim); flex-shrink:0;">{ a.Server }</span>
		<span style="font-family:var(--font-mono); font-size:10px; color:var(--faint); width:30px; text-align:right; flex-shrink:0;">{ a.Age }</span>
	</a>
}

templ fleetClusterCard(c FleetClusterCard) {
	<a href={ templ.SafeURL(c.Href) } class="fl-card" style="border:1px solid var(--line); border-radius:2px; background:var(--surface); padding:12px 14px; display:flex; flex-direction:column; gap:11px; text-decoration:none; color:inherit;">
		<div style="display:flex; align-items:center; gap:10px; font-family:var(--font-mono);">
			<span style="font-size:13px; font-weight:600;">{ c.Name }</span>
			if c.Version != "" {
				<span class="fl-acc2" style="font-size:10px;">{ "v" + c.Version }</span>
			}
			if c.Provider != "" {
				<span title={ c.ProviderName } class="fl-info" style="font-size:8.5px; font-weight:600; border:1px solid var(--line); padding:2px 5px; border-radius:1px; letter-spacing:.08em;">{ c.Provider }</span>
			}
			<span style="flex:1;"></span>
			<span class={ c.HealthClass } style="font-size:10px;">{ "[" + c.Health + "]" }</span>
			<span class="fl-acc2" style="font-size:9px; letter-spacing:.08em; font-weight:600;">{ c.Engine }</span>
			<span style="width:22px; height:22px; border:1.5px solid var(--acc2); color:var(--acc2); display:flex; align-items:center; justify-content:center; border-radius:2px; flex-shrink:0;">
				<svg width="13" height="13" viewBox="0 0 24 24"><use href={ "#" + c.EngineIcon }></use></svg>
			</span>
		</div>
		<div style="display:flex; gap:18px; align-items:flex-end;">
			@fleetMetric("QPS", c.QPS)
			@fleetMetric("LATENCY MS", c.LatencyMs)
			@fleetMetric("CONNS", c.Conns)
			@fleetMetric("TOP WAIT", c.TopWait)
		</div>
		<div style="display:flex; gap:8px; font-family:var(--font-mono); font-size:10.5px; align-items:center;">
			<span class="fl-crit">{ fmt.Sprintf("%d CRIT", c.Crit) }</span>
			<span class="fl-warn">{ fmt.Sprintf("%d WARN", c.Warn) }</span>
			<span class="fl-info">{ fmt.Sprintf("%d INFO", c.Info) }</span>
		</div>
	</a>
}

templ fleetMetric(label, value string) {
	<div>
		<div style="font-family:var(--font-mono); font-size:9.5px; letter-spacing:.1em; color:var(--faint);">{ label }</div>
		<div style="font-family:var(--font-mono); font-size:20px; font-weight:600; font-variant-numeric:tabular-nums;">{ value }</div>
	</div>
}
```

- [ ] **Step 3c: Add the small templ helper funcs** (plain Go, in `web/fleet.go`) referenced by the header toggle and the auto-poll trigger:
```go
// fleetSortLabel renders the current sort mode for the SORT toggle.
func fleetSortLabel(sort string) string {
	if sort == "name" {
		return "NAME"
	}
	return "HEALTH"
}

// fleetOtherSort returns the mode the toggle switches to.
func fleetOtherSort(sort string) string {
	if sort == "name" {
		return "health"
	}
	return "name"
}

// fleetPartialURL builds the HTMX refresh URL, preserving the active sort +
// range so the 30s auto-poll and the SORT toggle never clobber the user's
// selection. Rendered from inside #fleet-body so every swap carries the live
// params. `&` is HTML-escaped to `&amp;` by templ in the attribute value.
func fleetPartialURL(sort, rng string) string {
	if rng == "" {
		rng = "24h"
	}
	return "/partial/fleet?sort=" + sort + "&range=" + rng
}
```

- [ ] **Step 4: Regenerate + run — expect PASS.**
```
make templ
go test ./web/ -run TestFleetBody
go build ./...
```
Expected: `ok github.com/dobbo-ca/lynceus/web`. (Note: `templ.SafeURL` HTML-escapes `&` to `&amp;` in the rendered `href`, which the test asserts.)

- [ ] **Step 5: Commit.**
```
git add web/fleet.go web/fleet.templ web/fleet_templ.go web/fleet_test.go
git commit -m "feat(web): fleet dashboard view-models + templ (strips, needs-attention, problem-only cards, all-clear) (ly-ae6.4)"
```

---

### Task 4: Handler + routes (`/` → fleet, `/queries` → top-queries, `/partial/fleet`)

Wires the domain model to the screen: `fetchFleet` maps `fleetview.FleetView` → `web.FleetView` (age formatting, sev→class, scope-aware deep-link hrefs, problem-only filter + sort + hidden-healthy count), and serves it at the fleet-scope landing route.

**Files**
- Create: `internal/api/fleet.go`
- Modify: `internal/api/server.go` (routes)
- Modify: `internal/api/dashboard_test.go` (top-queries moves to `/queries`)
- Create test: `internal/api/fleet_test.go`

**Interfaces**

Consumes:
- `fleetview.BuildFleetView(ctx, s.conf, s.stats, since, until) (fleetview.FleetView, error)` (Task 2).
- Server fields: `s.conf store.Config`, `s.stats store.Stats` (existing).

Produces (handlers on `*Server`):
- `func (s *Server) handleFleet(w http.ResponseWriter, r *http.Request)` — full page.
- `func (s *Server) handleFleetPartial(w http.ResponseWriter, r *http.Request)` — `#fleet-body` fragment.
- `func (s *Server) fetchFleet(r *http.Request) web.FleetView` — mapping/formatting.
- Helpers: `formatAge(d time.Duration) string`, `attentionHref(a fleetview.AttentionItem) string`, `sevSquareClass(sev fleetview.Sev) string`, `healthTextClass(sev fleetview.Sev) string`, `fleetRange(param string, now time.Time) (since time.Time, canonical, label string)`, `openSub(db int, enableSearch, enableCache bool) string`, `clustersNoun(n int) string`, `dashIfEmpty(s string) string`.

Route changes in `server.go` `routes()`:
- `s.mux.HandleFunc("GET /", s.handleFleet)` (was `s.handleDashboard`)
- add `s.mux.HandleFunc("GET /queries", s.handleDashboard)` (preserve the top-queries page)
- add `s.mux.HandleFunc("GET /partial/fleet", s.handleFleetPartial)`
- keep `s.mux.HandleFunc("GET /partial/queries", s.handleQueriesPartial)` unchanged

Steps:

- [ ] **Step 1: Write the failing tests.** Create `internal/api/fleet_test.go`. It reuses the two-DB seeding pattern from `cluster_views_test.go` (`newDBPool`, config+stats migrations, `NewServer`). Include a healthy-fleet server via a helper that seeds a cluster with no firing checks/insights.
```go
package api_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dobbo-ca/lynceus/internal/api"
	"github.com/dobbo-ca/lynceus/internal/store"
)

// setupFleet wires a server over config+stats DBs, seeds one cluster/instance/
// 2 streams, one firing critical check, and one high insight. Returns the server
// plus the seeded clusterID, a serverID, and an insight fingerprint.
func setupFleet(t *testing.T) (srv *httptest.Server, clusterID, serverID, fp string) {
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

	for _, id := range []string{"fl-srv-a", "fl-srv-b"} {
		if _, err := configPool.Exec(ctx, `INSERT INTO servers (id, name) VALUES ($1,$1)`, id); err != nil {
			t.Fatalf("seed server: %v", err)
		}
	}
	cl, err := cfg.CreateCluster(ctx, "orders-prod")
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}
	inst, err := cfg.CreateInstance(ctx, cl.ID, "srv-orders-primary")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	for _, id := range []string{"fl-srv-a", "fl-srv-b"} {
		if err := cfg.AssignServerToInstance(ctx, id, inst.ID); err != nil {
			t.Fatalf("assign: %v", err)
		}
	}
	now := time.Now().UTC()
	fingerprint := "f41b7d09"
	if err := stats.WriteChecksResults(ctx, []store.ChecksResultRow{
		{ServerID: "fl-srv-a", EvaluatedAt: now.Add(-2 * time.Hour), CheckID: "settings.fsync",
			Category: "settings", Severity: "critical", Status: "firing", Object: "fsync",
			Detail: "fsync = off — a crash can lose committed transactions"},
	}); err != nil {
		t.Fatalf("seed checks: %v", err)
	}
	if err := stats.WriteInsights(ctx, []store.InsightRow{
		{ServerID: "fl-srv-a", CapturedAt: now.Add(-4 * time.Hour), Kind: "slow_scan", Severity: "high",
			Fingerprint: fingerprint, Relation: "orders_audit", NodePath: "Seq Scan(orders_audit)",
			RowsReturned: 1, RowsScanned: 1200000, Selectivity: 0.0000008, Detail: "seq scan on orders_audit"},
	}); err != nil {
		t.Fatalf("seed insights: %v", err)
	}
	httpSrv := httptest.NewServer(api.NewServer(api.Config{DevAuth: true}, stats, cfg).Handler())
	t.Cleanup(httpSrv.Close)
	return httpSrv, cl.ID, "fl-srv-a", fingerprint
}

func TestFleet_rootServesDashboardWithTriageAndDeepLinks(t *testing.T) {
	srv, clusterID, serverID, fp := setupFleet(t)
	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	html := string(body)

	for _, want := range []string{
		"Fleet", "LIVE",
		"DATABASES", "OPEN CRITICAL",
		"NEEDS ATTENTION", "settings.fsync", "insight: slow_scan",
		"orders-prod", "[DEGRADED]", "POSTGRESQL",
		// scope-aware deep links (contract with ly-ae6.2)
		"/checks?scope=" + serverID + "&amp;check=settings.fsync",
		"/databases/" + clusterID + "/insights?scope=" + serverID + "&amp;fp=" + fp,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("fleet dashboard missing %q", want)
		}
	}
	// privacy: no literal-looking leak (detail strings are T1 normalized already)
	for _, forbidden := range []string{"@example.com", "secret-value"} {
		if strings.Contains(html, forbidden) {
			t.Errorf("LITERAL LEAK: %q", forbidden)
		}
	}
}

func TestFleetPartial_returnsBodyFragmentOnly(t *testing.T) {
	srv, _, _, _ := setupFleet(t)
	resp, err := http.Get(srv.URL + "/partial/fleet")
	if err != nil {
		t.Fatalf("GET /partial/fleet: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	html := string(body)
	if strings.Contains(html, "<!DOCTYPE html>") {
		t.Error("partial returned a full document; expected a fragment")
	}
	if !strings.Contains(html, `id="fleet-body"`) {
		t.Error("partial missing #fleet-body swap target")
	}
	// the poll trigger is rendered INSIDE the swapped fragment so it survives swaps
	if !strings.Contains(html, `hx-trigger="every 30s"`) {
		t.Error("partial must carry the self-refresh poll trigger inside #fleet-body")
	}
}

func TestFleet_rangeParamDrivesLabelAndPollPreservesIt(t *testing.T) {
	srv, _, _, _ := setupFleet(t)
	resp, err := http.Get(srv.URL + "/?range=1h&sort=name")
	if err != nil {
		t.Fatalf("GET /?range=1h: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	html := string(body)
	if !strings.Contains(html, "RANGE 1H") {
		t.Error("header must reflect the ?range= param label (1H)")
	}
	// the auto-poll trigger + SORT toggle must carry the chosen range+sort so a
	// 30s refresh doesn't revert them (contract with ly-ae6.2's range control).
	if !strings.Contains(html, `hx-get="/partial/fleet?sort=name&amp;range=1h"`) {
		t.Error("poll/toggle URL must preserve sort+range")
	}
}

func TestFleet_engineGateDefaultsOffNoSearchOrCacheCells(t *testing.T) {
	srv, _, _, _ := setupFleet(t)
	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	html := string(body)
	if !strings.Contains(html, "DATABASES") || !strings.Contains(html, "2 db") {
		t.Error("Row1 DATABASES cell + engine-neutral 'N db' severity sub expected")
	}
	// search/cache verticals are gated off (ly-ae6.10 / ly-ae6.11): no SEARCH/CACHE
	// stat cells and no '· 0 search · 0 cache' noise in the severity subs.
	for _, forbidden := range []string{">SEARCH<", ">CACHE<", "0 search", "0 cache"} {
		if strings.Contains(html, forbidden) {
			t.Errorf("engine gate off: unexpected %q in fleet HTML", forbidden)
		}
	}
}

func TestQueries_movedToDedicatedRoute(t *testing.T) {
	pool, srv := setup(t, api.Config{DevAuth: true})
	seedStats(t, pool)
	resp, err := http.Get(srv.URL + "/queries")
	if err != nil {
		t.Fatalf("GET /queries: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `id="queries-table"`) {
		t.Error("/queries must still serve the top-queries page")
	}
}
```

- [ ] **Step 2: Run it — expect FAIL.**
```
go test ./internal/api/ -run 'TestFleet|TestQueries_moved'
```
Expected: fails — `/` still serves top-queries (no "NEEDS ATTENTION"), `/queries` 404s, `/partial/fleet` 404s.

- [ ] **Step 3a: Implement `internal/api/fleet.go`:**
```go
package api

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/dobbo-ca/lynceus/internal/fleetview"
	"github.com/dobbo-ca/lynceus/web"
)

// handleFleet renders the full fleet dashboard (fleet-scope OVERVIEW landing).
func (s *Server) handleFleet(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.FleetPage(s.fetchFleet(r)).Render(r.Context(), w)
}

// handleFleetPartial renders just the #fleet-body fragment for HTMX refresh.
func (s *Server) handleFleetPartial(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.FleetBody(s.fetchFleet(r)).Render(r.Context(), w)
}

// fetchFleet builds the domain fleet view and maps it onto the presentation
// view-model: range parsing, age formatting, sev->class, engine-neutral stat
// strips (gated), scope-aware deep links, the problem-only card filter + sort,
// and the healthy/hidden footer links. A BuildFleetView error surfaces an
// explicit error panel — never a false all-clear.
func (s *Server) fetchFleet(r *http.Request) web.FleetView {
	sortMode := r.URL.Query().Get("sort")
	if sortMode != "name" {
		sortMode = "health"
	}
	now := time.Now().UTC()
	since, rangeParam, rangeLabel := fleetRange(r.URL.Query().Get("range"), now)

	// Postgres-only today. The real per-engine enable source is a fleet-config
	// concern wired WITH the Search/Cache verticals in ly-ae6.10 / ly-ae6.11;
	// here both gates are default-off so Row1/Row2 stay db-only and neutral.
	const enableSearch, enableCache = false, false

	dv, err := fleetview.BuildFleetView(r.Context(), s.conf, s.stats, since, now.Add(time.Minute))
	if err != nil {
		// Surface an explicit error state, NOT Healthy:true — a DB/backend blip
		// must never render as the green all-clear. The 30s poll keeps retrying.
		log.Printf("fleet: BuildFleetView: %v", err)
		return web.FleetView{
			LoadError:     true,
			Sort:          sortMode,
			Range:         rangeParam,
			RangeLabel:    rangeLabel,
			EngineSummary: "FLEET DATA UNAVAILABLE",
		}
	}

	v := web.FleetView{
		Sort:       sortMode,
		Range:      rangeParam,
		RangeLabel: rangeLabel,
		Healthy:    dv.Healthy,
	}
	v.EngineSummary = fmt.Sprintf("%d DB CLUSTERS / RANGE %s", dv.ClusterCount, rangeLabel)

	// row 1: engine-neutral counts. DATABASES always; SEARCH/CACHE only when
	// their gate is on (both off today — see the const above).
	v.Row1 = []web.FleetStat{{
		Label: "DATABASES",
		Value: fmt.Sprintf("%d", dv.ClusterCount),
		Sub:   fmt.Sprintf("clusters · %d nodes · %d databases", dv.NodeCount, dv.DatabaseCount),
	}}
	if enableSearch {
		v.Row1 = append(v.Row1, web.FleetStat{Label: "SEARCH", Value: "0", Sub: "no domains"})
	}
	if enableCache {
		v.Row1 = append(v.Row1, web.FleetStat{Label: "CACHE", Value: "0", Sub: "no clusters"})
	}

	// row 2: open crit/warn/info with engine-neutral subs (db-only today).
	if dv.Healthy {
		v.Row2 = []web.FleetStat{
			{Label: "OPEN CRITICAL", Value: "0", Sub: "all clear", ValueClass: "fl-acc2"},
			{Label: "OPEN WARN", Value: "0", Sub: "no checks firing"},
			{Label: "OPEN INFO", Value: "0", Sub: "no advisories"},
		}
	} else {
		v.Row2 = []web.FleetStat{
			{Label: "OPEN CRITICAL", Value: fmt.Sprintf("%d", dv.OpenCrit), Sub: openSub(dv.OpenCrit, enableSearch, enableCache), ValueClass: "fl-crit"},
			{Label: "OPEN WARN", Value: fmt.Sprintf("%d", dv.OpenWarn), Sub: openSub(dv.OpenWarn, enableSearch, enableCache), ValueClass: "fl-warn"},
			{Label: "OPEN INFO", Value: fmt.Sprintf("%d", dv.OpenInfo), Sub: openSub(dv.OpenInfo, enableSearch, enableCache), ValueClass: "fl-info"},
		}
	}

	// needs-attention rows
	for i := range dv.Attention {
		a := &dv.Attention[i]
		v.Attention = append(v.Attention, web.FleetAttentionRow{
			SevClass: sevSquareClass(a.Sev),
			ID:       a.ID,
			Detail:   a.Detail,
			Server:   a.ServerName,
			Age:      formatAge(now.Sub(a.At)),
			Href:     attentionHref(*a),
		})
		switch a.Sev {
		case fleetview.SevCrit:
			v.AttnCrit++
		case fleetview.SevWarn:
			v.AttnWarn++
		}
	}

	// problem-only cluster cards (crit||warn > 0), sorted, with hidden-healthy count
	shown := make([]fleetview.FleetCluster, 0, len(dv.Clusters))
	for i := range dv.Clusters {
		c := dv.Clusters[i]
		if c.Crit > 0 || c.Warn > 0 {
			shown = append(shown, c)
		}
	}
	// footer only on a NON-all-clear board (matches prototype `!healthyFleet &&
	// hiddenHealthy > 0`); on a healthy fleet the all-clear panel's HealthyLinks
	// already links to all clusters, so a "NOT SHOWN" footer would be redundant.
	if hidden := len(dv.Clusters) - len(shown); hidden > 0 && !dv.Healthy {
		v.HiddenLinks = []web.FleetLink{{
			Label: fmt.Sprintf("%d HEALTHY DB %s NOT SHOWN →", hidden, clustersNoun(hidden)),
			Href:  "/databases",
		}}
	}
	// all-clear panel per-vertical healthy link (DB today; search/cache when enabled)
	if dv.Healthy && dv.ClusterCount > 0 {
		v.HealthyLinks = []web.FleetLink{{
			Label: fmt.Sprintf("%d DATABASE %s HEALTHY →", dv.ClusterCount, clustersNoun(dv.ClusterCount)),
			Href:  "/databases",
		}}
	}
	sortClusters(shown, sortMode)
	for i := range shown {
		c := &shown[i]
		v.Cards = append(v.Cards, web.FleetClusterCard{
			Name:         c.Name,
			Version:      c.Version,
			Provider:     c.Provider,
			ProviderName: c.ProviderName,
			Engine:       c.Engine,
			EngineIcon:   c.EngineIcon,
			Health:       c.Health,
			HealthClass:  healthTextClass(c.HealthSev),
			QPS:          fmt.Sprintf("%.0f", c.QPS),
			LatencyMs:    fmt.Sprintf("%.1f", c.LatencyMs),
			Conns:        fmt.Sprintf("%d", c.ActiveConns),
			TopWait:      dashIfEmpty(c.TopWait),
			Crit:         c.Crit,
			Warn:         c.Warn,
			Info:         c.Info,
			Href:         "/databases/" + c.ClusterID + "?scope=" + c.ClusterID,
		})
	}
	return v
}

// sortClusters orders the problem cards: "health" = crit-band first then name;
// "name" = alphabetical.
func sortClusters(cs []fleetview.FleetCluster, mode string) {
	less := func(i, j int) bool { return cs[i].Name < cs[j].Name }
	if mode != "name" {
		less = func(i, j int) bool {
			ri, rj := healthRank(cs[i]), healthRank(cs[j])
			if ri != rj {
				return ri < rj
			}
			return cs[i].Name < cs[j].Name
		}
	}
	sortSlice(cs, less)
}

func healthRank(c fleetview.FleetCluster) int {
	if c.Crit > 0 {
		return 0
	}
	if c.Warn > 0 {
		return 1
	}
	return 2
}

func attentionHref(a fleetview.AttentionItem) string {
	scope := "?scope=" + a.ServerID
	if a.Kind == "insight" {
		// Navigable insights page (the per-query drilldown is a /partial/ route
		// only); carry the fingerprint as a hint for ly-ae6.6 to auto-expand.
		return "/databases/" + a.ClusterID + "/insights" + scope + "&fp=" + a.Fingerprint
	}
	if a.Category == "vacuum" || strings.HasPrefix(a.CheckID, "vacuum.") {
		return "/vacuum-advisor" + scope
	}
	return "/checks" + scope + "&check=" + a.CheckID
}

func formatAge(d time.Duration) string {
	switch {
	case d < time.Hour:
		m := int(d.Minutes())
		if m < 1 {
			m = 1
		}
		return fmt.Sprintf("%dm", m)
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func sevSquareClass(s fleetview.Sev) string {
	switch s {
	case fleetview.SevCrit:
		return "fl-sq-crit"
	case fleetview.SevWarn:
		return "fl-sq-warn"
	default:
		return "fl-sq-info"
	}
}

func healthTextClass(s fleetview.Sev) string {
	switch s {
	case fleetview.SevCrit:
		return "fl-crit"
	case fleetview.SevWarn:
		return "fl-warn"
	default:
		return "fl-ok"
	}
}

func dashIfEmpty(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// fleetRange maps the shared ?range= param (fed by ly-ae6.2's top-bar segmented
// control) to the since-window + canonical param + header label. Default 24h.
// Canonical set matches the control: 15m|1h|24h|7d|30d → 15M|1H|24H|7D|30D.
func fleetRange(param string, now time.Time) (since time.Time, canonical, label string) {
	switch param {
	case "15m":
		return now.Add(-15 * time.Minute), "15m", "15M"
	case "1h":
		return now.Add(-time.Hour), "1h", "1H"
	case "7d":
		return now.AddDate(0, 0, -7), "7d", "7D"
	case "30d":
		return now.AddDate(0, 0, -30), "30d", "30D"
	default:
		return now.AddDate(0, 0, -1), "24h", "24H"
	}
}

// openSub renders a Row2 severity sub, engine-neutral: db always; search/cache
// only when their vertical is enabled (both off today, so subs read "%d db").
func openSub(db int, enableSearch, enableCache bool) string {
	sub := fmt.Sprintf("%d db", db)
	if enableSearch {
		sub += " · 0 search"
	}
	if enableCache {
		sub += " · 0 cache"
	}
	return sub
}

// clustersNoun pluralizes CLUSTER/CLUSTERS for the footer + all-clear links.
func clustersNoun(n int) string {
	if n == 1 {
		return "CLUSTER"
	}
	return "CLUSTERS"
}
```
Add a tiny local sort shim (avoids importing `sort` twice with a closure signature mismatch) at the bottom of `internal/api/fleet.go`:
```go
func sortSlice(cs []fleetview.FleetCluster, less func(i, j int) bool) {
	// insertion sort: N is the fleet's cluster count (small); keeps the file
	// dependency-free and stable.
	for i := 1; i < len(cs); i++ {
		for j := i; j > 0 && less(j, j-1); j-- {
			cs[j], cs[j-1] = cs[j-1], cs[j]
		}
	}
}
```
> Note: a `sevTextClass` helper is intentionally NOT added — it is unused by the final mapping (row-2 value colors are set via `ValueClass` literals like `"fl-crit"`, and health uses `healthTextClass`). Do not introduce an unused helper.

- [ ] **Step 3b: Update `internal/api/server.go` routes.** In `routes()`, change the `GET /` registration and add the two new routes:
```go
	s.mux.HandleFunc("GET /", s.handleFleet)
	s.mux.HandleFunc("GET /queries", s.handleDashboard)
	s.mux.HandleFunc("GET /partial/fleet", s.handleFleetPartial)
	s.mux.HandleFunc("GET /partial/queries", s.handleQueriesPartial)
```
(Delete the old `s.mux.HandleFunc("GET /", s.handleDashboard)` line; keep `handleDashboard`/`fetchTop` in `dashboard.go` unchanged — they now serve `/queries`.)

- [ ] **Step 3c: Update `internal/api/dashboard_test.go`.** The existing `TestDashboard_rendersTableWithSeededRowsAndNoLiterals` hits `/`; point it at `/queries` (the top-queries page's new home):
```go
	resp, err := http.Get(srv.URL + "/queries")
```
(The `TestQueriesPartial_returnsTableFragmentOnly` test hits `/partial/queries` — unchanged.)

- [ ] **Step 4: Run — expect PASS.**
```
go test ./internal/api/ -run 'TestFleet|TestQueries|TestDashboard'
go build ./...
```
Expected: all listed tests pass. Then run the full package to confirm no regression: `go test ./internal/api/...`.

- [ ] **Step 5: Commit.**
```
git add internal/api/fleet.go internal/api/server.go internal/api/fleet_test.go internal/api/dashboard_test.go
git commit -m "feat(api): serve fleet dashboard at / with scope-aware deep links; move top-queries to /queries (ly-ae6.4)"
```

---

### Task 5: Full verification + bead handoff

**Files**
- No new files. Verification + bead metadata only.

Steps:

- [ ] **Step 1: Full build + test sweep.**
```
make templ            # confirm no uncommitted regen drift
git diff --exit-code   # generated *_templ.go must be in sync (CI parity)
go build ./...
go test ./web/... ./internal/fleetview/... ./internal/api/...
```
Expected: `git diff --exit-code` clean; all packages `ok`. If `make templ` produces a diff, commit it.

- [ ] **Step 2: Manual smoke (optional, if a dev DB is up).**
```
make dev-up
LYNCEUS_DEV_AUTH=1 go run ./cmd/api
# open http://localhost:<port>/  → Fleet header + strips render; toggle theme via user JS API;
# /queries still serves top-queries. make dev-down when done.
```

- [ ] **Step 3: Update the bead.** Record handoff context and flip the label per the feature lifecycle (this closes the plan phase; implementation subagents pick up from `ready-impl`):
```
bd note ly-ae6.4 "Plan written at docs/superpowers/plans/ui-fleet-dashboard.md. 5 tasks: sprite, fleetview rollup, view-models+templ, handler+routes, verify. Depends on ly-ae6.2 (scope shell) + ly-ae6.3 (nav) for full shell; screen renders inside existing web.Layout meanwhile. Contracts for ly-ae6.2: ?scope= deep-links AND ?range= time window (15m|1h|24h|7d|30d, default 24h) — header RANGE + since follow it, preserved on poll/toggle URLs. Engine gate is handler-local default-off consts (enableSearch/enableCache); Search/Cache cards + real enable source = ly-ae6.10/ly-ae6.11 (no dead struct field). LoadError path renders an explicit error panel, never a false all-clear. Info advisories surfaced in Needs-Attention but non-degrading (explicit design decision). PM SIGN-OFF NEEDED: card metric is LATENCY MS (mean) not P95 (no percentile in stats store). Backend gaps: version/provider (ly-99s.5)."
bd label remove ly-ae6.4 needs-plan
bd label add ly-ae6.4 ready-impl
```

- [ ] **Step 4: Commit the plan.**
```
git add docs/superpowers/plans/ui-fleet-dashboard.md
git commit -m "docs(plan): fleet dashboard — triage strips, needs-attention, problem-only cards (ly-ae6.4)"
```

---

## Self-Review

### Spec-coverage: every COMPARISON `#### Fleet dashboard` gap → task

| COMPARISON gap (lines 143–153) | Covered by |
| --- | --- |
| No stat strip: row1 (DATABASES/SEARCH/CACHE counts) + row2 (OPEN CRIT/WARN/INFO with per-engine subs) | Task 2 (`OpenCrit/Warn/Info`, `ClusterCount/NodeCount/DatabaseCount`), Task 3 (`Row1`/`Row2` + `fleetStatCell`), Task 4 (`fetchFleet` builds both rows; `openSub` renders the engine-neutral subs) |
| No Needs-Attention card (sev square + id + detail + server + age; deep-link sets scope + opens explanation) | Task 2 (`AttentionItem`, sorted list), Task 3 (`fleetAttentionRow`), Task 4 (`attentionHref` → checks/vacuum-advisor/insights page with `?scope=`, `formatAge`) |
| No `fleetState` healthy/unhealthy; no all-clear panel + per-vertical healthy links | Task 2 (`Healthy`), Task 3 (all-clear branch: "ALL CLEAR …" + `HealthyLinks` loop), Task 4 (`HealthyLinks` = "N DATABASE CLUSTER(S) HEALTHY →" when healthy; per-engine SEARCH/CACHE healthy links deferred with those verticals). Also Task 4 `LoadError` path renders an explicit error panel instead of a false all-clear on a `BuildFleetView` failure |
| Not problem-only; missing "N HEALTHY CLUSTERS NOT SHOWN" + per-engine ALL links | Task 4 (problem-only filter + `HiddenLinks`, pluralized "N HEALTHY DB CLUSTER(S) NOT SHOWN →" via `clustersNoun`), Task 3 (footer `HiddenLinks` loop). Per-engine ALL SEARCH/CACHE links gated off until ly-ae6.10/ly-ae6.11 (engines disabled) — documented |
| Cluster card anatomy: version chip, provider chip (AWS/AZ tooltip), [HEALTH], engine mark + icon, strict n CRIT/WARN/INFO footer | Task 1 (sprite), Task 3 (`fleetClusterCard` — conditional version/provider chips, `[HEALTH]`, `POSTGRESQL` + `#eng-pg`, crit/warn/info footer), Task 2 (health, counts) |
| Removed noise: sparkline SVG + stream/instance component counts | Task 3 (card omits both; `TestFleetBody` asserts no `<polyline`, no `components`, no `db-card`) |
| No underlying severity/health data in fleetview | Task 2 (`BuildFleetView` severity rollup + `deriveHealth`) |
| No multi-engine presence (Search/Cache cells + placeholder cards + enable flags) | Task 4 `fetchFleet` gates `Row1` cells + `Row2` subs behind two **default-off** consts `enableSearch`/`enableCache` (`openSub` drops the search/cache segments when off; `SEARCH`/`CACHE` cells append only when on); `TestFleet_engineGateDefaultsOffNoSearchOrCacheCells` asserts neither cell nor `· 0 search`/`· 0 cache` noise renders today. Search/Cache **cards** + the real per-engine enable source are ly-ae6.10 / ly-ae6.11 (documented, out of scope). No dead `EnableSearch`/`EnableCache` struct field is added — the gate lives in the handler where it is used |
| No fleet header (Fleet title, LIVE badge, engine-count summary, RANGE, SORT HEALTH/NAME) | Task 3 header (`Fleet` + `LIVE` + `EngineSummary` + SORT toggle), Task 4 (`sortMode`, `EngineSummary`, `sortClusters`, `fleetRange` making the header `RANGE` label follow `?range=`) |
| No design tokens applied | All tasks: tokens via `var(--…)` + fleet utility classes; `web/static/css/legacy.css` not used; `TestFleetBody` asserts `var(--` present |
| Fleet dashboard lives at no dedicated route (`/` is old top-queries) | Task 4: `/` → `handleFleet`; top-queries relocated to `/queries`; `dashboard_test` updated |

### Bead acceptance-criteria → task

- "engine-neutral stat strips" → Task 3 `Row1`/`Row2`, Task 4 mapping. ✅
- "computed needs-attention list that sets scope + opens the explanation" → Task 2 assembly + Task 4 `attentionHref` (`?scope=` + target screen). ✅
- "problem-only cluster cards with healthy hidden behind footer links" → Task 4 filter + `HiddenLinks`, Task 3 footer. ✅
- "all-clear healthy state" → Task 2 `Healthy`, Task 3 all-clear panel + `HealthyLinks`; `LoadError` guarantees a backend failure is never mislabeled all-clear. ✅

### Placeholder scan
No `TBD`, no "add error handling", no "similar to Task N", no code step without real code. Every Go symbol referenced is either defined in an earlier task (`fleetview.BuildFleetView`, `web.FleetView`, `web.FleetLink`, `web.FleetBody`, `EngineSprites`, `fleetSortLabel`/`fleetOtherSort`/`fleetPartialURL`, `formatAge`, `attentionHref`, `sevSquareClass`, `healthTextClass`, `dashIfEmpty`, `fleetRange`, `openSub`, `clustersNoun`, `sortClusters`, `sortSlice`) or verified present in the repo (`fleetview.ListClusterSummaries`; `store.Config.{ListClusters,ListInstances,ListServerStreams,ServerIDsForCluster}`; `store.Stats.{LatestChecksResults,TopInsightsForServers}`; `store.{Cluster,Instance,ServerStream,ChecksResultRow,InsightRow}`; `store.ApplyConfigMigrations`/`ApplyStatsMigrations`; api test helpers `setup`/`seedStats`/`newDBPool`; `web.Layout`; `templ.SafeURL`; stdlib `log`). No struct field is declared and left unread: `web.FleetView.{Row1,Row2,Attention,AttnCrit,AttnWarn,Cards,HealthyLinks,HiddenLinks,Healthy,LoadError,RangeLabel,Range,Sort,EngineSummary}` are each produced by `fetchFleet` and consumed by `FleetBody`; the removed `HiddenHealthy` field is fully replaced by `HiddenLinks`, and no `EnableSearch`/`EnableCache` field exists (the gate is `fetchFleet`-local consts — matches the backend note, closing the earlier prose-vs-code overclaim).

### Type-consistency check
- `fleetview.Sev` (`SevCrit/SevWarn/SevInfo`) is the single normalized severity; `normSev` folds checks (`info/warning/critical`) and insights (`low/medium/high`) — matches the store vocabularies verified in `internal/checks/checks.go` and `internal/insight/insight.go`, and the prototype `sevColor` mapping (`critical|high`→crit, `warning|medium`→warn, else info).
- `web` view-models are strings/ints only (no `store`/`fleetview` import) — mirrors the existing `web.DatabaseCard` boundary; the api handler does all formatting. `web.FleetStat.ValueClass`, `FleetAttentionRow.SevClass`, `FleetClusterCard.HealthClass` are CSS class names (never inline dynamic `style=`), respecting templ's dynamic-style sanitization.
- Check `Status`/`Muted` filter (`Status=="firing" && !Muted`) matches `internal/checks` (`StatusFiring="firing"`, `StatusOK="ok"`) and `ChecksResultRow.Muted bool`.
- Deep-link targets resolve to real registered GET routes: `/checks` (`GET /checks`), `/vacuum-advisor` (`GET /vacuum-advisor`), `/databases/{clusterID}/insights` (`GET /databases/{clusterID}/insights` — the insight rows target this navigable page, not the `/partial/.../query/{fingerprint}` drilldown which is registered only under `/partial/`; the fingerprint rides along as `&fp=` for ly-ae6.6 to auto-expand), and `/databases/{clusterID}` (`GET /databases/{clusterID}`) for cluster cards. All four are present in `server.go` `routes()`. The Scope-contract deep-link example (Global Constraints) is aligned to this exact insights URL.
- **Auto-poll never clobbers state.** The 30s poll trigger is rendered INSIDE `#fleet-body` (not `FleetPage`) and its `hx-get` is `fleetPartialURL(v.Sort, v.Range)`; the SORT toggle uses the same helper with the flipped mode. Because both the toggle and the poll swap `#fleet-body` and re-emit the trigger from the freshly-rendered fragment, a `SORT: NAME`/`?range=1h` selection survives the next refresh. `TestFleetBody_unhealthy` and `TestFleet_rangeParamDrivesLabelAndPollPreservesIt` assert the `sort=…&range=…` URLs (templ escapes `&`→`&amp;`).
- **Range canonical set.** `fleetRange` accepts exactly `15m|1h|24h|7d|30d` (default `24h`) and returns the matching header label `15M|1H|24H|7D|30D` — the value set ly-ae6.2's segmented control emits. No other param values are honored; unknown → 24h.
- **No false all-clear on failure.** `fetchFleet` returns `LoadError:true` (Healthy stays false) and `log.Printf`s a `BuildFleetView` error; `FleetBody` renders the crit `FLEET DATA UNAVAILABLE — RETRYING…` panel via the `if v.LoadError { … } else if v.Healthy { … } else { … }` three-way, so a DB blip can never surface the green all-clear.

### Known deliberate deviations (documented, not gaps)
- **P95 → mean latency — REQUIRES PM SIGN-OFF.** The card metric label reads `LATENCY MS` (fed by `AvgLatencyMs`) instead of the prototype's `P95 MS` (`Lynceus.dc.html:277`), because the stats store exposes only mean latency (`SUM(total_time_ms)/SUM(calls)`), no percentile. This is a visible parity divergence from the pixel-truth prototype: honest and justified, but it is the one label a PM should explicitly bless (or fund a percentile aggregation in the stats store as a backend follow-up) before implementation.
- **Version/Provider empty.** View-model slots exist and render conditionally (chip hidden when empty); data source not wired (provider = ly-99s.5; version derivable from `pg_settings server_version`, out of scope here).
- **Search/Cache verticals absent; engine gate is handler-local + default-off.** `fetchFleet` gates `Row1` cells and `Row2` subs behind default-off consts `enableSearch`/`enableCache`; today Row1 is the single `DATABASES` cell and subs read `"%d db"`. No `EnableSearch`/`EnableCache` struct field is added (that would be dead speculative config); the real per-engine enable source and the Search/Cache **cards** ship together in ly-ae6.10 / ly-ae6.11. This closes the earlier prose-vs-code gap where the plan claimed a struct-field gate that did not exist.
- **Time-range consumption (header follows `?range=`; widget deferred).** The header `RANGE` label and the `since` window are driven by `?range=` (`fleetRange`, canonical `15m|1h|24h|7d|30d`, default `24h`) and preserved on the poll/toggle URLs. ly-ae6.2 owns the top-bar segmented control that *sets* the param; this screen owns *consuming* it. Until the shell lands, `?range=` is reachable only by direct URL — the header still follows it.
- **Info advisories surfaced but non-degrading (explicit design decision).** `Healthy` (all-clear) is `len(Attention)==0`, so an info-band item makes the fleet non-all-clear and shows in Needs-Attention, yet `deriveHealth` keeps its cluster `HEALTHY` — the card is hidden by the problem-only filter and the cluster is counted in "N HEALTHY DB CLUSTERS NOT SHOWN". An info-only cluster can therefore have a Needs-Attention row that deep-links to a cluster the same screen calls healthy-and-hidden. Intentional: info = advisory, not a health regression; the row keeps it discoverable and the deep-link resolves. See Backend data notes for the full statement.
- **Load-error state, not all-clear.** A `BuildFleetView` failure renders an explicit `FLEET DATA UNAVAILABLE` panel (logged) and never the green all-clear; the 30s poll keeps retrying and self-heals.
