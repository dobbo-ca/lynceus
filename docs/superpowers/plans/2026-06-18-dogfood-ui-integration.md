# Dogfood UI — Integration Branch

> **Umbrella PR for epic `ly-yuc`.** This branch (`dogfood-ui`) accumulates the
> remaining dogfood-dashboard phases via stacked phase PRs, each merged here on
> green CI. **Review this PR's cumulative diff before merging to `main`.**

## What this is

The PlanetScale-style dogfood dashboard (design:
`docs/superpowers/specs/2026-06-13-dogfood-dashboard-design.md`). **Phase 1**
(insights pipeline + cluster roll-up reads, bead `ly-yuc.1`) already merged to
`main` in PR #29. This branch carries the remaining phases, built autonomously by
an implementer + reviewer agent loop. Each phase lands as its own PR **targeting
this branch** (not `main`).

## Phases on this branch

- [ ] **Phase 2 — `ly-yuc.2`**: `/databases` dashboard — cluster cards/list +
  search, backed by `fleetview.ListClusterSummaries`; a Databases nav link.
  *Scoped dashboard-first*: the full two-level top-bar/sidebar shell rework (which
  would touch the 7 existing global pages) is intentionally deferred.
- [ ] **Phase 3 — `ly-yuc.3`**: cluster Overview view (topology + latency +
  most-expensive-queries with plan/insight drill-in + facts panel) and the left
  sidebar (built where it's actually used).
- [ ] **Phase 4 — `ly-yuc.4` (partial)**: wire the collector to ship
  `query_plans` (so insights/plans flow) + the basic remaining sidebar views.

## Hard stop (NOT autonomous)

The **live PlanetScale `dobbo-uw2` cutover** — creating a read-only monitoring
role, confirming `pg_stat_statements` / `auto_explain`, and pointing a collector
at the real database — requires operator credentials and cannot be done by the
agent loop. It is the final step, left for a human, and tracked under `ly-yuc.4`.

## Review

When the autonomous phases are complete and CI is green, review the cumulative
diff of this PR, then merge to `main`. Per-phase detail lives in each phase PR and
in `docs/superpowers/plans/2026-06-18-dogfood-phase*.md`.
