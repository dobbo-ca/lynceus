# Dogfood Phase 2 — Databases Dashboard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development. Steps use checkbox (`- [ ]`).

**Goal:** A `/databases` dashboard (bead **ly-yuc.2**) that lists monitored **clusters** as cards or a list, with a search box and a Cards/List dropdown — the PlanetScale-style landing — backed by `fleetview.ListClusterSummaries`. Add a "Databases" nav link. Dashboard-first scope: the full top-bar/sidebar shell rework is deferred.

**Architecture:** Mirror the existing audit-log page exactly (`internal/api/audit.go` + `web/audit.templ`): a persistent controls **form** (search `q` + view `select`) whose HTMX `hx-get` swaps a `#databases-body` fragment; the body renders a **cards grid** or a **list table** depending on `view`. Data comes from `fleetview.ListClusterSummaries(ctx, s.conf, s.stats, since, now)` — the Server already holds `s.conf` (*store.Config) and `s.stats` (*store.Stats). No new store method. q/s + sparkline are derived in the handler from each summary's `QPSBuckets`.

**Tech Stack:** Go, a-h/templ v0.3.1020 (`make templ` to regenerate `*_templ.go`), HTMX 2.0.4 (CDN, already loaded), net/http ServeMux (Go 1.22 method routes), testcontainers integration tests.

---

## File structure

- `web/databases.templ` — **create**: `DatabaseCard` + `DatabasesView` view-models; `DatabasesPage`, `DatabasesControls`, `DatabasesBody` components (+ card/list sub-rendering). Generated `web/databases_templ.go` committed alongside.
- `web/layout.templ` — **modify**: add a `Databases` nav link (first) + a little CSS for the card grid. Regenerate `web/layout_templ.go`.
- `internal/api/databases.go` — **create**: `handleDatabases`, `handleDatabasesPartial`, `fetchDatabases`, `sparklinePoints`.
- `internal/api/server.go` — **modify**: register `GET /databases` + `GET /partial/databases`.
- `internal/api/databases_test.go` — **create**: handler integration tests (two DBs, testcontainers).

---

## Contracts (exact)

### View-models (`web/databases.templ`, package `web`)

```go
// DatabaseCard is the dashboard view-model for one cluster, metrics rolled up
// across its streams. All values are T1 (counts / aggregates / labels).
type DatabaseCard struct {
	ClusterID     string
	Name          string
	QPS           float64 // latest hourly bucket calls / 3600
	AvgLatencyMs  float64
	ActiveConns   int64
	TopWait       string // "" -> rendered as "—"
	StreamCount   int
	InstanceCount int
	InsightCount  int
	Sparkline     string // SVG <polyline> points attr over viewBox 0 0 100 24; "" if <2 buckets
}

// DatabasesView is the whole dashboard view-model: the (already search-filtered)
// cards plus the echoed-back controls state.
type DatabasesView struct {
	Cards []DatabaseCard
	View  string // "cards" (default) | "list"
	Query string // search term, echoed into the form input
}
```

### templ components (`web/databases.templ`)

- `DatabasesPage(v DatabasesView)` → `@Layout("Lynceus — databases", "monitored databases") { @DatabasesControls(v)  <div id="databases-body">@DatabasesBody(v)</div> }`.
- `DatabasesControls(v DatabasesView)` → a `<form class="filters" action="/databases" method="get" hx-get="/partial/databases" hx-target="#databases-body" hx-swap="outerHTML" hx-trigger="submit, keyup changed delay:300ms from:input[name='q'], change from:select[name='view']">` containing: a search `<input name="q" value={ v.Query } placeholder="Search databases…">` and a `<select name="view">` with options `cards`/`list` (selected per `v.View`). Degrades to a plain GET to `/databases` without JS.
- `DatabasesBody(v DatabasesView)` → `<div id="databases-body">`: if `len(v.Cards)==0` an `.empty` message ("No databases monitored yet — run a collector and check back."); else if `v.View=="list"` a `<table>` (cols: Database · q/s · Avg latency (ms) · Conns · Top wait · Streams · Insights); else a cards grid (`<div class="cards">` of `<a class="db-card" href={ templ.SafeURL("/databases/" + c.ClusterID) }>` each showing the sparkline SVG (only if `c.Sparkline != ""`), name, `q/s`, insight badge, and a small facts line). The card links to `/databases/{ClusterID}` (the Phase 3 Overview route — fine to link ahead; it 404s until Phase 3).

Render numbers with `fmt.Sprintf` (`%.1f` for QPS/latency, `%d` for counts). `TopWait` empty → `—`.

### Handler (`internal/api/databases.go`)

```go
func (s *Server) handleDatabases(w http.ResponseWriter, r *http.Request) {
	v := s.fetchDatabases(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.DatabasesPage(v).Render(r.Context(), w)
}

func (s *Server) handleDatabasesPartial(w http.ResponseWriter, r *http.Request) {
	v := s.fetchDatabases(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.DatabasesBody(v).Render(r.Context(), w)
}
```

`fetchDatabases(r)`:
- `q := r.URL.Query()`; `view := q.Get("view")` (only "list" or "cards"; default "cards"); `query := q.Get("q")`.
- `now := time.Now().UTC()`; `sums, err := fleetview.ListClusterSummaries(r.Context(), s.conf, s.stats, now.AddDate(0,0,-1), now)`; on err return `DatabasesView{View: view, Query: query}` (empty cards).
- For each summary: build a `web.DatabaseCard` — `QPS` = (last `QPSBuckets` entry `.Calls`)/3600.0 (0 if none), map AvgLatencyMs/ActiveConns/TopWait/StreamCount/InstanceCount/InsightCount from the summary and `Cluster.Name`/`.ID`, `Sparkline = sparklinePoints(sum.QPSBuckets)`.
- If `query != ""`, keep only cards whose `Name` contains `query` (case-insensitive, `strings.Contains(strings.ToLower(name), strings.ToLower(query))`).
- Return `DatabasesView{Cards, View, Query}`.

`sparklinePoints(buckets []store.QPSBucket) string`: if `len(buckets) < 2` return ""; find min/max of `.Calls`; map each bucket to x = `i*100/(n-1)`, y = `22 - (val-min)/(max-min)*20` (flat line at y=12 if max==min); return space-joined `"x,y"` with one decimal. Pure function.

### Routes (`internal/api/server.go` `routes()`)
```go
	s.mux.HandleFunc("GET /databases", s.handleDatabases)
	s.mux.HandleFunc("GET /partial/databases", s.handleDatabasesPartial)
```

### Nav (`web/layout.templ`)
Add as the FIRST nav link: `<a href="/databases">Databases</a>` (before "Top queries"). Add minimal CSS to the `<style>` block: `.cards { display:grid; grid-template-columns:repeat(auto-fill,minmax(220px,1fr)); gap:0.75rem; } .db-card { display:block; border:1px solid #e0e0e0; border-radius:8px; padding:0.75rem; text-decoration:none; color:inherit; } .db-card:hover { border-color:#2b6cb0; } .db-card .badge { background:#fffbe6; border:1px solid #9e6a03; color:#8a5a00; border-radius:9px; padding:0 0.4rem; font-size:0.75rem; }`.

---

## Tasks

### Task 1: view-models + templ components + nav/CSS

- [ ] Write `web/databases.templ` with the view-models + 3 components above.
- [ ] Add the Databases nav link + card CSS to `web/layout.templ`.
- [ ] Run `make templ` (installs pinned templ v0.3.1020, runs `templ generate`); commit BOTH `.templ` and generated `_templ.go`. Verify in sync: `make templ && git diff --exit-code web/`.
- [ ] `go build ./...` passes.
- [ ] Commit: `feat(web): databases dashboard templ components + nav link (ly-yuc.2)`.

### Task 2: handler + routes (TDD)

- [ ] **Write the failing handler test** `internal/api/databases_test.go` first. Use two testcontainers DBs (config + stats) like `internal/fleetview/summary_test.go`; construct `api.NewServer(api.Config{DevAuth:true}, store.NewStats(statsPool), store.NewConfig(configPool))`; seed one cluster/instance/two servers + query_stats so a card appears. Hit the handler via `httptest` (`srv.Handler().ServeHTTP`). Assert:
  - `GET /databases` (page): status 200, body contains `<!DOCTYPE html>`, the cluster name, and the `databases-body` id.
  - `GET /partial/databases`: status 200, body does NOT contain `<!DOCTYPE`, contains the cluster name.
  - `GET /partial/databases?q=<nonmatch>`: body shows the empty-state text (search filters out the cluster).
  - `GET /partial/databases?view=list`: body contains `<table>`.
  Run it, confirm it FAILS (route not registered → 404, no name in body).
- [ ] Write `internal/api/databases.go` (handlers + `fetchDatabases` + `sparklinePoints`) and register the two routes in `server.go`.
- [ ] Run the test → PASS.
- [ ] `make templ && git diff --exit-code web/` still clean; `go build ./...` passes.
- [ ] Commit: `feat(api): /databases dashboard handler + routes (ly-yuc.2)`.

### Task 3: full verification + PR into the integration branch

- [ ] `go test ./... -race` green; `~/go/bin/golangci-lint run` 0 issues (keep funcs under gocyclo 20; `sparklinePoints` and `fetchDatabases` should be simple — split a helper if needed).
- [ ] `git push -u origin HEAD`; open PR **with base `dogfood-ui`** (NOT main): `gh pr create --base dogfood-ui --title "feat(dogfood): Phase 2 — /databases dashboard (ly-yuc.2)" --body "<summary>"`.
- [ ] Watch CI; on green the orchestrator merges this PR into `dogfood-ui` and moves the bead.

---

## Self-review
- Dashboard granularity = cluster (rolled up via `fleetview`), matching the design. ✓
- Reuses the audit page's form+fragment HTMX pattern (persistent controls form, `#databases-body` swap target) so search keeps input focus. ✓
- Cards link to `/databases/{ClusterID}` (Phase 3 Overview) — forward reference, 404 until Phase 3; acceptable. ✓
- Privacy: only T1 (names, counts, aggregates, wait-event labels) rendered; no literal. ✓
- Deferred (NOT here): full top-bar/sidebar shell, per-cluster scoping of existing pages, tags/Filters. ✓
