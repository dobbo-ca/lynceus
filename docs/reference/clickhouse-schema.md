# ClickHouse Schema Reference

**Living reference — regenerate when `internal/store/migrations/clickhouse/*.sql` changes.**

- **Source of truth:** `internal/store/migrations/clickhouse/*.sql`, applied in lexical order by
  `store.ApplyClickHouseMigrations` (`internal/store/chmigrate.go`). Each file is split on `;` and
  run statement-by-statement; applied versions are tracked in `schema_migrations`.
- **Database:** every table is defined **unqualified** and resolves against the connection's
  default database, which comes from `LYNCEUS_CLICKHOUSE_DSN`. In dev/CI that database is
  **`lynceus_stats`** (`docker-compose.dev.yml` → `CLICKHOUSE_DB=lynceus_stats`; `internal/testch`
  → `WithDatabase("lynceus_stats")`). Tables are shown here **db-qualified for clarity**
  (`lynceus_stats.<table>`); the DDL itself is **not** qualified — see "Database qualification"
  below.
- **Tier column:** `data_tier Int16` (T1 = 1, T2 = 2) rides on every telemetry table for parity
  with Postgres, even where only one tier is produced.
- **Read idiom:** current-state / latest-as-of reads use `argMax(...)` or `LIMIT 1 BY`, never
  in-place update (ClickHouse has no `UPDATE`/`DELETE`). Mutable state is modeled with a
  `ReplacingMergeTree` + tombstone (`check_mutes`) or an `AggregatingMergeTree` (`schema_objects`).

Type legend: `String` (UTF-8/bytes, binary-safe), `DateTime64(3,'UTC')` (ms precision, UTC),
`Int16/Int32/Int64`, `Float64`, `UInt8` (0/1 boolean), `Nullable(T)`, `SimpleAggregateFunction(fn, T)`.

---

## Database `lynceus_stats` — T1 telemetry (broadly readable)

### `lynceus_stats.query_stats` — per-fingerprint query statistics (T1)
```sql
CREATE TABLE lynceus_stats.query_stats (
  server_id         String,
  collected_at      DateTime64(3, 'UTC'),
  fingerprint       String,
  normalized_query  String,          -- normalized ($1) skeleton — literal-free (T1)
  data_tier         Int16   DEFAULT 1,
  calls             Int64,
  total_time_ms     Float64,
  mean_time_ms      Float64,
  `rows`            Int64,            -- backticked: collides with a CH contextual keyword
  shared_blks_hit   Int64,
  shared_blks_read  Int64
) ENGINE = MergeTree
PARTITION BY toYYYYMM(collected_at)
ORDER BY (server_id, fingerprint, collected_at)
TTL toDateTime(collected_at) + INTERVAL 90 DAY;
```

### `lynceus_stats.activity_buckets` — connection-state / wait histogram (T1)
```sql
CREATE TABLE lynceus_stats.activity_buckets (
  server_id        String,
  database_name    String,
  state            String,
  wait_event_type  String,
  wait_event       String,
  bucket_start     DateTime64(3, 'UTC'),
  bucket_seconds   Int32,
  sample_count     Int32,
  count_sum        Int64,
  count_max        Int64,
  data_tier        Int16 DEFAULT 1
) ENGINE = MergeTree
PARTITION BY toYYYYMM(bucket_start)
ORDER BY (server_id, bucket_start, state)
TTL toDateTime(bucket_start) + INTERVAL 90 DAY;
```

### `lynceus_stats.query_plans` — normalized EXPLAIN plans (T1)
```sql
CREATE TABLE lynceus_stats.query_plans (
  server_id             String,
  fingerprint           String,
  captured_at           DateTime64(3, 'UTC'),
  format_version        Int32,
  total_cost            Float64,
  actual_total_time_ms  Float64,
  plan_tree             String,       -- serialized normalized plan proto — literal-free (T1)
  data_tier             Int16 DEFAULT 1
) ENGINE = MergeTree
PARTITION BY toYYYYMM(captured_at)
ORDER BY (server_id, fingerprint, captured_at)
TTL toDateTime(captured_at) + INTERVAL 90 DAY;
```

### `lynceus_stats.insights` — detected anti-patterns, derived server-side (T1)
```sql
CREATE TABLE lynceus_stats.insights (
  server_id      String,
  captured_at    DateTime64(3, 'UTC'),
  kind           String,
  severity       String,
  fingerprint    String,
  relation       String,
  node_path      String,
  rows_returned  Int64,
  rows_scanned   Int64,
  selectivity    Float64,
  detail         String,
  data_tier      Int16 DEFAULT 1
) ENGINE = MergeTree
PARTITION BY toYYYYMM(captured_at)
ORDER BY (server_id, kind, captured_at)
TTL toDateTime(captured_at) + INTERVAL 90 DAY;
```

### `lynceus_stats.table_stats` — per-table size & tuple stats time series (T1)
```sql
CREATE TABLE lynceus_stats.table_stats (
  server_id            String,
  collected_at         DateTime64(3, 'UTC'),
  schema_name          String,
  object_name          String,
  fqn                  String,
  total_bytes          Int64,
  heap_bytes           Int64,
  toast_bytes          Int64,
  indexes_bytes        Int64,
  row_estimate         Int64,
  live_tuples          Int64,
  dead_tuples          Int64,
  n_mod_since_analyze  Int64,
  seq_scan             Int64,
  idx_scan             Int64,
  n_tup_ins            Int64,
  n_tup_upd            Int64,
  n_tup_del            Int64,
  n_tup_hot_upd        Int64,
  last_vacuum          Nullable(DateTime64(3, 'UTC')),
  last_autovacuum      Nullable(DateTime64(3, 'UTC')),
  last_analyze         Nullable(DateTime64(3, 'UTC')),
  last_autoanalyze     Nullable(DateTime64(3, 'UTC')),
  vacuum_count         Int64,
  autovacuum_count     Int64,
  data_tier            Int16 DEFAULT 1
) ENGINE = MergeTree
PARTITION BY toYYYYMM(collected_at)
ORDER BY (server_id, fqn, collected_at)
TTL toDateTime(collected_at) + INTERVAL 90 DAY;
```

### `lynceus_stats.index_stats` — per-index usage & validity (T1)
```sql
CREATE TABLE lynceus_stats.index_stats (
  server_id     String,
  collected_at  DateTime64(3, 'UTC'),
  schema_name   String,
  object_name   String,
  fqn           String,
  table_fqn     String,
  idx_scan      Int64,
  size_bytes    Int64,
  is_valid      UInt8,
  is_ready      UInt8,
  is_unique     UInt8,
  is_primary    UInt8,
  data_tier     Int16 DEFAULT 1
) ENGINE = MergeTree
PARTITION BY toYYYYMM(collected_at)
ORDER BY (server_id, fqn, collected_at)
TTL toDateTime(collected_at) + INTERVAL 90 DAY;
```

### `lynceus_stats.freeze_ages` — wraparound / freeze age time series (T1)
```sql
CREATE TABLE lynceus_stats.freeze_ages (
  server_id                  String,
  collected_at               DateTime64(3, 'UTC'),
  scope                      String,
  schema_name                String,
  object_name                String,
  fqn                        String,
  xid_age                    Int64,
  mxid_age                   Int64,
  autovacuum_freeze_max_age  Int64,
  data_tier                  Int16 DEFAULT 1
) ENGINE = MergeTree
PARTITION BY toYYYYMM(collected_at)
ORDER BY (server_id, fqn, collected_at)
TTL toDateTime(collected_at) + INTERVAL 90 DAY;
```

### `lynceus_stats.xmin_horizon` — oldest-xmin horizon time series (T1)
```sql
CREATE TABLE lynceus_stats.xmin_horizon (
  server_id        String,
  collected_at     DateTime64(3, 'UTC'),
  oldest_xmin_age  Int64,
  holder_kind      String,
  data_tier        Int16 DEFAULT 1
) ENGINE = MergeTree
PARTITION BY toYYYYMM(collected_at)
ORDER BY (server_id, collected_at)
TTL toDateTime(collected_at) + INTERVAL 90 DAY;
```

### `lynceus_stats.settings` — curated pg_settings GUCs (T1)
```sql
CREATE TABLE lynceus_stats.settings (
  server_id        String,
  collected_at     DateTime64(3, 'UTC'),
  name             String,
  value            String,          -- bounded config value, allowlisted at the collector
  unit             String,
  source           String,
  pending_restart  UInt8 DEFAULT 0,
  data_tier        Int16 DEFAULT 1
) ENGINE = MergeTree
PARTITION BY toYYYYMM(collected_at)
ORDER BY (server_id, name, collected_at)
TTL toDateTime(collected_at) + INTERVAL 90 DAY;
```

### `lynceus_stats.connection_samples` — per-PID connection samples (T1)
```sql
CREATE TABLE lynceus_stats.connection_samples (
  server_id        String,
  observed_at      DateTime64(3, 'UTC'),
  pid              Int64,
  state            String,
  active_seconds   Int64,
  xact_seconds     Int64,
  state_seconds    Int64,
  wait_event_type  String,
  data_tier        Int16 DEFAULT 1
) ENGINE = MergeTree
PARTITION BY toYYYYMM(observed_at)
ORDER BY (server_id, observed_at, pid)
TTL toDateTime(observed_at) + INTERVAL 90 DAY;
```

### `lynceus_stats.blocking_edges` — lock-wait edges (T1)
```sql
CREATE TABLE lynceus_stats.blocking_edges (
  server_id             String,
  observed_at           DateTime64(3, 'UTC'),
  blocked_pid           Int64,
  blocker_pid           Int64,
  blocked_wait_seconds  Int64,
  data_tier             Int16 DEFAULT 1
) ENGINE = MergeTree
PARTITION BY toYYYYMM(observed_at)
ORDER BY (server_id, observed_at, blocked_pid, blocker_pid)
TTL toDateTime(observed_at) + INTERVAL 90 DAY;
```

### `lynceus_stats.checks_results` — firing-check observations per tick (T1)
```sql
CREATE TABLE lynceus_stats.checks_results (
  server_id     String,
  evaluated_at  DateTime64(3, 'UTC'),
  check_id      String,
  category      String,
  severity      String,
  status        String,
  object        String,
  detail        String,
  muted         UInt8 DEFAULT 0,
  data_tier     Int16 DEFAULT 1
) ENGINE = MergeTree
PARTITION BY toYYYYMM(evaluated_at)
ORDER BY (server_id, check_id, object, evaluated_at)
TTL toDateTime(evaluated_at) + INTERVAL 90 DAY;
```

### `lynceus_stats.check_mutes` — operator mute suppressions (T1, mutable)
Append-only versions collapsed by `ReplacingMergeTree(updated_at)`; `deleted` is a tombstone
(`SetMute` → 0, `ClearMute` → 1). Readers take the latest version per key via `argMax(updated_at)`.
`updated_at` is `DateTime64(9)` so rapid same-key mutations still order deterministically.
**No TTL** — a mute set far in the future must never be silently dropped.
```sql
CREATE TABLE lynceus_stats.check_mutes (
  server_id    String,
  check_id     String,
  object       String,
  muted_until  DateTime64(3, 'UTC'),
  reason       String,
  deleted      UInt8 DEFAULT 0,
  updated_at   DateTime64(9, 'UTC')
) ENGINE = ReplacingMergeTree(updated_at)
PARTITION BY toYYYYMM(updated_at)
ORDER BY (server_id, check_id, object);
```

### `lynceus_stats.log_events` — classified Postgres log events (T1)
Every column is classification metadata only (fixed-vocabulary strings, catalog idents, a hashed
client IP, coarse counters) — never statement text, bind params, error detail, or the raw message
(enforced by the `LogEvent` privacy contract test).
```sql
CREATE TABLE lynceus_stats.log_events (
  server_id         String,
  event_type        String,
  severity          String,
  occurred_at       DateTime64(3, 'UTC'),
  logged_at         DateTime64(3, 'UTC'),
  pid               Int64,
  backend_type      String,
  database_name     String,
  user_name         String,
  application_name  String,
  client_addr_hash  String,          -- hashed, never the raw client IP
  sql_state         String,
  session_line_num  Int64,
  transaction_id    Int64,
  data_tier         Int16 DEFAULT 1
) ENGINE = MergeTree
PARTITION BY toYYYYMM(occurred_at)
ORDER BY (server_id, event_type, occurred_at)
TTL toDateTime(occurred_at) + INTERVAL 90 DAY;
```

---

## Database `lynceus_stats` — T2 (literal-bearing) — INTERIM location

> ⚠️ **Interim.** `query_stats_t2` currently lives in the **same database and under the same
> credential** as the T1 tables. The ADR §4.4 raw-isolation guardrail is **not yet enforced**.
> ly-cwr.6 moves it to an isolated database + dedicated gateway credential — see
> "T2 access control & isolation" below. Do **not** write production T2 literals to ClickHouse
> before ly-cwr.6 lands.

### `lynceus_stats.query_stats_t2` — literal-bearing query statistics (T2)
Same shape as `query_stats`, `data_tier` defaults to 2, **7-day TTL** (short literal-custody
window). Read **only** by `chStats.ReadQueryStatsTier2`, whose sole caller is the audited
`T2Reader` gateway.
```sql
CREATE TABLE lynceus_stats.query_stats_t2 (
  server_id         String,
  collected_at      DateTime64(3, 'UTC'),
  fingerprint       String,
  normalized_query  String,          -- MAY carry literals (T2)
  data_tier         Int16   DEFAULT 2,
  calls             Int64,
  total_time_ms     Float64,
  mean_time_ms      Float64,
  `rows`            Int64,
  shared_blks_hit   Int64,
  shared_blks_read  Int64
) ENGINE = MergeTree
PARTITION BY toYYYYMM(collected_at)
ORDER BY (server_id, collected_at)
TTL toDateTime(collected_at) + INTERVAL 7 DAY;
```

---

## Pending — added by ly-cwr.7 (ingestion on ClickHouse)

Not yet in `main`. Design: `docs/superpowers/specs/2026-07-14-ly-cwr7-ingestion-on-clickhouse-design.md`.

### `lynceus_stats.dlq` — dead-letter queue (operational, T1 payloads)
Parks ingest frames that failed to write (rate-limited / malformed / write error). Append-only,
TTL-bounded (no retry consumer exists today). `raw` holds a serialized `Snapshot` protobuf — T1,
literal-free by contract.
```sql
CREATE TABLE lynceus_stats.dlq (
  received_at  DateTime64(3, 'UTC') DEFAULT now64(3),
  server_id    String,               -- '' for pre-server_id failures (unmarshal errors)
  reason       String,
  raw          String                -- serialized protobuf; String is binary-safe
) ENGINE = MergeTree
PARTITION BY toYYYYMM(received_at)
ORDER BY (received_at, server_id)
TTL toDateTime(received_at) + INTERVAL 14 DAY;
```

### `lynceus_stats.schema_objects` — object inventory, current-state (T1)
Current-state inventory with a **stable `first_seen_at`** (load-bearing for the future Index
Advisor / schema-change insights). `AggregatingMergeTree` collapses to one row per
`(server_id, kind, fqn)` — bounding volume — while `min`/`max` preserve the earliest first-seen
and latest last-seen without a read-before-write. `anyLast` holds the latest scalar values.
Writer appends raw values (`SimpleAggregateFunction` accepts scalars on `INSERT`), stamping
`first_seen_at = last_seen_at = now()`. **No TTL** (permanent current-state, like `check_mutes`).
```sql
CREATE TABLE lynceus_stats.schema_objects (
  server_id      String,
  kind           Int16,
  fqn            String,
  schema         SimpleAggregateFunction(anyLast, String),
  name           SimpleAggregateFunction(anyLast, String),
  size_bytes     SimpleAggregateFunction(anyLast, Int64),
  is_partition   SimpleAggregateFunction(anyLast, UInt8),
  parent_fqn     SimpleAggregateFunction(anyLast, String),
  data_tier      SimpleAggregateFunction(anyLast, Int16),
  first_seen_at  SimpleAggregateFunction(min, DateTime64(3, 'UTC')),
  last_seen_at   SimpleAggregateFunction(max, DateTime64(3, 'UTC'))
) ENGINE = AggregatingMergeTree
ORDER BY (server_id, kind, fqn);
```
Read current state with `FINAL` or `GROUP BY (server_id, kind, fqn)` +
`min(first_seen_at)`, `max(last_seen_at)`, `anyLast(size_bytes)`, …

---

## Database qualification

The DDL uses **unqualified** table names on purpose: the database is chosen by the connection's
DSN (`lynceus_stats` in dev/CI). Hardcoding `lynceus_stats.<table>` into the T1 migrations/queries
would couple the schema to one database name and buy **no isolation** — every T1 consumer already
shares that database. So T1 tables stay unqualified.

Qualification becomes meaningful only for **T2/raw isolation** (below), where a *second database*
plus a *dedicated credential* is the boundary — and even there, the enforced boundary is the
credential/role, not the name prefix.

## T2 access control & isolation (ly-cwr.5 / ly-cwr.6)

Layers, from enforced to defense-in-depth:

1. **Audited gateway is the enforcement point (unchanged).** Everything Lynceus serves reaches T2
   literals **only** through the Go `T2Reader` gateway: fast-reject on `servers.t2_enabled` →
   `EffectiveCapability` authorize → **audit append FIRST, fail-closed (Postgres hash-chain)** →
   the sole literal-returning SELECT (`ReadQueryStatsTier2`). ClickHouse RBAC **cannot audit a
   read** (no SELECT trigger), so CH controls are never the enforcement/audit point — only
   containment beneath the gateway.

2. **Isolated database + dedicated credential (ly-cwr.6).** Move `query_stats_t2` (and any future
   raw table) into a dedicated database — e.g. **`lynceus_raw`** — reachable **only** by a
   dedicated gateway CH role. Analysis / Bedrock roles are granted the T1 database only and
   **denied** the raw database. This is the strongest lever: the raw table is network/credential
   isolated, and the gateway connection is the only client that can reach it.

3. **Row-level & column-level security as defense-in-depth (colleague's suggestion — adopted).**
   ClickHouse OSS supports:
   - **Column grants:** `GRANT SELECT(col, …) ON <db>.<table> TO <role>` — grant a role the
     non-literal columns and **withhold the literal-bearing column** (`normalized_query`).
   - **Row policies:** `CREATE ROW POLICY … USING data_tier = 1 TO <analysis_role>` — a role sees
     only T1 rows even on a shared table.

   These are **enforced RBAC** (unlike a plain `VIEW`, which the ADR rejected as merely
   conventional), so they are a legitimate containment layer. Use them belt-and-suspenders under
   layers 1–2: even if a query reaches the store on the analysis role, column/row policy denies
   the literal. They **do not** replace the audited gateway (they cannot audit) and are weaker
   alone than a separate database + credential (a single misgrant re-exposes literals on a shared
   table). Recommended posture: **separate `lynceus_raw` DB + dedicated gateway role (primary
   boundary) + row/column policies on every non-gateway role (defense-in-depth)**, with
   `query_log` scrubbed/disabled on the raw path and the raw table TTL-bounded.

**Open item (ADR §7.3):** raw-table retention — short TTL (retrospective T2, bounded custody) vs a
`Null`/ultra-short engine (zero literals at rest, no retrospective T2). Decided in ly-cwr.6.
