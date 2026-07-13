# Shell UI Consolidation Implementation Plan

> **For agentic workers:** Executed via a Workflow (deterministic fan-out). Phases:
> Foundation → Author (parallel) → Integrate → Re-home → Remove-legacy → Verify.

**Goal:** Every clickable Shell target resolves to a page in the scope-driven Shell
template (per-screen skeletons, no live data); re-home the legacy `/databases*` +
`/audit`; delete the legacy UI afterward.

**Architecture:** `web/shell.templ` is the sole layout. Handlers build a `ShellView`
via `s.shellViewFor(r, "<screen>")` and render `web.<Screen>SkeletonPage(sv)` as the
Shell's children (mirrors `internal/api/checks.go` + `web/checks.templ`). Routes come
from `web/nav.go` `screenPath` (already mapped for Postgres). Skeleton pages make no
`store` calls.

**Tech Stack:** Go, templ v0.3.1020 (`make templ` to regenerate), net/http ServeMux,
existing `web/static/css/{tokens,shell,nav,screens}.css`.

## Global Constraints

- Worktree: `/Users/cdobbyn/.dobbo/worktrees/lynceus/shell-consolidation-ffa9fa`,
  branch `shell-consolidation-ffa9fa`. All work + commits here. Verify branch before commit.
- templ CLI not on PATH → regenerate with `make templ` (installs `templ@v0.3.1020`).
  Regeneration rewrites all `web/*_templ.go`; run it **serially**, never in parallel agents.
- Skeleton handlers: no `store` calls. Reference pattern: `internal/api/checks.go`,
  `web/checks.templ`, `internal/api/checks_test.go`.
- Route tests use existing `setup(t, api.Config{DevAuth:true})` → `(pool, srv)`; assert
  200 + Shell chrome (`ln-nav`) + a stable `data-screen="<id>"` marker; no seeding.
- Commit message trailer: `Claude-Session: https://claude.ai/code/session_01MTyKybjokkzcDupSnx8b5G`.

## Foundation (serial)

Create `web/skeleton.templ` — shared components, each rendering tokenized shimmer:
- `SkeletonPageHeader(title, screenID string)` — title + scope pill + range; emits
  the wrapping `<section data-screen={screenID}>`.
- `SkeletonToolbar(controls ...string)` — inert search/filter/segmented row.
- `SkeletonTable(cols []string, rows int)` — labelled columns + `rows` shimmer rows.
- `SkeletonStatTiles(labels []string)` — KPI tile row.
- `SkeletonPanel(title string, lines int)` — titled panel of shimmer lines.
- `SkeletonList(rows int)` — generic list.

Create `web/static/css/skeleton.css` (shimmer keyframes, muted fills, respects
`prefers-reduced-motion`); link it in `web/shell.templ` `<head>`. Run `make templ`,
`go build ./...`, commit. Deliverable: the exact component signatures.

## Screen inventory (Author — parallel; one unit per row/group)

All `screenPath` routes below already exist in `nav.go` (currently 404). Each unit
creates `web/<name>_skeleton.templ` + `internal/api/<name>.go` (handler) +
`internal/api/<name>_test.go`; returns its route-registration line.

| Screen id | Route | Handler / templ | Skeleton content |
|-----------|-------|-----------------|------------------|
| clusterdetail | `/cluster` | `handleClusterScopeOverview` / `ClusterOverviewSkeletonPage` | stat tiles (Q/S, latency, conns, insights), topology tiles, expensive-queries table, insights panel, facts |
| nodes | `/nodes` | `handleNodes` / `NodesSkeletonPage` | table NAME/ROLE/CONNS/QPS/LAG |
| databases (list) | `/databases/all` | `handleDatabasesList` / `DatabasesListSkeletonPage` | table NAME/CLUSTER/SIZE/QPS/WAITS |
| capabilities | `/capabilities` | `handleCapabilities` / `CapabilitiesSkeletonPage` | policy list: capability/scope/state/set-by |
| connections | `/connections` | `handleConnections` / `ConnectionsSkeletonPage` | active table + blocking panel (T2 badge) |
| alerts | `/alerts` | `handleAlerts` / `AlertsSkeletonPage` | rules table name/route/severity/state |
| console | `/console` | `handleConsole` / `ConsoleSkeletonPage` | SQL editor block + results table (T2) |
| scripts | `/scripts` | `handleScripts` / `ScriptsSkeletonPage` | saved-scripts list name/owner/updated |
| schema (×3, one unit) | `/schema/inventory`, `/schema/table-growth`, `/schema/indexes` | `handleSchema*` / `SchemaInventorySkeletonPage`, `SchemaTableGrowthSkeletonPage`, `SchemaIndexesSkeletonPage` | objects table / growth table+spark / indexes table |
| loginsights | `/logs/insights` | `handleLogInsights` / `LogInsightsSkeletonPage` | event-class table class/count/last-seen |
| admin (×5, one unit) | `/settings/{access,providers,collectors,retention,general}` | `handleSettings*` / `Settings*SkeletonPage` | roles table / providers list / collectors table / retention form / general form |

## Integrate (serial)

Apply all route-registration lines to `internal/api/server.go`; repoint the admin
user-menu `href="#"` entries in `web/shell.templ` to `/settings/*`. Run `make templ`,
`go build ./...`, `go test ./internal/api/...`. Fix compile/test failures to green.
Commit.

## Re-home (serial)

- `/databases` cards → Shell (fleet-scope Clusters list). Repoint cards: click →
  `/cluster?scope=<cluster>` (`web.ScopeHref`). Convert handler to `shellViewFor(r, "clusters")`.
- `/audit` → Shell (`shellViewFor(r, "")`, wrap existing body).
- `/` fleet body → replace placeholder with a fleet dashboard skeleton.
Run `make templ`, `go build ./...`, `go test ./internal/api/...`. Commit.

## Remove-legacy (serial)

Delete `web/layout.templ`, `web/static/css/legacy.css`, `placeholderSidebar` (in
`shell.templ`), dogfood `ClusterSidebar` + the `/databases/{id}/{queries,insights,
activity,settings}` tab handlers/routes/templ (superseded by scope nav). Remove
orphaned view-models the deletion creates. `go build ./...`, `go test ./...`. Commit.

## Verify (serial)

`go build ./... && go test ./...`. Boot `cmd/api` against the seeded dev DBs; curl
every in-scope `screenPath` route + `/settings/*`; assert 200 + Shell marker + no
`href="#"` on in-scope routes. Report the route matrix.
