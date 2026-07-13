# Shell UI Consolidation — Design

**Date:** 2026-07-13
**Status:** Approved (brainstorming) — pending implementation plan
**Related:** ly-ae6 (UI design parity epic), nav model in `web/nav.go`, `web/shell.templ`

## Problem

The product UI is split across two layouts and two navigation paradigms:

- **Shell (ly-ae6), scope-driven** — `web/shell.templ` (top-bar scope picker + `BuildNav`
  sidebar, `shell/nav/screens.css`). Already live on 9 routes: `/`, `/queries`,
  `/insights`, `/plan`, `/index-advisor`, `/vacuum-advisor`, `/config-advisor`,
  `/waits`, `/checks`.
- **Dogfood (ly-yuc), route-driven** — `web/layout.templ` (plain top-nav, `legacy.css`)
  with per-cluster tabs. Still on `/databases*` (cards → cluster overview +
  Queries/Insights/Activity/Settings tabs → query drilldown) and `/audit`.

The Shell sidebar (`nav.go` `screenPath`) links ahead to 17 routes that 404 today.
Because the sidebar's "Databases" group points at the legacy dogfood dashboard, the
normal click-path lands in the old UI — so only `/` reads as "modern," and most nav
links are dead. The two nav systems are the inconsistency to eliminate.

## Goal

Make every clickable target on the Shell resolve to a page rendered in the **same
scope-driven Shell template**, all the way down. Structure and per-screen skeletons
first; **live data wiring is a later phase**, gated on a separate data-storage design
discussion. After consolidation, delete the legacy UI components.

Non-goal (this phase): live data. Non-goal (this phase): `/search/*` and `/cache/*`
(engine-gated future datastores — not shown in Postgres-only nav).

## Decisions

| Decision | Choice |
|----------|--------|
| Canonical nav paradigm | **Scope-driven (Shell-native).** Top-bar scope picker drives the `BuildNav` sidebar; low-level screens already accept `?scope=`. |
| Not-yet-built pages | **Per-screen skeleton** — each page's real layout (header, toolbar, tables/panels/stat-tiles) with representative static mock content, no store calls. |
| Boundary | **Postgres surface + admin menu.** Skip `/search/*`, `/cache/*`. |
| Admin routes | Under `/settings/*` (e.g. `/settings/access`, `/settings/providers`, `/settings/collectors`, `/settings/retention`, `/settings/general`). |
| Databases paths | Keep Shell convention: Clusters = `/databases`, Databases = `/databases/all`. |
| Legacy removal | **Yes** — delete `layout.templ`, `legacy.css`, dogfood `ClusterSidebar` + tab routes, `placeholderSidebar`, once their Shell replacements exist. |

## Architecture

`web/shell.templ` is the sole layout. `nav.go` `screenPath` is the single source of
truth for routes. Every handler builds a `ShellView` (via the existing `shell.go`
helpers: scope parse, `web.Sidebar(sc, label, engines, active)`, range options) and
renders its screen templ as the Shell's children — mirroring the 9 handlers that
already do this (e.g. `internal/api/checks.go`, `waits.go`).

Scope-driven identity model:

- Fleet scope: `DATABASE` group = **Clusters** (`/databases`), **Nodes** (`/nodes`),
  **Databases** (`/databases/all`). These are the fleet-level lists.
- Clicking a cluster card sets scope → `/cluster?scope=<cluster>` (the scoped cluster
  Overview). The dogfood `/databases/{id}` overview content is migrated here as a
  skeleton.
- Under a cluster/node/database scope, `BuildNav` already surfaces the scope-aware
  low-level screens (Top Queries, Plans, Wait Events, Checks, Advisors, …) — these
  exist and take `?scope=`, so no new work beyond ensuring scope threads through.

## Work breakdown (Phase 1)

Each item is an independent, testable unit: a handler + a screen templ + route
registration + a route test. Grouped by kind; order within a group is not
significant (parallelizable), except that a legacy family's Shell replacement must
exist before that legacy component is deleted.

### 1. Re-home existing legacy → Shell
- **Clusters list** — re-home `/databases` cards into the Shell as the fleet-scope
  Clusters list. Cards repoint: click → `/cluster?scope=<cluster>` (was
  `/databases/{id}`). Content kept as skeleton/mock.
- **Audit** — wrap `/audit` in the Shell (drop `layout.templ`).

### 2. Scoped identity skeletons (new routes)
- `/cluster` — scoped cluster Overview (migrate dogfood overview content: stat tiles,
  topology, expensive-queries, insights, facts — as skeleton).
- `/nodes` — fleet Nodes list + node Overview skeleton.
- `/databases/all` — fleet Databases list skeleton.
- `/capabilities` — scoped capabilities skeleton.

### 3. Low-level not-yet-built skeletons
- `/connections` (T2), `/alerts`, `/console` (SQL Console T2), `/scripts`
  (Saved Scripts), `/schema/inventory`, `/schema/table-growth`, `/schema/indexes`,
  `/logs/insights`.

### 4. Admin menu skeletons (`/settings/*`)
- `/settings/access` (Access & Roles), `/settings/providers` (Provider Setup),
  `/settings/collectors` (Collectors), `/settings/retention` (Data & Retention),
  `/settings/general` (Settings). Repoint the user-menu `href="#"` in `shell.templ`.

### 5. Fleet body
- Replace `/`'s "Dashboard body arrives with ly-ae6.4" placeholder with a fleet
  dashboard skeleton (fleet stat tiles, per-cluster summary rows, cross-signal
  panels — mock content).

### 6. Legacy removal (after 1–5 land)
- Delete `web/layout.templ`, `web/static/css/legacy.css`, dogfood `ClusterSidebar`,
  the dogfood tab handlers/routes (`/databases/{id}/{queries,insights,activity,settings}`
  and the drilldown if superseded), and `placeholderSidebar` from `shell.templ`.
- Remove now-orphaned imports/view-models created only by the deleted code.

## Skeleton component standard

A shared `Skeleton*` vocabulary in `web/` (e.g. `web/skeleton.templ`) so every page
reads as one system:

- **Page header** — title + scope pill + range indicator (reuses Shell tokens).
- **Toolbar row** — search/filter/segmented controls (inert or nav-only).
- **Content primitives** — `SkeletonTable` (labelled columns + shimmer rows),
  `SkeletonPanel`, `SkeletonStatTiles`, `SkeletonList`.

Mock content is representative (real column names, plausible labels) but static —
**no `store` calls**. Styling via existing tokens in `screens.css`/`nav.css`; add a
small `skeleton.css` (shimmer, muted fills) linked from the Shell if needed.

## Handler pattern

```
func (s *Server) handleNodes(w http.ResponseWriter, r *http.Request) {
    sv := s.shellView(r, "nodes")          // scope parse + Sidebar + range (existing helper)
    render(w, r, web.NodesSkeleton(sv))     // Shell-wrapped screen templ
}
```

Register in `internal/api/server.go` at the `screenPath` route. No store dependency
for skeleton handlers, so they construct fine without a live DB.

## Testing (TDD)

- **Per route:** `httptest` request (no DB required — skeletons don't hit the store)
  asserting: HTTP 200; Shell chrome present (scope bar + `ln-nav` sidebar); the
  screen's skeleton markers present (a stable `data-screen="…"` attribute or heading).
- **Nav-completeness test:** table-driven over every in-scope `screenPath` route +
  the 5 `/settings/*` routes — each returns 200, is Shell-wrapped, and contains no
  `href="#"`. Assert the legacy layout marker (`legacy.css` / dogfood top-nav) is
  absent on all in-scope routes.
- **Legacy-removal test/guard:** after group 6, a test (or `go build` + grep guard)
  confirms `layout.templ`, `legacy.css`, `ClusterSidebar`, `placeholderSidebar` no
  longer exist and are unreferenced.

Write the failing route test first, then the handler + templ, per the repo's TDD
convention. Integration tests that hit real Postgres are unchanged and remain in
Phase 2 when data is wired.

## Phase 2 (deferred — not designed here)

Wire live data into each skeleton, replacing mock content with `store` reads.
**Gated on a separate data-storage design discussion** (requested by the user before
any data work). Not specified in this document.

## Risks / notes

- **Scope threading:** clicking a cluster must set scope and keep it across the
  low-level screens. The mechanism (`ScopeHref` / `?scope=` / `scope.Parse`) exists;
  verify end-to-end when re-homing the Clusters list.
- **Drilldown fate:** the dogfood query drilldown (`/databases/{id}/query/{fp}`) may
  map to the scoped Plans/Query-detail screen; confirm during implementation whether
  it is migrated or retired.
- **Dead legacy view-models:** deleting dogfood templ may orphan Go view-model types;
  remove only those the deletion orphans (surgical).
