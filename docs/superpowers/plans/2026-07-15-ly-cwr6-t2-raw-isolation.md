# ly-cwr.6 — T2 raw-isolation (two CH identities + RLS) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Isolate T2 (literal-bearing) `query_stats_t2` in the shared, org-operated ClickHouse so that only Lynceus's runtime USER identity can read its rows, its literals never reach the shared `system.query_log`, and its retention is a bounded, configurable window — without changing the audited `T2Reader` gateway.

**Architecture:** Two Lynceus CH identities — an ADMIN (DDL/provisioning) and a runtime USER (all reads/writes). A row policy `USING (currentUser() = '<user>') TO ALL` on `query_stats_t2` makes the USER the only identity that sees T2 rows; every other tenant on the shared CH (and ADMIN) sees zero. Per-statement `log_queries=0` scrubs the T2 read/write path; a configurable TTL (default 7d) bounds custody. The app self-bootstraps these with ADMIN at `OpenStats`.

**Tech Stack:** Go 1.26, ClickHouse 25.8 (`clickhouse-go/v2` v2.47.0), testcontainers-go v0.43 (`modules/clickhouse` v0.42), real Postgres via `internal/testpg`, real ClickHouse via `internal/testch`.

**Spec:** `docs/superpowers/specs/2026-07-15-ly-cwr6-t2-raw-isolation-design.md`

## Global Constraints

Every task's requirements implicitly include these:

- **Authoritative audit stays vanilla Postgres** (hash-chain). No audit logic moves to ClickHouse. `T2Reader` ordering (fast-reject → `EffectiveCapability` → `AppendAuditReturning` FIRST fail-closed → sole SELECT) is **unchanged**.
- **No new literal-bearing field on any T1 proto/table.** This work touches storage/RBAC only.
- **`store.Stats` is the seam.** Its interface signature does **not** change; `ReadQueryStatsTier2` and `WriteQueryStats` stay on it.
- **Exactly one `FROM query_stats_t2` in non-test store source**, living in `chstats.go` (enforced by `TestT2Read_OnlyOneTier2SelectInStoreSource`). New code must not add another `FROM query_stats_t2`.
- **Tests use the shared-container helpers only** — `testch.Start`/`testch.StartDSN`, `testpg`/`newPool`. Never reintroduce per-test `tcpostgres.Run` or per-test CH boots.
- **RLS construct is `USING (currentUser() = '<user>') TO ALL`** — verified empirically on CH 25.8 (2026-07-15); the naive `USING 1 TO <user>` does **not** isolate.
- **Provisioning statements are scrubbed** (`log_queries=0`) because `CREATE USER … IDENTIFIED BY '<pw>'` would otherwise write the runtime password to `system.query_log`.

---

## File Structure

- **Create** `internal/store/chsecurity.go` — `ProvisionCHSecurity` + `ProvisionOpts`. One responsibility: apply the T2 RLS policy, the runtime-user + grants, and the TTL, idempotently, as ADMIN.
- **Create** `internal/store/chsecurity_test.go` — isolation-proof + TTL tests (real CH).
- **Modify** `internal/testch/testch.go` — enable `access_management`; add `OpenAs` helper.
- **Create** `internal/store/chsecurity_testch_test.go` — access_management pin test. *(Kept in `store` package so it can use `testch.Start`; small.)*
- **Modify** `internal/store/chstats.go` — scrub `log_queries` on the T2 read + T2 write.
- **Modify** `internal/store/chstats_scrub_test.go` — *(create)* query_log-scrub behavioral test.
- **Modify** `internal/store/open.go` — env rename + `ADMIN_DSN` + two-conn `OpenStats` flow.
- **Modify** `internal/store/open_test.go` — update for `USER_DSN` + two-identity.
- **Modify** `docker-compose.dev.yml`, `README.md`, `docs/reference/clickhouse-schema.md` — dev enablement + docs.

---

## Task 1: testch — enable ClickHouse access_management

**Files:**
- Modify: `internal/testch/testch.go`
- Test: `internal/store/chsecurity_testch_test.go` (create)

**Interfaces:**
- Consumes: nothing.
- Produces: `testch.Start`/`StartDSN` now boot a container whose bootstrap user can `CREATE ROLE/USER/ROW POLICY` (needed by Tasks 3–4).

- [ ] **Step 1: Write the failing test**

Create `internal/store/chsecurity_testch_test.go`:

```go
package store_test

import (
	"context"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/testch"
)

// Pins that testch boots ClickHouse with access management enabled, so RBAC
// provisioning (CREATE USER / ROW POLICY) works in the store RBAC tests.
func TestCH_AccessManagementEnabled(t *testing.T) {
	ctx := context.Background()
	conn := testch.Start(t)
	if err := conn.Exec(ctx, "CREATE ROLE IF NOT EXISTS probe_role"); err != nil {
		t.Fatalf("access_management not enabled (CREATE ROLE failed): %v", err)
	}
	_ = conn.Exec(ctx, "DROP ROLE IF EXISTS probe_role")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestCH_AccessManagementEnabled -count=1 -v`
Expected: FAIL — `CREATE ROLE` errors with an access-denied / "Not enough privileges" message (default user lacks access management).

- [ ] **Step 3: Enable access_management in testch boot**

In `internal/testch/testch.go`, add the import and the env customizer. Change the import block to include:

```go
	"github.com/testcontainers/testcontainers-go"
```

Change the `tcclickhouse.Run(...)` call inside `boot()` to:

```go
	c, err := tcclickhouse.Run(ctx,
		"clickhouse/clickhouse-server:25.8",
		tcclickhouse.WithDatabase("lynceus_stats"),
		tcclickhouse.WithUsername("test"),
		tcclickhouse.WithPassword("test"),
		testcontainers.WithEnv(map[string]string{"CLICKHOUSE_DEFAULT_ACCESS_MANAGEMENT": "1"}),
	)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestCH_AccessManagementEnabled -count=1 -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/testch/testch.go internal/store/chsecurity_testch_test.go
git commit -m "test(testch): enable ClickHouse access_management for RBAC tests

ly-cwr.6: CLICKHOUSE_DEFAULT_ACCESS_MANAGEMENT=1 so the shared test
container's bootstrap user can CREATE USER / ROW POLICY."
```

---

## Task 2: chstats — scrub query_log on the T2 read + write path

**Files:**
- Modify: `internal/store/chstats.go`
- Test: `internal/store/chstats_scrub_test.go` (create)

**Interfaces:**
- Consumes: nothing.
- Produces: `ReadQueryStatsTier2` and the `query_stats_t2` insert run with `log_queries=0`, `log_query_threads=0`. Adds an unexported `scrubbedCtx(context.Context) context.Context` in package `store` (reused by Task 3).

- [ ] **Step 1: Write the failing test**

Create `internal/store/chstats_scrub_test.go`:

```go
package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/dobbo-ca/lynceus/internal/testch"
)

// A T2 read must not appear in system.query_log (log_queries=0 on the T2 path).
// Deterministic on the shared container because package tests run sequentially:
// a CH-sourced baseline windows out earlier tests, and an unscrubbed control
// query proves the window/filter actually detects a `FROM query_stats_t2` SELECT.
func TestCHStats_T2Read_ScrubsQueryLog(t *testing.T) {
	ctx := context.Background()
	conn := testch.Start(t)
	if err := store.ApplyClickHouseMigrations(ctx, conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := store.NewCHStats(conn)

	when := time.Now().UTC()
	if err := s.WriteQueryStats(ctx, []store.QueryStat{{
		ServerID: "srv-scrub", CollectedAt: when, Fingerprint: "fp",
		NormalizedQuery: "SELECT * FROM t WHERE ssn = '123-45-6789'", DataTier: 2, Calls: 1, TotalTimeMs: 1,
	}}); err != nil {
		t.Fatalf("seed t2: %v", err)
	}

	baseline := func() string {
		var s string
		if err := conn.QueryRow(ctx, "SELECT toString(now64(6))").Scan(&s); err != nil {
			t.Fatalf("baseline: %v", err)
		}
		return s
	}
	t2Selects := func(since string) uint64 {
		var n uint64
		if err := conn.QueryRow(ctx, `SELECT count() FROM system.query_log
			WHERE event_time_microseconds >= parseDateTime64BestEffort(?)
			  AND type = 'QueryFinish'
			  AND positionCaseInsensitive(query, 'from query_stats_t2') > 0`, since).Scan(&n); err != nil {
			t.Fatalf("query_log count: %v", err)
		}
		return n
	}

	base := baseline()
	if _, err := s.ReadQueryStatsTier2(ctx, "srv-scrub", when.Add(-time.Hour), when.Add(time.Hour), 10); err != nil {
		t.Fatalf("t2 read: %v", err)
	}
	if err := conn.Exec(ctx, "SYSTEM FLUSH LOGS"); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if got := t2Selects(base); got != 0 {
		t.Fatalf("scrubbed T2 read leaked into query_log: %d rows", got)
	}

	// Positive control: an UNSCRUBBED SELECT FROM query_stats_t2 must be detected.
	if err := conn.Exec(ctx, "SELECT count() FROM query_stats_t2 WHERE server_id = 'srv-scrub'"); err != nil {
		t.Fatalf("control select: %v", err)
	}
	if err := conn.Exec(ctx, "SYSTEM FLUSH LOGS"); err != nil {
		t.Fatalf("flush2: %v", err)
	}
	if got := t2Selects(base); got == 0 {
		t.Fatalf("control failed: query_log window/filter detected 0 SELECTs (test is blind)")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestCHStats_T2Read_ScrubsQueryLog -count=1 -v`
Expected: FAIL at the first `t2Selects(base) != 0` check — the T2 read is currently logged.

- [ ] **Step 3: Implement the scrub**

In `internal/store/chstats.go`, add the `clickhouse` import:

```go
import (
	"context"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)
```

Add the helper (place it just above `WriteQueryStats`):

```go
// scrubbedCtx suppresses ClickHouse query logging for statements on the T2
// (literal-bearing) path, so a T2 SELECT/INSERT — and, on the provisioning
// path, a CREATE USER … IDENTIFIED BY — never lands in the shared
// system.query_log. Structural to the query (literals are bind params or in
// row data, not the query text), but scrubbed regardless per the ADR §4.5.
func scrubbedCtx(ctx context.Context) context.Context {
	return clickhouse.Context(ctx, clickhouse.WithSettings(clickhouse.Settings{
		"log_queries":        0,
		"log_query_threads":  0,
	}))
}
```

In `WriteQueryStats`, scrub the T2 insert only. Change the final return:

```go
	if err := s.insertQueryStats(ctx, "query_stats", t1); err != nil {
		return err
	}
	return s.insertQueryStats(scrubbedCtx(ctx), "query_stats_t2", t2)
```

In `ReadQueryStatsTier2`, scrub the SELECT. Change the `s.conn.Query(ctx, …)` call to use `scrubbedCtx(ctx)`:

```go
	rows, err := s.conn.Query(scrubbedCtx(ctx),
		`SELECT `+chQueryStatsCols+`
		   FROM query_stats_t2
		  WHERE server_id = ? AND collected_at >= ? AND collected_at < ?
		  ORDER BY collected_at DESC
		  LIMIT ?`,
		serverID, since, until, uint64(limit),
	)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestCHStats_T2Read_ScrubsQueryLog -count=1 -v`
Expected: PASS.

- [ ] **Step 5: Run the existing chstats + t2 tests to confirm no regression**

Run: `go test ./internal/store/ -run 'TestCHStats|TestT2Read' -count=1`
Expected: PASS (including `TestT2Read_OnlyOneTier2SelectInStoreSource` — no new `FROM query_stats_t2` was added).

- [ ] **Step 6: Commit**

```bash
git add internal/store/chstats.go internal/store/chstats_scrub_test.go
git commit -m "feat(store): scrub query_log on the ClickHouse T2 path

ly-cwr.6: run the query_stats_t2 read + write with log_queries=0 so T2
literals never reach the shared system.query_log. Adds scrubbedCtx."
```

---

## Task 3: chsecurity — ProvisionCHSecurity (RLS + runtime user + grants + TTL)

**Files:**
- Create: `internal/store/chsecurity.go`
- Modify: `internal/testch/testch.go` (add `OpenAs`)
- Test: `internal/store/chsecurity_test.go` (create)

**Interfaces:**
- Consumes: `scrubbedCtx` (Task 2); testch access_management (Task 1).
- Produces:
  - `func ProvisionCHSecurity(ctx context.Context, admin driver.Conn, opts ProvisionOpts) error`
  - `type ProvisionOpts struct { UserName string; UserPassword string; DB string; T2TTLDays int }`
  - `func testch.OpenAs(t *testing.T, dsn, user, pass string) driver.Conn`
  - Consumed by Task 4 (`OpenStats`).

- [ ] **Step 1: Add the testch.OpenAs helper**

In `internal/testch/testch.go`, add (imports `clickhouse` and `driver` are already present):

```go
// OpenAs opens a second connection to the same shared server and database as
// `dsn` but authenticated as user/pass — for RBAC/isolation tests that read as
// a non-Lynceus identity. Closed via t.Cleanup.
func OpenAs(t *testing.T, dsn, user, pass string) driver.Conn {
	t.Helper()
	opts, err := clickhouse.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	o := *opts
	o.Auth.Username = user
	o.Auth.Password = pass
	conn, err := clickhouse.Open(&o)
	if err != nil {
		t.Fatalf("open as %s: %v", user, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}
```

- [ ] **Step 2: Write the failing isolation test**

Create `internal/store/chsecurity_test.go`:

```go
package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/dobbo-ca/lynceus/internal/testch"
)

const (
	chUser = "lynceus_user"
	chPass = "user-pw"
)

// provision migrates the isolated test DB and provisions T2 security, returning
// the admin conn, the isolated-DB dsn, and the db name.
func provision(t *testing.T, ttlDays int) (driver.Conn, string, string) {
	t.Helper()
	ctx := context.Background()
	admin, dsn := testch.StartDSN(t)
	if err := store.ApplyClickHouseMigrations(ctx, admin); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	db := dbFromDSN(t, dsn)
	if err := store.ProvisionCHSecurity(ctx, admin, store.ProvisionOpts{
		UserName: chUser, UserPassword: chPass, DB: db, T2TTLDays: ttlDays,
	}); err != nil {
		t.Fatalf("provision: %v", err)
	}
	return admin, dsn, db
}

func dbFromDSN(t *testing.T, dsn string) string {
	t.Helper()
	// testch DSN is clickhouse://user:pass@host:port/<db>
	for i := len(dsn) - 1; i >= 0; i-- {
		if dsn[i] == '/' {
			return dsn[i+1:]
		}
	}
	t.Fatalf("no db in dsn %q", dsn)
	return ""
}

// The RLS boundary: a non-Lynceus user with SELECT on the DB sees ZERO
// query_stats_t2 rows; the Lynceus USER sees them.
func TestCHSecurity_RowPolicy_DeniesNonUser(t *testing.T) {
	ctx := context.Background()
	admin, dsn, db := provision(t, 7)

	// Write a T2 row AS the Lynceus USER (the runtime identity + the only one
	// the row policy admits on read).
	userConn := testch.OpenAs(t, dsn, chUser, chPass)
	when := time.Now().UTC()
	if err := store.NewCHStats(userConn).WriteQueryStats(ctx, []store.QueryStat{{
		ServerID: "srv", CollectedAt: when, Fingerprint: "fp",
		NormalizedQuery: "SELECT * FROM t WHERE ssn='123-45-6789'", DataTier: 2, Calls: 1, TotalTimeMs: 1,
	}}); err != nil {
		t.Fatalf("user write t2: %v", err)
	}

	// A third-party CH user with SELECT on the DB.
	if err := admin.Exec(ctx, "CREATE USER third IDENTIFIED BY 'tp'"); err != nil {
		t.Fatalf("create third: %v", err)
	}
	if err := admin.Exec(ctx, "GRANT SELECT ON "+db+".* TO third"); err != nil {
		t.Fatalf("grant third: %v", err)
	}
	third := testch.OpenAs(t, dsn, "third", "tp")

	count := func(conn driver.Conn, tbl string) uint64 {
		var n uint64
		if err := conn.QueryRow(ctx, "SELECT count() FROM "+tbl).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", tbl, err)
		}
		return n
	}

	if got := count(userConn, "query_stats_t2"); got != 1 {
		t.Fatalf("lynceus USER query_stats_t2 count = %d, want 1", got)
	}
	if got := count(third, "query_stats_t2"); got != 0 {
		t.Fatalf("RLS breach: third sees %d query_stats_t2 rows, want 0", got)
	}
}

// T1 tables carry no policy → a non-Lynceus user reads them.
func TestCHSecurity_T1_ReadableByNonUser(t *testing.T) {
	ctx := context.Background()
	admin, dsn, db := provision(t, 7)

	userConn := testch.OpenAs(t, dsn, chUser, chPass)
	when := time.Now().UTC()
	if err := store.NewCHStats(userConn).WriteQueryStats(ctx, []store.QueryStat{{
		ServerID: "srv", CollectedAt: when, Fingerprint: "fp-t1",
		NormalizedQuery: "SELECT 1", DataTier: 1, Calls: 1, TotalTimeMs: 1,
	}}); err != nil {
		t.Fatalf("user write t1: %v", err)
	}
	if err := admin.Exec(ctx, "CREATE USER third2 IDENTIFIED BY 'tp'"); err != nil {
		t.Fatalf("create third2: %v", err)
	}
	if err := admin.Exec(ctx, "GRANT SELECT ON "+db+".* TO third2"); err != nil {
		t.Fatalf("grant third2: %v", err)
	}
	third := testch.OpenAs(t, dsn, "third2", "tp")

	var n uint64
	if err := third.QueryRow(ctx, "SELECT count() FROM query_stats").Scan(&n); err != nil {
		t.Fatalf("third read query_stats: %v", err)
	}
	if n != 1 {
		t.Fatalf("third query_stats count = %d, want 1 (T1 must be open)", n)
	}
}

// TTL is configurable: provisioning with T2TTLDays=3 sets the table TTL to 3 days.
func TestCHSecurity_TTLConfigurable(t *testing.T) {
	ctx := context.Background()
	admin, _, _ := provision(t, 3)

	var createSQL string
	if err := admin.QueryRow(ctx, "SHOW CREATE TABLE query_stats_t2").Scan(&createSQL); err != nil {
		t.Fatalf("show create: %v", err)
	}
	if !containsAll(createSQL, "TTL", "INTERVAL 3 DAY") {
		t.Fatalf("query_stats_t2 TTL not 3 days:\n%s", createSQL)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		found := false
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/store/ -run TestCHSecurity -count=1 -v`
Expected: FAIL — `store.ProvisionCHSecurity` / `store.ProvisionOpts` undefined (compile error).

- [ ] **Step 4: Implement ProvisionCHSecurity**

Create `internal/store/chsecurity.go`:

```go
package store

import (
	"context"
	"fmt"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// ProvisionOpts configures T2 security provisioning. UserName/UserPassword are
// the Lynceus runtime identity (parsed from LYNCEUS_CLICKHOUSE_USER_DSN); DB is
// the stats database; T2TTLDays is the query_stats_t2 retention window.
type ProvisionOpts struct {
	UserName     string
	UserPassword string
	DB           string
	T2TTLDays    int
}

// ProvisionCHSecurity applies, idempotently and as the ADMIN identity, the
// ClickHouse-layer T2 isolation for ly-cwr.6:
//
//  1. bounds query_stats_t2 retention to opts.T2TTLDays,
//  2. ensures the runtime USER exists (dev/test; a prod org may pre-create it),
//  3. grants the USER SELECT+INSERT on the stats DB, and
//  4. installs the row policy that makes the USER the ONLY identity that can
//     read query_stats_t2 rows (USING currentUser()='<user>' TO ALL — verified
//     against CH 25.8; a naive `TO <user>` does NOT deny other users).
//
// Statements run scrubbed (log_queries=0) so the CREATE USER password never
// reaches system.query_log. It is a hard error here; OpenStats decides whether
// to tolerate a failure (a locked-down prod ADMIN) by logging and continuing.
func ProvisionCHSecurity(ctx context.Context, admin driver.Conn, opts ProvisionOpts) error {
	if opts.UserName == "" || opts.DB == "" {
		return fmt.Errorf("provision ch security: UserName and DB are required")
	}
	ttl := opts.T2TTLDays
	if ttl <= 0 {
		ttl = 7
	}
	sctx := scrubbedCtx(ctx)
	stmts := []string{
		fmt.Sprintf(
			"ALTER TABLE query_stats_t2 MODIFY TTL toDateTime(collected_at) + INTERVAL %d DAY", ttl),
		fmt.Sprintf(
			"CREATE USER IF NOT EXISTS %s IDENTIFIED BY '%s'", opts.UserName, opts.UserPassword),
		fmt.Sprintf(
			"GRANT SELECT, INSERT ON %s.* TO %s", opts.DB, opts.UserName),
		fmt.Sprintf(
			"CREATE ROW POLICY OR REPLACE t2_lynceus_only ON query_stats_t2 "+
				"USING (currentUser() = '%s') TO ALL", opts.UserName),
	}
	for _, s := range stmts {
		if err := admin.Exec(sctx, s); err != nil {
			return fmt.Errorf("provision ch security: %w", err)
		}
	}
	return nil
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/store/ -run TestCHSecurity -count=1 -v`
Expected: PASS (all three: RowPolicy_DeniesNonUser, T1_ReadableByNonUser, TTLConfigurable).

- [ ] **Step 6: Commit**

```bash
git add internal/store/chsecurity.go internal/store/chsecurity_test.go internal/testch/testch.go
git commit -m "feat(store): ProvisionCHSecurity — T2 RLS + runtime user + TTL

ly-cwr.6: row policy USING currentUser()='<user>' TO ALL makes the
Lynceus USER the only identity that can read query_stats_t2 rows (T1
stays open); grants the runtime user; bounds T2 retention. Adds
testch.OpenAs. RLS construct verified against real ClickHouse 25.8."
```

---

## Task 4: OpenStats — env rename + ADMIN identity + two-conn bootstrap

**Files:**
- Modify: `internal/store/open.go`
- Test: `internal/store/open_test.go`

**Interfaces:**
- Consumes: `ProvisionCHSecurity`, `ProvisionOpts` (Task 3).
- Produces: `OpenStats(ctx) (Stats, error)` unchanged in signature; now reads `LYNCEUS_CLICKHOUSE_USER_DSN` (required), `LYNCEUS_CLICKHOUSE_ADMIN_DSN` (optional), `LYNCEUS_CLICKHOUSE_T2_TTL_DAYS` (default 7); bootstraps via ADMIN, returns a USER-backed `chStats`.

- [ ] **Step 1: Update the failing test**

Replace the body of `internal/store/open_test.go` with (keep the package clause and the existing helper that provides a CH DSN via testch):

```go
package store_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/dobbo-ca/lynceus/internal/testch"
)

// Two-identity happy path: ADMIN bootstraps (migrate + provision), OpenStats
// returns a USER-backed Stats that can round-trip a T1 write/read.
func TestOpenStats_ClickHouse_TwoIdentity(t *testing.T) {
	ctx := context.Background()
	_, adminDSN := testch.StartDSN(t)
	userDSN := userDSNFor(t, adminDSN)

	t.Setenv("LYNCEUS_STATS_BACKEND", "clickhouse")
	t.Setenv("LYNCEUS_CLICKHOUSE_ADMIN_DSN", adminDSN)
	t.Setenv("LYNCEUS_CLICKHOUSE_USER_DSN", userDSN)
	t.Setenv("LYNCEUS_CLICKHOUSE_T2_TTL_DAYS", "7")

	stats, err := store.OpenStats(ctx)
	if err != nil {
		t.Fatalf("OpenStats: %v", err)
	}
	when := time.Now().UTC()
	if err := stats.WriteQueryStats(ctx, []store.QueryStat{{
		ServerID: "srv", CollectedAt: when, Fingerprint: "fp", NormalizedQuery: "SELECT 1",
		DataTier: 1, Calls: 1, TotalTimeMs: 1,
	}}); err != nil {
		t.Fatalf("write via USER-backed stats: %v", err)
	}
	got, err := stats.TopQueriesByTotalTime(ctx, when.Add(-time.Hour), when.Add(time.Hour), 10)
	if err != nil || len(got) != 1 {
		t.Fatalf("top = (%v, %v), want 1 row", got, err)
	}
}

// USER_DSN is required.
func TestOpenStats_ClickHouse_RequiresUserDSN(t *testing.T) {
	t.Setenv("LYNCEUS_STATS_BACKEND", "clickhouse")
	t.Setenv("LYNCEUS_CLICKHOUSE_ADMIN_DSN", "")
	t.Setenv("LYNCEUS_CLICKHOUSE_USER_DSN", "")
	if _, err := store.OpenStats(context.Background()); err == nil {
		t.Fatal("expected error when LYNCEUS_CLICKHOUSE_USER_DSN is unset")
	}
}

// userDSNFor derives a runtime-user DSN from the admin DSN by ensuring the user
// exists (via the admin conn) and swapping in its credentials. The user is
// created here so OpenStats' own provisioning is exercised as a no-op re-create.
func userDSNFor(t *testing.T, adminDSN string) string {
	t.Helper()
	ctx := context.Background()
	admin := testch.OpenAs(t, adminDSN, "test", "test") // testch bootstrap creds
	if err := admin.Exec(ctx, "CREATE USER IF NOT EXISTS lynceus_user IDENTIFIED BY 'user-pw'"); err != nil {
		t.Fatalf("create user: %v", err)
	}
	db := dbFromDSN(t, adminDSN)
	if err := admin.Exec(ctx, "GRANT SELECT, INSERT ON "+db+".* TO lynceus_user"); err != nil {
		t.Fatalf("grant: %v", err)
	}
	// Rebuild the DSN with the user's creds (same host/port/db).
	// adminDSN form: clickhouse://test:test@host:port/db
	return replaceUserInfo(adminDSN, "lynceus_user", "user-pw")
}

func replaceUserInfo(dsn, user, pass string) string {
	// dsn: scheme://user:pass@rest
	at := indexOf(dsn, '@')
	scheme := "clickhouse://"
	return scheme + user + ":" + pass + dsn[at:]
}

func indexOf(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

var _ = strconv.Itoa // keep strconv imported if unused after edits
```

*(If the existing `open_test.go` already defines a CH-DSN helper, reuse it and drop the duplicate; `dbFromDSN` is defined in `chsecurity_test.go` from Task 3 and is available in the same `store_test` package.)*

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestOpenStats_ClickHouse -count=1 -v`
Expected: FAIL — `OpenStats` still reads `LYNCEUS_CLICKHOUSE_DSN`, so `USER_DSN` is unset → error (or the two-identity path is absent).

- [ ] **Step 3: Rewrite the clickhouse case in OpenStats**

In `internal/store/open.go`, add imports `strconv` and `log`, and replace the `case "clickhouse":` block with:

```go
	case "clickhouse":
		userDSN := os.Getenv("LYNCEUS_CLICKHOUSE_USER_DSN")
		if userDSN == "" {
			return nil, errors.New("LYNCEUS_CLICKHOUSE_USER_DSN required for clickhouse backend")
		}
		userOpts, err := clickhouse.ParseDSN(userDSN)
		if err != nil {
			return nil, fmt.Errorf("parse LYNCEUS_CLICKHOUSE_USER_DSN: %w", err)
		}

		// Bootstrap (DDL + provisioning) runs as ADMIN when provided; otherwise
		// fall back to USER for simple single-identity dev where USER has DDL rights.
		bootstrapDSN := os.Getenv("LYNCEUS_CLICKHOUSE_ADMIN_DSN")
		if bootstrapDSN == "" {
			bootstrapDSN = userDSN
		}
		bootOpts, err := clickhouse.ParseDSN(bootstrapDSN)
		if err != nil {
			return nil, fmt.Errorf("parse LYNCEUS_CLICKHOUSE_ADMIN_DSN: %w", err)
		}
		bootConn, err := clickhouse.Open(bootOpts)
		if err != nil {
			return nil, err
		}
		if err := ApplyClickHouseMigrations(ctx, bootConn); err != nil {
			_ = bootConn.Close()
			return nil, err
		}
		// T2 security is best-effort: a locked-down prod ADMIN may lack rights —
		// log and continue so reads still work, rather than failing startup.
		if err := ProvisionCHSecurity(ctx, bootConn, ProvisionOpts{
			UserName:     userOpts.Auth.Username,
			UserPassword: userOpts.Auth.Password,
			DB:           userOpts.Auth.Database,
			T2TTLDays:    t2TTLDaysFromEnv(),
		}); err != nil {
			log.Printf("WARNING: ClickHouse T2 security provisioning failed; T2 RLS may not be enforced: %v", err)
		}
		_ = bootConn.Close()

		userConn, err := clickhouse.Open(userOpts)
		if err != nil {
			return nil, err
		}
		return NewCHStats(userConn), nil
```

Add the TTL env helper at the bottom of `open.go`:

```go
// t2TTLDaysFromEnv reads LYNCEUS_CLICKHOUSE_T2_TTL_DAYS (default 7).
func t2TTLDaysFromEnv() int {
	if v := os.Getenv("LYNCEUS_CLICKHOUSE_T2_TTL_DAYS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 7
}
```

Update the doc comment on `OpenStats` (lines ~12–24) to name `LYNCEUS_CLICKHOUSE_USER_DSN` / `LYNCEUS_CLICKHOUSE_ADMIN_DSN` instead of `LYNCEUS_CLICKHOUSE_DSN`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestOpenStats_ClickHouse -count=1 -v`
Expected: PASS.

- [ ] **Step 5: Build + full store package test**

Run: `go build ./... && go test ./internal/store/ -count=1`
Expected: build OK; all store tests PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/store/open.go internal/store/open_test.go
git commit -m "feat(store): two-identity OpenStats (ADMIN bootstrap + USER runtime)

ly-cwr.6: rename LYNCEUS_CLICKHOUSE_DSN -> LYNCEUS_CLICKHOUSE_USER_DSN,
add optional LYNCEUS_CLICKHOUSE_ADMIN_DSN and LYNCEUS_CLICKHOUSE_T2_TTL_DAYS.
OpenStats migrates + provisions T2 security via ADMIN (best-effort),
then returns a USER-backed chStats. cmd/api and cmd/ingestion are
unchanged (OpenStats signature is stable)."
```

---

## Task 5: Dev enablement + docs

**Files:**
- Modify: `docker-compose.dev.yml`
- Modify: `README.md`
- Modify: `docs/reference/clickhouse-schema.md`

**Interfaces:**
- Consumes: everything above.
- Produces: dev CH that supports RBAC; docs describing the two-identity + RLS posture.

- [ ] **Step 1: Enable access_management on the dev ClickHouse**

In `docker-compose.dev.yml`, under the `clickhouse:` service `environment:` block (alongside `CLICKHOUSE_USER`/`CLICKHOUSE_PASSWORD`/`CLICKHOUSE_DB`), add:

```yaml
      CLICKHOUSE_DEFAULT_ACCESS_MANAGEMENT: "1"
```

- [ ] **Step 2: Document the two DSNs in README**

In `README.md`, replace the sentence referencing `LYNCEUS_CLICKHOUSE_DSN` (line ~31) with wording that names both identities. Use:

```markdown
ClickHouse is the stats backend (`LYNCEUS_STATS_BACKEND=clickhouse`), reached with **two identities**: `LYNCEUS_CLICKHOUSE_ADMIN_DSN` (DDL + one-time T2 security provisioning) and `LYNCEUS_CLICKHOUSE_USER_DSN` (all runtime reads/writes — the only identity permitted to read the literal-bearing `query_stats_t2` rows). `LYNCEUS_CLICKHOUSE_T2_TTL_DAYS` (default 7) bounds T2 retention. The **config + tamper-evident audit** database always stays vanilla PostgreSQL.
```

- [ ] **Step 3: Rewrite the reference doc's T2 access-control section**

In `docs/reference/clickhouse-schema.md`:

- Update the "Database:" bullet (line ~14) to reference `LYNCEUS_CLICKHOUSE_USER_DSN` instead of `LYNCEUS_CLICKHOUSE_DSN`.
- Replace the "⚠️ Interim" callout above `query_stats_t2` (lines ~359–370) with a note that ly-cwr.6 has landed: T2 rows are readable only by the Lynceus USER via the RLS policy, the T2 path scrubs `query_log`, and TTL is configurable.
- Replace §"T2 access control & isolation (ly-cwr.5 / ly-cwr.6)" (lines ~403–437) with the shipped design:

```markdown
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
```

- [ ] **Step 4: Verify the docs build / no stale references**

Run: `grep -rn "LYNCEUS_CLICKHOUSE_DSN" README.md docs/reference/ internal/ cmd/`
Expected: no matches in live source/docs (only historical `docs/superpowers/{plans,specs}/2026-07-14-*` may still mention it — leave those).

- [ ] **Step 5: Commit**

```bash
git add docker-compose.dev.yml README.md docs/reference/clickhouse-schema.md
git commit -m "docs(ch): document two-identity + RLS T2 isolation; enable dev access_management

ly-cwr.6: dev ClickHouse gets CLICKHOUSE_DEFAULT_ACCESS_MANAGEMENT=1;
README + clickhouse-schema.md describe the ADMIN/USER split, the
query_stats_t2 row policy, query_log scrub, and configurable TTL."
```

---

## Final verification

- [ ] **Full build + vet**

Run: `go build ./... && go vet ./internal/store/... ./internal/testch/...`
Expected: clean.

- [ ] **Full store + testch tests**

Run: `go test ./internal/store/... ./internal/testch/... -count=1`
Expected: PASS — new tests plus the unchanged `t2_read_test.go` (fail-closed, happy path, fast-reject, auth-deny, config-source-of-truth) and `TestT2Read_OnlyOneTier2SelectInStoreSource`.

- [ ] **Broader regression (packages that call OpenStats)**

Run: `go test ./... -count=1`
Expected: PASS. (If unrelated pre-existing failures appear, report them; do not fix out of scope.)

- [ ] **Lint (if configured)**

Run: `golangci-lint run ./internal/store/... ./internal/testch/...` (skip if not installed)
Expected: clean, or only pre-existing findings.

---

## Self-Review notes (author)

- **Spec coverage:** two identities (Task 4) ✓; RLS `currentUser()` policy (Task 3) ✓; query_log scrub (Task 2) ✓; configurable TTL (Tasks 3+4) ✓; env rename (Task 4) ✓; testch access_management (Task 1) ✓; isolation proof + T1-open + TTL tests (Task 3) ✓; unchanged `T2Reader` + single-SELECT invariant (Global Constraints, re-run in Task 2/Final) ✓; docs + dev enablement + override record (Task 5) ✓.
- **Placeholder scan:** none — every code step has full code; `<user>` in SQL is filled from `opts.UserName`.
- **Type consistency:** `ProvisionOpts{UserName,UserPassword,DB,T2TTLDays}` and `ProvisionCHSecurity(ctx, driver.Conn, ProvisionOpts)` used identically in Tasks 3 and 4; `testch.OpenAs(t, dsn, user, pass)` used identically in Tasks 3 and 4; `scrubbedCtx` defined in Task 2, reused in Task 3.
- **Empirical grounding:** the RLS construct, `access_management` env, and `log_queries=0` scrub were verified against real ClickHouse 25.8 before this plan (spike, 2026-07-15).
