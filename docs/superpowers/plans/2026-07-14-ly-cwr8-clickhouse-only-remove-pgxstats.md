# ClickHouse-only Stats Store — Remove pgxStats Implementation Plan (ly-cwr.7 + ly-cwr.8)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans. Steps use checkbox (`- [ ]`) syntax.

**Supersedes** the keep-pgxStats parts of `2026-07-14-ly-cwr7-ingestion-on-clickhouse.md`. Task 1 of that plan (ParkDLQ on the seam) is already committed (`bd59334`) and is reused here.

**Goal:** Make ClickHouse the *sole* stats store and delete the Postgres stats backend (`pgxStats`) entirely. Postgres remains only for config + the authoritative hash-chained `audit_log` (+ the dev monitored target).

**Architecture:** Finish moving the last two writes (DLQ done; schema_objects) onto `store.Stats`; point ingestion at `store.OpenStats`; migrate every stats integration test to ClickHouse (`internal/testch`); then delete `pgxStats`, `migrations/stats/`, `ApplyStatsMigrations`, the PG weekly-partition machinery, the stats read-replica split, and the `OpenStats` postgres branch. Keep all shared row-type structs, the `Stats` interface, the `chStats` impl, the `T2Reader` gateway, and the entire config/audit (PG) layer.

**Tech Stack:** Go, clickhouse-go v2, pgx v5 (config/audit only), testcontainers (`internal/testch`, `internal/testpg`).

## Global Constraints

- **Risk accepted (user directive 2026-07-14):** CH-only ships ahead of ly-cwr.6 (raw-T2 isolation). Until ly-cwr.6, `query_stats` + `query_stats_t2` share one CH db/credential. Do **not** wire any *new production T2-literal producer* in this work — the collector produces T1 only today; keep it that way here.
- Config + authoritative `audit_log` stay vanilla Postgres. The entire config/audit layer (`pgxConfig`, `ApplyConfigMigrations`, `migrations/config/`, audit/capability/fleet/saved_scripts/discovered_capability code **and tests**) is **untouched**.
- Integration tests use real engines via testcontainers — never mock. Stats tests → `internal/testch` + `store.NewCHStats`; config/audit tests stay on `internal/testpg`.
- Keep every shared row-type struct and the `Stats` interface. `var _ Stats = (*chStats)(nil)` is the only conformance assertion after removal.
- `go build ./...` and `go test ./...` (Docker up) must be green after each task. Commit per task. No push without approval. Do not modify beads/plan/spec from within implementation.
- Branch: `ch-ingestion-cwr7-809a`. Verify with `git branch --show-current` before every commit.

---

### Task 1 — ParkDLQ on the Stats seam — DONE (`bd59334`)

Already committed: `chStats.ParkDLQ` + `pgxStats.ParkDLQ` + `Stats.ParkDLQ` + `migrations/clickhouse/0011_dlq.sql` + tests. `pgxStats.ParkDLQ` (`internal/store/dlq.go`) is throwaway and is removed in Task 8. No action.

---

### Task 2 — WriteSchemaObjects on the Stats seam

**Files:**
- Verify/keep: `internal/store/migrations/clickhouse/0012_schema_objects.sql` (already present, untracked)
- Create: `internal/store/chstats_schema_objects.go`
- Create: `internal/store/chstats_schema_objects_test.go`
- Modify: `internal/store/schema_objects.go` (add throwaway `pgxStats.WriteSchemaObjects` delegation — removed in Task 8)
- Modify: `internal/store/stats.go` (add to `Stats` interface)

**Interfaces:**
- Consumes: `SchemaObjectRow` (in `schema_objects.go`); `chTableIndexBool(bool) uint8` (in `chstats_tableindex.go`).
- Produces: `WriteSchemaObjects(ctx context.Context, rows []SchemaObjectRow) error` on `store.Stats`.

- [ ] **Step 1: Confirm the CH migration matches spec.** Run `cat internal/store/migrations/clickhouse/0012_schema_objects.sql` and confirm it is the `AggregatingMergeTree` with `SimpleAggregateFunction(anyLast/min/max)` columns and `ORDER BY (server_id, kind, fqn)`. If missing/mismatched, write it from the spec (`docs/superpowers/specs/2026-07-14-ly-cwr7-ingestion-on-clickhouse-design.md` §4.2).

- [ ] **Step 2: Write the failing chStats test** — `internal/store/chstats_schema_objects_test.go` (identical to plan `2026-07-14-ly-cwr7-ingestion-on-clickhouse.md` Task 2 Step 2: `TestCH_schemaObjects_WriteAndFirstSeenStable` — write twice with growing size, assert one merged row via `FINAL`, `size_bytes==16384`, and `first_seen_at` unchanged).

- [ ] **Step 3: Run — verify fail (compile: `s.WriteSchemaObjects undefined`).** `go test ./internal/store/ -run TestCH_schemaObjects_WriteAndFirstSeenStable -v`

- [ ] **Step 4: Implement `chStats.WriteSchemaObjects`** — `internal/store/chstats_schema_objects.go` (identical to the ly-cwr.7 plan Task 2 Step 4: batch insert of `schemaObjectCHColumns`, `first_seen_at=last_seen_at=time.Now().UTC()`, `chTableIndexBool(r.IsPartition)`, `data_tier=int16(1)`).

- [ ] **Step 5: Run — verify pass.** `go test ./internal/store/ -run TestCH_schemaObjects_WriteAndFirstSeenStable -count=1 -v`

- [ ] **Step 6: Add throwaway `pgxStats.WriteSchemaObjects`** in `internal/store/schema_objects.go`:
```go
// WriteSchemaObjects satisfies store.Stats for the (soon-removed) Postgres
// backend by delegating to the existing upsert. Deleted in Task 8.
func (s *pgxStats) WriteSchemaObjects(ctx context.Context, rows []SchemaObjectRow) error {
	return NewSchemaObjects(s.pool).UpsertSchemaObjects(ctx, rows)
}
```

- [ ] **Step 7: Add to the `Stats` interface** in `internal/store/stats.go` (after `ParkDLQ`):
```go
	ParkDLQ(ctx context.Context, serverID, reason string, raw []byte) error
	WriteSchemaObjects(ctx context.Context, rows []SchemaObjectRow) error
```

- [ ] **Step 8: Build + store tests.** `go build ./... && go test ./internal/store/ -run 'SchemaObjects' -count=1 -v` → green.

- [ ] **Step 9: Commit.**
```bash
git add internal/store/migrations/clickhouse/0012_schema_objects.sql \
  internal/store/chstats_schema_objects.go internal/store/chstats_schema_objects_test.go \
  internal/store/schema_objects.go internal/store/stats.go
git commit -m "feat(store): WriteSchemaObjects on the Stats seam (CH AggregatingMergeTree)"
```

---

### Task 3 — ingest.Server drops the raw pool

Identical to the ly-cwr.7 plan Task 3: `NewServer(cfg Config, stats store.Stats)` (remove the `pool`/`schemaObjects` fields + the `*pgxpool.Pool` param), route `parkDLQ` → `s.stats.ParkDLQ` and schema_objects → `s.stats.WriteSchemaObjects`, drop the `pgxpool` import, and update `server_test.go`'s `setup` to the 2-arg `NewServer(cfg, store.NewStats(pool))` (server_test stays on PG here; it is ported to CH in Task 6).

- [ ] **Step 1–5:** Follow the ly-cwr.7 plan Task 3 steps verbatim.
- [ ] **Step 6: Build + ingest tests.** `go build ./... && go test ./internal/ingest/ -count=1 -v` → green.
- [ ] **Step 7: Commit** `refactor(ingest): route DLQ + schema_objects through store.Stats; drop the raw pool`.

---

### Task 4 — cmd/ingestion selects the backend via OpenStats

Identical to the ly-cwr.7 plan Task 4: replace the `LYNCEUS_STATS_DSN` block + `ApplyStatsMigrations(pool)` with `stats, err := store.OpenStats(ctx)`; keep `configPool` for the scheduler lock; `ingest.NewServer(cfg, stats)`; `checks.NewScheduler(stats, configPool, …)`.

- [ ] **Step 1–3:** Follow the ly-cwr.7 plan Task 4 steps verbatim. Verify `go build ./... && go vet ./cmd/ingestion/`.
- [ ] **Step 4: Commit** `feat(ingestion): select stats backend via store.OpenStats`.

---

### Task 5 — Store-package test migration to ClickHouse

Move the `internal/store` stats tests off `pgxStats` **while `pgxStats` still exists** (so each edit keeps compiling). After this task nothing in `internal/store/*_test.go` calls `ApplyStatsMigrations`/`NewStats` except the config/audit tests.

**Delete (coverage already exists in `chstats_*_test.go`):**
`table_stats_test.go`, `index_stats_test.go`, `connections_test.go`, `freeze_ages_test.go`, `xmin_horizon_test.go`, `settings_test.go`, `insights_test.go`, `plans_test.go`, `log_events_test.go`, `checks_results_test.go`, `schema_objects_test.go`, `dlq_test.go`, `stats_cache_test.go` (weekly-partition cache — obsolete under CH).

**Surgery (mixed config + stats files — keep the config/audit halves):**
- `store_test.go`: delete `TestApplyStatsMigrations_createsPartitionedQueryStats` and `TestWriteQueryStats_createsPartitionAndRoundtripsTopQueries`. **Keep** `TestApplyConfigMigrations_*`, `TestAuditAppend_roundtrips`, `TestAppendAudit_populatesChainColumns` (config/audit — PG stays).
- `pool_routing_test.go`: delete `TestStats_ReadsRouteToReplica`, `TestStats_NoReplica_ReadsFromPrimary`. **Keep** `TestConfig_ReadsRouteToReplica`, `TestConfig_NoReplica_ReadsFromPrimary` (config RO split stays).

**Port to CH (unique coverage — rewrite setup from `tcpostgres`+`ApplyStatsMigrations`+`NewStats(pool)` to `testch.Start`+`ApplyClickHouseMigrations`+`NewCHStats`, and rewrite raw-SQL assertions to CH):**
- `rollup_test.go` (`TestQueryReadsForServers_scopeAndAggregate`, `TestActivitySummaryForServers` — both read-aggregation methods present on `chStats`).
- `t2_read_test.go` (`TestT2Read_FailsClosed_whenAuditAppendFails` — the privacy-critical gateway; stats side → CH `query_stats_t2`, audit side stays config PG via `testpg`). **This one keeps its `testpg` config container for the audit DB and adds a `testch` container for the T2 read.**

- [ ] **Step 1: Delete the redundant + obsolete test files** (list above). `git rm` them.
- [ ] **Step 2: Build to confirm nothing else referenced them.** `go build ./... && go vet ./internal/store/`
- [ ] **Step 3: Surgery on `store_test.go` and `pool_routing_test.go`** — remove only the named stats tests; keep the config/audit tests. Confirm the files still reference `ApplyConfigMigrations`/config setup only.
- [ ] **Step 4: Port `rollup_test.go` to CH.** Replace the container setup with `conn := testch.Start(t); store.ApplyClickHouseMigrations(ctx, conn); s := store.NewCHStats(conn)`; write rows via the `Stats` write methods; keep the existing assertions on the returned aggregates (they are backend-agnostic — they call `s.QueryReadsForServers`/`s.ActivitySummaryForServers`).
- [ ] **Step 5: Port `t2_read_test.go` to CH.** Keep the `testpg` config/audit container; add `testch` for the stats store; construct the `T2Reader` with the CH stats + PG config exactly as today but with `NewCHStats(conn)` in place of `NewStats(pool)`; write the T2 row via `WriteQueryStats` with `DataTier: 2` (routes to `query_stats_t2`); assert audit-first-fail-closed unchanged.
- [ ] **Step 6: Run the store suite.** `go test ./internal/store/ -count=1` → green (all remaining tests run on CH or config-PG).
- [ ] **Step 7: Commit** `test(store): migrate stats tests to ClickHouse; drop redundant pgx store tests`.

---

### Task 6 — Higher-level test migration to ClickHouse

Port the integration tests that stand up a stats store to CH. Same setup swap as Task 5 (`testch` + `NewCHStats` in place of `tcpostgres` + `ApplyStatsMigrations` + `NewStats`). Where a test seeds data with raw PG `INSERT`/`pool.Exec`, replace with the typed `Stats` write methods (backend-agnostic) so no raw SQL remains.

**Files:** `internal/ingest/server_test.go` (all cases → CH; this folds in the ly-cwr.7 CH e2e cases), `internal/checks/scheduler_test.go`, `internal/api/{server_test,overview_test,cluster_views_test,scoped_overview_test,fleet_test,vertical_helpers_test}.go`, `internal/fleetview/{issues_test,summary_test}.go`, `test/e2e/{slice_test,log_slice_test}.go`.

> **Worked pattern** (apply to each): where the test has
> ```go
> c, _ := tcpostgres.Run(ctx, "postgres:16", tcpostgres.WithDatabase("lynceus_stats"), …, testpg.ReadyWait())
> pool, _ := pgxpool.New(ctx, url); store.ApplyStatsMigrations(ctx, pool)
> stats := store.NewStats(pool)
> ```
> replace with
> ```go
> conn := testch.Start(t); store.ApplyClickHouseMigrations(ctx, conn)
> stats := store.NewCHStats(conn)
> ```
> Keep any **config/audit** container as-is (still `testpg`). Seed via `stats.Write*`, and change post-write assertions that used `pool.QueryRow(...PG SQL...)` to the equivalent `conn.Query(...CH SQL...)` or a `stats.*` read.

- [ ] **Step 1: Port `internal/ingest/server_test.go`** — convert every case to CH; ensure the DLQ/schema_objects/query_stats assertions read from `conn` (CH). Run `go test ./internal/ingest/ -count=1 -v` → green.
- [ ] **Step 2: Commit** `test(ingest): run server tests on ClickHouse`.
- [ ] **Step 3: Port `internal/api/*_test.go` (6 files)** — swap stats setup to CH; keep config container (`testpg`) for RBAC/audit; seed via `stats.Write*`. Run `go test ./internal/api/ -count=1` → green.
- [ ] **Step 4: Commit** `test(api): run handler tests against ClickHouse stats`.
- [ ] **Step 5: Port `internal/fleetview/*_test.go` (2) + `internal/checks/scheduler_test.go`** — same swap. `go test ./internal/fleetview/ ./internal/checks/ -count=1` → green.
- [ ] **Step 6: Commit** `test(fleetview,checks): run against ClickHouse stats`.
- [ ] **Step 7: Port `test/e2e/*_test.go` (2)** — same swap. `go test ./test/e2e/ -count=1` → green.
- [ ] **Step 8: Commit** `test(e2e): run vertical slices on ClickHouse`.
- [ ] **Step 9: Whole-suite check.** `go build ./... && go test ./... -count=1` → green. Confirm no `_test.go` outside the config/audit set still references `ApplyStatsMigrations`/`NewStats`: `grep -rl "ApplyStatsMigrations\|store.NewStats" --include='*_test.go' .` should list only config/audit tests (audit/capability/fleet/saved_scripts/discovered_capability) and `store_test.go`/`pool_routing_test.go` (config halves).

---

### Task 7 — Delete pgxStats and the PG stats machinery

Now nothing references the PG stats backend except the interface conformance and the `OpenStats` postgres branch. Remove it. **Use the compiler as your guide: delete, `go build ./...`, fix the next dangling reference, repeat.**

**Delete outright (pgx stats domain files — chStats has its own equivalents):**
`internal/store/table_stats.go`, `index_stats.go`, `connections.go`, `freeze_ages.go`, `xmin_horizon.go`, `settings.go`, `insights.go`, `plans.go`, `checks_results.go`, `rollup.go`, `dlq.go`, `stats_cache.go`.

**Before deleting, relocate any *shared* type still needed** (row-type structs, column-name consts used by `chStats`, `PlanKey`, `QPSBucket`, `Throughput`, `ActivitySummary`, etc.). Move them into a new `internal/store/types.go`. Practical method: delete a file, run `go build ./...`, and for each `undefined: X` that is a **type/struct/const** (not a `pgxStats` method), move that definition into `types.go`.

**Surgery (keep the non-pgxStats parts):**
- `internal/store/stats.go`: keep the `Stats` interface + any shared types defined here; **delete** the `pgxStats` struct, `var _ Stats = (*pgxStats)(nil)`, `NewStats`, `(*pgxStats) Pool`, `(*pgxStats) WithReadPool`, and every `func (s *pgxStats) …` method (WriteQueryStats, TopQueriesByTotalTime, WriteActivityBuckets, WaitEventHistogram, EnsureWeeklyPartition, DropPartitionsOlderThan, EnsureActivityWeeklyPartition + the `partitionName`/`isoWeekBounds`/`parsePartitionUpper`/`activityPartitionName` helpers).
- `internal/store/schema_objects.go`: **delete** the `SchemaObjects` type, `NewSchemaObjects`, `UpsertSchemaObjects`, `ListByServer`, `FirstSeenAt`, and `pgxStats.WriteSchemaObjects`; **keep** the `SchemaObjectRow` / `SchemaObjectRecord` structs (move to `types.go` if the file is emptied).
- `internal/store/t2_read.go`: **keep** the `T2Reader` gateway (backend-agnostic); **delete** `(*pgxStats) ReadQueryStatsTier2` only.
- `internal/store/migrate.go`: **delete** `ApplyStatsMigrations` and the `//go:embed migrations/stats/*.sql` + `statsMigrations` var; **keep** `Migrate`, `ApplyConfigMigrations`, and the config embed.
- `internal/store/open.go`: **delete** the `case "postgres"` branch and the `LYNCEUS_STATS_DSN`/`LYNCEUS_STATS_RO_DSN` handling; leave `case "clickhouse"` and make the error text list `clickhouse` only. (Keep `LYNCEUS_STATS_BACKEND` required, value `clickhouse`.)

**Delete the migrations dir:** `git rm -r internal/store/migrations/stats/`.

- [ ] **Step 1:** `git rm` the pgx stats domain files above and `migrations/stats/`.
- [ ] **Step 2:** Create `internal/store/types.go` (package `store`) and iteratively `go build ./...`, moving each `undefined:` **type/const** into it until only `pgxStats`-method references remain.
- [ ] **Step 3:** Do the surgery on `stats.go`, `schema_objects.go`, `t2_read.go`, `migrate.go`, `open.go` per above.
- [ ] **Step 4:** `go build ./...` → green; `grep -rn "pgxStats\|ApplyStatsMigrations\|NewStats(" --include='*.go' internal/store cmd` returns nothing (config uses `pgxConfig`/`NewConfig`, not `NewStats`).
- [ ] **Step 5:** `go vet ./... && go test ./... -count=1` → green.
- [ ] **Step 6: Commit** `refactor(store): remove the Postgres stats backend (pgxStats); ClickHouse is the sole stats store`.

---

### Task 8 — Dev environment + docs

- [ ] **Step 1: docker-compose.dev.yml + Makefile** — remove the second Postgres (the `stats-db` on `:5433`) and any `LYNCEUS_STATS_DSN`/stats-DSN wiring; keep the config Postgres (`:5432`), ClickHouse (`:8123`), and the collector's monitored target. Update the `make dev-up` summary line accordingly.
- [ ] **Step 2: Bring the dev stack up and smoke-check.** `make dev-up`; run `cmd/ingestion` and `cmd/api` with `LYNCEUS_STATS_BACKEND=clickhouse` set and confirm they start without a stats-PG DSN. `make dev-down`.
- [ ] **Step 3: Docs.** Update `CLAUDE.md` (the "Two databases, both vanilla PostgreSQL" line → config/audit is PG, stats is ClickHouse), the design spec note, and `docs/reference/clickhouse-schema.md` (mark the PG stats backend removed; move `dlq`/`schema_objects` from "Pending" to live under `lynceus_stats`).
- [ ] **Step 4: Commit** `chore(dev,docs): drop the stats Postgres; document ClickHouse as the sole stats store`.

---

## Final validation

- [ ] `go build ./... && go vet ./... && go test ./... -count=1` all green (Docker up).
- [ ] `grep -rn "pgxStats\|ApplyStatsMigrations\|LYNCEUS_STATS_DSN\|migrations/stats" --include='*.go' .` → no hits.
- [ ] Config/audit tests still pass on Postgres; stats/api/fleetview/ingest/e2e tests pass on ClickHouse.
- [ ] `bd close ly-cwr.7 ly-cwr.8` (after review); update `docs/reference/clickhouse-schema.md` two-backend section.

## Deferred / follow-up

- **ly-cwr.6 (security):** T2 raw-isolation (dedicated `lynceus_raw` db + gateway-only creds + RLS/column-security). Still required before any **production** T2-literal producer. Not in this plan.
- **ly-cwr.5:** normalization MV + RBAC (T1/T2 boundary). Independent of this removal.
