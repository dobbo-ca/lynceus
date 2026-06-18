# Dogfood Phase 4 — Cluster Sidebar Views Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development. Steps use checkbox (`- [ ]`).

**Goal:** Turn the Overview sidebar's placeholder anchors into real cluster-scoped views (bead **ly-yuc.4**, autonomous portion): `GET /databases/{clusterID}/queries|insights|activity|settings`, each rendered in the same shell + sidebar (with the right item active). All four reuse the Phase-3 `fleetview.GetClusterDetail` + `toOverviewVM` — **no new store reads**.

**Deferred to a human follow-up (NOT in this plan — the hard stop):** wiring the collector to actually ship `query_plans` and the live PlanetScale `dobbo-uw2` cutover. The collector's log source is unwired (same dependency as PlanetScale `auto_explain`); both need operator credentials/infra. Documented in the umbrella PR #30.

**Architecture:** Refactor `OverviewSidebar(clusterID)` → `ClusterSidebar(clusterID, active string)` (real route links + active highlighting), used by the Overview and all four views. Add four page components that take the existing `web.OverviewVM` (built by the existing `toOverviewVM(detail)`) and render the relevant section fuller. Handlers mirror `handleClusterOverview` (fetch `GetClusterDetail`, 404 if not found).

**Tech Stack:** Go, templ v0.3.1020 (`make templ`), HTMX 2.0.4, Go 1.22 `r.PathValue` routing, testcontainers.

---

## File structure

- `web/overview.templ` — **modify**: rename `OverviewSidebar(clusterID string)` → `ClusterSidebar(clusterID, active string)`; links to `/databases/{id}` (Overview) + `/databases/{id}/queries|insights|activity|settings`; apply `sidebar-link--active` to the `active` item. Update `OverviewPage` to call `@ClusterSidebar(vm.ClusterID, "overview")`.
- `web/cluster_views.templ` — **create**: `ClusterQueriesPage`, `ClusterInsightsPage`, `ClusterActivityPage`, `ClusterSettingsPage` (each takes `OverviewVM`), plus small section components (`ClusterActivityView`, `ClusterSettingsView`). Reuse existing `@OverviewQueries`, `@OverviewInsights`, `@OverviewTopology`.
- `internal/api/cluster_views.go` — **create**: `handleClusterQueries`, `handleClusterInsights`, `handleClusterActivity`, `handleClusterSettings` + a shared `fetchClusterVM(r) (web.OverviewVM, bool)` helper.
- `internal/api/server.go` — **modify**: register the four routes.
- `internal/api/cluster_views_test.go` — **create**: handler integration tests.

(Regenerated `web/*_templ.go` committed alongside the `.templ` edits.)

---

## Contracts (exact)

### `web/overview.templ` — sidebar refactor
```templ
templ ClusterSidebar(clusterID, active string) {
	<nav class="sidebar">
		@sidebarLink(clusterID, "", "overview", active, "Overview")
		@sidebarLink(clusterID, "/queries", "queries", active, "Queries")
		@sidebarLink(clusterID, "/insights", "insights", active, "Insights")
		@sidebarLink(clusterID, "/activity", "activity", active, "Activity")
		@sidebarLink(clusterID, "/settings", "settings", active, "Settings")
	</nav>
}

templ sidebarLink(clusterID, suffix, key, active, label string) {
	if key == active {
		<a class="sidebar-link sidebar-link--active" href={ templ.SafeURL("/databases/" + clusterID + suffix) }>{ label }</a>
	} else {
		<a class="sidebar-link" href={ templ.SafeURL("/databases/" + clusterID + suffix) }>{ label }</a>
	}
}
```
Update `OverviewPage` to call `@ClusterSidebar(vm.ClusterID, "overview")` (was `@OverviewSidebar(vm.ClusterID)`).

### `web/cluster_views.templ` — four pages (each takes `OverviewVM`)
```templ
templ ClusterQueriesPage(vm OverviewVM) {
	@Layout("Lynceus — " + vm.Name + " queries", "expensive queries") {
		<div class="cluster-shell">
			@ClusterSidebar(vm.ClusterID, "queries")
			<main class="cluster-main">
				<h2>{ vm.Name } — queries</h2>
				@OverviewQueries(vm)
			</main>
		</div>
	}
}
templ ClusterInsightsPage(vm OverviewVM) {
	@Layout("Lynceus — " + vm.Name + " insights", "insights") {
		<div class="cluster-shell">
			@ClusterSidebar(vm.ClusterID, "insights")
			<main class="cluster-main">
				<h2>{ vm.Name } — insights</h2>
				@OverviewInsights(vm.Insights)
			</main>
		</div>
	}
}
templ ClusterActivityPage(vm OverviewVM) {
	@Layout("Lynceus — " + vm.Name + " activity", "connections & waits") {
		<div class="cluster-shell">
			@ClusterSidebar(vm.ClusterID, "activity")
			<main class="cluster-main">
				<h2>{ vm.Name } — activity &amp; waits</h2>
				@ClusterActivityView(vm)
			</main>
		</div>
	}
}
templ ClusterSettingsPage(vm OverviewVM) {
	@Layout("Lynceus — " + vm.Name + " settings", "cluster settings") {
		<div class="cluster-shell">
			@ClusterSidebar(vm.ClusterID, "settings")
			<main class="cluster-main">
				<h2>{ vm.Name } — settings</h2>
				@ClusterSettingsView(vm)
			</main>
		</div>
	}
}
```
`ClusterActivityView(vm OverviewVM)` → a summary line (`vm.ActiveConns` active connections, top wait `vm.TopWait` or "—") + a per-instance table (Name, Role, Calls, ActiveConns from `vm.Instances`). `ClusterSettingsView(vm OverviewVM)` → a definition list: Cluster ID (`vm.ClusterID`), instances (count + names/roles from `vm.Instances`), databases monitored (`vm.StreamCount`), data tier ("T1 · T2 off"). Render counts/labels only (T1).

### `internal/api/cluster_views.go`
```go
func (s *Server) fetchClusterVM(r *http.Request) (web.OverviewVM, bool) {
	clusterID := r.PathValue("clusterID")
	now := time.Now().UTC()
	detail, found, err := fleetview.GetClusterDetail(r.Context(), s.conf, s.stats, clusterID, now.AddDate(0,0,-1), now)
	if err != nil || !found {
		return web.OverviewVM{}, false
	}
	return toOverviewVM(&detail), true
}

func (s *Server) handleClusterQueries(w http.ResponseWriter, r *http.Request) {
	vm, ok := s.fetchClusterVM(r)
	if !ok { http.NotFound(w, r); return }
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.ClusterQueriesPage(vm).Render(r.Context(), w)
}
// handleClusterInsights / handleClusterActivity / handleClusterSettings: identical shape,
// rendering ClusterInsightsPage / ClusterActivityPage / ClusterSettingsPage.
```

### Routes (`server.go`, grouped with the other `/databases/{clusterID}` routes)
```go
	s.mux.HandleFunc("GET /databases/{clusterID}/queries", s.handleClusterQueries)
	s.mux.HandleFunc("GET /databases/{clusterID}/insights", s.handleClusterInsights)
	s.mux.HandleFunc("GET /databases/{clusterID}/activity", s.handleClusterActivity)
	s.mux.HandleFunc("GET /databases/{clusterID}/settings", s.handleClusterSettings)
```
ServeMux distinguishes these from `GET /databases/{clusterID}` (the Overview) by the extra path segment — no shadowing.

---

## Tasks

### Task 1: sidebar refactor + four view templ + handlers + routes (TDD)
- [ ] Write the failing `internal/api/cluster_views_test.go` first (two DBs like `overview_test.go`; seed a cluster + data): for each of `/databases/{id}/queries|insights|activity|settings` assert 200 + doctype + cluster name + the active sidebar link (`sidebar-link--active` on the right item) + a view-specific marker (e.g. a query fingerprint on queries, "active connections" on activity, "Cluster ID" on settings); and `/databases/{id}/queries` for an unknown id → 404. Run → fails (routes 404 / components undefined).
- [ ] Refactor the sidebar in `web/overview.templ` (rename to `ClusterSidebar(clusterID, active)`, update `OverviewPage`). Create `web/cluster_views.templ` + `internal/api/cluster_views.go`; register the four routes. Run `make templ`; run the test → PASS. Confirm `make templ && git diff --exit-code web/` clean and `go build ./...`.
- [ ] Verify the existing Overview test still passes (the sidebar rename must not break it — it asserted "Overview" sidebar text, which still renders).
- [ ] Commit `feat(api,web): cluster-scoped sidebar views (queries/insights/activity/settings) (ly-yuc.4)`.

### Task 2: verification + PR into integration branch
- [ ] `go test ./... -race` green; `~/go/bin/golangci-lint run` 0 issues (bodyclose → `defer func(){ _ = resp.Body.Close() }()`; range structs by index; funcs < gocyclo 20).
- [ ] Update the umbrella tracking doc `docs/superpowers/plans/2026-06-18-dogfood-ui-integration.md`: check off Phases 2/3/4; under the hard stop, note that the collector-plan-shipping + live PlanetScale cutover remain the human follow-up. Commit `docs: mark dogfood Phases 2-4 done; collector/PlanetScale cutover = human follow-up (ly-yuc.4)`.
- [ ] `git push -u origin HEAD`; `gh pr create --base dogfood-ui --title "feat(dogfood): Phase 4 — cluster sidebar views (ly-yuc.4)" --body "<summary>"`.
- [ ] Orchestrator merges into `dogfood-ui` on green.

---

## Self-review / scope
- Reuses `GetClusterDetail` + `toOverviewVM` + `@OverviewQueries`/`@OverviewInsights`/`@OverviewTopology` — no new store reads, minimal new rendering. ✓
- Sidebar is now real navigation (Overview + 4 views), active-highlighted, shared by all five pages. ✓
- Settings is read-only (no cluster-rename / no new write methods — YAGNI). ✓
- Privacy: only T1 (names, roles, counts, normalized queries, severity/wait labels). ✓
- Deferred (documented, NOT built here): collector ships query_plans; live PlanetScale dobbo-uw2 cutover (needs operator creds). ✓
