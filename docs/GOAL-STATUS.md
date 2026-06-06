# Lynceus — Goal Status Tracker

> **Purpose:** Single living status file for the overarching product goal, so any session (or machine) can pick the work back up. Update this file whenever a goal sub-item advances. Companion to the dated session handoffs in `docs/superpowers/`.
>
> **Last updated:** 2026-06-05 · **Repo HEAD at update:** see `git log` · **Branch:** `goal-status-ci-3f9a`

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
| 2 | pganalyze parity | 🟡 **In progress** | M2–M6 epics; ~80 features in `docs/specs/2026-05-29-lynceus-features.md`. 87 open beads. |
| 3 | Performance review + CI tooling | 🟡 **In progress** | CI perf/lint workflows being added; per-hotspot review tracked in beads (see below). |
| 4 | Security review + CI tooling (HITRUST) | 🟡 **In progress** | SAST/SCA/secret/container scanning workflows being added; HITRUST control mapping below. |

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
| M3 | `ly-u4t` | Collector-side analysis (EXPLAIN insights) | blocked on M2 `ly-xqf.10` auto_explain → `ly-cxe.1` (done) |
| M4 | `ly-cxe` | Log Insights (parsing ✅ `ly-cxe.1`; sources, PII filters, event catalog) | parsing done; rest open |
| M5 | `ly-8b0` | Auth & governance (OIDC, SCIM, audit log, enrollment) | open |
| M6 | `ly-7ck` | HA & ops (Helm hardening, retention, cluster aggregation, public API) | open |
| — | `ly-xnk` | Capability discovery + per-database operator policy | `ly-xnk.1` ready |

**Recently shipped:** `ly-xqf.1` pg_stat_activity reader + connection-state history — **done (PR #9)**: collector samples → 10s/60s aggregation → T1 `ActivityBucket` → ingestion persists → partitioned `activity_buckets`. Unblocks `ly-xqf.3` (wait events — labels already collected).

**Highest-leverage next moves** (long-reach unblocks):

1. `ly-xqf.10` auto_explain plan extraction — now unblocked (log parsing `ly-cxe.1` done); cascades into all 8 M3 EXPLAIN insights.
2. `ly-xqf.3` wait-event histograms — read path over the `activity_buckets` data already collected by `ly-xqf.1` (no new schema/wire).
3. `ly-xnk.1` capability discovery — gates per-DB operator policy + safe feature enablement on RDS.

Planned (have TDD plans in `docs/superpowers/plans/`): `ly-xqf.1`, `ly-xqf.5`, `ly-8b0.3`, `ly-cxe.1`(done).

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

- `ly-17l` — bump Go toolchain to clear the 2 reachable stdlib vulns (patch/vuln mgmt 10.m).
- `ly-cli` — enforce TLS in transit: collector websocket (wss) + pgx `sslmode=require`/`verify-full` on RDS.
- `ly-kwk` — ✅ **done**: HITRUST control-to-evidence mapping doc at [`docs/security/hitrust-controls.md`](security/hitrust-controls.md).
- Tamper-evident audit log writer (`ly-8b0.3`, plan written) — audit-trail integrity (09.aa).
- Scoped collector token issuance + rotation (`ly-8b0.8`); RBAC + least privilege (M5 `ly-8b0`).
- Secrets management (no plaintext creds) — gitleaks gate now enforces.

**Status:** scanning workflows added (`security.yml`, `dependency-review.yml`); local scans run; findings filed under `ly-1g1`.

---

## Session log

- **2026-06-05** — Verified MVP (Goal 1, 62 tests). Established this tracker + security/perf CI tooling (PR #7). Filed perf/security review epics (`ly-69x`, `ly-1g1`). Shipped `ly-3na` CopyFrom write path (PR #8) and `ly-xqf.1` pg_stat_activity connection-state history end-to-end (PR #9). Suite now 70 tests. Wrote HITRUST control-evidence doc (`ly-kwk` ✅) and recorded the `ly-ry1` reader/writer endpoint decision (satisfied at service boundary). Open PRs: #7 (docs+CI+HITRUST), #8 (perf), #9 (feature) — awaiting merge.

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

# 4. Pick highest-leverage: ly-xqf.10 (auto_explain) or ly-xqf.1 (pg_stat_activity)
bd update <id> --status in_progress
```

**Update protocol:** when you close a goal-relevant bead or land perf/security work, edit the "Status at a glance" table + the relevant section here in the same change. Keep this file truthful — it is the contract for resuming.
