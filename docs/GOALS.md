# Lynceus — Goal Status Tracker

> **Purpose:** Single living status file for the overarching product goal, so any session (or machine) can pick the work back up. Update this file whenever a goal sub-item advances. Companion to the dated session handoffs in `docs/superpowers/`.
>
> **Last updated:** 2026-06-09 · **Repo HEAD:** `main` @ `3b0fcfb` (PR #23, Layer 2, merged) · **Layer 2 merged** as PR #23 (squash `3b0fcfb`, atop PR #20 Layer 1 `3dc464d` / PR #19 Layer 0 `fa99c4a`) — 8 beads: `ly-u4t.20` Checks engine framework, `ly-u4t.26` TXID/MultiXact wraparound (+ VACUUM Freezing view), `ly-u4t.4/.5/.9/.10/.11` EXPLAIN insights, `ly-u4t.27` Index-Advisor notification — all closed. Follow-up `ly-32k` filed for the 3 remaining (non-critical) vacuum checks of the `ly-u4t.26` bundle. **Layer 1** merged earlier as PR #20 (squash `3dc464d`). Remaining Layer-2 Checks bundles + notifications, Layer 3 log breadth, Workstream B (HA/perf) ahead.

> **Merge state (2026-06-06):** PRs #7–#12 merged to `main`; #13 closed (duplicate of #3). A **parallel session** also landed #3 tamper-evident audit log (`ly-8b0.3`), #4 CI generator pinning (commit `40756db`, no tracking bead — referenced as `ly-eg3`), #5 capability-policy storage (`ly-xnk.2`), #6 audit-log viewer (`ly-8b0.7`) + **read/write DSN split** (commit `33b4da5`, referenced as `ly-lt9` — no such bead; the work is tracked by `ly-ry1`, now closed). `main` is green: `go test ./...` passes incl. e2e, secure, store.
>
> **Bead reconcile (2026-06-06):** parallel session merged the above code but left its beads open. Closed `ly-cxe.1`, `ly-xnk.1`, `ly-xnk.2`, `ly-8b0.7` to match `main`. `ly-eg3`/`ly-lt9` were never beads in this Dolt DB (commit-only IDs). Counts then: **88 open / 20 closed / 42 ready** (`bd stats`). Closing `ly-xnk.2` unblocked `ly-xnk.4` (capability matrix API).
>
> **Merge state (2026-06-07):** `ly-xqf.14` (PR #16) + `ly-u4t.7` Slow Scan insight (PR #17) merged to `main` (squash, HEAD `0ff4b5c`). M3 EXPLAIN-insight engine (`internal/insight`) now exists. `main` green locally: `go test ./...` → **158 tests / 16 pkgs**. Counts now: **87 open / 22 closed / 54 ready** (`bd stats`). CI on `main` still red for the 4 pre-existing environmental reasons tracked by `ly-1zw` (gitleaks license, golangci-lint version, dependency-graph, testcontainers flake) — not code.
>
> **Layer 0 Foundation (2026-06-08) — PR #19 MERGED (squash `fa99c4a`):** All 7 beads of the parity program's Layer 0 landed on `main`: `ly-xqf.5` schema/object inventory, `ly-xqf.6` table size/growth/TOAST, `ly-hnt` insight HTTP surfacing (`/insights` — **was mis-attributed to `ly-u4t.21`; that bead is actually the Layer-2 Checks bundle "Queries", still open — see `ly-gu3`**), `ly-xqf.10` plan visualization, `ly-xnk.4` capability matrix API, `ly-xnk.3` capability reader gate, `ly-cxe.2` file-tail log source. Collector stays outbound-only (one monitored-DB pool); proto T1 contract test green.
>
> **Layer 1 Analysis Engines (2026-06-08) — PR #20 MERGED (squash `3dc464d`):** 8 beads, subagent-driven TDD, **16 commits**. Five EXPLAIN insights as pure `insight.Detector`s plugged into the live engine — `ly-u4t.1` Disk Sort, `ly-u4t.2` Hash Batches, `ly-u4t.3` Inefficient Index, `ly-u4t.6` Mis-Estimate, `ly-u4t.8` Stale Stats (added six **count/enum** `PlanNode` fields — sort/hash spill telemetry — contract-allowlisted, no literal). Two server-tier advisors in new `internal/advisor` pkg: `ly-u4t.12` **Index Advisor** (recommends indexes from Seq-Scan plan evidence over `normalized_condition` + table-size ranking; no HypoPG) at `/index-advisor`, `ly-u4t.16` **VACUUM Advisor** (Bloat/Performance/Activity from `table_stats`; Freezing deferred to `ly-u4t.26`) at `/vacuum-advisor`. `ly-xqf.3` **wait-event histograms** (`store.WaitEventHistogram` over `activity_buckets`, on-CPU preserved) at `/waits`. Follow-ups folded in: `ly-gu3` (ly-u4t.21→ly-hnt doc fix), `ly-egn` (port-based e2e wait strategy — **and a latent production bug it surfaced: real `auto_explain.log_format=json` emits a bare object, not an array, so `planextract.Extract` was silently dropping every real plan; fixed `decodeEnvelope` to accept both + regression test**), `ly-dfs` (partitioned `log_events` table + COPY writer + ingestion persistence, replacing the `ly-cxe.2` no-op). Suite green locally: `go test ./... -p 1` → **17 pkgs all ok** (incl. `test/e2e` now running, not skipping). **All 8 beads + 3 follow-ups closed on merge.**
>
> **Layer 2 Checks/Alerts (2026-06-09) — PR #23 MERGED (squash `3b0fcfb`):** 8 beads, subagent-driven TDD (one foreground implementer per plan, verified after each: `go build`, bounded `go test`, arch grep). **15 feature commits + 4 plans.** (1) **`ly-u4t.20` Checks engine framework** — new `internal/checks` pkg: pure `Check`/`Severity`(info/warning/critical)/`Result`/registry/`Run` engine (mirrors `internal/insight`); new partitioned `checks_results` table + `check_mutes` table + `RecentServerIDs`; an **advisory-locked `Scheduler`** that runs in the **ingestion** service (write side) on a ticker (`pg_try_advisory_lock` → one replica acts), assembles per-server input from store reads, persists results, honors muting, and dispatches via a `Notifier` seam (no-op default; the attach point for `ly-7ck.5/.6` email/Slack); a `/checks` page (api-side read of `checks_results`, mirrors `/vacuum-advisor`) + nav. (2) **`ly-u4t.26` TXID/MultiXact wraparound** (critical-safety) — new literal-free T1 `FreezeAge` proto message (ages = counts only; contract-allowlisted), gated collector `FreezeAgeReader` (`age(relfrozenxid)`/`age(datfrozenxid)`/`mxid_age`), `freeze_ages` store table + ingest routing, `WraparoundCheck` (critical ≥1.5e9, warning ≥0.5e9) registered in the engine, **plus the deferred VACUUM Advisor Freezing view** (`advisor.FreezeAdvice` at `/vacuum-advisor`). (3) **5 EXPLAIN insights** plugged into the live `internal/insight` engine, reusing existing `PlanNode` fields (no new proto): `ly-u4t.4` Large Offset, `ly-u4t.5` Lossy Bitmaps, `ly-u4t.9` Inefficient Nested Loops, `ly-u4t.10` Wrong Index (ORDER BY), `ly-u4t.11` Disk Spill (query-level work_mem rec). (4) **`ly-u4t.27` Index-Advisor notification bundle** — scheduler runs `advisor.RecommendIndexes`, `IndexAdvisorCheck` thresholds them into results. Architecture re-verified: collector outbound-only (one `pgxpool.New` in `cmd/collector`, zero in `internal/collector`, zero store imports); T1 proto contract green (FreezeAge + checks carry no literal-capable field). Suite green locally: `go test ./... -p 1` → **18 pkgs all ok**. **All 8 beads closed on merge** (`ly-u4t.20/.26/.4/.5/.9/.10/.11/.27`). `ly-u4t.26` shipped only its 2 CRITICAL wraparound checks + the Freezing view; its 3 remaining vacuum checks (insufficient frequency, blocked-by-xmin-horizon, inefficient index phase) are tracked in new follow-up **`ly-32k`** (ready — framework done). Deferred (need new collector data / window logic — own plans next): `ly-u4t.21` Queries (new-slow-query regression), `.22` Connections, `.23` Schema invalid/unused indexes, `.25` System out-of-disk, `.18` config tuning (needs `pg_settings` reader); notifications `ly-7ck.5/.6` (implement the `Notifier` seam).

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
| 2 | pganalyze parity | 🟡 **In progress** | M2–M6 epics; ~80 features in `docs/specs/2026-05-29-lynceus-features.md`. **Layer 0 merged** (PR #19: schema/table readers, insight+plan UI, capability gate, log source). **Layer 1 merged** (PR #20: 5 EXPLAIN insights, Index + VACUUM advisors, wait histograms). **Layer 2 merged** (PR #23: Checks/Alerts engine framework + TXID/MultiXact wraparound + Freezing view + 5 more EXPLAIN insights + Index-Advisor bundle); remaining Layer-2 bundles (`ly-u4t.21/.22/.23/.25`, `ly-32k`) + notifications, Layer 3 log breadth + Workstream B (HA/perf) ahead. |
| 3 | Performance review + CI tooling | 🟡 **In progress** | CI lint+bench merged (`.golangci.yml`, `lint.yml`); `ly-3na` CopyFrom writes merged; reader/writer split merged (`ly-ry1`). `ly-bsf` partition cache + `ly-awh` collector fan-out **done on branch `perf-vacuum-checks-7k3q`** (unmerged). Remaining: Workstream B (Helm/probes/retention/load-test). |
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
| M3 | `ly-u4t` | Collector-side analysis (EXPLAIN insights + Checks) | **Layers 1–2 merged.** Extraction `ly-xqf.14` + Slow Scan `.7` + surfacing `ly-hnt`; **11 EXPLAIN insights ✅** (`.7` Slow Scan, `.1` Disk Sort, `.2` Hash Batches, `.3` Inefficient Index, `.6` Mis-Estimate, `.8` Stale Stats; `.4` Large Offset, `.5` Lossy Bitmaps, `.9` Nested Loops, `.10` Wrong Index ORDER BY, `.11` Disk Spill) + Index Advisor `.12` ✅ + VACUUM Advisor `.16` ✅ (incl. Freezing view). **Layer-2 Checks (PR #23): `.20` engine framework ✅, `.26` TXID/MultiXact wraparound ✅, `.27` Index-Advisor bundle ✅.** Remaining: Checks bundles `.21` Queries / `.22` Connections / `.23` Schema / `.25` System, vacuum-checks remainder `ly-32k`, config tuning `.18` |
| M4 | `ly-cxe` | Log Insights (parsing ✅ `ly-cxe.1`; sources, PII filters, event catalog) | parsing ✅; **`ly-cxe.2` file-tail source in PR #19** (wires log events + auto_explain plans from a running collector); dir/S3/Azure, PII filters, event catalog open |
| M5 | `ly-8b0` | Auth & governance (OIDC, SCIM, audit log, enrollment) | audit log ✅ (`ly-8b0.3`) + viewer ✅ (`ly-8b0.7`); OIDC/SCIM/enrollment open |
| M6 | `ly-7ck` | HA & ops (Helm hardening, retention, cluster aggregation, public API) | open (Workstream B — not started) |
| — | `ly-xnk` | Capability discovery + per-database operator policy | discovery ✅ + policy storage ✅; **`ly-xnk.4` matrix API + `ly-xnk.3` reader gate in PR #19**; `ly-xnk.5` matrix UI open |

**Recently shipped:** `ly-xqf.1` pg_stat_activity reader + connection-state history — **done (PR #9)**: collector samples → 10s/60s aggregation → T1 `ActivityBucket` → ingestion persists → partitioned `activity_buckets`. Unblocks `ly-xqf.3` (wait events — labels already collected).

**Recently shipped:** `ly-xqf.14` auto_explain plan extraction — **done (PR #16)**: `planextract` parses JSON auto_explain bodies → normalized T1 `QueryPlan`/`PlanNode` (fail-closed condition normalizer, no literal can survive — contract test); partitioned `query_plans` table + COPY writer (`TopPlansByQuery` is the M3 read entry point); `collector.ExtractPlans` + ingestion persistence. **Unblocked all 13 M3/M6 insight beads** (`ly-u4t.1–.11`, `ly-7ck.13/.15`). Collector `main.go` not wired (no log source yet — attaches with `ly-cxe.2`).

**Recently shipped:** `ly-u4t.7` Slow Scan EXPLAIN insight — **done (PR #17)**: new neutral `internal/insight` package (pure `Detector` interface, `DetectAll`/`DetectPlans`, literal-free tree walk) is the reusable engine the other 12 M3 insight beads plug into. `SlowScanDetector` flags a Seq Scan that reads ≥1000 rows and returns ≤10% of them (severity by selectivity). Required adding `rows_removed_by_filter` (a **count**, contract-allowlisted — no literal) to T1 `PlanNode` + the extractor. Suite: **158 tests / 16 pkgs**. HTTP surfacing of insights deferred to `ly-u4t.21`/`ly-xqf.10` (engine built so those are a thin caller).

**Highest-leverage next moves** (post-Layer 1 = Layer 2 of the parity program — see `docs/specs/2026-06-08-parity-ha-perf-roadmap.md`):

1. **Layer 2 Checks/Alerts** — `ly-u4t.20` Checks engine framework (severity, scheduling, results table, gating), then bundles: pull **TXID/MultiXact wraparound** (`ly-u4t.26`, critical-safety) early; `ly-u4t.21` Queries (new-slow-query regression + advisor-insight notifications), `.22` Connections, `.23` Schema, `.25` System, `.27` Index-Advisor; notifications `ly-7ck.5/.6` (email/Slack).
2. **Remaining M3 Query-Advisor insights** (plug into the live engine like Layer 1): `ly-u4t.4` Large Offset, `.5` Lossy Bitmaps, `.9` Inefficient Nested Loops, `.10` Wrong Index (ORDER BY), `.11` Disk Spill (low work_mem); config tuning `.18`.
3. **Workstream B (HA + perf + load-testing)** — quick-plan track, **not yet started**: Dockerfiles, Helm (`ly-7ck.1`), `/healthz`+`/readyz` probes, collector graceful-shutdown flush, pgxpool tuning, retention scheduler (`ly-7ck.4`), partition cache (`ly-bsf`), reader fan-out (`ly-awh`), and a k6/bench harness + SLO targets.
4. **Layer 3 Log Insights breadth** — log sources (dir/S3/Azure `ly-cxe.3/.4/.5`), event catalog `ly-cxe.6`, PII filters `ly-cxe.7/.8/.9`.

Done & merged on `main`: `ly-xqf.1`, `ly-xqf.14`, `ly-u4t.7`, `ly-8b0.3`, `ly-cxe.1`, `ly-xnk.1`, `ly-xnk.2`; **PR #19** (Layer 0): `ly-xqf.5/.6/.10`, `ly-hnt`, `ly-xnk.3/.4`, `ly-cxe.2`; **PR #20** (Layer 1): `ly-u4t.1/.2/.3/.6/.8/.12/.16`, `ly-xqf.3`, + `ly-gu3`/`ly-egn`/`ly-dfs`.

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
- `ly-bsf` — ✅ **done (branch `perf-vacuum-checks-7k3q`, unmerged)**: per-process `sync.Map` cache of ensured weekly partition names on `pgxStats`; `EnsureWeeklyPartition`/`EnsureActivityWeeklyPartition` skip the `CREATE TABLE` round-trip on a cache hit; `DropPartitionsOlderThan` evicts to stay coherent with retention.
- `ly-awh` — ✅ **done (branch `perf-vacuum-checks-7k3q`, unmerged)**: `collector.RunBounded` (stdlib semaphore, no errgroup — a failing reader never cancels siblings); `runFull` fans out all 6 catalog readers under a global per-cycle query budget (`LYNCEUS_QUERY_BUDGET`, default 3); query-stats-failure ship-gate preserved.

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
- **2026-06-06 (reconcile)** — Cross-checked GOALS vs git log + `bd`. Parallel session's merged code (PRs #1–#6) had left its beads open; closed `ly-cxe.1`, `ly-xnk.1`, `ly-xnk.2`, `ly-8b0.7` to match `main`. Confirmed `ly-eg3`/`ly-lt9` are commit-only IDs with no bead in this Dolt DB. Corrected audit-log attribution (PR #3, not #13). Counts: **88 open / 20 closed / 42 ready**. `ly-xnk.4` now unblocked.

- **2026-06-06 (ly-xqf.14)** — Implemented auto_explain plan extraction end-to-end, TDD (5 commits, branch `auto-explain-extract-7f3a`, PR pending review): T1 `QueryPlan`/`PlanNode` proto + privacy contract test; fail-closed `planextract.NormalizeCondition` + `Extract` (JSON-only, fixtures from real PG16 EXPLAIN JSON); `query_plans` partitioned table + COPY writer + `TopPlansByQuery`; `collector.ExtractPlans` + ingestion persistence. Collector `main.go` deliberately not wired (no log source yet — attaches with `ly-cxe.2`). Full suite green: **147 tests / 15 pkgs**. Unblocks 13 M3/M6 insight beads.

- **2026-06-07 (ly-u4t.7)** — Merged `ly-xqf.14` (PR #16) + Slow Scan EXPLAIN insight (PR #17, squash, HEAD `0ff4b5c`), TDD. Built the reusable `internal/insight` engine (pure `Detector` interface, `DetectAll`/`DetectPlans`, literal-free tree walk) — the other 12 M3 insight beads plug in here. `SlowScanDetector` flags Seq Scans reading ≥1000 rows / returning ≤10% (severity by selectivity, loop-aware). Added `rows_removed_by_filter` (a count, contract-allowlisted) to T1 `PlanNode` + extractor. Suite: **158 tests / 16 pkgs**. Surfacing over HTTP deferred to `ly-u4t.21`/`ly-xqf.10`.

- **2026-06-08 (Layer 0 Foundation — PR #19)** — Brainstormed + decomposed the remaining parity work into a 4-layer program + a Workstream-B HA/perf/load-testing track (roadmap `docs/specs/2026-06-08-parity-ha-perf-roadmap.md`; Layer 0 spec `docs/specs/2026-06-08-layer0-foundation.md`). Implemented all 7 Layer 0 beads on branch `worktree-layer0-foundation-6e0b` (41 commits, 71 files), subagent-driven TDD: `ly-xqf.5` schema inventory, `ly-xqf.6` table size/growth/TOAST, `ly-u4t.21` insight HTTP surfacing (`/insights`), `ly-xqf.10` plan viz (`/plan`), `ly-xnk.4` capability matrix API, `ly-xnk.3` capability reader gate (all readers gated, fail-open, HTTP `/policy-snapshot` refresh), `ly-cxe.2` file-tail log source (wires the dormant `logparse`/`planextract`/`insight` engines into a running collector). Caught + fixed 3 issues beyond the plans: collector↔stats-DB architecture break (inventory must ship first-seen-less; ingestion persists), an e2e caller break from the gate signature change, and a stale generated `layout_templ.go`. Suite green: `go test ./...` → **13 pkgs all ok**. Collector stays outbound-only; proto contract test green. Follow-ups filed: `ly-egn` (e2e log wait-strategy), `ly-dfs` (log_events store table). **PR #19 open — close the 7 beads on merge.**

- **2026-06-08 (Layer 1 Analysis Engines)** — After PR #19 merged (`fa99c4a`), implemented all 8 Layer-1 beads on branch `worktree-layer1-engines-a3f1`, subagent-driven TDD (one foreground implementer per plan, verified after each: `go build`, bounded `go test`, arch grep). **16 commits.** (1) Five EXPLAIN insights as pure `insight.Detector`s — `ly-u4t.1` Disk Sort, `.2` Hash Batches, `.3` Inefficient Index, `.6` Mis-Estimate, `.8` Stale Stats — adding six count/enum `PlanNode` fields (sort/hash spill telemetry, contract-allowlisted). (2) `internal/advisor` pkg: `ly-u4t.12` Index Advisor (indexes from Seq-Scan plan evidence; no HypoPG) at `/index-advisor`; `ly-u4t.16` VACUUM Advisor (Bloat/Performance/Activity from `table_stats`; Freezing → `ly-u4t.26`) at `/vacuum-advisor`. (3) `ly-xqf.3` wait-event histograms (`WaitEventHistogram` over `activity_buckets`, on-CPU preserved) at `/waits`. Folded in follow-ups: `ly-gu3` (re-attributed insight surfacing `ly-u4t.21`→`ly-hnt` across GOALS + 2 specs; `ly-u4t.21` is the Layer-2 Queries Checks bundle), `ly-egn` (port-based e2e wait strategy — which **uncovered + fixed a latent production bug**: real `auto_explain.log_format=json` emits a bare object not an array, so `planextract.Extract` silently dropped every real plan; `decodeEnvelope` now accepts both, with a regression test), `ly-dfs` (partitioned `log_events` table + COPY writer + ingestion persistence, replacing the `ly-cxe.2` no-op). Architecture re-verified: collector outbound-only (one `pgxpool.New` in `cmd/collector`, zero in `internal/collector`); advisors + wait path are api-side reads; T1 proto contract green. Suite: `go test ./... -p 1` → **17 pkgs all ok** (`test/e2e` now runs, no longer skips). **Merged as PR #20 (squash `3dc464d`); all 8 beads + 3 follow-ups closed.**

- **2026-06-09 (Layer 2 reconcile)** — Synced local checkout (was 2 commits behind `origin/main`); confirmed Layer 1 landed as **PR #20** (`3dc464d`, atop PR #19 `fa99c4a`), all 11 beads already closed by the prior session. Reconciled this file's merge-state lines (header, status table, Layer 1 block, session log, next-session pointer) from "pending PR" → merged. Starting **Layer 2 Checks/Alerts** on branch `worktree-layer2-checks-engine-b7d2`.

- **2026-06-09 (Layer 2 Checks/Alerts)** — Implemented the Layer-2 keystone + parallel insight track on branch `worktree-layer2-checks-engine-b7d2`, subagent-driven TDD (one foreground implementer per plan, verified after each: `go build`, bounded `go test`, arch grep). **15 feature commits + 4 plans, 8 beads.** (1) **`ly-u4t.20` Checks engine framework** — new `internal/checks` pkg (pure `Check`/`Severity`/`Result`/registry/`Run`, mirrors `internal/insight`); partitioned `checks_results` + `check_mutes` tables + `RecentServerIDs`; **advisory-locked `Scheduler` in the ingestion service** (`pg_try_advisory_lock` → one replica evaluates+persists per tick), assembling per-server input from store reads, honoring muting, dispatching via a `Notifier` seam (no-op default; attach point for `ly-7ck.5/.6`); `/checks` page. (2) **`ly-u4t.26` TXID/MultiXact wraparound** (critical-safety) — literal-free T1 `FreezeAge` proto (ages=counts, contract-allowlisted), gated collector `FreezeAgeReader`, `freeze_ages` table + ingest routing, `WraparoundCheck` (critical ≥1.5e9 / warning ≥0.5e9), **+ the deferred VACUUM Advisor Freezing view** (`advisor.FreezeAdvice`). (3) **5 EXPLAIN insights** reusing existing `PlanNode` fields (no proto change): `ly-u4t.4` Large Offset, `.5` Lossy Bitmaps, `.9` Inefficient Nested Loops, `.10` Wrong Index (ORDER BY, keyed on `scan_direction` to not dup `.3`), `.11` Disk Spill (query-level work_mem rec, complements per-node `.1/.2`). (4) **`ly-u4t.27` Index-Advisor notification bundle** — scheduler runs `advisor.RecommendIndexes`, `IndexAdvisorCheck` thresholds them. Architecture re-verified: collector outbound-only (1 `pgxpool.New` in `cmd/collector`, 0 in `internal/collector`, 0 store imports); T1 contract green. Suite: `go test ./... -p 1` → **18 pkgs all ok**. **Merged as PR #23 (squash `3b0fcfb`); all 8 beads closed.** `ly-u4t.26` delivered only its 2 CRITICAL wraparound checks + the Freezing view; its 3 remaining vacuum checks are tracked in follow-up `ly-32k` (ready). Deferred (own plans next): `ly-u4t.21` Queries regression, `.22` Connections, `.23` Schema invalid/unused indexes, `.25` System out-of-disk, `.18` config tuning (needs a `pg_settings` reader); notifications `ly-7ck.5/.6` implement the `Notifier` seam.

- **2026-06-09 (Layer 2 docs reconcile)** — After PR #23 merged (`3b0fcfb`), reconciled this file's merge-state lines (header, status table, M3 row, Layer 2 block, session log, next-session pointer) from "pending PR" → merged, and recorded follow-up `ly-32k` (3 remaining vacuum checks of the `ly-u4t.26` bundle).

- **2026-07-08 (perf + vacuum checks bundle)** — Branch `perf-vacuum-checks-7k3q` off `origin/main` @ `0f644a9` (#34; note this file's header still reflects #23 — intermediate PRs #24–#34, incl. Connections/Schema checks, Fleet entity model, dogfood UI, and P0 foundation, landed via other sessions and are not yet reconciled here). Workflow-orchestrated, subagent-driven TDD (scout → sequential implement → verify), 3 beads: **`ly-bsf`** per-process `sync.Map` weekly-partition cache (+ drop eviction) — storage-side only, no wire change; **`ly-awh`** `collector.RunBounded` bounded reader fan-out + global query budget (`LYNCEUS_QUERY_BUDGET`, default 3), `runFull` now fans out all 6 catalog readers, ship-gate preserved; **`ly-32k`** 2 of 3 remaining vacuum checks — `vacuum.insufficient_frequency` (pure check over `table_stats`, autovacuum-trigger model) and `vacuum.xmin_horizon` (full literal-free FreezeAge-template path: new T1 `XminHorizon` proto {age count + fixed `holder_kind` label} → gated cluster-global `XminHorizonReader` → ingest persist → `xmin_horizon` partitioned table (migration `0013`) → scheduler → `XminHorizonCheck`; contract test extended). Check (3) inefficient index-cleanup phase **deferred** (only signal is live `pg_stat_progress_vacuum`, not in `table_stats`) → new follow-up bead **`ly-08j`**. Full suite green on real Postgres: `go test ./... -p 1` → **17 pkgs all ok** (store 93s, e2e 4.9s); collector outbound-only re-verified (0 `pgxpool.New` / 0 store imports in `internal/collector`); T1 contract green. **Branch unmerged — PR pending.**

> **Next-session start here:** Layer 0 (PR #19) + Layer 1 (PR #20, `3dc464d`) + **Layer 2 (PR #23, `3b0fcfb`)** all merged; their beads closed. The Checks framework (`internal/checks` + advisory-locked scheduler in ingestion + `checks_results`/`check_mutes` + `/checks` page + `Notifier` seam) now **unblocks the remaining Checks bundles** — highest-leverage next: `ly-u4t.21` Queries (new-slow-query regression — needs two-window `query_stats` comparison + first-seen; advisor-insight half can reuse the `.27` "scheduler-assembles → check-thresholds" pattern with `insight.DetectPlans`), `.22` Connections (needs per-connection duration / idle-in-tx / blocking — `activity_buckets` are aggregated, so likely needs a new reader or `ly-xqf.2/.4`), `.23` Schema invalid/unused indexes (needs per-index validity/usage — `ly-xqf.7`), `.25` System out-of-disk (needs a disk-space metric/reader), and `ly-32k` (the 3 remaining vacuum checks of the `.26` bundle — (1)/(3) from `table_stats`, (2) needs an xmin-horizon reader). Then **notifications** `ly-7ck.5` (email) / `ly-7ck.6` (Slack) implement the `Notifier` interface (`internal/checks/scheduler.go`). `ly-u4t.18` config tuning needs a `pg_settings` reader (own plan). **Workstream B** (HA/perf/load-testing) still not started. Run `bd ready` for the full set. **CI on `main` still red per `ly-1zw`** (environmental — gitleaks license, golangci-lint toolchain, dependency-graph, testcontainers `LogStrategy` flake in `TestActivityReader_seesDistinctConnectionStates`); trust local `go test ./... -p 1`.

## How to pick this up next session

```bash
# 1. Hydrate
git pull && bd bootstrap

# 2. Read state
cat docs/GOALS.md          # this file
bd ready                         # unblocked work
bd query 'label = "ready-impl"'  # planned features

# 3. Verify MVP still green
go test ./... -timeout 15m

# 4. Layers 0–2 merged (#19/#20/#23). Pick remaining Layer-2 Checks bundles: ly-u4t.21 (Queries) / .22 (Connections) / .23 (Schema) / .25 (System) / ly-32k (vacuum-checks remainder), notifications ly-7ck.5/.6 (Notifier seam), config tuning ly-u4t.18. Or Workstream B (HA/perf) / Layer 3 (log breadth).
bd update <id> --claim
```

**Update protocol:** when you close a goal-relevant bead or land perf/security work, edit the "Status at a glance" table + the relevant section here in the same change. Keep this file truthful — it is the contract for resuming.
