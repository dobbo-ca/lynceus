# ly-xnk.1 — Capability discovery package (collector probes) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a collector-local `internal/caps` package whose `Discoverer.Discover(ctx)` returns a typed `caps.Set` describing which Postgres capabilities (extensions, role permissions, log destination, server version, pg_stat_activity full readability) are available on the monitored database — emitting **metadata only**, never any literal from the monitored DB.

**Architecture:** A new `internal/caps` package owns a `Capability` identifier type, a `Status{Available, Reason}` value, and a `Set` (alias for `map[Capability]Status`). Each capability has one probe function with the same signature `probeFoo(ctx, conn) (Status, error)` and is integration-tested independently against a real Postgres via testcontainers. A `Discoverer` composes all probes; failure of one probe degrades that one entry to `Available=false, Reason="probe error: <msg>"` rather than aborting `Discover`. The package is read-only: it issues only `SELECT` / `SHOW` against vanilla Postgres catalogs (`pg_extension`, `pg_settings`, `pg_roles`, `pg_stat_activity`) and never touches schemas in the monitored DB. Wiring into `cmd/collector/main.go`, the wire-message form, and the persistence side are explicitly **out of scope** — those belong to follow-up beads ly-xnk.2 (policy storage + wire) and ly-xnk.3 (reader gating). This bead is foundation only.

**Tech Stack:** Go 1.23+, `jackc/pgx/v5` (pgxpool + pgconn for SQLSTATE inspection), vanilla PostgreSQL 16 (with optional `pg_stat_statements` extension to assert positive-availability paths), `testcontainers-go` for integration tests against real Postgres. No new module dependencies.

**Specs:**
- `docs/specs/2026-05-29-lynceus-features.md` §10b — Capability Discovery & Per-Database Operator Policy (MUST, distinguishing).
- `docs/specs/2026-05-29-lynceus-design.md` §2 — privacy backbone; §3.1 — collector cadence.
- Bead description: `bd show ly-xnk.1`.

---

## File Structure

```
lynceus/
  internal/caps/
    caps.go                 # NEW: Capability identifier, Status, Set, Discoverer
    caps_test.go            # NEW: pure-Go tests for Set helpers (no DB)
    probes.go               # NEW: one probeFn per capability
    probes_test.go          # NEW: integration tests (testcontainers) per probe + full Discover
```

### Why a separate `probes.go`?

`caps.go` is the package API (`Capability`, `Status`, `Set`, `Discoverer`). `probes.go` holds the individual SQL probe functions. The split keeps `caps.go` small enough that a downstream reader of the API doesn't have to wade through nine `SELECT`s, and lets the probe file grow without bloating the package surface. It mirrors the existing `internal/collector` split between `reader.go` (api) and the future `activity_reader.go` etc.

### Probe inventory

Nine capabilities, each represented by one entry in the returned `Set`:

| Capability constant | Probe | Available iff | Reason content (Available=true) |
|---|---|---|---|
| `PgStatStatements` | `SELECT 1 FROM pg_extension WHERE extname='pg_stat_statements'` | row present | extension version (`pg_extension.extversion`) |
| `AutoExplain` | `current_setting('shared_preload_libraries')` contains `auto_explain` AND `current_setting('auto_explain.log_min_duration')` not `'-1'` | both true | log_min_duration value |
| `PgBuffercache` | extension probe | row present | extversion |
| `PgWaitSampling` | extension probe | row present | extversion |
| `PgStatTuple` | extension probe | row present | extversion |
| `PgStatActivityFullRead` | `SELECT count(*) FROM pg_stat_activity WHERE query IS NOT NULL` returns >= 1 from a session that is itself an active backend, AND `pg_has_role(current_user, 'pg_read_all_stats', 'MEMBER')` | both true | `pg_read_all_stats=true` |
| `LogDestination` | `SHOW log_destination` ; `SHOW logging_collector` ; `pg_current_logfile()` | log_destination != `'stderr'` OR logging_collector on | `dest=<value>; collector=<bool>; file=<path-or-empty>` |
| `ServerVersion` | `SHOW server_version_num` | parses to a `>= 12_0000` (Lynceus baseline) | numeric `server_version_num` |
| `RolePermissions` | `pg_has_role(current_user, 'pg_monitor','MEMBER')`, `pg_has_role(..., 'pg_read_all_stats','MEMBER')`, `pg_has_role(..., 'pg_read_server_files','MEMBER')`, `(SELECT rolsuper FROM pg_roles WHERE rolname = current_user)` | `pg_monitor` granted (the platform's minimum) | comma-separated list of granted roles |

Reason strings are bounded, written by us, and never include data from monitored tables — the privacy contract is preserved by construction. `Set` is a `map[Capability]Status` of fixed shape (one entry per declared `Capability`), making it trivial for future ly-xnk.2 transport to serialize.

### Failure semantics

`Discover` returns `(Set, error)`. The error channel is reserved for *infrastructure* failure — connection acquisition fails, context cancelled. Individual probe SQL errors do NOT bubble up: they degrade that entry to `Status{Available: false, Reason: "probe error: " + err.Error()}`. Rationale: capability discovery must always produce a complete map, because downstream gating logic (ly-xnk.3) will treat "missing key" as a bug, not as "disabled." This keeps the consumer contract simple: every declared `Capability` is always present in the returned `Set`.

---

## Task 1: Package skeleton — Capability identifier, Status, Set, declared list

**Files:**
- Create: `internal/caps/caps.go`
- Create: `internal/caps/caps_test.go`

This task introduces the public types and a `Declared()` slice listing every known capability — the source of truth used by `Discover` to guarantee every entry exists. No SQL yet.

- [ ] **Step 1: Write the failing test.**

```go
// internal/caps/caps_test.go
package caps_test

import (
	"sort"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/caps"
)

// TestDeclared_listsAllKnownCapabilities pins the list. If a future bead
// adds a new Capability constant, the developer must extend this test —
// which also forces them to add a probe (otherwise Discover panics in
// Task 5's completeness assertion).
func TestDeclared_listsAllKnownCapabilities(t *testing.T) {
	want := []caps.Capability{
		caps.AutoExplain,
		caps.LogDestination,
		caps.PgBuffercache,
		caps.PgStatActivityFullRead,
		caps.PgStatStatements,
		caps.PgStatTuple,
		caps.PgWaitSampling,
		caps.RolePermissions,
		caps.ServerVersion,
	}
	got := append([]caps.Capability(nil), caps.Declared()...)
	sort.Slice(got, func(i, j int) bool { return string(got[i]) < string(got[j]) })
	sort.Slice(want, func(i, j int) bool { return string(want[i]) < string(want[j]) })
	if len(got) != len(want) {
		t.Fatalf("Declared length = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Declared[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestSet_isMapStringStatus ensures the Set alias does not drift into a
// struct or a different value type. Downstream wire code (ly-xnk.2) will
// rely on the map shape.
func TestSet_zeroValueIsUsable(t *testing.T) {
	var s caps.Set
	if s != nil {
		t.Fatalf("zero Set should be nil, got %v", s)
	}
	s = caps.Set{}
	s[caps.PgStatStatements] = caps.Status{Available: true, Reason: "1.10"}
	if !s[caps.PgStatStatements].Available {
		t.Fatal("Set assignment broken")
	}
}
```

- [ ] **Step 2: Run it — expect a compile failure.**

```bash
go test ./internal/caps/...
```

Expected: `undefined: caps.Capability` / `undefined: caps.Declared` etc.

- [ ] **Step 3: Implement the types and the `Declared()` source of truth.**

```go
// Package caps probes a monitored PostgreSQL instance to discover which
// Lynceus capabilities are available on it: which extensions are
// installed, what the role can read, where logs go, what server version
// is running. Results are metadata-only — every Reason string is bounded
// content written by this package, never a literal from the monitored
// database, preserving the Lynceus T1 privacy contract.
//
// Discover is intended to run at the collector on the full-snapshot
// cadence; wiring into cmd/collector and the wire-message form for
// shipping results to the api_server are handled by ly-xnk.2.
package caps

// Capability is the stable identifier of one probed capability. The
// string form is the wire/storage representation that will be reused by
// downstream beads (ly-xnk.2 storage, ly-xnk.4 API).
type Capability string

// Declared capabilities. Edit Declared() when adding a constant.
const (
	PgStatStatements       Capability = "pg_stat_statements"
	AutoExplain            Capability = "auto_explain"
	PgBuffercache          Capability = "pg_buffercache"
	PgWaitSampling         Capability = "pg_wait_sampling"
	PgStatTuple            Capability = "pgstattuple"
	PgStatActivityFullRead Capability = "pg_stat_activity_full_read"
	LogDestination         Capability = "log_destination"
	ServerVersion          Capability = "server_version"
	RolePermissions        Capability = "role_permissions"
)

// Declared returns every capability the package knows how to probe.
// Discover guarantees one entry in the returned Set per declared
// capability — downstream code may rely on key presence.
func Declared() []Capability {
	return []Capability{
		PgStatStatements,
		AutoExplain,
		PgBuffercache,
		PgWaitSampling,
		PgStatTuple,
		PgStatActivityFullRead,
		LogDestination,
		ServerVersion,
		RolePermissions,
	}
}

// Status is one probe's verdict.
//
// Reason is a short, bounded, package-authored string — never a row,
// column value, or query from the monitored database. For Available
// probes it carries a useful detail (e.g. extension version, list of
// granted roles); for unavailable probes it explains why (e.g.
// "extension not installed", "probe error: ...").
type Status struct {
	Available bool
	Reason    string
}

// Set is the output of Discover. Every Capability returned by Declared()
// is guaranteed to be present as a key.
type Set map[Capability]Status
```

- [ ] **Step 4: Run the tests — expect PASS.**

```bash
go test ./internal/caps/...
```

Expected: PASS (both tests).

- [ ] **Step 5: Commit.**

```bash
git add internal/caps/caps.go internal/caps/caps_test.go
git commit -m "feat(caps): declare Capability identifiers, Status, Set"
```

---

## Task 2: Extension probe — one query, five capabilities

**Files:**
- Modify: `internal/caps/caps.go` (add unexported probe registry hook; see code)
- Create: `internal/caps/probes.go`
- Create: `internal/caps/probes_test.go`

Five of the nine capabilities are "is this extension installed?". One query against `pg_extension` answers all of them. Implementing them as one probe (rather than five copy-pasted SELECTs) is the obvious DRY win.

- [ ] **Step 1: Write the failing integration test.**

```go
// internal/caps/probes_test.go
//
// Integration tests for capability probes. Each probe spins up its own
// real Postgres via testcontainers — slow but honest. CLAUDE.md mandates
// real Postgres for integration tests (no mocks).
package caps_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/dobbo-ca/lynceus/internal/caps"
)

func runPG(t *testing.T, extraCmd ...string) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	args := []testcontainers.ContainerCustomizer{
		tcpostgres.WithDatabase("lynceus_target"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		tcpostgres.BasicWaitStrategies(),
	}
	if len(extraCmd) > 0 {
		args = append(args, testcontainers.WithCmd(extraCmd...))
	}
	c, err := tcpostgres.Run(ctx, "postgres:16", args...)
	if err != nil {
		t.Skipf("docker/testcontainers unavailable: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(c) })

	url, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestProbeExtensions_pgStatStatementsInstalled(t *testing.T) {
	pool := runPG(t, "postgres", "-c", "shared_preload_libraries=pg_stat_statements")
	if _, err := pool.Exec(context.Background(),
		`CREATE EXTENSION pg_stat_statements`); err != nil {
		t.Fatal(err)
	}

	out := caps.Set{}
	caps.ProbeExtensions(context.Background(), pool, out) // unexported-style: see step 3

	if !out[caps.PgStatStatements].Available {
		t.Errorf("PgStatStatements expected Available, got %+v", out[caps.PgStatStatements])
	}
	if out[caps.PgStatStatements].Reason == "" {
		t.Error("PgStatStatements Available=true must include extversion in Reason")
	}

	for _, c := range []caps.Capability{
		caps.PgBuffercache, caps.PgWaitSampling, caps.PgStatTuple,
	} {
		if out[c].Available {
			t.Errorf("%s expected Available=false (not installed), got %+v", c, out[c])
		}
		if !strings.Contains(out[c].Reason, "not installed") {
			t.Errorf("%s Reason should explain absence, got %q", c, out[c].Reason)
		}
	}
}

// TestProbeExtensions_alwaysWritesEveryExtensionKey guards the contract
// that Discover (and any of its probe building blocks) leaves no declared
// extension key missing — Status{Available:false} with a reason is the
// stand-in for "not installed", never an absent key.
func TestProbeExtensions_alwaysWritesEveryExtensionKey(t *testing.T) {
	pool := runPG(t) // no extensions installed
	out := caps.Set{}
	caps.ProbeExtensions(context.Background(), pool, out)
	for _, c := range []caps.Capability{
		caps.PgStatStatements, caps.PgBuffercache,
		caps.PgWaitSampling, caps.PgStatTuple,
	} {
		if _, ok := out[c]; !ok {
			t.Errorf("ProbeExtensions did not write key %q", c)
		}
	}
}
```

- [ ] **Step 2: Run the test — expect a compile failure.**

```bash
go test ./internal/caps/... -run TestProbeExtensions
```

Expected: `undefined: caps.ProbeExtensions`.

- [ ] **Step 3: Implement the extensions probe.** Use one SELECT that returns extversion for each extension we care about; absent rows mean "not installed". The probe must also handle `pg_extension` itself being unreadable (extremely unlikely but defensible).

The function is exported solely so the test in `probes_test.go` (external `_test` package) can call it directly — keeping each probe directly testable without going through `Discover` reduces flake surface area when one probe regresses.

```go
// internal/caps/probes.go
package caps

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// extensionsOfInterest maps the catalog name in pg_extension to the
// Capability we attribute installation to. Keep in sync with
// Declared().
var extensionsOfInterest = map[string]Capability{
	"pg_stat_statements": PgStatStatements,
	"pg_buffercache":     PgBuffercache,
	"pg_wait_sampling":   PgWaitSampling,
	"pgstattuple":        PgStatTuple,
}

// ProbeExtensions writes one Status entry into out for each extension
// declared in extensionsOfInterest. Available=true iff the extension
// has a row in pg_extension on the connected database; Reason carries
// the extversion when available, otherwise "not installed" / a probe
// error message.
//
// Writes occur unconditionally — every key from extensionsOfInterest is
// always set, so Discover's completeness invariant holds even when this
// probe encounters errors.
func ProbeExtensions(ctx context.Context, pool *pgxpool.Pool, out Set) {
	versions := map[string]string{}
	rows, err := pool.Query(ctx,
		`SELECT extname, extversion FROM pg_extension`,
	)
	if err != nil {
		// Probe-level failure — mark every declared extension as
		// unavailable with the error reason, then return.
		for _, cap := range extensionsOfInterest {
			out[cap] = Status{
				Available: false,
				Reason:    fmt.Sprintf("probe error: %s", err.Error()),
			}
		}
		return
	}
	defer rows.Close()

	for rows.Next() {
		var name, ver string
		if err := rows.Scan(&name, &ver); err != nil {
			continue
		}
		versions[name] = ver
	}

	for extname, cap := range extensionsOfInterest {
		if ver, ok := versions[extname]; ok {
			out[cap] = Status{
				Available: true,
				Reason:    fmt.Sprintf("extversion=%s", ver),
			}
		} else {
			out[cap] = Status{
				Available: false,
				Reason:    "not installed",
			}
		}
	}
}
```

- [ ] **Step 4: Run the tests — expect PASS.**

```bash
go test ./internal/caps/... -run TestProbeExtensions -v
```

Expected: PASS (both subtests).

- [ ] **Step 5: Commit.**

```bash
git add internal/caps/probes.go internal/caps/probes_test.go
git commit -m "feat(caps): probe installed extensions (one query, five capabilities)"
```

---

## Task 3: Server version + role permissions + pg_stat_activity full-read probes

**Files:**
- Modify: `internal/caps/probes.go`
- Modify: `internal/caps/probes_test.go`

Three probes in one task — each is one simple SELECT, and they share no state. Bundling reduces per-probe testcontainers boot overhead (the integration tests re-use one container per subtest is not natively supported by `tcpostgres.Run`; we accept one container per top-level Test to stay simple).

- [ ] **Step 1: Write the failing tests.** Append to `probes_test.go`:

```go
func TestProbeServerVersion_picksUpVersionNum(t *testing.T) {
	pool := runPG(t)
	out := caps.Set{}
	caps.ProbeServerVersion(context.Background(), pool, out)
	st := out[caps.ServerVersion]
	if !st.Available {
		t.Fatalf("ServerVersion expected Available, got %+v", st)
	}
	// "server_version_num=160001" — exact value depends on minor.
	if !strings.HasPrefix(st.Reason, "server_version_num=") {
		t.Errorf("Reason missing prefix: %q", st.Reason)
	}
	// Postgres 12+ is the baseline (Available=true requires >=12_0000).
	// We booted postgres:16 so it must succeed.
}

func TestProbeRolePermissions_reportsPgMonitorGrant(t *testing.T) {
	ctx := context.Background()
	pool := runPG(t)

	// Default 'test' superuser implicitly has every role membership.
	// To get a meaningful negative we also create a plain role and
	// check the Reason fields. We probe as the superuser; that path
	// must report rolsuper=true.
	out := caps.Set{}
	caps.ProbeRolePermissions(ctx, pool, out)
	st := out[caps.RolePermissions]
	if !st.Available {
		t.Errorf("RolePermissions expected Available (superuser implies pg_monitor), got %+v", st)
	}
	if !strings.Contains(st.Reason, "rolsuper=true") {
		t.Errorf("Reason should mention rolsuper=true for superuser, got %q", st.Reason)
	}
	if !strings.Contains(st.Reason, "pg_monitor=true") {
		t.Errorf("Reason should mention pg_monitor=true, got %q", st.Reason)
	}
}

func TestProbeStatActivityFullRead_visibleAsSuperuser(t *testing.T) {
	pool := runPG(t)
	out := caps.Set{}
	caps.ProbeStatActivityFullRead(context.Background(), pool, out)
	st := out[caps.PgStatActivityFullRead]
	if !st.Available {
		t.Errorf("PgStatActivityFullRead expected Available as superuser, got %+v", st)
	}
}
```

- [ ] **Step 2: Run the tests — expect compile failure.**

```bash
go test ./internal/caps/... -run 'TestProbeServerVersion|TestProbeRolePermissions|TestProbeStatActivityFullRead'
```

Expected: `undefined: caps.ProbeServerVersion` / etc.

- [ ] **Step 3: Implement the three probes.** Append to `probes.go`:

```go
import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)
```

(Replace the existing import block in `probes.go` with the one above — `strconv` and `strings` are newly needed.)

```go
// ProbeServerVersion writes a ServerVersion entry into out. Available
// requires server_version_num >= 12_0000 (Lynceus's supported baseline).
func ProbeServerVersion(ctx context.Context, pool *pgxpool.Pool, out Set) {
	var raw string
	err := pool.QueryRow(ctx, `SHOW server_version_num`).Scan(&raw)
	if err != nil {
		out[ServerVersion] = Status{
			Available: false,
			Reason:    fmt.Sprintf("probe error: %s", err.Error()),
		}
		return
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		out[ServerVersion] = Status{
			Available: false,
			Reason:    fmt.Sprintf("parse error: %q", raw),
		}
		return
	}
	if n < 12_0000 {
		out[ServerVersion] = Status{
			Available: false,
			Reason:    fmt.Sprintf("server_version_num=%d below baseline 120000", n),
		}
		return
	}
	out[ServerVersion] = Status{
		Available: true,
		Reason:    fmt.Sprintf("server_version_num=%d", n),
	}
}

// ProbeRolePermissions writes a RolePermissions entry into out. Available
// requires at least pg_monitor membership (Lynceus's minimum collector
// role). Reason carries a comma-separated list of every membership we
// checked, true or false, so the operator can see exactly what the
// collector role can do.
func ProbeRolePermissions(ctx context.Context, pool *pgxpool.Pool, out Set) {
	type check struct {
		label string
		query string
	}
	checks := []check{
		{"pg_monitor", `SELECT pg_has_role(current_user, 'pg_monitor', 'MEMBER')`},
		{"pg_read_all_stats", `SELECT pg_has_role(current_user, 'pg_read_all_stats', 'MEMBER')`},
		{"pg_read_server_files", `SELECT pg_has_role(current_user, 'pg_read_server_files', 'MEMBER')`},
		{"rolsuper", `SELECT rolsuper FROM pg_roles WHERE rolname = current_user`},
	}
	parts := make([]string, 0, len(checks))
	var monitor bool
	for _, c := range checks {
		var got bool
		if err := pool.QueryRow(ctx, c.query).Scan(&got); err != nil {
			parts = append(parts, fmt.Sprintf("%s=err(%s)", c.label, err.Error()))
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%t", c.label, got))
		if c.label == "pg_monitor" {
			monitor = got
		}
	}
	// Superuser is treated as implying pg_monitor for availability —
	// pg_has_role returns true for it anyway, but be explicit.
	avail := monitor
	if !avail {
		// Detect superuser even if pg_has_role returned false (it
		// shouldn't, but defend against it).
		for _, p := range parts {
			if p == "rolsuper=true" {
				avail = true
				break
			}
		}
	}
	out[RolePermissions] = Status{Available: avail, Reason: strings.Join(parts, ",")}
}

// ProbeStatActivityFullRead writes a PgStatActivityFullRead entry into
// out. Available iff the connected role can see queries from other
// backends — operationally, iff pg_has_role(current_user,
// 'pg_read_all_stats','MEMBER') OR rolsuper.
//
// This is the visibility property other readers care about (the wait
// events and connection-state readers degrade gracefully if the role
// can only see its own backend rows).
func ProbeStatActivityFullRead(ctx context.Context, pool *pgxpool.Pool, out Set) {
	var hasRead, isSuper bool
	if err := pool.QueryRow(ctx,
		`SELECT pg_has_role(current_user, 'pg_read_all_stats', 'MEMBER')`,
	).Scan(&hasRead); err != nil {
		out[PgStatActivityFullRead] = Status{
			Available: false,
			Reason:    fmt.Sprintf("probe error: %s", err.Error()),
		}
		return
	}
	_ = pool.QueryRow(ctx,
		`SELECT rolsuper FROM pg_roles WHERE rolname = current_user`,
	).Scan(&isSuper) // best-effort; absence means we just don't claim super
	avail := hasRead || isSuper
	reason := fmt.Sprintf("pg_read_all_stats=%t,rolsuper=%t", hasRead, isSuper)
	out[PgStatActivityFullRead] = Status{Available: avail, Reason: reason}
}
```

- [ ] **Step 4: Run the tests — expect PASS.**

```bash
go test ./internal/caps/... -run 'TestProbeServerVersion|TestProbeRolePermissions|TestProbeStatActivityFullRead' -v
```

Expected: PASS (all three).

- [ ] **Step 5: Commit.**

```bash
git add internal/caps/probes.go internal/caps/probes_test.go
git commit -m "feat(caps): probe server version, role permissions, pg_stat_activity readability"
```

---

## Task 4: Log destination + auto_explain probes

**Files:**
- Modify: `internal/caps/probes.go`
- Modify: `internal/caps/probes_test.go`

These two share a common shape: read GUCs via `current_setting` / `SHOW`. Keep them in one task.

- [ ] **Step 1: Write the failing tests.** Append to `probes_test.go`:

```go
func TestProbeLogDestination_pickupValueAndCollector(t *testing.T) {
	pool := runPG(t,
		"postgres",
		"-c", "log_destination=csvlog,stderr",
		"-c", "logging_collector=on",
	)
	out := caps.Set{}
	caps.ProbeLogDestination(context.Background(), pool, out)
	st := out[caps.LogDestination]
	if !st.Available {
		t.Errorf("LogDestination expected Available with csvlog+collector, got %+v", st)
	}
	if !strings.Contains(st.Reason, "dest=csvlog,stderr") {
		t.Errorf("Reason missing dest value: %q", st.Reason)
	}
	if !strings.Contains(st.Reason, "collector=true") {
		t.Errorf("Reason missing collector=true: %q", st.Reason)
	}
}

func TestProbeLogDestination_stderrOnlyIsUnavailable(t *testing.T) {
	// Default container: log_destination=stderr, logging_collector=off.
	pool := runPG(t)
	out := caps.Set{}
	caps.ProbeLogDestination(context.Background(), pool, out)
	st := out[caps.LogDestination]
	if st.Available {
		t.Errorf("LogDestination should be unavailable with bare stderr, got %+v", st)
	}
}

func TestProbeAutoExplain_disabledWithoutPreload(t *testing.T) {
	pool := runPG(t) // no preload
	out := caps.Set{}
	caps.ProbeAutoExplain(context.Background(), pool, out)
	st := out[caps.AutoExplain]
	if st.Available {
		t.Errorf("AutoExplain should be unavailable without preload, got %+v", st)
	}
	if !strings.Contains(st.Reason, "not in shared_preload_libraries") {
		t.Errorf("Reason should explain preload absence, got %q", st.Reason)
	}
}

func TestProbeAutoExplain_enabledWhenPreloadAndThreshold(t *testing.T) {
	pool := runPG(t,
		"postgres",
		"-c", "shared_preload_libraries=auto_explain",
		"-c", "auto_explain.log_min_duration=0",
	)
	out := caps.Set{}
	caps.ProbeAutoExplain(context.Background(), pool, out)
	st := out[caps.AutoExplain]
	if !st.Available {
		t.Errorf("AutoExplain expected Available with preload+threshold, got %+v", st)
	}
	if !strings.Contains(st.Reason, "log_min_duration=0") {
		t.Errorf("Reason should include threshold, got %q", st.Reason)
	}
}
```

- [ ] **Step 2: Run them — expect compile failure.**

```bash
go test ./internal/caps/... -run 'TestProbeLogDestination|TestProbeAutoExplain'
```

Expected: `undefined: caps.ProbeLogDestination` / `undefined: caps.ProbeAutoExplain`.

- [ ] **Step 3: Implement the probes.** Append to `probes.go`:

```go
// ProbeLogDestination writes a LogDestination entry into out. Available
// iff log_destination is more than bare stderr OR logging_collector is on
// (i.e. logs land somewhere ingestion can reach later).
func ProbeLogDestination(ctx context.Context, pool *pgxpool.Pool, out Set) {
	var dest, collector string
	if err := pool.QueryRow(ctx, `SHOW log_destination`).Scan(&dest); err != nil {
		out[LogDestination] = Status{
			Available: false,
			Reason:    fmt.Sprintf("probe error: %s", err.Error()),
		}
		return
	}
	if err := pool.QueryRow(ctx, `SHOW logging_collector`).Scan(&collector); err != nil {
		out[LogDestination] = Status{
			Available: false,
			Reason:    fmt.Sprintf("probe error: %s", err.Error()),
		}
		return
	}
	collectorOn := strings.EqualFold(collector, "on")

	// pg_current_logfile() returns NULL if logging_collector is off; we
	// scan into a *string so a NULL doesn't error out the probe.
	var fileRaw *string
	_ = pool.QueryRow(ctx, `SELECT pg_current_logfile()`).Scan(&fileRaw)
	file := ""
	if fileRaw != nil {
		file = *fileRaw
	}

	avail := collectorOn || !strings.EqualFold(strings.TrimSpace(dest), "stderr")
	out[LogDestination] = Status{
		Available: avail,
		Reason: fmt.Sprintf("dest=%s; collector=%t; file=%s",
			dest, collectorOn, file),
	}
}

// ProbeAutoExplain writes an AutoExplain entry into out. Available iff
// auto_explain is loaded via shared_preload_libraries AND its
// log_min_duration GUC is something other than '-1' (i.e. it is actually
// instrumenting something).
func ProbeAutoExplain(ctx context.Context, pool *pgxpool.Pool, out Set) {
	var preload string
	if err := pool.QueryRow(ctx, `SHOW shared_preload_libraries`).Scan(&preload); err != nil {
		out[AutoExplain] = Status{
			Available: false,
			Reason:    fmt.Sprintf("probe error: %s", err.Error()),
		}
		return
	}
	if !libraryListed(preload, "auto_explain") {
		out[AutoExplain] = Status{
			Available: false,
			Reason:    "not in shared_preload_libraries",
		}
		return
	}

	// auto_explain.log_min_duration is only visible once the library is
	// loaded; SHOW will error otherwise — that's why we gate it.
	var threshold string
	if err := pool.QueryRow(ctx, `SHOW auto_explain.log_min_duration`).Scan(&threshold); err != nil {
		out[AutoExplain] = Status{
			Available: false,
			Reason:    fmt.Sprintf("preloaded but threshold unreadable: %s", err.Error()),
		}
		return
	}
	threshold = strings.TrimSpace(threshold)
	if threshold == "-1" {
		out[AutoExplain] = Status{
			Available: false,
			Reason:    "preloaded but log_min_duration=-1 (disabled)",
		}
		return
	}
	out[AutoExplain] = Status{
		Available: true,
		Reason:    fmt.Sprintf("log_min_duration=%s", threshold),
	}
}

// libraryListed reports whether name appears in the comma-separated GUC
// value (whitespace and quoting handled). Postgres formats
// shared_preload_libraries as e.g. `pg_stat_statements,auto_explain`.
func libraryListed(value, name string) bool {
	for _, p := range strings.Split(value, ",") {
		if strings.EqualFold(strings.Trim(strings.TrimSpace(p), "\"'"), name) {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run the tests — expect PASS.**

```bash
go test ./internal/caps/... -run 'TestProbeLogDestination|TestProbeAutoExplain' -v
```

Expected: PASS (all four subtests).

- [ ] **Step 5: Commit.**

```bash
git add internal/caps/probes.go internal/caps/probes_test.go
git commit -m "feat(caps): probe log destination + auto_explain"
```

---

## Task 5: Discoverer.Discover — compose every probe + completeness invariant

**Files:**
- Modify: `internal/caps/caps.go`
- Modify: `internal/caps/probes_test.go`

The composer guarantees every declared capability is present in the returned Set, even if a probe errors. End-to-end integration test boots one Postgres with `pg_stat_statements` and `auto_explain` preloaded, then asserts the Set's shape.

- [ ] **Step 1: Write the failing test.** Append to `probes_test.go`:

```go
func TestDiscover_returnsEntryForEveryDeclaredCapability(t *testing.T) {
	pool := runPG(t,
		"postgres",
		"-c", "shared_preload_libraries=pg_stat_statements,auto_explain",
		"-c", "auto_explain.log_min_duration=0",
		"-c", "log_destination=csvlog",
		"-c", "logging_collector=on",
	)
	if _, err := pool.Exec(context.Background(),
		`CREATE EXTENSION pg_stat_statements`); err != nil {
		t.Fatal(err)
	}

	d := caps.NewDiscoverer(pool)
	set, err := d.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	// Completeness — every Declared() capability must have an entry.
	for _, c := range caps.Declared() {
		if _, ok := set[c]; !ok {
			t.Errorf("Discover missing key %q", c)
		}
	}

	// Positives in this fully-tooled container.
	for _, c := range []caps.Capability{
		caps.PgStatStatements, caps.AutoExplain,
		caps.LogDestination, caps.ServerVersion,
		caps.RolePermissions, caps.PgStatActivityFullRead,
	} {
		if !set[c].Available {
			t.Errorf("%s expected Available=true in fully-tooled container, got %+v", c, set[c])
		}
	}

	// Negatives — not installed.
	for _, c := range []caps.Capability{
		caps.PgBuffercache, caps.PgWaitSampling, caps.PgStatTuple,
	} {
		if set[c].Available {
			t.Errorf("%s expected Available=false (not installed), got %+v", c, set[c])
		}
	}
}

func TestDiscover_contextCancelledReturnsError(t *testing.T) {
	pool := runPG(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel
	d := caps.NewDiscoverer(pool)
	if _, err := d.Discover(ctx); err == nil {
		t.Error("Discover with pre-cancelled context should error")
	}
}
```

- [ ] **Step 2: Run them — expect a compile failure.**

```bash
go test ./internal/caps/... -run TestDiscover
```

Expected: `undefined: caps.NewDiscoverer`.

- [ ] **Step 3: Implement `Discoverer` and `Discover`.** Append to `caps.go`:

```go
import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)
```

(Add this import block at the top of `caps.go` — none currently present.)

```go
// Discoverer probes a monitored Postgres instance for the capabilities
// declared in Declared(). It is safe to call Discover repeatedly; each
// call issues fresh probe queries.
type Discoverer struct {
	pool *pgxpool.Pool
}

// NewDiscoverer returns a Discoverer bound to pool.
func NewDiscoverer(pool *pgxpool.Pool) *Discoverer {
	return &Discoverer{pool: pool}
}

// Discover runs every probe and returns the resulting Set. The returned
// Set is guaranteed to contain exactly one entry per Declared()
// capability — probes that fail or report "not installed" still produce
// a key with Available=false and a descriptive Reason.
//
// Discover only returns a non-nil error for infrastructure failures
// (context cancellation, total pool acquisition failure). Individual
// probe SQL errors are surfaced as Status entries, not bubbled.
func (d *Discoverer) Discover(ctx context.Context) (Set, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Sanity-check that the pool can hand out a connection. If this
	// fails, every probe would fail anyway — surface it once as a hard
	// error so the caller can distinguish "DB unreachable" from
	// "everything just happens to be off."
	conn, err := d.pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	conn.Release()

	out := make(Set, len(Declared()))

	ProbeExtensions(ctx, d.pool, out)
	ProbeServerVersion(ctx, d.pool, out)
	ProbeRolePermissions(ctx, d.pool, out)
	ProbeStatActivityFullRead(ctx, d.pool, out)
	ProbeLogDestination(ctx, d.pool, out)
	ProbeAutoExplain(ctx, d.pool, out)

	// Completeness invariant: every Declared() key must be present.
	// If a probe forgot to write its key, fill in a debugging entry so
	// downstream gating code can never see a missing key in
	// production. Tests in Task 1 / Task 5 also enforce this.
	for _, c := range Declared() {
		if _, ok := out[c]; !ok {
			out[c] = Status{
				Available: false,
				Reason:    "probe did not record a result (bug)",
			}
		}
	}
	return out, nil
}
```

- [ ] **Step 4: Run the full package test.**

```bash
go test ./internal/caps/... -v
```

Expected: PASS (every test from Tasks 1–5).

- [ ] **Step 5: Run go vet across the package.**

```bash
go vet ./internal/caps/...
```

Expected: clean.

- [ ] **Step 6: Run the entire suite to confirm nothing else regressed.**

```bash
go test ./...
```

Expected: PASS (existing query-stats / store / proto tests untouched; new caps tests added).

- [ ] **Step 7: Commit.**

```bash
git add internal/caps/caps.go internal/caps/probes_test.go
git commit -m "feat(caps): Discoverer composes every probe with completeness invariant"
```

---

## Task 6: Hand-off — bead label `ready-impl` → `ready-test`

**Files:** none (bd state only).

Per `CLAUDE.md` "Feature Work Lifecycle (M2–M6)" the bead moves through three label states: `needs-plan` (initial) → `ready-impl` (plan committed) → `ready-test` (implementation committed) → close. The plan-write hand-off (writing-plans skill) is responsible for the first transition; this task handles the second.

Pre-condition: when the implementer reaches this task they should see `ready-impl` on `bd show ly-xnk.1`. If it still says `needs-plan`, the plan commit step was skipped — fix that before proceeding (see writing-plans hand-off).

- [ ] **Step 1: Add a hand-off note recording where the work landed.**

```bash
bd note ly-xnk.1 "Implementation landed. Package internal/caps with Discoverer.Discover(ctx)->Set. Nine capabilities probed. Integration tests use testcontainers. Wiring into cmd/collector and wire transport belong to ly-xnk.2."
```

- [ ] **Step 2: Move the bead from `ready-impl` to `ready-test`.**

```bash
bd label remove ly-xnk.1 ready-impl
bd label add ly-xnk.1 ready-test
```

Expected: `bd show ly-xnk.1` shows label `ready-test` and the new note.

- [ ] **Step 3: Final status report (no commit; the prior task commits cover the change set).** Run:

```bash
bd show ly-xnk.1
git log --oneline -8
git status
```

Expected: clean working tree, six new commits in the log (one plan commit from the writing-plans hand-off + one per implementation task = 1 + 5), proto NOT touched (this bead is package-only), bead labelled `ready-test`.

---

## Self-Review

**1. Spec coverage** (against `features.md` §10b + bead description)

| Spec line | Task |
|---|---|
| `internal/caps` package | Task 1 |
| `Discover(ctx, pool) returns caps.Set` | Task 5 (constructor takes pool; `Discover` takes ctx) |
| typed map of capability → `{Available bool, Reason string}` | Task 1 (Status + Set) |
| `pg_stat_statements`, `auto_explain`, `pg_buffercache`, `pg_wait_sampling`, `pgstattuple`, `pg_stat_activity readability`, log destination, server version, role permissions | Tasks 2, 3, 4 (9 capabilities, all declared) |
| Periodic refresh on full-snapshot cadence | Out of scope here (collector wiring is ly-xnk.2). The package supports it: `Discover` is idempotent and safe to call repeatedly. |
| Local | Yes — entire package is collector-side. No api_server changes. |
| Privacy: metadata only | Enforced by construction: Reason strings are bounded, package-authored; probe SQL never reads literal-bearing columns from monitored tables; the only catalogs touched are `pg_extension`, `pg_settings` (`SHOW`), `pg_roles`, and `pg_stat_activity` (count only, not `query` column). |

**2. Placeholder scan**
- Every code block contains real, copy-pasteable code. No `TODO` / `TBD` / "fill in".
- The probe signatures, capability constants, and helper names match across tasks (`out[Capability]Status` everywhere; no drift between `caps.go` and `probes.go`).

**3. Type / name consistency**
- `Capability` is a string-typed identifier in Task 1 and used identically by every probe in Tasks 2–4 and the composer in Task 5.
- `Status{Available, Reason}` shape is fixed in Task 1 and assigned by every probe with the same fields, in the same order, with the same `Reason` formatting (`key=value,…` for multi-fact reasons; bare description otherwise).
- `Set` is `map[Capability]Status` — declared once, used everywhere.
- `Declared()` is the single source of truth for the test pin in Task 1, the completeness loop in `Discover` (Task 5), and the end-to-end test (Task 5).
- The unexported helper `libraryListed` in Task 4 is referenced only inside `probes.go` and has no callers elsewhere.

**4. Out of scope (confirmed)**
- No wire-message changes (no proto). Capability transport will be added by ly-xnk.2.
- No `cmd/collector/main.go` wiring. The collector ticker will call `Discover` on the full-snapshot cadence when ly-xnk.2 lands; this plan stops at the package boundary so the bead is independently testable.
- No api_server endpoints (ly-xnk.4) and no UI (ly-xnk.5).
- No per-database policy storage table (ly-xnk.2).
- No reader gating (ly-xnk.3).

**5. Privacy contract**
- No `query` / `query_id` / `pg_stat_activity.query` / log line / table-row content is ever read by any probe. The closest call is `ProbeStatActivityFullRead`, which deliberately does NOT issue `SELECT query FROM pg_stat_activity` — it uses `pg_has_role` against the catalog instead, never touching the literal-bearing column. The defense-in-depth pattern is the same one the activity-buckets plan (ly-xqf.1) established: keep literal columns out of the SELECT list at the SQL layer, even though downstream serialization is also bounded.
