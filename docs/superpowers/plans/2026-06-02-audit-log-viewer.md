# Audit Log Viewer (ly-8b0.7) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a read-only templ+HTMX page to the api_server that displays the tamper-evident audit log, filterable by actor, action, server, time range, and data tier.

**Architecture:** The audit log lives in the **config/metadata DB** (`lynceus_config`), separate from the stats DB the api_server already connects to. We add a read method (`ListAudit`) to the existing `store.Config` type, wire a second pool (the config DB) into the api `Server`, and serve a filterable page at `GET /audit` plus an HTMX table fragment at `GET /partial/audit`. The filter form submits via `hx-get` and swaps the results table in place. Read-only: no writes, the audit chain writer (ly-8b0.3) is untouched.

**Tech Stack:** Go 1.x, pgx/v5, templ (v0.3.1020, generated via `make templ`), HTMX 2.x, testcontainers-go for integration tests against real PostgreSQL.

---

## Background / key facts the implementer must know

- **Two databases.** `docker-compose.dev.yml` runs `config-db` (localhost:5432, `lynceus_config`) and `stats-db` (localhost:5433, `lynceus_stats`). `audit_log` is created by the **config** migrations (`internal/store/migrations/config/0001_init.sql` + `0002_audit_chain.sql`). The api_server's `main.go` today only opens `LYNCEUS_STATS_DSN`. This plan adds `LYNCEUS_CONFIG_DSN`.
- **`audit_log` columns** (`0001_init.sql` + `0002_audit_chain.sql`): `id BIGSERIAL`, `actor TEXT NOT NULL`, `action TEXT NOT NULL`, `server_id TEXT` (NULL for org-level events), `data_tier SMALLINT` (NULL for tier 0 / non-data events — the writer uses `NULLIF(tier,0)`), `detail JSONB`, `at TIMESTAMPTZ`, `prev_hash BYTEA`, `row_hash BYTEA`.
- **Existing reader shape.** `store.AuditRecord` (config.go:39) already models a persisted row: `ID, Actor, Action, ServerID, DataTier, Detail []byte, At, PrevHash, RowHash`. Reuse it for `ListAudit` output. `VerifyChain` (config.go:148) shows the exact `SELECT` projection + `COALESCE` idioms to copy.
- **Existing api/web patterns to mirror:**
  - `internal/api/dashboard.go` — `handleDashboard` (full page) + `handleQueriesPartial` (fragment), both call a `fetch*` helper and `web.Xxx(...).Render(ctx, w)`.
  - `web/queries.templ` — `QueriesPage` wraps `@Layout(...)` around `QueriesTable`; the table `<div>` carries the HTMX attrs and an `id` used as the outerHTML swap target.
  - `web/layout.templ` — `Layout(title string)` shell; currently hardcodes the subtitle "top queries by total time". This plan parameterizes the subtitle and adds a nav.
  - `internal/api/server.go` — `NewServer(cfg, stats)` + `routes()`. `withAuth` already gates everything behind `DevAuth`.
- **Tests use testcontainers** (`internal/api/server_test.go` `setup()`): one `postgres:16` container, `ApplyStatsMigrations` applied. For audit we additionally apply `ApplyConfigMigrations` to the **same** pool (in tests both schemas can share one DB — table names don't collide) and pass `store.NewConfig(pool)` as the config store.
- **templ codegen:** after editing any `.templ`, run `make templ` (installs pinned templ `v0.3.1020`, runs `templ generate`) to regenerate `*_templ.go`. Both `.templ` and generated `_templ.go` are committed.
- **Run a single Go test:** `go test ./internal/store/ -run TestName -v` (testcontainers self-skip if Docker is unavailable — see the `t.Skipf` in `setup`).

## File Structure

- **Modify** `internal/store/config.go` — add `AuditFilter` struct + `ListAudit` method (read-only query with dynamic WHERE).
- **Create** `internal/store/audit_list_test.go` — integration tests for `ListAudit` filters.
- **Modify** `internal/api/server.go` — add `conf *store.Config` field; change `NewServer` signature; register `/audit` + `/partial/audit` routes.
- **Create** `internal/api/audit.go` — handlers + query-param parsing → `store.AuditFilter` → `web.AuditRow` mapping.
- **Modify** `internal/api/dashboard.go` — none required (left as-is); only listed here so the implementer knows it is the pattern source.
- **Create** `web/audit.templ` — `AuditRow` view-model, `AuditFilterValues`, `AuditPage`, `AuditFilterForm`, `AuditTable`.
- **Modify** `web/layout.templ` — `Layout(title, subtitle string)` + a small nav (`Top queries` | `Audit log`).
- **Modify** `web/queries.templ` — update the `@Layout(...)` call to pass the subtitle.
- **Modify** `cmd/api/main.go` — open the config DB pool (`LYNCEUS_CONFIG_DSN`) and pass `store.NewConfig(configPool)` to `NewServer`.
- **Modify** `internal/api/server_test.go` — `setup()` applies config migrations and passes the config store to `NewServer`; add an `seedAudit` helper.
- **Create** `internal/api/audit_test.go` — handler-level integration tests (full page + fragment + a filter).

---

## Task 1: Store — `AuditFilter` + `ListAudit` reader

**Files:**
- Modify: `internal/store/config.go`
- Test: `internal/store/audit_list_test.go` (create)

- [ ] **Step 1: Write the failing test**

Create `internal/store/audit_list_test.go`:

```go
package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/dobbo-ca/lynceus/internal/store"
)

func seedAuditRows(t *testing.T, cfg *store.Config) {
	t.Helper()
	ctx := context.Background()
	rows := []store.AuditEntry{
		{Actor: "alice", Action: "login", ServerID: "srv-1", DataTier: 0},
		{Actor: "alice", Action: "viewed.t2", ServerID: "srv-1", DataTier: 2, Detail: map[string]any{"fp": "abc"}},
		{Actor: "bob", Action: "viewed.t2", ServerID: "srv-2", DataTier: 2},
		{Actor: "bob", Action: "config.toggle", ServerID: "srv-2", DataTier: 1},
		{Actor: "carol", Action: "login", DataTier: 0},
	}
	for i, e := range rows {
		if _, err := cfg.AppendAuditReturning(ctx, e); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}
}

func TestListAudit_noFilter_returnsAllMostRecentFirst(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg := store.NewConfig(pool)
	seedAuditRows(t, cfg)

	got, err := cfg.ListAudit(ctx, store.AuditFilter{Limit: 100})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("rows = %d, want 5", len(got))
	}
	// Most recent first → highest id first → last seeded actor "carol".
	if got[0].Actor != "carol" {
		t.Errorf("got[0].Actor = %q, want carol (most recent first)", got[0].Actor)
	}
}

func TestListAudit_filtersByActorActionServerTier(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg := store.NewConfig(pool)
	seedAuditRows(t, cfg)

	byActor, err := cfg.ListAudit(ctx, store.AuditFilter{Actor: "alice", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(byActor) != 2 {
		t.Errorf("actor=alice rows = %d, want 2", len(byActor))
	}

	byAction, err := cfg.ListAudit(ctx, store.AuditFilter{Action: "viewed.t2", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(byAction) != 2 {
		t.Errorf("action=viewed.t2 rows = %d, want 2", len(byAction))
	}

	byServer, err := cfg.ListAudit(ctx, store.AuditFilter{ServerID: "srv-2", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(byServer) != 2 {
		t.Errorf("server=srv-2 rows = %d, want 2", len(byServer))
	}

	tier := int16(2)
	byTier, err := cfg.ListAudit(ctx, store.AuditFilter{Tier: &tier, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(byTier) != 2 {
		t.Errorf("tier=2 rows = %d, want 2", len(byTier))
	}

	// Combined filter narrows further.
	both, err := cfg.ListAudit(ctx, store.AuditFilter{Actor: "bob", Tier: &tier, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(both) != 1 || both[0].Action != "viewed.t2" {
		t.Errorf("actor=bob tier=2 = %+v, want exactly bob/viewed.t2", both)
	}
}

func TestListAudit_filtersByTimeRangeAndAppliesLimit(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg := store.NewConfig(pool)
	seedAuditRows(t, cfg)

	now := time.Now().UTC()

	// Window covering "now" returns everything.
	wide, err := cfg.ListAudit(ctx, store.AuditFilter{
		Since: now.Add(-time.Hour), Until: now.Add(time.Hour), Limit: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(wide) != 5 {
		t.Errorf("wide window rows = %d, want 5", len(wide))
	}

	// Window entirely in the future returns nothing.
	future, err := cfg.ListAudit(ctx, store.AuditFilter{
		Since: now.Add(time.Hour), Limit: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(future) != 0 {
		t.Errorf("future window rows = %d, want 0", len(future))
	}

	// Limit caps the result set.
	capped, err := cfg.ListAudit(ctx, store.AuditFilter{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(capped) != 2 {
		t.Errorf("limit=2 rows = %d, want 2", len(capped))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestListAudit -v`
Expected: compile failure — `cfg.ListAudit undefined` and `store.AuditFilter` undefined.

- [ ] **Step 3: Write minimal implementation**

In `internal/store/config.go`, add after the `AuditRecord` type (around line 49) the filter type and method. Use `fmt` (already imported) for building args:

```go
// AuditFilter narrows a ListAudit query. Empty strings and zero times
// impose no constraint. Tier is a pointer so the caller can distinguish
// "any tier" (nil) from a specific tier value. Limit caps the result
// set; ListAudit applies a sane default and ceiling if it is <= 0 or
// absurdly large.
type AuditFilter struct {
	Actor    string
	Action   string
	ServerID string
	Since    time.Time
	Until    time.Time
	Tier     *int16
	Limit    int
}

// ListAudit returns audit records matching f, ordered most-recent-first
// (id DESC). It is read-only and never touches the hash chain. The
// projection mirrors VerifyChain so callers get fully-populated
// AuditRecords (including the chain hashes, for display).
func (c *Config) ListAudit(ctx context.Context, f AuditFilter) ([]AuditRecord, error) {
	limit := f.Limit
	if limit <= 0 || limit > 1000 {
		limit = 200
	}

	q := `SELECT id, actor, action, COALESCE(server_id,''), COALESCE(data_tier,0),
	             COALESCE(detail::text,''), at, prev_hash, row_hash
	        FROM audit_log
	       WHERE 1=1`
	var args []any
	add := func(clause string, val any) {
		args = append(args, val)
		q += fmt.Sprintf(" AND %s $%d", clause, len(args))
	}
	if f.Actor != "" {
		add("actor =", f.Actor)
	}
	if f.Action != "" {
		add("action =", f.Action)
	}
	if f.ServerID != "" {
		add("server_id =", f.ServerID)
	}
	if f.Tier != nil {
		add("data_tier =", *f.Tier)
	}
	if !f.Since.IsZero() {
		add("at >=", f.Since)
	}
	if !f.Until.IsZero() {
		add("at <=", f.Until)
	}
	args = append(args, limit)
	q += fmt.Sprintf(" ORDER BY id DESC LIMIT $%d", len(args))

	rows, err := c.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list audit: %w", err)
	}
	defer rows.Close()

	var out []AuditRecord
	for rows.Next() {
		var (
			rec    AuditRecord
			detail string
		)
		if err := rows.Scan(&rec.ID, &rec.Actor, &rec.Action, &rec.ServerID,
			&rec.DataTier, &detail, &rec.At, &rec.PrevHash, &rec.RowHash); err != nil {
			return nil, fmt.Errorf("scan audit row: %w", err)
		}
		if detail != "" {
			rec.Detail = []byte(detail)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("audit rows: %w", err)
	}
	return out, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestListAudit -v`
Expected: PASS (3 tests). If Docker is unavailable the tests self-skip — re-run where Docker is available before claiming done.

- [ ] **Step 5: Commit**

```bash
git add internal/store/config.go internal/store/audit_list_test.go
git commit -m "feat(store): ListAudit filtered reader for audit log (ly-8b0.7)"
```

---

## Task 2: Web — layout subtitle + nav, audit templ components

**Files:**
- Modify: `web/layout.templ`
- Modify: `web/queries.templ`
- Create: `web/audit.templ`
- Regenerate: `web/*_templ.go` via `make templ`

- [ ] **Step 1: Parameterize the layout subtitle and add nav**

Edit `web/layout.templ`. Change the `Layout` signature and body. Replace lines 18–44 (the `templ Layout` block) with:

```go
templ Layout(title, subtitle string) {
	<!DOCTYPE html>
	<html lang="en">
		<head>
			<meta charset="UTF-8"/>
			<meta name="viewport" content="width=device-width, initial-scale=1"/>
			<title>{ title }</title>
			<script src="https://unpkg.com/htmx.org@2.0.4"></script>
			<style>
				body { font-family: system-ui, sans-serif; margin: 2rem; max-width: 1200px; color: #1d1d1d; }
				h1 { margin-bottom: 0.25rem; }
				nav { margin-bottom: 0.5rem; }
				nav a { color: #2b6cb0; text-decoration: none; margin-right: 1rem; font-size: 0.9rem; }
				nav a:hover { text-decoration: underline; }
				.subtitle { color: #666; margin-top: 0; margin-bottom: 1.5rem; }
				table { border-collapse: collapse; width: 100%; }
				th, td { text-align: left; padding: 0.5rem 0.75rem; border-bottom: 1px solid #e0e0e0; vertical-align: top; }
				th { background: #f4f4f4; font-size: 0.85rem; text-transform: uppercase; letter-spacing: 0.04em; }
				td.num { text-align: right; font-variant-numeric: tabular-nums; white-space: nowrap; }
				code { font-family: ui-monospace, "SF Mono", Menlo, monospace; font-size: 0.85rem; }
				.empty { color: #666; font-style: italic; padding: 2rem 0; }
				form.filters { display: flex; flex-wrap: wrap; gap: 0.75rem; align-items: flex-end; margin-bottom: 1.25rem; }
				form.filters label { display: flex; flex-direction: column; font-size: 0.75rem; text-transform: uppercase; letter-spacing: 0.04em; color: #666; gap: 0.2rem; }
				form.filters input, form.filters select { padding: 0.35rem 0.5rem; font-size: 0.9rem; }
				form.filters button { padding: 0.4rem 0.9rem; font-size: 0.9rem; cursor: pointer; }
			</style>
		</head>
		<body>
			<h1>Lynceus</h1>
			<nav>
				<a href="/">Top queries</a>
				<a href="/audit">Audit log</a>
			</nav>
			<p class="subtitle">{ subtitle }</p>
			{ children... }
		</body>
	</html>
}
```

- [ ] **Step 2: Update the QueriesPage call site**

Edit `web/queries.templ`. Change the `@Layout(...)` line in `QueriesPage` (line 7) from:

```go
	@Layout("Lynceus — top queries") {
```

to:

```go
	@Layout("Lynceus — top queries", "top queries by total time") {
```

- [ ] **Step 3: Create the audit templ components**

Create `web/audit.templ`:

```go
package web

import "fmt"

// AuditRow is the view-model for one rendered audit-log row. All fields
// are already-normalized strings/ints from the config DB; the detail is
// the canonical JSON text as stored (may be empty).
type AuditRow struct {
	ID       int64
	Actor    string
	Action   string
	ServerID string
	DataTier int16
	Detail   string
	At       string // pre-formatted timestamp (RFC3339, UTC)
}

// AuditFilterValues echoes the active filter back into the form so the
// inputs stay populated across HTMX submissions.
type AuditFilterValues struct {
	Actor    string
	Action   string
	ServerID string
	Since    string // yyyy-mm-dd or empty
	Until    string // yyyy-mm-dd or empty
	Tier     string // "", "1", or "2"
}

// AuditPage is the full filterable audit-log page.
templ AuditPage(f AuditFilterValues, rows []AuditRow) {
	@Layout("Lynceus — audit log", "tamper-evident audit log") {
		@AuditFilterForm(f)
		@AuditTable(rows)
	}
}

// AuditFilterForm submits via hx-get to /partial/audit and swaps the
// results table (#audit-table) in place. A normal submit (no JS) falls
// back to a full-page GET /audit with the same query params.
templ AuditFilterForm(f AuditFilterValues) {
	<form class="filters" action="/audit" method="get"
		hx-get="/partial/audit" hx-target="#audit-table" hx-swap="outerHTML">
		<label>
			Actor
			<input type="text" name="actor" value={ f.Actor } placeholder="any"/>
		</label>
		<label>
			Action
			<input type="text" name="action" value={ f.Action } placeholder="any"/>
		</label>
		<label>
			Server
			<input type="text" name="server" value={ f.ServerID } placeholder="any"/>
		</label>
		<label>
			Tier
			<select name="tier">
				<option value="" selected?={ f.Tier == "" }>Any</option>
				<option value="1" selected?={ f.Tier == "1" }>T1</option>
				<option value="2" selected?={ f.Tier == "2" }>T2</option>
			</select>
		</label>
		<label>
			From
			<input type="date" name="since" value={ f.Since }/>
		</label>
		<label>
			To
			<input type="date" name="until" value={ f.Until }/>
		</label>
		<button type="submit">Filter</button>
	</form>
}

// AuditTable renders just the results table. The wrapping div carries the
// id used as the HTMX swap target.
templ AuditTable(rows []AuditRow) {
	<div id="audit-table">
		if len(rows) == 0 {
			<p class="empty">No audit entries match the current filter.</p>
		} else {
			<table>
				<thead>
					<tr>
						<th class="num">ID</th>
						<th>Time (UTC)</th>
						<th>Actor</th>
						<th>Action</th>
						<th>Server</th>
						<th class="num">Tier</th>
						<th>Detail</th>
					</tr>
				</thead>
				<tbody>
					for _, r := range rows {
						<tr>
							<td class="num">{ fmt.Sprintf("%d", r.ID) }</td>
							<td>{ r.At }</td>
							<td>{ r.Actor }</td>
							<td><code>{ r.Action }</code></td>
							<td>{ r.ServerID }</td>
							<td class="num">{ fmt.Sprintf("%d", r.DataTier) }</td>
							<td><code>{ r.Detail }</code></td>
						</tr>
					}
				</tbody>
			</table>
		}
	</div>
}
```

- [ ] **Step 4: Regenerate templ Go and build**

Run: `make templ && go build ./...`
Expected: `templ generate` writes `web/audit_templ.go` and rewrites `layout_templ.go` / `queries_templ.go`; `go build ./...` succeeds.

Note: `go build ./...` will FAIL at this point only inside `internal/api` and `cmd/api` because `NewServer` still has the old signature and nothing renders the audit page yet — that is expected and fixed in Tasks 3–4. If the build error is confined to `NewServer` arity / unused symbols in api, proceed. The `web` package itself must compile cleanly: `go build ./web/` must pass.

Run: `go build ./web/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/layout.templ web/queries.templ web/audit.templ web/*_templ.go
git commit -m "feat(web): audit-log templ components + layout nav (ly-8b0.7)"
```

---

## Task 3: API — wire config store + audit handlers

**Files:**
- Modify: `internal/api/server.go`
- Create: `internal/api/audit.go`

- [ ] **Step 1: Add the config store to the server and register routes**

Edit `internal/api/server.go`:

Add the `conf` field to the `Server` struct (after the `stats` field):

```go
// Server bundles routes and dependencies.
type Server struct {
	cfg   Config
	stats *store.Stats
	conf  *store.Config
	mux   *http.ServeMux
}
```

Change `NewServer` to accept the config store:

```go
// NewServer returns a fully wired Server. stats is the stats-DB store;
// conf is the config/metadata-DB store (used by the audit-log viewer).
func NewServer(cfg Config, stats *store.Stats, conf *store.Config) *Server {
	s := &Server{cfg: cfg, stats: stats, conf: conf, mux: http.NewServeMux()}
	s.routes()
	return s
}
```

Add the two audit routes inside `routes()` (after the queries partial line):

```go
func (s *Server) routes() {
	s.mux.HandleFunc("GET /", s.handleDashboard)
	s.mux.HandleFunc("GET /partial/queries", s.handleQueriesPartial)
	s.mux.HandleFunc("GET /audit", s.handleAuditPage)
	s.mux.HandleFunc("GET /partial/audit", s.handleAuditPartial)
	s.mux.HandleFunc("GET /api/queries/top", s.handleTopQueries)
	s.mux.HandleFunc("GET /api/scim/v2/", s.notImplemented("SCIM"))
	s.mux.HandleFunc("GET /api/oidc/", s.notImplemented("OIDC"))
}
```

- [ ] **Step 2: Create the audit handlers**

Create `internal/api/audit.go`:

```go
package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/dobbo-ca/lynceus/web"
)

// dateLayout is the format produced by HTML <input type="date">.
const dateLayout = "2006-01-02"

// handleAuditPage renders the full filterable audit-log page.
func (s *Server) handleAuditPage(w http.ResponseWriter, r *http.Request) {
	values, rows := s.fetchAudit(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.AuditPage(values, rows).Render(r.Context(), w)
}

// handleAuditPartial renders just the results table, for HTMX in-place
// filtering.
func (s *Server) handleAuditPartial(w http.ResponseWriter, r *http.Request) {
	_, rows := s.fetchAudit(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.AuditTable(rows).Render(r.Context(), w)
}

// fetchAudit parses the request's query params into a store filter,
// queries the config DB, and returns both the echoed-back form values
// and the rendered rows.
func (s *Server) fetchAudit(r *http.Request) (web.AuditFilterValues, []web.AuditRow) {
	q := r.URL.Query()
	values := web.AuditFilterValues{
		Actor:    q.Get("actor"),
		Action:   q.Get("action"),
		ServerID: q.Get("server"),
		Since:    q.Get("since"),
		Until:    q.Get("until"),
		Tier:     q.Get("tier"),
	}

	filter := store.AuditFilter{
		Actor:    values.Actor,
		Action:   values.Action,
		ServerID: values.ServerID,
		Limit:    200,
	}
	if t, err := time.Parse(dateLayout, values.Since); err == nil {
		filter.Since = t
	}
	if t, err := time.Parse(dateLayout, values.Until); err == nil {
		// Inclusive end-of-day so "until 2026-06-02" includes that day.
		filter.Until = t.Add(24*time.Hour - time.Nanosecond)
	}
	if n, err := strconv.Atoi(values.Tier); err == nil && (n == 1 || n == 2) {
		tier := int16(n)
		filter.Tier = &tier
	}

	recs, err := s.conf.ListAudit(r.Context(), filter)
	if err != nil {
		return values, nil
	}
	out := make([]web.AuditRow, 0, len(recs))
	for _, rec := range recs {
		out = append(out, web.AuditRow{
			ID:       rec.ID,
			Actor:    rec.Actor,
			Action:   rec.Action,
			ServerID: rec.ServerID,
			DataTier: rec.DataTier,
			Detail:   string(rec.Detail),
			At:       rec.At.UTC().Format(time.RFC3339),
		})
	}
	return values, out
}
```

- [ ] **Step 3: Build**

Run: `go build ./internal/api/ ./web/`
Expected: PASS. (`cmd/api` still fails until Task 4 — that's expected.)

- [ ] **Step 4: Commit**

```bash
git add internal/api/server.go internal/api/audit.go
git commit -m "feat(api): audit-log viewer handlers + config store wiring (ly-8b0.7)"
```

---

## Task 4: Wire the config DB pool into cmd/api

**Files:**
- Modify: `cmd/api/main.go`

- [ ] **Step 1: Open the config pool and pass the config store**

Edit `cmd/api/main.go`. After reading `dsn` (the stats DSN), add a config DSN read; after opening `pool`, open `configPool`; pass `store.NewConfig(configPool)` to `NewServer`.

Replace the env-read block (lines 19–27) with:

```go
	dsn := os.Getenv("LYNCEUS_STATS_DSN")
	if dsn == "" {
		log.Fatal("LYNCEUS_STATS_DSN required")
	}
	configDSN := os.Getenv("LYNCEUS_CONFIG_DSN")
	if configDSN == "" {
		log.Fatal("LYNCEUS_CONFIG_DSN required")
	}
	addr := os.Getenv("LYNCEUS_API_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	devAuth := os.Getenv("LYNCEUS_DEV_AUTH") == "true"
```

Replace the pool-open + server-construction block (lines 33–39) with:

```go
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Fatalf("connect stats db: %v", err)
	}
	defer pool.Close()

	configPool, err := pgxpool.New(ctx, configDSN)
	if err != nil {
		log.Fatalf("connect config db: %v", err)
	}
	defer configPool.Close()

	srv := api.NewServer(api.Config{DevAuth: devAuth},
		store.NewStats(pool), store.NewConfig(configPool))
```

- [ ] **Step 2: Build everything**

Run: `go build ./...`
Expected: PASS (whole repo compiles now).

- [ ] **Step 3: Commit**

```bash
git add cmd/api/main.go
git commit -m "feat(api): open config DB pool for audit viewer (ly-8b0.7)"
```

---

## Task 5: API integration tests for the audit viewer

**Files:**
- Modify: `internal/api/server_test.go`
- Create: `internal/api/audit_test.go`

- [ ] **Step 1: Update the shared test setup to apply config migrations + pass the config store**

Edit `internal/api/server_test.go`. In `setup()`, after `ApplyStatsMigrations`, apply config migrations and pass the config store into `NewServer`. Replace the migrate + server lines (currently lines 45–48) with:

```go
	if err := store.ApplyStatsMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate stats: %v", err)
	}
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate config: %v", err)
	}
	srv := httptest.NewServer(
		api.NewServer(cfg, store.NewStats(pool), store.NewConfig(pool)).Handler())
```

Then add a `seedAudit` helper at the end of `server_test.go`:

```go
func seedAudit(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	cfg := store.NewConfig(pool)
	entries := []store.AuditEntry{
		{Actor: "alice", Action: "login", ServerID: "srv-1", DataTier: 0},
		{Actor: "alice", Action: "viewed.t2", ServerID: "srv-1", DataTier: 2},
		{Actor: "bob", Action: "config.toggle", ServerID: "srv-2", DataTier: 1},
	}
	for i, e := range entries {
		if _, err := cfg.AppendAuditReturning(ctx, e); err != nil {
			t.Fatalf("seed audit %d: %v", i, err)
		}
	}
}
```

- [ ] **Step 2: Write the failing handler tests**

Create `internal/api/audit_test.go`:

```go
package api_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/api"
)

func TestAuditPage_rendersRowsAndNav(t *testing.T) {
	pool, srv := setup(t, api.Config{DevAuth: true})
	seedAudit(t, pool)

	resp, err := http.Get(srv.URL + "/audit")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		t.Errorf("content-type = %q, want text/html...", resp.Header.Get("Content-Type"))
	}

	body, _ := io.ReadAll(resp.Body)
	html := string(body)
	for _, want := range []string{
		"<!DOCTYPE html>",      // full page
		`id="audit-table"`,     // HTMX swap target
		`hx-get="/partial/audit"`,
		`href="/audit"`,        // nav link
		"alice",                // seeded actor
		"config.toggle",        // seeded action
		"srv-2",                // seeded server
	} {
		if !strings.Contains(html, want) {
			t.Errorf("audit page missing %q", want)
		}
	}
}

func TestAuditPartial_returnsFragmentOnly(t *testing.T) {
	pool, srv := setup(t, api.Config{DevAuth: true})
	seedAudit(t, pool)

	resp, err := http.Get(srv.URL + "/partial/audit")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	html := string(body)
	if strings.Contains(html, "<!DOCTYPE html>") {
		t.Error("partial returned a full document; expected a fragment only")
	}
	if !strings.Contains(html, `id="audit-table"`) {
		t.Error("partial missing the swap-target id")
	}
}

func TestAuditPartial_filtersByActor(t *testing.T) {
	pool, srv := setup(t, api.Config{DevAuth: true})
	seedAudit(t, pool)

	resp, err := http.Get(srv.URL + "/partial/audit?actor=bob")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	html := string(body)

	if !strings.Contains(html, "config.toggle") {
		t.Error("actor=bob result missing bob's action config.toggle")
	}
	if strings.Contains(html, "viewed.t2") {
		t.Error("actor=bob result leaked alice's action viewed.t2")
	}
}

func TestAuditPage_withoutDevAuth_returns401(t *testing.T) {
	_, srv := setup(t, api.Config{DevAuth: false})
	resp, err := http.Get(srv.URL + "/audit")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}
```

- [ ] **Step 3: Run the api tests**

Run: `go test ./internal/api/ -run TestAudit -v`
Expected: PASS (4 tests). Existing `TestDashboard*` / `TestTopQueries*` must still pass too:

Run: `go test ./internal/api/ -v`
Expected: all PASS (self-skip if Docker unavailable — verify on a Docker host).

- [ ] **Step 4: Commit**

```bash
git add internal/api/server_test.go internal/api/audit_test.go
git commit -m "test(api): audit-log viewer integration tests (ly-8b0.7)"
```

---

## Task 6: Full verification + docs touch-up

**Files:**
- Modify: `cmd/api/main.go` log line is fine; no doc file is strictly required. If a dev-run doc exists referencing api env vars, add `LYNCEUS_CONFIG_DSN`.

- [ ] **Step 1: Whole-repo build + test + vet**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all PASS (integration tests self-skip only if Docker is absent — run on a Docker host for a true green).

- [ ] **Step 2: Confirm templ generated files are in sync**

Run: `make templ && git diff --exit-code web/`
Expected: no diff (generated files already committed and current). If there's a diff, commit the regenerated files.

- [ ] **Step 3: Manual smoke (optional, requires dev DBs)**

```bash
make dev-up
# apply config migrations to the config DB once (e.g. via a tiny migrate run or psql),
# then:
LYNCEUS_STATS_DSN='postgres://postgres:dev@localhost:5433/lynceus_stats?sslmode=disable' \
LYNCEUS_CONFIG_DSN='postgres://postgres:dev@localhost:5432/lynceus_config?sslmode=disable' \
LYNCEUS_DEV_AUTH=true go run ./cmd/api
# visit http://localhost:8080/audit
```

- [ ] **Step 4: Commit any remaining changes**

```bash
git status
# commit anything outstanding with an appropriate message
```

---

## Acceptance criteria (from ly-8b0.7)

- [x] Read-only templ+HTMX view of the audit log (no writes to `audit_log`).
- [x] Filterable by **actor** (Task 1 store filter + Task 3 param + Task 2 form).
- [x] Filterable by **action**.
- [x] Filterable by **server**.
- [x] Filterable by **time range** (since/until date inputs → inclusive `at` bounds).
- [x] Filterable by **data tier** (T1 / T2 / any).
- [x] Served by `cmd/api` (templ SSR + HTMX fragment swap).
- [x] Integration tests hit real Postgres via testcontainers (no DB mocks).

## Self-review notes

- **Spec coverage:** every filter dimension named in the issue maps to a task (actor/action/server/tier/time → Task 1 `AuditFilter`, surfaced via Task 3 param parsing + Task 2 form inputs). Read-only confirmed: only `SELECT` is added; no INSERT/UPDATE/DELETE on `audit_log`.
- **Type consistency:** `store.AuditFilter` fields (`Actor, Action, ServerID, Since, Until, Tier *int16, Limit`) are used identically in Task 3. `web.AuditRow` / `web.AuditFilterValues` fields match between Task 2 (templ) and Task 3 (handler mapping). `NewServer(cfg, stats, conf)` arity is consistent across Task 3 (def), Task 4 (cmd/api), Task 5 (test).
- **Privacy note:** the audit `detail` JSONB *can* legitimately contain identifiers (it is config-DB audit metadata, not T1 query data), so unlike the queries page there is no literal-leak assertion — this is intentional and correct for an audit viewer that is itself gated behind auth/RBAC.
