# Lynceus — Feature Parity Specification

**Date:** 2026-05-29
**Status:** Draft
**Companion to:** `2026-05-29-lynceus-design.md` (architecture/technology). This document defines **what the product does** — the feature set and our parity targets against pganalyze.

## How to read this

Lynceus aims for feature parity with pganalyze's analytical capabilities, built open-source and privacy-first. Each feature below carries a **parity priority**:

- **MUST** — core value; Lynceus is not credible without it.
- **SHOULD** — strong differentiator; targeted for v1.
- **COULD** — nice-to-have; post-v1 / opportunistic.

And a **locality**: where the analysis runs. This matters because of our defining constraint (see design spec §2): **analysis happens at the collector; only normalized data leaves the customer's infrastructure.**

### The architectural payoff

A full audit of pganalyze's feature set shows that **almost everything is computable at a single collector from one instance's snapshot** — per-query stats, query normalization, every EXPLAIN insight, the Query Advisor insights, the index "what-if" cost model, all VACUUM advisories, connection/lock/wait analysis, all health checks except replication, and schema/column/buffer-cache statistics. This means Lynceus's privacy-first model is not a feature sacrifice — we can match pganalyze's analytical depth while keeping raw data on-box.

**The genuinely server-side features** (require cross-instance aggregation or a persistent backend), tracked but lower priority:
- Cluster-wide query aggregation across primary + replicas.
- Replication checks (high lag, missing HA follower) — need cross-instance correlation.
- Notification *delivery* (email/Slack/PagerDuty) and bidirectional incident sync.
- Historical trend storage & cross-time comparison (this is our stats DB by design).
- Multi-tenant surfaces: GraphQL/REST API sharing, MCP server, Workbook sharing.

---

## 1. Query Performance — *MUST core*
| Feature | Priority | Locality | Source | Privacy |
|---|---|---|---|---|
| Per-query statistics (latency, calls, runtime %, rows, block hits, I/O timing, charted) | MUST | local | `pg_stat_statements` | normalized only — safe |
| Query normalization / fingerprinting (literals → `$1`) | MUST | local | query parser (pg_query) | *this is the privacy mechanism* |
| Weighted/percentile latency | SHOULD | local | pg_stat_statements deltas | safe |
| Query samples (real literal samples, ≤5/24h) | SHOULD | local | logs / auto_explain | **T2** — opt-in, audited, filterable |
| Query search by text/role | COULD | local | — | safe |
| Query tags (ORM/sqlcommenter attribution, 7d) | COULD | local | log/query comments | comment text may leak context |
| Cluster-wide aggregation (primary + replicas) | COULD | **server** | multiple collectors | safe |

## 2. EXPLAIN / Plan Analysis — *MUST core*
- **Automatic EXPLAIN collection** from `auto_explain` logs — MUST, local.
- **Plan visualization** — tree view + grid/node view — MUST, local.
- **Plan comparison** (side-by-side diff for the same query) — SHOULD, local.
- **EXPLAIN Insights** (auto-detected anti-patterns from a single plan; all local; plan node labels only, no literals):
  1. Disk Sort (sort spills to disk) — MUST
  2. Hash Batches (hash spills to disk) — MUST
  3. Inefficient Index (reads excess rows before filter) — MUST
  4. Large Offset (recommend keyset pagination) — SHOULD
  5. Lossy Bitmaps (Bitmap Heap Scan goes lossy) — SHOULD
  6. Mis-Estimate (planner estimate vs actual divergence) — MUST
  7. Slow Scan (Seq Scan discarding many rows) — MUST
  8. Stale Stats (table not recently ANALYZEd) — MUST

## 3. Query Advisor — *SHOULD*
Continuously scans plans for anti-patterns and suggests rewrites/hints (all local):
- Inefficient Nested Loops — SHOULD
- Wrong Index Due To ORDER BY — SHOULD
- Disk Spill Due To Low `work_mem` — SHOULD
- Alerts on new insights — SHOULD

## 4. Index Advisor & Indexing Engine — *MUST advisor / SHOULD engine*
- **Index Advisor** — missing single/multi-column indexes per query — MUST, local. Needs WHERE/JOIN column structure (normalized), no literals.
- **Workload-wide batch analysis** — MUST, local.
- **Indexing Engine "What-If"** — decomposes workload into per-table scans, simulates candidate indexes against an internal cost model entirely in-app (no DB extension, no prod risk) — SHOULD, local. *Strongest differentiator.*
- **"Good Enough" minimal index-set selection** (speedup vs write overhead) — SHOULD, local.
- **Index Write Overhead** metric — COULD, local.
- **Standalone single-query index advisor** (free web tool) — COULD, local.

## 5. VACUUM Advisor — *MUST*
- **Per-table VACUUM statistics** (dead-row cleanup, freezing perf, autovacuum scheduling) — MUST, local. Source: `pg_stat_user_tables` + VACUUM logs.
- **VACUUM Simulator** (models autovacuum triggering under settings) — SHOULD, local. Differentiator.
- **Bloat / Freezing / Performance / Activity** views — MUST/SHOULD, local. All statistical, no PII.

## 6. Log Insights — *MUST (this is the unique data-source work)*
- **Structured log extraction** into classified events — MUST, local. *Highest PII risk → governed by filtering below.*
- **100+ log event classes/filters** (connection authorized, constraint/not-null violations, deadlocks, checkpoints, autovacuum completions, query duration, temp file, …) — MUST, local.
- **`auto_explain` log integration** (feeds §2) — MUST, local.
- **VACUUM monitoring from logs** — SHOULD, local.
- **Multi-source log ingestion** — local file tail, filesystem dir, **AWS S3**, **Azure Blob Storage** — MUST, local. *(Lynceus differentiator — first-class multi-cloud log sources.)*
- **PII filtering control plane** (collector-side, before transmission) — MUST, local. The core privacy controls:
  - `filter_log_secret` (credential, parsing_error, unidentified, statement_text, statement_parameter, table_data, ops)
  - `filter_query_sample` (none / normalize / all) — default `normalize`
  - `filter_query_text` (none / unparsable) — default `unparsable`

## 7. Connections & Wait Events — *MUST states / SHOULD rest*
- **Connection states history** (active/idle/idle-in-txn, bucketed) — MUST, local. Source: `pg_stat_activity`.
- **Connection traces** (live running queries, auto-refresh, click-to-timepoint) — SHOULD, local. Shows live query text → **T2**, filterable.
- **Lock / blocking analysis** incl. chained blocking (A→B→C) — SHOULD, local. Source: `pg_locks` + `pg_stat_activity`.
- **Wait events** historical breakdown (e.g. `IO/DataFileRead`), sampled — SHOULD, local. Source: `pg_stat_activity.wait_event` / `pg_wait_sampling`.

## 8. Alerts, Checks & Notifications
Checks run on each snapshot with severity (info/warning/critical). All locally computable **except replication** (needs cross-instance correlation).

**Checks (parity targets):**
- Queries: New Slow Queries (MUST), Advisor Insights (SHOULD)
- Connections: Active/Long-Running Queries (MUST), Idle Transactions (MUST), Blocking Queries (MUST)
- Index Advisor: Missing Indexes (SHOULD)
- Schema: Invalid Indexes (MUST), Unused Indexes (MUST)
- Settings: Disabled features, Disabled fsync, Too-small `shared_buffers`, Disabled stats collection, Too-small `work_mem` (SHOULD)
- System: Out of Disk Space (MUST) — needs OS/storage metrics (self-managed only)
- Replication: High Lag, Missing HA Follower (SHOULD) — **server / cross-instance**
- Vacuum: Inefficient Index Phase, Insufficient VACUUM Frequency, Blocked by Xmin Horizon, **Approaching TXID Wraparound**, **Approaching MultiXact Wraparound** (MUST — wraparound is a critical safety class)

**Notifications (delivery = server-side):** Email (SHOULD), Slack (SHOULD), PagerDuty bidirectional ack/resolve (COULD), Check-Up periodic reports (COULD).

## 9. Schema & Configuration — *MUST inventory / SHOULD rest*
- **Schema/object inventory** (tables, indexes, views, functions; size treemap; first-seen) — MUST, local. Schema names may be sensitive → `ignore_schema_regexp` filter.
- **Table size & growth + TOAST breakdown** — MUST, local.
- **Partition breakdown** (per-partition sizes) — COULD, local.
- **Per-table index list** (size, usage, write overhead, buffer-cache) — SHOULD, local.
- **Column statistics** (`null_frac`, `n_distinct`, avg width — scalar stats only, **not** MCV/histogram literal bounds) — SHOULD, local. Privacy: collect scalars only, never MCV values.
- **HOT update tracking** — COULD, local.
- **Buffer cache statistics** (per-table/index hit ratio) — SHOULD, local. Source: `pg_buffercache`.
- **Config tuning recommendations** (shared_buffers/work_mem/autovacuum/fsync vs workload) — SHOULD, local.
- **Per-table autovacuum recommendations** — SHOULD, local.

## 10. Workbooks — *COULD*
- Baseline + variants (test planner settings / `pg_hint_plan` / rewrites) — COULD.
- Parameter sets (`$1` → named params, multiple bind values) — COULD. Real params → **T2** sensitive.
- Auto/manual `EXPLAIN ANALYZE` runner (collector-run, time-capped) — COULD.

## 11. Platform, Integrations & Security
- **Collector agent** (self-hosted, filters before send) — MUST (the architecture).
- **Managed installs** — RDS/Aurora, Azure, Cloud SQL, Heroku, Crunchy Bridge, Aiven, self-managed — SHOULD.
- **Auth & governance** — OIDC SSO, SCIM provisioning, RBAC by group, tamper-evident audit (see design spec §2.3) — MUST. *(Lynceus treats this as core, given health-data compliance, where pganalyze gates it behind tiers.)*
- **Enterprise self-hosted / data residency** — Lynceus is self-hosted by default — N/A as a tier; MUST as a property.
- **REST/GraphQL API** — COULD, server.
- **MCP server** (expose metrics/insights to AI assistants) — COULD, server.
- **OpenTelemetry** trace span → EXPLAIN plan — COULD.
- **Query formatter** (free tool) — COULD.

---

## Feature → Milestone mapping

| Milestone (beads epic) | Features |
|---|---|
| **M1** Vertical slice | Per-query stats + normalization + top-queries dashboard (proves pipeline + privacy contract) |
| **M2** Collector depth | Connection states/traces, wait events, lock/blocking; schema/table/column/buffer-cache stats; auto_explain EXPLAIN collection + plan viz |
| **M3** Analysis | 8 EXPLAIN insights; Query Advisor; Index Advisor + Indexing Engine "What-If"; VACUUM Advisor + Simulator; config tuning; the checks engine |
| **M4** Log Insights | Structured extraction, 100+ filters, multi-source ingestion (file/S3/Azure), PII filtering control plane |
| **M5** Auth & governance | OIDC, SCIM, RBAC by group, audited T2 access |
| **M6** HA & ops | Notification delivery (email/Slack/PagerDuty), retention, cluster-wide aggregation, replication checks, API/MCP |

## MVP (Milestone 1) feature scope
Only **per-query statistics** + **query normalization** + a **top-queries-by-total-time dashboard**, end to end. Everything else above is deferred to M2–M6. The MVP exists to prove the pipeline and the privacy contract, not to deliver breadth.

## Source
Derived from a full crawl of pganalyze's marketing and `/docs/*` pages (query-performance, explain, query-advisor, index-advisor, indexing-engine, vacuum-advisor, log-insights, connections, checks, schema-statistics, workbooks, collector/settings, mcp, opentelemetry), 2026-05-29. Priorities and the privacy/locality classifications are Lynceus's own targets, not pganalyze's.
