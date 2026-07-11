# SQL Console (T2) Implementation Plan

> For agentic workers: execute this plan with **superpowers:subagent-driven-development** — one task per subagent, TDD (write the failing test, watch it fail, implement, watch it pass, commit) at every step. Bead: **ly-ae6.8** (UI: SQL Console (T2) — target picker, session-grant gate, results, strict audit). Design source of truth: `docs/design/README.md` §"SQL Console (T2)", `docs/design/PRODUCT_INTENT.md` §6, `docs/design/COMPARISON.md` §"SQL Console (T2)", and the prototype `docs/design/Lynceus.dc.html` lines 1591–1745 / 2554–2614 / 2845–2863 / 3362–3430.

**Goal:** Ship the scoped, session-granted SQL Console screen — target picker (RUN inert until node+database resolve), per-cluster session-grant banner and request-access gate, editor with row-limit/timeout, paginated results with per-user rows-per-page plus COPY/CSV/SQL export, and strict per-statement audit into the org audit log and session history.

**Architecture:** A new `internal/console` package holds the execution/grant/session seams (interfaces + deterministic stubs + an in-memory per-actor run cache) so the screen is fully demoable before the real outbound execution transport exists. `internal/api/console.go` builds a `web.ConsoleVM` from the config-DB topology (cluster → instances → server streams), the grant reader, and the session cache, and renders `web/console.templ` (a fully token-styled screen with a `#console-body` HTMX swap unit). RUN executes via the stub `console.Executor`, writes a real tamper-evident audit row via the existing `store.Config.AppendAuditReturning` (action `console.query.execute`, tier 2), and caches the result for pagination/export/history without re-auditing on read-back.

**Tech Stack:** Go 1.x, templ + HTMX SSR (no JS framework; one tiny self-hosted `web/static/js/console.js` for ⌘↵ and clipboard copy), pgx/pgxpool over vanilla PostgreSQL, testcontainers for handler tests, design tokens in `web/static/css/tokens.css`.

## Global Constraints (project-wide rules — copied verbatim, do not violate)

- **Privacy T1/T2.** Only T1 (normalized, literal-free) data renders anywhere unless the screen is *explicitly* T2. The SQL Console **is** the T2 surface: statement text and result cells are literal-capable and permitted **only** here, behind the session grant, and **every** statement run is audited. Never introduce a raw-literal field into a T1 path; never add a raw-sample/raw-text field to a T1 proto message. The audit row for a run is a deliberate T2 governance artifact stored in `audit_log` (`data_tier = 2`), not a stats-store path.
- **T2 reads are gated + audited, fail-closed.** A run returns results to the user **only after** its audit row is durably written; if the audit append fails, discard the result and surface an error (mirrors the fail-closed ordering in `internal/store/t2_read.go` and `internal/store/capability_policy.go`). A run against a cluster with no active session grant must not execute and must not audit.
- **No external hosts.** `web/static/` is embedded and served at `/static/` (auth-free). Add new css/js/svg under `web/static/` and reference `/static/...`. NEVER add a CDN/font/script host — there is a contract test `web.TestLayout_NoExternalHosts`. Any new page must likewise reference only same-origin assets.
- **Tokens, not legacy.** New screens are built with design tokens (`var(--x)` from `tokens.css`), NOT the pre-design `legacy.css` component classes. Match the prototype's inline-`style="…var(--x)…"` approach for parity.
- **templ regen is committed.** After editing any `web/*.templ`, run `make templ` to regenerate the committed `_templ.go`; CI checks they are in sync.
- **Integration tests hit real Postgres via testcontainers.** `internal/api` handler tests spin up `postgres:16` and apply migrations (see `internal/api/server_test.go`, `internal/api/databases_test.go`); do NOT mock the database. Pure logic (`internal/console`, view-model builders, templ render) is unit-tested without a DB.

## Dependencies & integration contract (state, do not re-plan)

- **ly-ae6.2 (scope state + top bar)** and **ly-ae6.3 (scoped nav)** — the console lives inside the scoped shell and appears in nav **only at cluster/node/database scope**. This plan owns the console routes and their URL contract; ly-ae6.3 owns the nav entry that links to them. **Contract ly-ae6.3 must honour:**
  - Cluster scope → link `GET /databases/{clusterID}/console` (picker lets the user choose node **and** database).
  - Node scope → link `GET /databases/{clusterID}/console?node={instanceName}&lockNode=1` (node axis fixed; pick database).
  - Database scope → link `GET /databases/{clusterID}/console?db={dbName}&lockDatabase=1` (database axis fixed; pick node).
  The console reads `node`, `db`, `lockNode`, `lockDatabase` from the query string, so it already behaves correctly once ly-ae6.3 emits these links. **This plan does not edit `web/layout.templ` nav** (owned by ly-ae6.3).
- **ly-8b0.6 (M5: T2 gating + per-read audit)** — the `audit_log` table, hash chain, and `store.Config.AppendAuditReturning` used for the strict per-statement audit already exist on this branch (`internal/store/config.go`, `internal/store/audit_hash.go`). This plan consumes them; it does not re-plan the audit writer.
- **ly-8b0.5 (M5: RBAC group→tier grants)** — the *real* per-cluster, time-boxed session-grant authority that `console.GrantReader` abstracts. Until it lands, `console.StubGrantReader` grants read-only access iff the cluster is named `orders-prod` (matching the prototype). **File a backend bead:** *"Session-grant service — per-cluster time-boxed T2 grant (group/approver/expiry/incident), request/approval flow"* — this plan stubs it and references it.
- **NEW backend bead to file:** *"Live read-only SQL execution transport — bounded (row-limit/timeout) ad-hoc SELECT against a monitored target, returning rows"*. `COMPARISON.md` line 332 flags this as a net-new architectural capability that exists nowhere (the collector is outbound-only). This plan defines the `console.Executor` seam and ships `console.StubExecutor`; the real transport replaces the stub with no UI change.
- **ly-ae6.9 (Saved Scripts)** — the editor toolbar's `SAVE ▾` control and saved-script search input are the seam where ly-ae6.9 mounts its save/list/run-a-script behavior. This plan renders those controls (for screen parity) pointing at ly-ae6.9-owned hrefs (`ConsoleEditorVM.SaveScriptsHref`, `.SearchScriptsHref`); their dynamic dropdown behavior is ly-ae6.9's, not this bead's.

---

### Task 1: `internal/console` seams — Executor, GrantReader, in-memory session cache

Pure Go, no DB. Establishes the interfaces the handler consumes and the deterministic stubs that mirror the prototype so the screen is fully testable now.

**Files**
- create `internal/console/console.go` — `Result`, `Statement`, `Executor`
- create `internal/console/executor_stub.go` — `StubExecutor` + fixed dataset
- create `internal/console/grant.go` — `SessionGrant`, `GrantReader`, `StubGrantReader`
- create `internal/console/session.go` — `Run`, `Sessions`
- create `internal/console/console_test.go` — unit tests

**Interfaces**

Produces:
```go
// package console
type Result struct {
	Columns    []string
	Rows       [][]string // each cell an already-rendered display string (T2)
	DurationMs float64
}
func (r Result) RowCount() int // == len(r.Rows)

type Statement struct {
	ClusterID, ClusterName string
	Node, Database         string // instance name; database name within it
	ServerID               string // resolved stream key for (Node, Database)
	SQL                    string
	RowLimit, TimeoutSecs  int
	Actor                  string
}
type Executor interface {
	Execute(ctx context.Context, stmt Statement) (Result, error)
}
type StubExecutor struct{} // deterministic pg_stat_user_tables-shaped rows, truncated to RowLimit

type SessionGrant struct {
	ClusterName, Group, Approver, Incident string
	ReadOnly                               bool
	GrantedAt, ExpiresAt                   time.Time
}
type GrantReader interface {
	ActiveGrant(ctx context.Context, clusterID, clusterName, actor string) (SessionGrant, bool, error)
}
type StubGrantReader struct{ Now func() time.Time } // grants iff clusterName=="orders-prod"

type Run struct {
	ID         string // FULL audit-row hash hex — stable, URL-safe lookup key (restore token + Get key)
	ShortHash  string // display form only, e.g. "6c1d…e44"; NEVER a lookup key (multibyte ellipsis)
	ClusterID  string // owning cluster — the cache is keyed by (actor, clusterID) so runs never leak across scope
	At         time.Time
	SQL        string
	Node, Database string
	Result     Result
	DurationMs float64
}
type Sessions struct{ /* per-(actor,cluster) ring, newest first */ }
func NewSessions(capacity int) *Sessions
func (s *Sessions) Append(actor, clusterID string, r Run)
func (s *Sessions) Recent(actor, clusterID string) []Run
func (s *Sessions) Latest(actor, clusterID string) (Run, bool)
func (s *Sessions) Get(actor, clusterID, id string) (Run, bool)
```

> **Why a full-hex `ID` distinct from `ShortHash`.** The displayed hash (`6c1d…e44`) contains a multibyte ellipsis; round-tripping it through a URL query param and an HTML attribute as a `restore=` token / map key is encoding-fragile and collision-prone. `ID` is the raw `hex.EncodeToString(rowHash)` (64 ASCII hex chars, URL-safe) used for `sessions.Get` and the `restore=` link; `ShortHash` is display-only.
> **Why `(actor, clusterID)` keys.** Pagination, export and history-restore read the cache back on later requests. Keying by actor alone would let `GET /databases/<clusterB>/console/export` return the actor's latest run from `clusterA`. Keying by `(actor, clusterID)` scopes every read-back to the URL's cluster.

- [ ] **Step 1: Write the failing test.** Create `internal/console/console_test.go`:
```go
package console

import (
	"context"
	"testing"
	"time"
)

func TestStubExecutor_deterministicRowsTruncatedToLimit(t *testing.T) {
	full, err := StubExecutor{}.Execute(context.Background(), Statement{RowLimit: 0})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := []string{"relname", "n_dead_tup", "last_autovacuum"}; len(full.Columns) != 3 ||
		full.Columns[0] != got[0] || full.Columns[1] != got[1] || full.Columns[2] != got[2] {
		t.Fatalf("columns = %v, want %v", full.Columns, got)
	}
	if full.RowCount() != 54 {
		t.Fatalf("full row count = %d, want 54 (deterministic dataset)", full.RowCount())
	}
	if full.DurationMs != 18.4 {
		t.Errorf("duration = %v, want 18.4", full.DurationMs)
	}
	limited, _ := StubExecutor{}.Execute(context.Background(), Statement{RowLimit: 10})
	if limited.RowCount() != 10 {
		t.Errorf("limited row count = %d, want 10", limited.RowCount())
	}
	again, _ := StubExecutor{}.Execute(context.Background(), Statement{RowLimit: 10})
	if again.Rows[0][0] != limited.Rows[0][0] {
		t.Error("stub executor is not deterministic across calls")
	}
}

func TestStubGrantReader_grantsOnlyOrdersProd(t *testing.T) {
	fixed := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	g := StubGrantReader{Now: func() time.Time { return fixed }}
	grant, ok, err := g.ActiveGrant(context.Background(), "c1", "orders-prod", "dev-admin")
	if err != nil || !ok {
		t.Fatalf("orders-prod: ok=%v err=%v, want granted", ok, err)
	}
	if grant.Group != "dba-oncall" || grant.Incident != "INC-2214" || !grant.ReadOnly {
		t.Errorf("grant = %+v, want dba-oncall/INC-2214/read-only", grant)
	}
	if !grant.ExpiresAt.After(fixed) {
		t.Error("grant must expire in the future")
	}
	if _, ok, _ := g.ActiveGrant(context.Background(), "c2", "staging", "dev-admin"); ok {
		t.Error("staging must NOT be granted by the stub")
	}
}

func TestSessions_appendCapAndGet(t *testing.T) {
	s := NewSessions(2)
	s.Append("dev-admin", "c1", Run{ID: "a", SQL: "SELECT 1"})
	s.Append("dev-admin", "c1", Run{ID: "b", SQL: "SELECT 2"})
	s.Append("dev-admin", "c1", Run{ID: "c", SQL: "SELECT 3"})
	recent := s.Recent("dev-admin", "c1")
	if len(recent) != 2 || recent[0].ID != "c" || recent[1].ID != "b" {
		t.Fatalf("recent = %v, want [c b] (capped, newest first)", recent)
	}
	latest, ok := s.Latest("dev-admin", "c1")
	if !ok || latest.ID != "c" {
		t.Fatalf("latest = %v,%v want c,true", latest.ID, ok)
	}
	if _, ok := s.Get("dev-admin", "c1", "a"); ok {
		t.Error("evicted run 'a' should be gone")
	}
	if got, ok := s.Get("dev-admin", "c1", "b"); !ok || got.SQL != "SELECT 2" {
		t.Errorf("Get(b) = %v,%v", got.SQL, ok)
	}
	if _, ok := s.Latest("nobody", "c1"); ok {
		t.Error("unknown actor should have no latest")
	}
	// Cross-cluster isolation: a run cached under c1 must NOT be visible under c2.
	if _, ok := s.Latest("dev-admin", "c2"); ok {
		t.Error("runs must be scoped per (actor, cluster) — no cross-cluster leak")
	}
	if _, ok := s.Get("dev-admin", "c2", "b"); ok {
		t.Error("Get must not return a run from a different cluster")
	}
}
```
- [ ] **Step 2: Run it — expect FAIL (does not compile: package/types undefined).**
  `go test ./internal/console/...`
  Expected: build failure (`undefined: StubExecutor`, etc.).
- [ ] **Step 3: Implement `internal/console/console.go`:**
```go
// Package console holds the SQL-Console execution/grant/session seams.
// It is the app's single explicitly-T2 execution surface: statement text
// and result cells are literal-capable and permitted here alone, behind a
// session grant, with every run audited by the caller.
package console

import "context"

// Result is one executed statement's full result set. Cell values are
// already-rendered display strings; literals are allowed (T2 surface only).
type Result struct {
	Columns    []string
	Rows       [][]string
	DurationMs float64
}

// RowCount is the total number of rows in the full result (not a page).
func (r Result) RowCount() int { return len(r.Rows) }

// Statement is one bounded, read-only execution request against a single
// resolved (cluster, node, database) target.
type Statement struct {
	ClusterID   string
	ClusterName string
	Node        string
	Database    string
	ServerID    string
	SQL         string
	RowLimit    int
	TimeoutSecs int
	Actor       string
}

// Executor runs one bounded read-only statement and returns its rows. The
// production implementation is a net-new outbound execution transport
// (tracked as a separate backend bead); StubExecutor stands in until then.
type Executor interface {
	Execute(ctx context.Context, stmt Statement) (Result, error)
}
```
- [ ] **Step 4: Implement `internal/console/executor_stub.go`:**
```go
package console

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// StubExecutor returns a deterministic pg_stat_user_tables-shaped result
// (relname, n_dead_tup, last_autovacuum), truncated to Statement.RowLimit.
// It mirrors the design prototype's fixed dataset (Lynceus.dc.html:2211-2226)
// so the screen is fully demoable before the real transport lands.
type StubExecutor struct{}

var stubColumns = []string{"relname", "n_dead_tup", "last_autovacuum"}
var stubRows = buildStubRows()

// Execute ignores SQL and returns the fixed dataset truncated to RowLimit.
func (StubExecutor) Execute(_ context.Context, stmt Statement) (Result, error) {
	limit := stmt.RowLimit
	if limit <= 0 || limit > len(stubRows) {
		limit = len(stubRows)
	}
	rows := make([][]string, limit)
	copy(rows, stubRows[:limit])
	return Result{Columns: stubColumns, Rows: rows, DurationMs: 18.4}, nil
}

func buildStubRows() [][]string {
	base := []string{"orders", "orders_audit", "events", "customers", "order_items",
		"payments", "shipments", "refunds", "sessions", "webhooks"}
	seed := int64(7)
	rnd := func() int64 { seed = (seed * 16807) % 2147483647; return seed }
	var rows [][]string
	for i, tbl := range base {
		parts := 5
		if i < 4 {
			parts = 6
		}
		for p := 0; p < parts; p++ {
			name := tbl
			if p > 0 {
				name = fmt.Sprintf("%s_p2026_0%d", tbl, p)
			}
			ceil := int64(20000)
			if p == 0 {
				ceil = 200000
			}
			dead := rnd() % ceil
			var last string
			if rnd()%100 < 15 {
				last = "never"
			} else {
				last = fmt.Sprintf("2026-07-%02d %02d:%02d:12Z", 9+int(rnd()%2), rnd()%24, rnd()%60)
			}
			rows = append(rows, []string{name, groupThousands(dead), last})
		}
	}
	sort.SliceStable(rows, func(a, b int) bool { return ungroup(rows[a][1]) > ungroup(rows[b][1]) })
	return rows
}

func groupThousands(n int64) string {
	s := strconv.FormatInt(n, 10)
	var out strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			out.WriteByte(',')
		}
		out.WriteRune(c)
	}
	return out.String()
}

func ungroup(s string) int64 {
	n, _ := strconv.ParseInt(strings.ReplaceAll(s, ",", ""), 10, 64)
	return n
}
```
- [ ] **Step 5: Implement `internal/console/grant.go`:**
```go
package console

import (
	"context"
	"time"
)

// SessionGrant is an active, time-boxed, per-cluster T2 grant. The real
// grant authority is the RBAC session-grant service (backend bead);
// StubGrantReader mirrors the prototype's single granted cluster.
type SessionGrant struct {
	ClusterName string
	Group       string
	Approver    string
	Incident    string
	ReadOnly    bool
	GrantedAt   time.Time
	ExpiresAt   time.Time
}

// GrantReader reports the caller's active session grant for a cluster.
type GrantReader interface {
	ActiveGrant(ctx context.Context, clusterID, clusterName, actor string) (SessionGrant, bool, error)
}

// StubGrantReader grants read-only access iff the cluster is named
// "orders-prod" (Lynceus.dc.html:2855), with a fixed 3h12m window from Now.
type StubGrantReader struct{ Now func() time.Time }

// ActiveGrant implements GrantReader.
func (g StubGrantReader) ActiveGrant(_ context.Context, _, clusterName, _ string) (SessionGrant, bool, error) {
	if clusterName != "orders-prod" {
		return SessionGrant{}, false, nil
	}
	now := time.Now().UTC()
	if g.Now != nil {
		now = g.Now()
	}
	return SessionGrant{
		ClusterName: clusterName,
		Group:       "dba-oncall",
		Approver:    "j.alvarez",
		Incident:    "INC-2214",
		ReadOnly:    true,
		GrantedAt:   now,
		ExpiresAt:   now.Add(3*time.Hour + 12*time.Minute),
	}, true, nil
}
```
- [ ] **Step 6: Implement `internal/console/session.go`:**
```go
package console

import (
	"sync"
	"time"
)

// Run is one executed console statement retained per (actor, cluster) so
// pagination, export and history-restore read back the full result without
// re-executing or re-auditing.
type Run struct {
	ID         string // full audit-row hash hex — stable, URL-safe lookup key
	ShortHash  string // display form only ("6c1d…e44"); never a lookup key
	ClusterID  string // owning cluster (redundant with the map key; kept for callers)
	At         time.Time
	SQL        string
	Node       string
	Database   string
	Result     Result
	DurationMs float64
}

// Sessions is an in-memory ring of recent runs, keyed by (actor, cluster) and
// newest first. It is process-local UI state (like the prototype's
// consoleSession), not a durable store; the durable record of every run is the
// audit_log. Keying by cluster keeps a run from one scope from being read back
// (paginated / exported / restored) under a different cluster's URL.
type Sessions struct {
	mu    sync.Mutex
	byKey map[string][]Run
	cap   int
}

// sessionKey composites the actor and cluster into a single map key. \x00 is a
// safe separator because neither actor ids nor cluster ids contain a NUL byte.
func sessionKey(actor, clusterID string) string { return actor + "\x00" + clusterID }

// NewSessions returns a cache retaining `capacity` runs per (actor, cluster).
func NewSessions(capacity int) *Sessions {
	if capacity <= 0 {
		capacity = 5
	}
	return &Sessions{byKey: map[string][]Run{}, cap: capacity}
}

// Append prepends r for (actor, clusterID) and trims to the capacity.
func (s *Sessions) Append(actor, clusterID string, r Run) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := sessionKey(actor, clusterID)
	list := append([]Run{r}, s.byKey[k]...)
	if len(list) > s.cap {
		list = list[:s.cap]
	}
	s.byKey[k] = list
}

// Recent returns a copy of the (actor, cluster) runs, newest first.
func (s *Sessions) Recent(actor, clusterID string) []Run {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Run(nil), s.byKey[sessionKey(actor, clusterID)]...)
}

// Latest returns the (actor, cluster) most recent run.
func (s *Sessions) Latest(actor, clusterID string) (Run, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if list := s.byKey[sessionKey(actor, clusterID)]; len(list) > 0 {
		return list[0], true
	}
	return Run{}, false
}

// Get returns the (actor, cluster) run with the given full-hex id.
func (s *Sessions) Get(actor, clusterID, id string) (Run, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.byKey[sessionKey(actor, clusterID)] {
		if r.ID == id {
			return r, true
		}
	}
	return Run{}, false
}
```
- [ ] **Step 7: Run — expect PASS.**
  `go test ./internal/console/...`
  Expected: `ok  github.com/dobbo-ca/lynceus/internal/console`.
- [ ] **Step 8: Commit.**
  `git add internal/console && git commit -m "console: execution/grant/session seams + deterministic stubs (ly-ae6.8)"`

---

### Task 2: Console view-models + `console.templ` screen + `console.js`

The token-styled screen with all four states (unavailable / target-picker+granted / target-picker+locked), plus the self-hosted JS for ⌘↵ and clipboard copy. Rendered-HTML unit tests only (no DB).

**Files**
- create `web/console.go` — view-model structs
- create `web/console.templ` — `ConsolePage`, `ConsoleBody`
- create `web/static/js/console.js` — ⌘↵ run + clipboard copy (size-guarded)
- create `web/console_test.go` — templ render tests
- (generated) `web/console_templ.go` via `make templ`

**Interfaces**

Produces (`package web`):
```go
type ConsoleVM struct {
	ClusterID        string
	ClusterName      string
	Granted          bool
	Grant            ConsoleGrantVM
	Picker           ConsolePickerVM
	CapabilitiesHref string // request-access target when locked
	Editor           ConsoleEditorVM
	HasResult        bool
	Result           ConsoleResultVM
	History          []ConsoleHistoryRow
}
type ConsoleGrantVM struct{ Group, Incident, Expires, Approver, AuditHref string; ReadOnly bool }
type ConsolePickerVM struct {
	ClusterLabel  string
	GrantChip     string // "● SESSION GRANT ACTIVE …" | "○ NO SESSION GRANT ON THIS CLUSTER"
	Granted       bool
	Nodes         []ConsoleChip
	Databases     []ConsoleChip
	NodeFixed     bool
	DatabaseFixed bool
}
type ConsoleChip struct{ Label, Href string; Selected bool }
type ConsoleEditorVM struct {
	TargetName        string // "primary · db: appdb" | "(SELECT NODE & DATABASE ABOVE)"
	SQL               string
	RowLimit          int
	TimeoutSecs       int
	Ready             bool
	RunHref           string
	SaveScriptsHref   string // seam → ly-ae6.9
	SearchScriptsHref string // seam → ly-ae6.9
}
type ConsoleResultVM struct {
	Columns      []string
	Rows         [][]string // current page only
	TotalRows    int
	DurationMs   float64
	Hash         string // short "6c1d…e44"
	PageLabel    string // "ROWS 1–25 OF 54"
	PrevHref     string
	NextHref     string
	PrevActive   bool
	NextActive   bool
	PageSizes    []ConsolePageSize
	CopyTSV      string // full-result TSV embedded for client copy; "" if too large
	CopyTooLarge bool
	CsvHref      string
	SqlHref      string
}
type ConsolePageSize struct{ Label, Href string; Selected bool }
type ConsoleHistoryRow struct{ TS, Stmt, Ms, Hash, Href string }
```
Consumes: `templ`, `tokens.css` variables, `/static/js/console.js`.

- [ ] **Step 1: Write the failing test.** Create `web/console_test.go`:
```go
package web

import (
	"context"
	"strings"
	"testing"
)

func renderConsoleBody(t *testing.T, vm ConsoleVM) string {
	t.Helper()
	var sb strings.Builder
	if err := ConsoleBody(vm).Render(context.Background(), &sb); err != nil {
		t.Fatalf("render: %v", err)
	}
	return sb.String()
}

func grantedVM() ConsoleVM {
	return ConsoleVM{
		ClusterID:   "c1",
		ClusterName: "orders-prod",
		Granted:     true,
		Grant:       ConsoleGrantVM{Group: "dba-oncall", Incident: "INC-2214", Expires: "3H 12M", ReadOnly: true, AuditHref: "/audit"},
		Picker: ConsolePickerVM{
			ClusterLabel: "orders-prod", GrantChip: "● SESSION GRANT ACTIVE", Granted: true,
			Nodes:     []ConsoleChip{{Label: "primary", Href: "/partial/databases/c1/console?node=primary", Selected: true}},
			Databases: []ConsoleChip{{Label: "appdb", Href: "/partial/databases/c1/console?db=appdb", Selected: true}},
		},
		Editor: ConsoleEditorVM{
			TargetName: "primary · db: appdb", RowLimit: 500, TimeoutSecs: 5, Ready: true,
			RunHref: "/partial/databases/c1/console/run", SaveScriptsHref: "/scripts", SearchScriptsHref: "/partial/scripts/search",
		},
	}
}

func TestConsoleBody_grantedRendersBannerEditorTokens(t *testing.T) {
	html := renderConsoleBody(t, grantedVM())
	for _, want := range []string{
		`id="console-body"`,
		"SESSION GRANT ACTIVE",
		"dba-oncall",
		"INC-2214",
		"ROW LIMIT 500 · STATEMENT TIMEOUT 5S",
		"RUN ⌘↵",
		`data-console-run`,                                   // ⌘↵ hook, present when Ready
		`hx-post="/partial/databases/c1/console/run"`,        // RUN wired when Ready
		"var(--acc)",                                         // tokens, not legacy classes
		"JetBrains Mono",                                     // mono data font
		`src="/static/js/console.js"`,                        // self-hosted JS
	} {
		if !strings.Contains(html, want) {
			t.Errorf("granted body missing %q", want)
		}
	}
	if strings.Contains(html, "class=\"empty\"") {
		t.Error("must not use legacy component classes")
	}
}

func TestConsoleBody_runInertUntilResolved(t *testing.T) {
	vm := grantedVM()
	vm.Editor.Ready = false
	vm.Editor.TargetName = "(SELECT NODE & DATABASE ABOVE)"
	html := renderConsoleBody(t, vm)
	if strings.Contains(html, "data-console-run") || strings.Contains(html, `hx-post="/partial/databases/c1/console/run"`) {
		t.Error("RUN must be inert (no hx-post / no ⌘↵ hook) until node+database resolve")
	}
	if !strings.Contains(html, "(SELECT NODE &amp; DATABASE ABOVE)") {
		t.Error("editor should prompt for target when not ready")
	}
}

func TestConsoleBody_lockedShowsRequestGate(t *testing.T) {
	vm := ConsoleVM{
		ClusterID: "c2", ClusterName: "staging", Granted: false,
		CapabilitiesHref: "/databases/c2/capabilities",
		Picker:           ConsolePickerVM{ClusterLabel: "staging", GrantChip: "○ NO SESSION GRANT ON THIS CLUSTER"},
	}
	html := renderConsoleBody(t, vm)
	for _, want := range []string{
		"NO SESSION GRANT ON staging",
		"REQUEST SESSION GRANT →",
		`href="/databases/c2/capabilities"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("locked body missing %q", want)
		}
	}
	if strings.Contains(html, "RUN ⌘↵") {
		t.Error("locked state must not render the editor/RUN")
	}
}

func TestConsoleBody_fixedAxisChipIsInert(t *testing.T) {
	vm := grantedVM()
	// Node axis fixed: consoleChips (Task 3) emits a single chip with an empty
	// Href for the locked axis; the templ must render it inert.
	vm.Picker.NodeFixed = true
	vm.Picker.Nodes = []ConsoleChip{{Label: "primary", Href: "", Selected: true}}
	// Free database axis still carries hrefs (which include the lock in prod).
	vm.Picker.Databases = []ConsoleChip{{Label: "appdb", Href: "/partial/databases/c1/console?db=appdb&lockNode=1&node=primary", Selected: true}}
	html := renderConsoleBody(t, vm)
	if !strings.Contains(html, "data-console-chip-fixed") {
		t.Error("fixed node chip must render inert (data-console-chip-fixed, no hx-get)")
	}
	// The inert node chip must NOT be an hx-get link.
	if strings.Contains(html, `hx-get="/partial/databases/c1/console?node=primary"`) {
		t.Error("fixed node chip must not be clickable")
	}
	// The free database chip must carry the lock so a click preserves the fixed node.
	if !strings.Contains(html, "lockNode=1") {
		t.Error("free-axis chip href must preserve the locked axis (lockNode=1)")
	}
}

func TestConsoleBody_resultsPaginationAndExport(t *testing.T) {
	vm := grantedVM()
	vm.HasResult = true
	vm.Result = ConsoleResultVM{
		Columns: []string{"relname", "n_dead_tup", "last_autovacuum"},
		Rows:    [][]string{{"orders", "182,431", "never"}},
		TotalRows: 54, DurationMs: 18.4, Hash: "6c1d…e44",
		PageLabel: "ROWS 1–25 OF 54",
		PrevHref:  "/partial/databases/c1/console?page=0", NextHref: "/partial/databases/c1/console?page=1",
		PrevActive: false, NextActive: true,
		PageSizes: []ConsolePageSize{{Label: "25", Href: "/partial/databases/c1/console?pagesize=25", Selected: true}},
		CopyTSV:   "relname\tn_dead_tup\tlast_autovacuum\norders\t182,431\tnever",
		CsvHref:   "/databases/c1/console/export?format=csv", SqlHref: "/databases/c1/console/export?format=sql",
	}
	html := renderConsoleBody(t, vm)
	for _, want := range []string{
		"T2 READ LOGGED · 6c1d…e44",
		"ROWS 1–25 OF 54",
		"↓ CSV", "↓ SQL", "⧉ COPY",
		`href="/databases/c1/console/export?format=csv"`,
		`id="console-copy-src"`,        // hidden copy payload for console.js
		"data-console-copy",
		"RELNAME", "N_DEAD_TUP", "LAST_AUTOVACUUM", // uppercased header labels
	} {
		if !strings.Contains(html, want) {
			t.Errorf("result body missing %q", want)
		}
	}
}
```
- [ ] **Step 2: Run — expect FAIL (undefined `ConsoleBody`, `ConsoleVM`).**
  `go test ./web/ -run TestConsoleBody`
  Expected: build failure.
- [ ] **Step 3: Implement `web/console.go`** with the view-model structs exactly as in **Interfaces** above (package `web`, doc-comment each type; `ConsoleResultVM` cells are the T2 result — note the comment: "literal-capable; rendered only inside the granted T2 console").
- [ ] **Step 4: Implement `web/console.templ`.** Mirror `Lynceus.dc.html:1591-1745` using tokens. Structure:
```go
package web

import "fmt"

// ConsolePage is the full SQL Console screen (Layout + swap unit).
templ ConsolePage(vm ConsoleVM) {
	@Layout("Lynceus — SQL Console", "SQL Console") {
		@ConsoleBody(vm)
	}
}

// ConsoleBody is the HTMX swap unit — target picker, grant gate, editor,
// results and history. Node/DB chips, pagination and history-restore all
// re-render this whole div (outerHTML). Explicitly the T2 surface.
templ ConsoleBody(vm ConsoleVM) {
	<div id="console-body" style="padding:18px 22px 32px;display:flex;flex-direction:column;gap:14px;max-width:1400px;">
		<div style="display:flex;align-items:center;gap:12px;">
			<span style="font-size:17px;font-weight:600;">SQL Console</span>
			<span style="font-family:var(--font-mono);font-size:10px;color:var(--warnT);border:1px solid var(--warn);padding:0 5px;border-radius:var(--radius-badge);">T2 · SESSION-GRANTED</span>
			<span style="font-family:var(--font-mono);font-size:10.5px;color:var(--faint);letter-spacing:.08em;">DIRECT READ ACCESS TO ONE INSTANCE AT A TIME — EVERY STATEMENT AUDITED</span>
		</div>
		@consolePicker(vm.Picker)
		if vm.Granted {
			@consoleGrantBanner(vm.Grant)
			@consoleEditor(vm)
			@consoleResult(vm)
			@consoleHistory(vm.History)
		} else {
			<div style="border:1px solid var(--warn);border-radius:var(--radius);background:var(--surface);padding:28px 22px;display:flex;flex-direction:column;gap:12px;align-items:center;text-align:center;">
				<span style="font-family:var(--font-mono);font-size:11px;color:var(--warnT);letter-spacing:.08em;">◈ NO SESSION GRANT ON { vm.Picker.ClusterLabel }</span>
				<span style="font-size:12.5px;color:var(--mut);max-width:540px;line-height:1.6;">Direct SQL access is a Tier 2 capability, granted per instance and per session. Request a time-boxed grant — an approver from dba-oncall signs it, and every statement you run is written to the audit log.</span>
				<a href={ templ.SafeURL(vm.CapabilitiesHref) } style="border:1px solid var(--acc);color:var(--acc2);background:var(--accbg);padding:7px 16px;border-radius:var(--radius);font-family:var(--font-mono);font-size:11px;letter-spacing:.06em;">REQUEST SESSION GRANT →</a>
			</div>
		}
		<script src="/static/js/console.js" defer></script>
	</div>
}
```
Then the sub-components (all inline-token styled, faithful to the prototype):
  - `consolePicker(p ConsolePickerVM)` — the `TARGET — <cluster>` header with the grant chip (`color:var(--acc2)` when `p.Granted`, else `var(--faint)`), a `NODE` row iterating `p.Nodes` via `@consoleChip(c)`, a `DATABASE` row iterating `p.Databases` via `@consoleChip(c)`. Render each chip through this helper so a **fixed axis** (empty `Href`, set by `consoleChips` when locked) renders as an inert span, while a free axis renders a clickable HTMX link:
    ```go
    templ consoleChip(c ConsoleChip) {
    	if c.Href == "" {
    		<span data-console-chip-fixed aria-disabled="true" style={ chipStyle(c.Selected) }>{ c.Label }</span>
    	} else {
    		<a href={ templ.SafeURL(c.Href) } hx-get={ c.Href } hx-target="#console-body" hx-swap="outerHTML"
    		   style={ chipStyle(c.Selected) }>{ c.Label }</a>
    	}
    }
    ```
    where `chipStyle(sel bool) string` returns the selected (`var(--acc)` border / `var(--acc2)` color / `var(--accbg)` bg) vs idle (`var(--line)`/`var(--mut)`/`transparent`) inline style. The fixed-axis single-chip case and the lock-carrying hrefs of the free axis are produced by `consoleChips` in Task 3 (README l.67: `node → node fixed, pick database`; `database → database fixed, pick node`); the templ only needs to honour an empty `Href` as "inert".
  - `consoleGrantBanner(g ConsoleGrantVM)` — the `● SESSION GRANT ACTIVE` acc-bordered strip: `GROUP { g.Group }`, `READ-ONLY — WRITES & DDL BLOCKED BY POLICY`, `EXPIRES IN { g.Expires }`, `REF { g.Incident }`, and an `AUDIT TRAIL →` `<a href={ g.AuditHref }>`.
  - `consoleEditor(vm ConsoleVM)` — a `<form id="console-editor-form">` wrapping: the toolbar (`EDITOR — { vm.Editor.TargetName }`, `ROW LIMIT { vm.Editor.RowLimit } · STATEMENT TIMEOUT { vm.Editor.TimeoutSecs }S`), the saved-script **search input** (`hx-*` to `vm.Editor.SearchScriptsHref` — seam to ly-ae6.9) and **`SAVE ▾`** control (`<a href={ vm.Editor.SaveScriptsHref }>` — seam to ly-ae6.9), the RUN control, and the `<textarea name="sql">`. Hidden inputs carry the resolved target: `<input type="hidden" name="node" value={ selectedNodeLabel }>` / `name="db"`. RUN is conditional on `vm.Editor.Ready`:
    ```go
    if vm.Editor.Ready {
      <button type="button" data-console-run hx-post={ vm.Editor.RunHref } hx-include="#console-editor-form"
        hx-target="#console-body" hx-swap="outerHTML" style={ accButtonStyle() }>RUN ⌘↵</button>
    } else {
      <span style={ inertButtonStyle() } aria-disabled="true">RUN ⌘↵</span>
    }
    ```
    The textarea renders the SQL as the element's **text content** (so a restored statement round-trips as `>…SQL…</textarea>`): `<textarea name="sql" spellcheck="false" style="…font-family:var(--font-mono);font-size:12.5px;line-height:1.7;background:var(--raised);…">{ vm.Editor.SQL }</textarea>`.
  - `consoleResult(vm ConsoleVM)` — if `vm.HasResult`: a header row (`RESULT — { vm.Result.TotalRows } ROWS · { fmt.Sprintf("%.1f", vm.Result.DurationMs) } MS`, then `⧉ COPY` (`<span data-console-copy …>`), `↓ CSV` (`<a href={ vm.Result.CsvHref }>`), `↓ SQL` (`<a href={ vm.Result.SqlHref }>`), `T2 READ LOGGED · { vm.Result.Hash }`), a hidden copy payload `<script type="text/plain" id="console-copy-src" data-too-large={ boolAttr(vm.Result.CopyTooLarge) }>{ vm.Result.CopyTSV }</script>` and `<span id="console-copy-note"></span>`, a token-styled `<div style="overflow-x:auto;">` table (uppercased faint letter-spaced headers from `vm.Result.Columns`, tabular-nums mono cells from `vm.Result.Rows`, `min-width:860px`), and the pagination footer (`{ vm.Result.PageLabel }`, `‹ PREV`/`NEXT ›` as `hx-get` links to `Prev/NextHref` targeting `#console-body`, colored by `Prev/NextActive`, then `ROWS / PAGE` with the `vm.Result.PageSizes` chips + `· SAVED TO YOUR PROFILE`). Else the idle card: `NO STATEMENT EXECUTED THIS SESSION — RUN TO SEE RESULTS`.
  - `consoleHistory(rows []ConsoleHistoryRow)` — the `STATEMENT HISTORY — STRICT AUDIT: ACTOR, TARGET & HASH RECORDED PER RUN` card with `CLICK A ROW TO RETRIEVE ITS RESULT`; each row is an `hx-get={ l.Href }` link (target `#console-body`) showing `{ l.TS }`, `{ l.Stmt }` (ellipsized), `{ l.Ms }`, `{ l.Hash }`, `↩`.
  Add the small Go helpers (`chipStyle`, `accButtonStyle`, `inertButtonStyle`, `boolAttr`) in `web/console.go`.
- [ ] **Step 5: Implement `web/static/js/console.js`** (self-hosted; ⌘↵ triggers RUN, click copies the guarded payload):
```js
// SQL Console progressive enhancement. Self-hosted (privacy backbone).
document.addEventListener('keydown', function (e) {
  if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
    var run = document.querySelector('[data-console-run]');
    if (run) { e.preventDefault(); run.click(); }
  }
});
document.addEventListener('click', function (e) {
  var btn = e.target.closest('[data-console-copy]');
  if (!btn) return;
  var src = document.getElementById('console-copy-src');
  var note = document.getElementById('console-copy-note');
  var setNote = function (t) { if (note) note.textContent = t; };
  if (!src || src.getAttribute('data-too-large') === '1') {
    setNote('⚠ RESULT TOO LARGE FOR CLIPBOARD — USE CSV');
    return;
  }
  navigator.clipboard.writeText(src.textContent).then(
    function () { setNote('✓ COPIED'); },
    function () { setNote('⚠ CLIPBOARD BLOCKED — USE CSV'); }
  );
});
```
- [ ] **Step 6: Regenerate templ.**
  `make templ`
  Expected: `web/console_templ.go` created; no diff drift in other `_templ.go`.
- [ ] **Step 7: Run — expect PASS.**
  `go test ./web/ -run TestConsoleBody`
  Expected: all four tests pass.
- [ ] **Step 8: Verify no external hosts / build.**
  `go build ./... && go test ./web/ -run TestLayout`
  Expected: PASS (console.js is same-origin; `TestLayout_NoExternalHosts` unaffected).
- [ ] **Step 9: Commit.**
  `git add web/console.go web/console.templ web/console_templ.go web/static/js/console.js web/console_test.go && git commit -m "console: token-styled SQL Console screen + view-models + console.js (ly-ae6.8)"`

---

### Task 3: Page + target-picker handlers, routes, RUN-inert gating

Wire the console into the server: resolve the cluster → nodes/databases, build the picker, drive selection and grant state, render the page and its `#console-body` partial. Uses testcontainers config DB.

**Files**
- modify `internal/api/server.go` — add console deps to `Server`, construct stubs in `NewServer`, register routes
- create `internal/api/console.go` — `buildConsoleVM`, `handleConsolePage`, `handleConsolePartial`, helper `resolveConsoleTarget`, `consoleResultVM`/`consoleHistoryVM` (empty stubs, filled in Tasks 4/5)
- create `internal/api/console_test.go` — handler tests + `setupConsole` helper

**Interfaces**

Consumes: `store.Config.ListClusters`, `.ListInstances(ctx, clusterID) ([]store.Instance,…)`, `.ListServerStreams(ctx, instanceID) ([]store.ServerStream,…)`; `console.GrantReader`, `console.Sessions`, `console.Executor`; `actorFromContext(r) string` (existing, returns `"dev-admin"`).
Produces:
```go
// package api
func (s *Server) buildConsoleVM(w http.ResponseWriter, r *http.Request) (web.ConsoleVM, bool) // false => cluster not found (404). w is used to persist the rows-per-page cookie (Task 5).
func (s *Server) handleConsolePage(w http.ResponseWriter, r *http.Request)
func (s *Server) handleConsolePartial(w http.ResponseWriter, r *http.Request)
func (s *Server) resolveConsoleTarget(ctx context.Context, clusterID, node, db string) (serverID string, t2Enabled, ok bool, err error)
func consoleResultVM(run console.Run, page, pageSize int, clusterID string) web.ConsoleResultVM // Task 3: returns zero value
func consoleHistoryVM(runs []console.Run, clusterID string) []web.ConsoleHistoryRow             // Task 3: returns nil
```

- [ ] **Step 1: Write the failing test.** Create `internal/api/console_test.go` (reuse `newPGPool`/`newDBPool` from the package's existing test files):
```go
package api_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/api"
	"github.com/dobbo-ca/lynceus/internal/store"
)

// setupConsole seeds cluster(clusterName) → instance "primary" → server
// stream "srv-con" (database "appdb", t2_enabled). Returns the server and
// the cluster id. Name it "orders-prod" for the granted flow, anything else
// for the locked flow (StubGrantReader rule).
func setupConsole(t *testing.T, clusterName string) (*httptest.Server, string) {
	t.Helper()
	ctx := context.Background()
	pool := newPGPool(t)
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate config: %v", err)
	}
	cfg := store.NewConfig(pool)
	cl, err := cfg.CreateCluster(ctx, clusterName)
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}
	inst, err := cfg.CreateInstance(ctx, cl.ID, "primary")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO servers (id, name, instance_id, database_name, t2_enabled) VALUES ($1,$1,$2,$3,true)`,
		"srv-con", inst.ID, "appdb"); err != nil {
		t.Fatalf("seed server stream: %v", err)
	}
	srv := httptest.NewServer(api.NewServer(api.Config{DevAuth: true}, store.NewStats(pool), cfg).Handler())
	t.Cleanup(srv.Close)
	return srv, cl.ID
}

func TestConsolePage_grantedRendersPickerAndBanner(t *testing.T) {
	srv, clusterID := setupConsole(t, "orders-prod")
	resp, err := http.Get(srv.URL + "/databases/" + clusterID + "/console")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	html := readBody(t, resp)
	for _, want := range []string{
		"<!doctype html>",     // full page
		`id="console-body"`,
		"SESSION GRANT ACTIVE", // orders-prod is granted
		"primary",              // node chip
		"appdb",                // database chip
	} {
		if !strings.Contains(strings.ToLower(html), strings.ToLower(want)) {
			t.Errorf("page missing %q", want)
		}
	}
}

func TestConsolePartial_returnsFragmentOnly(t *testing.T) {
	srv, clusterID := setupConsole(t, "orders-prod")
	resp, _ := http.Get(srv.URL + "/partial/databases/" + clusterID + "/console")
	defer func() { _ = resp.Body.Close() }()
	html := readBody(t, resp)
	if strings.Contains(strings.ToLower(html), "<!doctype html>") {
		t.Error("partial returned a full document; want fragment")
	}
	if !strings.Contains(html, `id="console-body"`) {
		t.Error("partial missing swap-target id")
	}
}

func TestConsole_runInertUntilNodeAndDbSelected(t *testing.T) {
	srv, clusterID := setupConsole(t, "orders-prod")
	// No selection: RUN inert.
	r1, _ := http.Get(srv.URL + "/partial/databases/" + clusterID + "/console")
	defer func() { _ = r1.Body.Close() }()
	if h := readBody(t, r1); strings.Contains(h, "data-console-run") {
		t.Error("RUN must be inert with no node/db selected")
	}
	// Both selected: RUN active.
	r2, _ := http.Get(srv.URL + "/partial/databases/" + clusterID + "/console?node=primary&db=appdb")
	defer func() { _ = r2.Body.Close() }()
	h2 := readBody(t, r2)
	if !strings.Contains(h2, "data-console-run") {
		t.Error("RUN must be active once node+db resolve")
	}
	if !strings.Contains(h2, "primary · db: appdb") {
		t.Error("editor should show the resolved target name")
	}
}

func TestConsole_lockedAxisStaysInertAcrossChipClick(t *testing.T) {
	srv, clusterID := setupConsole(t, "orders-prod")

	// Node scope (ly-ae6.3 link ?node=primary&lockNode=1): the node axis is
	// fixed. The database chips must carry lockNode=1 so that a click keeps the
	// node fixed, and the node chip itself must render inert.
	r1, _ := http.Get(srv.URL + "/partial/databases/" + clusterID + "/console?node=primary&lockNode=1")
	defer func() { _ = r1.Body.Close() }()
	h1 := readBody(t, r1)
	if !strings.Contains(h1, "data-console-chip-fixed") {
		t.Error("node scope must render the fixed node axis as an inert chip")
	}
	if !strings.Contains(h1, "lockNode=1") {
		t.Error("database chip hrefs must carry lockNode=1 to preserve the fixed node axis")
	}
	// Simulate the database chip click (node+db+lockNode=1): node stays fixed.
	r2, _ := http.Get(srv.URL + "/partial/databases/" + clusterID + "/console?node=primary&db=appdb&lockNode=1")
	defer func() { _ = r2.Body.Close() }()
	h2 := readBody(t, r2)
	if !strings.Contains(h2, "data-console-chip-fixed") {
		t.Error("after choosing a database the node axis must remain fixed (inert)")
	}

	// Symmetric database scope (?db=appdb&lockDatabase=1): database fixed, pick node.
	r3, _ := http.Get(srv.URL + "/partial/databases/" + clusterID + "/console?db=appdb&lockDatabase=1")
	defer func() { _ = r3.Body.Close() }()
	h3 := readBody(t, r3)
	if !strings.Contains(h3, "data-console-chip-fixed") {
		t.Error("database scope must render the fixed database axis as an inert chip")
	}
	if !strings.Contains(h3, "lockDatabase=1") {
		t.Error("node chip hrefs must carry lockDatabase=1 to preserve the fixed database axis")
	}
}

func TestConsole_lockedClusterShowsRequestGate(t *testing.T) {
	srv, clusterID := setupConsole(t, "staging")
	resp, _ := http.Get(srv.URL + "/databases/" + clusterID + "/console")
	defer func() { _ = resp.Body.Close() }()
	html := readBody(t, resp)
	if !strings.Contains(html, "REQUEST SESSION GRANT →") {
		t.Error("ungranted cluster must show the request-access gate")
	}
	if !strings.Contains(html, "/databases/"+clusterID+"/capabilities") {
		t.Error("request gate must link to the cluster Capabilities page")
	}
	if strings.Contains(html, "RUN ⌘↵</button") || strings.Contains(html, "data-console-run") {
		t.Error("locked state must not render an active editor")
	}
}

func TestConsolePage_unknownClusterIs404(t *testing.T) {
	srv, _ := setupConsole(t, "orders-prod")
	resp, _ := http.Get(srv.URL + "/databases/does-not-exist/console")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}
```
(Note: if `readBody`/`io` already exist in the package's `_test.go` files, drop the duplicate and reuse them — `databases_test.go` defines `body(...)`; either reuse `body` or keep `readBody`, but do not redeclare.)
- [ ] **Step 2: Run — expect FAIL (routes 404 / undefined handlers).**
  `go test ./internal/api/ -run TestConsole`
  Expected: build failure (`s.handleConsolePage` undefined) or 404s.
- [ ] **Step 3: Wire the Server.** In `internal/api/server.go` add the imports (`"github.com/dobbo-ca/lynceus/internal/console"`) and fields:
```go
type Server struct {
	cfg      Config
	stats    store.Stats
	conf     store.Config
	disc     *store.DiscoveredCapabilities
	exec     console.Executor
	grants   console.GrantReader
	sessions *console.Sessions
	mux      *http.ServeMux
}
```
In `NewServer`, set the stub defaults:
```go
	s := &Server{
		cfg:      cfg,
		stats:    stats,
		conf:     conf,
		disc:     store.NewDiscoveredCapabilities(conf.Pool()),
		exec:     console.StubExecutor{},
		grants:   console.StubGrantReader{},
		sessions: console.NewSessions(5),
		mux:      http.NewServeMux(),
	}
```
In `routes()` register:
```go
	s.mux.HandleFunc("GET /databases/{clusterID}/console", s.handleConsolePage)
	s.mux.HandleFunc("GET /partial/databases/{clusterID}/console", s.handleConsolePartial)
	s.mux.HandleFunc("POST /partial/databases/{clusterID}/console/run", s.handleConsoleRun)   // Task 4
	s.mux.HandleFunc("GET /databases/{clusterID}/console/export", s.handleConsoleExport)      // Task 5
```
(Register all four now so the file compiles once Tasks 4/5 add the handlers; if Go rejects references to not-yet-defined `handleConsoleRun`/`handleConsoleExport`, add minimal `http.Error(w, "not implemented", 501)` stubs in `internal/api/console.go` in this task and replace their bodies in Tasks 4/5.)
- [ ] **Step 4: Implement `internal/api/console.go`.** Page + partial handlers and the view-model builder:
```go
package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/dobbo-ca/lynceus/internal/console"
	"github.com/dobbo-ca/lynceus/web"
)

const (
	consoleRowLimit    = 500
	consoleTimeoutSecs = 5
)

func (s *Server) handleConsolePage(w http.ResponseWriter, r *http.Request) {
	vm, ok := s.buildConsoleVM(w, r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.ConsolePage(vm).Render(r.Context(), w)
}

func (s *Server) handleConsolePartial(w http.ResponseWriter, r *http.Request) {
	vm, ok := s.buildConsoleVM(w, r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.ConsoleBody(vm).Render(r.Context(), w)
}

// buildConsoleVM assembles the console screen from the cluster topology,
// the caller's grant, and the session cache. Returns ok=false when the
// cluster id is unknown (404). It takes w so it can persist the per-user
// rows-per-page cookie via s.consolePageSize(w, r) (wired in Task 5); in
// this task w is unused. Go permits an unused parameter — no _ = w needed.
func (s *Server) buildConsoleVM(w http.ResponseWriter, r *http.Request) (web.ConsoleVM, bool) {
	ctx := r.Context()
	clusterID := r.PathValue("clusterID")
	q := r.URL.Query()
	actor := actorFromContext(r)

	clusters, err := s.conf.ListClusters(ctx)
	if err != nil {
		return web.ConsoleVM{}, false
	}
	var clusterName string
	for _, c := range clusters {
		if c.ID == clusterID {
			clusterName = c.Name
			break
		}
	}
	if clusterName == "" {
		return web.ConsoleVM{}, false
	}

	// Nodes + databases for the cluster.
	instances, err := s.conf.ListInstances(ctx, clusterID)
	if err != nil {
		return web.ConsoleVM{}, false
	}
	nodeNames := make([]string, 0, len(instances))
	dbSet := map[string]struct{}{}
	var dbNames []string
	for _, in := range instances {
		nodeNames = append(nodeNames, in.Name)
		streams, err := s.conf.ListServerStreams(ctx, in.ID)
		if err != nil {
			return web.ConsoleVM{}, false
		}
		for _, st := range streams {
			if st.DatabaseName == "" {
				continue
			}
			if _, seen := dbSet[st.DatabaseName]; !seen {
				dbSet[st.DatabaseName] = struct{}{}
				dbNames = append(dbNames, st.DatabaseName)
			}
		}
	}

	node := q.Get("node")
	db := q.Get("db")
	lockNode := q.Get("lockNode") == "1"
	lockDatabase := q.Get("lockDatabase") == "1"

	base := "/partial/databases/" + clusterID + "/console"
	// Both axes get the SAME (node, db, lockNode, lockDatabase) context so every
	// free-axis chip href preserves the locked axis (README l.67).
	nodeChips := consoleChips(base, "node", nodeNames, node, db, lockNode, lockDatabase)
	dbChips := consoleChips(base, "db", dbNames, node, db, lockNode, lockDatabase)

	grant, granted, err := s.grants.ActiveGrant(ctx, clusterID, clusterName, actor)
	if err != nil {
		return web.ConsoleVM{}, false
	}

	vm := web.ConsoleVM{
		ClusterID:        clusterID,
		ClusterName:      clusterName,
		Granted:          granted,
		CapabilitiesHref: "/databases/" + clusterID + "/capabilities",
		Picker: web.ConsolePickerVM{
			ClusterLabel:  clusterName,
			GrantChip:     consoleGrantChip(grant, granted),
			Granted:       granted,
			Nodes:         nodeChips,
			Databases:     dbChips,
			NodeFixed:     lockNode,
			DatabaseFixed: lockDatabase,
		},
	}
	if granted {
		vm.Grant = web.ConsoleGrantVM{
			Group: grant.Group, Incident: grant.Incident, Approver: grant.Approver,
			ReadOnly: grant.ReadOnly, Expires: consoleExpiry(grant),
			AuditHref: "/audit?action=console.query.execute",
		}
		ready := node != "" && db != ""
		vm.Editor = web.ConsoleEditorVM{
			TargetName:        consoleTargetName(node, db, ready),
			SQL:               "",
			RowLimit:          consoleRowLimit,
			TimeoutSecs:       consoleTimeoutSecs,
			Ready:             ready,
			RunHref:           base + "/run",
			SaveScriptsHref:   "/scripts",                 // seam → ly-ae6.9
			SearchScriptsHref: "/partial/scripts/search",  // seam → ly-ae6.9
		}
		// Result + history are wired from the (actor, cluster) session cache.
		// The restore-aware version of this block lands in Task 5 Step 8.
		page, pageSize := consolePage(r), consolePageSize(r)
		if run, ok := s.sessions.Latest(actor, clusterID); ok {
			vm.HasResult = true
			vm.Result = consoleResultVM(run, page, pageSize, clusterID)
		}
		vm.History = consoleHistoryVM(s.sessions.Recent(actor, clusterID), clusterID)
	}
	return vm, true
}

// consoleChipHref builds a picker href that carries the CURRENT selection of
// BOTH axes plus any active lock flags. Preserving lockNode/lockDatabase is
// what keeps a fixed axis fixed after the user clicks a free-axis chip
// (README l.67). url.Values.Encode sorts + escapes; exact ordering is not
// asserted by any test.
func consoleChipHref(base, node, db string, lockNode, lockDatabase bool) string {
	q := url.Values{}
	if node != "" {
		q.Set("node", node)
	}
	if db != "" {
		q.Set("db", db)
	}
	if lockNode {
		q.Set("lockNode", "1")
	}
	if lockDatabase {
		q.Set("lockDatabase", "1")
	}
	if enc := q.Encode(); enc != "" {
		return base + "?" + enc
	}
	return base
}

// consoleChips builds one axis of picker chips (axis == "node" | "db"). Every
// emitted href carries the other axis's current selection AND both lock flags,
// so clicking a free-axis chip preserves the fixed axis. The fixed axis (its
// own lock set) collapses to a single inert chip (empty Href → the templ
// renders a non-clickable span).
func consoleChips(base, axis string, values []string, node, db string, lockNode, lockDatabase bool) []web.ConsoleChip {
	locked, selected := lockNode, node
	if axis == "db" {
		locked, selected = lockDatabase, db
	}
	build := func(v string) web.ConsoleChip {
		n, d := node, db
		if axis == "node" {
			n = v
		} else {
			d = v
		}
		c := web.ConsoleChip{Label: v, Href: consoleChipHref(base, n, d, lockNode, lockDatabase), Selected: v == selected}
		if locked {
			c.Href = "" // fixed axis: inert, no navigation
		}
		return c
	}
	if locked && selected != "" {
		return []web.ConsoleChip{build(selected)}
	}
	out := make([]web.ConsoleChip, 0, len(values))
	for _, v := range values {
		out = append(out, build(v))
	}
	return out
}

func consoleGrantChip(g console.SessionGrant, granted bool) string {
	if !granted {
		return "○ NO SESSION GRANT ON THIS CLUSTER"
	}
	return "● SESSION GRANT ACTIVE — " + g.Group + " · READ-ONLY · EXPIRES " + consoleExpiry(g)
}

func consoleExpiry(g console.SessionGrant) string {
	d := g.ExpiresAt.Sub(g.GrantedAt)
	if d < 0 {
		d = 0
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	return fmt.Sprintf("%dH %dM", h, m)
}

func consoleTargetName(node, db string, ready bool) string {
	if !ready {
		return "(SELECT NODE & DATABASE ABOVE)"
	}
	return node + " · db: " + db
}

func consolePage(r *http.Request) int {
	n, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if n < 0 {
		n = 0
	}
	return n
}

func (s *Server) resolveConsoleTarget(ctx context.Context, clusterID, node, db string) (serverID string, t2Enabled, ok bool, err error) {
	instances, err := s.conf.ListInstances(ctx, clusterID)
	if err != nil {
		return "", false, false, err
	}
	for _, in := range instances {
		if in.Name != node {
			continue
		}
		streams, err := s.conf.ListServerStreams(ctx, in.ID)
		if err != nil {
			return "", false, false, err
		}
		for _, st := range streams {
			if st.DatabaseName == db {
				return st.ServerID, st.T2Enabled, true, nil
			}
		}
	}
	return "", false, false, nil
}
```
Add the Task-3 empty placeholders (real bodies land in Tasks 4/5), and the pagesize reader (finalized in Task 5):
```go
// consoleResultVM is filled in Task 4/5.
func consoleResultVM(run console.Run, page, pageSize int, clusterID string) web.ConsoleResultVM {
	return web.ConsoleResultVM{}
}

// consoleHistoryVM is filled in Task 4.
func consoleHistoryVM(runs []console.Run, clusterID string) []web.ConsoleHistoryRow {
	return nil
}

// consolePageSize is finalized in Task 5 (cookie persistence); default 25 now.
func consolePageSize(r *http.Request) int {
	if n, err := strconv.Atoi(r.URL.Query().Get("pagesize")); err == nil && consoleValidPageSize(n) {
		return n
	}
	return 25
}

func consoleValidPageSize(n int) bool {
	return n == 10 || n == 25 || n == 50 || n == 100
}
```
(Task 3 imports exactly what it uses — `context fmt net/http net/url strconv` + `console web`. `strings` is deliberately NOT imported here; Task 4 adds it when `handleConsoleRun`/`consoleHistoryVM` first use it. Keep the import list minimal so every commit compiles.)
- [ ] **Step 5: Run — expect PASS.**
  `go test ./internal/api/ -run TestConsole`
  Expected: the five Task-3 tests pass (page, partial, inert gating, locked gate, 404).
- [ ] **Step 6: Full build.**
  `go build ./... && go vet ./internal/api/... ./internal/console/...`
  Expected: clean.
- [ ] **Step 7: Commit.**
  `git add internal/api/server.go internal/api/console.go internal/api/console_test.go && git commit -m "console: page/partial handlers, target picker, grant gate, RUN-inert gating (ly-ae6.8)"`

---

### Task 4: RUN handler — execute + strict per-statement audit + session cache + history

The RUN endpoint: refuse without a grant; execute via the stub; write a fail-closed `console.query.execute` T2 audit row via `AppendAuditReturning`; cache the run; re-render the body with the result + history. Fills `consoleResultVM`/`consoleHistoryVM`.

**Files**
- modify `internal/api/console.go` — `handleConsoleRun`, real `consoleResultVM` (single page for now) + `consoleHistoryVM`, `shortHash`, `sha256Hex`
- modify `internal/api/console_test.go` — RUN happy-path (asserts audit row) + locked-refusal tests

**Interfaces**

Consumes: `store.Config.AppendAuditReturning(ctx, store.AuditEntry) (store.AuditRecord, error)` (returns `.RowHash []byte`, `.At time.Time`), `store.Config.ListAudit`, `console.Executor.Execute`, `console.Sessions.Append`.
Produces:
```go
func (s *Server) handleConsoleRun(w http.ResponseWriter, r *http.Request)
func shortHash(h []byte) string  // "6c1d…e44"
func sha256Hex(s string) string
```

- [ ] **Step 1: Write the failing test.** Append to `internal/api/console_test.go`:
```go
func TestConsoleRun_executesAuditsAndShowsResult(t *testing.T) {
	srv, clusterID := setupConsole(t, "orders-prod")
	form := url.Values{"sql": {"SELECT relname FROM pg_stat_user_tables"}, "node": {"primary"}, "db": {"appdb"}}
	resp, err := http.PostForm(srv.URL+"/partial/databases/"+clusterID+"/console/run", form)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	html := readBody(t, resp)
	for _, want := range []string{"T2 READ LOGGED ·", "RELNAME", "STATEMENT HISTORY"} {
		if !strings.Contains(html, want) {
			t.Errorf("run body missing %q", want)
		}
	}
	// The audit_log carries exactly one console.query.execute T2 row.
	recs, err := store.NewConfig(consolePool(t, srv)).ListAudit(context.Background(), store.AuditFilter{Action: "console.query.execute"})
	_ = recs
	_ = err
}
```
(The audit assertion needs the pool. Refactor `setupConsole` to also return the `*pgxpool.Pool` so the test can query `ListAudit` directly. Replace the placeholder above with:)
```go
func TestConsoleRun_executesAuditsAndShowsResult(t *testing.T) {
	srv, clusterID, pool := setupConsole(t, "orders-prod")
	form := url.Values{"sql": {"SELECT relname FROM pg_stat_user_tables"}, "node": {"primary"}, "db": {"appdb"}}
	resp, err := http.PostForm(srv.URL+"/partial/databases/"+clusterID+"/console/run", form)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	html := readBody(t, resp)
	if !strings.Contains(html, "T2 READ LOGGED ·") || !strings.Contains(html, "STATEMENT HISTORY") {
		t.Fatalf("run body missing result/history markers")
	}
	recs, err := store.NewConfig(pool).ListAudit(context.Background(), store.AuditFilter{Action: "console.query.execute"})
	if err != nil {
		t.Fatalf("ListAudit: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("audit rows = %d, want 1", len(recs))
	}
	r0 := recs[0]
	if r0.Actor != "dev-admin" || r0.DataTier != 2 || r0.ServerID != "srv-con" {
		t.Errorf("audit row = %+v, want actor=dev-admin tier=2 server=srv-con", r0)
	}
	if !strings.Contains(string(r0.Detail), "console.query.execute") && !strings.Contains(string(r0.Detail), "statement_sha256") {
		t.Errorf("audit detail missing structural keys: %s", r0.Detail)
	}
}

func TestConsoleRun_refusedWithoutGrant_writesNoAudit(t *testing.T) {
	srv, clusterID, pool := setupConsole(t, "staging") // ungranted
	form := url.Values{"sql": {"SELECT 1"}, "node": {"primary"}, "db": {"appdb"}}
	resp, _ := http.PostForm(srv.URL+"/partial/databases/"+clusterID+"/console/run", form)
	defer func() { _ = resp.Body.Close() }()
	html := readBody(t, resp)
	if !strings.Contains(html, "REQUEST SESSION GRANT →") {
		t.Error("ungranted RUN must return the request-access gate, not results")
	}
	recs, _ := store.NewConfig(pool).ListAudit(context.Background(), store.AuditFilter{Action: "console.query.execute"})
	if len(recs) != 0 {
		t.Errorf("ungranted RUN wrote %d audit rows, want 0", len(recs))
	}
}
```
Update `setupConsole` to return `(*httptest.Server, string, *pgxpool.Pool)` and add `import ("net/url"; "github.com/jackc/pgx/v5/pgxpool")` to the test file. Then fix EVERY Task-3 call site that used the 2-value form to `srv, clusterID, _ := setupConsole(...)` — namely `TestConsolePage_grantedRendersPickerAndBanner`, `TestConsolePartial_returnsFragmentOnly`, `TestConsole_runInertUntilNodeAndDbSelected`, `TestConsole_lockedAxisStaysInertAcrossChipClick`, `TestConsole_lockedClusterShowsRequestGate`, `TestConsolePage_unknownClusterIs404`.
- [ ] **Step 2: Run — expect FAIL (`handleConsoleRun` is the 501 stub / undefined).**
  `go test ./internal/api/ -run TestConsoleRun`
  Expected: 501 body / assertion failures.
- [ ] **Step 3: Implement `handleConsoleRun`** in `internal/api/console.go` (replace the Task-3 stub):
Task 4 grows the `internal/api/console.go` import block to (Task 3 set + `crypto/sha256`, `encoding/hex`, `strings`, `store`):
```go
import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/dobbo-ca/lynceus/internal/console"
	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/dobbo-ca/lynceus/web"
)

// handleConsoleRun executes one statement against the resolved target and
// records a strict, fail-closed T2 audit row before returning any result:
// execute → audit → (audit error: discard, error) → cache + render. A run
// against a cluster with no active grant does not execute and does not audit.
func (s *Server) handleConsoleRun(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	clusterID := r.PathValue("clusterID")
	actor := actorFromContext(r)

	clusters, err := s.conf.ListClusters(ctx)
	if err != nil {
		http.Error(w, "load clusters", http.StatusInternalServerError)
		return
	}
	var clusterName string
	for _, c := range clusters {
		if c.ID == clusterID {
			clusterName = c.Name
			break
		}
	}
	if clusterName == "" {
		http.NotFound(w, r)
		return
	}

	// Grant gate — no grant, no execution, no audit; re-render the body
	// (which shows the request-access gate).
	_, granted, err := s.grants.ActiveGrant(ctx, clusterID, clusterName, actor)
	if err != nil {
		http.Error(w, "grant check", http.StatusInternalServerError)
		return
	}
	if !granted {
		s.renderConsoleBody(w, r)
		return
	}

	node := r.FormValue("node")
	db := r.FormValue("db")
	sql := strings.TrimSpace(r.FormValue("sql"))
	serverID, _, ok, err := s.resolveConsoleTarget(ctx, clusterID, node, db)
	if err != nil {
		http.Error(w, "resolve target", http.StatusInternalServerError)
		return
	}
	if !ok || sql == "" {
		// Target not fully resolved or empty statement — re-render (RUN inert).
		s.renderConsoleBody(w, r)
		return
	}

	res, err := s.exec.Execute(ctx, console.Statement{
		ClusterID: clusterID, ClusterName: clusterName, Node: node, Database: db,
		ServerID: serverID, SQL: sql, RowLimit: consoleRowLimit, TimeoutSecs: consoleTimeoutSecs, Actor: actor,
	})
	if err != nil {
		http.Error(w, "execute", http.StatusInternalServerError)
		return
	}

	// Strict audit BEFORE returning results — fail closed. Detail is a T2
	// governance artifact (statement text is permitted in the audit_log).
	rec, err := s.conf.AppendAuditReturning(ctx, store.AuditEntry{
		Actor: actor, Action: "console.query.execute", ServerID: serverID, DataTier: 2,
		Detail: map[string]any{
			"target_node":      node,
			"target_database":  db,
			"statement":        sql,
			"statement_sha256": sha256Hex(sql),
			"duration_ms":      res.DurationMs,
			"row_count":        res.RowCount(),
			"row_limit":        consoleRowLimit,
			"timeout_secs":     consoleTimeoutSecs,
			"read_only":        true,
		},
	})
	if err != nil {
		// Fail closed: the run happened but could not be recorded — withhold results.
		http.Error(w, "audit failed — result withheld", http.StatusInternalServerError)
		return
	}

	// ID is the FULL hex (URL-safe lookup key for restore/Get); ShortHash is
	// display-only. Scope the cache entry to (actor, clusterID).
	s.sessions.Append(actor, clusterID, console.Run{
		ID: hex.EncodeToString(rec.RowHash), ShortHash: shortHash(rec.RowHash), ClusterID: clusterID,
		At: rec.At, SQL: sql, Node: node, Database: db,
		Result: res, DurationMs: res.DurationMs,
	})
	// The freshly-cached run drives the re-render; carry the selection so the
	// picker stays resolved.
	r2 := r.Clone(ctx)
	q := r2.URL.Query()
	q.Set("node", node)
	q.Set("db", db)
	r2.URL.RawQuery = q.Encode()
	vm, _ := s.buildConsoleVM(w, r2)
	vm.Editor.SQL = sql // in Task 4 buildConsoleVM does not set Editor.SQL; Task 5's Latest path does, making this redundant (harmless).
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.ConsoleBody(vm).Render(ctx, w)
}

// renderConsoleBody re-renders the swap unit for the current request.
func (s *Server) renderConsoleBody(w http.ResponseWriter, r *http.Request) {
	vm, ok := s.buildConsoleVM(w, r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.ConsoleBody(vm).Render(r.Context(), w)
}

// shortHash renders a display-only abbreviation ("6c1d…e44"). NEVER use it as
// a lookup key — the multibyte ellipsis is not URL/attribute round-trip safe;
// use the full hex (Run.ID) for restore tokens and sessions.Get.
func shortHash(h []byte) string {
	s := hex.EncodeToString(h)
	if len(s) < 7 {
		return s
	}
	return s[:4] + "…" + s[len(s)-3:]
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
```
- [ ] **Step 4: Implement real `consoleResultVM` (single page for now) + `consoleHistoryVM`** (replace the Task-3 stubs). Pagination refinement (prev/next math, pagesize chips, export/copy) is completed in Task 5; here render the full result as page 0:
```go
func consoleResultVM(run console.Run, page, pageSize int, clusterID string) web.ConsoleResultVM {
	res := run.Result
	total := res.RowCount()
	// Task 5 slices by page/pageSize; here show the whole set.
	upper := total
	label := fmt.Sprintf("ROWS %d–%d OF %d", min1(total), upper, total)
	base := "/partial/databases/" + clusterID + "/console"
	return web.ConsoleResultVM{
		Columns:    res.Columns,
		Rows:       res.Rows,
		TotalRows:  total,
		DurationMs: res.DurationMs,
		Hash:       run.ShortHash, // display-only short form
		PageLabel:  label,
		PrevHref:   base + "?page=0",
		NextHref:   base + "?page=0",
		CsvHref:    "/databases/" + clusterID + "/console/export?format=csv",
		SqlHref:    "/databases/" + clusterID + "/console/export?format=sql",
	}
}

func min1(n int) int {
	if n == 0 {
		return 0
	}
	return 1
}

func consoleHistoryVM(runs []console.Run, clusterID string) []web.ConsoleHistoryRow {
	base := "/partial/databases/" + clusterID + "/console"
	out := make([]web.ConsoleHistoryRow, 0, len(runs))
	for _, r := range runs {
		// restore token is the FULL hex Run.ID (URL-safe); node/db are DB
		// identifiers. Hash shows the short form.
		href := fmt.Sprintf("%s?restore=%s&node=%s&db=%s", base,
			url.QueryEscape(r.ID), url.QueryEscape(r.Node), url.QueryEscape(r.Database))
		out = append(out, web.ConsoleHistoryRow{
			TS:   r.At.UTC().Format("15:04:05Z"),
			Stmt: strings.Join(strings.Fields(r.SQL), " "),
			Ms:   fmt.Sprintf("%.1f ms", r.DurationMs),
			Hash: r.ShortHash,
			Href: href,
		})
	}
	return out
}
```
- [ ] **Step 5: Run — expect PASS.**
  `go test ./internal/api/ -run TestConsoleRun`
  Expected: both RUN tests pass (audit row written for granted; none for locked).
- [ ] **Step 6: Regression + build.**
  `go build ./... && go test ./internal/api/ -run TestConsole`
  Expected: all Task-3 + Task-4 console tests green.
- [ ] **Step 7: Commit.**
  `git add internal/api/console.go internal/api/console_test.go && git commit -m "console: RUN executes + strict console.query.execute T2 audit + session cache (ly-ae6.8)"`

---

### Task 5: Pagination + per-user rows-per-page + CSV/SQL export + copy guard + history restore

Complete the results surface: real prev/next paging, pagesize chips persisted per user via cookie, CSV/SQL downloads over the cached full result, the size-guarded copy payload, and click-to-retrieve history restore.

**Files**
- modify `internal/console/console.go` (or new `internal/console/export.go`) — `CopyTSV`, `CSV`, `SQLInserts`, `ConsoleCopyGuardBytes`
- create `internal/console/export_test.go` — export/guard unit tests
- modify `internal/api/console.go` — real `consoleResultVM` (paging + pagesize chips + copy payload), `consolePageSize` (cookie), `handleConsoleExport`, restore handling in `buildConsoleVM`
- modify `internal/api/console_test.go` — pagination/pagesize/export/restore tests

**Interfaces**

Produces:
```go
// package console
const ConsoleCopyGuardBytes = 500000
func CopyTSV(res Result) (tsv string, tooLarge bool) // "" + true when > guard
func CSV(res Result) string                          // RFC4180-quoted
func SQLInserts(res Result, table string) string      // INSERT INTO <table> (...) VALUES (...);
// package api
func (s *Server) handleConsoleExport(w http.ResponseWriter, r *http.Request)
```

- [ ] **Step 1: Write the failing export/guard unit test.** Create `internal/console/export_test.go`:
```go
package console

import (
	"strings"
	"testing"
)

func sampleResult() Result {
	return Result{
		Columns: []string{"relname", "n_dead_tup", "last_autovacuum"},
		Rows:    [][]string{{"orders", "182,431", "never"}, {"events, live", "12", "2026-07-10 01:02:12Z"}},
	}
}

func TestCopyTSV_underGuard(t *testing.T) {
	tsv, tooLarge := CopyTSV(sampleResult())
	if tooLarge {
		t.Fatal("small result should not be too large")
	}
	if !strings.HasPrefix(tsv, "relname\tn_dead_tup\tlast_autovacuum\n") {
		t.Errorf("tsv header wrong: %q", tsv)
	}
	if !strings.Contains(tsv, "orders\t182,431\tnever") {
		t.Errorf("tsv body wrong: %q", tsv)
	}
}

func TestCopyTSV_overGuardReturnsTooLarge(t *testing.T) {
	big := Result{Columns: []string{"c"}}
	for i := 0; i < ConsoleCopyGuardBytes/4+10; i++ {
		big.Rows = append(big.Rows, []string{"xxxx"})
	}
	tsv, tooLarge := CopyTSV(big)
	if !tooLarge || tsv != "" {
		t.Errorf("over-guard should be ('', true), got (%d bytes, %v)", len(tsv), tooLarge)
	}
}

func TestCSV_quotesFieldsWithCommas(t *testing.T) {
	csv := CSV(sampleResult())
	if !strings.Contains(csv, `"events, live"`) {
		t.Errorf("comma field not quoted: %q", csv)
	}
}

func TestSQLInserts_emitsInsertPerRow(t *testing.T) {
	sql := SQLInserts(sampleResult(), "result")
	if strings.Count(sql, "INSERT INTO result") != 2 {
		t.Errorf("want 2 INSERTs, got: %q", sql)
	}
}
```
- [ ] **Step 2: Run — expect FAIL (undefined `CopyTSV`, etc.).**
  `go test ./internal/console/ -run 'TestCopyTSV|TestCSV|TestSQLInserts'`
  Expected: build failure.
- [ ] **Step 3: Implement `internal/console/export.go`:**
```go
package console

import (
	"strconv"
	"strings"
)

// ConsoleCopyGuardBytes caps the clipboard payload embedded in the page
// (mirrors the prototype's 500000 guard); larger results must use CSV.
const ConsoleCopyGuardBytes = 500000

// CopyTSV renders the full result as tab-separated text for clipboard copy,
// or ("", true) when it would exceed the guard.
func CopyTSV(res Result) (string, bool) {
	var b strings.Builder
	b.WriteString(strings.Join(res.Columns, "\t"))
	b.WriteByte('\n')
	for i, row := range res.Rows {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(strings.Join(row, "\t"))
	}
	if b.Len() > ConsoleCopyGuardBytes {
		return "", true
	}
	return b.String(), false
}

// CSV renders the full result as RFC4180 CSV.
func CSV(res Result) string {
	var b strings.Builder
	writeRow := func(cells []string) {
		for i, c := range cells {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(csvField(c))
		}
		b.WriteByte('\n')
	}
	writeRow(res.Columns)
	for _, row := range res.Rows {
		writeRow(row)
	}
	return b.String()
}

func csvField(s string) string {
	if strings.ContainsAny(s, ",\"\n") {
		return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
	}
	return s
}

// SQLInserts renders the full result as one INSERT statement per row.
func SQLInserts(res Result, table string) string {
	cols := strings.Join(res.Columns, ", ")
	var b strings.Builder
	for _, row := range res.Rows {
		b.WriteString("INSERT INTO ")
		b.WriteString(table)
		b.WriteString(" (")
		b.WriteString(cols)
		b.WriteString(") VALUES (")
		for i, c := range row {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(sqlLiteral(c))
		}
		b.WriteString(");\n")
	}
	return b.String()
}

func sqlLiteral(s string) string {
	if s == "never" || s == "" {
		return "NULL"
	}
	if _, err := strconv.ParseInt(strings.ReplaceAll(s, ",", ""), 10, 64); err == nil {
		return strings.ReplaceAll(s, ",", "")
	}
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
```
- [ ] **Step 4: Run the console export tests — expect PASS.**
  `go test ./internal/console/...`
  Expected: `ok`.
- [ ] **Step 5: Write the failing handler tests.** Append to `internal/api/console_test.go`:
```go
func runOnce(t *testing.T, srv *httptest.Server, clusterID string) {
	t.Helper()
	form := url.Values{"sql": {"SELECT relname FROM pg_stat_user_tables"}, "node": {"primary"}, "db": {"appdb"}}
	resp, err := http.PostForm(srv.URL+"/partial/databases/"+clusterID+"/console/run", form)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	_ = resp.Body.Close()
}

func TestConsole_paginationAndPageSizeCookie(t *testing.T) {
	srv, clusterID, _ := setupConsole(t, "orders-prod")
	runOnce(t, srv, clusterID) // 54 rows cached

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	// Default page size 25 → "ROWS 1–25 OF 54".
	r1, _ := client.Get(srv.URL + "/partial/databases/" + clusterID + "/console?node=primary&db=appdb")
	defer func() { _ = r1.Body.Close() }()
	if !strings.Contains(readBody(t, r1), "ROWS 1–25 OF 54") {
		t.Error("default page label wrong")
	}
	// Choose 50 → sets cookie + "ROWS 1–50 OF 54".
	r2, _ := client.Get(srv.URL + "/partial/databases/" + clusterID + "/console?node=primary&db=appdb&pagesize=50")
	defer func() { _ = r2.Body.Close() }()
	if !strings.Contains(readBody(t, r2), "ROWS 1–50 OF 54") {
		t.Error("pagesize=50 not applied")
	}
	// Subsequent request without the param uses the persisted 50.
	r3, _ := client.Get(srv.URL + "/partial/databases/" + clusterID + "/console?node=primary&db=appdb")
	defer func() { _ = r3.Body.Close() }()
	if !strings.Contains(readBody(t, r3), "ROWS 1–50 OF 54") {
		t.Error("rows-per-page not persisted per user (cookie)")
	}
	// Page 1 at size 50 → "ROWS 51–54 OF 54".
	r4, _ := client.Get(srv.URL + "/partial/databases/" + clusterID + "/console?node=primary&db=appdb&page=1")
	defer func() { _ = r4.Body.Close() }()
	if !strings.Contains(readBody(t, r4), "ROWS 51–54 OF 54") {
		t.Error("second page label wrong")
	}
}

func TestConsoleExport_csvAndSql(t *testing.T) {
	srv, clusterID, _ := setupConsole(t, "orders-prod")
	runOnce(t, srv, clusterID)
	csv, _ := http.Get(srv.URL + "/databases/" + clusterID + "/console/export?format=csv")
	defer func() { _ = csv.Body.Close() }()
	if cd := csv.Header.Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Errorf("csv missing attachment disposition: %q", cd)
	}
	if b := readBody(t, csv); !strings.HasPrefix(b, "relname,n_dead_tup,last_autovacuum") {
		t.Errorf("csv header wrong: %q", b[:40])
	}
	sqlResp, _ := http.Get(srv.URL + "/databases/" + clusterID + "/console/export?format=sql")
	defer func() { _ = sqlResp.Body.Close() }()
	if b := readBody(t, sqlResp); !strings.Contains(b, "INSERT INTO result") {
		t.Error("sql export missing INSERTs")
	}
}

// restoreTokens extracts every `restore=<fullhex>` token from rendered history
// HTML, in document order (newest-first). templ escapes `&` as `&amp;` in
// attribute values, so a token ends at the first `&`, `"` or `'`.
func restoreTokens(html string) []string {
	var toks []string
	s := html
	for {
		i := strings.Index(s, "restore=")
		if i < 0 {
			break
		}
		s = s[i+len("restore="):]
		end := strings.IndexAny(s, "&\"'")
		if end < 0 {
			break
		}
		toks = append(toks, s[:end])
		s = s[end:]
	}
	return toks
}

func TestConsole_historyRestoreLoadsStatement(t *testing.T) {
	srv, clusterID, _ := setupConsole(t, "orders-prod")
	// Two distinct runs, oldest first.
	for _, q := range []string{"SELECT 1", "SELECT 2"} {
		form := url.Values{"sql": {q}, "node": {"primary"}, "db": {"appdb"}}
		resp, _ := http.PostForm(srv.URL+"/partial/databases/"+clusterID+"/console/run", form)
		_ = resp.Body.Close()
	}
	// History is newest-first → tokens are [SELECT 2, SELECT 1].
	list, _ := http.Get(srv.URL + "/partial/databases/" + clusterID + "/console?node=primary&db=appdb")
	defer func() { _ = list.Body.Close() }()
	toks := restoreTokens(readBody(t, list))
	if len(toks) < 2 {
		t.Fatalf("want >=2 restore tokens in history, got %d", len(toks))
	}
	oldest := toks[len(toks)-1] // the run whose SQL was "SELECT 1"

	// GET the restore URL for the OLDER run — the editor must reload exactly
	// that statement (SELECT 1), not the newer one. This drives the real
	// restore path: q.Get("restore") → sessions.Get(actor, clusterID, id) → vm.Editor.SQL.
	rr, _ := http.Get(srv.URL + "/partial/databases/" + clusterID + "/console?node=primary&db=appdb&restore=" + oldest)
	defer func() { _ = rr.Body.Close() }()
	rh := readBody(t, rr)
	if !strings.Contains(rh, "SELECT 1</textarea>") {
		t.Error("restore must reload the older statement (SELECT 1) into the editor")
	}
	if strings.Contains(rh, "SELECT 2</textarea>") {
		t.Error("restoring the older run must not reload the newer statement into the editor")
	}
}
```
Add import `"net/http/cookiejar"` to the test file (used by `TestConsole_paginationAndPageSizeCookie`). The restore test now GETs `?restore=<fullhex>` and asserts the editor textarea reloads the specific older statement (`SELECT 1</textarea>`) and not the newer one — a genuine assertion over the restore code path, replacing the old byte-offset extraction that discarded its token.
- [ ] **Step 6: Run — expect FAIL** (pagination label always full set; no cookie; export is 501).
  `go test ./internal/api/ -run 'TestConsole_pagination|TestConsoleExport|TestConsole_historyRestore'`
  Expected: assertion failures / 501.
- [ ] **Step 7: Implement real paging in `consoleResultVM`** (replace the Task-4 body):
```go
func consoleResultVM(run console.Run, page, pageSize int, clusterID string) web.ConsoleResultVM {
	res := run.Result
	total := res.RowCount()
	if pageSize <= 0 {
		pageSize = 25
	}
	pageCount := (total + pageSize - 1) / pageSize
	if pageCount < 1 {
		pageCount = 1
	}
	if page < 0 {
		page = 0
	}
	if page > pageCount-1 {
		page = pageCount - 1
	}
	start := page * pageSize
	end := start + pageSize
	if end > total {
		end = total
	}
	pageRows := res.Rows[start:end]

	base := "/partial/databases/" + clusterID + "/console?node=" + run.Node + "&db=" + run.Database
	lower := 0
	if total > 0 {
		lower = start + 1
	}
	tsv, tooLarge := console.CopyTSV(res)
	sizes := make([]web.ConsolePageSize, 0, 4)
	for _, n := range []int{10, 25, 50, 100} {
		sizes = append(sizes, web.ConsolePageSize{
			Label:    strconv.Itoa(n),
			Href:     fmt.Sprintf("%s&pagesize=%d", base, n),
			Selected: n == pageSize,
		})
	}
	return web.ConsoleResultVM{
		Columns:    res.Columns,
		Rows:       pageRows,
		TotalRows:  total,
		DurationMs: res.DurationMs,
		Hash:       run.ShortHash, // display-only short form; full hex lives in Run.ID
		PageLabel:  fmt.Sprintf("ROWS %d–%d OF %d", lower, end, total),
		PrevHref:   fmt.Sprintf("%s&page=%d", base, max0(page-1)),
		NextHref:   fmt.Sprintf("%s&page=%d", base, minInt(page+1, pageCount-1)),
		PrevActive: page > 0,
		NextActive: page < pageCount-1,
		PageSizes:  sizes,
		CopyTSV:    tsv,
		CopyTooLarge: tooLarge,
		CsvHref:    "/databases/" + clusterID + "/console/export?format=csv",
		SqlHref:    "/databases/" + clusterID + "/console/export?format=sql",
	}
}

func max0(n int) int { if n < 0 { return 0 }; return n }
func minInt(a, b int) int { if a < b { return a }; return b }
```
Remove the now-unused `min1` helper.
- [ ] **Step 8: Implement pagesize cookie persistence** — replace `consolePageSize`:
```go
const consolePageSizeCookie = "lynceus.console.pagesize"

// consolePageSize resolves rows-per-page from (1) a ?pagesize= override,
// which it persists to a per-user cookie, then (2) the cookie, else 25.
func (s *Server) consolePageSize(w http.ResponseWriter, r *http.Request) int {
	if raw := r.URL.Query().Get("pagesize"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && consoleValidPageSize(n) {
			http.SetCookie(w, &http.Cookie{Name: consolePageSizeCookie, Value: raw, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode})
			return n
		}
	}
	if ck, err := r.Cookie(consolePageSizeCookie); err == nil {
		if n, err := strconv.Atoi(ck.Value); err == nil && consoleValidPageSize(n) {
			return n
		}
	}
	return 25
}
```
`buildConsoleVM(w, r)` already takes `w` (threaded since Task 3), so there is **no signature refactor** in this task. Two edits inside `buildConsoleVM`:

1. Swap the standalone `consolePageSize(r)` for the cookie-persisting method `s.consolePageSize(w, r)` (defined above), and delete the now-unused standalone `consolePageSize`/leave `consoleValidPageSize` in place.
2. Replace the Task-3 result-wiring block (the `page, pageSize := consolePage(r), consolePageSize(r)` / `if run, ok := s.sessions.Latest(actor, clusterID)` / `vm.History = consoleHistoryVM(...)` lines) with this single restore-aware block. All three cache reads pass `clusterID` so a restore/latest/history read cannot cross scope:
```go
	// Restore a cached run by full-hex id (history click) — else the latest run
	// for THIS (actor, cluster). Get/Latest/Recent are cluster-scoped.
	page := consolePage(r)
	pageSize := s.consolePageSize(w, r)
	var current console.Run
	var haveCurrent bool
	if id := q.Get("restore"); id != "" {
		current, haveCurrent = s.sessions.Get(actor, clusterID, id)
	} else {
		current, haveCurrent = s.sessions.Latest(actor, clusterID)
	}
	if haveCurrent {
		vm.HasResult = true
		vm.Result = consoleResultVM(current, page, pageSize, clusterID)
		vm.Editor.SQL = current.SQL // reload the restored/last statement into the editor
	}
	vm.History = consoleHistoryVM(s.sessions.Recent(actor, clusterID), clusterID)
```
This is now the **single** result-wiring path (the Task-4 `Latest` block is gone). Because it sets `vm.Editor.SQL = current.SQL`, the explicit `vm.Editor.SQL = sql` in `handleConsoleRun` becomes redundant — leave it (harmless) or drop it.
- [ ] **Step 9: Implement `handleConsoleExport`** in `internal/api/console.go`:
```go
// handleConsoleExport streams the caller's latest cached result FOR THIS
// CLUSTER as a CSV or SQL-INSERT download. Reads from the (actor, cluster)
// session cache — the clusterID from the URL scopes the read so an export
// under one cluster cannot return another cluster's run (no re-execution, no
// re-audit — the run was already audited when it ran).
func (s *Server) handleConsoleExport(w http.ResponseWriter, r *http.Request) {
	clusterID := r.PathValue("clusterID")
	run, ok := s.sessions.Latest(actorFromContext(r), clusterID)
	if !ok {
		http.Error(w, "no result to export", http.StatusNotFound)
		return
	}
	var body, filename, ctype string
	switch r.URL.Query().Get("format") {
	case "sql":
		body, filename, ctype = console.SQLInserts(run.Result, "result"), "lynceus-result.sql", "application/sql"
	default:
		body, filename, ctype = console.CSV(run.Result), "lynceus-result.csv", "text/csv"
	}
	w.Header().Set("Content-Type", ctype+"; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename="+filename)
	_, _ = w.Write([]byte(body))
}
```
- [ ] **Step 10: Regenerate templ if any VM field usage changed** (the `CopyTooLarge` attr and pagesize chips already exist from Task 2; no templ change expected). Then run:
  `make templ && go build ./...`
  Expected: clean build.
- [ ] **Step 11: Run — expect PASS.**
  `go test ./internal/api/ -run 'TestConsole' && go test ./internal/console/...`
  Expected: all console tests green.
- [ ] **Step 12: Full regression.**
  `go test ./web/... ./internal/api/... ./internal/console/...`
  Expected: PASS.
- [ ] **Step 13: Commit.**
  `git add internal/console/export.go internal/console/export_test.go internal/api/console.go internal/api/console_test.go && git commit -m "console: pagination, per-user rows-per-page, CSV/SQL export, copy guard, history restore (ly-ae6.8)"`

---

## Self-Review

### Spec-coverage: every COMPARISON gap → task

`docs/design/COMPARISON.md` §"SQL Console (T2)" (lines 331–339):

| COMPARISON gap | Covered by |
| --- | --- |
| No live ad-hoc query-execution path (net-new capability) | Task 1 `console.Executor` seam + `StubExecutor`; real transport = **new backend bead**, referenced |
| No SQL Console UI/route; target picker (cluster→node+db, node→db, db→node); RUN-inert-until-resolved | Task 2 (picker markup) + Task 3 (routes, `consoleChips`, `NodeFixed/DatabaseFixed` via `lockNode/lockDatabase`, `Editor.Ready` gating) |
| Editor: mono textarea, `ROW LIMIT 500 · STATEMENT TIMEOUT 5S`, RUN ⌘↵, SAVE ▾ | Task 2 `consoleEditor` (`consoleRowLimit=500`, `consoleTimeoutSecs=5`, `data-console-run` + console.js ⌘↵); SAVE ▾ rendered as ly-ae6.9 seam |
| No per-cluster time-boxed session-grant model + SESSION GRANT ACTIVE banner | Task 1 `GrantReader`/`StubGrantReader`; Task 2 `consoleGrantBanner`; Task 3 grant wiring; real grant service = ly-8b0.5 + **new backend bead** |
| No request-access gate linking to Capabilities | Task 2 locked card + Task 3 `CapabilitiesHref = /databases/{id}/capabilities` |
| No results pagination; rows-per-page (10/25/50/100) persisted per user; COPY (size-guard)/CSV/SQL export | Task 5 (`consoleResultVM` paging, `consolePageSize` cookie, `handleConsoleExport`, `console.CopyTSV/CSV/SQLInserts`, `CopyTooLarge`) |
| No statement history click-to-retrieve; no `console.query.execute` per-statement audit wired to audit_log | Task 4 (audit via `AppendAuditReturning`, action `console.query.execute`, tier 2) + `consoleHistoryVM`; Task 5 restore path |
| No saved-scripts model/storage/UI | **Out of scope — ly-ae6.9**; console renders the SAVE ▾ + search seam only (stated in Dependencies) |
| Existing audit surface ignores design system | **Out of scope — ly-ae6.7** audit retrofit; noted. Console runs still land in `/audit` today |

`COMPARISON.md` line 326 (Capabilities gap: "Session-grant / request-access flow absent") is satisfied by the grant banner + request-access gate (Tasks 2–4).

Design-doc requirements (README §"SQL Console (T2)", PRODUCT_INTENT §6):
- Target rules cluster/node/db → Task 3 (`node`/`db`/`lockNode`/`lockDatabase`). **`node → node fixed, pick database` / `database → database fixed, pick node` (README l.67):** the locked axis is preserved across a free-axis chip click because `consoleChipHref` carries `lockNode`/`lockDatabase` into every chip href, and `consoleChips` emits the fixed axis as a single inert (empty-Href) chip — proven by `TestConsole_lockedAxisStaysInertAcrossChipClick` (handler) + `TestConsoleBody_fixedAxisChipIsInert` (templ). RUN inert until both resolve → Task 2/3 (`Editor.Ready`).
- Grant banner exact fields (GROUP, READ-ONLY — WRITES & DDL BLOCKED, EXPIRES IN, REF) + audit-trail link → Task 2 `consoleGrantBanner`, Task 3 `ConsoleGrantVM`.
- rows-per-page persisted per user → Task 5 cookie (per-browser HttpOnly cookie; SSR equivalent of the prototype's localStorage — substitution noted below).
- COPY size-guarded "too large — use CSV" fallback; CSV; SQL(INSERT) — all over the full result → Task 5.
- `T2 READ LOGGED · <hash>` → Task 4: the tamper-evident chain hash `rec.RowHash` is stored as the run's full hex `ID` (URL-safe lookup key) and displayed as `ShortHash` ("6c1d…e44"); `vm.Result.Hash` shows `ShortHash`.
- Statement history strict audit (actor, target, timestamp, duration, hash), click-to-retrieve → Task 4 audit detail + Task 5 restore. Restore round-trips the **full-hex** `Run.ID` (never the multibyte short form) as the `restore=` token and `sessions.Get` key — proven by `TestConsole_historyRestoreLoadsStatement` (GETs `?restore=<hash>`, asserts the older statement reloads).
- Runs append to the org Audit Log → Task 4 (`AppendAuditReturning`, visible at `/audit`).

### Bead acceptance-criteria checklist (ly-ae6.8 description)
- "Cluster/node/db scope only" → routes under `/databases/{clusterID}/console`; nav gating owned by ly-ae6.3 (contract stated).
- "target picker (RUN inert until resolved)" → Tasks 2/3.
- "per-cluster time-boxed session grant banner + request gate" → Tasks 1–4.
- "editor with row-limit/timeout" → Task 2 (`consoleRowLimit`/`consoleTimeoutSecs`).
- "paginated results with per-user rows-per-page + COPY/CSV/SQL export" → Task 5.
- "strict per-statement audit to history + org audit log" → Task 4.

### Adversarial-review fixes (all resolved in this revision)
| Review finding | Resolution |
| --- | --- |
| **MUST-FIX** — `consoleChips` dropped `lockNode=1`/`lockDatabase=1`, so the first free-axis chip click un-fixed the locked axis (broke README l.67 / PRODUCT_INTENT §6). | `consoleChipHref` now carries both lock flags into every chip href; `consoleChips` takes `(node, db, lockNode, lockDatabase)` and emits the fixed axis as a single inert (empty-Href) chip. New tests: `TestConsole_lockedAxisStaysInertAcrossChipClick` (Task 3, both node & database scope) and `TestConsoleBody_fixedAxisChipIsInert` (Task 2). |
| **TDD gap** — restore test extracted a `restore=` token then discarded it (`_ = tok`); the restore code path was untested. | `TestConsole_historyRestoreLoadsStatement` (Task 5) now GETs `?restore=<fullhex>` for the OLDER run and asserts `SELECT 1</textarea>` reloads while `SELECT 2</textarea>` does not — driving `q.Get("restore") → sessions.Get → vm.Editor.SQL`. `restoreTokens` helper parses `&amp;`-escaped hrefs correctly. |
| **Fragile lookup key** — `Run.ID = shortHash(...)` (multibyte ellipsis) used as URL token + map key; byte-offset extraction was wrong. | Split into `Run.ID` (full 64-char hex, URL-safe, the only lookup/restore key) and `Run.ShortHash` (display only). `sessions.Get`, the `restore=` token, and the `T2 READ LOGGED · <hash>` display all updated; `shortHash` doc-comment forbids using it as a key. |
| **Cross-scope leak** — `sessions.Latest/.Get` keyed by actor only; export/pagination/restore ignored `{clusterID}`. | `Sessions` is now keyed by `(actor, clusterID)`; `Append/Recent/Latest/Get` all take `clusterID`; `handleConsoleExport` reads `r.PathValue("clusterID")`. Cross-cluster isolation asserted in `TestSessions_appendCapAndGet`. |
| **Dead state** — `vm.Unavailable` ("NO TARGET SCOPE") never set true (route always has `{clusterID}`). | Dropped the field and its templ branch entirely (simplicity-first; unknown cluster is already a 404). |
| **Hand-waving** — Task 3/5 described the `w`-threading refactor in prose. | `buildConsoleVM(w, r)` takes `w` from Task 3 (with a comment explaining why); Task 5 is now a one-line swap (`consolePageSize(r)` → `s.consolePageSize(w, r)`) + one block replacement — no multi-site refactor. Task 4 import block is spelled out explicitly. |
| **Note** — rows-per-page is a per-browser cookie, not a per-user setting. | Accepted substitution (no user-settings store exists yet; DevAuth is single-user). Documented at README-bullet + below. |

### Placeholder scan
No "TBD" / "similar to Task N" / bodyless steps. Every step names an exact file, real code, an exact command, and expected FAIL/PASS. The two intentional forward-references (`consoleResultVM`/`consoleHistoryVM` stubbed in Task 3, filled in Tasks 4/5; the `handleConsoleRun`/`handleConsoleExport` 501 stubs registered in Task 3) are called out explicitly with their finishing task, so the tree compiles at every commit. The `buildConsoleVM(w, …)` parameter is intentionally unused until Task 5 (Go permits unused parameters; commented at its definition).

### Known substitutions (deliberate, not gaps)
- **rows-per-page** is persisted in an HttpOnly cookie (`lynceus.console.pagesize`), i.e. per-browser, not a per-user server setting — there is no user-settings store yet and DevAuth is single-user. When a settings store lands, swap the cookie read/write in `s.consolePageSize` for it; no UI change.
- **Session cache** is process-local and keyed by `(actor, clusterID)`; it is UI convenience state only. The durable record of every run is the `audit_log`. Export/pagination/restore read this cache back and are intentionally NOT re-audited (they operate on the already-audited full result of one logged run).

### Type-consistency check
- `AppendAuditReturning(ctx, store.AuditEntry) (store.AuditRecord, error)` — verified `internal/store/config.go:18,186`; `AuditRecord.RowHash []byte`, `.At time.Time` — verified `config.go:79-89`. `Detail any` accepts `map[string]any` — verified `config.go:74`.
- `ListInstances(ctx, clusterID) ([]store.Instance,…)`, `ListServerStreams(ctx, instanceID) ([]store.ServerStream,…)`, `ListClusters` — verified `internal/store/fleet.go:70,88,119`; `Instance.Name`, `ServerStream{ServerID, DatabaseName, T2Enabled}` — verified `fleet.go:20-38`.
- `actorFromContext(*http.Request) string` returns `"dev-admin"` — verified `internal/api/capabilities.go:34`; tests assert `"dev-admin"` (`capabilities_test.go:196`).
- Test harness `newPGPool(t)` + `store.ApplyConfigMigrations` + `httptest.NewServer(api.NewServer(...).Handler())` — verified `internal/api/server_test.go:22-79`; servers columns `id,name,instance_id,database_name,t2_enabled` — verified `migrations/config/0001_init.sql` + `0005_fleet.sql:29`.
- templ render `Component.Render(context.Context, io.Writer) error` — verified `web/layout_test.go:12`. `templ.SafeURL` used for hrefs; component naming (`ConsolePage`/`ConsoleBody`) matches the `XxxPage`+`XxxBody` convention.
- Tokens referenced (`--acc`, `--acc2`, `--accbg`, `--warn`, `--warnT`, `--line`, `--surface`, `--raised`, `--mut`, `--dim`, `--faint`, `--font-mono`, `--radius`, `--radius-badge`) all exist in `web/static/css/tokens.css:23-38`.
- No external hosts introduced: only `/static/js/console.js` (same-origin, embedded by `web/static.go`); `TestLayout_NoExternalHosts` unaffected (Task 2 Step 8).
- `console.Sessions` is keyed by `(actor, clusterID)`: `Append(actor, clusterID string, r Run)`, `Recent/Latest(actor, clusterID string)`, `Get(actor, clusterID, id string)` — every call site in `internal/api/console.go` (`buildConsoleVM`, `handleConsoleRun`, `handleConsoleExport`) passes the URL's `clusterID`. `Run.ID` is the full `hex.EncodeToString(rec.RowHash)` (lookup key); `Run.ShortHash` is display-only. `net/url` is imported in `internal/api/console.go` (Task 3, for `consoleChipHref`/`url.QueryEscape`) and in `internal/api/console_test.go` (Task 4).
