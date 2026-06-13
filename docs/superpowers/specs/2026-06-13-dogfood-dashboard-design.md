# Dogfood Dashboard — Design Spec

> Bead: **ly-yuc** (P0). Source: brainstorming session 2026-06-13. Builds on Fleet A
> (cluster → instance → stream entity model, merged in PR #28).

## Goal

Dogfood Lynceus end-to-end: point our own collector at a **real PlanetScale
Postgres** (`dobbo-uw2`), run the collector → ingestion → stats-store pipeline
against real traffic, and surface the collected data in a product UI modeled on
PlanetScale's own dashboard. The purpose is to **see what we collect and confirm
the data model + collection + literal-free privacy are right** before building
more collectors and insights.

## Context (current state)

- **Frontend exists**: templ + HTMX SSR. View components in `web/` (`layout.templ`,
  `queries.templ`, `audit.templ`); handlers in `internal/api` served by `cmd/api`.
  Today: two pages — `GET /` (top-queries dashboard) and `GET /audit` (audit log),
  each a full page + a `/partial/...` HTMX fragment. Nav is a simple top nav.
  Canonical "how to add a page" reference: `docs/superpowers/plans/2026-06-02-audit-log-viewer.md`.
- **Collected data** (stats store, range-partitioned, vanilla PG):
  `query_stats` (per-fingerprint pg_stat_statements: calls, total/mean time, rows,
  shared_blks_hit/read), `activity_buckets` (60s connection-state + wait-event
  histograms — labels/counts only, never query text), `query_plans` (normalized,
  literal-free auto_explain plan trees), `dlq`.
- **Config store**: `servers` (now with `instance_id`/`database_name` from Fleet A),
  `cluster`, `instance`, `audit_log`, `capability_policy`.
- **Fleet A read funcs** (PR #28): `ListClusters`, `ListInstances(clusterID)`,
  `ListServerStreams(instanceID)`, `ResolveServer(serverID)`,
  `ServerIDsForInstance`, `ServerIDsForCluster`, plus `BackfillFleet`.
- **Insight engine** (`internal/insight`): pure functions over `*lynceusv1.QueryPlan`.
  One detector — **Slow Scan** (`KindSlowScan`). Result struct `insight.Insight`
  {Kind, Severity, Fingerprint, Relation, NodePath, RowsReturned, RowsScanned,
  Selectivity, Detail}. **Not persisted, not wired to any route** — only tests
  consume it today.
- **Privacy backbone**: only normalized, literal-free T1 data leaves the customer's
  infra; T1 proto messages have a contract test forbidding any literal-carrying
  field. No feature may depend on TimescaleDB.

## Design decisions

### 1. App shell — two-level navigation

Rework the nav into the PlanetScale two-level model:

- **Top bar (global, always present)**: Lynceus home (→ databases dashboard) ·
  a **cluster switcher** dropdown (`<cluster name> ▾`) · a user/account placeholder
  (OIDC is a Milestone-5 stub).
- **Left sidebar (cluster context only)**: appears once inside a cluster; switches
  between that cluster's views. v1 list: **Overview · Queries · Insights ·
  Activity & waits · Settings**. (Logs, Audit-in-shell = later.)

The existing `/` (top-queries) and `/audit` fold into this shell. The shell
(`Layout`) is extended/replaced to render the top bar always and the sidebar when
a cluster is in context.

### 2. Granularity = cluster

Dashboard rows/cards **and** the detail page represent **clusters** (the logical
"database"). All metrics roll up across the cluster's server-streams via
`ServerIDsForCluster`. Primary vs replica appears **only** in the Overview
topology panel, never on the dashboard. (Matches PlanetScale: one card per logical
database, query rate summed across all underlying resources.)

### 3. Routes

- `GET /databases` — databases dashboard (cluster cards/list). Becomes the home.
- `GET /databases/{clusterID}` — Overview.
- `GET /databases/{clusterID}/queries|insights|activity|settings` — sidebar views.
- Each view has a paired `GET /partial/...` HTMX fragment, mirroring the existing
  `dashboard.go` / `audit.go` page+fragment pattern.
- `GET /` redirects to `/databases`. `/audit` retained (org-level for now).

### 4. Insights — persisted, computed at the collector

> **Phase 1 reframe (2026-06-13):** planning found the collector does not ship
> `query_plans` yet and PlanetScale may not expose `auto_explain`, so Phase 1
> (ly-yuc.1) derives insights **server-side at ingestion** from the already-T1
> plans instead of via a collector-emitted `Insight` proto. Privacy is
> equivalent; collector plan-shipping is deferred to Phase 4 (ly-yuc.4). The
> collector-side description below is the longer-term target, not the Phase 1
> build — see `docs/superpowers/plans/2026-06-13-dogfood-phase1-insights-and-rollup-reads.md`.

Insights are **persisted** to a new table, and detection runs **at the collector**
(analysis-at-the-edge is the product backbone). Flow:

1. Collector runs the existing `insight` engine over the `QueryPlan`s it extracts.
2. Collector emits a **new T1 `Insight` proto message** inside the `Snapshot`.
   The message carries only literal-free fields (kind, severity, fingerprint,
   relation, node_path, rows_returned, rows_scanned, selectivity, detail-template)
   and **must pass the T1 contract test** (`internal/proto/lynceus/v1/contract_test.go`).
3. Ingestion persists it to a new **`insights`** stats-store table.
4. api reads it for the Insights view + dashboard insight-counts.

`insights` table (range-partitioned like the other stats tables, vanilla PG):
`server_id, fingerprint, captured_at, kind, severity, relation, node_path,
rows_returned, rows_scanned, selectivity, detail, data_tier`. A read method returns
per-server / per-cluster insights and counts.

> **Considered & rejected for v1**: deriving insights server-side at ingestion from
> the already-normalized `query_plans` (privacy-safe, less wiring) — rejected to keep
> analysis at the edge and set the pattern for all future insights/checks.

### 5. Cluster roll-up read layer (new store methods)

Build on Fleet A. New `store` reads:

- `ListClusterSummaries(since, until)` → per cluster: name, combined latest q/s +
  a q/s sparkline series, avg latency (call-weighted mean of `mean_time_ms`),
  active conns + top wait (from `activity_buckets`), insight count, last-seen,
  instance/stream counts. Drives the dashboard.
- Per-cluster time-bucketed **q/s series** (aggregate `query_stats.calls` over time
  across the cluster's `server_id`s) — sparkline + Overview latency/throughput charts.
- Per-cluster **top queries**: `TopQueriesByTotalTime` currently takes no server
  filter — add a variant scoped to a set of `server_id`s.
- Per-cluster **activity** aggregation (roll up `TopActivityBucketsByState`
  across the cluster's streams).
- Per-cluster **insights** read (from the new table).

These methods take the `server_id` set from `ServerIDsForCluster` and aggregate over
the unchanged stats store.

### 6. Dogfood pipeline (real PlanetScale `dobbo-uw2`)

- Create a **read-only monitoring role** on `dobbo-uw2`; confirm `pg_stat_statements`
  is available (and `auto_explain` if PlanetScale exposes it — plans/insights are
  partial if not).
- Run a local collector pointed at it (bootstrap: server-id + ingestion URL + api URL
  + key; tunables come from the server-side policy snapshot per the zero-touch model).
- `BackfillFleet` (or explicit create) links the new stream into a cluster/instance so
  it appears on the dashboard.
- Result: one cluster / one instance / its database(s) with **real traffic** in the UI.
- **Deferred** (need Fleet B fan-out and/or host metrics): multi-node topology across
  replicas, replica lag, CPU/memory/disk.

### 7. Views (v1)

- **Dashboard** (`/databases`): cluster **cards** (q/s sparkline + name + a few facts +
  insight badge) and a **list** view (columns: Database/cluster · q/s sparkline · avg
  latency · conns · top wait · last seen · insights). A single **Cards/List dropdown**
  switches view. **Search** by name. (Tags + a Filters control are a fast-follow.)
- **Overview** (`/databases/{id}`): PlanetScale 2-column layout —
  - left: **topology graph** (instances; role from `instance.role`, may read
    `unknown` until role detection exists), **latency chart** (mean / max / stddev
    from `pg_stat_statements` — **not** p50/p95/p99, which pg_stat_statements cannot
    provide), **most-expensive-queries** table (expand a row → normalized plan tree +
    that query's Slow-Scan insight);
  - right: **facts panel** (Postgres version, instances, databases monitored, data
    tier T1/T2, capabilities, collector last-seen, monitoring-since).
- **Queries / Insights / Activity & waits**: real but basic v1 (the data exists) —
  deeper tables/charts of the same underlying reads.
- **Settings**: minimal (rename; capability-policy view). Tags later.

### 8. Privacy

Only literal-free T1 data is rendered. The new `Insight` proto message is added to
the T1 contract test. An e2e test asserts no literal appears in any stored insight row
or in the rendered HTML of the new pages (extending the existing MVP privacy e2e test).

## Component boundaries

- **proto** — new T1 `Insight` message in the snapshot contract.
- **collector** — emit insights from extracted plans (reuse `internal/insight`).
- **ingestion** — persist insights to the `insights` table.
- **store** — `insights` migration; insights write + read; cluster roll-up reads.
- **api (handlers)** — new routes/handlers + `/partial` fragments per existing pattern.
- **web (templ)** — app shell (top bar + sidebar), dashboard (cards + list), Overview
  (topology, latency, expensive queries, facts), and the basic sidebar views.

Each is independently testable: store reads via testcontainers; collector emission via
unit test; templ views via render tests; privacy via e2e.

## Testing

- Integration (testcontainers, real PG): `insights` migration; insights write/read;
  each cluster roll-up read method.
- Collector unit test: insight emission from a known plan.
- Proto contract test: the new `Insight` T1 message is literal-free.
- e2e privacy test: no literal in any stored insight row or rendered HTML of the new
  pages.
- templ render tests for the new components.

## Scope

**v1 (this bead, ly-yuc):** dogfood `dobbo-uw2`; `insights` table + collector emission;
cluster roll-up reads; app shell; dashboard (cards + list + search); Overview; basic
Queries/Insights/Activity/Settings views; privacy tests.

**Fast-follow (separate beads):** tags + Filters control; host metrics (CPU/mem/disk),
multi-node topology + replica lag; percentile latency (sampling); Logs view; audit
folded into the cluster shell; T2 literal views.

## Suggested implementation phasing

1. **Data/pipeline**: `insights` migration + read; new T1 `Insight` proto + contract
   test; collector emission; ingestion persistence; cluster roll-up read methods.
2. **Shell + dashboard**: app shell (top bar + sidebar) + `/databases` cards/list + search.
3. **Overview**: topology + latency + expensive queries (+ plan/insight expand) + facts.
4. **Remaining sidebar views + dogfood cutover** against real `dobbo-uw2`.

## Risks / open items

- **PlanetScale managed Postgres**: availability of `pg_stat_statements` and
  `auto_explain`, and whether a read-only role can read `pg_stat_*`. If `auto_explain`
  is unavailable, plans + Slow-Scan insights are partial/empty — acceptable validation
  finding for v1.
- **`instance.role` is `unknown`** until role detection (Fleet C) exists; the topology
  panel shows `unknown` unless we add a minimal `pg_is_in_recovery()` probe (candidate
  small enhancement, otherwise deferred).
- Bead is large; the plan should follow the phasing above and may be split if a single
  plan is unwieldy.
