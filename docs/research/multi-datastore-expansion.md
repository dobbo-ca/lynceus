# Multi-Datastore Expansion: Cross-Cutting Synthesis

**Status:** decision-ready architecture report
**Author:** principal architect synthesis
**Date:** 2026-06-22
**Scope:** Can the Lynceus pattern (privacy-first, collector-normalized, literal-free T1 telemetry) expand beyond PostgreSQL to Redis/Valkey and OpenSearch/Elasticsearch?
**Per-datastore source docs:** [`docs/research/expand-redis.md`](./expand-redis.md), [`docs/research/expand-opensearch.md`](./expand-opensearch.md)

---

## 0. What makes Lynceus *Lynceus*

Before scoring anything, name the thing under test. Lynceus is not "a metrics exporter with a nice UI." Its defensible core is a single, narrow, fail-closed primitive:

> **The slow-query stream is normalized to a literal-free template *at the source*, by the database's own grammar, before any per-execution value can leave the customer's infrastructure.**

For Postgres this is delivered by exactly one package — [`internal/normalize/normalize.go`](../../internal/normalize/normalize.go) — which delegates to `pganalyze/pg_query_go/v6` (libpg_query, Postgres's actual C parser over cgo). `Normalize(query)` returns `("", TierBlocked)` and **discards the original text** on any parse failure (`normalize.go:40-49`); `Fingerprint(query)` returns a stable structural hash so literal-only variants collapse to one key. The EXPLAIN path reuses the same engine via [`planextract.NormalizeCondition`](../../internal/planextract/normalize_cond.go), which wraps bare predicates in `SELECT * FROM __ly_cond WHERE …`, re-checks the prefix survived, and has a belt-and-suspenders guard: *any surviving single quote ⇒ return ""* (`normalize_cond.go:45-47`). The wire contract is enforced at schema level by a reflection-based per-message field allowlist ([`contract_test.go:20-45`](../../internal/proto/lynceus/v1/contract_test.go)) — you physically cannot add a `raw_text` field to a T1 message and merge.

**That primitive is the whole product thesis.** Everything else (readers, store, insights, checks, fleetview, control plane) is plumbing that many tools have. So the decisive question for any new datastore is not "can we read its stats" (almost always yes) but **"can its slow-command stream be normalized literal-free the same deterministic, fail-closed way?"** That is criterion B below, and it is the one that decides whether an expansion is "Lynceus" or "yet another exporter."

---

## 1. Per-datastore verdicts

### 1a. Redis / Valkey / Falkey — verdict: **PARTIAL FIT**

Valkey is a BSD-3 protocol-compatible fork of Redis 7.2.4 (Linux Foundation; AWS/Google/Oracle backing); Redis 8 is tri-licensed SSPLv1/RSALv2/AGPLv3 ([redis.io/blog/what-is-valkey](https://redis.io/blog/what-is-valkey/)). Falkey is the same lineage (another BSD-3 Redis-protocol fork). **The monitoring surfaces — `SLOWLOG`, `INFO`, `LATENCY`, `CLIENT`, `COMMAND` — are shared across all of them today**, so a single Redis-family collector covers Redis OSS, Valkey, and Falkey; licensing/roadmap divergence affects only Redis 8's new *core data types* (JSON/vector/timeseries), not the base telemetry. Score the family as one.

| Criterion | Score (/10) | One-line basis |
|---|---|---|
| A. Access (limited role + slow stream, incl. managed) | 7 | `SLOWLOG`/`INFO`/`LATENCY`/`CLIENT` reachable via ACL grants self-hosted and on ElastiCache **node-based**; **fails on ElastiCache Serverless** (slowlog/latency/client/memory/object all unavailable). |
| B. Privacy crux (literal-free normalization) | **4** | Decisive weakness — see §2. Values strippable deterministically; **key names are not**, and keys are the schema. |
| C. Parity (opinionated insights) | 8 | Rich: hit-ratio, eviction, fragmentation, replication lag, big/hot keys, plus built-in `LATENCY DOCTOR`/`MEMORY DOCTOR` advisors (OSS-only). |
| D. Reuse of platform | 6 | ~40% reuse / 20% interface / 40% bespoke. |
| E. HA/topology + cost/positioning | 7 | Cluster/Sentinel maps cleanly to fleetview; repl-offset lag observable. |

**Access nuance (verified, cuts against the raw track):** ElastiCache restricts `config`/`debug` for *all* users (so the collector cannot `CONFIG GET maxmemory-policy`; that must come from the parameter-group AWS API). On **ElastiCache Serverless** the entire slow-command surface (`slowlog`, `latency`, `client`, `memory`, `object`, `commandlog`, `keys`, `monitor`) is unavailable at the data plane — no ACL grant re-enables it ([AWS SupportedCommands](https://docs.aws.amazon.com/AmazonElastiCache/latest/dg/SupportedCommands.html)). So managed reachability is **tier-dependent**: node-based works, serverless collapses to coarse INFO/CloudWatch metrics only. Also note `INFO`/`SLOWLOG`/`LATENCY` are `@admin`/`@dangerous`, not `@read` — `+@read` alone grants nothing; explicit per-command grants are mandatory for least privilege ([redis.io ACL](https://redis.io/docs/latest/operate/oss_and_stack/management/security/acl/)).

### 1b. OpenSearch / Elasticsearch — verdict: **FITS WITH WORK**

OpenSearch is Apache-2.0 (OpenSearch Foundation under Linux Foundation since Sept 2024); Elasticsearch is tri-licensed SSPL/AGPLv3/Elastic License v2 ([elastic.co licensing FAQ](https://www.elastic.co/pricing/faq/licensing)). The slowlog format and DSL are near-identical (OpenSearch forked from ES 7.10), so one collector covers both; an Apache-2.0-positioned OSS monitor aligns better with OpenSearch.

| Criterion | Score (/10) | One-line basis |
|---|---|---|
| A. Access (limited role + slow stream, incl. managed) | 8 | `cluster_monitor` (+`indices_monitor`) read-only role is a clean `pg_monitor` analog; Amazon OpenSearch Service supports the stats/cat APIs; slow logs reachable (file self-hosted, CloudWatch managed). |
| B. Privacy crux (literal-free normalization) | **5** | Feasible but bespoke and fail-open-risky — see §2. DSL is structured JSON (walkable) but no native parser; Lucene `query_string` + Painless are opaque. |
| C. Parity (opinionated insights) | 9 | Strongest of the three: shard imbalance, mapping explosion, JVM/GC, circuit-breaker trips, threadpool rejections, segment/merge, unassigned shards, hot shards, slow-query fingerprints (Profile API = EXPLAIN analog). |
| D. Reuse of platform | 5 | >half net-new (the normalizer + readers are full rewrites). |
| E. HA/topology + cost/positioning | 8 | cluster→node→shard maps natively to fleetview; managed-tier story strong. |

**A useful correction (verified, in OpenSearch's favor):** the raw tracks claimed OpenSearch has "no query-fingerprinting analog." That is **wrong**. The **Query Insights plugin** (bundled by default since 2.16; grouping since 2.17, on Amazon OpenSearch Service too) does "query grouping by similarity" — it strips search terms/filter values and keys groups by clause-type + field-name + field-type skeleton, with aggregated avg latency / execution count / CPU+memory per group. That is functionally a `pg_stat_statements`-over-normalized-templates analog ([OpenSearch Query Insights grouping](https://docs.opensearch.org/latest/observing-your-data/query-insights/grouping-top-n-queries/)). **Two caveats that keep it at 5, not 8:** (1) grouping is **off by default** (`group_by=none`, must be set to `similarity`); (2) each group ships a **literal-bearing "representative query example"** — so the native feature is *not* privacy-clean out of the box; a Lynceus pipeline must consume only the structural group key and deliberately discard the example. The differentiator is real here but is *our* discipline layered on their feature, not a free gift.

---

## 2. THE DECISIVE QUESTION: does the literal-free differentiator transfer?

This is the only question that determines whether expansion preserves what makes Lynceus defensible. Verdict per datastore: **does the slow-command/query stream normalize literal-free, deterministically, fail-closed, the way SQL fingerprints?**

### Postgres (baseline) — **YES, for free.**
`pg_stat_statements` is *already normalized at the source* (literals → `$n`), and libpg_query lets the collector re-prove it (`reader.go` re-runs every row through `Normalize`+`Fingerprint` and drops unparseable rows). The grammar deterministically classifies literal vs identifier. This is the gold standard the others are measured against.

### Redis / Valkey / Falkey — **NO (clean transfer). Partial, heuristic, fail-open.**
This is the decisive negative finding for Redis, and it is verified at source level, not inferred:

- **The slow stream is literal-FULL at the source.** `slowlogCreateEntry()` in `src/slowlog.c` stores arguments **verbatim** (`dupStringObject`); the only modification is memory-bounding truncation (`"... (N more bytes)"`, `"... (N more arguments)"`) — **never redaction**. Key names, values, even `AUTH`/`CONFIG SET` passwords sit intact up to the caps ([redis SLOWLOG GET](https://redis.io/docs/latest/commands/slowlog-get/); [src/slowlog.c](https://github.com/redis/redis/blob/unstable/src/slowlog.c)). `MONITOR` redacts only `AUTH` and costs >50% throughput ([redis MONITOR](https://redis.io/docs/latest/commands/monitor/)). Unlike Postgres, **there is no native normalized stream** — the collector must synthesize one.
- **Values *can* be stripped deterministically.** `COMMAND`/`COMMAND DOCS` expose arity + key-spec positions (legacy `first_key/last_key/step` pre-7.0; full key-specifications 7.0+), so a collector can mechanically locate **key** positions and treat the rest as values: `SET user:1001:session "<jwt>"` → `SET <key> <value>` ([redis key-specs](https://redis.io/docs/latest/develop/reference/key-specs/)). *Caveat (verified):* the metadata identifies **key** positions only, not value structure, and movable-key commands (`SORT`, `MIGRATE`, `ZUNION` via numkeys) need a `COMMAND GETKEYS` runtime fallback — so even value-stripping is "mostly deterministic," not "always."
- **The keys themselves are literals — and there is no grammar to normalize them.** This is the crux. In Redis **the key name *is* the schema** (no separate catalog), and the product's flagship insights (hot key / big key / TTL hygiene) **require key identity**. Convention `{service}:{entity}:{id}:{field}` embeds identifiers, and the documented secondary-index pattern puts **PII directly in keys** (`user:email:john@example.com`) ([redis secondary indexes](https://redis.io/docs/latest/develop/clients/patterns/indexes/)). Collapsing `user:1001:session` → `user:*:session` is a **regex heuristic** (digits→`{id}`, email-shaped→`{email}`): it over-redacts structural tokens and under-redacts novel literals. **There is no parser that declares which colon-segment is a literal**, so you cannot fail-closed without throwing away the very signal you sell.
- **Resolution onto the contract:** ship **command-shape fingerprints + numeric/aggregate stats in T1** (clean, passes the no-literal-field contract test); push **concrete key identity / big-key samples to T2** (off by default, RBAC + audit), optionally constrained by an operator key-pattern allowlist filtered at the collector boundary (mirrors the inventory schema-regexp pattern). The `data_tier`/`audit_log` model — already in the platform from day one — becomes **load-bearing** here.

**Net for Redis: the differentiator SURVIVES for command-shape telemetry but is materially weaker than Postgres, because Redis's flagship insight (key-level hot/big-key) is inherently literal-bearing and only deliverable as T2.** It is not "fail-closed and deterministic"; it is "fail-closed for command verbs/positions, best-effort-heuristic for keys, T2-gated for identity." That is a real privacy story, but a weaker one — and it must be sold honestly.

### OpenSearch / Elasticsearch — **YES, WITH WORK (collector-built, fail-open-risky).**
- **Both leak by default.** The search slowlog emits the **verbatim request body** in `source[{…}]` (literals included; default first 1000 chars on indexing-side, AWS caps published lines at 255k chars — truncation, *not* redaction) ([OpenSearch logs](https://opensearch.isharkfly.com/install-and-configure/configuring-opensearch/logs/); [Elastic slow-log](https://www.elastic.co/docs/reference/elasticsearch/index-settings/slow-log)). The Profile API fuses field+value into `description: "title:wind"` by design ([OpenSearch Profile](https://docs.opensearch.org/1.3/api-reference/profile)).
- **But the DSL is structured JSON, so normalization is genuinely feasible.** A collector-local JSON-tree walker keeps clause type (`term`/`match`/`range`/`bool`…) + field names, replaces every leaf value with `$n`, then canonicalizes + fingerprints the skeleton. **This is a real differentiator** vs shipping raw slowlogs to CloudWatch/SaaS — and the Query Insights similarity grouping (§1b) proves the datastore itself considers the skeleton meaningful.
- **Why it scores 5, not 8 (the honest part):** (1) **No libpg_query analog** — we hand-roll and *own* the walker; it **fails open** on any unhandled clause type / DSL version, the opposite of Postgres's fail-closed posture. (2) `query_string`/`simple_query_string` embed a **Lucene mini-language** with values in one string (`"status:active AND age:>30"`) — must parse that grammar or drop/T2. (3) **Painless scripts** are opaque code carrying literals — drop or T2. (4) **Field names ARE the DSL keys** and may be sensitive (`patient_ssn_hash`) yet are needed for analysis — a new T1 policy decision (treat as non-secret identifiers like SQL column names, or hash them) that the Postgres design never faced.
- **Mitigation that preserves the fail-closed ethos:** allowlist known clause types, drop-on-unknown-key, and add a serialize-then-re-verify guard in the exact style of `planextract.NormalizeCondition` (the single-quote-survivor check, generalized to "any non-`$n` leaf value survived ⇒ drop").

### Cross-cutting verdict on B

| Datastore | Transfers literal-free? | Determinism | Fail mode | Strength vs Postgres |
|---|---|---|---|---|
| Postgres | **YES, free** | parser-deterministic | fail-closed | baseline |
| OpenSearch/ES | **YES, with work** | hand-rolled walker | **fail-open** (mitigable) | ~70% — structured DSL is normalizable; field-names policy + opaque sublanguages are the gaps |
| Redis/Valkey/Falkey | **PARTIAL/NO** | values mostly-deterministic; **keys heuristic** | fail-closed on shape, **leaky on keys** | ~50% — flagship key-level insight is inherently T2 |

**The single most important sentence in this report:** OpenSearch preserves the differentiator with engineering effort (a walker we build and own); Redis cannot preserve it cleanly because its highest-value insight requires the literal (the key) the privacy model is supposed to strip. **OpenSearch is the expansion that stays "Lynceus"; Redis is the one that risks becoming "a privacy-flavored Redis exporter."**

---

## 3. Generalization plan — what the platform needs to become multi-datastore

The grounding's reuse split is the spine: **~25% transfers as-is, ~40% needs a per-datastore interface/re-shaped payload, ~35% is fully bespoke (and decisive).** Below is the concrete refactor, layer by layer, mapped to that split.

### 3.1 Per-datastore T1 proto + contract test (needs-abstraction → per-datastore)
Keep the **discipline**, redefine the **fields**. The reflection-based allowlist contract test ([`contract_test.go:20-45`](../../internal/proto/lynceus/v1/contract_test.go)) is the most valuable transferable *idea* — it makes "no literal field" a compile/CI gate, not a code-review hope. Generalize:
- One proto package per datastore family: `proto/lynceus/pg/v1` (existing), `proto/lynceus/redis/v1`, `proto/lynceus/os/v1`.
- Each defines its own Snapshot envelope and message set. Redis: `CommandShapeStat`, `KeyspaceStat`, `MemoryStat`, `EvictionStat`, `ReplicationSample`, `ClientSample`, `LatencyEvent` (T1) + `KeyIdentity`, `BigKeySample` (T2). OpenSearch: `ShardStat`, `NodeJVMStat`, `BreakerStat`, `ThreadpoolStat`, `MappingStat`, `SlowQueryStat` (T1).
- **Each message gets its own allowlist contract test.** The discipline is non-negotiable: for Redis, the test must assert the slowlog `[arg array]` can **never** map to a T1 field; for OpenSearch, that the `source[]` body and Profile `description` can never appear in T1.

### 3.2 Collector Reader interface (postgres-specific readers → interface)
Today readers are hardcoded pgx SQL ([`reader.go`](../../internal/collector/reader.go) and siblings). Introduce a `Reader` seam:
```
type Reader interface {
    Collect(ctx) ([]*anypb.Any, error)  // emits T1 sub-messages for one surface
    Capabilities() []caps.Capability     // which probes must be green to run
}
```
- Postgres readers refactor behind it unchanged (pgx SQL).
- Redis readers: `info`/`slowlog`/`latency`/`memory`/`client`/`keyspace-sampler` over a `go-redis` pool + a **managed parameter-group reader** (AWS API) to recover `CONFIG` values ElastiCache hides.
- OpenSearch readers: HTTP GETs against `_nodes/stats` / `_cat/*` / `_cluster/stats` + a slowlog/CloudWatch tail. **The collector forks by environment**: self-hosted tails the slowlog file; Amazon OpenSearch Service pulls from CloudWatch (needs an IAM/CloudWatch read path on top of the monitor role).

### 3.3 Per-datastore Normalizer — the literal-free fingerprinter (fully bespoke, decisive)
This is the 35% that *is* the product. There is no shared code here; there is a **shared contract**:
```
type Normalizer interface {
    // Returns (template, fingerprint, TierNormalized) on success;
    // ("", "", TierBlocked) on any failure. MUST fail closed.
    Normalize(raw) (template, fingerprint string, tier Tier)
}
```
- **Postgres:** the existing `internal/normalize` (libpg_query) satisfies it as-is — keep it the reference implementation.
- **Redis (`redisnorm`):** parse the slowlog arg array using cached `COMMAND` key-specs → keep command verb + key positions, drop values; apply operator key-pattern allowlist to keys; **fail closed to command-shape-only** when key-specs are incomplete (movable-key commands). Key *identity* is never emitted T1 — it is a separate T2 path. **Highest risk/effort item in the whole expansion.**
- **OpenSearch (`osnorm`):** the JSON DSL-tree walker — allowlist clause types, leaf-value → `$n`, parse-or-drop `query_string` Lucene, drop/T2 Painless, plus a serialize-then-re-verify guard cloned from `NormalizeCondition`. **Owns the fail-open risk** — invest the verification guard here.

The interface makes the fail-closed *posture* uniform even though no two implementations share a line of parsing logic.

### 3.4 Insight / check / advisor engines (needs-abstraction: harness generic, predicates bespoke)
The harnesses are already clean: `insight.Detector` is a pure function over a typed struct ([`insight.go:38-50`](../../internal/insight/insight.go)), `checks.Check` is a pure predicate over an assembled `Input`. **Keep the registry + dispatch + Severity + walkPath machinery generic; swap the predicate set per datastore.**
- Postgres detectors (SlowScan, DiskSort, NestedLoop, …) stay keyed on EXPLAIN node strings.
- Redis: new detectors over `MemoryStat`/`KeyspaceStat`/`CommandShapeStat` (hit-ratio, eviction pressure, fragmentation, big/hot key, TTL hygiene); the built-in `LATENCY DOCTOR`/`MEMORY DOCTOR` text can be *normalized verbatim* (OSS-only — **not reachable on managed Redis Cloud/Software**, so do not credit them for the managed story).
- OpenSearch: detectors over `ShardStat`/`NodeJVMStat`/`BreakerStat`/`SlowQueryStat` (oversharding, mapping explosion, GC pressure, breaker trips, threadpool rejections, deep-pagination/leading-wildcard slow queries). **Highest parity ceiling of the three.**
- Advisor (Postgres index/vacuum) is the most Postgres-shaped (btree leading-column heuristics over normalized predicates) — treat as Postgres-only for now; each datastore grows its own remediation set.

### 3.5 Capability probes + gate (needs-abstraction: Discoverer/Gate generic, probe bodies bespoke)
`caps.Discoverer → Capability → Status → Gate` is a clean design ([`probes.go`](../../internal/caps/probes.go)); generalize the probe **bodies**:
- Redis: `ACL WHOAMI`/`ACL GETUSER` for `+slowlog`/`+info`/`+latency` grants; `INFO server` version; `CONFIG GET maxmemory-policy` (or parameter-group API on managed) for LFU/hotkeys; **managed-tier + serverless detection** (serverless ⇒ gate off the entire slow surface).
- OpenSearch: has `cluster_monitor`? slowlog thresholds > -1? source logging on? CloudWatch publishing on?
- The RDS/Aurora "limited role, no superuser, no extensions" constraint becomes a per-datastore probe matrix — the existing design already encodes exactly this idea for Postgres.

### 3.6 CRD kinds per datastore (control plane — note: not yet implemented)
**Reality check from the grounding:** there is no k8s operator/CRD code yet (`cmd/` has only `api`, `collector`, `ingestion`). The *realized* control plane is config-DB-backed capability policy served at `/api/servers/{id}/policy-snapshot` and pulled by [`collector.FetchPolicySnapshot`](../../internal/collector/policy_refresh.go) into a `caps.Gate` map. The delivery mechanism (HTTP snapshot → gate) is datastore-agnostic; only the *capability vocabulary* it toggles is Postgres-specific. So:
- Generalize the policy snapshot to carry a `datastore_kind` discriminator + per-kind capability vocabulary. This is the cheap, available win — do it before any CRD work.
- When/if the operator lands, define CRD kinds per datastore family: `PostgresMonitor`, `RedisMonitor`, `OpenSearchMonitor` — each carrying connection params, which-surfaces-to-poll, the key-pattern allowlist (Redis) / clause-type allowlist (OpenSearch), and T2 enablement flags. The CRD→policy-snapshot→gate path itself is reusable; only the spec fields differ.

### 3.7 Transfers as-is (~25% — do nothing)
The stats-store partitioning/COPY/retention engine ([`stats.go`](../../internal/store/stats.go) — note the *row schemas* mirror Postgres stats and need per-datastore tables, but the partition/COPY/retention *engine* is generic), the fleetview cluster→instance→stream entity model ([`fleet.go`](../../internal/store/fleet.go) — Redis: Cluster/Sentinel→instance, keyspace DBs/slots→stream; OpenSearch: cluster→node→shard), the ingestion websocket/rate-limit/DLQ transport, and the policy-snapshot→gate delivery. These carry no datastore assumption beyond primary/replica topology naming.

---

## 4. Effort ranking + recommendation

### Effort ranking (easier/higher-value first)

**1st — OpenSearch / Elasticsearch (easier *and* higher value).**
- **Access is the cleanest** (`cluster_monitor` read-only role = direct `pg_monitor` analog; managed APIs supported).
- **Parity ceiling is the highest** (score 9 — shard/JVM/breaker/threadpool/mapping/slow-query insights are genuinely opinionated and underserved by existing OSS tools).
- **The differentiator transfers** (score 5 but *with work, not blocked*): the DSL is structured JSON, normalizable by a walker we own, and Query Insights proves the skeleton is meaningful. The fail-open risk is real but mitigable with the `NormalizeCondition`-style guard.
- **Cost:** the normalizer is net-new but bounded (JSON tree-walk + one Lucene sub-grammar), and >half the code is new — but it is *additive*, riding on the 25% as-is infra + the generic engine harnesses.

**2nd — Redis / Valkey / Falkey (higher demand, but the differentiator is compromised).**
- **Larger addressable market** and a single collector covers three forks.
- **But the privacy crux is the weakest** (score 4): the flagship key-level insight is inherently T2, the key normalizer is heuristic/fail-open-on-keys, and **managed reachability is tier-fragmented** (node-based works; Serverless collapses to INFO/CloudWatch metrics, killing the slow-command stream entirely). The OSS-only `*DOCTOR` advisors don't help the managed story.
- **Cost:** `redisnorm` (slowlog arg-array parser + key-spec cache + movable-key fallback + key-pattern allowlist) is the **single highest-risk item across both expansions**, and it delivers a *weaker* privacy story than Postgres or OpenSearch.

### Recommendation

**Expand to OpenSearch/Elasticsearch first.** It is the expansion that *stays Lynceus*: the literal-free differentiator transfers with bounded, ownable engineering; the access story is the cleanest; the parity ceiling is the highest; and it forces exactly the generalization refactor (per-datastore proto+contract-test, Reader interface, Normalizer interface, generic-harness/bespoke-predicate split, policy-snapshot discriminator) that any third datastore will then reuse cheaply. Treat OpenSearch as the proof that the platform is *a platform*, not a Postgres tool.

**Before any of that, do the cheap structural prerequisite:** extract the `Reader` and `Normalizer` interfaces and add the `datastore_kind` discriminator to the policy snapshot, against the existing Postgres implementation. This is low-risk refactoring with no new datastore, it keeps the Postgres tests green as the regression net, and it converts "multi-datastore" from a rewrite into an additive plug-in.

**Defer Redis** until the OpenSearch expansion has proven the interfaces, and when you do build it, **scope it honestly**: command-shape T1 telemetry + T2-gated key identity, *self-hosted and node-based managed only*, with the privacy positioning stated as "command-shape literal-free, key-identity audited" rather than the unqualified "literal-free" claim Postgres earns. Falkey/Valkey come for free with the Redis collector. Do **not** lead the Redis story on the `*DOCTOR` advisors (OSS-only) or on Serverless ElastiCache (no slow surface).

---

## 5. Open questions (resolve before committing engineering)

1. **OpenSearch field-name policy:** are DSL field names T1 (treated as non-secret identifiers like SQL columns) or do they need hashing? This is a new privacy-policy decision with no Postgres precedent and it gates the `osnorm` design.
2. **Redis key-normalization heuristic ownership:** is a per-deployment operator-supplied key-pattern allowlist acceptable as the *only* gate on T1 key shapes, or does legal/security require keys to be T2-always? This decides whether Redis has *any* T1 key-level signal.
3. **Managed ingestion forks:** for OpenSearch managed, is taking a hard dependency on AWS CloudWatch/IAM in the collector acceptable, or must we abstract a "managed log source" seam first? Same question for Redis ElastiCache parameter-group API.
4. **Query Insights dependency:** do we require the operator to enable OpenSearch Query Insights similarity grouping (off by default) and rely on it, or do we always build our own walker over the raw slowlog and treat Query Insights as optional enrichment? (Recommend the latter for fail-closed control.)
5. **Contract-test reach:** the current contract test is per-message hand-maintained allowlists. With 3+ datastores × ~10 messages each, should the allowlist discipline become a generated/annotated mechanism (e.g. proto field option `(lynceus.tier) = T1`) so the test enumerates from annotations rather than hand-kept maps?
6. **Operator/CRD timing:** the CRD control plane is described in the architecture but unbuilt. Does multi-datastore expansion wait on the operator, or proceed on the existing config-DB policy-snapshot path (recommended) with CRDs as a later layer?
