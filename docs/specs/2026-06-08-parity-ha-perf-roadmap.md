# Lynceus — Parity + HA/Perf Program Roadmap

> **Date:** 2026-06-08 · **Status:** approved shape, specs in progress
> **Companion to:** `docs/GOALS.md` (living status), `docs/specs/2026-05-29-lynceus-features.md` (feature catalog).
> **Purpose:** Decompose the "pganalyze parity + HA/performance/load-testing" effort into ordered, independently-specced sub-projects mapped onto the existing M2–M6 beads. This is the program-level design; each layer gets its own detailed spec → plan → implementation cycle.

## Intent

Two workstreams, run together but weighted toward parity:

- **Workstream A — pganalyze feature parity (the bulk).** Build out the full pganalyze surface in dependency-layered order. Every feature is testable against a **Docker Postgres** via testcontainers (the existing `test/e2e` + per-package pattern) — seed schema / workload / log fixtures, assert detector/advisor/reader output.
- **Workstream B — HA + performance + load-testing (a lighter "quick plan").** Make all three services production-safe on Kubernetes and put numbers on the hot paths. Pragmatic, not gold-plated.

Sequencing chosen: **dependency-layered** — build the data foundation first, then the engines that consume it, then breadth. Minimizes rework; every layer is demoable.

Cross-cutting constraints (unchanged, from `GOALS.md`): privacy-by-design (only literal-free T1 leaves customer infra; proto contract test enforces it), read-only on the monitored DB, RDS-safe (vanilla Postgres, no extensions), Kubernetes-native (Docker for dev only).

---

## Workstream A — Parity (4 layers)

### Layer 0 — Foundation (wire-up + data plumbing) · **first sub-project, specced in detail**

Turns built-but-dormant code into a running pipeline and lays the readers everything downstream needs. **Keystone** — unblocks the most work.

| Bead | Component | Why it's foundational |
|------|-----------|----------------------|
| `ly-cxe.2` | File-tail log source + wire `planextract`/`logparse`/`insight` into `cmd/collector/main.go` | Collector main is deliberately unwired today (`GOALS.md:81` — "no log source yet"). This turns on plan extraction + log events from a *running* collector. |
| `ly-xqf.5` | Schema / object inventory + size treemap (first-seen tracking) | New readers → T1 proto → store. **Prereq for Index Advisor + schema Checks.** |
| `ly-xqf.6` | Table size & growth over time + TOAST breakdown | Same data foundation; feeds VACUUM/bloat advisor. |
| `ly-hnt` | HTTP surfacing of computed insights (`store.TopPlansByQuery` → `insight.DetectPlans`) | Makes analysis *visible*, not just stored. "Thin caller" per GOALS. (Originally mis-attributed to `ly-u4t.21`, which is actually a Layer-2 Checks bundle — see Layer 2 below.) |
| `ly-xqf.10` | Plan visualization — tree + node/grid view | Renders stored plans; pairs with surfacing. |
| `ly-xnk.4` | Capability matrix API (GET + POST toggle) | Exposes per-DB operator policy (storage already done, `ly-xnk.2`). |
| `ly-xnk.3` | Effective-policy gate at each reader (retrofit + bake into new readers) | New schema/table readers respect per-DB policy from day one. |

**Detailed spec:** `docs/specs/2026-06-08-layer0-foundation.md`.

### Layer 1 — Analysis engines (consume Layer 0)

Each EXPLAIN insight is **one `Detector` + fixtures**, no new schema — plugs into the live `internal/insight` engine (`ly-u4t.7` Slow Scan is the template).

| Bead | Component | Depends on |
|------|-----------|-----------|
| `ly-u4t.1` | EXPLAIN Insight — Disk Sort | Layer 0 plan flow |
| `ly-u4t.2` | EXPLAIN Insight — Hash Batches | Layer 0 plan flow |
| `ly-u4t.3` | EXPLAIN Insight — Inefficient Index | Layer 0 plan flow |
| `ly-u4t.6` | EXPLAIN Insight — Mis-Estimate | Layer 0 plan flow |
| `ly-u4t.8` | EXPLAIN Insight — Stale Stats | Layer 0 plan flow |
| `ly-u4t.12` | **Index Advisor** — missing single/multi-column indexes per query (server-tier analysis, **no HypoPG**) | Layer 0 schema stats (`ly-xqf.5/.6`) |
| `ly-u4t.16` | **VACUUM Advisor** — Bloat / Freezing / Performance / Activity | `pg_stat_user_tables` reader (Layer 0) |
| `ly-xqf.3` | Wait-event histograms | Read path over activity data already collected (`ly-xqf.1`) |

### Layer 2 — Checks / Alerts engine + notifications

| Bead | Component | Note |
|------|-----------|------|
| `ly-u4t.20` | Checks engine framework (severity, scheduling, results table, gating) | Foundation for all bundles |
| `ly-u4t.26` | Vacuum checks incl. **TXID + MultiXact wraparound (CRITICAL safety)** | **Pulled early** — only needs txid-age reader; critical-safety MUST |
| `ly-u4t.21` | Checks bundle — Queries (New Slow Queries regression + Advisor-Insight notifications) | Depends on `ly-u4t.20`; the insight *surfacing* is `ly-hnt` (Layer 0), not this |
| `ly-u4t.22` | Checks bundle — Connections (long-running, idle-tx, blocking) | Uses activity data |
| `ly-u4t.23` | Checks bundle — Schema (invalid / unused indexes) | Uses Layer 0 schema stats |
| `ly-u4t.25` | Checks bundle — System (out of disk) | |
| `ly-u4t.27` | Checks bundle — Index Advisor (missing-index notification) | Uses Layer 1 advisor |
| `ly-7ck.5` | Notification delivery — email | |
| `ly-7ck.6` | Notification delivery — Slack (threaded insight updates) | |

### Layer 3 — Log Insights breadth

| Bead | Component |
|------|-----------|
| `ly-cxe.3` | Log source — filesystem directory (glob) |
| `ly-cxe.4` | Log source — AWS S3 bucket |
| `ly-cxe.5` | Log source — Azure Blob Storage |
| `ly-cxe.6` | Postgres log event classes (~100-event parity catalog) |
| `ly-cxe.7` | PII filter — `filter_log_secret` |
| `ly-cxe.8` | PII filter — `filter_query_sample` |
| `ly-cxe.9` | PII filter — `filter_query_text` |

---

## Workstream B — HA + Performance + Load-testing (quick plan)

Lighter track, runs alongside Layer 0/1. Pragmatic defaults (approved):

### HA / deployment (mostly `ly-7ck.*`)
- **Containerization:** Dockerfiles for all three services (none exist today).
- **Helm chart** (`ly-7ck.1`): per-service Deployment/values, wires the RDS reader/writer DSN split (`ly-ry1`) into values, security context.
- **Probes:** add `/healthz` + `/readyz` to every service (none exist — k8s probes currently have nothing to hit).
- **Graceful shutdown:** collector flushes the in-memory activity aggregator on SIGTERM (currently drops ≤60s of buckets); api already correct.
- **Connection pools:** set pgxpool `MaxConns` / max-lifetime (all pools use library defaults today → exhaustion risk as replicas grow).
- **Collector concurrency — pragmatic singleton (approved):** `replicas=1` + PodDisruptionBudget, document the per-target ownership constraint. (Leader-elected advisory-lock leasing deferred — not "quick".)
- **Ingestion correctness fixes:** guard `ApplyStatsMigrations` so only one replica runs DDL on cold start; bound the in-memory rate-limiter map (memory leak) and note the N-replica × limit caveat; **DLQ drain/retry worker** (DLQ is currently write-only to the same failing DB).
- **Managed Postgres** (`ly-7ck.3`): RDS/Aurora/Cloud SQL wiring.

### Performance (structural)
- Read-path **covering index** for `TopQueriesByTotalTime` (currently full scan/aggregate over all weekly partitions).
- `ly-bsf`: cache known weekly partitions → skip the `CREATE TABLE … PARTITION OF` round-trip per write batch.
- `ly-7ck.4`: **retention scheduler** to call `DropPartitionsOlderThan` (implemented but never invoked → unbounded growth).
- `ly-awh`: collector bounded concurrent reader fan-out + global query budget (also de-risks wiring the new Layer 0 readers).

### Load-testing — measure-first (approved)
- Build a load-test harness (k6 or Go) against the **ws ingestion** endpoint and the **API read path** (none exists today — zero benchmarks).
- Set explicit **QPS / p95 / p99 SLO targets**.
- Measure, then fix the **top measured** bottleneck. Perf fixes above are validated against numbers, not intuition.

**Detailed spec:** to follow after Layer 0 (`docs/specs/2026-06-08-ha-perf-loadtest.md`).

---

## Program sequencing

```
Layer 0 Foundation  ─┬─→ Layer 1 engines ─┬─→ Layer 2 Checks/Alerts ──→ Layer 3 Log breadth
                     │   (insights, Index  │   (incl. wraparound early)
                     │    Advisor, VACUUM) │
Workstream B ────────┴─────────────────────┴── (runs alongside; load-test harness early to gate perf fixes)
```

- **Now:** Layer 0 spec → plan → implement. Workstream B harness + Dockerfiles/probes can start in parallel (independent of parity data model).
- **Then:** Layer 1 (engines), pulling **wraparound safety** (`ly-u4t.26`) forward from Layer 2 because it is critical-safety and cheap.
- **Definition of program done:** every MUST/SHOULD in `docs/specs/2026-05-29-lynceus-features.md` closed with privacy + RDS-safety honored; all three services run HA on k8s with probes + graceful shutdown; documented SLOs met under load test.

## Conventions (unchanged)

- TDD: failing test first; integration tests hit real Postgres via testcontainers, never mocked.
- Per-feature lifecycle: `needs-plan` → write plan in `docs/superpowers/plans/` → `ready-impl` → implement per task → `ready-test` → close.
- Privacy: never add a literal-capable field to a T1 message; the contract test enforces it.
