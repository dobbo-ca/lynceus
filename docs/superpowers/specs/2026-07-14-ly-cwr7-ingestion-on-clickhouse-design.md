# Spec: Ingestion on ClickHouse (ly-cwr.7)

- **Status:** Draft (design-ready)
- **Date:** 2026-07-14
- **Epic:** ly-cwr — Lynceus core architecture (privacy-first hybrid)
- **Bead:** ly-cwr.7 — ingestion-on-ClickHouse: move DLQ + schema_objects off the stats PG pool
- **ADR:** `docs/research/2026-07-14-clickhouse-bedrock-architecture.md` (§2 decisions, §5 current-state facts, §6 program item #5)
- **Depends on:** ly-cwr.4 (`store.OpenStats` factory), ly-cwr.3 (chStats — all telemetry Write* methods) — both merged (PR #42).

---

## 1. Problem

`cmd/ingestion` cannot run on the ClickHouse backend. The typed write path (all 12 row
types) already routes through `store.Stats` → `chStats`, but `ingest.NewServer(cfg, stats,
pool)` hard-depends on a `*pgxpool.Pool` for **two** writes that bypass the `Stats` seam:

1. **DLQ** — `parkDLQ` runs `pool.Exec("INSERT INTO dlq …")` against the stats PG pool.
2. **schema_objects** — `store.NewSchemaObjects(pool).UpsertSchemaObjects(…)` against the stats PG pool.

So `cmd/ingestion` still hardcodes `store.NewStats(pool)` (Postgres) instead of
`store.OpenStats(ctx)`. Both remaining PG couplings must move so ingestion is
backend-agnostic and can run with `LYNCEUS_STATS_BACKEND=clickhouse`.

**Verified couplings (grep, 2026-07-14):** `dlq` is touched only by `ingest/server.go`;
`NewSchemaObjects` is constructed only by `ingest/server.go` and one unit test. `dlq` and
`schema_objects` have **no production reader** in the tree today (the Index Advisor /
schema-change consumers that will read `schema_objects.first_seen_at` are future work). CH
migrations define neither table, so no name collision.

---

## 2. Decision: both DLQ and schema_objects move to the stats backend (ClickHouse), via the `Stats` seam

Per the operator's data-classification rule: **configuration / detections / findings / LLM
output / authoritative audit stay in Postgres; logs, metrics, and collected data go to
ClickHouse.**

- **schema_objects** is collected telemetry about the monitored Postgres (object inventory +
  sizes) → **ClickHouse**.
- **dlq** is the parking table for ingest frames that failed to write. The parked payloads are
  serialized `Snapshot` protobufs — **T1, literal-free by contract** — i.e. collected data
  that failed to land. It has **no retry/reader consumer today** (append-with-TTL, not yet a
  mutable queue), so the "mutable operational queue → Postgres" objection does not bind → **ClickHouse**.

Both ride the `Stats` interface (the single backend seam), so `ingest.Server` drops its
`*pgxpool.Pool` field **entirely** and becomes fully backend-agnostic. This also pre-advances
ly-cwr.5 (all telemetry on the seam).

The config/audit Postgres DB is **not touched** by this change. `cmd/ingestion` keeps a config
pool solely for the checks scheduler's cross-replica advisory lock (added in ly-cwr.1).

### 2.1 What stays in Postgres

Nothing new moves to Postgres. The existing PG stats migrations `0002_dlq.sql` and
`0005_schema_objects.sql` remain in `migrations/stats/` — they are the **postgres-backend**
implementations of these two tables, reached by `pgxStats`. They are unchanged.

---

## 3. Interface changes

Add two methods to `store.Stats` (both backends implement):

```go
WriteSchemaObjects(ctx context.Context, rows []SchemaObjectRow) error
ParkDLQ(ctx context.Context, serverID, reason string, raw []byte) error
```

- **`pgxStats.WriteSchemaObjects`** — delegates to the existing upsert:
  `return NewSchemaObjects(s.pool).UpsertSchemaObjects(ctx, rows)`. No SQL duplication; the
  standalone `SchemaObjects` type and its first-seen-stability unit test are retained as the
  PG-upsert validation.
- **`pgxStats.ParkDLQ`** — `INSERT INTO dlq (server_id, reason, raw) VALUES (NULLIF($1,''), $2, $3)`
  (the current `parkDLQ` SQL, lifted verbatim).
- **`chStats.WriteSchemaObjects`** / **`chStats.ParkDLQ`** — see §4.

`var _ Stats = (*pgxStats)(nil)` and `var _ Stats = (*chStats)(nil)` keep both impls honest at
compile time.

---

## 4. ClickHouse schema

Two new migrations, applied by the existing `ApplyClickHouseMigrations` runner (semicolon-split,
`CREATE TABLE IF NOT EXISTS`, version-tracked):

### 4.1 `migrations/clickhouse/0011_dlq.sql`

```sql
CREATE TABLE IF NOT EXISTS dlq (
  received_at DateTime64(3, 'UTC') DEFAULT now64(3),
  server_id   String,
  reason      String,
  raw         String            -- serialized protobuf; ClickHouse String is binary-safe
) ENGINE = MergeTree
PARTITION BY toYYYYMM(received_at)
ORDER BY (received_at, server_id)
TTL toDateTime(received_at) + INTERVAL 14 DAY;
```

Append-only, TTL-bounded (there is no retry consumer to delete rows). `server_id` is `''` for
pre-`server_id` failures (unmarshal errors), matching the current `NULLIF($1,'')` intent.

### 4.2 `migrations/clickhouse/0012_schema_objects.sql`

`schema_objects` is a **current-state** table with a **stable `first_seen_at`** — the
load-bearing semantic (downstream Index Advisor / schema-change insights rely on it). In
ClickHouse this needs an engine that (a) collapses to one row per `(server_id, kind, fqn)` so
volume stays bounded, and (b) preserves the **minimum** `first_seen_at` across re-observations.
`AggregatingMergeTree` with `SimpleAggregateFunction` does both without a read-before-write:

```sql
CREATE TABLE IF NOT EXISTS schema_objects (
  server_id     String,
  kind          Int16,
  fqn           String,
  schema        SimpleAggregateFunction(anyLast, String),
  name          SimpleAggregateFunction(anyLast, String),
  size_bytes    SimpleAggregateFunction(anyLast, Int64),
  is_partition  SimpleAggregateFunction(anyLast, UInt8),
  parent_fqn    SimpleAggregateFunction(anyLast, String),
  data_tier     SimpleAggregateFunction(anyLast, Int16),
  first_seen_at SimpleAggregateFunction(min, DateTime64(3, 'UTC')),
  last_seen_at  SimpleAggregateFunction(max, DateTime64(3, 'UTC'))
) ENGINE = AggregatingMergeTree
ORDER BY (server_id, kind, fqn);
```

- **Write** is a plain batch append of raw values (`SimpleAggregateFunction` accepts scalar
  values on `INSERT`); `first_seen_at` and `last_seen_at` are both stamped server-side to the
  write time — `min`/`max` do the rest across merges. This mirrors the PG upsert, whose
  `first_seen_at`/`last_seen_at` are also `now()`-stamped server-side.
- **Read** (current state; only in tests today) uses `FINAL` (small data) or
  `GROUP BY (server_id, kind, fqn)` with `min(first_seen_at)`, `max(last_seen_at)`,
  `anyLast(size_bytes)`, etc.
- **No TTL**: like PG `schema_objects` (and the CH `check_mutes` precedent), this is a permanent
  current-state table, not a time series. `first_seen_at` must not be silently dropped.

**Rejected alternatives:** plain `MergeTree` + `min()` at read (unbounded row growth — one row
per object per snapshot); `ReplacingMergeTree(version)` (collapses volume but the surviving
max-version row loses the earliest `first_seen_at` unless the writer reads-before-write).
`AggregatingMergeTree` is the minimum correct design, not gold-plating.

### 4.3 `chStats` methods

```go
func (s *chStats) WriteSchemaObjects(ctx context.Context, rows []SchemaObjectRow) error {
    // batch INSERT INTO schema_objects (server_id, kind, fqn, schema, name,
    //   size_bytes, is_partition, parent_fqn, data_tier, first_seen_at, last_seen_at)
    // now := time.Now().UTC(); first_seen_at = last_seen_at = now; data_tier = 1;
    // is_partition -> UInt8(0/1). Same PrepareBatch/Append/Send shape as the other writers.
}

func (s *chStats) ParkDLQ(ctx context.Context, serverID, reason string, raw []byte) error {
    // batch INSERT INTO dlq (received_at, server_id, reason, raw); received_at = now.
}
```

---

## 5. `ingest.Server`

- `NewServer(cfg Config, stats store.Stats)` — **drop the `pool *pgxpool.Pool` param** and the
  `pool` / `schemaObjects` fields. `pgxpool` import removed if now unused.
- `parkDLQ(ctx, serverID, reason, raw)` → `s.stats.ParkDLQ(...)`, keeping the current
  log-and-swallow behavior (a DLQ failure must not crash ingest, and `context.Canceled` is not
  logged):

  ```go
  if err := s.stats.ParkDLQ(ctx, serverID, reason, raw); err != nil && !errors.Is(err, context.Canceled) {
      log.Printf("ingest: dlq insert failed: %v", err)
  }
  ```
- `persistExtendedRows`: `s.schemaObjects.UpsertSchemaObjects(ctx, objs)` →
  `s.stats.WriteSchemaObjects(ctx, objs)`.

No other handler logic changes.

---

## 6. `cmd/ingestion/main.go`

- **Remove** the direct stats-pool path: the `LYNCEUS_STATS_DSN` requirement + its
  `secure.CheckDatabaseDSN`, `pgxpool.New(ctx, dsn)`, and `ApplyStatsMigrations(pool)`.
  `store.OpenStats` owns DSN validation + migrations per backend.
- `stats, err := store.OpenStats(ctx)` (backend selected by required `LYNCEUS_STATS_BACKEND`).
- Keep the config pool (`LYNCEUS_CONFIG_DSN`, still required) — used only by the checks
  scheduler's advisory lock. No `ApplyConfigMigrations` needed (ingestion writes no config-DB
  tables now).
- `srv := ingest.NewServer(ingest.Config{…}, stats)`.
- `scheduler := checks.NewScheduler(stats, configPool, …)` — shares the one `stats` handle
  (`NewScheduler` already takes `store.Stats`; `lockPool` = configPool). This removes the second
  `store.NewStats(pool)` construction.

Env after this change: `LYNCEUS_STATS_BACKEND` (required), backend DSN (`LYNCEUS_STATS_DSN` for
postgres or `LYNCEUS_CLICKHOUSE_DSN` for clickhouse), `LYNCEUS_CONFIG_DSN` (required).

---

## 7. Tests (TDD)

Integration only, real engines via testcontainers (`internal/testpg`, `internal/testch`). No mocks.

1. **Store unit tests** (write the failing test first per method):
   - `pgxStats.WriteSchemaObjects` / `ParkDLQ` land rows in PG (may reuse the existing
     `schema_objects_test.go` first-seen-stability assertions via the delegated upsert).
   - `chStats.WriteSchemaObjects` lands one aggregated row per key in CH; a second observation
     with a larger size updates `size_bytes` (anyLast) while `first_seen_at` (min) is unchanged —
     the stability guarantee, proven on CH.
   - `chStats.ParkDLQ` lands a row in the CH `dlq` table with the raw bytes intact.
2. **`ingest/server_test.go`** — existing 7 PG tests: single Postgres container serves as both
   config and stats; only change is dropping the third `NewServer` arg
   (`NewServer(cfg, store.NewStats(pool))`). Assertions unchanged (dlq + schema_objects stay in
   the same PG via stats migrations).
3. **New `TestIngest_clickhouseBackend_e2e`** — the acceptance criterion. It wires
   `ingest.Server` directly (the established `server_test` pattern), not `main()`; the scheduler
   / config pool are not part of the ingest write path, so no `testpg` container is needed.
   - `testch.StartDSN` for CH; `t.Setenv("LYNCEUS_STATS_BACKEND", "clickhouse")`,
     `t.Setenv("LYNCEUS_CLICKHOUSE_DSN", dsn)`; `stats, _ := store.OpenStats(ctx)` (exercises the
     real backend selection + CH migrations — the "runs via `store.OpenStats`" criterion).
   - `NewServer(cfg, stats)`; send a `Snapshot` carrying `QueryStats` + `SchemaObjects`.
   - Assert: `query_stats` present in CH (`stats.TopQueriesByTotalTime` or direct query);
     `schema_objects` present in CH (direct `SELECT … FINAL`); `dlq` empty (happy path).
   - A rate-limited variant asserts the over-limit frame lands in the CH `dlq` table.

---

## 8. Acceptance criteria (from the bead)

- `cmd/ingestion` obtains its `Stats` via `store.OpenStats` and runs end-to-end with
  `LYNCEUS_STATS_BACKEND=clickhouse`: collector snapshots ingest into CH; DLQ + schema_objects
  no longer require a stats PG pool.
- Integration test against real ClickHouse (testcontainers) passes.
- No Postgres **stats** pool needed when `backend=clickhouse` (a config pool remains for the
  scheduler lock).

## 9. Invariants preserved

- No new literal-bearing field on any T1 proto message (no proto change at all).
- Authoritative audit log untouched — stays vanilla Postgres, hash-chained.
- No feature couples to a specific backend: DLQ + schema_objects now behind `store.Stats`.
- `schema_objects.first_seen_at` stability guarantee preserved on both backends.

## 10. Out of scope

- Direct-connect vs Firehose ingestion transport (ly-cwr program #5 — separate).
- Any DLQ **retry/reader** worker (none exists; not introduced here).
- Retiring the PG stats store (ly-cwr program #10).
- Normalization MV / RBAC split (ly-cwr.5) and isolated raw T2 table (ly-cwr.6).
