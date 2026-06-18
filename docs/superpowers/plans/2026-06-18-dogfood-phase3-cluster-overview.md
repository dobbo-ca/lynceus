# Dogfood Phase 3 — Cluster Overview Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development. Steps use checkbox (`- [ ]`).

**Goal:** The cluster Overview page (bead **ly-yuc.3**) at `GET /databases/{clusterID}` — a left sidebar + summary tiles + a topology panel (instances/streams, primary/replica) + most-expensive-queries (each expands to its normalized plan tree + Slow-Scan insight via HTMX) + an insights panel + a facts panel. Clicking a dashboard card (Phase 2) lands here.

**Architecture:** Add a single-cluster aggregator `fleetview.ClusterDetail` mirroring `ListClusterSummaries` (resolve the cluster's `server_id` set via `cfg.ServerIDsForCluster`, then fan out to the Phase-1 `*Stats` reads). Render with new templ components in `web/overview.templ`. The expensive-query drill-in **reuses** the existing `web.ToPlanVM` + `web.PlanView` (plan tree) — the handler finds a stored plan for the fingerprint by trying the cluster's servers. Mirror the existing handler/page+fragment pattern (`internal/api/plan.go`, `internal/api/databases.go`).

**Tech Stack:** Go, a-h/templ v0.3.1020 (`make templ`), HTMX 2.0.4, Go 1.22 `r.PathValue` routing (already used by `/api/servers/{id}/...`), testcontainers.

---

## Verified reads (all exist; signatures from the workflow extract)

- `fleetview.ListClusterSummaries(ctx, cfg *store.Config, stats *store.Stats, since, until) ([]ClusterSummary, error)` — the pattern to mirror.
- `cfg`: `ListClusters`, `ListInstances(clusterID)→[]store.Instance`, `ListServerStreams(instanceID)→[]store.ServerStream`, `ServerIDsForInstance(instanceID)→[]string`, `ServerIDsForCluster(clusterID)→[]string`. `store.Instance{ID,ClusterID,Name,Role,CreatedAt}`, `store.ServerStream{ServerID,Name,InstanceID,DatabaseName,T2Enabled,CreatedAt}`, `store.Cluster{ID,Name,CreatedAt}`.
- `stats`: `ThroughputForServers`, `TopQueriesForServers(...,limit)→[]store.TopQuery`, `QPSBucketsForServers`, `ActivitySummaryForServers`, `TopInsightsForServers(...,limit)→[]store.InsightRow`, `InsightCountForServers`, `TopPlansByQuery(serverID, fingerprint, since, until, limit)→[]store.QueryPlanRow`. `store.TopQuery{Fingerprint,NormalizedQuery,Calls,TotalTimeMs}`, `store.InsightRow{ServerID,CapturedAt,Kind,Severity,Fingerprint,Relation,NodePath,RowsReturned,RowsScanned,Selectivity,Detail,DataTier}`, `store.QueryPlanRow{ServerID,CapturedAt,Plan *lynceusv1.QueryPlan,DataTier}`.
- Reuse: `web.ToPlanVM(serverID string, p *lynceusv1.QueryPlan) web.PlanVM` and `templ PlanView(vm PlanVM)` (fragment, root `<div id="plan-view">`).

---

## File structure

- `internal/fleetview/detail.go` — **create**: `ClusterDetail` struct + `InstanceTopo` + `GetClusterDetail(ctx, cfg, stats, clusterID, since, until) (ClusterDetail, bool, error)` (bool=found).
- `internal/fleetview/detail_test.go` — **create**: integration test (two DBs).
- `web/overview.templ` — **create**: Overview view-models + `OverviewPage`, `OverviewSidebar`, topology/tiles/queries/insights/facts sub-components, and a `QueryDrilldown` fragment.
- `web/layout.templ` — **modify**: add minimal CSS for the sidebar + tiles + topology (no nav change).
- `internal/api/overview.go` — **create**: `handleClusterOverview`, `handleClusterQueryDrilldown`, `fetchOverview`.
- `internal/api/server.go` — **modify**: register `GET /databases/{clusterID}` + `GET /partial/databases/{clusterID}/query/{fingerprint}`. **IMPORTANT ordering:** these must be registered AFTER the static `GET /databases` and `GET /partial/databases` (ServeMux prefers the more specific pattern, but keep them grouped and verify the static dashboard still wins for `/databases`).
- `internal/api/overview_test.go` — **create**: handler integration tests.

---

## Contracts (exact)

### `internal/fleetview/detail.go`

```go
type InstanceTopo struct {
	Instance    store.Instance
	Streams     []store.ServerStream
	Calls       int64 // window calls across this instance's streams
	ActiveConns int64
}

type ClusterDetail struct {
	Cluster      store.Cluster
	Instances    []InstanceTopo
	StreamCount  int
	Calls        int64
	AvgLatencyMs float64
	ActiveConns  int64
	TopWait      string
	InsightCount int
	QPSBuckets   []store.QPSBucket
	TopQueries   []store.TopQuery   // limit 20
	Insights     []store.InsightRow // limit 50
}

// GetClusterDetail assembles a single cluster's Overview data. found=false (no
// error) if clusterID is unknown. Mirrors ListClusterSummaries' roll-up.
func GetClusterDetail(ctx context.Context, cfg *store.Config, stats *store.Stats, clusterID string, since, until time.Time) (ClusterDetail, bool, error)
```
Implementation: `ListClusters` → find the one with `ID==clusterID`; if none, `return ClusterDetail{}, false, nil`. `serverIDs = ServerIDsForCluster(clusterID)`. For each `inst` in `ListInstances(clusterID)`: `streams = ListServerStreams(inst.ID)`; `ids = ServerIDsForInstance(inst.ID)`; `tp = ThroughputForServers(ids,...)`; `act = ActivitySummaryForServers(ids,...)`; build `InstanceTopo{inst, streams, tp.Calls, act.ActiveConns}`. Cluster-wide: `tp = ThroughputForServers(serverIDs,...)` → Calls + AvgLatencyMs (tp.TotalTimeMs/tp.Calls, guard>0); `QPSBuckets`, `ActivitySummaryForServers` (ActiveConns+TopWait), `InsightCountForServers`, `TopQueriesForServers(serverIDs,...,20)`, `TopInsightsForServers(serverIDs,...,50)`. `StreamCount=len(serverIDs)`. Return `found=true`.

### `internal/api/overview.go`

```go
func (s *Server) handleClusterOverview(w http.ResponseWriter, r *http.Request) {
	clusterID := r.PathValue("clusterID")
	now := time.Now().UTC()
	detail, found, err := fleetview.GetClusterDetail(r.Context(), s.conf, s.stats, clusterID, now.AddDate(0,0,-1), now)
	if err != nil || !found {
		http.NotFound(w, r) // 404 for unknown cluster or read error
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.OverviewPage(toOverviewVM(detail)).Render(r.Context(), w)
}

// handleClusterQueryDrilldown renders the plan tree + insight for one fingerprint
// in a cluster (HTMX fragment). Finds a stored plan by trying the cluster's
// servers; renders web.PlanView via web.ToPlanVM, plus any matching insight.
func (s *Server) handleClusterQueryDrilldown(w http.ResponseWriter, r *http.Request) {
	clusterID := r.PathValue("clusterID")
	fp := r.PathValue("fingerprint")
	now := time.Now().UTC()
	serverIDs, _ := s.conf.ServerIDsForCluster(r.Context(), clusterID)
	var planVM web.PlanVM
	planVM.Empty = true
	for _, sid := range serverIDs {
		rows, err := s.stats.TopPlansByQuery(r.Context(), sid, fp, now.AddDate(0,0,-1), now, 1)
		if err == nil && len(rows) > 0 {
			planVM = web.ToPlanVM(sid, rows[0].Plan)
			break
		}
	}
	insights, _ := s.stats.TopInsightsForServers(r.Context(), serverIDs, now.AddDate(0,0,-1), now, 50)
	var match *web.OverviewInsight
	for i := range insights {
		if insights[i].Fingerprint == fp { v := toOverviewInsight(insights[i]); match = &v; break }
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.QueryDrilldown(planVM, match).Render(r.Context(), w)
}
```
`toOverviewVM(detail)` maps `ClusterDetail` → `web.OverviewVM` (see below); `toOverviewInsight(InsightRow)` → `web.OverviewInsight`. Use the dashboard's `sparklinePoints` helper for the cluster QPS sparkline (extract it to a shared spot or duplicate a small copy — a 1-line `// reuse` is fine; if duplication trips lint, move `sparklinePoints` to a shared `internal/api/sparkline.go`).

### `web/overview.templ` view-models + components

View-models (package `web`):
```go
type OverviewInstance struct { Name, Role string; Databases []string; Calls, ActiveConns int64 }
type OverviewQuery struct { Fingerprint, NormalizedQuery string; Calls int64; MeanMs, TotalMs float64; HasInsight bool }
type OverviewInsight struct { Severity, Relation, Detail, Fingerprint string }
type OverviewVM struct {
	ClusterID, Name string
	QPS float64; AvgLatencyMs float64; ActiveConns int64; TopWait string; InsightCount, StreamCount int
	Sparkline string
	Instances []OverviewInstance
	Queries   []OverviewQuery
	Insights  []OverviewInsight
}
```
Components:
- `OverviewPage(vm OverviewVM)` → `@Layout("Lynceus — " + vm.Name, "cluster overview")` wrapping a 2-col layout: `@OverviewSidebar(vm.ClusterID)` + a main column with summary tiles, `@OverviewTopology(vm.Instances)`, `@OverviewQueries(vm)`, `@OverviewInsights(vm.Insights)`, and a facts list (instances count, databases monitored=StreamCount, data tier "T1", top wait).
- `OverviewSidebar(clusterID string)` → a `<nav class="sidebar">` with "Overview" (active) + Queries/Insights/Activity/Settings as in-page `#anchors` (the dedicated cluster-scoped view routes are Phase 4).
- `OverviewTopology(insts []OverviewInstance)` → cards per instance showing Role (badge: primary/replica/unknown), Name, databases, Calls, ActiveConns.
- `OverviewQueries(vm OverviewVM)` → a `<table>` of `vm.Queries`; each row’s normalized query is a `<button>`/`<a>` with `hx-get={ "/partial/databases/" + vm.ClusterID + "/query/" + q.Fingerprint }` `hx-target` a per-row detail cell (use a sibling `<tr>`/`<div id={ "drill-" + q.Fingerprint }>` and `hx-swap="innerHTML"`), and a "slow scan" badge when `q.HasInsight`.
- `QueryDrilldown(plan PlanVM, insight *OverviewInsight)` → renders the matching insight (if non-nil) then `@PlanView(plan)`.
- `OverviewInsights(items []OverviewInsight)` → a list/table (severity, relation, detail).

`OverviewQuery.HasInsight` = true if the fingerprint appears in `detail.Insights`. `OverviewQuery.MeanMs` = TotalMs/Calls (guard >0).

### Routes (`server.go`, after the static `/databases` routes)
```go
	s.mux.HandleFunc("GET /databases/{clusterID}", s.handleClusterOverview)
	s.mux.HandleFunc("GET /partial/databases/{clusterID}/query/{fingerprint}", s.handleClusterQueryDrilldown)
```

---

## Tasks

### Task 1: `fleetview.GetClusterDetail` + test (TDD)
- [ ] Write failing `internal/fleetview/detail_test.go` (two DBs like `summary_test.go`): seed a cluster/instance/2 servers + query_stats + an insight; assert `found`, `StreamCount==2`, `Calls`, non-empty `Instances`/`TopQueries`, `InsightCount==1`; and `found==false` for an unknown clusterID. Run → fails to build.
- [ ] Write `internal/fleetview/detail.go`. Run → PASS.
- [ ] `go build ./...`. Commit `feat(fleetview): GetClusterDetail single-cluster aggregator (ly-yuc.3)`.

### Task 2: Overview templ + handler + routes (TDD)
- [ ] Write failing `internal/api/overview_test.go`: two DBs; seed a cluster + data; assert `GET /databases/{id}` → 200, doctype, cluster name, "Overview" sidebar text, a query row; `GET /databases/unknown` → 404; `GET /partial/databases/{id}/query/{fp}` → 200, no doctype, contains "Plan tree" or the empty-plan message. Run → fails (routes 404 / components undefined).
- [ ] Write `web/overview.templ` (+ CSS in `layout.templ`), `internal/api/overview.go`, register routes. Run `make templ`; run the test → PASS. Verify `make templ && git diff --exit-code web/` clean; `go build ./...`.
- [ ] Commit `feat(api,web): cluster Overview page + query plan drill-in (ly-yuc.3)`.

### Task 3: verification + PR into integration branch
- [ ] `go test ./... -race` green; `~/go/bin/golangci-lint run` 0 issues (watch bodyclose in tests → use `defer func(){ _ = resp.Body.Close() }()`; range structs by index for gocritic; keep funcs < gocyclo 20 — extract helpers).
- [ ] `git push -u origin HEAD`; `gh pr create --base dogfood-ui --title "feat(dogfood): Phase 3 — cluster Overview (ly-yuc.3)" --body "<summary>"`.
- [ ] Orchestrator merges into `dogfood-ui` on green.

---

## Self-review / scope
- Reuses `web.ToPlanVM`/`PlanView` for the drill-in (no new plan rendering). ✓
- Single-cluster aggregator mirrors the reviewed `ListClusterSummaries` pattern. ✓
- Sidebar introduced here (where it's used); dedicated cluster-scoped view routes (Queries/Insights/Activity/Settings pages) + folding the 7 global pages = Phase 4 / later. Sidebar non-Overview items are in-page anchors for now. ✓
- Privacy: only T1 (identifiers, normalized conditions, counts, wait labels). The plan tree is already-normalized (literal-free). ✓
- Facts panel: instances, databases monitored, data tier, top wait (pg version / CPU / disk / replica-lag are deferred host metrics). ✓
