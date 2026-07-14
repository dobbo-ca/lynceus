# Design: ClickHouse Stats Store (`chStats`) — Engine-Swap Milestone

- **Status:** Approved (brainstorming), ready for planning
- **Date:** 2026-07-14
- **Author:** Chris Dobbyn (with AI agent)
- **Source of truth:** [ADR — ClickHouse Stats Store + Bedrock](../../research/2026-07-14-clickhouse-bedrock-architecture.md) §6 (program decomposition), §4 (privacy guardrails)
- **Epic:** `ly-cwr` · relates to `ly-7vd` (ClickHouse optional T1 backend)
- **Program position:** ADR §6 sub-project **#3 (chStats)**; also designs **#4 (MV + RBAC)** and **#7 (isolated raw-table T2 retarget)** as sequenced follow-on tasks.

---

## 1. Goal

Make ClickHouse a stats-store backend behind the existing `store.Stats` interface (`internal/store/stats.go:15`), selected by config, **without breaking the privacy invariants**. The `pgxStats` Postgres implementation keeps working; the config/audit DB stays vanilla Postgres always.

This milestone is an **engine swap**: `chStats` round-trips the *existing* collector-normalized `QueryStat` contract into ClickHouse. It does **not** rebuild the ingestion/normalization path. The materialized-view normalization boundary (#4) and the network-isolated raw-table T2 retarget (#7) are designed here but implemented as later, sequenced tasks in the plan.

### Out of scope (this milestone)
- ADR #4 MV + RBAC — *implementation* (design captured in §9).
- ADR #7 raw-table isolation / `query_log` scrub / creds split — *implementation* (design captured in §9).
- ADR #5 direct-connect ingestion path, #6 WebSocket control plane, #8 detection, #9 Bedrock.
- ADR #10 retire the PG stats store — only after #3 + #7 land.

---

## 2. Key decisions (locked in brainstorming)

1. **Write-path scope = engine-swap first.** `chStats` writes the current collector-normalized `QueryStat` rows into ClickHouse tables and reads them back. No ingestion rewrite. #4/#7 sequence later.
2. **T1 and T2 live in separate ClickHouse tables** (`query_stats` vs `query_stats_t2`), from day one. Table identity is the tier boundary. This makes #7 isolation a grant/creds change, not a data migration.
3. **`Pool()` is removed from the `Stats` interface.** Its only consumer is the checks scheduler's `pg_try_advisory_lock`. The scheduler takes a dedicated lock pool = the **config DB pool (always vanilla Postgres)**. `chStats` therefore never needs to fake a `*pgxpool.Pool`.
4. **Backend selection = env factory, no default.** `LYNCEUS_STATS_BACKEND` is **required** (`postgres` | `clickhouse`). Existing Postgres deployments/CI must set `LYNCEUS_STATS_BACKEND=postgres`.

---

## 3. Architecture

```
                       store.Stats  (interface — the seam, unchanged consumers)
                              ▲
              ┌───────────────┴────────────────┐
        pgxStats (Postgres)              chStats (ClickHouse)   ← new
        internal/store/stats.go          internal/store/chstats.go
              │                                 │
     PG stats DB (partitioned)          ClickHouse (org-operated)
     query_stats (data_tier col)          ├─ query_stats      (T1, MergeTree, TTL 90d)
                                           └─ query_stats_t2   (T2, MergeTree, TTL 7d)

  Backend chosen by store.OpenStats(ctx): LYNCEUS_STATS_BACKEND ∈ {postgres, clickhouse}

  T2 read path (unchanged gateway):
    api_server ─▶ T2Reader (internal/store/t2_read.go)
        fast-reject servers.t2_enabled
        ─▶ EffectiveCapability (config PG)
        ─▶ AppendAuditReturning FIRST, fail-closed  → config PG hash-chain
        ─▶ stats.ReadQueryStatsTier2()  → reads query_stats_t2 (CH) when backend=clickhouse

  config / audit DB = vanilla Postgres, always (hash-chain, VerifyChain, advisory locks)
```

Consumers (`internal/ingest`, `internal/api`, `internal/checks`, `internal/fleetview`) already depend on `store.Stats`, not the concrete type. The only interface change is the removal of `Pool()` (§5).

---

## 4. Components

### 4.1 Driver + type
- Dependency: `github.com/ClickHouse/clickhouse-go/v2` (native protocol, port 9000). Native `conn.PrepareBatch` is the COPY analog for efficient batch inserts.
- New file `internal/store/chstats.go`:
  - `type chStats struct { conn clickhouse.Conn }`
  - `var _ Stats = (*chStats)(nil)`
  - `func NewCHStats(conn clickhouse.Conn) *chStats`

### 4.2 Interface change — remove `Pool()`
- `Stats` loses `Pool() *pgxpool.Pool`.
- `checks.NewScheduler` gains a lock-pool parameter: a `*pgxpool.Pool` for the **config DB** (always vanilla PG). `sc.stats.Pool().Acquire(ctx)` → `sc.lockPool.Acquire(ctx)`; the `pg_try_advisory_lock` / `pg_advisory_unlock` calls run on the config pool.
- `pgxStats` keeps a concrete `Pool()` method (it is still handy), just not in the interface.
- Wiring: `cmd/ingestion` currently opens only the stats pool and uses it for the scheduler lock. Under CH mode there is no stats PG pool, so ingestion gains a **config-DB connection** for the scheduler lock. This is harmless in PG mode too (config DB always exists).

### 4.3 CH schema + migrations
- New embedded dir `internal/store/migrations/clickhouse/`, new runner `ApplyClickHouseMigrations(ctx, conn)`.
- Migration runner: applies each `*.sql` in lexical filename order, idempotently. Tracks applied versions in a CH `schema_migrations` table (`ENGINE = MergeTree ORDER BY version`; insert version after apply). ClickHouse has no transactional DDL, but the DDL is `CREATE TABLE IF NOT EXISTS`, so re-apply is safe; the version table skips already-applied files.
- `0001_query_stats.sql`:

```sql
-- T1: normalized, broadly readable
CREATE TABLE IF NOT EXISTS query_stats (
  server_id String,
  collected_at DateTime64(3, 'UTC'),
  fingerprint String,
  normalized_query String,
  data_tier Int16 DEFAULT 1,
  calls Int64,
  total_time_ms Float64,
  mean_time_ms Float64,
  rows Int64,
  shared_blks_hit Int64,
  shared_blks_read Int64
) ENGINE = MergeTree
PARTITION BY toYYYYMM(collected_at)
ORDER BY (server_id, fingerprint, collected_at)
TTL toDateTime(collected_at) + INTERVAL 90 DAY;

-- T2: literal-bearing, gateway-only, short custody window
CREATE TABLE IF NOT EXISTS query_stats_t2 (
  server_id String,
  collected_at DateTime64(3, 'UTC'),
  fingerprint String,
  normalized_query String,
  data_tier Int16 DEFAULT 2,
  calls Int64,
  total_time_ms Float64,
  mean_time_ms Float64,
  rows Int64,
  shared_blks_hit Int64,
  shared_blks_read Int64
) ENGINE = MergeTree
PARTITION BY toYYYYMM(collected_at)
ORDER BY (server_id, collected_at)
TTL toDateTime(collected_at) + INTERVAL 7 DAY;
```

- `data_tier` stays as a column (so `QueryStat` round-trips unchanged) but is **not** in either `ORDER BY` — the table itself encodes the tier.
- No `EnsureWeeklyPartition` equivalent: ClickHouse auto-creates parts. Retention is the table `TTL`, not `DropPartitionsOlderThan`.
- T2 `TTL` is 7 days (short custody window). The `Null`-engine "zero literals at rest" option (ADR open-Q #3) is deferred to #7.

### 4.4 Write — `WriteQueryStats`
- Route the input slice by `DataTier`: rows with `DataTier == 2` batch into `query_stats_t2`; all others (`0` normalized to `1`) batch into `query_stats`.
- Two `PrepareBatch` streams, one method call. Empty batches are skipped.
- The `DataTier == 0 → 1` default matches `pgxStats.WriteQueryStats`.

### 4.5 Read (first slice) — `TopQueriesByTotalTime`
```sql
SELECT fingerprint, normalized_query, SUM(calls), SUM(total_time_ms)
  FROM query_stats
 WHERE collected_at >= ? AND collected_at < ? AND data_tier = 1
 GROUP BY fingerprint, normalized_query
 ORDER BY SUM(total_time_ms) DESC
 LIMIT ?;
```
Reads only `query_stats` (T1). `?` positional placeholders via clickhouse-go native. Timestamps are UTC `DateTime64(3)`.

### 4.6 T2 — `ReadQueryStatsTier2`
- Reads `query_stats_t2` (its own table): `WHERE server_id = ? AND collected_at >= ? AND collected_at < ? ORDER BY collected_at DESC LIMIT ?`. The `data_tier = 2` filter is implicit (the table holds only T2), retained in the WHERE for clarity/defense.
- The `T2Reader` gateway ordering (fast-reject → `EffectiveCapability` → `AppendAuditReturning` first, fail-closed → sole SELECT) is **unchanged**; the audit row still goes to the **config PG** hash-chain. Only the backing SELECT target moves from PG to CH.

### 4.7 Remaining ~35 methods
Stubbed to satisfy the interface (`return fmt.Errorf("chStats.<Method>: not implemented")` / typed-nil returns), filled per later TDD slices (task **t3**) grouped by domain: activity buckets, plans, insights, table/index stats, freeze/xmin, settings, connections/blocking edges, checks results/mutes, log events, `RecentServerIDs`, throughput/QPS/summary aggregates.

### 4.8 Backend factory (task t4)
- `func OpenStats(ctx context.Context) (Stats, error)` reads `LYNCEUS_STATS_BACKEND` (**required**):
  - `postgres` → open `LYNCEUS_STATS_DSN` pgxpool, `ApplyStatsMigrations`, `NewStats`.
  - `clickhouse` → open `LYNCEUS_CLICKHOUSE_DSN` clickhouse.Conn, `ApplyClickHouseMigrations`, `NewCHStats`.
  - unset/other → error.
- Wire into `cmd/ingestion` and `cmd/api`. Document that PG deploys/CI must set `LYNCEUS_STATS_BACKEND=postgres`.

---

## 5. Testing strategy (TDD, real ClickHouse)

- Add `github.com/testcontainers/testcontainers-go/modules/clickhouse`.
- New `internal/testch/testch.go` mirrors `internal/testpg/testpg.go`: boot a CH container (image `clickhouse/clickhouse-server:25.8` to match dev), wait until it accepts queries (`SELECT 1`), apply CH migrations, return a `clickhouse.Conn` (+ cleanup). Integration tests hit a **real** ClickHouse — no mocking.
- **First red→green slice** (`internal/store/chstats_test.go`):
  1. `WriteQueryStats` + `TopQueriesByTotalTime` round-trip: write N T1 rows across fingerprints, assert results ordered by total time desc with correct summed `calls`/`total_time_ms`.
  2. **Separation test:** write a mix of `DataTier=1` and `DataTier=2` rows; assert `TopQueriesByTotalTime` returns **only** the T1 rows, and `ReadQueryStatsTier2` returns **only** the T2 rows (from `query_stats_t2`). Proves T2 never leaks into T1 reads and lands in its own table.
- These tests are red first (no `chStats` impl), then green.

---

## 6. Privacy invariants (must remain true)

1. **Authoritative audit stays vanilla Postgres** (hash-chain, `VerifyChain`, advisory locks). `T2Reader` ordering and audit target unchanged. ✓
2. **No new raw/literal field on any T1 proto message.** `chStats` never touches the wire contract. ✓
3. **`data_tier` carried through** to ClickHouse (as column). ✓
4. **No feature depends on the backend.** `store.Stats` is the seam; `pgxStats` stays fully functional; backend selected by config. ✓
5. **T1 and T2 physically separated** in CH from day one; T2 reachable only via `ReadQueryStatsTier2` (gateway-only). ✓

---

## 7. Known interim gap (must be loud in the plan + bead)

Until task **t6 (#7)** lands, `query_stats` and `query_stats_t2` sit in the **same CH database under one `chStats` credential**. The ADR §4.4 raw-isolation guardrail (network-isolated raw table, dedicated gateway creds, `query_log` scrub) — described as the *single most important* guardrail once literals are at rest in CH — is **not yet enforced**. This is acceptable **only** because this milestone targets **dev/test**, never customer production literal custody. #7 must land the isolation (separate CH database/role/creds granting the gateway `query_stats_t2` only; `query_log` disabled/scrubbed on that path) **before** any production T2 literal is written to ClickHouse.

---

## 8. Task sequence (beads under `ly-cwr`)

| Task | Description | Depends |
|---|---|---|
| **t1** | Remove `Pool()` from `Stats`; `checks.Scheduler` takes a config-PG lock pool; rewire `cmd/ingestion` (+ `cmd/api`). Keeps `main` green on `pgxStats`. | — |
| **t2** | `chStats` skeleton + `clickhouse-go/v2` dep + CH migrations (`query_stats`, `query_stats_t2`) + `internal/testch` harness + **first red→green slice** (`WriteQueryStats`, `TopQueriesByTotalTime`, `ReadQueryStatsTier2`, separation test). **← first code deliverable** | t1 |
| **t3** | Fill remaining ~35 `Stats` methods on `chStats`, TDD per domain group. | t2 |
| **t4** | `store.OpenStats` backend factory + `cmd/*` wiring + env docs. | t2 |
| **t5 (#4)** | Normalization materialized view + CH RBAC (grant T1 target, deny raw) — *design captured; implement after t3.* | t3 |
| **t6 (#7)** | Isolated raw table + retarget `ReadQueryStatsTier2` + short TTL + `query_log` scrub + gateway creds split. | t3, t4 |

`ly-cwr` children carry `needs-plan` until the plan is written, then flip to `ready-impl`.

---

## 9. Follow-on design notes (#4 and #7)

Captured now so the engine-swap tables are shaped to receive them:

- **#4 MV boundary:** Under the ADR target, the collector ships *raw* rows to a raw table; a ClickHouse **materialized view** derives the normalized T1 target, and RBAC grants Lynceus/Bedrock the T1 target only (raw denied). In the engine-swap tables, `query_stats` is the eventual MV *target* shape and `query_stats_t2` is the eventual *raw* table. When #4 lands, `query_stats` becomes an MV target populated from the raw table rather than written directly.
- **#7 isolation:** `query_stats_t2` moves to a dedicated CH database/role; the gateway gets isolated creds that can read only it; analysis/Bedrock creds are denied it; `query_log` is scrubbed/disabled on that path; TTL may switch to a `Null`/ultra-short engine per the retention decision (ADR open-Q #3).

---

## 10. Open items

1. **T2 retention policy** (ADR open-Q #3): short TTL (7d here, retrospective T2, bounded custody) vs `Null`/ultra-short engine (zero literals at rest, no retrospective T2). Deferred to #7; 7-day TTL is the interim placeholder.
2. **CH DSN format / TLS** for `LYNCEUS_CLICKHOUSE_DSN` and how `secure.CheckDatabaseDSN` (currently PG-oriented) applies to ClickHouse. Resolve in t4.
3. **`ORDER BY` tuning** for the aggregate reads filled in t3 (throughput/QPS/activity) — validate against real query shapes when those methods are implemented.
