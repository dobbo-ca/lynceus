# Lynceus Transferability: Redis / Valkey / Falkey

**Date:** 2026-06-22
**Datastore:** Redis (SSPL/RSALv2/AGPLv3 tri-license, v8+) and Redis-compatible forks — Valkey (Linux Foundation, BSD-3), Falkey/other forks.
**Verdict:** `partial-fit` — strong on access/parity/platform-reuse, but the **decisive privacy-normalization criterion is materially weaker than Postgres**. The Redis slow-command stream is *fingerprintable in structure* but **not cleanly literal-free**, because (a) the slowlog stores argument values verbatim with no redaction, and (b) Redis **key names are themselves the namespace** and routinely encode identifiers/PII — they cannot be deterministically separated from "structure" the way SQL literals can.

---

## Transferability Scores (0–10)

| Criterion | Score | One-line justification |
|---|---|---|
| **A. Access** | **7** | A non-admin ACL user CAN be granted INFO/SLOWLOG/LATENCY/CLIENT; managed (ElastiCache) keeps these but kills CONFIG/MONITOR/DEBUG. |
| **B. Privacy normalization (DECISIVE)** | **4** | Command structure is deterministically parseable (COMMAND key-specs → strip arg values), but key names carry identifiers/PII and cannot be deterministically reduced to a literal-free pattern. No parser-grade "fail-closed" guarantee like libpg_query gives Postgres. |
| **C. Parity** | **8** | Rich opinionated signals: hot/big keys, eviction/maxmemory pressure, fragmentation, slow/blocking commands, repl lag, conn saturation, TTL hygiene; LATENCY DOCTOR / MEMORY DOCTOR are built-in advisors. |
| **D. Platform reuse** | **6** | Collector pattern, T1/T2 model, caps-probe/gate, ingestion, fleetview, checks engine reuse well; the entire normalize+fingerprint layer and all readers are bespoke. |
| **E. HA / cost / positioning** | **7** | Clean cluster→instance→stream mapping (Cluster shards / Sentinel / primary-replica); strong managed-cloud-gap positioning, but commodity-metrics competition is crowded. |

---

## B. PRIVACY CRUX (decisive) — can the slow-command stream be normalized literal-free?

**Short answer: partially, and not with the deterministic, fail-closed guarantee that makes Lynceus-on-Postgres defensible.**

### What the raw stream contains (no redaction)
- **`SLOWLOG GET` entries store the command arguments verbatim.** Each entry = `[id, unix_ts, exec_micros, [arg array], client_addr, client_name]`. The arg array is "the array composing the arguments of the command" — i.e. command name + **key names + argument values**. There is **no redaction by default**. ([SLOWLOG GET docs](https://redis.io/docs/latest/commands/slowlog-get/))
- Confirmed in source: `slowlogCreateEntry()` in `src/slowlog.c` stores args verbatim and applies **only memory-bounding truncation** — long strings get `"... (%lu more bytes)"` appended (`server.slowlog_max_string_len`), and excess args collapse to `"... (%d more arguments)"` (`server.slowlog_max_argc`). **Zero redaction logic**; key names, argument values, and even passwords sit intact up to the length cap. ([redis/redis src/slowlog.c](https://github.com/redis/redis/blob/unstable/src/slowlog.c))
- **`MONITOR`** is even more exposing — a firehose of every command. The *only* redaction is `AUTH` (since 6.0) and historically a few auth-bearing commands; notably **`EVAL`/`EVALSHA` args are explicitly logged again as of 6.2.4**. So argument values are *not* generally redacted. ([MONITOR docs](https://redis.io/docs/latest/commands/monitor/))

### Why fingerprinting is *structurally* possible
Redis ships machine-readable command metadata that is the analogue of "where are the literals":
- **`COMMAND` / `COMMAND DOCS` / `COMMAND INFO`** expose **arity** and, pre-7.0, the legacy **first_key / last_key / step** scheme; 7.0+ adds richer **key specifications** (`begin_search` + `find_keys` range with `lastkey`/`keystep`). ([COMMAND key specs](https://redis.io/docs/latest/develop/reference/key-specs/))
- This lets a collector deterministically split any slowlog arg array into **(command name, key-argument positions, value-argument positions)** and discard the value positions. So `SET user:1001:session "<jwt>"` → `SET <key> <value>` is mechanical and reliable.

### Why it is NOT cleanly literal-free (the gap vs Postgres)
This is the decisive weakness:
1. **Key names ARE the schema in Redis.** There is no separate catalog. The product value of a hot-key / big-key / TTL insight depends on showing *which key (pattern)*. But Redis key naming convention is `{service}:{entity}:{id}:{field}` — keys routinely embed identifiers, and the documented anti-pattern of secondary-index keys puts **PII directly in the key** (e.g. `user:email:john@example.com → 1001`). ([Redis key naming](https://redis.io/docs/latest/operate/oss_and_stack/management/security/acl/), [key-naming conventions](https://medium.com/nerd-for-tech/unveiling-the-art-of-redis-key-naming-best-practices-6e20f3839e4a))
2. **Reducing a key to a literal-free pattern is heuristic, not deterministic.** Collapsing `user:1001:session` → `user:*:session` requires *guessing* which colon-segments are identifiers vs structure. Unlike libpg_query (Postgres's own C parser, deterministic, value-free output), there is no grammar that says "segment 2 is a literal." A naive "numeric segment → `*`" heuristic leaks on `user:email:john@example.com`, on UUID/hash keys, on free-form keys, and on apps that don't use the colon convention at all. **You cannot fail-closed cleanly:** if you drop any key you can't prove safe, you lose the very signal the product sells (the hot key).
3. **Argument values for many commands carry semantics you'd want but can't keep.** e.g. `GEOADD`, `ZADD score member`, Lua `EVAL` bodies, `HSET field value` — the member/field names are often as identifier-laden as keys.

### Contrast with the Lynceus Postgres crux
Lynceus's guarantee is that `internal/normalize.Normalize`/`Fingerprint` over `pganalyze/pg_query_go` (libpg_query) produces a provably literal-free fingerprint, and `planextract.NormalizeCondition` **fails closed** (returns empty + a surviving-single-quote guard). For Redis the equivalent fingerprint = `command-name + arg-arity/types + key-arg-positions`, which IS literal-free **for the command shape** — but the *moment you attach a key identity* (required for actionable insight) you re-introduce a literal that has no deterministic stripping rule. **The privacy differentiator survives for "command-shape" T1 telemetry but does not survive for the key-level insights that are Redis's core value.**

### A genuinely literal-free design IS possible — at reduced product value
You can ship T1 as: per-command-shape latency histograms (`SET`/`MGET[n]`/`EVAL` keyed by normalized shape), counts, arg-arity buckets, and *aggregate* big-key/hot-key statistics bucketed by **operator-supplied key-pattern allowlist** (the collector is given `user:*:session` patterns by config and only emits matches, mirroring the inventory schema-regexp boundary filter). Concrete key identities (the actual hot key) become **T2** (literal-bearing, off by default, RBAC+audit). This is a clean, defensible mapping onto the existing T1/T2 contract — but it means Redis's flagship insight (the specific hot/big key) is gated behind T2 by construction, whereas Postgres delivers its flagship insight (the normalized slow query) entirely in T1.

### Managed angle that helps
**AWS ElastiCache's CloudWatch *slow-log delivery* already redacts** — the exported `Command` field shows the command name and replaces actual key names/values with `"(N more arguments)"` to avoid exposing sensitive data. So the *managed-export* path is literal-free by AWS's own design (command-shape only) — but that's a redacted feed (no key identity at all), and it is the log-delivery path, **not** what `SLOWLOG GET` returns to a connected client (which is still verbatim). ([ElastiCache log delivery / slow-log format](https://repost.aws/knowledge-center/elasticache-turn-on-slow-log), [Log delivery docs](https://docs.aws.amazon.com/AmazonElastiCache/latest/dg/Log_Delivery.html))

---

## A. ACCESS — limited/read-only role + stats/slow surface

### Self-managed (ACL)
- Redis ACLs (6.0+) gate by **command, command category, key pattern (`~`), and pubsub channel (`&`)**. New users start `-@all`; the default user is `~* &* +@all`. ([ACL docs](https://redis.io/docs/latest/operate/oss_and_stack/management/security/acl/))
- **Category placement matters for the collector role:** `SLOWLOG`, `LATENCY`, `MONITOR` are in the **`admin`** category; `INFO`, `CONFIG`, `CLIENT`, `COMMAND`, `KEYS` are in **`dangerous`**. A blanket `+@read` does NOT grant them. ([ACL CAT](https://redis.io/docs/latest/commands/acl-cat/))
- **A working read-only monitoring role is constructible and well-precedented:** e.g. `~* +@read +info +slowlog +latency +client|info +command|docs +memory|usage +memory|stats +dbsize -@dangerous -@admin -keys -monitor` (grant specific admin/dangerous subcommands without the whole category). This is read-only and never mutates the DB — fits Lynceus's "read-only Postgres access" invariant. *(Construction follows documented ACL subcommand-grant syntax; exact string must be validated per version.)*
- Caveat: some signals need otherwise-dangerous commands — `SCAN` (for big-key sampling) is fine, but `--bigkeys`/`--hotkeys` walk the keyspace (overhead), `OBJECT FREQ` (hot keys) requires `maxmemory-policy *-lfu`, and `MONITOR` is admin + ~50% throughput hit so it's debug-only, never a steady T1 source.

### Managed cloud (the RDS-analog) — ElastiCache / MemoryDB
- **ElastiCache makes these commands *unavailable* (cannot be run at all):** `config`, `debug`, `monitor`'s siblings — exact list: `acl setuser/load/save/deluser`, `bgrewriteaof`, `bgsave`, `cluster addslot/delslot/setslot/failover/forget/meet/...`, **`config`**, **`debug`**, `migrate`, `psync`, `replicaof`, `save`, `slaveof`, `shutdown`, `sync`. ([ElastiCache RestrictedCommands](https://github.com/awsdocs/amazon-elasticache-docs/blob/master/doc_source/redis/RestrictedCommands.md))
- **Crucially, `SLOWLOG`, `INFO`, `LATENCY`, `CLIENT` are NOT restricted** → reachable by a non-admin RBAC user. This is better than feared: the core slow/stats surface survives in managed.
- **But `CONFIG` is unavailable** → the collector cannot read `maxmemory`, `maxmemory-policy`, `slowlog-log-slower-than`, etc. via `CONFIG GET`. These must come from the **ElastiCache parameter-group API** (an AWS control-plane call, not a data-plane Redis command) — a managed-specific reader, analogous to how Lynceus already treats RDS as no-superuser/no-extensions.
- ElastiCache **RBAC** (Valkey 7.2+, Redis OSS 6.0–7.2) supports access strings + IAM auth; `-@dangerous` disallows KEYS/MONITOR/SORT etc. SLOWLOG itself is supported (`valkey-cli slowlog get 10`). ([ElastiCache RBAC](https://docs.aws.amazon.com/AmazonElastiCache/latest/dg/Clusters.RBAC.html), [auth-redis](https://docs.aws.amazon.com/AmazonElastiCache/latest/dg/auth-redis.html))
- **MemoryDB** similarly restricts privileged commands but supports SLOWLOG/INFO for monitoring. ([MemoryDB restricted commands](https://docs.aws.amazon.com/memorydb/latest/devguide/restrictedcommands.html))
- **Managed slow-log export** (CloudWatch Logs / Kinesis Firehose, JSON/TEXT) is available for Valkey 7.x and Redis OSS 6.0+ — and is **already redacted** (see privacy section). A viable *alternative ingest path* for managed clusters where direct `SLOWLOG GET` polling is undesirable. ([Log delivery](https://docs.aws.amazon.com/AmazonElastiCache/latest/dg/Log_Delivery.html))

---

## C. PARITY — opinionated insight catalog (signal → source → remediation)

| Insight | Primary source | Opinionated output |
|---|---|---|
| **Cache hit ratio degradation** | `INFO stats` `keyspace_hits`/`keyspace_misses` | "hit ratio < X% → review TTLs / key churn / undersized cache" |
| **Eviction / maxmemory pressure** | `INFO` `evicted_keys`, `maxmemory`, `maxmemory_policy`, `used_memory` | "rising evictions at maxmemory → raise memory or change policy" ([eviction](https://redis.io/docs/latest/develop/reference/eviction/)) |
| **Memory fragmentation** | `INFO memory` `mem_fragmentation_ratio` (`used_memory_rss`/`used_memory`); **`MEMORY DOCTOR`** | "ratio >1.5 → activedefrag/restart; <1.0 → swapping, critical" ([MEMORY STATS](https://redis.io/docs/latest/commands/memory-stats/)) — built-in advisor reusable verbatim |
| **Latency spikes by event** | **`LATENCY DOCTOR` / `LATENCY HISTORY` / `LATENCY LATEST`** | Human-readable root-cause + remedy (fork, expire-cycle, AOF) — an *advisor already exists*, collector normalizes it ([LATENCY DOCTOR](https://redis.io/docs/latest/commands/latency-doctor/)) |
| **Slow / blocking commands** | `SLOWLOG GET` (normalized to command-shape) | "KEYS/large MGET/Lua/O(N) commands dominating P99 → SCAN/pagination/move to replica" |
| **Big keys** | `MEMORY USAGE`, `SCAN`+`--bigkeys` sampling | "key-pattern P has multi-MB values → split/compress" (key identity → T2) |
| **Hot keys** | `OBJECT FREQ` (LFU), `--hotkeys` sampling | "key-pattern P is access-hot → client-side cache / replica reads" (requires LFU policy) |
| **Replication lag** | `INFO replication` `master_repl_offset` vs replica offset, `master_link_status` | "replica N bytes behind / link down → investigate network/load" |
| **Connection saturation** | `INFO clients` `connected_clients`, `blocked_clients`, `maxclients`; `CLIENT LIST/INFO` | "approaching maxclients / many blocked → pool tuning" |
| **TTL hygiene** | `INFO keyspace` (`expires` vs `keys` per db); sampling | "DB has keys with no TTL in a cache workload → unbounded growth" |
| **Persistence health** | `INFO persistence` (`rdb_last_bgsave_status`, `aof_last_write_status`) | "last bgsave failed → durability risk" |

Verdict: **parity is strong** — and notably Redis ships two *opinionated advisors* (`LATENCY DOCTOR`, `MEMORY DOCTOR`) the collector can normalize/re-host, lowering the cost of "produce opinionated insight, not raw metrics." Most of these are pure T1 (numeric/aggregate, no literals). Only big-key/hot-key *identity* crosses into T2.

---

## D. PLATFORM REUSE breakdown

**Reuses as-is (interface unchanged):**
- **Ingestion server, stats store** (vanilla partitioned Postgres), **fleetview entity model** (cluster→instance→stream maps cleanly to Cluster→node→keyspace-DB / Sentinel master-replica group).
- **Checks/alerts engine, insight engine** — pattern reuses; thresholds/inputs are Redis-specific data.
- **T1/T2 tier model + audit_log + data_tier** — reuses, and is in fact *load-bearing* here: big-key/hot-key identity must live in T2.
- **Wire contract approach** (Snapshot envelope + reflection-based field-allowlist contract test) reuses **as a pattern**, but every message type is new.

**Needs an interface / abstraction:**
- **Capability probes + Gate (`internal/caps`)** generalize well: probes become `ACL WHOAMI`/`ACL GETUSER` (does my role have SLOWLOG/INFO/LATENCY?), `INFO server` version, `CONFIG GET maxmemory-policy` (is LFU on, for hot keys?), managed-vs-self-hosted detection. Same Discoverer→Capability→Status→Gate shape; Redis-specific probe bodies.
- **Wire contract** needs a parallel set of T1 messages: `CommandShapeStat` (normalized command + arity buckets + latency histogram), `KeyspaceStat`, `MemoryStat`, `EvictionStat`, `ReplicationSample`, `ClientSample`, `LatencyEvent`, plus a T2 `KeyIdentity`/`BigKeySample`. The contract test (no literal-bearing field in T1) is the same enforcement; the allowlist is new. **Critically, the `[arg array]` from slowlog must NEVER map to a T1 field** — same discipline as "never add raw_text to a T1 message."

**Bespoke (datastore-specific, all new):**
- **The entire normalize/fingerprint layer.** No libpg_query analogue. Build a `redisnorm` package: parse slowlog arg arrays using `COMMAND` key-specs (fetched once, cached) → emit command-shape fingerprint; apply operator-configured key-pattern allowlist for any key-level emission; **fail-closed** (drop the entry / send only command-shape if a key can't be matched to an allowlisted pattern). This is the highest-risk, highest-effort component and is where the privacy guarantee lives or dies.
- **All readers** — `info_reader`, `slowlog_reader`, `latency_reader`, `memory_reader`, `client_reader`, `keyspace_sampler` (SCAN-based big/hot key). Each issues Redis commands over a client pool (go-redis) instead of pgx SQL.
- **Managed parameter-group reader** for ElastiCache/MemoryDB (AWS API) to recover CONFIG values that `CONFIG GET` can't provide.

Rough split: ~40% reuses as-is, ~20% needs a Redis-shaped interface, ~40% bespoke (normalize layer + readers).

---

## E. HA / TOPOLOGY + COST / POSITIONING

- **Topology maps cleanly** onto fleetview: **Cluster** (16384 hash slots sharded across primaries, each with replicas), **Sentinel** (master + replicas + sentinel quorum), **standalone primary/replica**. A collector connects per-node (or via cluster discovery) → instance entities; the keyspace DBs/slots → stream entities. Repl lag via `master_repl_offset` deltas. ([Redis HA](https://redis.io/tutorials/operate/redis-at-scale/high-availability/), [replication](https://redis.io/docs/latest/operate/oss_and_stack/management/replication/))
- **Collector placement:** outbound-only, near-Redis, limited ACL role — same deploy story as Postgres; Helm chart reuses.
- **Positioning:** The Valkey fork (BSD, Linux Foundation, backed by AWS/Google/Oracle; default in Fedora/Ubuntu/Debian/Arch; millions of ElastiCache nodes migrated) created a large license-anxious user base that wants vendor-neutral OSS tooling — **good tailwind for an open-source, privacy-first monitor**. ([What is Valkey](https://redis.io/blog/what-is-valkey/))
- **Cost/competition risk:** Redis monitoring is a *crowded commodity* (Datadog, Grafana/Prometheus redis_exporter, SigNoz, RedisInsight, cloud-native CloudWatch). Lynceus's differentiators (privacy-first T1/T2, collector-side analysis, opinionated remediation) are real but the **privacy moat is weaker here** (see B), so positioning leans more on "opinionated, HA, k8s-native, multi-engine fleet view" than on the privacy story that anchors Postgres.

---

## Valkey / Falkey divergence notes
- **Valkey is protocol-compatible with Redis 7.2.4 base**; INFO/SLOWLOG/LATENCY/MEMORY/COMMAND/ACL all present and behave the same → collector readers are engine-agnostic across Redis 7.x and Valkey. ([What is Valkey](https://redis.io/blog/what-is-valkey/))
- **Divergence is growing** (Valkey 8.x multi-threaded I/O, different roadmap; Redis 8 folded JSON/TimeSeries/probabilistic/vector data types into core under the tri-license). Practical impact for monitoring: **new data types → new `MEMORY USAGE`/type-specific big-key semantics**, and version/engine detection (`INFO server` `redis_version` vs Valkey's reporting) must drive a caps probe. Falkey and other forks track the same surfaces but should be capability-probed, not assumed.
- **Licensing/positioning:** Redis = SSPLv1 / RSALv2 / AGPLv3 tri-license (8+); Valkey = BSD-3 (permissive). A Lynceus collector linking/embedding nothing of the server is unaffected, but the OSS-positioning message resonates most with the Valkey/BSD audience. ([Redis vs Valkey license](https://redis.io/blog/what-is-valkey/))

---

## Top risks
1. **Privacy normalization is not deterministic for key-level insight (decisive).** The product's most valuable Redis insights (the specific hot/big key) are inherently literal-bearing → forced into T2, weakening the "broadly-viewable privacy-first telemetry" pitch relative to Postgres. Mitigation: operator-supplied key-pattern allowlist + fail-closed; ship command-shape in T1, key identity in T2.
2. **Heuristic key-pattern collapsing leaks.** Any "auto-collapse numeric segments" approach leaks on email-in-key / UUID / non-convention keys. Must default to allowlist-only, never auto-anonymize.
3. **Managed CONFIG gap.** No `CONFIG GET` on ElastiCache → must build an AWS parameter-group reader; some insights (policy-dependent, e.g. hot keys needs LFU) can't even be *enabled* if the operator hasn't set the param group.
4. **Sampling overhead.** Big-key/hot-key discovery via SCAN/`--bigkeys`/`--hotkeys` and `MONITOR` carry real production cost; must be rate-limited, replica-targeted, and off by default.
5. **Slowlog is shallow.** Default `slowlog-max-len` 128 and a threshold-based capture → it's a *sample*, not a complete statement census like `pg_stat_statements`. Insight quality depends on threshold tuning the collector cannot set in managed (CONFIG restricted).
