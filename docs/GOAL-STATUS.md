# Lynceus — Goal Status Tracker

> **Purpose:** Single living status file for the overarching product goal, so any session (or machine) can pick the work back up. Update this file whenever a goal sub-item advances. Companion to the dated session handoffs in `docs/superpowers/`.
>
> **Last updated:** 2026-06-06 · **Repo HEAD:** `main` @ `95234fe` · all session PRs (#1–#12, #14, #15) merged. `ly-xqf.14` PR pending (branch `auto-explain-extract-7f3a`).

> **Merge state (2026-06-06):** PRs #7–#12 merged to `main`; #13 closed (duplicate of #3). A **parallel session** also landed #3 tamper-evident audit log (`ly-8b0.3`), #4 CI generator pinning (commit `40756db`, no tracking bead — referenced as `ly-eg3`), #5 capability-policy storage (`ly-xnk.2`), #6 audit-log viewer (`ly-8b0.7`) + **read/write DSN split** (commit `33b4da5`, referenced as `ly-lt9` — no such bead; the work is tracked by `ly-ry1`, now closed). `main` is green: `go test ./...` passes incl. e2e, secure, store.
>
> **Bead reconcile (2026-06-06):** parallel session merged the above code but left its beads open. Closed `ly-cxe.1`, `ly-xnk.1`, `ly-xnk.2`, `ly-8b0.7` to match `main`. `ly-eg3`/`ly-lt9` were never beads in this Dolt DB (commit-only IDs). Counts now: **88 open / 20 closed / 42 ready** (`bd stats`). Closing `ly-xnk.2` unblocked `ly-xnk.4` (capability matrix API).

## The Goal (verbatim intent)

Build Lynceus — a privacy-first, Kubernetes-native, HA PostgreSQL monitoring platform (open-source reimagining of pganalyze) — to satisfy:

1. **MVP** — collector queries Postgres → ships data via our webserver → lands in another Postgres.
2. **Feature parity with pganalyze.**
3. **Performance** — parallelism (in-pod + external workers), tighten hot code blocks, simplify (5 lines beating 20), use GitHub Actions tooling where it exists.
4. **Security** — GitHub Actions security tooling; code secure enough to survive a **HITRUST** audit.

**Cross-cutting product constraints (apply to every feature):**

- Efficient on Postgres — never overwhelm the monitored DB.
- Obsessively light network traffic (only normalized T1 data leaves customer infra).
- Obsessively light + efficient time-series storage — must run on **AWS RDS** (vanilla Postgres, no extensions required; TimescaleDB optional behind `store.Stats`).
- Use reader or writer endpoint appropriately for our own DB.
- Kubernetes-native (local dev via Docker).

---

## Status at a glance

| # | Goal | Status | Evidence / Where |
|---|------|--------|------------------|
| 1 | MVP vertical slice | ✅ **DONE & VERIFIED** | `ly-58w` epic closed (9/9). `go test ./...` → 62 tests pass across 12 pkgs incl. `test/e2e/slice_test.go`. |
| 2 | pganalyze parity | 🟡 **In progress** | M2–M6 epics; ~80 features in `docs/specs/2026-05-29-lynceus-features.md`. 88 open / 20 closed / 42 ready (`bd stats`). |
| 3 | Performance review + CI tooling | 🟡 **In progress** | CI lint+bench merged (`.golangci.yml`, `lint.yml`); `ly-3na` CopyFrom writes merged; reader/writer split merged (`ly-ry1`). Remaining: `ly-bsf` partition cache, `ly-awh` collector fan-out. |
| 4 | Security review + CI tooling (HITRUST) | 🟡 **In progress** | Scanning merged (`security.yml`, `dependency-review.yml`); `govulncheck` clean (`ly-17l`); TLS-in-transit guards merged (`ly-cli`, DB half); tamper-evident audit log merged (`ly-8b0.3`); [HITRUST map](security/hitrust-controls.md). Remaining: OIDC/SCIM (M5), Helm controls (`ly-7ck.1`), collector wss (`ly-ckd`). |

Legend: ✅ done · 🟡 in progress · ⬜ not started · 🔴 blocked

---

## Goal 1 — MVP ✅

**Verified working end-to-end.** Data path:

```
collector (cmd/collector)
  → reads pg_stat_statements (internal/collector/reader.go)
  → normalizes + privacy-classifies (internal/normalize)
  → ships T1 proto over websocket (internal/collector/shipper.go)
ingestion_server (cmd/ingestion)
  → terminates ws, rate-limit + DLQ (internal/ingest/server.go)
  → writes to stats Postgres (internal/store/stats.go)
api_server (cmd/api)
  → top-queries API + templ/HTMX dashboard (internal/api, web/)
```

Proof: `test/e2e/slice_test.go` runs the full chain against real Postgres via testcontainers; passes. Nothing further required for Goal 1 — future work only deepens it (M2+).

---

## Goal 2 — pganalyze parity 🟡

Tracked as milestone epics in beads. Run `bd ready` for unblocked work; `bd query 'label = "ready-impl"'` for planned-but-unimplemented features.

| Milestone | Epic | Theme | State |
|-----------|------|-------|-------|
| M1 | `ly-58w` | Vertical slice (MVP) | ✅ closed |
| M2 | `ly-xqf` | Collector depth (pg_stat_activity, schema/index/table stats, plans, locks, waits) | open, several `ready-impl` |
| M3 | `ly-u4t` | Collector-side analysis (EXPLAIN insights) | **unblocked** — `ly-xqf.14` auto_explain plan extraction implemented (PR pending); 13 insight beads now read `query_plans` |
| M4 | `ly-cxe` | Log Insights (parsing ✅ `ly-cxe.1`; sources, PII filters, event catalog) | parsing closed; rest open |
| M5 | `ly-8b0` | Auth & governance (OIDC, SCIM, audit log, enrollment) | audit log ✅ (`ly-8b0.3`) + viewer ✅ (`ly-8b0.7`); OIDC/SCIM/enrollment open |
| M6 | `ly-7ck` | HA & ops (Helm hardening, retention, cluster aggregation, public API) | open |
| — | `ly-xnk` | Capability discovery + per-database operator policy | discovery ✅ (`ly-xnk.1`) + policy storage ✅ (`ly-xnk.2`); `ly-xnk.4` matrix API ready |

**Recently shipped:** `ly-xqf.1` pg_stat_activity reader + connection-state history — **done (PR #9)**: collector samples → 10s/60s aggregation → T1 `ActivityBucket` → ingestion persists → partitioned `activity_buckets`. Unblocks `ly-xqf.3` (wait events — labels already collected).

**Recently shipped:** `ly-xqf.14` auto_explain plan extraction — **implemented (PR pending)**: `planextract` parses JSON auto_explain bodies → normalized T1 `QueryPlan`/`PlanNode` (fail-closed condition normalizer, no literal can survive — new contract test); partitioned `query_plans` table + COPY writer (`TopPlansByQuery` is the M3 read entry point); `collector.ExtractPlans` + ingestion persistence. **Unblocks all 13 M3/M6 insight beads** (`ly-u4t.1–.11`, `ly-7ck.13/.15`). Collector `main.go` not wired (no log source yet — attaches with `ly-cxe.2`).

**Highest-leverage next moves** (long-reach unblocks):

1. `ly-u4t.*` M3 EXPLAIN insights — now unblocked; each reads `query_plans.plan_tree` (JSONB) + scalars via `TopPlansByQuery`. No new schema needed.
2. `ly-xqf.3` wait-event histograms — read path over the `activity_buckets` data already collected by `ly-xqf.1` (no new schema/wire).
3. `ly-xnk.4` capability matrix API (GET + POST toggle) — newly unblocked by `ly-xnk.2` (policy storage done); completes the per-DB operator-policy surface, then `ly-xnk.3` retrofits readers behind the gate.

Planned (have TDD plans in `docs/superpowers/plans/`): `ly-xqf.5` (open); done & merged: `ly-xqf.1`, `ly-8b0.3`, `ly-cxe.1`, `ly-xnk.1`, `ly-xnk.2`. Implemented (PR pending): `ly-xqf.14`.

**Parity definition of done:** every MUST/SHOULD feature in `docs/specs/2026-05-29-lynceus-features.md` closed, with its privacy classification + RDS-safety honored.

---

## Goal 3 — Performance 🟡

**CI tooling (this session):** `golangci-lint` (incl. `gocritic`, `prealloc`, `gocyclo`), Go benchmark job. See `.github/workflows/`.

**Review focus areas** (file beads as `perf` findings; do not refactor working code speculatively):

- **In-pod parallelism:** collector readers (multiple `pg_stat_*` sources) should fan out concurrently with a bounded worker pool, not serially; respect a global query-budget so we never overwhelm Postgres.
- **External workers:** ingestion is horizontally scalable behind the websocket terminator; confirm stateless + idempotent writes so N replicas scale linearly. Partition by collector/db key.
- **Network frugality:** T1 batching + compression on the websocket; delta/aggregate at collector so bytes-on-wire stay minimal. Audit payload sizes.
- **Storage efficiency (RDS):** time-range partition pruning, narrow column types, append-only writes, COPY/batch inserts over row-by-row, retention drop via partition DROP (not DELETE). Verify reader vs writer endpoint usage.
- **Hot blocks / simplification:** prefer 5-line idiomatic Go over 20; `golangci-lint` `prealloc`/`gocritic` surface candidates.

**Findings filed (epic `ly-69x`):**

- `ly-3na` — ✅ **done (PR #8)**: `stats.WriteQueryStats` now uses `CopyFrom` (COPY protocol) instead of per-row INSERTs. (New `activity_buckets` writer also uses COPY.)
- `ly-ry1` — RDS **reader vs writer endpoint**. **Design decided:** the split is satisfied at the *service boundary* — `api_server` is read-only (point its DSN at the RDS **reader** endpoint), `ingestion_server` is write-only (point at the **writer** endpoint). No single process mixes read/write against one endpoint, so no in-process split-pool is needed yet (YAGNI). Enforced via Helm values in `ly-7ck.1`. Bead remains open to track the chart wiring + a guard if a future service does both.
- `ly-bsf` — cache known weekly partitions to skip a `CREATE TABLE` round-trip per write.
- `ly-awh` — collector bounded concurrent reader fan-out + global query budget (forward-looking, for M2 readers).

**Status:** CI tooling added (`lint.yml`, `.golangci.yml`); `govulncheck` clean of perf-relevant issues; findings above filed, not yet implemented.

---

## Goal 4 — Security (HITRUST) 🟡

**CI tooling (this session) — maps to HITRUST controls:**

| Workflow | Tool | HITRUST control area |
|----------|------|----------------------|
| `security.yml` → CodeQL | GitHub CodeQL (SAST) | Secure SDLC / vuln identification (10.b) |
| `security.yml` → govulncheck | Go vuln DB (SCA, call-graph aware) | Patch/vuln mgmt (10.m) |
| `security.yml` → gosec | Go SAST | Secure coding (10.b) |
| `security.yml` → gitleaks | Secret scanning | Credential protection (01.d / 10.k) |
| `security.yml` → Trivy | Container/filesystem CVE scan | Vuln mgmt on images (10.m) |
| `dependency-review.yml` | GitHub Dependency Review | Supply-chain change control (10.m) |

**Built-in design controls (already in codebase):**

- Privacy-by-design: T1 proto cannot carry literals — enforced by contract test (`internal/proto/.../contract_test.go`).
- `data_tier` column + `audit_log` table from day one; T2 reads gated + audited.
- Collector is read-only on monitored DB, outbound-only, runs as limited role.

**Local scan results (2026-06-05):** `govulncheck` — 2 reachable Go **stdlib** vulns via `ListenAndServe`→x509 (`ly-17l`, bump toolchain). `gosec` — 9 issues, 8 are protobuf-generated `unsafe` noise (now excluded via `-exclude-generated`), 1 LOW log-injection (`cmd/api/main.go:47`, addr/config-sourced — accepted). No application-code high-severity findings.

**HITRUST gaps to close (epic `ly-1g1`):**

- `ly-17l` — ✅ **done (PR #11)**: pinned `toolchain go1.26.4`; `govulncheck` now reports no vulnerabilities.
- `ly-cli` — 🟡 **partial (PR #12)**: `internal/secure` fail-closed guards (`CheckDatabaseDSN` sslmode + `CheckWebsocketURL` wss) wired into api/ingestion mains, 15 unit tests. Remaining (`ly-ckd`): collector wss wiring + TLS listener (with Helm `ly-7ck.1`).
- `ly-kwk` — ✅ **done**: HITRUST control-to-evidence mapping doc at [`docs/security/hitrust-controls.md`](security/hitrust-controls.md).
- ✅ **done (PR #3, commit `65f073b`)** — tamper-evident audit log (`ly-8b0.3`): SHA-256 hash chain + append-only triggers + `VerifyChain`. Audit-trail integrity (09.aa). (PR #13 was a duplicate of this, closed unmerged.)
- Scoped collector token issuance + rotation (`ly-8b0.8`); RBAC + least privilege (M5 `ly-8b0`).
- Secrets management (no plaintext creds) — gitleaks gate now enforces.

**Status:** scanning workflows added (`security.yml`, `dependency-review.yml`); local scans run; findings filed under `ly-1g1`.

---

## Session log

- **2026-06-05** — Verified MVP (Goal 1, 62 tests). Established this tracker + security/perf CI tooling (PR #7). Filed perf/security review epics (`ly-69x`, `ly-1g1`). Shipped `ly-3na` CopyFrom write path (PR #8) and `ly-xqf.1` pg_stat_activity connection-state history end-to-end (PR #9). Suite now 70 tests. Wrote HITRUST control-evidence doc (`ly-kwk` ✅) and recorded the `ly-ry1` reader/writer endpoint decision (satisfied at service boundary). Wrote auto_explain extraction plan (`ly-xqf.14` → `ready-impl`, PR #10) — unblocks 13 M3 beads when implemented. Remediated security findings: `ly-17l` Go toolchain bump → govulncheck clean (PR #11); `ly-cli` TLS-in-transit guards (PR #12, partial). Implemented tamper-evident audit log (`ly-8b0.3`, PR #13) — hash chain + VerifyChain + append-only triggers (HITRUST 09.aa). Open PRs: #7 (docs+CI+HITRUST), #8 (perf), #9 (feature), #10 (plan), #11 (toolchain), #12 (TLS), #13 (audit chain) — awaiting merge.

- **2026-06-06** — Merged all session PRs to `main` (#7–#12); closed #13 as duplicate of the parallel session's #3. Rebased #9 (activity) and #12 (TLS) over the parallel work (read/write split, `ly-8b0.3` audit). `main` green end-to-end. **`ly-ry1` is satisfied by the merged read/write split** (api uses read pool via `WithReadPool`; ingestion writes to primary).
- **2026-06-06 (reconcile)** — Cross-checked GOAL-STATUS vs git log + `bd`. Parallel session's merged code (PRs #1–#6) had left its beads open; closed `ly-cxe.1`, `ly-xnk.1`, `ly-xnk.2`, `ly-8b0.7` to match `main`. Confirmed `ly-eg3`/`ly-lt9` are commit-only IDs with no bead in this Dolt DB. Corrected audit-log attribution (PR #3, not #13). Counts: **88 open / 20 closed / 42 ready**. `ly-xnk.4` now unblocked.

- **2026-06-06 (ly-xqf.14)** — Implemented auto_explain plan extraction end-to-end, TDD (5 commits, branch `auto-explain-extract-7f3a`, PR pending review): T1 `QueryPlan`/`PlanNode` proto + privacy contract test; fail-closed `planextract.NormalizeCondition` + `Extract` (JSON-only, fixtures from real PG16 EXPLAIN JSON); `query_plans` partitioned table + COPY writer + `TopPlansByQuery`; `collector.ExtractPlans` + ingestion persistence. Collector `main.go` deliberately not wired (no log source yet — attaches with `ly-cxe.2`). Full suite green: **147 tests / 15 pkgs**. Unblocks 13 M3/M6 insight beads.

> **Next-session start here:** PR for `ly-xqf.14` is open and awaiting review (branch `auto-explain-extract-7f3a`). Once merged, the M3 EXPLAIN insights (`ly-u4t.*`) are unblocked — each reads `query_plans` via `TopPlansByQuery`. Other unblocked: `ly-xnk.4` (caps matrix API), `ly-xqf.3` (wait events), `ly-bsf`/`ly-awh` (perf), `ly-ckd` (collector wss), M5 OIDC/SCIM. Run `bd ready` for the full set.

## How to pick this up next session

```bash
# 1. Hydrate
git pull && bd bootstrap

# 2. Read state
cat docs/GOAL-STATUS.md          # this file
bd ready                         # unblocked work
bd query 'label = "ready-impl"'  # planned features

# 3. Verify MVP still green
go test ./... -timeout 15m

# 4. Pick highest-leverage: ly-xqf.14 (auto_explain, ready-impl) or ly-xnk.4 (caps matrix API)
bd update <id> --status in_progress
```

**Update protocol:** when you close a goal-relevant bead or land perf/security work, edit the "Status at a glance" table + the relevant section here in the same change. Keep this file truthful — it is the contract for resuming.
