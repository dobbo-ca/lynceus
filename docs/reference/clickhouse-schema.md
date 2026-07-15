# ClickHouse Schema Reference

**Living reference — regenerate when `internal/store/migrations/clickhouse/*.sql` changes.**

> **ClickHouse is the sole stats store (ly-cwr.7/8).** The Postgres stats backend
> (`pgxStats`, `migrations/stats/`, the `LYNCEUS_STATS_DSN` pool) has been removed;
> `LYNCEUS_STATS_BACKEND=clickhouse` is the only supported value. Postgres remains
> only for the config/metadata + authoritative audit DB and the dev monitored target.

- **Source of truth:** `internal/store/migrations/clickhouse/*.sql`, applied in lexical order by
  `store.ApplyClickHouseMigrations` (`internal/store/chmigrate.go`). Each file is split on `;` and
  run statement-by-statement; applied versions are tracked in `schema_migrations`.
- **Database:** every table is defined **unqualified** and resolves against the connection's
  default database, which comes from `LYNCEUS_CLICKHOUSE_USER_DSN`. In dev/CI that database is
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

## Database `lynceus_stats` — T2 (literal-bearing)

> **ly-cwr.6 has landed.** `query_stats_t2` lives in the same `lynceus_stats` database as the T1
> tables, but is isolated at the ClickHouse layer: a row policy makes the Lynceus runtime **USER**
> the only identity that can read its rows (`USING (currentUser() = '<user>') TO ALL`), the T2
> read/write path runs with `log_queries=0` so literals never reach the shared `system.query_log`,
> and the retention window is configurable (`LYNCEUS_CLICKHOUSE_T2_TTL_DAYS`, default 7). See
> "T2 access control & isolation" below.

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

## Database qualification

The DDL uses **unqualified** table names on purpose: the database is chosen by the connection's
DSN (`lynceus_stats` in dev/CI). Hardcoding `lynceus_stats.<table>` into the T1 migrations/queries
would couple the schema to one database name and buy **no isolation** — every T1 consumer already
shares that database. So T1 tables stay unqualified.

Qualification becomes meaningful only for **T2/raw isolation** (below), where a *second database*
plus a *dedicated credential* is the boundary — and even there, the enforced boundary is the
credential/role, not the name prefix.

## T2 access control & isolation (ly-cwr.6 — SHIPPED)

Lynceus is a **tenant** of a shared, org-operated ClickHouse; it does not own the store, so the
boundary is what a tenant controls, not a separate database.

1. **Audited gateway is the enforcement point (unchanged).** Everything Lynceus serves reaches T2
   literals only through the Go `T2Reader`: fast-reject on `servers.t2_enabled` →
   `EffectiveCapability` → audit append FIRST, fail-closed (Postgres hash-chain) → the sole
   literal-returning SELECT (`ReadQueryStatsTier2`). ClickHouse cannot audit a read.

2. **Two Lynceus identities.** `LYNCEUS_CLICKHOUSE_ADMIN_DSN` runs DDL + one-time provisioning;
   `LYNCEUS_CLICKHOUSE_USER_DSN` runs all runtime reads/writes.

3. **Row-level security restricts `query_stats_t2` to the USER.**
   `CREATE ROW POLICY t2_lynceus_only ON query_stats_t2 USING (currentUser() = '<user>') TO ALL`.
   Because the policy applies to *all* users, only the Lynceus USER's rows pass; every other tenant
   (and the ADMIN identity) sees zero rows. **Verified on ClickHouse 25.8** — the naive
   `USING 1 TO <user>` does *not* deny users not named in it. T1 tables carry no policy → readable
   by all, per design.

4. **`query_log` scrub.** The T2 read/write (and the provisioning `CREATE USER`) run with
   `log_queries=0`, so T2 literals and the runtime password never reach the shared
   `system.query_log`.

5. **Configurable retention.** `LYNCEUS_CLICKHOUSE_T2_TTL_DAYS` (default 7) sets the `query_stats_t2`
   TTL via `ALTER TABLE … MODIFY TTL` at bootstrap.

**Accepted residual risk:** a leaked USER credential can read T2 directly (no separate gateway role).
Narrower than the prior single-shared-credential state; the audited `T2Reader` remains the
enforcement point for everything Lynceus serves.

**Optional future hardening (not built):** column-level security withholding `normalized_query`
from other identities (redundant while the row policy already yields zero rows). Org-owned RBAC for
Grafana / analysts / Bedrock is out of Lynceus's scope.
