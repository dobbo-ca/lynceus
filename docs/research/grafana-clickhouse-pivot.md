# ADR: Grafana + ClickHouse Pivot Evaluation

- **Status:** Proposed (decision-ready)
- **Date:** 2026-06-19
- **Author:** Principal architect (synthesis of 5 research strands + independent verification)
- **Decision driver (the CRUX):** Can mandatory audit-on-read for T2 + data-tier RBAC be **enforced** (not theater) when a generic tool (Grafana) queries a generic store (ClickHouse)?

---

## 0. TL;DR

**No.** The CRUX fails. Mandatory, tamper-evident audit-on-read for T2 plus data-tier RBAC **cannot be enforced** as a system property when Grafana queries ClickHouse directly. Both halves of the stack lack the primitive:

- **ClickHouse** has RBAC that *blocks/filters* (row policies + column `GRANT`) but has **no SELECT trigger and no way to make an audit-write a precondition of a read**. Authorization and audit are decoupled subsystems. Its only native read-audit, `system.query_log`, is best-effort, async, admin-disableable/mutable, ~30-day default, **and stores the raw query text — making the audit log itself a T2 literal sink**, directly inverting the literal-free backbone. (ClickHouse docs: [query_log](https://clickhouse.com/docs/operations/system-tables/query_log), [database-audit-log](https://clickhouse.com/docs/cloud/security/audit-logging/database-audit-log), [data-masking](https://clickhouse.com/docs/cloud/guides/data-masking); cross-user leak [issue 17766](https://github.com/ClickHouse/ClickHouse/issues/17766).)
- **Grafana** is a generic SQL pass-through. Its ClickHouse datasource "executes queries exactly as written and does not validate or restrict SQL," uses a **single shared service credential**, and per-viewer identity rides only the **unverifiable, spoofable `X-Grafana-User` header** (HTTP-only, "should not be relied upon for security decisions"). Data-source permissions, RBAC, and audit logging are **all Enterprise/Cloud-paid**; in OSS any Viewer can run arbitrary queries via `/api/ds/query`. Even Enterprise audit logs default to **excluding query bodies** (v13, Apr 2026) and are bypassable via Explore, alerting, query-cache, and public dashboards. ([plugin config](https://grafana.com/docs/plugins/grafana-clickhouse-datasource/latest/configure/), [data-source-management](https://grafana.com/docs/grafana/latest/administration/data-source-management/), [audit-grafana](https://grafana.com/docs/grafana/latest/setup-grafana/configure-security/audit-grafana/), [X-Grafana-User trust thread](https://community.grafana.com/t/can-i-trust-the-x-grafana-user-header-value/89635), [CVE-2022-21673 / GHSA-8wjh-59cw-9xh4](https://github.com/grafana/grafana/security/advisories/GHSA-8wjh-59cw-9xh4), [issue 114769](https://github.com/grafana/grafana/issues/114769).)

The only enforceable design is to **forbid Grafana from touching T2 at all** and keep every T2 read behind a bespoke audited gateway — which is exactly the `api_server` Lynceus already has. At that point "Grafana querying the store" is no longer the architecture, and the pivot's premise dissolves.

Two further hard facts make the full pivot worse than a clean swap:
1. **No pluggable seam exists today.** There is no `store.Stats` / `store.Config` Go interface — both are concrete structs over `*pgxpool.Pool` (verified: `internal/store/stats.go:25` `NewStats(pool *pgxpool.Pool)`, no `type Stats interface` anywhere). The "TimescaleDB optional backend behind the interface" is documentation aspiration. A ClickHouse backend is therefore **two projects**: extract the interface from ~12 reader/writer method sets, then build a second impl.
2. **ClickHouse is not RDS/Aurora.** There is no AWS-first-party managed ClickHouse; the vendor positions it as an *alternative* to RDS. Adopting it breaks the "both DBs are vanilla Postgres on RDS/Aurora, no extensions" portability promise. ([no AWS-native ClickHouse](https://clickhouse.com/resources/engineering/aws-rds-alternatives), [Amazon RDS engine list](https://docs.aws.amazon.com/AmazonRDS/latest/UserGuide/Welcome.html), [Tinybird: no native managed CH on AWS](https://www.tinybird.co/blog/managed-clickhouse-on-aws).)

**Recommendation: HYBRID.** Keep the custom SSR product surface and the api_server-as-sole-T2-gateway. Optionally offer **T1-only, read-only Grafana dashboards** as an *export/embed* convenience (a generic-store-friendly view that physically never contains a literal and never touches the audited tier). Do **not** adopt ClickHouse as the default/primary stats store and do **not** put T2 behind Grafana. If columnar scale is ever needed, first extract the `store.Stats` interface (good hygiene regardless) and treat ClickHouse as an opt-in T1 backend only — with eyes open that "no feature depends on it" forfeits ClickHouse's headline features (native TTL tiering, etc.).

---

## 1. The CRUX, in detail

### Verdict: **NO** — not enforceable as a system property in a Grafana + ClickHouse stack.

Lynceus's invariant is *atomic*: every T2 literal read must be **both RBAC-authorized AND written to a tamper-evident audit record**, with **no unaudited path**. Today this holds because every read funnels through a Go store method (the natural choke point), `data_tier=1` is hardcoded in SQL (verified: 4 hardcoded `data_tier = 1` filters across `internal/store/*.go`; no read path ever selects tier != 1), and the audit writer is a hash-chained, append-only, advisory-lock-serialized log with `VerifyChain` in the **config DB** (not the stats DB). The only production caller of `AppendAuditReturning` today is `store.SetCapabilityPolicy` (an admin write) — so audit-on-*read* is currently *design intent*, but the enforcement choke point (Go store method) and the tamper-evident writer both already exist and work.

Grafana + ClickHouse breaks the atomic coupling three structural ways:

**(1) ClickHouse RBAC only blocks/filters — it cannot audit a successful read.**
Row policies and column `GRANT SELECT` are real and server-enforced (a Grafana account *can* be denied T2 columns, even under `SELECT *`). But "denied a column" is not "T2-readable-when-gated AND every read audited." An *allowed* read is not tied to any mandatory audit write. There is no SELECT trigger, no on-grant hook, no mechanism that makes an audit INSERT a transactional precondition of returning a row. ([row-column-policy KB](https://clickhouse.com/docs/knowledgebase/row-column-policy)) — *Independently confirmed.*

**(2) `system.query_log` cannot be the audit source.**
It is (a) disableable per-query/per-session (`SETTINGS log_queries=0`), (b) sampled/thresholded (`log_queries_probability`, `log_queries_min_query_duration_ms`), (c) async/buffered (lost on crash before flush; not transactional with the read), (d) a mutable MergeTree the admin can `ALTER`/`TRUNCATE` (no hash chain, no `VerifyChain` equivalent), (e) ~30-day default retention, (f) historically cross-user-leaky ([issue 17766](https://github.com/ClickHouse/ClickHouse/issues/17766)), and — decisively — **(g) it stores the full literal query text**, so using it as the T2 audit log turns the audit trail into the *largest unencrypted literal store in the system*, with weaker access control than the data it protects. Masking is best-effort regex, log-only, and explicitly does **not** mask query results. ([query_log](https://clickhouse.com/docs/operations/system-tables/query_log), [query-level settings](https://clickhouse.com/docs/operations/settings/query-level), [data-masking](https://clickhouse.com/docs/cloud/guides/data-masking)) — *Independently confirmed.*

**(3) Grafana is a generic pass-through with first-class unaudited paths.**
"Grafana executes queries exactly as written and does not validate or restrict SQL"; non-read statements run if the DB user allows them. A single shared read-only account serves all users. Per-viewer identity exists only via `X-Grafana-User`, which Grafana itself says must not be relied on for security decisions, is HTTP-protocol-only (Native protocol drops it), is browser-spoofable absent a header-stripping proxy, and has a real CVE where the *wrong* user's OAuth identity was forwarded ([GHSA-8wjh-59cw-9xh4](https://github.com/grafana/grafana/security/advisories/GHSA-8wjh-59cw-9xh4)). Unaudited / un-RBAC'd bypass paths confirmed:
- **`/api/ds/query`** — a Viewer runs arbitrary SQL with a session cookie even when Explore UI is restricted ([issue 114769](https://github.com/grafana/grafana/issues/114769)).
- **Explore** — free-form arbitrary queries; "any query the data source's credentials allow is allowed to be sent."
- **Alerting / recording rules** — run server-side with **no end-user identity**, so user-keyed row policies don't apply and no per-viewer audit is possible.
- **Query caching** (Enterprise) — serves cached results without re-hitting the store, and historically leaked cross-user (CVE-2022-23498); a store-level audit hook never fires on a cache hit.
- **Public / anonymous dashboards & snapshots** — render data with no per-user identity.
([plugin config](https://grafana.com/docs/plugins/grafana-clickhouse-datasource/latest/configure/), [data-source-management](https://grafana.com/docs/grafana/latest/administration/data-source-management/), [security best-practices blog](https://grafana.com/blog/2024/05/06/data-source-security-in-grafana-best-practices-and-what-to-avoid/)) — *Independently confirmed.*

### Edition / licensing the (failed) approximation would require
Even to *approximate* the differentiator you must buy in:
- **Data-source permissions, RBAC custom/fixed roles, app-plugin RBAC, audit logging, SAML, team sync, query caching** are **all Grafana Enterprise/Cloud** ([access-control](https://grafana.com/docs/grafana/latest/administration/roles-and-permissions/access-control/), [audit-grafana](https://grafana.com/docs/grafana/latest/setup-grafana/configure-security/audit-grafana/)). OSS has none.
- Even on Enterprise, audit records the *occurrence* of a query, not the SQL/results, unless `log_datasource_query_request_body` / `_response_body` are explicitly enabled — and as of **Grafana v13 (released 2026-04-14)** those default to **false** ([enterprise-configuration](https://grafana.com/docs/grafana/latest/setup-grafana/configure-grafana/enterprise-configuration/), [v13 what's-new](https://grafana.com/whats-new/2026-04-14-auditing--changed-defaults-for-data-source-queries-audit-logging-settings/)). Enabling them dumps literals/PII into the Grafana audit log (another T2 sink), and audit is at the Grafana layer — anyone with direct ClickHouse credentials bypasses it entirely.
- Grafana Enterprise cost band is ~**$25K–$150K/yr**, custom-quoted ([CloudZero](https://www.cloudzero.com/blog/grafana-cloud-pricing/), [CostBench](https://costbench.com/software/business-intelligence/grafana-enterprise/) — both low/medium confidence).
- AGPLv3: running *unmodified* Grafana OSS as a service is fine, but **forking core to inject real T1/T2 enforcement** triggers source-disclosure, pushing you to the proprietary Enterprise binary or a commercial license ([Grafana licensing](https://grafana.com/licensing/)). Note a custom **data-source plugin remains Apache-licensed**, so a plugin-only integration dodges AGPL — but a plugin cannot fix the bypass paths above.

### The only enforceable architecture (and why it negates the pivot)
The one sound pattern is a **bespoke authenticating, authorizing, audit-writing proxy in front of ClickHouse**, with ClickHouse network-isolated so *no other path exists* (no direct TCP/HTTP, no Explore raw SQL, no shared accounts, no second datasource). That proxy must authenticate the human, RBAC-check the tier, write the hash-chained audit row in the **config DB**, and *only then* forward/return literals — failing closed if the audit write fails. **That is precisely what `api_server` already does.** So the enforceable design is "keep api_server as the sole T2 path; Grafana never sees T2." Grafana then is a T1-only renderer, not "the thing querying the store" for the protected tier. The CRUX as posed — *generic tool queries the store for T2 and audit still holds* — is **answered no**. ([Grafana backend plugin auth model, for completeness](https://grafana.com/developers/plugin-tools/key-concepts/backend-plugins/).)

---

## 2. Decision matrix

Scoring: 5 = best/strongest, 1 = worst/weakest, for the *Lynceus product as defined* (privacy differentiator is the pitch).

| Criterion | **Custom-only** (status quo: SSR + vanilla-PG store) | **Hybrid** (custom SSR + api_server sole T2 gate; **T1-only** Grafana export; PG store, CH optional later) | **Full-pivot** (Grafana primary viz + ClickHouse primary store) |
|---|---|---|---|
| **Privacy enforceability (CRUX)** | **5** — atomic authorize+audit in Go store; tamper-evident hash chain; literal-free by construction | **5** — T2 stays behind api_server (unchanged); Grafana sees only literal-free T1, so nothing to bypass | **1** — audit-on-read becomes theater; unbypassable paths; `query_log` = literal sink; identity = spoofable header |
| **Eng cost** | **5** — none (already built) | **3** — add a read-only T1 export datasource/views + a few Grafana dashboards-as-code; optionally extract `store.Stats` interface | **1** — extract interface (project 1) + ClickHouse impl (project 2) + bespoke audit proxy (project 3) + re-home insight/advisor/flamegraph UX Grafana can't express |
| **Ops cost** | **5** — vanilla PG on RDS/Aurora, zero new infra | **4** — same RDS posture; Grafana OSS is a thin add-on (or none) | **1** — self-managed CH: 2+ ReplicatedMergeTree replicas + ≥3 Keeper quorum + clickhouse-backup/DR; ~$1,070–2,480/mo infra + ~$1,600–4,800/mo ops labor, OR third-party SaaS dependency |
| **Insight / UX fidelity** | **5** — flamegraph, 11 EXPLAIN detectors, index/vacuum advisors, checks engine, cross-signal Overview all native | **5** — identical (custom surface retained); Grafana only adds basic T1 time-series panels | **2** — Grafana cannot express node-tree flamegraph, plan-walk detectors, advisor rationale, or cross-signal joins; these still need bespoke Go+SSR alongside Grafana (no scope reduction) |
| **Positioning** | **5** — "privacy-first, runs on managed vanilla Postgres, no extensions, you run it" | **4** — same promise intact; "also exports to your Grafana" is a plus, not a dependency | **1** — breaks RDS/no-extensions promise; pushes vendor SaaS or heavy ops; privacy claim unsubstantiated → marketing/legal liability |
| **Licensing cost** | **5** — none | **4** — Grafana OSS (AGPL, unmodified, fine) or none; CH core Apache-2.0 if ever used | **2** — needs Grafana Enterprise (~$25K–$150K/yr) to even approximate RBAC/audit; AGPL trap if core modified |
| **Columnar scale headroom** | **2** — Go-managed PG partitioning; fine to mid-scale, not OLAP-grade | **3** — same now; clean path to optional CH T1 backend later if needed | **5** — MergeTree columnar + native TTL tiering; genuine OLAP scale |
| **Net** | **Strong** | **Strongest for this product** | **Weak — becomes a different product** |

---

## 3. ClickHouse-as-backend assessment

**Technical fit (high confidence):** Excellent for the *time-series stats* workload in isolation. Columnar MergeTree, vectorized execution, aggressive codecs (`DoubleDelta`/`Gorilla`/`LowCardinality`), `ORDER BY (series_id, ts)`. Ingest beats Timescale/InfluxDB in third-party benches (~345k–900k+ rows/s high-cardinality; ~1.3M pts/s at 48 clients — confirmed against QuestDB and SciTS primary sources, but vendor-adjacent and large-batch-favoring). Native TTL (row/column/table, `ttl_only_drop_parts=1`, `TO DISK/VOLUME` tiering on self-managed) is genuinely more capable than the Go-managed `PARTITION OF` machinery. *Confirmed, with caveats: tiering is OSS-only (not Cloud); TTL is lazy (merge-time, default ~4h), not a point-in-time hard delete.* ([D.E.Shaw 7x](https://clickhouse.com/blog/deshaw), [managing-data/TTL](https://github.com/ClickHouse/clickhouse-docs/blob/main/docs/use-cases/observability/build-your-own/managing-data.md), [TTL guide](https://clickhouse.com/docs/guides/developer/ttl), [QuestDB bench](https://questdb.com/blog/2021/06/16/high-cardinality-time-series-data-performance/), [SciTS](https://arxiv.org/abs/2204.09795).)

**Managed / HA (high confidence, decisive against):** No AWS-first-party engine. HA is self-managed: ≥3 dedicated Keeper (Raft quorum) + 2+ ReplicatedMergeTree replicas + operator-driven `clickhouse-backup` to S3 + DR runbooks; **no continuous WAL-style PITR anywhere** (even Cloud is daily snapshots). Managed options are third-party SaaS/control-planes (ClickHouse Cloud, Altinity.Cloud BYOC, Aiven) — none is RDS-grade AWS-native. This **breaks the core portability invariant**. ([replication](https://clickhouse.com/docs/architecture/replication), [backup](https://clickhouse.com/docs/operations/backup), [clickhouse-backup](https://github.com/Altinity/clickhouse-backup), [no managed CH on AWS](https://www.tinybird.co/blog/managed-clickhouse-on-aws).)

**Cost (confirmed):** CH Cloud usage-based — storage $25.30/TB-mo; compute $0.218–$0.390/unit-hr; Basic from ~$66.52/mo (single replica), Scale from ~$499.38/mo (HA), Enterprise ~$2,669–$9,714/mo. Self-host 3-node HA ~$870–1,200/mo infra (~$2,480+ cross-region DR) + ~$1,600–4,800/mo eng time. ([Tinybird self-host](https://www.tinybird.co/blog/self-hosted-clickhouse-cost), [Beton teardown](https://www.getbeton.ai/blog/clickhouse-pricing-teardown/).)

**Licensing (confirmed):** Core is Apache-2.0, no volume fees, redistributable — compatible with OSS positioning. Caveat: open-core gating (SharedMergeTree, some security features) is Cloud-only; the OSS binary is not feature-equivalent. ([LICENSE](https://github.com/ClickHouse/ClickHouse/blob/master/LICENSE).)

### Can ClickHouse be an OPTIONAL backend without violating "no feature depends on it"?
**Yes mechanically, but it forfeits the point — and only after real work.**
- The seam doesn't exist: first extract a `store.Stats` interface from ~12 concrete pgx-bound reader/writer method sets (`stats.go`, `rollup.go`, `plans.go`, `insights.go`, `table_stats.go`, `index_stats.go`, `freeze_ages.go`, `connections.go`, `checks_results.go`, `schema_objects.go`, `log_events.go`), then change `NewServer`/`fleetview` signatures, then add a second impl. The insight/advisor/checks/fleetview layers already consume Go structs (not SQL), so they *are* correctly insulated — good.
- **The contradiction:** ClickHouse's value lives in CH-specific features (MergeTree TTL tiering, `toYYYYWW` partitioning, materialized views). An abstraction generic enough that "no feature depends on it" cannot use those — so you keep the ops/positioning costs while leaning on none of the benefits. The `query_plans` JSONB plan-tree → CH JSON/String mapping must also preserve the literal-free guarantee.
- **Hard constraint:** the **config/audit DB must stay vanilla Postgres** regardless. The hash chain depends on `pg_advisory_xact_lock` + append-only `BEFORE UPDATE/DELETE` triggers + `BIGSERIAL` + transactional semantics ClickHouse does not have. So any CH adoption is a **two-engine split**, not a swap.

**Conclusion:** ClickHouse is a fine *optional T1 stats backend for large-fleet operators* once the interface exists — but it is the wrong **default**, and it must never host audit or T2.

---

## 4. Gained vs Lost — and what becomes a different product

**GAINED (full pivot):**
- Mature, dashboards-as-code visualization + alerting + OIDC login (Grafana).
- Columnar OLAP scale and first-class TTL/retention (ClickHouse).
- Less bespoke T1 dashboard code to maintain.

**LOST (full pivot):**
- **The enforceable privacy differentiator** — audit-on-read + data-tier RBAC become theater (the whole pitch).
- **Literal-free backbone** — the only available audit sources (`query_log`, Grafana request-body audit) *store literals*, inverting the core promise.
- **Tamper-evidence** — hash-chained `VerifyChain` audit cannot move off Postgres; `query_log` is mutable.
- **RDS/Aurora portability + zero-new-infra ops** — replaced by self-managed Keeper/replicas or third-party SaaS.
- **Opinionated remediation UX** — flamegraph, 11 EXPLAIN detectors, index/vacuum advisors, checks engine, cross-signal Overview, fleet→cluster→instance topology — Grafana cannot express these; they remain bespoke, so "less UI code" is largely illusory.
- **Open-source positioning** — the privacy/RBAC/audit feature set sits behind Grafana Enterprise.

**What becomes a DIFFERENT PRODUCT (state plainly):**
- **Exposing T2 through Grafana** turns "privacy-first PostgreSQL monitoring with enforced, audited, literal-free access control" into "a generic ClickHouse-backed observability dashboard with best-effort access logging." That is a different product, with a different (and unsubstantiated) privacy claim — a marketing/legal liability, not a feature regression.
- **Making ClickHouse the default store** turns "runs on managed vanilla Postgres you operate yourself" into "ship/operate a Keeper+replica OLAP cluster or buy SaaS." Different buyer, different ops contract, different positioning.
- **A pure Custom-only or Hybrid path is NOT a different product** — it preserves every invariant. T1-only Grafana export is additive convenience.

---

## 5. Minimal POC plan — the smallest experiment that validates or kills the CRUX

**Goal:** Empirically prove/disprove that mandatory, tamper-evident audit-on-read for T2 can be enforced when reads flow through a generic Grafana → (proxy) → ClickHouse path. Design it to *try hardest to make the pivot succeed*, so a failure is decisive.

**Setup (1–2 days):**
1. Stand up single-node ClickHouse (Docker) with one table carrying a `data_tier` column and at least one literal-bearing T2 column; load synthetic rows (tier 1 and tier 2).
2. Configure ClickHouse RBAC: a read-only role denied the T2 column via `GRANT`, a row policy keyed on `X-Grafana-User`, and `system.query_log` enabled.
3. Add Grafana OSS with the official ClickHouse datasource (single shared credential), HTTP protocol, `X-Grafana-User` forwarding on.
4. Build a thin **audited authorizing proxy** in Go (reuse `store.Config.AppendAuditReturning` against a vanilla-PG config DB) sitting between Grafana and ClickHouse: authenticate, RBAC-check tier, write hash-chained audit row, fail-closed, then forward.

**Attack/verify steps (the pass/fail bar):**
| # | Test | PASS condition (pivot survives) | Expected result |
|---|---|---|---|
| A | Authorized T2 read **through the proxy** produces exactly one tamper-evident audit row, and a forced audit-write failure blocks the read | both hold | PASS (this is just api_server reinvented) |
| B | Same user hits **`/api/ds/query`** directly (session cookie), bypassing dashboards | request denied OR a mandatory audit row is written | **FAIL** — Viewer reads T2, no audit ([114769](https://github.com/grafana/grafana/issues/114769)) |
| C | Read T2 via **Grafana Explore** | denied OR audited | **FAIL** unless Explore disabled org-wide (Enterprise RBAC) |
| D | **Spoof** `X-Grafana-User` (curl/Postman to the CH HTTP endpoint or through a misconfigured proxy) | row policy still binds to true identity | **FAIL** — header forgeable; per-user gate defeated |
| E | Issue `SELECT … SETTINGS log_queries=0` (or rely on async flush + crash) | the T2 read is still durably audited | **FAIL** — native audit disableable/lossy |
| F | Re-read identical T2 query with **query cache** on | second read still audited | **FAIL** — cache hit skips the store/audit hook |
| G | Trigger a **Grafana alert rule** reading T2 | per-user audited | **FAIL** — alert runs with no user identity |
| H | Point a second **direct clickhouse-client** at the store | no path to ClickHouse except the proxy | **FAIL** unless CH is network-isolated to the proxy only |

**Kill criterion:** If **any** of B–H fails while Grafana is permitted to reach ClickHouse for T2 (and they will), the CRUX is dead — confirming audit-on-read is unenforceable with a generic tool on the store. The **only** way to pass B–H is to network-isolate ClickHouse so the *only* path is the audited proxy and forbid Grafana from querying T2 — i.e., test A in isolation, which proves you've rebuilt api_server and removed Grafana from the protected path.

**Cheaper kill (½ day, no ClickHouse):** Re-read the verified Grafana/ClickHouse docs in §1 — every bypass (B–H) is already documented behavior. The POC mainly produces a demonstrable artifact for stakeholders; the literature already settles it.

**What the POC legitimately *validates* (the salvageable path):** Run only **T1-only** data through Grafana → ClickHouse (or → a read replica of the PG stats store). Confirm: (i) the dataset is literal-free by construction (reuse the proto contract-test discipline), (ii) no T2 column is reachable, (iii) the existing api_server still owns all T2 reads with audit intact. PASS here greenlights the **Hybrid** recommendation.

---

## 6. Recommendation

**Adopt the HYBRID. Reject the full pivot. Do not put T2 behind Grafana. Do not make ClickHouse the default store.**

Concretely:
1. **Keep the custom SSR surface** for everything that is the product: flamegraph, EXPLAIN detectors, index/vacuum advisors, checks engine, cross-signal Overview, fleet topology, capability matrix, audit viewer. Grafana cannot express these and adds no scope relief.
2. **Keep `api_server` as the sole T2 gateway**, and *now* wire the audit-on-read that the schema already anticipates: wrap each T2-capable read handler with `EffectiveCapability` check + `AppendAudit(action=read, data_tier=2)`. This is trivially insertable because every read already funnels through a Go store method — close this gap regardless of any UI decision.
3. **Offer T1-only Grafana as an optional export/embed**, read-only, over the literal-free tier only (a read replica or a dedicated T1 view), never the audited tier. This satisfies "bring your own Grafana" without risking the differentiator. Ship it as dashboards-as-code; keep Grafana *unmodified* (no AGPL exposure).
4. **Extract the `store.Stats` interface** as standalone hygiene (it's already promised in docs and is the prerequisite for *any* backend flexibility). Keep insight/advisor/checks/fleetview on Go structs.
5. **Treat ClickHouse as a future opt-in T1 stats backend only**, for large-fleet operators who accept its ops/positioning costs — never default, never audit/T2, config+audit DB stays vanilla Postgres. Gate it behind the new interface so "no feature depends on it" holds (accepting that this forfeits CH-specific features).

The pivot's appeal is real (mature viz, columnar scale, less dashboard code), but it is purchased by **deleting the one thing that distinguishes Lynceus from a generic ClickHouse-on-Grafana observability stack** — and by trading a zero-infra RDS deployment for a self-run OLAP cluster. That is not an optimization of this product; it is the construction of a different one. If the team *wants* that different product, do it deliberately and rename the privacy claim accordingly — do not arrive there by treating the pivot as a backend swap.

---

## Appendix: key codebase facts (verified this session)
- No `store.Stats` / `store.Config` interface exists; `internal/store/stats.go:25` → `func NewStats(pool *pgxpool.Pool) *Stats`. The pluggable seam is documentation only.
- `data_tier = 1` is hardcoded in stats SELECTs (4+ occurrences across `internal/store/*.go`); no read path ever serves tier ≠ 1. `data_tier` is today a write-side label + constant T1 filter, not an access-control mechanism that serves T2.
- The only production caller of `AppendAuditReturning` is `internal/store/capability_policy.go` (`SetCapabilityPolicy`, an admin write). No read handler audits today — but the choke point (Go store methods) and the tamper-evident writer (config DB hash chain + `VerifyChain`) both exist and work, so audit-on-read is a small, well-scoped addition in the Custom/Hybrid path.
- Config/audit DB hash chain relies on Postgres `pg_advisory_xact_lock` + append-only triggers + `BIGSERIAL`; it cannot move to ClickHouse and must remain vanilla Postgres.
