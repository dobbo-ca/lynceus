# Lynceus — Goal Status Tracker

> **Purpose:** Single living status file for the overarching product goal, so any session (or machine) can pick the work back up. Update this file whenever a goal sub-item advances. Companion to the dated session handoffs in `docs/superpowers/`.
>
> **Last updated:** 2026-06-08 · **Repo HEAD:** `main` @ `0ff4b5c` · PRs #1–#17 merged; **PR #19 OPEN** — Layer 0 Foundation (7 parity beads), pending review/merge.

> **Merge state (2026-06-06):** PRs #7–#12 merged to `main`; #13 closed (duplicate of #3). A **parallel session** also landed #3 tamper-evident audit log (`ly-8b0.3`), #4 CI generator pinning (commit `40756db`, no tracking bead — referenced as `ly-eg3`), #5 capability-policy storage (`ly-xnk.2`), #6 audit-log viewer (`ly-8b0.7`) + **read/write DSN split** (commit `33b4da5`, referenced as `ly-lt9` — no such bead; the work is tracked by `ly-ry1`, now closed). `main` is green: `go test ./...` passes incl. e2e, secure, store.
>
> **Bead reconcile (2026-06-06):** parallel session merged the above code but left its beads open. Closed `ly-cxe.1`, `ly-xnk.1`, `ly-xnk.2`, `ly-8b0.7` to match `main`. `ly-eg3`/`ly-lt9` were never beads in this Dolt DB (commit-only IDs). Counts then: **88 open / 20 closed / 42 ready** (`bd stats`). Closing `ly-xnk.2` unblocked `ly-xnk.4` (capability matrix API).
>
> **Merge state (2026-06-07):** `ly-xqf.14` (PR #16) + `ly-u4t.7` Slow Scan insight (PR #17) merged to `main` (squash, HEAD `0ff4b5c`). M3 EXPLAIN-insight engine (`internal/insight`) now exists. `main` green locally: `go test ./...` → **158 tests / 16 pkgs**. Counts now: **87 open / 22 closed / 54 ready** (`bd stats`). CI on `main` still red for the 4 pre-existing environmental reasons tracked by `ly-1zw` (gitleaks license, golangci-lint version, dependency-graph, testcontainers flake) — not code.
>
> **Layer 0 Foundation (2026-06-08) — PR #19 OPEN (pending review/merge):** All 7 beads of the parity program's Layer 0 implemented on branch `worktree-layer0-foundation-6e0b` (41 commits, 71 files): `ly-xqf.5` schema/object inventory, `ly-xqf.6` table size/growth/TOAST, `ly-u4t.21` insight HTTP surfacing, `ly-xqf.10` plan visualization, `ly-xnk.4` capability matrix API, `ly-xnk.3` capability reader gate, `ly-cxe.2` file-tail log source. Suite green: `go test ./...` → **13 pkgs, all ok** (testcontainers PG16). Collector stays outbound-only (one monitored-DB pool); proto T1 contract test green. New docs: program roadmap `docs/specs/2026-06-08-parity-ha-perf-roadmap.md`, Layer 0 spec `docs/specs/2026-06-08-layer0-foundation.md`. Follow-ups filed: `ly-egn` (e2e log wait-strategy), `ly-dfs` (log_events store table). **Close the 7 beads on merge.**

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
| 2 | pganalyze parity | 🟡 **In progress** | M2–M6 epics; ~80 features in `docs/specs/2026-05-29-lynceus-features.md`. **Parity-program Layer 0 = 7 beads in PR #19** (schema/table readers, insight+plan UI, capability gate, log source). M3 insight engine live (`ly-u4t.7`). Layers 1–3 + Workstream B (HA/perf) ahead. |
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
| M2 | `ly-xqf` | Collector depth (pg_stat_activity, schema/index/table stats, plans, locks, waits) | `ly-xqf.1` ✅; **`ly-xqf.5` inventory + `ly-xqf.6` table stats + `ly-xqf.10` plan-viz in PR #19**; `ly-xqf.3` waits open |
| M3 | `ly-u4t` | Collector-side analysis (EXPLAIN insights) | **in progress** — `ly-xqf.14` extraction + `ly-u4t.7` Slow Scan ✅; **`ly-u4t.21` HTTP surfacing in PR #19**; 7 EXPLAIN insights + Index/VACUUM advisor + Checks remain (Layer 1/2) |
| M4 | `ly-cxe` | Log Insights (parsing ✅ `ly-cxe.1`; sources, PII filters, event catalog) | parsing ✅; **`ly-cxe.2` file-tail source in PR #19** (wires log events + auto_explain plans from a running collector); dir/S3/Azure, PII filters, event catalog open |
| M5 | `ly-8b0` | Auth & governance (OIDC, SCIM, audit log, enrollment) | audit log ✅ (`ly-8b0.3`) + viewer ✅ (`ly-8b0.7`); OIDC/SCIM/enrollment open |
| M6 | `ly-7ck` | HA & ops (Helm hardening, retention, cluster aggregation, public API) | open (Workstream B — not started) |
| — | `ly-xnk` | Capability discovery + per-database operator policy | discovery ✅ + policy storage ✅; **`ly-xnk.4` matrix API + `ly-xnk.3` reader gate in PR #19**; `ly-xnk.5` matrix UI open |

**Recently shipped:** `ly-xqf.1` pg_stat_activity reader + connection-state history — **done (PR #9)**: collector samples → 10s/60s aggregation → T1 `ActivityBucket` → ingestion persists → partitioned `activity_buckets`. Unblocks `ly-xqf.3` (wait events — labels already collected).

**Recently shipped:** `ly-xqf.14` auto_explain plan extraction — **done (PR #16)**: `planextract` parses JSON auto_explain bodies → normalized T1 `QueryPlan`/`PlanNode` (fail-closed condition normalizer, no literal can survive — contract test); partitioned `query_plans` table + COPY writer (`TopPlansByQuery` is the M3 read entry point); `collector.ExtractPlans` + ingestion persistence. **Unblocked all 13 M3/M6 insight beads** (`ly-u4t.1–.11`, `ly-7ck.13/.15`). Collector `main.go` not wired (no log source yet — attaches with `ly-cxe.2`).

**Recently shipped:** `ly-u4t.7` Slow Scan EXPLAIN insight — **done (PR #17)**: new neutral `internal/insight` package (pure `Detector` interface, `DetectAll`/`DetectPlans`, literal-free tree walk) is the reusable engine the other 12 M3 insight beads plug into. `SlowScanDetector` flags a Seq Scan that reads ≥1000 rows and returns ≤10% of them (severity by selectivity). Required adding `rows_removed_by_filter` (a **count**, contract-allowlisted — no literal) to T1 `PlanNode` + the extractor. Suite: **158 tests / 16 pkgs**. HTTP surfacing of insights deferred to `ly-u4t.21`/`ly-xqf.10` (engine built so those are a thin caller).

**Highest-leverage next moves** (post-PR #19 = Layer 1 of the parity program — see `docs/specs/2026-06-08-parity-ha-perf-roadmap.md`):

1. **Layer 1 analysis engines** (data foundation now landed in Layer 0): remaining EXPLAIN insights (`ly-u4t.1/.2/.3/.6/.8` — each one `Detector` + fixtures, no new schema), **Index Advisor** (`ly-u4t.12`, consumes the new schema/table stats), **VACUUM Advisor** (`ly-u4t.16`, consumes `pg_stat_user_tables`); plus `ly-xqf.3` wait-event histograms.
2. **Layer 2 Checks/Alerts** — `ly-u4t.20` framework, pulling **TXID/MultiXact wraparound** (`ly-u4t.26`, critical-safety) early; notifications `ly-7ck.5/.6`.
3. **Workstream B (HA + perf + load-testing)** — quick-plan track, **not yet started**: Dockerfiles, Helm (`ly-7ck.1`), `/healthz`+`/readyz` probes, collector graceful-shutdown flush, pgxpool tuning, retention scheduler (`ly-7ck.4`), partition cache (`ly-bsf`), reader fan-out (`ly-awh`), and a k6/bench harness + SLO targets.

Done & merged on `main`: `ly-xqf.1`, `ly-xqf.14`, `ly-u4t.7`, `ly-8b0.3`, `ly-cxe.1`, `ly-xnk.1`, `ly-xnk.2`. In **PR #19** (pending merge): `ly-xqf.5/.6/.10`, `ly-u4t.21`, `ly-xnk.3/.4`, `ly-cxe.2`.

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

- **2026-06-07 (ly-u4t.7)** — Merged `ly-xqf.14` (PR #16) + Slow Scan EXPLAIN insight (PR #17, squash, HEAD `0ff4b5c`), TDD. Built the reusable `internal/insight` engine (pure `Detector` interface, `DetectAll`/`DetectPlans`, literal-free tree walk) — the other 12 M3 insight beads plug in here. `SlowScanDetector` flags Seq Scans reading ≥1000 rows / returning ≤10% (severity by selectivity, loop-aware). Added `rows_removed_by_filter` (a count, contract-allowlisted) to T1 `PlanNode` + extractor. Suite: **158 tests / 16 pkgs**. Surfacing over HTTP deferred to `ly-u4t.21`/`ly-xqf.10`.

- **2026-06-08 (Layer 0 Foundation — PR #19)** — Brainstormed + decomposed the remaining parity work into a 4-layer program + a Workstream-B HA/perf/load-testing track (roadmap `docs/specs/2026-06-08-parity-ha-perf-roadmap.md`; Layer 0 spec `docs/specs/2026-06-08-layer0-foundation.md`). Implemented all 7 Layer 0 beads on branch `worktree-layer0-foundation-6e0b` (41 commits, 71 files), subagent-driven TDD: `ly-xqf.5` schema inventory, `ly-xqf.6` table size/growth/TOAST, `ly-u4t.21` insight HTTP surfacing (`/insights`), `ly-xqf.10` plan viz (`/plan`), `ly-xnk.4` capability matrix API, `ly-xnk.3` capability reader gate (all readers gated, fail-open, HTTP `/policy-snapshot` refresh), `ly-cxe.2` file-tail log source (wires the dormant `logparse`/`planextract`/`insight` engines into a running collector). Caught + fixed 3 issues beyond the plans: collector↔stats-DB architecture break (inventory must ship first-seen-less; ingestion persists), an e2e caller break from the gate signature change, and a stale generated `layout_templ.go`. Suite green: `go test ./...` → **13 pkgs all ok**. Collector stays outbound-only; proto contract test green. Follow-ups filed: `ly-egn` (e2e log wait-strategy), `ly-dfs` (log_events store table). **PR #19 open — close the 7 beads on merge.**

> **Next-session start here:** Layer 0 is in **PR #19** (review/merge it, then close `ly-xqf.5/.6/.10`, `ly-u4t.21`, `ly-xnk.3/.4`, `ly-cxe.2`). Then **Layer 1** of the parity program — the EXPLAIN insights (`ly-u4t.1/.2/.3/.6/.8`, one `Detector` each, no new schema), **Index Advisor** (`ly-u4t.12`) and **VACUUM Advisor** (`ly-u4t.16`) which now have their schema/table-stats data foundation, plus `ly-xqf.3` wait events. After that **Layer 2** Checks/Alerts (`ly-u4t.20`, pull wraparound `ly-u4t.26` early) and **Workstream B** (HA/perf/load-testing — not started). Run `bd ready` for the full set. CI on `main` still red per `ly-1zw` (environmental, not code).

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

# 4. Review/merge PR #19 (Layer 0); then pick Layer 1: ly-u4t.1/.3 (EXPLAIN insight), ly-u4t.12 (Index Advisor), ly-u4t.16 (VACUUM Advisor)
bd update <id> --claim
```

**Update protocol:** when you close a goal-relevant bead or land perf/security work, edit the "Status at a glance" table + the relevant section here in the same change. Keep this file truthful — it is the contract for resuming.
