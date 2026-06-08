# Insight HTTP Surfacing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Surface the already-built insight engine and stored plans over HTTP as templ/HTMX SSR views by adding a `ListPlanKeys` store reader, two handlers (`/insights`, `/partial/insights`), a `web/insights.templ` view, and a nav link — all T1-only, no proto changes.

**Architecture:** A thin, stateless read surface over existing reads. `fetchInsights` enumerates `(server_id, fingerprint)` plan keys via the new `store.ListPlanKeys`, loads recent plans per key via the existing `TopPlansByQuery`, runs `insight.DetectPlans`, and maps each `insight.Insight` to a `web.InsightRow` view-model. Rendering mirrors the existing dashboard/audit templ+HTMX pattern (`outerHTML` self-reswap fragment on a 10s poll), inheriting the `withAuth` dev-auth gate exactly like `/audit`.

**Tech Stack:** Go, protobuf (`make proto`), pgx/pgxpool, templ+HTMX (where relevant), testcontainers (`postgres:16`) for integration tests.

**Bead:** ly-u4t.21  ·  **Spec:** docs/specs/2026-06-08-layer0-foundation.md  ·  **Layer:** 0 Foundation

This plan covers ly-u4t.21 ONLY (insight list surface + `ListPlanKeys`). The plan-visualization surface (`/plan`, `web/plan.templ`, `ToPlanVM`) is bead ly-xqf.10 and is OUT OF SCOPE here. No proto changes are made by this plan, so the privacy contract test (`internal/proto/lynceus/v1/contract_test.go`) is untouched — `ListPlanKeys` only reads the already-allowlisted `server_id`/`fingerprint` columns.

---

## File Structure

| Path | Action | Responsibility |
|---|---|---|
| `internal/store/plans.go` | Modify (append after `plansPartitionName`, line 139) | Add `PlanKey` type + `ListPlanKeys` read-only reader (`s.ro`, `data_tier = 1`). |
| `internal/store/plans_test.go` | Modify (append after `TestWriteQueryPlans_emptyNoop`, line 110) | `TestListPlanKeys_returnsDistinctKeys` — two plans on one key + one on a second key → exactly two distinct rows. |
| `web/insights.templ` | Create | `InsightRow` view-model + `InsightsPage`/`InsightsTable` templ components (HTMX fragment), mirroring `web/audit.templ`. |
| `web/insights_templ.go` | Create (via `templ generate`) | Generated Go for `web/insights.templ` — DO NOT hand-edit. |
| `web/layout.templ` | Modify (nav block, lines 47-50) | Add `<a href="/insights">Insights</a>` nav link. |
| `web/layout_templ.go` | Regenerate (via `templ generate`) | Generated Go for the nav change. |
| `internal/api/insights.go` | Create | `handleInsightsPage`, `handleInsightsPartial`, `fetchInsights`. |
| `internal/api/server.go` | Modify (`routes()`, lines 40-48) | Register `GET /insights` and `GET /partial/insights`. |
| `internal/api/insights_test.go` | Create (package `api_test`) | Render + fragment + 401 + privacy tests; `seedPlans` helper. |

---

## Tasks

### Task 1: `ListPlanKeys` store reader + test

**Files:**
- Modify: `internal/store/plans.go` (append after line 139, the end of `plansPartitionName`)
- Test: `internal/store/plans_test.go` (append after line 110, the end of `TestWriteQueryPlans_emptyNoop`)

This mirrors `TopPlansByQuery` (`plans.go:69-118`): same `s.ro` replica, same `data_tier = 1` predicate, same `captured_at >= $ AND captured_at < $` window. The reader runs in package `store`; the test runs in package `store_test` and reuses the `newPool(t)` harness already used by `plans_test.go` (line 13) and the `QueryPlanRow`/`WriteQueryPlans` fixture (`plans_test.go:40-66`).

- [ ] **Step 1: Write the failing test** — append this exact code to `internal/store/plans_test.go`:

```go

func TestListPlanKeys_returnsDistinctKeys(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyStatsMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := store.NewStats(pool)

	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC) // a Wednesday
	planFor := func(fp string) *lynceusv1.QueryPlan {
		return &lynceusv1.QueryPlan{
			Fingerprint:    fp,
			CapturedAtUnix: now.Unix(),
			FormatVersion:  1,
			Root:           &lynceusv1.PlanNode{NodeType: "Seq Scan", RelationName: "orders"},
		}
	}
	// Two plans on key (srv-1, fp-a) + one plan on key (srv-1, fp-b).
	rows := []store.QueryPlanRow{
		{ServerID: "srv-1", Plan: planFor("fp-a"), CapturedAt: now},
		{ServerID: "srv-1", Plan: planFor("fp-a"), CapturedAt: now.Add(time.Minute)},
		{ServerID: "srv-1", Plan: planFor("fp-b"), CapturedAt: now},
	}
	if err := s.WriteQueryPlans(ctx, rows); err != nil {
		t.Fatalf("write: %v", err)
	}

	keys, err := s.ListPlanKeys(ctx, now.Add(-time.Hour), now.Add(time.Hour), 100)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("got %d distinct keys, want 2: %+v", len(keys), keys)
	}
	// ORDER BY server_id, fingerprint => fp-a then fp-b.
	if keys[0].ServerID != "srv-1" || keys[0].Fingerprint != "fp-a" {
		t.Errorf("keys[0] = %+v, want {srv-1 fp-a}", keys[0])
	}
	if keys[1].Fingerprint != "fp-b" {
		t.Errorf("keys[1].Fingerprint = %q, want fp-b", keys[1].Fingerprint)
	}
}
```

- [ ] **Step 2: Run test to verify it fails** — `go test ./internal/store/ -run TestListPlanKeys_returnsDistinctKeys`. Expected: compile failure `s.ListPlanKeys undefined (type *store.Stats has no field or method ListPlanKeys)`. (If Docker is unavailable the harness `t.Skipf`s instead — re-run after Docker is up.)

- [ ] **Step 3: Implement** — append this exact code to `internal/store/plans.go` (after line 139, the closing `}` of `plansPartitionName`):

```go

// PlanKey is one (server_id, fingerprint) pair that has at least one stored
// plan in the queried window. Both fields are structural identifiers (the same
// columns WriteQueryPlans persists, plans.go:25-28) — no literal.
type PlanKey struct {
	ServerID    string
	Fingerprint string
}

// ListPlanKeys enumerates the distinct (server_id, fingerprint) keys that have
// at least one plan captured in [since, until), most recent server/fingerprint
// order, up to limit. data_tier = 1 only (T1). Runs on the read replica,
// exactly like TopPlansByQuery (plans.go:72).
func (s *Stats) ListPlanKeys(
	ctx context.Context, since, until time.Time, limit int,
) ([]PlanKey, error) {
	rows, err := s.ro.Query(ctx,
		`SELECT DISTINCT server_id, fingerprint
		   FROM query_plans
		  WHERE captured_at >= $1 AND captured_at < $2
		    AND data_tier = 1
		  ORDER BY server_id, fingerprint
		  LIMIT $3`,
		since, until, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []PlanKey
	for rows.Next() {
		var k PlanKey
		if err := rows.Scan(&k.ServerID, &k.Fingerprint); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Run test to verify it passes** — `go test ./internal/store/ -run TestListPlanKeys_returnsDistinctKeys`. Expected: `ok  github.com/dobbo-ca/lynceus/internal/store` (PASS). (Or `SKIP` if Docker is unavailable.)

- [ ] **Step 5: Commit** — `git add internal/store/plans.go internal/store/plans_test.go` then `git commit -m "feat(store): ListPlanKeys distinct (server,fingerprint) reader (ly-u4t.21)"`.

---

### Task 2: `web/insights.templ` view-model + components, nav link, generate

**Files:**
- Create: `web/insights.templ`
- Modify: `web/layout.templ` (nav block, lines 47-50)
- Create (generated): `web/insights_templ.go`, regenerate `web/layout_templ.go`

`InsightRow` lives in the `web` package like `AuditRow` (`audit.templ:8-27`). `InsightsPage` wraps `@Layout(...)` (`layout.templ:18`) around `InsightsTable`, an HTMX `outerHTML` self-reswap fragment (`hx-get="/partial/insights" hx-trigger="every 10s" hx-swap="outerHTML"`, the `QueriesTable` pattern, `queries.templ:15-16`). The table head/body, `fmt.Sprintf` numerics, `<code>` identifiers, and `.num` class are copied from `AuditTable` (`audit.templ:77-109`). There is no failing-test step here — templ files are verified end-to-end by the `api_test` render tests in Task 4; this task's verification is that `templ generate` and `go build` succeed.

- [ ] **Step 1: Create `web/insights.templ`** with this exact content:

```go
package web

import "fmt"

// InsightRow is the view-model for one detected insight row. Every field is a
// structural identifier or an aggregate count — the same literal-free shape as
// insight.Insight (internal/insight/insight.go:31-41); the engine guarantees no
// literal reaches Detail.
type InsightRow struct {
	Kind         string
	Severity     string
	Fingerprint  string
	Relation     string
	NodePath     string
	RowsScanned  int64
	RowsReturned int64
	Detail       string
	ServerID     string
}

// InsightsPage is the full insights dashboard.
templ InsightsPage(rows []InsightRow) {
	@Layout("Lynceus — insights", "query plan insights from the detection engine") {
		@InsightsTable(rows)
	}
}

// InsightsTable is also served as a stand-alone HTMX fragment so the table
// auto-refreshes (hx-get + outerHTML swap re-installs the same hx-* attributes
// on every poll), mirroring QueriesTable (queries.templ:15-16).
templ InsightsTable(rows []InsightRow) {
	<div id="insights-table" hx-get="/partial/insights" hx-trigger="every 10s" hx-swap="outerHTML">
		if len(rows) == 0 {
			<p class="empty">No insights detected yet — start a collector and check back.</p>
		} else {
			<table>
				<thead>
					<tr>
						<th>Severity</th>
						<th>Kind</th>
						<th>Relation</th>
						<th>Node path</th>
						<th class="num">Rows scanned</th>
						<th class="num">Rows returned</th>
						<th>Detail</th>
						<th>Fingerprint</th>
					</tr>
				</thead>
				<tbody>
					for _, r := range rows {
						<tr>
							<td><code>{ r.Severity }</code></td>
							<td><code>{ r.Kind }</code></td>
							<td><code>{ r.Relation }</code></td>
							<td><code>{ r.NodePath }</code></td>
							<td class="num">{ fmt.Sprintf("%d", r.RowsScanned) }</td>
							<td class="num">{ fmt.Sprintf("%d", r.RowsReturned) }</td>
							<td>{ r.Detail }</td>
							<td><code>{ r.Fingerprint }</code></td>
						</tr>
					}
				</tbody>
			</table>
		}
	</div>
}
```

- [ ] **Step 2: Add the nav link** — in `web/layout.templ`, replace the nav block (lines 47-50):

```go
				<nav>
					<a href="/">Top queries</a>
					<a href="/audit">Audit log</a>
				</nav>
```

with:

```go
				<nav>
					<a href="/">Top queries</a>
					<a href="/insights">Insights</a>
					<a href="/audit">Audit log</a>
				</nav>
```

- [ ] **Step 3: Run templ generate** — `make templ`. This installs the pinned `templ` CLI (`Makefile:39`, `TEMPL_VERSION := v0.3.1020`) and runs `templ generate`, producing `web/insights_templ.go` and refreshing `web/layout_templ.go`. Expected: command exits 0; `git status --porcelain web/` shows `?? web/insights.templ`, `?? web/insights_templ.go`, ` M web/layout.templ`, ` M web/layout_templ.go`.

- [ ] **Step 4: Verify it builds** — `go build ./web/...`. Expected: no output, exit 0 (the new `InsightsPage`/`InsightsTable`/`InsightRow` symbols compile).

- [ ] **Step 5: Commit** — `git add web/insights.templ web/insights_templ.go web/layout.templ web/layout_templ.go` then `git commit -m "feat(web): insights templ view + nav link (ly-u4t.21)"`.

---

### Task 3: `/insights` + `/partial/insights` handlers and routes

**Files:**
- Create: `internal/api/insights.go`
- Modify: `internal/api/server.go` (`routes()`, lines 40-48)

Handler signatures match `func (s *Server) handleX(w http.ResponseWriter, r *http.Request)` (`dashboard.go:11,19`); the render is the same one-liner as `handleDashboard` (`dashboard.go:14`). `fetchInsights` follows the error-degrades-to-nil convention (`dashboard.go:29-31`, `audit.go:62-65`). The 30-day window matches `fetchTop` (`dashboard.go:27`). The proto alias is `lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"` (verified in use at `internal/ingest/server.go:21`). The verification for this task is the build (the render behavior is tested in Task 4).

- [ ] **Step 1: Create `internal/api/insights.go`** with this exact content:

```go
package api

import (
	"net/http"
	"time"

	"github.com/dobbo-ca/lynceus/internal/insight"
	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
	"github.com/dobbo-ca/lynceus/web"
)

// handleInsightsPage renders the full insights page.
func (s *Server) handleInsightsPage(w http.ResponseWriter, r *http.Request) {
	rows := s.fetchInsights(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.InsightsPage(rows).Render(r.Context(), w)
}

// handleInsightsPartial renders just the table fragment, used by HTMX for
// in-place auto-refresh.
func (s *Server) handleInsightsPartial(w http.ResponseWriter, r *http.Request) {
	rows := s.fetchInsights(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.InsightsTable(rows).Render(r.Context(), w)
}

// fetchInsights enumerates plan keys over the last 30 days, loads recent plans
// per key, runs the detection engine, and maps each Insight to a view-model.
// Errors degrade to nil rows (same convention as fetchTop, dashboard.go:29).
func (s *Server) fetchInsights(r *http.Request) []web.InsightRow {
	now := time.Now().UTC()
	since := now.AddDate(0, 0, -30) // same 30d window as fetchTop (dashboard.go:27)

	keys, err := s.stats.ListPlanKeys(r.Context(), since, now, 200)
	if err != nil {
		return nil
	}

	var out []web.InsightRow
	for _, k := range keys {
		plans, err := s.stats.TopPlansByQuery(r.Context(), k.ServerID, k.Fingerprint, since, now, 10)
		if err != nil {
			continue
		}
		qps := make([]*lynceusv1.QueryPlan, 0, len(plans))
		for _, p := range plans {
			qps = append(qps, p.Plan)
		}
		for _, in := range insight.DetectPlans(qps) {
			out = append(out, web.InsightRow{
				Kind:         string(in.Kind),
				Severity:     string(in.Severity),
				Fingerprint:  in.Fingerprint,
				Relation:     in.Relation,
				NodePath:     in.NodePath,
				RowsScanned:  in.RowsScanned,
				RowsReturned: in.RowsReturned,
				Detail:       in.Detail,
				ServerID:     k.ServerID,
			})
		}
	}
	return out
}
```

- [ ] **Step 2: Register the routes** — in `internal/api/server.go`, replace the `routes()` body (lines 40-48):

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

with:

```go
func (s *Server) routes() {
	s.mux.HandleFunc("GET /", s.handleDashboard)
	s.mux.HandleFunc("GET /partial/queries", s.handleQueriesPartial)
	s.mux.HandleFunc("GET /insights", s.handleInsightsPage)
	s.mux.HandleFunc("GET /partial/insights", s.handleInsightsPartial)
	s.mux.HandleFunc("GET /audit", s.handleAuditPage)
	s.mux.HandleFunc("GET /partial/audit", s.handleAuditPartial)
	s.mux.HandleFunc("GET /api/queries/top", s.handleTopQueries)
	s.mux.HandleFunc("GET /api/scim/v2/", s.notImplemented("SCIM"))
	s.mux.HandleFunc("GET /api/oidc/", s.notImplemented("OIDC"))
}
```

- [ ] **Step 3: Verify it builds** — `go build ./...`. Expected: no output, exit 0 (handlers, routes, and the `insight`/`web`/proto imports all resolve).

- [ ] **Step 4: Commit** — `git add internal/api/insights.go internal/api/server.go` then `git commit -m "feat(api): /insights + /partial/insights handlers and routes (ly-u4t.21)"`.

---

### Task 4: Render, fragment, 401, and privacy tests

**Files:**
- Create: `internal/api/insights_test.go` (package `api_test`)

These mirror the existing surface tests: render assertions copy `TestAuditPage_rendersRowsAndNav` (`audit_test.go:12-43`), the fragment test copies `TestAuditPartial_returnsFragmentOnly` (`audit_test.go:45-65`), the 401 test copies `TestAuditPage_withoutDevAuth_returns401` (`audit_test.go:87-97`), and the privacy assertion copies the forbidden-substring loop in `dashboard_test.go:43-51`. `seedPlans` mirrors `seedStats` (`server_test.go:80-93`) but uses `store.QueryPlanRow` with a `Seq Scan` child engineered so `DefaultSlowScan` fires: `MinRowsScanned=1000`, `MaxSelectivity=0.10` (`slowscan.go:18`). With `ActualLoops=1`, `ActualRows=5`, `RowsRemovedByFilter=9995` → `totalScanned=10000 ≥ 1000`, `sel = 5/10000 = 0.0005 ≤ 0.10` → fires at `SeverityHigh` (`slowscan.go:73`). The harness `setup(t, ...)` (applies stats migrations) and `newPGPool` come from `server_test.go:21-62`.

The privacy assertion plants a canary literal inside the seeded plan via the `NormalizedCondition` field, but the slow-scan `Detail` is templated from identifiers + counts only (`slowscan.go:61-65`) and `InsightRow` carries no condition field — so the canary must NOT reach the rendered HTML. Use a distinct canary string that is not a substring of any rendered identifier/count.

- [ ] **Step 1: Write the failing tests** — create `internal/api/insights_test.go` with this exact content:

```go
package api_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dobbo-ca/lynceus/internal/api"
	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
	"github.com/dobbo-ca/lynceus/internal/store"
)

// insightCanary is a literal that lives only inside the seeded plan's
// normalized-condition field. The slow-scan Detail is built from identifiers +
// counts only (slowscan.go:61-65) and InsightRow has no condition field, so
// this string must never reach the rendered HTML.
const insightCanary = "PHI-CANARY-INSIGHT-7f3a"

// seedPlans writes one stored plan whose Seq Scan child trips DefaultSlowScan
// (MinRowsScanned=1000, MaxSelectivity=0.10, slowscan.go:18). Mirrors seedStats
// (server_test.go:80) but for query_plans via store.WriteQueryPlans.
func seedPlans(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	s := store.NewStats(pool)
	capturedAt := time.Now().UTC().Add(-time.Hour)
	plan := &lynceusv1.QueryPlan{
		Fingerprint:    "fp-slowscan",
		CapturedAtUnix: capturedAt.Unix(),
		FormatVersion:  1,
		Root: &lynceusv1.PlanNode{
			NodeType: "Aggregate",
			Plans: []*lynceusv1.PlanNode{{
				NodeType:            "Seq Scan",
				RelationName:        "orders_audit",
				ActualLoops:         1,
				ActualRows:          5,
				RowsRemovedByFilter: 9995, // scanned 10000, returned 5 => sel 0.0005
				NormalizedCondition: "(email = '" + insightCanary + "')",
			}},
		},
	}
	rows := []store.QueryPlanRow{{ServerID: "srv-1", Plan: plan, CapturedAt: capturedAt}}
	if err := s.WriteQueryPlans(ctx, rows); err != nil {
		t.Fatalf("seed plans: %v", err)
	}
}

func TestInsightsPage_rendersDetectedInsights(t *testing.T) {
	pool, srv := setup(t, api.Config{DevAuth: true})
	seedPlans(t, pool)

	resp, err := http.Get(srv.URL + "/insights")
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
		"<!doctype html>",             // full page (templ emits lowercase)
		`id="insights-table"`,         // HTMX swap target
		`hx-get="/partial/insights"`,  // poll target
		`href="/insights"`,            // nav link
		"orders_audit",                // seeded relation
		"slow_scan",                   // KindSlowScan (insight.go:16)
		"high",                        // SeverityHigh (slowscan.go:73)
	} {
		if !strings.Contains(html, want) {
			t.Errorf("insights page missing %q", want)
		}
	}

	// THE PRIVACY GUARANTEE on the rendered surface: the canary that lived only
	// in the plan's normalized-condition must NOT appear in the rendered HTML.
	if strings.Contains(html, insightCanary) {
		t.Errorf("LITERAL LEAK in rendered HTML: contains %q", insightCanary)
	}
}

func TestInsightsPartial_returnsFragmentOnly(t *testing.T) {
	pool, srv := setup(t, api.Config{DevAuth: true})
	seedPlans(t, pool)

	resp, err := http.Get(srv.URL + "/partial/insights")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	html := string(body)
	if strings.Contains(html, "<!doctype html>") {
		t.Error("partial returned a full document; expected a fragment only")
	}
	if !strings.Contains(html, `id="insights-table"`) {
		t.Error("partial missing the swap-target id (HTMX outerHTML reswap would break)")
	}
	if !strings.Contains(html, "orders_audit") {
		t.Error("partial missing seeded insight row")
	}
}

func TestInsights_withoutDevAuth_returns401(t *testing.T) {
	_, srv := setup(t, api.Config{DevAuth: false})
	resp, err := http.Get(srv.URL + "/insights")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}
```

- [ ] **Step 2: Run tests to verify they pass** — `go test ./internal/api/ -run 'TestInsights'`. (At this point Tasks 1-3 are already merged, so these compile and should PASS immediately; this is the verification step for the whole feature.) Expected: `ok  github.com/dobbo-ca/lynceus/internal/api` with `TestInsightsPage_rendersDetectedInsights`, `TestInsightsPartial_returnsFragmentOnly`, and `TestInsights_withoutDevAuth_returns401` all passing. (Or `SKIP` if Docker is unavailable — re-run once Docker is up; do not claim done on a skip.)

  - If `TestInsightsPage_rendersDetectedInsights` fails because no row rendered (the `"orders_audit"`/`"slow_scan"`/`"high"` asserts), the slow-scan fixture is not tripping the detector — recheck the `ActualLoops`/`ActualRows`/`RowsRemovedByFilter` math against `slowscan.go:30-50` before changing anything else (use superpowers:systematic-debugging).

- [ ] **Step 3: Run the full package + store suites to confirm no regression** — `go test ./internal/api/ ./internal/store/`. Expected: both packages `ok` (or `SKIP` under no-Docker). This re-runs the existing dashboard/audit/queries tests and the `ListPlanKeys` test from Task 1 together.

- [ ] **Step 4: Commit** — `git add internal/api/insights_test.go` then `git commit -m "test(api): insights render, fragment, 401, and privacy tests (ly-u4t.21)"`.

---

## Verification before completion

Run these and confirm the actual output before claiming the bead complete (use superpowers:verification-before-completion):

- [ ] `go build ./...` → exit 0, no output.
- [ ] `make templ` → exit 0; `git status --porcelain web/` is clean (generated `web/*_templ.go` is current and already committed — CI assumes generated output is up to date, §4.3.5).
- [ ] `go test ./internal/api/ ./internal/store/ ./internal/insight/` → all `ok` (or `SKIP` only when Docker is unavailable, mirroring `server_test.go:33` `t.Skipf`). The `internal/insight` package is included to confirm the engine the surface depends on is unchanged.
- [ ] Spot-check privacy: the `TestInsightsPage_rendersDetectedInsights` privacy assertion passed (the `insightCanary` planted in `NormalizedCondition` never reached the HTML) — this is the load-bearing proof that the surface ships identifiers + counts only.

## Out of scope (do not implement here)

- The plan-visualization surface — `/plan`, `/partial/plan`, `web/plan.templ`, `web/plan_vm.go` (`ToPlanVM`/`flatten`), `PlanVM`/`PlanNodeVM`, the `.badge.sev-*`/`ul.plan-tree` CSS, and `plan_test.go`. That is bead ly-xqf.10 (spec §4.3.2-§4.3.3 plan-viz half) and is a separate unit of work.
- Any proto change or `contract_test.go` edit — this plan adds no proto field and reads only the already-allowlisted `server_id`/`fingerprint` columns.
- A time-window filter form — the 30-day window is hardcoded to match `fetchTop` (spec §4.3.5 risk; acceptable for MVP).
- Replacing the N+1 `ListPlanKeys`→`TopPlansByQuery` read with a single join — deliberate, bounded by `LIMIT 200` keys × 10 plans, replica-served (spec §4.3.5 risk).
