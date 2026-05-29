# Lynceus MVP — Vertical Slice Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prove the entire Lynceus pipeline end-to-end — collector reads normalized `pg_stat_statements`, ships over a websocket, ingestion_server rate-limits + persists to TimescaleDB, api_server serves a templ/HTMX dashboard of top queries — with the privacy contract (T1-only wire schema, audit + data-classification columns) present from day one.

**Architecture:** Single Go monorepo, three binaries (`cmd/collector`, `cmd/ingestion`, `cmd/api`) sharing `internal/` packages and a versioned protobuf wire contract in `proto/`. Two vanilla Postgres databases (config/metadata + stats) run via docker-compose for dev. Collector connects outbound only.

**Tech Stack:** Go 1.23+, Protocol Buffers (`protovalidate`/`protoc-gen-go`), `nhooyr.io/websocket` (or `gorilla/websocket`), **vanilla PostgreSQL 16** for both databases (the stats DB uses native declarative range partitioning so it runs on RDS/Aurora — **no extensions required**; TimescaleDB is an optional backend behind the `store.Stats` interface), `jackc/pgx/v5`, `a-h/templ` + HTMX, golang-migrate, testcontainers-go for integration tests.

**Spec:** `docs/specs/2026-05-29-lynceus-design.md`

---

## File Structure

```
lynceus/
  go.mod                              # module github.com/dobbo-ca/lynceus
  Makefile                            # build/test/proto/dev targets
  docker-compose.dev.yml              # postgres (config) + timescaledb (stats)
  proto/
    lynceus/v1/snapshot.proto         # T1 wire contract (query-stats snapshot)
  internal/
    proto/lynceusv1/                  # generated Go (do not edit)
    normalize/
      normalize.go                    # query normalization + classification
      normalize_test.go
    store/
      config.go                       # config/metadata DB access (audit, classification)
      stats.go                        # stats DB writes/reads (native-partitioned Postgres)
      partition.go                    # weekly partition create + retention (Go, no extensions)
      migrations/config/*.sql
      migrations/stats/*.sql
    ingest/
      server.go                       # websocket receiver, rate limit, DLQ, writer
      server_test.go
    collector/
      pgstatements.go                 # pg_stat_statements reader
      shipper.go                      # websocket client
  cmd/
    collector/main.go
    ingestion/main.go
    api/main.go
  internal/api/
    server.go                         # routes, dev-auth middleware
    queries.go                        # top-queries query API + handler
  web/
    queries.templ                     # top-queries dashboard
    layout.templ
  test/e2e/slice_test.go              # full pipeline test
```

---

## Task 1: Monorepo scaffold + dev harness

**Files:**
- Create: `go.mod`, `Makefile`, `docker-compose.dev.yml`, `cmd/collector/main.go`, `cmd/ingestion/main.go`, `cmd/api/main.go`

- [ ] **Step 1:** Initialize the module.

```bash
cd /Users/christopherdobbyn/work/dobbo-ca/lynceus
go mod init github.com/dobbo-ca/lynceus
```

- [ ] **Step 2:** Create stub `main.go` for each binary so the tree builds.

```go
// cmd/collector/main.go
package main

import "fmt"

func main() { fmt.Println("lynceus collector") }
```

(Repeat for `cmd/ingestion/main.go` → "lynceus ingestion" and `cmd/api/main.go` → "lynceus api".)

- [ ] **Step 3:** Create `docker-compose.dev.yml` with two services.

```yaml
services:
  config-db:
    image: postgres:16
    environment: { POSTGRES_PASSWORD: dev, POSTGRES_DB: lynceus_config }
    ports: ["5432:5432"]
  stats-db:
    image: postgres:16
    environment: { POSTGRES_PASSWORD: dev, POSTGRES_DB: lynceus_stats }
    command: ["postgres", "-c", "shared_preload_libraries=pg_stat_statements"]
    ports: ["5433:5432"]
```

- [ ] **Step 4:** Create `Makefile` with `build`, `test`, `proto`, `dev-up`, `dev-down` targets.

- [ ] **Step 5:** Verify the tree builds.

Run: `go build ./...`
Expected: builds with no errors (binaries print their names).

- [ ] **Step 6:** Commit.

```bash
git add go.mod Makefile docker-compose.dev.yml cmd/
git commit -m "feat: monorepo scaffold and dev harness"
```

---

## Task 2: T1 wire contract (proto) + privacy contract test

**Files:**
- Create: `proto/lynceus/v1/snapshot.proto`, `internal/proto/lynceusv1/` (generated), `internal/proto/contract_test.go`

- [ ] **Step 1:** Write the privacy contract test FIRST (it asserts the schema only carries normalized data).

```go
// internal/proto/contract_test.go
package lynceusv1_test

// Asserts every field in QueryStat is a fingerprint/metric — no field
// named or typed to carry a literal value. Fails if someone adds e.g.
// `query_sample` or `raw_text` to the T1 message.
func TestQueryStatHasNoLiteralFields(t *testing.T) {
	allowed := map[string]bool{
		"fingerprint": true, "normalized_query": true, "calls": true,
		"total_time_ms": true, "mean_time_ms": true, "rows": true,
		"shared_blks_hit": true, "shared_blks_read": true,
	}
	msg := (&lynceusv1.QueryStat{}).ProtoReflect().Descriptor()
	for i := 0; i < msg.Fields().Len(); i++ {
		name := string(msg.Fields().Get(i).Name())
		if !allowed[name] {
			t.Fatalf("unexpected field %q in T1 QueryStat — possible literal leak", name)
		}
	}
}
```

- [ ] **Step 2:** Run it — expect a compile failure (no generated type yet).

Run: `go test ./internal/proto/...`
Expected: FAIL (package `lynceusv1` does not exist).

- [ ] **Step 3:** Define the proto. `normalized_query` holds literal-free text (`WHERE id = $1`); there is deliberately **no** field for a raw sample.

```proto
// proto/lynceus/v1/snapshot.proto
syntax = "proto3";
package lynceus.v1;
option go_package = "github.com/dobbo-ca/lynceus/internal/proto/lynceusv1";

message Snapshot {
  string server_id = 1;
  int64 collected_at_unix = 2;
  repeated QueryStat query_stats = 3;
}

message QueryStat {
  string fingerprint = 1;       // stable hash of the normalized query
  string normalized_query = 2;  // literals stripped, e.g. "SELECT * FROM t WHERE id = $1"
  int64 calls = 3;
  double total_time_ms = 4;
  double mean_time_ms = 5;
  int64 rows = 6;
  int64 shared_blks_hit = 7;
  int64 shared_blks_read = 8;
}
```

- [ ] **Step 4:** Generate Go and run the contract test.

Run: `make proto && go test ./internal/proto/...`
Expected: PASS.

- [ ] **Step 5:** Commit.

```bash
git add proto/ internal/proto/ Makefile
git commit -m "feat: T1 snapshot wire contract with privacy contract test"
```

---

## Task 3: Query normalization + classification

**Files:**
- Create: `internal/normalize/normalize.go`, `internal/normalize/normalize_test.go`

- [ ] **Step 1:** Write adversarial tests asserting literals never survive.

```go
// internal/normalize/normalize_test.go
package normalize

func TestNormalizeStripsLiterals(t *testing.T) {
	cases := []struct{ in, want string }{
		{"SELECT * FROM users WHERE email = 'a@b.com'", "SELECT * FROM users WHERE email = $1"},
		{"INSERT INTO t (a,b) VALUES (1, 'secret')", "INSERT INTO t (a, b) VALUES ($1, $2)"},
		{"SELECT 1 WHERE x IN (1,2,3)", "SELECT 1 WHERE x IN ($1, $2, $3)"},
	}
	for _, c := range cases {
		got, tier := Normalize(c.in)
		if got != c.want {
			t.Errorf("Normalize(%q) = %q, want %q", c.in, got, c.want)
		}
		if tier != TierNormalized {
			t.Errorf("expected TierNormalized for %q", c.in)
		}
		if strings.ContainsAny(got, "'@.") && !strings.Contains(got, "$") {
			t.Errorf("possible literal leak in %q", got)
		}
	}
}

func TestUnparseableIsBlocked(t *testing.T) {
	_, tier := Normalize("this is not sql ';DROP")
	if tier != TierBlocked {
		t.Errorf("unparseable input must be TierBlocked, got %v", tier)
	}
}
```

- [ ] **Step 2:** Run — expect FAIL (package not implemented).

Run: `go test ./internal/normalize/...`
Expected: FAIL.

- [ ] **Step 3:** Implement `Normalize` using a SQL parser (e.g. `pg_query_go`) to replace constants with positional placeholders; return `(normalized string, tier Tier)`. Define `Tier` enum: `TierNormalized`, `TierBlocked`. Anything the parser rejects → `TierBlocked` with empty output.

- [ ] **Step 4:** Run — expect PASS.

Run: `go test ./internal/normalize/...`
Expected: PASS.

- [ ] **Step 5:** Commit.

```bash
git add internal/normalize/
git commit -m "feat: query normalization with literal-stripping and classification tiers"
```

---

## Task 4: Database schemas + migrations

**Files:**
- Create: `internal/store/migrations/config/0001_init.sql`, `internal/store/migrations/stats/0001_init.sql`, `internal/store/config.go`, `internal/store/stats.go`, `internal/store/store_test.go`

- [ ] **Step 1:** Write an integration test (testcontainers) asserting the audit table and `data_tier` column exist and a query-stat row round-trips.

- [ ] **Step 2:** Run — expect FAIL.

- [ ] **Step 3:** Config DB migration — include the privacy/audit primitives from day one:

```sql
-- internal/store/migrations/config/0001_init.sql
CREATE TABLE servers (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  t2_enabled BOOLEAN NOT NULL DEFAULT false,   -- T2 capture off by default
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE audit_log (
  id BIGSERIAL PRIMARY KEY,
  actor TEXT NOT NULL,
  action TEXT NOT NULL,
  server_id TEXT,
  data_tier SMALLINT,        -- which classification tier was accessed
  detail JSONB,
  at TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

- [ ] **Step 4:** Stats DB migration — **native declarative range partitioning, no extensions** (runs on RDS/Aurora). `data_tier` column present even though MVP writes only tier 1:

```sql
-- internal/store/migrations/stats/0001_init.sql
CREATE TABLE query_stats (
  server_id TEXT NOT NULL,
  collected_at TIMESTAMPTZ NOT NULL,
  fingerprint TEXT NOT NULL,
  normalized_query TEXT NOT NULL,
  data_tier SMALLINT NOT NULL DEFAULT 1,
  calls BIGINT, total_time_ms DOUBLE PRECISION, mean_time_ms DOUBLE PRECISION,
  rows BIGINT, shared_blks_hit BIGINT, shared_blks_read BIGINT
) PARTITION BY RANGE (collected_at);
CREATE INDEX query_stats_brin_time ON query_stats USING brin (collected_at);
CREATE INDEX query_stats_srv_fp ON query_stats (server_id, fingerprint);
```

- [ ] **Step 5:** Implement `store.Config`, `store.Stats`, and `store.partition` (pgx pools, migration runner, `WriteQueryStats`, `TopQueriesByTotalTime`, `AppendAudit`). `store.Stats` is an interface; the default impl ensures the weekly partition for an incoming row's timestamp exists before insert (creating `query_stats_YYYY_WW` via `CREATE TABLE ... PARTITION OF`) and provides a retention call that drops partitions older than the configured window — all in Go, no Postgres extensions. (A TimescaleDB impl can satisfy the same interface later.)

- [ ] **Step 6:** Run — expect PASS. Commit.

```bash
git commit -am "feat: config + timescale stats schemas with audit and data-tier columns"
```

---

## Task 5: Collector — pg_stat_statements reader + websocket shipper

**Files:**
- Create: `internal/collector/pgstatements.go`, `internal/collector/shipper.go`, `internal/collector/collector_test.go`; Modify: `cmd/collector/main.go`

- [ ] **Step 1:** Write a test (testcontainers Postgres with `pg_stat_statements`) asserting the reader returns `[]QueryStat` whose `normalized_query` contains no literals (runs a query with a literal, reads it back normalized).

- [ ] **Step 2:** Run — expect FAIL.

- [ ] **Step 3:** Implement `ReadQueryStats(ctx, pool) ([]*lynceusv1.QueryStat, error)` — selects from `pg_stat_statements`, passes `query` through `normalize.Normalize`, drops `TierBlocked` rows, computes fingerprint.

- [ ] **Step 4:** Implement `Shipper` — dials the ingestion websocket with a dev token header, marshals a `Snapshot`, sends it. Wire `cmd/collector/main.go` to read on the full-snapshot cadence and ship.

- [ ] **Step 5:** Run — expect PASS. Commit.

```bash
git commit -am "feat: collector reads normalized pg_stat_statements and ships snapshots"
```

---

## Task 6: ingestion_server — receiver + rate limit + DLQ + writer

**Files:**
- Create: `internal/ingest/server.go`, `internal/ingest/server_test.go`; Modify: `cmd/ingestion/main.go`

- [ ] **Step 1:** Write tests: (a) a valid snapshot over the websocket lands in `query_stats`; (b) exceeding the rate limit parks the snapshot in the DLQ table rather than dropping it.

- [ ] **Step 2:** Run — expect FAIL.

- [ ] **Step 3:** Implement the websocket handler: validate the dev token, decode the `Snapshot`, apply a token-bucket rate limiter per server, write accepted snapshots via `store.Stats.WriteQueryStats`, and insert rejected/over-limit snapshots into a `dlq` table for retry.

- [ ] **Step 4:** Run — expect PASS. Commit.

```bash
git commit -am "feat: ingestion websocket receiver with rate limit and dead-letter queue"
```

---

## Task 7: api_server — dev auth + top-queries API

**Files:**
- Create: `internal/api/server.go`, `internal/api/queries.go`, `internal/api/queries_test.go`; Modify: `cmd/api/main.go`

- [ ] **Step 1:** Write a handler test: with `LYNCEUS_DEV_AUTH=true`, `GET /api/queries/top` returns the rows from `store.Stats.TopQueriesByTotalTime` as JSON; without it and no OIDC, returns 401.

- [ ] **Step 2:** Run — expect FAIL.

- [ ] **Step 3:** Implement dev-auth middleware (env-gated static admin) and the `/api/queries/top` handler. Leave OIDC/SCIM as a documented stub returning 501.

- [ ] **Step 4:** Run — expect PASS. Commit.

```bash
git commit -am "feat: api_server dev-auth mode and top-queries endpoint"
```

---

## Task 8: Frontend — templ + HTMX top-queries dashboard

**Files:**
- Create: `web/layout.templ`, `web/queries.templ`; Modify: `internal/api/server.go` (mount the page route)

- [ ] **Step 1:** Write a handler test asserting `GET /` renders HTML containing a `<table>` and at least one normalized query string from seeded data.

- [ ] **Step 2:** Run — expect FAIL.

- [ ] **Step 3:** Implement `templ` templates: a layout and a queries table sorted by total time, with an HTMX `hx-get` polling refresh on the table fragment. Mount at `/`.

- [ ] **Step 4:** Run `templ generate && go test ./internal/api/...` — expect PASS. Commit.

```bash
git commit -am "feat: SSR top-queries dashboard with templ and HTMX"
```

---

## Task 9: End-to-end vertical-slice test

**Files:**
- Create: `test/e2e/slice_test.go`

- [ ] **Step 1:** Write an e2e test that spins up (testcontainers) both databases, starts ingestion + api in-process, runs the collector against a Postgres that has executed a literal-bearing query, then asserts: (a) the dashboard shows the normalized query, (b) no literal value appears anywhere in the stored row or rendered HTML.

- [ ] **Step 2:** Run — iterate until PASS.

- [ ] **Step 3:** Add a CI workflow (`.github/workflows/ci.yml`) running `go test ./...`. Commit.

```bash
git commit -am "test: end-to-end vertical slice with privacy assertion + CI"
```

---

## Self-Review Notes
- **Spec coverage:** Privacy/T1 contract (Task 2 + 3 + the `data_tier`/audit columns in Task 4); collector→ingestion→stats→api→frontend pipeline (Tasks 5–8); audit + classification present from day one (Task 4); OIDC/SCIM/RBAC explicitly stubbed (Task 7), deferred per spec §7.
- **Deferred (later milestones, tracked as beads epics M2–M6):** `pg_stat_activity`/wait events, `auto_explain` plans, index/plan/vacuum advisors, log sources (file/S3/Azure), full OIDC/SCIM/RBAC + audited T2 access, Helm/HA hardening, alerting.
- **Library choices** (websocket lib, SQL parser, migration tool) are pinned in `go.mod` at Task 1 execution; swap-compatible.
