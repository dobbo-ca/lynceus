# Lynceus Pattern Transferability: OpenSearch / Elasticsearch

**Date:** 2026-06-22
**Analyst:** research subagent
**Question:** Does the Lynceus collector pattern (near-datastore, read-only role, local
normalize+analyze, literal-free T1 wire contract, ingestion + stats store + insight
engine + k8s CRD control plane) extend to OpenSearch and Elasticsearch?

**Verdict: fits-with-work.** The metrics/parity surfaces transfer extremely well and the
managed-cloud access story is good. The decisive privacy-normalization crux is *feasible
but bespoke and riskier than SQL* — the query DSL is structured JSON that a collector can
walk and strip to a literal-free skeleton, but there is no battle-tested external parser
(the libpg_query analog), and `query_string`/scripts/field-name sensitivity create
fail-open hazards that the Postgres pipeline does not have.

---

## A. ACCESS — score 8/10

A limited, read-only "monitor" role can read the rich stats surfaces both self-hosted and
in managed cloud.

- **Self-hosted RBAC.** The OpenSearch security plugin ships a `cluster_monitor` cluster
  permission; a read-only role is configured with `cluster_permissions: ["cluster_monitor"]`,
  which is exactly what `_cat/*`, `_cluster/health`, `_cluster/stats`, and `_nodes/stats`
  require (a `GET _cat/nodes` performs `cluster:monitor/*` actions). This is the direct
  analog of the Postgres `pg_monitor` role.
  Source: https://docs.opensearch.org/latest/security/access-control/permissions/
- **Rich stats surface confirmed.** `_nodes/stats` exposes `jvm` (heap/GC), `thread_pool`
  (per-pool queue/rejected), and `breakers` (per-circuit-breaker tripped counts);
  `_cat/indices|shards|segments|nodes|allocation|thread_pool` give shard/segment/allocation
  detail; `_cluster/health` gives status + unassigned shards; `_tasks` and
  `_nodes/hot_threads` give live work; `_cluster/settings` (GET) gives config.
  Source: https://docs.opensearch.org/latest/api-reference/nodes-apis/nodes-stats/
- **The slow-query stream is a LOG, not an API.** Unlike Postgres `pg_stat_statements`
  (a queryable view), the slow query stream is the **search/index slowlog** written to a
  log file. Self-hosted, the collector must read the slowlog file (or its stdout/JSON form);
  managed, it is published to CloudWatch (see §F). There is **no in-cluster API that returns
  per-fingerprint aggregated query stats** — no `pg_stat_statements` equivalent. This is a
  structural gap: the collector must do its own aggregation from a log tail rather than
  polling a pre-aggregated view.
  Source: https://docs.opensearch.org/latest/install-and-configure/configuring-opensearch/logs/

**Why not 10:** the slow stream is file/log-shaped (no aggregated query-stats API), and
enabling it requires a cluster/index setting change (`PUT _cluster/settings` /
`index.search.slowlog.*`), i.e. the operator must opt-in and accept disk/perf cost — closer
to enabling `auto_explain` than to reading an always-on view.

---

## B. PRIVACY-NORMALIZATION CRUX — score 5/10 (DECISIVE)

**The slow-query / Profile stream carries raw literals, and there is no off-the-shelf
fingerprinter. Normalization is *achievable* but must be built bespoke and fails-open in
ways the Postgres path does not.**

### What leaks today (concrete, cited)

- **Search slowlog `source[]` is the raw query DSL with values.** Example slowlog line:
  `... total_hits[4 hits], search_type[QUERY_THEN_FETCH], shards[{...}],
  source[{"query":{"match_all":{"boost":1.0}}}], id[]` — the `source` field is the verbatim
  request body. With a real query it contains the literal term/match/range VALUES
  (`{"term":{"user_id":"abc123"}}`).
  Source: https://opensearch.isharkfly.com/install-and-configure/configuring-opensearch/logs/
- Elasticsearch is identical: `source[{"query":{"match_all":{"boost":1.0}}}]`, default logs
  the first 1000 chars of source; `source: false`/`0` disables, `true` logs the whole thing.
  Source: https://www.elastic.co/docs/reference/elasticsearch/index-settings/slow-log
- **Profile API fuses literals into the description.** `_search?profile=true` returns query
  nodes whose `description` is e.g. `"title:wind"` / `"title:rise"` — field name AND the
  literal value, concatenated into a Lucene representation.
  Source: https://docs.opensearch.org/1.3/api-reference/profile

### Why this is harder than SQL fingerprinting

1. **No external parser = no libpg_query analog.** Lynceus's privacy backbone is that
   `pganalyze/pg_query_go/v6` *is Postgres's own C parser*; `normalize.Normalize` and
   `Fingerprint` delegate to it and **fail closed** (`TierBlocked`, drop the row) on anything
   unparseable. For OpenSearch we would hand-roll a JSON-tree walker over the query DSL. The
   DSL is well-specified — leaf clauses (`term`/`terms`/`match`/`range`/`prefix`/`wildcard`/
   `ids`) vs compound clauses (`bool` with `must`/`should`/`filter`/`must_not`) — so a walker
   *can* keep clause type + field name and replace every leaf value with `$n`. But we own that
   parser; every new query type or DSL version is a fail-open risk. There is no vendor parser
   to lean on (the OpenSearch/Elastic "fingerprint" analyzer/processor are for *document*
   dedup, NOT query normalization).
   Source: https://docs.opensearch.org/latest/ingest-pipelines/processors/fingerprint/
2. **`query_string` / `simple_query_string` embed a mini-language with values in a single
   string** (`"status:active AND age:>30"`). Stripping values from that requires parsing the
   Lucene query-string grammar, not just walking JSON keys. Realistic policy: treat any
   `query_string` clause as un-normalizable and **fail closed** (drop / mark T2-only), which
   reduces parity for clients that use it.
3. **Scripts (Painless) are opaque code carrying literals** (`script_score`, `script` query,
   scripted sort/agg). These cannot be normalized structurally; they must be dropped or
   T2-gated. Same posture as Postgres functions-with-literals but more common in ES/OS.
4. **Field names themselves may be sensitive.** In SQL, identifiers (table/column) are part
   of the T1 normalized form and treated as non-secret. In a doc store, field names can be PII
   or reveal data structure (`{"term":{"patient_ssn_hash": ...}}`). The DSL key *is* the field
   name and cannot be removed without destroying the analytic value (you need the field to say
   "this query targets an unindexed/high-cardinality field"). So the T1 contract must either
   (a) treat field names as non-secret (matching the SQL-identifier stance) or (b) add an
   optional field-name hashing mode — a new decision the Postgres design never had to make.

### Net feasibility

A literal-free skeleton IS extractable for the common, structured DSL: keep `bool/should/
filter/must`, keep clause types and field names, replace leaf values with positional
placeholders, then fingerprint the canonicalized skeleton (sort keys, drop `boost`). This is
a real, defensible privacy story and a genuine differentiator vs. shipping raw slowlogs to a
SaaS. **But** it must be written and maintained bespoke, it fails open if the walker misses a
new clause type, `query_string`/scripts force drop-or-T2, and field-name sensitivity is an
open policy question. That is why this scores 5, not 8 — the differentiator transfers, but
not *cleanly*, and not for free. Mitigation that preserves the Lynceus "fail-closed" ethos:
allowlist known clause types, drop on any unknown key, and run the existing surviving-quote /
provably-literal-free guard pattern (`planextract.NormalizeCondition` style) over the
serialized skeleton before it can become T1.

---

## C. PARITY — score 9/10

The signal surface is rich and supports opinionated, remediable insights — arguably broader
than the Postgres MVP.

Insight catalog (all from read-only monitor surfaces):

- **Shard imbalance / oversharding** — `_cat/shards`, `_cat/allocation`, `_cluster/stats`
  (shard count vs node count vs heap). Remediation: shrink/reindex, ISM rollover.
- **Mapping explosion / field-count blowup** — `_cluster/stats` (`indices.mappings.field_types`,
  total field count) and index mapping; flag indices nearing `index.mapping.total_fields.limit`.
- **JVM heap & GC pressure** — `_nodes/stats` `jvm.mem` + `jvm.gc.collectors` (old-gen GC
  time/freq). Source: https://docs.opensearch.org/latest/api-reference/nodes-apis/nodes-stats/
- **Circuit-breaker trips** — `_nodes/stats` `breakers.*.tripped` (parent/fielddata/request).
  Source: https://repost.aws/knowledge-center/opensearch-circuit-breaker-exception
- **Fielddata pressure** — `breakers.fielddata` + `_cat/fielddata`; flag aggregations/sorts on
  text fields. Source: https://repost.aws/knowledge-center/opensearch-high-jvm-memory-pressure
- **Threadpool rejections** — `_nodes/stats` `thread_pool.search.rejected` / `write.rejected`
  growing → under-provisioned. Source: https://repost.aws/knowledge-center/opensearch-resolve-429-error
- **Segment count / merge pressure** — `_cat/segments`, `_nodes/stats` `indices.merges`.
- **Unassigned shards / cluster red-yellow** — `_cluster/health`,
  `_cluster/allocation/explain`.
- **Hot shards / hot nodes** — `_nodes/hot_threads`, per-node `indices.search` rates.
- **Slow / expensive queries** — slowlog skeleton fingerprints (the §B stream): top
  fingerprints by took, deep pagination, wildcard/leading-wildcard, missing `filter` cache
  usage, large `size`/`from`. This is the direct EXPLAIN-insight analog and the Profile API is
  the EXPLAIN analog.

These map onto the existing insight-engine shape: `internal/insight` already iterates a
detector set over a typed plan/metric structure (see `SlowScanDetector` walking `PlanNode`s).
A `ShardImbalanceDetector` / `HeapPressureDetector` over node/shard stats fits the same
`Detect() []Insight` interface.

**Why not 10:** much parity value (heap/shard/breaker) is *cluster-metric* analysis that is
not literal-sensitive — strong, but it is the "raw metrics + thresholds" tier; the
*opinionated query remediation* tier depends on §B normalization succeeding.

---

## D. PLATFORM REUSE — score 5/10

| Layer | Reuse verdict | Why |
|---|---|---|
| **Wire contract approach (proto T1 + reflection contract test)** | **Reuses as concept; new messages** | The pattern — per-message field allowlist enforced by a reflection test (`contract_test.go`) so no `raw_text`/`source` field can be added to T1 — is datastore-agnostic and is the most valuable transferable idea. But every message body is new: `ShardStat`, `NodeJVMStat`, `BreakerStat`, `SlowQueryStat{fingerprint, normalized_dsl, took, hits, ...}`, `MappingStat`. Needs new `.proto` + new allowlists. |
| **Normalization / fingerprinting (privacy crux)** | **Bespoke rewrite** | `internal/normalize` is 100% Postgres (pg_query cgo). A DSL-tree walker + Lucene query-string handling + fail-closed guard must be built from scratch (see §B). This is the single largest net-new engineering item. |
| **Collector readers** | **Bespoke rewrite** | `internal/collector` issues hardcoded SQL over a `pgx` pool. OS/ES readers are HTTP GETs against `_nodes/stats` / `_cat` / a slowlog tail, decoding JSON. Zero SQL reuse; the *shape* (per-surface reader → T1) reuses. |
| **Capability probes + gate (`internal/caps`)** | **Reuses pattern; new probes** | `probes.go` is all `pg_has_role`/`SHOW`/`pg_extension`. Analog probes: does the role have `cluster_monitor`? are slowlog thresholds > -1? is `source` logging on? is CloudWatch publishing enabled (managed)? The Discoverer→Gate design transfers; the probe bodies are new. |
| **Ingestion server** | **Reuses ~as-is** | Websocket terminate + rate-limit + DLQ + write is payload-agnostic; only the proto types change. |
| **Stats store (vanilla Postgres, partitioned)** | **Reuses ~as-is** | `internal/store/stats.go` is a generic time-range-partitioned writer; `QueryStat` becomes `SlowQueryStat`/`NodeStat` etc. RDS/Aurora-vanilla-PG backend story unchanged. The T1/T2 `data_tier` + `audit_log` model reuses directly. |
| **Insight / check engine** | **Reuses pattern; new detectors** | `Detector.Detect() []Insight` over typed structs reuses cleanly (see §C). New detectors operate on node/shard/slowlog structs instead of `PlanNode`. |
| **k8s operator + CRD control plane** | **Reuses ~as-is** | Config-DB-as-source-of-truth → policy snapshot → zero-touch collector is datastore-agnostic; the CRD gains OS/ES connection + which-surfaces-to-poll fields. The operator pattern is reused; the policy schema extends. |
| **Fleetview entity model** | **Reuses with remap** | cluster→instance→stream maps naturally to OS/ES cluster→node→index/shard. Good conceptual fit; new entity types. |

Honest reuse: the *architecture, privacy philosophy, and contract-test discipline* reuse
strongly (this is what makes it "the Lynceus pattern"), but **the two crux layers — the
normalizer and the readers — are full rewrites**, and the wire messages/probes/detectors are
new bodies in reused frames. Hence 5/10: more than half the *code* is net-new, but the
hardest-won *design* carries over.

---

## E/F. HA-TOPOLOGY + COST/POSITIONING + MANAGED ACCESS — score 8/10

### HA / topology fit (strong)

OS/ES are natively HA-clustered: primary + replica shards, node roles
(data / cluster-manager(master) / ingest / coordinating), and shard allocation. This is a
*better* topology fit than single-primary Postgres — the fleetview cluster→node→shard model
is the native data model, and insights like "replica on same node as primary" or "shard
allocation skew" are first-class.

### Managed-cloud reality (the RDS/Aurora analog) — good, with caveats

- **Amazon OpenSearch Service** supports the monitoring surfaces: `_cat/indices`,
  `_cat/shards`, `_cat/segments`, `_cat/nodes`, `_cluster/stats`, `_nodes/stats`, `_tasks`,
  and hot threads. The RDS-analog limits: `PUT _cluster/settings` accepts only the **flat**
  settings form (rejects expanded form); some settings are locked
  (`cluster.max_shards_per_node` immovable since 2.17); no superuser / no node-level OS access.
  Source: https://docs.aws.amazon.com/opensearch-service/latest/developerguide/supported-operations.html
- **Slow logs in managed** are not file-readable; they are published to **CloudWatch Logs**
  (`-search-slow-logs` / `-index-slow-logs` streams), controllable independently, **first
  255,000 chars per line** retained. So the managed collector reads the slow stream from
  CloudWatch, not from a file tail — a different ingestion path than self-hosted, and the
  collector needs an AWS IAM/CloudWatch read path in addition to the OS monitor role.
  Source: https://docs.aws.amazon.com/opensearch-service/latest/developerguide/createdomain-configure-slow-logs.html
- **Fine-grained access control** (the security plugin in managed form) supports a read-only
  `cluster_monitor` role identical to self-hosted.
  Source: https://docs.aws.amazon.com/opensearch-service/latest/developerguide/fgac.html
- **Elastic Cloud** exposes slow logs and stack monitoring similarly (slow logs surfaced in
  the deploy/monitor tooling).
  Source: https://www.elastic.co/docs/deploy-manage/monitor/logging-configuration/slow-logs

**Caveat that lowers from 9:** the managed slow-log path forks the collector
(file-tail self-hosted vs CloudWatch-pull managed), and enabling slow logs is an operator
opt-in with disk/perf cost — not zero-touch by default.

### Cost / positioning (favorable, with a license fork to navigate)

- **Licensing splits the market and *favors an Apache-2.0-positioned* tool.** OpenSearch is
  Apache-2.0 (governed by the OpenSearch Foundation / Linux Foundation since 2024).
  Elasticsearch is tri-licensed SSPL / Elastic License v2 / **AGPLv3** (Elastic re-added the
  OSI-approved AGPLv3 option in Sept 2024). A privacy-first, OSS, self-hostable monitor is
  well-positioned against Elastic's own paid Stack Monitoring / commercial observability and
  against shipping raw slowlogs to a SaaS APM.
  Sources: https://www.elastic.co/pricing/faq/licensing ,
  https://pureinsights.com/blog/2025/elasticsearch-vs-opensearch-in-2025-what-the-fork/
- Positioning win: Lynceus's "literal-free, analyze-at-the-edge" pitch is *more* compelling
  here than for Postgres, because the default slow-log behavior (ship the raw query body to
  CloudWatch / your log pipeline) is exactly the privacy hole Lynceus closes.

---

## Transferability scores (0-10)

| Criterion | Score |
|---|---|
| **A. Access** | 8 |
| **B. Privacy normalization (decisive)** | 5 |
| **C. Parity** | 9 |
| **D. Reuse** | 5 |
| **E/F. HA + cost/positioning** | 8 |

---

## Top risks

1. **Bespoke normalizer is the privacy single-point-of-failure.** No libpg_query analog →
   we own a DSL walker that must fail closed. A missed clause type ships a literal. Mitigation:
   allowlist clause types, drop-on-unknown, serialize-then-re-verify guard.
2. **`query_string` / scripts / aggregations** carry literals in forms a JSON walker can't
   structurally strip → reduces query-tier parity unless dropped/T2-gated.
3. **Field-name sensitivity** is a new T1 policy decision (treat as non-secret like SQL
   identifiers, or add hashing). Gets the privacy contract wrong if unconsidered.
4. **No aggregated query-stats API** — the collector must tail+aggregate the slowlog itself
   (only queries above threshold are seen; below-threshold queries are invisible — sampling
   bias vs `pg_stat_statements`' full population).
5. **Managed collector forks** (CloudWatch pull vs file tail) and slow logs are opt-in with
   disk/perf cost — not zero-touch.

## Bottom line

The Lynceus *architecture and privacy discipline* transfer well, parity is strong, managed
access is real, and positioning is favorable (especially the Apache-2.0 OpenSearch angle).
The deciding factor — can the slow-command stream be made literal-free — is **yes, but
bespoke and fail-open-risky**: the structured DSL is normalizable into a skeleton, yet
without an external parser and with `query_string`/scripts/field-name edge cases, it is
materially harder and less airtight than SQL fingerprinting. **fits-with-work.**
