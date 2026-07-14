# ADR: ClickHouse Stats Store + Bedrock Diagnosis Layer

- **Status:** Proposed (decision-ready)
- **Date:** 2026-07-14
- **Amends / partially supersedes:** [ly-22u — Grafana + ClickHouse Pivot Evaluation](./grafana-clickhouse-pivot.md), on the *ClickHouse-as-store* axis only. Everything ly-22u protects about the audit/T2 boundary is **kept**.
- **Decision drivers:**
  1. Adopt ClickHouse as the canonical time-series stats store, and stand up an AWS Bedrock diagnosis layer over it — without destroying the privacy differentiator.
  2. Two facts changed since ly-22u made CH-as-store defensible where it wasn't before (see §1).

---

## 0. TL;DR

Adopt **ClickHouse as the canonical stats store for BOTH tiers**, **customer/org-operated**, connected **direct by default** (AWS Firehose is an *optional ingestion-protection shim* in front of CH at scale, not a required tier). Normalization moves **off the collector edge** and becomes a **ClickHouse materialized view + RBAC** over raw data the org already holds — and that **same materialized-view split is the T1/T2 boundary**: the raw table is T2 (literal-bearing), the MV's normalized target table is T1. A central, **additive** analysis layer runs deterministic detection plus **AWS Bedrock exploratory correlation** over the T1 target (and the org's existing app logs). Literal-bearing (T2) access stays behind the **existing `T2Reader` gateway** — audit-first, fail-closed, RBAC-gated — reading the **isolated CH raw table**, with the **audit log remaining in vanilla Postgres** (hash-chain). The Postgres **stats** store is retired; Postgres remains only for the config/audit DB (always vanilla).

The privacy claim **rescopes** from *"literals never leave your infra, not even to us"* to *"Lynceus only touches normalized data (RBAC-enforced), and any literal access is role-gated and audited."* This is weaker than the original pitch but honest and defensible given raw data already lives in the org's ClickHouse. **The org must consciously buy that rescoped claim** — it is the crux of this ADR.

---

## 1. What changed since ly-22u

ly-22u rejected ClickHouse-as-default for three reasons. Two are now scoped away; one is preserved by design.

| ly-22u objection | Status now |
|---|---|
| **No AWS-managed CH → breaks "vanilla Postgres on RDS/Aurora, no extensions" portability** | **Scoped away.** The org accepts an **AWS-native-default hybrid** deployment, and CH here is **customer/org-operated** — raw data (incl. application logs) *already lives in the org's ClickHouse*. CH is OSS and runs anywhere, so "direct-to-CH" is itself the portable path. Firehose is the only AWS-specific piece and is optional/scale-only. |
| **Audit/T2 cannot live in ClickHouse (no SELECT trigger, mutable `query_log` = literal sink, RBAC only blocks/filters)** | **Preserved, not challenged.** The **authoritative audit log stays in vanilla Postgres** (hash-chain). T2 reads stay behind the Go `T2Reader` gateway. CH holds *data* (and at most a non-authoritative audit mirror, §7.5), never the authoritative record, never the sole enforcement point. |
| **`store.Stats` interface doesn't exist — CH backend is two projects (extract interface, then impl)** | **Obsolete.** The interface **now exists** (`internal/store/stats.go:15`, `var _ Stats = (*pgxStats)(nil)`). A CH backend is now a **single new `Stats` implementation**, not an extraction project. |

**Net:** ly-22u's verdict ("CH is the wrong *default*") was correct *for the product it evaluated* — a portable, RDS-first, Lynceus-operated store. This ADR describes a **different deployment reality** (customer-operated CH the org already runs) where the portability objection dissolves and the audit objection is honored by keeping audit in Postgres.

---

## 2. Decisions (locked)

1. **ClickHouse is the canonical stats store for both tiers** (T1 and T2), customer/org-operated, **direct connection by default** (dev, test, and prod). Firehose is inserted in front of CH **only when ingestion needs protection at scale** — an optional shim, not a transport tier. The Postgres **stats** store is **retired entirely** — both T1 and T2 live in ClickHouse (see #2, #6). Postgres is retained only for the config/audit DB. The `Stats` interface still permits a PG stats impl, but it is no longer the default.
2. **Edge normalization is dropped.** Literal-stripping is **not** performed on the zero-touch collector. Normalization is a **ClickHouse materialized view** over the raw table, with **RBAC** granting Lynceus's role the derived (normalized) target table only — raw denied. **This MV is also the tier boundary:** the raw table = T2 (literal-bearing, gateway-only), the MV's normalized target table = T1 (broadly readable). (See §4 for why a plain view is insufficient and why a materialized view is required.)
3. **Analysis is additive.** Collector keeps its cheap edge **detectors** (now detection only, not normalization). A central layer adds, over the normalized view: (a) Lynceus **deterministic** detection (static thresholds, known-issue signatures), and (b) **AWS Bedrock** exploratory correlation.
4. **Bedrock = exploratory correlation, not authority.** Bedrock surfaces *possibly-relevant* cross-signal correlations across the normalized view and the org's app logs. It complements — does not replace — deterministic detection. Any literal (T2) access by Bedrock goes **only** through the audited `T2Reader` path.
5. **api_server ↔ collector control plane = persistent WebSocket** for realtime and on-demand needs; REST only for non-time-sensitive operations.
6. **T2 (literal-bearing) is the CH raw table behind the normalization MV.** It is reachable **only** by the `T2Reader` gateway (fast-reject → authorize → audit-first-fail-closed → sole SELECT) using isolated CH credentials; every other consumer (analysis, Bedrock, operators) sees only the T1 target. The raw table is **TTL-bounded** (short retention) so the literal-custody window stays small; reads are on-demand (analysis needs detail, or a human runs a direct query) and every read is audited to Postgres. For deployments wanting *zero* literals at rest, the raw table can use a `Null`/ultra-short-TTL engine — at the cost of no retrospective T2.
7. **Config/audit DB is vanilla Postgres, always.** The hash-chained, advisory-lock-serialized, append-only **authoritative** audit log cannot move to ClickHouse and does not (a non-authoritative CH mirror is allowed — §7.5). **Postgres scope shrinks to exactly two things: config + the audit log** (plus the dev monitored target). Derived, T1-grade outputs — **detections, analysis results, LLM-agent results** — carry no tamper-evidence requirement and MAY live in ClickHouse (colocated with the stats they summarize, Bedrock-readable) or in PG for relational joins with config. One caveat: **LLM-agent output that quotes a T2 literal inherits T2 gating** (isolated raw-table + audited read).

---

## 3. Architecture

```
CUSTOMER / ORG TRUST BOUNDARY  (raw data already lives here — org's own posture)
  Postgres ─▶ collector
              ├─ edge detectors (kept — detection, NOT normalization)
              ├─ persistent WebSocket ◀▶ api_server   (control, realtime, on-demand T2)
              └─ ship logs + metrics ─▶ ClickHouse
                    • direct connection            (default: dev / test / prod)
                    • Firehose ─▶ ClickHouse       (optional shim: ingestion protection at scale)

  ClickHouse (org-operated) — the SOLE stats store (T1 AND T2)
    ├─ RAW table  (PG logs + metrics + app logs)    = T2 source, literal-bearing
    │     access: T2Reader gateway ONLY (isolated CH creds); TTL-bounded
    └─ NORMALIZED materialized-view target (T1)     ← Lynceus + Bedrock read THIS
          │   (CH RBAC: analysis/Bedrock roles granted T1 target only; RAW denied)
          ├─ Lynceus deterministic detection (thresholds / known-issue signatures)
          ├─ AWS Bedrock exploratory correlation (T1 target + app logs)
          └─ existing SSR product surface (flamegraph, EXPLAIN detectors, advisors, checks)

  T2 / literal access (Lynceus-served): on-demand read (analysis-needs-more OR human direct query)
    api_server ─▶ T2Reader gateway
       ├─ fast-reject on servers.t2_enabled
       ├─ authorize via EffectiveCapability (config DB)
       ├─ AppendAuditReturning FIRST — fail closed (audit row → Postgres hash-chain)
       └─ sole SELECT of literals from ISOLATED CH RAW table (TTL'd)  ─▶ operator / Bedrock

  config / audit DB = vanilla Postgres, ALWAYS (hash-chain, VerifyChain) — the only remaining PG role
```

**Data classes in ClickHouse (both tiers, one store):**
- **Raw table (T2 / literal-capable):** the ingested raw data (PG logs, metrics, app logs). Literal-bearing, so it is **T2** — reachable **only** by the `T2Reader` gateway (isolated CH credentials), TTL-bounded, every read audited to Postgres. This is also the MV's source.
- **Normalized materialized-view target (T1):** what Lynceus and Bedrock read. Literal-free by construction of the MV; enforced by RBAC granting the T1 target and **denying the raw table** to analysis/Bedrock roles.
- **App logs:** already present in the org's CH, org's own posture. Read for correlation but not covered by Lynceus's privacy claim.

---

## 4. Privacy model & guardrails (the enforceable line)

The differentiator is **not** "where normalization runs." It is: *does raw, literal-bearing data come to rest in a store that is outside the customer's trust boundary or broadly readable, with reads that escape audit?* Redaction-at-presentation controls the **UI view**, not the data at rest — it is not a privacy boundary by itself. These guardrails are what keep the claim defensible:

1. **Normalization is enforced, not conventional.** A plain ClickHouse `VIEW` still requires `SELECT` on the underlying raw table at query time — granting the view implicitly grants raw. Use a **materialized view** (raw → derived normalized table) and grant Lynceus's CH role the **derived table only, raw denied**. Then Lynceus (and Bedrock-via-Lynceus) *cannot* reach literals except through the audited gateway.
2. **The `T2Reader` gateway is the sole path to literals** for anything Lynceus serves. It already exists (`internal/store/t2_read.go`): fast-reject → `EffectiveCapability` authorize → **audit append FIRST, fail closed** → the *only* literal-returning SELECT. Extending T2 to CH means the CH T2 read becomes that sole SELECT; the gateway ordering is unchanged. **Bedrock included** — it never queries CH-T2 directly.
3. **The authoritative audit record stays in vanilla Postgres** (hash-chain + `VerifyChain`). ClickHouse cannot host the *authoritative* log: no SELECT trigger, no serialized append, admin-mutable, and `query_log` stores literal query text (a literal sink). A best-effort **async mirror** may live in CH for analytics (§7.5), but it is never the source of truth. The T2 literal *data* lives in CH (the raw table).
4. **The raw (T2) table in CH is network-isolated** to the api_server/gateway (separate CH database/cluster or a dedicated gateway role; no other client credentials; no `clickhouse-client`, no second datasource, no Grafana). Analysis/Bedrock roles are granted the **T1 target only**; the raw table is denied to them. If any other path to the raw table exists, audit-on-read is theater. **This is the single most important guardrail now that literals are at rest in CH** — the earlier "keep T2 in PG" fallback is gone.
5. **CH `query_log` is disabled or scrubbed on the raw-table path**, and the raw table is **TTL'd** (short retention) so the literal-custody window is bounded. Note: the gateway's own SELECT is structural (no customer literals in the *query text*), but a human "direct query" can carry literals into `query_log` — so scrub it regardless. If Firehose ever fronts a literal path (it should not), its S3 source-record backup and any Lambda-transform logs must be disabled — those are shadow literal sinks.

**Positioning consequence (state it plainly to the org):** the claim rescopes from *"literals never leave your infra, not even to us"* to **"Lynceus only touches normalized data (RBAC-enforced); any literal access is role-gated and audited."** Honest and defensible because raw already sits in the org's own ClickHouse — but it is a **different, weaker claim** than the original pganalyze-killer line. Marketing and the privacy page must reflect the rescoped claim; do not advertise the stronger one.

---

## 5. Current-state facts (verified 2026-07-14)

These correct the stale `candidate-target-architecture` memory / ly-22u appendix:

- **`store.Stats` interface EXISTS** — `internal/store/stats.go:15`, ~40 reader/writer methods, `var _ Stats = (*pgxStats)(nil)`, `NewStats(pool) *pgxStats`. The seam ly-22u wanted is **done**. A CH backend is a single new `Stats` impl (e.g. `chStats`).
- **The enforceable T2 audit-on-read gateway EXISTS** — `internal/store/t2_read.go` `T2Reader`: fast-reject on `servers.t2_enabled` → `EffectiveCapability` → **`AppendAuditReturning` FIRST, fail-closed** → `ReadQueryStatsTier2` (the *only* `data_tier=2` SELECT). Audit row is written to the **config DB** via `pgxConfig`. This is precisely the "api_server sole T2 gateway" pattern the design relies on — already built for the PG backend.
- Audit writers now span real read/write paths (`console.go`, `t2_read.go`, `saved_scripts.go`, `capability_policy.go`) — audit-on-read is no longer only an admin write.
- T1 reads still hardcode `data_tier = 1` (`freeze_ages.go`, `checks_results.go`, etc.); T2 rows are served exclusively by `ReadQueryStatsTier2` via the gateway. **Today `ReadQueryStatsTier2` reads the PG `query_stats` table; the CH impl (#3) retargets that single SELECT at the isolated CH raw table — the gateway ordering (audit-first, fail-closed) is unchanged.**
- Config/audit hash-chain relies on Postgres `pg_advisory_xact_lock` + append-only triggers + `BIGSERIAL`; **must stay vanilla Postgres**.

---

## 6. Program decomposition

This is a **program**, not one spec. Each sub-project gets its own spec → plan → implementation. Ordered by de-risk, dependency, and unlock-Bedrock. `✔` = already built.

| # | Sub-project | Depends on | Notes |
|---|---|---|---|
| 0 | **This ADR** (org decision artifact) | — | The actual current blocker is organizational. This is the thing to circulate. |
| 1 | ~~Extract `store.Stats` interface~~ | — | **✔ Done** (`stats.go:15`). |
| 2 | ~~Enforceable `T2Reader` audit-on-read gateway~~ | 1 | **✔ Done** for PG (`t2_read.go`). Design extends it to CH. |
| 3 | **ClickHouse `Stats` implementation** (`chStats`) | 1 | Implement the T1 read/write methods against CH. `ReadQueryStatsTier2` retargets the isolated CH raw table (§7 resolved — T2 is in CH, not PG). |
| 4 | **Normalized materialized view + CH RBAC** | 3 | The enforced normalization boundary **and the T1/T2 split** (§4.1): raw table = T2 (gateway-only), MV target = T1. Analysis/Bedrock roles granted the T1 target only; raw denied. |
| 5 | **Direct-connect ingestion path** (+ optional Firehose shim) | 3 | Collector ships to CH direct by default; Firehose inserted only under ingestion pressure. |
| 6 | **Persistent WebSocket control plane** (api_server ↔ collector) | — | Realtime + on-demand T2 read requests. REST retained for non-timely ops. |
| 7 | **Isolated CH raw table + gateway extension** | 3, 6 | `T2Reader`'s sole SELECT targets the isolated CH raw table; TTL; `query_log` scrubbed; audit stays PG. |
| 8 | **Central deterministic detection over the T1 target** | 4 | Thresholds / known-issue signatures; additive to edge detectors. |
| 9 | **Bedrock exploratory correlation layer** | 4, 8 | T1 diet by default (T1 target + app logs). T2 only via the §7 audited path. The payoff. |
| 10 | **Retire the PG stats store** | 3, 7 | Once T1+T2 read/write against CH, decommission `stats-db`. Postgres remains for config/audit + the dev monitored target. Do **not** remove before #3/#7 land — `pgxStats` carries the load until then. |

---

## 7. Open questions / review items

1. **Materialized view vs plain view + RBAC grants.** Confirm the CH RBAC model that makes "Lynceus sees normalized only" *enforceable* (materialized view + derived-table-only grant), not merely conventional. This is the guardrail a security reviewer will probe first.
2. **App-log classification.** The org's application logs already sit in CH and may carry PII/literals. They are the *org's* posture, not Lynceus's contract — but Lynceus/Bedrock reading them must not be advertised as literal-free. Define what "app logs" covers and how Bedrock correlation treats them.
3. **T2 storage location — RESOLVED (2026-07-14): T2 lives in ClickHouse.** Both tiers share one store: the raw table (T2) sits behind the normalization MV, reachable only by the `T2Reader` gateway; the MV's normalized target is T1. The Postgres stats store is retired (Postgres remains only for config/audit + the dev target). This *reverses* the earlier "keep T2 in PG" recommendation — accepted deliberately, with the §4 guardrails (raw-table isolation, audit-in-PG, `query_log` scrub, TTL) as the price of admission. **Remaining follow-up:** pick the raw-table retention policy — a short TTL (retrospective T2, bounded custody) vs a `Null`/ultra-short engine (zero literals at rest, no retrospective T2).
4. **Rescoped privacy claim sign-off.** Product/marketing/legal must explicitly accept the §4 rescoped claim before any external messaging.
5. **Audit log in ClickHouse — RESOLVED (2026-07-14): PG authoritative, CH optional mirror (dual-write).** Moving the audit log to CH was considered (to shrink PG) and rejected as the *authoritative* store — CH cannot provide serialized append, append-only enforcement, truncation-resistance, or synchronous audit-first-fail-closed (see §8). **Approved compromise — dual-write:** the audit path commits to **Postgres first, synchronously, fail-closed** (unchanged; `VerifyChain` runs on PG), then **best-effort mirrors the row into a CH `audit_log` table** for analytics/colocation. The CH write is asynchronous and **never gates the read** — a failed mirror does not fail the request. **Asymmetric convergence:** evaluate the mirror's usefulness; redundancy can be cleaned up only by **dropping the CH mirror**, never PG. "CH is sufficient, drop PG" is not a technical outcome — it would mean abandoning tamper-evident audit, i.e. a *separate, conscious* compliance/claim decision, not a convergence. Prefer feeding the mirror via a PG→CH outbox/CDC over hot-path dual-write to avoid dual-write consistency bugs.

---

## 8. What ly-22u still gets right (unchanged invariants)

- api_server / the Go store gateway is the **sole enforceable T2 path**; audit-on-read is atomic (audit-before-read, fail-closed).
- The **authoritative, tamper-evident audit log lives in Postgres**; ClickHouse never hosts the *authoritative* log (a non-authoritative async mirror for analytics is allowed — see §7.5).
- ClickHouse RBAC **blocks/filters but cannot audit a read** — so it is never the enforcement point, only isolated storage behind the gateway.
- `query_log` stores literal query text and is mutable — **never** the audit source; scrub/disable it on any literal path.

The one thing this ADR changes from ly-22u is the **deployment premise**: a *customer-operated* ClickHouse the org already runs, with a *consciously rescoped* privacy claim. With that premise, ClickHouse-as-store is sound. Without it (a Lynceus-operated, multi-tenant CH holding raw literals with UI-only redaction), ly-22u's rejection still stands — and that path must not be taken by accident.
