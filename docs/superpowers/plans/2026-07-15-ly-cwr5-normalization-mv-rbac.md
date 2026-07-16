# ly-cwr.5 Normalization MV + ClickHouse RBAC — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** When a server has T2 enabled, the collector ships raw (literal-bearing) query text into ClickHouse `query_stats_t2`, and a ClickHouse materialized view derives the literal-free T1 `query_stats` rows by projection — the enforced T1/T2 boundary — while T2-disabled servers stay on today's edge-normalized direct path.

**Architecture:** Edge keeps pg_query as the authoritative fingerprint + normalized_query (parity with plans/insights). A new T2 wire message `QueryStatRaw` carries `raw_query` alongside those pg_query values, gated per server by a **fail-closed** `query_text_t2` capability (= `servers.t2_enabled` ∧ policy). Ingestion writes `query_stats_t2`; a ClickHouse `MATERIALIZED VIEW` projects the literal-free columns (raw_query excluded) into `query_stats` (T1). ly-cwr.6 RLS/TTL/scrub + the audited `T2Reader` gateway are unchanged; the T2 read retargets the new `raw_query` column.

**Tech Stack:** Go 1.26, protobuf (`protoc` 35.0 + `buf`, `make proto`), ClickHouse 25.8 (`clickhouse-go/v2`), Postgres (config/audit), testcontainers via `internal/testch` + `internal/testpg`, `pganalyze/pg_query_go/v6` (edge normalizer).

**Spec:** `docs/superpowers/specs/2026-07-15-ly-cwr5-normalization-mv-rbac-design.md` (read §3 data flow, §4 units, §5 privacy, §9 spike evidence).

## Global Constraints

- **Privacy is the backbone.** No new literal-bearing field on any **T1** proto message. `raw_query` lives ONLY on the new **T2** `QueryStatRaw`. The T1 `QueryStat`/`Snapshot`/`LogEvent`/… contract-test allowlists stay literal-free.
- **Authoritative audit stays vanilla Postgres** (hash-chain). `T2Reader` ordering (audit-first, fail-closed) is unchanged; keep the single `FROM query_stats_t2` choke point (guarded by `TestT2Read_OnlyOneTier2SelectInStoreSource`).
- **No backend coupling.** All store work is behind `store.Stats` / `store.Config`. ClickHouse is the sole stats backend (`LYNCEUS_STATS_BACKEND=clickhouse`).
- **Fail-closed for raw egress.** `query_text_t2` uses `AllowedStrict` (absent key → deny). Never `Allowed` (fail-open) for this capability.
- **Tests use shared containers** (`testch.Start`/`testch.StartDSN` + `testpg`, `testch.OpenAs`). Never per-test `tcpostgres.Run` / per-test CH boot.
- **Lint:** run `golangci-lint run <changed pkgs>` before finishing; add **no new** findings (the backlog `ly-69x.1` is pre-existing/non-required — do not fix it).
- **pg_query fingerprint parity:** the MV copies the edge `fingerprint` + `normalized_query` verbatim; it does NOT compute a CH fingerprint for T1. CH `normalizeQuery` is a test-time guardrail only.

## File Structure

| File | Responsibility | Task |
|---|---|---|
| `proto/lynceus/v1/snapshot.proto` | new `QueryStatRaw` message + `Snapshot.query_stat_raws = 15` | 1 |
| `internal/proto/lynceus/v1/snapshot.pb.go` | generated (`make proto`) | 1 |
| `internal/proto/lynceus/v1/contract_test.go` | widen Snapshot allowlist by `query_stat_raws`; new field-shape test | 1 |
| `internal/caps/caps.go` | `QueryTextT2` capability constant + `Declared()` | 2 |
| `internal/caps/gate.go` | `AllowedStrict` (fail-closed) | 2 |
| `internal/caps/gate_test.go` | `AllowedStrict` unit test | 2 |
| `internal/store/config.go` | add `ServerT2Enabled` to `Config` interface | 2 |
| `internal/store/fleet.go` | `pgxConfig.ServerT2Enabled` impl | 2 |
| `internal/api/capabilities.go` | emit explicit `query_text_t2` entry in `handlePolicySnapshot` | 2 |
| `internal/api/capabilities_test.go` | policy-snapshot gate test (4 combinations) | 2 |
| `internal/store/stats.go` | `QueryStat.RawQuery` field | 3 |
| `internal/store/migrations/clickhouse/0013_query_stats_raw_mv.sql` | `raw_query` column + MV | 3 |
| `internal/store/chstats.go` | split T1/T2 insert column lists; `ReadQueryStatsTier2` returns `raw_query` | 3 |
| `internal/store/chstats_mv_test.go` | MV literal-free/parity/guardrail + RLS×MV insert-identity | 3 |
| `internal/collector/reader.go` | `Read` returns raws; branch on `AllowedStrict(QueryTextT2)` | 4 |
| `internal/collector/reader_test.go` | gate-on emits raws-only; gate-off/absent emits T1-only | 4 |
| `cmd/collector/main.go:165` | set `QueryStatRaws: raws` on the full Snapshot | 4 |
| `internal/ingest/server.go` | `snapshotToRawRows`; persist raws → `query_stats_t2` | 5 |
| `internal/ingest/server_test.go` | raws persisted to T2; no direct T1 for a raw-only snapshot | 5 |
| `docs/reference/clickhouse-schema.md`, `README.md` | doc updates | 6 |

**Task→bead:** 1=ly-cwr.5a, 2=ly-cwr.5b, 3=ly-cwr.5d, 4=ly-cwr.5c, 5=ly-cwr.5e, 6=ly-cwr.5f. **Execution order: 1 → 3 → 2 → 4 → 5 → 6** (3 before 4 because the collector/ingest tasks depend on the `QueryStat.RawQuery` field and the CH MV existing for green integration tests; 2 is independent and can run any time after 1).

---

### Task 1: Proto — `QueryStatRaw` T2 message + `Snapshot.query_stat_raws` (ly-cwr.5a)

**Files:**
- Modify: `proto/lynceus/v1/snapshot.proto`
- Regenerate: `internal/proto/lynceus/v1/snapshot.pb.go`
- Test: `internal/proto/lynceus/v1/contract_test.go`

**Interfaces:**
- Produces: `lynceusv1.QueryStatRaw{RawQuery, Fingerprint, NormalizedQuery string; Calls int64; TotalTimeMs, MeanTimeMs float64; Rows, SharedBlksHit, SharedBlksRead int64}` and `Snapshot.QueryStatRaws []*QueryStatRaw` (field 15).

- [ ] **Step 1: Write the failing contract test** — append to `internal/proto/lynceus/v1/contract_test.go`:

```go
// TestSnapshotCarriesQueryStatRaws verifies the opt-in T2 raw payload field
// exists on the Snapshot envelope as repeated QueryStatRaw at field 15.
func TestSnapshotCarriesQueryStatRaws(t *testing.T) {
	fields := (&lynceusv1.Snapshot{}).ProtoReflect().Descriptor().Fields()
	f := fields.ByName("query_stat_raws")
	if f == nil {
		t.Fatal("query_stat_raws field missing from Snapshot")
	}
	if f.Number() != 15 {
		t.Fatalf("query_stat_raws field number = %d, want 15", f.Number())
	}
	if !f.IsList() {
		t.Fatal("query_stat_raws must be a repeated field")
	}
	if got := f.Message(); got == nil || got.Name() != "QueryStatRaw" {
		t.Fatalf("query_stat_raws must be repeated QueryStatRaw, got %v", got)
	}
}

// TestQueryStatRawCarriesRawQuery documents that QueryStatRaw is the ONE T2
// message permitted a literal-bearing raw_query field, alongside the pg_query
// fingerprint + normalized_query (literal-free). It is shipped only when the
// query_text_t2 gate is on (servers.t2_enabled ∧ policy).
func TestQueryStatRawCarriesRawQuery(t *testing.T) {
	fields := (&lynceusv1.QueryStatRaw{}).ProtoReflect().Descriptor().Fields()
	if f := fields.ByName("raw_query"); f == nil || f.Kind().String() != "string" {
		t.Fatal("QueryStatRaw.raw_query must exist and be string kind")
	}
	for _, n := range []string{"fingerprint", "normalized_query"} {
		if fields.ByName(protoreflect.Name(n)) == nil {
			t.Fatalf("QueryStatRaw.%s missing", n)
		}
	}
}
```

Also update the `TestSnapshotCarriesLogEvents` allowlist map: add `"query_stat_raws": {},` with a comment `// ly-cwr.5: opt-in T2 raw payload (gated, literal-bearing) — deliberately allowed`.

- [ ] **Step 2: Run test — verify it fails to compile** (`QueryStatRaw` undefined):

Run: `go test ./internal/proto/lynceus/v1/ -run 'QueryStatRaw|SnapshotCarriesQueryStatRaws' 2>&1 | head`
Expected: build error `undefined: lynceusv1.QueryStatRaw` (or the allowlist test fails).

- [ ] **Step 3: Add the proto message + field** — in `proto/lynceus/v1/snapshot.proto`, add after the `QueryStat` message:

```proto
// QueryStatRaw is the T2 (literal-bearing) sibling of QueryStat, shipped ONLY
// when the query_text_t2 capability is enabled for the server (servers.t2_enabled
// ∧ capability policy). The literal lives ONLY in raw_query; fingerprint and
// normalized_query are the pg_query (literal-free) values. Ingestion writes this
// to ClickHouse query_stats_t2; a materialized view projects the literal-free
// columns (raw_query excluded) into the T1 query_stats table. Gated behind RBAC +
// audit — see docs/superpowers/specs/2026-07-15-ly-cwr5-normalization-mv-rbac-design.md.
message QueryStatRaw {
  string raw_query        = 1; // literal-bearing (T2): raw pg_stat_statements text
  string fingerprint      = 2; // pg_query fingerprint (literal-free)
  string normalized_query = 3; // pg_query normalized ($1) skeleton (literal-free)
  int64  calls            = 4;
  double total_time_ms    = 5;
  double mean_time_ms     = 6;
  int64  rows             = 7;
  int64  shared_blks_hit  = 8;
  int64  shared_blks_read = 9;
}
```

And in `message Snapshot`, after `settings = 14`:

```proto
  // query_stat_raws (ly-cwr.5): opt-in T2 raw query payload. Populated ONLY for a
  // T2-enabled server; empty otherwise. Literal-bearing — the one T2 field on the
  // envelope. See QueryStatRaw.
  repeated QueryStatRaw query_stat_raws = 15;
```

- [ ] **Step 4: Regenerate Go from proto**

Run: `make proto`
Expected: `internal/proto/lynceus/v1/snapshot.pb.go` regenerated; `go build ./internal/proto/...` clean.

- [ ] **Step 5: Run the contract tests — verify pass**

Run: `go test ./internal/proto/lynceus/v1/ 2>&1 | tail -5`
Expected: PASS (all contract tests, incl. the two new ones and the unchanged T1 `QueryStat` allowlist).

- [ ] **Step 6: Commit**

```bash
git add proto/lynceus/v1/snapshot.proto internal/proto/lynceus/v1/
git commit -m "feat(proto): QueryStatRaw T2 message + Snapshot.query_stat_raws (ly-cwr.5a)"
```

**Goal / verify:** `go test ./internal/proto/...` green; `QueryStat` T1 allowlist untouched; `QueryStatRaw` is the sole raw-bearing message; `make proto` reproducible.

---

### Task 3: Store — `raw_query` column, MV, `QueryStat.RawQuery`, T2 read retarget (ly-cwr.5d)

**Files:**
- Modify: `internal/store/stats.go` (add `RawQuery` to `QueryStat`)
- Create: `internal/store/migrations/clickhouse/0013_query_stats_raw_mv.sql`
- Modify: `internal/store/chstats.go` (T1/T2 insert column split; `ReadQueryStatsTier2` returns `raw_query`)
- Test: `internal/store/chstats_mv_test.go`

**Interfaces:**
- Consumes: `store.QueryStat` (gains `RawQuery string`).
- Produces: `query_stats_t2.raw_query` column; MV `mv_query_stats_t2_to_t1 TO query_stats`; `ReadQueryStatsTier2` returns rows whose `NormalizedQuery`… (unchanged fields) plus the literal in `RawQuery`.

- [ ] **Step 1: Add `RawQuery` to `QueryStat`** — in `internal/store/stats.go`, in `type QueryStat struct`, after `NormalizedQuery string`:

```go
	// RawQuery is the literal-bearing raw query text. Populated ONLY on T2 rows
	// (DataTier==2) written to query_stats_t2; empty on T1. The normalization MV
	// projects the literal-free columns to query_stats and EXCLUDES this one.
	RawQuery string
```

- [ ] **Step 2: Write the failing MV test** — create `internal/store/chstats_mv_test.go`:

```go
package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dobbo-ca/lynceus/internal/testch"
)

// TestMV_DerivesLiteralFreeT1FromRaw pins the ly-cwr.5 boundary: inserting a T2
// row (with a literal in raw_query and a pg_query $1 normalized_query) into
// query_stats_t2 auto-populates query_stats (T1) via the MV, literal-free, with
// the edge pg_query fingerprint + normalized_query preserved verbatim (parity),
// and raw_query excluded.
func TestMV_DerivesLiteralFreeT1FromRaw(t *testing.T) {
	ctx := context.Background()
	conn := testch.Start(t) // shared container; migrations applied incl. 0013
	s := NewCHStats(conn)

	when := time.Now().UTC().Truncate(time.Second)
	if err := s.WriteQueryStats(ctx, []QueryStat{{
		ServerID:        "s-mv",
		CollectedAt:     when,
		Fingerprint:     "fp-parity",
		NormalizedQuery: "SELECT * FROM users WHERE email=$1 AND age>$2",
		RawQuery:        "SELECT * FROM users WHERE email='SECRET-LITERAL@x.com' AND age>30",
		DataTier:        2,
		Calls:           5,
	}}); err != nil {
		t.Fatalf("write T2: %v", err)
	}

	// The MV must have projected a T1 row into query_stats.
	var (
		fp, norm string
		leak     uint64
	)
	if err := conn.QueryRow(ctx,
		`SELECT fingerprint, normalized_query FROM query_stats WHERE server_id='s-mv' LIMIT 1`).
		Scan(&fp, &norm); err != nil {
		t.Fatalf("read T1 from MV: %v", err)
	}
	if fp != "fp-parity" || norm != "SELECT * FROM users WHERE email=$1 AND age>$2" {
		t.Fatalf("parity lost: fp=%q norm=%q", fp, norm)
	}
	if strings.Contains(norm, "SECRET") {
		t.Fatalf("literal leaked into T1 normalized_query: %q", norm)
	}
	// Defense-in-depth: CH normalizeQuery leaves an already-$1 skeleton unchanged.
	if err := conn.QueryRow(ctx,
		`SELECT count() FROM query_stats WHERE server_id='s-mv'
		   AND normalizeQuery(normalized_query) != normalized_query`).Scan(&leak); err != nil {
		t.Fatalf("guardrail query: %v", err)
	}
	if leak != 0 {
		t.Fatalf("normalizeQuery guardrail: %d T1 rows carry a stray literal", leak)
	}
}
```

- [ ] **Step 3: Run test — verify it fails** (no `raw_query` column / no MV):

Run: `go test ./internal/store/ -run TestMV_DerivesLiteralFreeT1FromRaw 2>&1 | tail -20`
Expected: FAIL — insert error (`raw_query` unknown) or the T1 read returns no row.

- [ ] **Step 4: Add the migration** — create `internal/store/migrations/clickhouse/0013_query_stats_raw_mv.sql`:

```sql
ALTER TABLE query_stats_t2 ADD COLUMN IF NOT EXISTS raw_query String;

CREATE MATERIALIZED VIEW IF NOT EXISTS mv_query_stats_t2_to_t1 TO query_stats AS
SELECT server_id, collected_at, fingerprint, normalized_query, 1 AS data_tier,
       calls, total_time_ms, mean_time_ms, `rows`, shared_blks_hit, shared_blks_read
FROM query_stats_t2;
```

- [ ] **Step 5: Split the insert column lists in `chstats.go`.** Replace the shared `chQueryStatsCols` usage so the `query_stats_t2` insert includes `raw_query` and the `query_stats` insert does not. In `internal/store/chstats.go`:

```go
// chQueryStatsColsT1 is the column order for query_stats (T1). No raw_query.
const chQueryStatsColsT1 = "server_id, collected_at, fingerprint, normalized_query, data_tier, " +
	"calls, total_time_ms, mean_time_ms, `rows`, shared_blks_hit, shared_blks_read"

// chQueryStatsColsT2 is query_stats_t2 (T2): chQueryStatsColsT1 + raw_query last.
const chQueryStatsColsT2 = chQueryStatsColsT1 + ", raw_query"
```

Update `WriteQueryStats` to call a tier-aware insert (T1 uses `chQueryStatsColsT1`; T2 uses `chQueryStatsColsT2` and appends `r.RawQuery`):

```go
func (s *chStats) WriteQueryStats(ctx context.Context, rows []QueryStat) error {
	if len(rows) == 0 {
		return nil
	}
	var t1, t2 []QueryStat
	for i := range rows {
		r := rows[i]
		if r.DataTier == 0 {
			r.DataTier = 1
		}
		if r.DataTier == 2 {
			t2 = append(t2, r)
		} else {
			t1 = append(t1, r)
		}
	}
	if err := s.insertQueryStatsT1(ctx, t1); err != nil {
		return err
	}
	return s.insertQueryStatsT2(scrubbedCtx(ctx), t2)
}

func (s *chStats) insertQueryStatsT1(ctx context.Context, rows []QueryStat) error {
	if len(rows) == 0 {
		return nil
	}
	batch, err := s.conn.PrepareBatch(ctx, "INSERT INTO query_stats ("+chQueryStatsColsT1+")")
	if err != nil {
		return err
	}
	for i := range rows {
		r := &rows[i]
		if err := batch.Append(
			r.ServerID, r.CollectedAt, r.Fingerprint, r.NormalizedQuery, r.DataTier,
			r.Calls, r.TotalTimeMs, r.MeanTimeMs, r.Rows, r.SharedBlksHit, r.SharedBlksRead,
		); err != nil {
			_ = batch.Abort()
			return err
		}
	}
	return batch.Send()
}

func (s *chStats) insertQueryStatsT2(ctx context.Context, rows []QueryStat) error {
	if len(rows) == 0 {
		return nil
	}
	batch, err := s.conn.PrepareBatch(ctx, "INSERT INTO query_stats_t2 ("+chQueryStatsColsT2+")")
	if err != nil {
		return err
	}
	for i := range rows {
		r := &rows[i]
		if err := batch.Append(
			r.ServerID, r.CollectedAt, r.Fingerprint, r.NormalizedQuery, r.DataTier,
			r.Calls, r.TotalTimeMs, r.MeanTimeMs, r.Rows, r.SharedBlksHit, r.SharedBlksRead,
			r.RawQuery,
		); err != nil {
			_ = batch.Abort()
			return err
		}
	}
	return batch.Send()
}
```

Remove the now-unused `chQueryStatsCols` and `insertQueryStats` if nothing else references them (grep first; `ReadQueryStatsTier2` uses `chQueryStatsCols` — update it in Step 6).

- [ ] **Step 6: Retarget `ReadQueryStatsTier2` to return `raw_query`.** The T2 read exists to return the literal to an authorized operator. In `internal/store/chstats.go`, change its SELECT to read `raw_query` into `QueryStat.RawQuery` (keep the single `FROM query_stats_t2`):

```go
func (s *chStats) ReadQueryStatsTier2(ctx context.Context, serverID string, since, until time.Time, limit int) ([]QueryStat, error) {
	rows, err := s.conn.Query(scrubbedCtx(ctx),
		`SELECT `+chQueryStatsColsT2+`
		   FROM query_stats_t2
		  WHERE server_id = ? AND collected_at >= ? AND collected_at < ?
		  ORDER BY collected_at DESC
		  LIMIT ?`,
		serverID, since, until, uint64(limit),
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []QueryStat
	for rows.Next() {
		var q QueryStat
		if err := rows.Scan(
			&q.ServerID, &q.CollectedAt, &q.Fingerprint, &q.NormalizedQuery, &q.DataTier,
			&q.Calls, &q.TotalTimeMs, &q.MeanTimeMs, &q.Rows, &q.SharedBlksHit, &q.SharedBlksRead,
			&q.RawQuery,
		); err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}
```

- [ ] **Step 7: Run the MV test + existing store tests — verify pass**

Run: `go test ./internal/store/ -run 'TestMV_|TestT2Read|ReadQueryStatsTier2' 2>&1 | tail -20`
Expected: PASS. Confirm `TestT2Read_OnlyOneTier2SelectInStoreSource` still green (single `FROM query_stats_t2`).

- [ ] **Step 8: Add the RLS×MV insert-identity test** — append to `internal/store/chstats_mv_test.go`:

```go
// TestMV_RLSInsertIdentity re-pins the spiked CH behaviour: a row policy on the
// MV source (query_stats_t2, ly-cwr.6) filters the MV transform by the INSERTING
// identity. An insert BY the runtime USER (matching the policy) populates the T1
// target via the MV. See spec §9.
func TestMV_RLSInsertIdentity(t *testing.T) {
	ctx := context.Background()
	admin := testch.Start(t)
	// Provision the ly-cwr.6 row policy + a runtime USER, then open AS that user.
	const user, pass = "lync_mv_user", "pw"
	if err := ProvisionCHSecurity(ctx, admin, ProvisionOpts{
		UserName: user, UserPassword: pass, DB: testch.DBName, T2TTLDays: 7,
	}); err != nil {
		t.Fatalf("provision: %v", err)
	}
	userConn := testch.OpenAs(t, user, pass)
	us := NewCHStats(userConn)
	if err := us.WriteQueryStats(ctx, []QueryStat{{
		ServerID: "s-rls", CollectedAt: time.Now().UTC(), Fingerprint: "fp",
		NormalizedQuery: "SELECT $1", RawQuery: "SELECT 'x'", DataTier: 2, Calls: 1,
	}}); err != nil {
		t.Fatalf("USER write T2: %v", err)
	}
	var n uint64
	if err := userConn.QueryRow(ctx,
		`SELECT count() FROM query_stats WHERE server_id='s-rls'`).Scan(&n); err != nil {
		t.Fatalf("read T1: %v", err)
	}
	if n == 0 {
		t.Fatal("MV did not populate T1 for a USER (policy-matching) insert")
	}
}
```

Adjust `testch.DBName` / `testch.OpenAs` to the actual helper names (see `internal/testch/testch.go`; the ly-cwr.6 tests in `chsecurity_testch_test.go` show the exact signatures — mirror them).

- [ ] **Step 9: Run — verify pass**

Run: `go test ./internal/store/ -run TestMV_RLSInsertIdentity 2>&1 | tail -20`
Expected: PASS.

- [ ] **Step 10: Commit**

```bash
git add internal/store/stats.go internal/store/chstats.go \
        internal/store/migrations/clickhouse/0013_query_stats_raw_mv.sql \
        internal/store/chstats_mv_test.go
git commit -m "feat(store): raw_query column + normalization MV; T2 read retarget (ly-cwr.5d)"
```

**Goal / verify:** MV derives literal-free T1 from raw (parity-exact, 0 leak, guardrail passes); RLS×MV insert-identity pinned; `ReadQueryStatsTier2` returns the literal via `raw_query`; single-choke-point test green; `go test ./internal/store/...` green.

---

### Task 2: caps `QueryTextT2` + fail-closed gate + api policy-snapshot emission (ly-cwr.5b)

**Files:**
- Modify: `internal/caps/caps.go`, `internal/caps/gate.go`, `internal/store/config.go`, `internal/store/fleet.go`, `internal/api/capabilities.go`
- Test: `internal/caps/gate_test.go`, `internal/api/capabilities_test.go`

**Interfaces:**
- Produces: `caps.QueryTextT2 Capability = "query_text_t2"`; `func (g *Gate) AllowedStrict(db string, c Capability) bool`; `store.Config.ServerT2Enabled(ctx, serverID) (bool, error)`; an explicit `query_text_t2` `policySnapshotEntry`.

- [ ] **Step 1: Write the failing gate test** — create/append `internal/caps/gate_test.go`:

```go
package caps

import "testing"

func TestAllowedStrict_FailClosed(t *testing.T) {
	g := NewGate()
	// empty gate: fail-open Allowed is true, fail-closed AllowedStrict is false.
	if !g.Allowed("db", QueryTextT2) {
		t.Fatal("Allowed should fail-open true on empty gate")
	}
	if g.AllowedStrict("db", QueryTextT2) {
		t.Fatal("AllowedStrict must fail-closed false on empty gate")
	}
	g.Replace(map[GateKey]bool{{Db: "db", Cap: QueryTextT2}: true})
	if !g.AllowedStrict("db", QueryTextT2) {
		t.Fatal("AllowedStrict must be true on explicit true")
	}
	g.Replace(map[GateKey]bool{{Db: "db", Cap: QueryTextT2}: false})
	if g.AllowedStrict("db", QueryTextT2) {
		t.Fatal("AllowedStrict must be false on explicit false")
	}
}
```

- [ ] **Step 2: Run — verify fail** (`QueryTextT2`, `AllowedStrict` undefined):

Run: `go test ./internal/caps/ -run TestAllowedStrict_FailClosed 2>&1 | head`
Expected: build error.

- [ ] **Step 3: Add the capability + `AllowedStrict`.** In `internal/caps/caps.go`, add the constant near the others and to `Declared()`:

```go
	// QueryTextT2 gates raw (literal-bearing) query-text egress (ly-cwr.5). It is
	// fail-CLOSED (see Gate.AllowedStrict): raw ships only on an explicit enable
	// (= servers.t2_enabled ∧ capability policy).
	QueryTextT2 Capability = "query_text_t2"
```

In `internal/caps/gate.go`:

```go
// AllowedStrict is the fail-CLOSED counterpart to Allowed: an absent key returns
// false. Use it for privacy-sensitive capabilities (raw-literal egress) where
// "no policy yet" must mean DENY, not default-enabled.
func (g *Gate) AllowedStrict(db string, c Capability) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.enabled[GateKey{Db: db, Cap: c}]
}
```

- [ ] **Step 4: Run — verify pass**

Run: `go test ./internal/caps/ 2>&1 | tail -3`
Expected: PASS.

- [ ] **Step 5: Add `ServerT2Enabled` to the Config store.** In `internal/store/config.go` `Config` interface, add:

```go
	// ServerT2Enabled reports the servers.t2_enabled kill switch for a server.
	ServerT2Enabled(ctx context.Context, serverID string) (bool, error)
```

In `internal/store/fleet.go` (match the `servers` table used by `ResolveServer`):

```go
func (c *pgxConfig) ServerT2Enabled(ctx context.Context, serverID string) (bool, error) {
	var enabled bool
	if err := c.pool.QueryRow(ctx,
		`SELECT t2_enabled FROM servers WHERE id = $1`, serverID).Scan(&enabled); err != nil {
		return false, fmt.Errorf("server t2_enabled: %w", err)
	}
	return enabled, nil
}
```

If any test double implements `store.Config`, add the method there too (compile errors will point them out).

- [ ] **Step 6: Write the failing api gate test** — append to `internal/api/capabilities_test.go` (mirror the existing policy-snapshot test setup in that file for server + policy fixtures):

```go
// TestPolicySnapshot_QueryTextT2Gate asserts the explicit query_text_t2 entry is
// enabled iff servers.t2_enabled AND the capability policy allow it.
func TestPolicySnapshot_QueryTextT2Gate(t *testing.T) {
	cases := []struct{ t2Enabled, policy, want bool }{
		{true, true, true}, {true, false, false}, {false, true, false}, {false, false, false},
	}
	for _, tc := range cases {
		// arrange: create a server with t2_enabled=tc.t2Enabled and a
		// capability_policy row query_text_t2=tc.policy (server-wide). GET the
		// policy snapshot; find the query_text_t2 entry; assert Enabled==tc.want.
		// (Use the same harness as the existing TestPolicySnapshot* in this file.)
	}
}
```

Fill the arrange/act/assert using the existing test harness in `capabilities_test.go` (server/policy factories + the `handlePolicySnapshot` request helper already present there).

- [ ] **Step 7: Emit the explicit entry.** In `internal/api/capabilities.go` `handlePolicySnapshot`, after the loop that builds `out`:

```go
	// query_text_t2 (ly-cwr.5): explicit, so the collector's fail-closed
	// AllowedStrict always has a value. Enabled only when the per-server T2 kill
	// switch AND the capability policy both allow raw-text egress.
	t2Enabled, err := s.conf.ServerT2Enabled(r.Context(), serverID)
	if err != nil {
		http.Error(w, "server t2_enabled", http.StatusInternalServerError)
		return
	}
	polOK, _, _, err := s.conf.EffectiveCapability(r.Context(), serverID, "", string(caps.QueryTextT2))
	if err != nil {
		http.Error(w, "effective capability", http.StatusInternalServerError)
		return
	}
	out = append(out, policySnapshotEntry{
		Capability:   string(caps.QueryTextT2),
		DatabaseName: "",
		Enabled:      t2Enabled && polOK,
	})
```

- [ ] **Step 8: Run api + caps tests — verify pass**

Run: `go test ./internal/api/ ./internal/caps/ 2>&1 | tail -10`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/caps/ internal/store/config.go internal/store/fleet.go internal/api/capabilities.go internal/api/capabilities_test.go
git commit -m "feat(caps,api): fail-closed query_text_t2 gate = t2_enabled ∧ policy (ly-cwr.5b)"
```

**Goal / verify:** `AllowedStrict` fail-closed proven; policy snapshot emits `query_text_t2 = t2_enabled ∧ policy` across all four combinations; `go test ./internal/api/... ./internal/caps/... ./internal/store/...` green.

---

### Task 4: Collector — ship `QueryStatRaw` when strictly gated (ly-cwr.5c)

**Files:**
- Modify: `internal/collector/reader.go`, `cmd/collector/main.go`
- Test: `internal/collector/reader_test.go`

**Interfaces:**
- Consumes: `caps.QueryTextT2`, `Gate.AllowedStrict`, `lynceusv1.QueryStatRaw`.
- Produces: `Reader.Read(ctx) ([]*lynceusv1.QueryStat, []*lynceusv1.QueryStatRaw, error)`.

- [ ] **Step 1: Update the failing reader test** — in `internal/collector/reader_test.go`, adjust existing `Read` callers to the 3-value signature and add:

```go
func TestRead_T2Gate_ShipsRawNotT1(t *testing.T) {
	// gate: query_text_t2 explicitly ON for the reader's db.
	gate := caps.NewGate()
	gate.Replace(map[caps.GateKey]bool{
		{Db: testDB, Cap: caps.PgStatStatements}: true,
		{Db: testDB, Cap: caps.QueryTextT2}:      true,
	})
	r := NewReader(pool, gate, testDB) // pool seeded with one literal-bearing stat
	stats, raws, err := r.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 0 {
		t.Fatalf("T2 mode must not emit T1 QueryStat, got %d", len(stats))
	}
	if len(raws) == 0 {
		t.Fatal("T2 mode must emit QueryStatRaw")
	}
	if raws[0].RawQuery == "" || raws[0].Fingerprint == "" || raws[0].NormalizedQuery == "" {
		t.Fatal("raw must carry raw_query + pg_query fingerprint + normalized")
	}
}

func TestRead_NoGate_ShipsT1Only(t *testing.T) {
	gate := caps.NewGate() // empty: PgStatStatements fail-open true, QueryTextT2 fail-closed false
	gate.Replace(map[caps.GateKey]bool{{Db: testDB, Cap: caps.PgStatStatements}: true})
	r := NewReader(pool, gate, testDB)
	stats, raws, err := r.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(raws) != 0 {
		t.Fatalf("no gate must emit no raw, got %d", len(raws))
	}
	if len(stats) == 0 {
		t.Fatal("no gate must still emit T1")
	}
}
```

(Match the existing `reader_test.go` harness for `pool`, `ctx`, `testDB`, and how it seeds `pg_stat_statements`.)

- [ ] **Step 2: Run — verify fail** (signature mismatch / no raws):

Run: `go test ./internal/collector/ -run 'TestRead_' 2>&1 | tail -15`
Expected: build error (3-value `Read`) then logic FAIL.

- [ ] **Step 3: Implement the branch** in `internal/collector/reader.go`. Change the signature and the per-row emit:

```go
func (r *Reader) Read(ctx context.Context) ([]*lynceusv1.QueryStat, []*lynceusv1.QueryStatRaw, error) {
	if !r.gate.Allowed(r.db, caps.PgStatStatements) {
		return nil, nil, nil // capability disabled: build & ship nothing
	}
	shipRaw := r.gate.AllowedStrict(r.db, caps.QueryTextT2) // fail-closed
	rows, err := r.pool.Query(ctx, /* unchanged SELECT */)
	if err != nil {
		return nil, nil, fmt.Errorf("query pg_stat_statements: %w", err)
	}
	defer rows.Close()

	var out []*lynceusv1.QueryStat
	var raws []*lynceusv1.QueryStatRaw
	for rows.Next() {
		// scan raw, calls, … (unchanged)
		normText, tier := normalize.Normalize(raw)
		if tier != normalize.TierNormalized {
			continue
		}
		fp, err := normalize.Fingerprint(raw)
		if err != nil {
			continue
		}
		if shipRaw {
			raws = append(raws, &lynceusv1.QueryStatRaw{
				RawQuery:        raw,
				Fingerprint:     fp,
				NormalizedQuery: normText,
				Calls:           calls,
				TotalTimeMs:     totalTimeMs,
				MeanTimeMs:      meanTimeMs,
				Rows:            rowsOut,
				SharedBlksHit:   sharedBlksHit,
				SharedBlksRead:  sharedBlksRead,
			})
			continue
		}
		out = append(out, &lynceusv1.QueryStat{ /* unchanged T1 fields */ })
	}
	return out, raws, rows.Err()
}
```

- [ ] **Step 4: Update the caller** in `cmd/collector/main.go`. Change the `stats, err = reader.Read(ctx)` task closure to capture raws, and set the Snapshot field:

```go
	// task 0 closure:
	stats, raws, e = reader.Read(ctx)
	// ...
	snap := &lynceusv1.Snapshot{
		ServerId:        cfg.serverID,
		CollectedAtUnix: time.Now().Unix(),
		QueryStats:      stats,
		QueryStatRaws:   raws,
		// ... rest unchanged
	}
```

Declare `raws []*lynceusv1.QueryStatRaw` alongside `stats` where `stats` is declared. Update the "shipped …" log line to include `len(raws)`.

- [ ] **Step 5: Run collector tests — verify pass**

Run: `go test ./internal/collector/ 2>&1 | tail -10 && go build ./cmd/collector/`
Expected: PASS + clean build.

- [ ] **Step 6: Commit**

```bash
git add internal/collector/reader.go internal/collector/reader_test.go cmd/collector/main.go
git commit -m "feat(collector): ship QueryStatRaw when query_text_t2 strictly enabled (ly-cwr.5c)"
```

**Goal / verify:** gate-on → raws only (raw+fp+normalized), no T1; gate-off/absent → T1 only, no raw (fail-closed); `TierBlocked` dropped both modes; `go test ./internal/collector/...` green + `cmd/collector` builds.

---

### Task 5: Ingestion — route `query_stat_raws` → `query_stats_t2` (ly-cwr.5e)

**Files:**
- Modify: `internal/ingest/server.go`
- Test: `internal/ingest/server_test.go`

**Interfaces:**
- Consumes: `snap.QueryStatRaws`, `store.QueryStat{RawQuery, DataTier:2}`, `Stats.WriteQueryStats`.

- [ ] **Step 1: Write the failing ingest test** — append to `internal/ingest/server_test.go` (mirror the existing `persistSnapshot`/`snapshotToRows` test harness + fake/real Stats used there):

```go
// TestPersist_RawGoesToT2_NotT1 asserts a raw-only snapshot writes query_stats_t2
// (raw_query populated) and no direct query_stats row (the MV produces T1).
func TestPersist_RawGoesToT2_NotT1(t *testing.T) {
	// arrange: a Snapshot with QueryStatRaws=[{RawQuery, Fingerprint, NormalizedQuery, ...}]
	// and empty QueryStats. persistSnapshot with a real chStats (testch) or a Stats
	// spy that records WriteQueryStats rows.
	// assert: exactly one row with DataTier==2 and RawQuery!="" reached WriteQueryStats;
	// no DataTier==1 row.
}
```

- [ ] **Step 2: Run — verify fail** (raws ignored):

Run: `go test ./internal/ingest/ -run TestPersist_RawGoesToT2_NotT1 2>&1 | tail -15`
Expected: FAIL (no T2 row recorded).

- [ ] **Step 3: Add `snapshotToRawRows` + wire it** in `internal/ingest/server.go`. Mirror `snapshotToRows` (line ~209):

```go
// snapshotToRawRows maps the opt-in T2 raw payload to store rows (DataTier=2).
// Only present for T2-enabled servers; the ClickHouse MV derives T1 from these.
func snapshotToRawRows(snap *lynceusv1.Snapshot) []store.QueryStat {
	collectedAt := time.Unix(snap.CollectedAtUnix, 0).UTC()
	if collectedAt.IsZero() || snap.CollectedAtUnix == 0 {
		collectedAt = time.Now().UTC()
	}
	rows := make([]store.QueryStat, 0, len(snap.QueryStatRaws))
	for _, q := range snap.QueryStatRaws {
		rows = append(rows, store.QueryStat{
			ServerID:        snap.ServerId,
			CollectedAt:     collectedAt,
			Fingerprint:     q.Fingerprint,
			NormalizedQuery: q.NormalizedQuery,
			RawQuery:        q.RawQuery,
			DataTier:        2,
			Calls:           q.Calls,
			TotalTimeMs:     q.TotalTimeMs,
			MeanTimeMs:      q.MeanTimeMs,
			Rows:            q.Rows,
			SharedBlksHit:   q.SharedBlksHit,
			SharedBlksRead:  q.SharedBlksRead,
		})
	}
	return rows
}
```

In `persistSnapshot` (~line 118), after the existing T1 `WriteQueryStats(snapshotToRows(snap))` call, add:

```go
	if raws := snapshotToRawRows(snap); len(raws) > 0 {
		if err := s.stats.WriteQueryStats(ctx, raws); err != nil {
			return "write raw", err
		}
	}
```

(`WriteQueryStats` routes `DataTier==2` → `query_stats_t2`, scrubbed. A raw-only snapshot has empty `QueryStats`, so `snapshotToRows` yields nothing and no direct T1 write happens.)

- [ ] **Step 4: Run — verify pass**

Run: `go test ./internal/ingest/ 2>&1 | tail -10`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ingest/server.go internal/ingest/server_test.go
git commit -m "feat(ingest): route query_stat_raws -> query_stats_t2 (ly-cwr.5e)"
```

**Goal / verify:** raw-only snapshot → one T2 row (`RawQuery` set), zero direct T1; `go test ./internal/ingest/...` green.

---

### Task 6: Docs + full-suite verification (ly-cwr.5f)

**Files:**
- Modify: `docs/reference/clickhouse-schema.md`, `README.md`

- [ ] **Step 1: Update the schema reference.** In `docs/reference/clickhouse-schema.md`, add `raw_query String` to the `query_stats_t2` DDL block and add a short subsection documenting the MV `mv_query_stats_t2_to_t1 (query_stats_t2 → query_stats)`: T1 is MV-derived (literal-free projection, raw_query excluded) for T2-enabled servers; direct write remains for T2-disabled servers.

- [ ] **Step 2: Update README.** Document the `query_text_t2` capability + per-server `servers.t2_enabled` raw-egress gate (fail-closed), and that raw literals leave the edge only for T2-enabled servers.

- [ ] **Step 3: Full build + suite + lint.**

Run:
```bash
go build ./...
go test ./internal/proto/... ./internal/caps/... ./internal/api/... ./internal/store/... ./internal/collector/... ./internal/ingest/...
golangci-lint run ./internal/proto/... ./internal/caps/... ./internal/api/... ./internal/store/... ./internal/collector/... ./internal/ingest/... ./cmd/collector/...
```
Expected: build clean; tests PASS; lint adds **no new** findings vs the pre-existing backlog.

- [ ] **Step 4: Commit**

```bash
git add docs/reference/clickhouse-schema.md README.md
git commit -m "docs(ch): raw_query column + normalization MV; query_text_t2 gate (ly-cwr.5f)"
```

**Goal / verify:** `go build ./...` clean; all six package suites green; no new lint findings; docs match the shipped shape.

---

## Self-Review (author checklist — done)

- **Spec coverage:** §4.1→T1, §4.2→T2, §4.3→T4, §4.4→T5, §4.5→T3, §4.6→T6, §5 privacy pinned by T1/T2/T3 tests, §6 testing folded into each task, §7 deferrals honored (test-time guardrail; TierBlocked dropped). Covered.
- **Placeholders:** test bodies for the api/ingest/collector harness-dependent cases point at the exact existing harness to mirror (`capabilities_test.go`, `server_test.go`, `reader_test.go`, `chsecurity_testch_test.go`) rather than inventing fixture APIs — this is a deliberate "match the neighbor" instruction, not a TODO. All new production code is shown in full.
- **Type consistency:** `QueryStat.RawQuery`, `chQueryStatsColsT1/T2`, `Read(...) ([]*QueryStat, []*QueryStatRaw, error)`, `AllowedStrict`, `ServerT2Enabled`, `snapshotToRawRows` — names consistent across tasks.
- **Order:** 1 → 3 → 2 → 4 → 5 → 6 (3 before 4/5 so `RawQuery` + MV exist for green integration tests).
