# Plan Visualization Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `/plan?server=<id>&fp=<fingerprint>` SSR surface that loads the most-recent stored plan via `store.TopPlansByQuery(..., 1)` and renders it as a recursive `PlanNode` tree plus a flat node grid, with a missing-key empty state.

**Architecture:** A thin, stateless read surface over the existing stats store: a `web/plan_vm.go` pure-Go mapper (`ToPlanVM` + depth-first `flatten`) turns the existing `*lynceusv1.QueryPlan` proto into a `PlanVM` view-model so the proto import stays out of both the `.templ` and the `internal/api` handler; `web/plan.templ` renders a self-recursive `PlanTreeNode` component and a flat grid; two handlers in `internal/api/plan.go` (`GET /plan`, `GET /partial/plan`) mirror the existing dashboard/audit pattern and inherit the `withAuth` dev-auth gate. No proto changes, no store schema changes — it reuses `TopPlansByQuery` and its `QueryPlanRow.Plan` field.

**Tech Stack:** Go, protobuf (`make proto`), pgx/pgxpool, templ+HTMX (where relevant), testcontainers (`postgres:16`) for integration tests.

**Bead:** ly-xqf.10  ·  **Spec:** docs/specs/2026-06-08-layer0-foundation.md  ·  **Layer:** 0 Foundation

---

## File Structure

| File | Action | Responsibility |
|---|---|---|
| `web/plan_vm.go` | **Create** | Pure-Go view-models `PlanVM`/`PlanNodeVM` + `ToPlanVM(serverID string, p *lynceusv1.QueryPlan) PlanVM` (nil-safe proto getters) + depth-first `flatten` populating `PlanVM.Flat`. Keeps the proto import out of the `.templ` and the `api` handler. |
| `web/plan_vm_test.go` | **Create** | `package web` unit tests for `ToPlanVM` (tree shape, recursion, nil/empty plan) and `flatten` (DFS order). No DB. |
| `web/plan.templ` | **Create** | `PlanPage` (wraps `@Layout`), `PlanView` (HTMX `outerHTML` fragment with `id="plan-view"`), self-recursive `PlanTreeNode`, and a flat `<table>` grid over `vm.Flat`; missing-key empty state. |
| `web/plan_templ.go` | **Create (generated)** | `templ generate` output for `web/plan.templ`. DO-NOT-EDIT. |
| `web/layout.templ` | **Modify** (`web/layout.templ:26-43` `<style>`; `:47-50` `<nav>`) | Add `ul.plan-tree` CSS for the tree indentation; add a `<a href="/insights">Insights</a>`-style nav anchor? **No** — only add the `.plan-tree` CSS. Nav link to `/plan` requires a fingerprint, so no static nav entry is added (consistent with `/audit` having one but `/plan` being parameterized). |
| `internal/api/plan.go` | **Create** | `handlePlanPage` + `handlePlanPartial` + `fetchPlan(r) web.PlanVM`. Mirrors `internal/api/dashboard.go`. Handlers live in `plan.go` (not `insights.go`, which is owned by ly-u4t.21). |
| `internal/api/server.go` | **Modify** (`internal/api/server.go:40-48` `routes()`) | Register `GET /plan` and `GET /partial/plan`. |
| `internal/api/plan_test.go` | **Create** | `package api_test` integration tests: tree+grid render, partial fragment, recursion, missing-key empty state, plus a `seedPlans` helper. Uses testcontainers. |

---

## Tasks

### Task 1: View-model mapper `web/plan_vm.go` (`ToPlanVM` + DFS `flatten`)

**Files:**
- Create: `web/plan_vm.go`
- Test: `web/plan_vm_test.go`

The view-models live in the `web` package exactly like `AuditRow` (`web/audit.templ:8-16`) and `TopQuery` (`web/layout.templ:11-16`). The mapper uses only the nil-safe generated getters verified at `internal/proto/lynceus/v1/plan.pb.go:85-309` (`GetRoot`, `GetNodeType`, `GetRelationName`, `GetIndexName`, `GetJoinType`, `GetScanDirection`, `GetPlanRows`, `GetActualRows`, `GetActualLoops`, `GetTotalCost`, `GetActualTotalTimeMs`, `GetRowsRemovedByFilter`, `GetNormalizedCondition`, `GetPlans`). None of these carry a literal (contract enforced — `proto/lynceus/v1/plan.proto:9-13`).

- [ ] **Step 1: Write the failing test** — create `web/plan_vm_test.go` with the COMPLETE code:

```go
package web

import (
	"testing"

	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
)

func TestToPlanVM_buildsTreeAndFlatDFS(t *testing.T) {
	plan := &lynceusv1.QueryPlan{
		Fingerprint:       "fp-1",
		FormatVersion:     1,
		TotalCost:         102.84,
		ActualTotalTimeMs: 12.5,
		Root: &lynceusv1.PlanNode{
			NodeType:  "Aggregate",
			TotalCost: 102.84,
			PlanRows:  1,
			Plans: []*lynceusv1.PlanNode{
				{
					NodeType:            "Hash Join",
					JoinType:            "Inner",
					TotalCost:           99.0,
					PlanRows:            2532,
					NormalizedCondition: "(a.id = b.id)",
					Plans: []*lynceusv1.PlanNode{
						{NodeType: "Seq Scan", RelationName: "orders", TotalCost: 50.0, PlanRows: 2532, ActualRows: 2532, ActualLoops: 1, RowsRemovedByFilter: 17, NormalizedCondition: "(total > $1)"},
						{NodeType: "Index Scan", RelationName: "customers", IndexName: "customers_pkey", TotalCost: 8.3, PlanRows: 1, ActualRows: 1, ActualLoops: 1},
					},
				},
			},
		},
	}

	vm := ToPlanVM("srv-1", plan)

	if vm.ServerID != "srv-1" || vm.Fingerprint != "fp-1" {
		t.Fatalf("header = (%q,%q), want (srv-1,fp-1)", vm.ServerID, vm.Fingerprint)
	}
	if vm.Empty {
		t.Fatal("vm.Empty = true, want false for a populated plan")
	}
	// Tree shape: root -> Hash Join -> [Seq Scan, Index Scan]
	if vm.Root == nil || vm.Root.NodeType != "Aggregate" {
		t.Fatalf("root node = %+v, want Aggregate", vm.Root)
	}
	if len(vm.Root.Children) != 1 || vm.Root.Children[0].NodeType != "Hash Join" {
		t.Fatalf("root children = %+v, want one Hash Join", vm.Root.Children)
	}
	hj := vm.Root.Children[0]
	if hj.JoinType != "Inner" || hj.Condition != "(a.id = b.id)" {
		t.Errorf("hash join = (join=%q,cond=%q)", hj.JoinType, hj.Condition)
	}
	if len(hj.Children) != 2 {
		t.Fatalf("hash join children = %d, want 2", len(hj.Children))
	}
	if hj.Children[0].Relation != "orders" || hj.Children[1].Index != "customers_pkey" {
		t.Errorf("join children rel/idx = (%q,%q)", hj.Children[0].Relation, hj.Children[1].Index)
	}
	// Flat is depth-first pre-order: Aggregate, Hash Join, Seq Scan, Index Scan.
	wantOrder := []string{"Aggregate", "Hash Join", "Seq Scan", "Index Scan"}
	if len(vm.Flat) != len(wantOrder) {
		t.Fatalf("flat len = %d, want %d", len(vm.Flat), len(wantOrder))
	}
	for i, w := range wantOrder {
		if vm.Flat[i].NodeType != w {
			t.Errorf("flat[%d] = %q, want %q", i, vm.Flat[i].NodeType, w)
		}
	}
	// Depth is set so the tree can indent.
	if vm.Flat[2].Depth != 2 {
		t.Errorf("Seq Scan depth = %d, want 2", vm.Flat[2].Depth)
	}
	// A count field round-trips (literal-free).
	if vm.Flat[2].RowsRemovedByFilter != 17 {
		t.Errorf("Seq Scan RowsRemovedByFilter = %d, want 17", vm.Flat[2].RowsRemovedByFilter)
	}
}

func TestToPlanVM_nilPlanIsEmpty(t *testing.T) {
	vm := ToPlanVM("srv-1", nil)
	if !vm.Empty {
		t.Fatal("nil plan: Empty = false, want true")
	}
	if vm.ServerID != "srv-1" {
		t.Errorf("ServerID = %q, want srv-1 (header preserved on empty)", vm.ServerID)
	}
	if vm.Root != nil || len(vm.Flat) != 0 {
		t.Errorf("nil plan should yield no root and no flat rows")
	}
}

func TestToPlanVM_nilRootIsEmpty(t *testing.T) {
	// A QueryPlan with no Root node (GetRoot() == nil) is also empty.
	vm := ToPlanVM("srv-1", &lynceusv1.QueryPlan{Fingerprint: "fp-x"})
	if !vm.Empty {
		t.Fatal("plan with nil root: Empty = false, want true")
	}
	if vm.Fingerprint != "fp-x" {
		t.Errorf("Fingerprint = %q, want fp-x", vm.Fingerprint)
	}
}
```

- [ ] **Step 2: Run test to verify it fails** — exact command + expected failure:

```
go test ./web/ -run TestToPlanVM
```

Expected: a build failure because `ToPlanVM`, `PlanVM`, `PlanNodeVM` do not exist yet, e.g.:

```
web/plan_vm_test.go:NN:8: undefined: ToPlanVM
web/plan_vm_test.go:NN:5: undefined: PlanVM (or vm.Root / vm.Flat unknown)
FAIL	github.com/dobbo-ca/lynceus/web [build failed]
```

- [ ] **Step 3: Implement** — create `web/plan_vm.go` with the COMPLETE code:

```go
package web

import (
	"fmt"

	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
)

// PlanNodeVM is the view-model for one node in the plan tree. Every field
// is a structural identifier, a normalized (literal-free) condition, a
// count, a size, or a metric — never a query literal (mirrors the proto
// invariant, proto/lynceus/v1/plan.proto:9-13).
type PlanNodeVM struct {
	Depth               int    // 0 = root; used to indent the flat grid
	NodeType            string // "Seq Scan", "Hash Join", ...
	Relation            string // table identifier, "" if none
	Index               string // index identifier, "" if none
	JoinType            string // "Inner" | "Left" | "", structural
	ScanDirection       string // "Forward" | "Backward" | ""
	Condition           string // normalized condition ($n), "" if not provable
	PlanRows            int64
	ActualRows          int64
	ActualLoops         int64
	TotalCost           float64
	ActualTotalTimeMs   float64
	RowsRemovedByFilter int64
	Children            []*PlanNodeVM
}

// PlanVM is the full view-model for the /plan surface. Empty drives the
// "no plan stored" branch in the template.
type PlanVM struct {
	ServerID    string
	Fingerprint string
	Empty       bool
	Root        *PlanNodeVM   // nil when Empty
	Flat        []*PlanNodeVM // depth-first pre-order, nil when Empty
}

// ToPlanVM maps a stored QueryPlan into the view-model. It is nil-safe: a
// nil plan or a plan with no root node yields an Empty PlanVM that still
// carries the requested ServerID/Fingerprint so the page can echo them.
func ToPlanVM(serverID string, p *lynceusv1.QueryPlan) PlanVM {
	vm := PlanVM{ServerID: serverID, Fingerprint: p.GetFingerprint()}
	root := p.GetRoot()
	if p == nil || root == nil {
		vm.Empty = true
		return vm
	}
	vm.Root = toNodeVM(root, 0)
	flatten(vm.Root, &vm.Flat)
	return vm
}

// toNodeVM converts one proto node (and its subtree) to a PlanNodeVM. All
// getters are nil-safe (plan.pb.go:192-309).
func toNodeVM(n *lynceusv1.PlanNode, depth int) *PlanNodeVM {
	node := &PlanNodeVM{
		Depth:               depth,
		NodeType:            n.GetNodeType(),
		Relation:            n.GetRelationName(),
		Index:               n.GetIndexName(),
		JoinType:            n.GetJoinType(),
		ScanDirection:       n.GetScanDirection(),
		Condition:           n.GetNormalizedCondition(),
		PlanRows:            n.GetPlanRows(),
		ActualRows:          n.GetActualRows(),
		ActualLoops:         n.GetActualLoops(),
		TotalCost:           n.GetTotalCost(),
		ActualTotalTimeMs:   n.GetActualTotalTimeMs(),
		RowsRemovedByFilter: n.GetRowsRemovedByFilter(),
	}
	for _, c := range n.GetPlans() {
		node.Children = append(node.Children, toNodeVM(c, depth+1))
	}
	return node
}

// flatten appends nodes depth-first (pre-order: visit node, then recurse
// into children) so the flat grid lists a node before its descendants.
func flatten(n *PlanNodeVM, out *[]*PlanNodeVM) {
	if n == nil {
		return
	}
	*out = append(*out, n)
	for _, c := range n.Children {
		flatten(c, out)
	}
}

// FmtCost renders a cost/metric for the grid; kept here so the .templ has
// no fmt import beyond what it already uses.
func FmtCost(v float64) string { return fmt.Sprintf("%.2f", v) }
```

- [ ] **Step 4: Run test to verify it passes** — exact command + expected PASS:

```
go test ./web/ -run TestToPlanVM
```

Expected:

```
ok  	github.com/dobbo-ca/lynceus/web	0.00Ns
```

(`PASS` for `TestToPlanVM_buildsTreeAndFlatDFS`, `TestToPlanVM_nilPlanIsEmpty`, `TestToPlanVM_nilRootIsEmpty`.)

- [ ] **Step 5: Commit**

```
git add web/plan_vm.go web/plan_vm_test.go
git commit -m "feat(web): PlanVM view-model + DFS flatten for plan viz (ly-xqf.10)"
```

---

### Task 2: `web/plan.templ` — recursive tree + flat grid + empty state

**Files:**
- Create: `web/plan.templ`
- Create (generated): `web/plan_templ.go` (via `templ generate`)
- Modify: `web/layout.templ` (`web/layout.templ:26-43` `<style>` block) — add `ul.plan-tree` CSS

The page mirrors `QueriesPage`/`QueriesTable` (`web/queries.templ:6-42`): `PlanPage` wraps `@Layout(...)` (`web/layout.templ:18`), and `PlanView` is an HTMX `outerHTML` self-reswap fragment carrying `id="plan-view"`, exactly like `QueriesTable`'s `id="queries-table"` (`web/queries.templ:16`). The recursive `PlanTreeNode` is the direct analogue of a self-recursive walk. Numeric formatting uses `fmt.Sprintf` like `web/audit.templ:97` / `web/queries.templ:34-35`.

- [ ] **Step 1: Write the failing test** — this task's verification is the render path, exercised end-to-end in Task 4's `plan_test.go`. For this task, the "test" is that `templ generate` produces compilable Go and `go build ./web/` succeeds. There is no separate unit test file for the `.templ`; proceed to Step 3 (the failing state is: `web/plan_templ.go` does not exist, so the `web.PlanPage`/`web.PlanView` symbols referenced by Task 3/4 are undefined).

  Confirm the pre-implementation failing state:

```
go build ./web/
```

  This currently SUCCEEDS (no new file references the missing symbols yet), so instead verify the symbols are absent:

```
grep -c 'func PlanPage' web/plan_templ.go 2>/dev/null || echo "ABSENT (expected before impl)"
```

  Expected output: `ABSENT (expected before impl)`.

- [ ] **Step 2: Add the `.plan-tree` CSS to `web/layout.templ`** — Edit the `<style>` block. Insert these lines immediately after the `form.filters button { ... }` line (`web/layout.templ:42`), before the closing `</style>` (`:43`):

```css
					ul.plan-tree { list-style: none; padding-left: 1.25rem; margin: 0.25rem 0; border-left: 1px solid #e0e0e0; }
					ul.plan-tree > li { margin: 0.15rem 0; }
					.node-type { font-weight: 600; }
					.node-meta { color: #666; font-size: 0.85rem; margin-left: 0.4rem; }
```

- [ ] **Step 3: Implement** — create `web/plan.templ` with the COMPLETE code:

```go
package web

import "fmt"

// PlanPage is the full plan-visualization page for one (server, fingerprint).
templ PlanPage(vm PlanVM) {
	@Layout("Lynceus — query plan", "most recent normalized plan") {
		<p>
			Server <code>{ vm.ServerID }</code> · fingerprint <code>{ vm.Fingerprint }</code>
		</p>
		@PlanView(vm)
	}
}

// PlanView is also served as a stand-alone HTMX fragment. The wrapping div
// carries the id used as the outerHTML swap target.
templ PlanView(vm PlanVM) {
	<div id="plan-view">
		if vm.Empty {
			<p class="empty">No plan stored for this server and fingerprint.</p>
		} else {
			<h2>Plan tree</h2>
			<ul class="plan-tree">
				@PlanTreeNode(vm.Root)
			</ul>
			<h2>Nodes</h2>
			<table>
				<thead>
					<tr>
						<th>Node type</th>
						<th>Relation</th>
						<th>Index</th>
						<th>Condition</th>
						<th class="num">Plan rows</th>
						<th class="num">Actual rows</th>
						<th class="num">Loops</th>
						<th class="num">Total cost</th>
						<th class="num">Rows removed</th>
					</tr>
				</thead>
				<tbody>
					for _, n := range vm.Flat {
						<tr>
							<td><code>{ n.NodeType }</code></td>
							<td>{ n.Relation }</td>
							<td>{ n.Index }</td>
							<td><code>{ n.Condition }</code></td>
							<td class="num">{ fmt.Sprintf("%d", n.PlanRows) }</td>
							<td class="num">{ fmt.Sprintf("%d", n.ActualRows) }</td>
							<td class="num">{ fmt.Sprintf("%d", n.ActualLoops) }</td>
							<td class="num">{ FmtCost(n.TotalCost) }</td>
							<td class="num">{ fmt.Sprintf("%d", n.RowsRemovedByFilter) }</td>
						</tr>
					}
				</tbody>
			</table>
		}
	</div>
}

// PlanTreeNode renders one node and recurses into its children. The
// self-reference makes the tree arbitrarily deep.
templ PlanTreeNode(n *PlanNodeVM) {
	<li>
		<span class="node-type">{ n.NodeType }</span>
		if n.Relation != "" {
			<span class="node-meta">on <code>{ n.Relation }</code></span>
		}
		if n.Index != "" {
			<span class="node-meta">using <code>{ n.Index }</code></span>
		}
		if n.JoinType != "" {
			<span class="node-meta">{ n.JoinType } join</span>
		}
		<span class="node-meta">cost { FmtCost(n.TotalCost) } · rows { fmt.Sprintf("%d", n.PlanRows) }</span>
		if len(n.Children) > 0 {
			<ul class="plan-tree">
				for _, c := range n.Children {
					@PlanTreeNode(c)
				}
			</ul>
		}
	</li>
}
```

- [ ] **Step 4: Generate templ Go + build** — run `templ generate` (installs the pinned CLI via the Makefile target) then build:

```
make templ && go build ./web/
```

Expected: `templ generate` reports it processed `web/plan.templ` (creating `web/plan_templ.go`) and `go build ./web/` exits 0 with no output. Confirm the generated file exists and carries the DO-NOT-EDIT header (mirrors `web/queries_templ.go:1`):

```
head -1 web/plan_templ.go
```

Expected: `// Code generated by templ - DO NOT EDIT.`

- [ ] **Step 5: Commit**

```
git add web/plan.templ web/plan_templ.go web/layout.templ
git commit -m "feat(web): recursive plan tree + node grid templ (ly-xqf.10)"
```

---

### Task 3: Handlers `internal/api/plan.go` + route registration

**Files:**
- Create: `internal/api/plan.go`
- Modify: `internal/api/server.go` (`internal/api/server.go:40-48` `routes()`)
- Test: covered by Task 4 (`internal/api/plan_test.go`)

`fetchPlan` parses `server` + `fp` query params (mirror `internal/api/audit.go:36-41` `q.Get(...)`), calls `s.stats.TopPlansByQuery(ctx, server, fp, since, until, 1)` (`internal/store/plans.go:69`, ORDER BY `captured_at DESC` so index 0 is most-recent), and returns `web.ToPlanVM(server, plan)`. On any error, or zero rows, it returns an empty `PlanVM` (error-degrades-to-empty convention — `internal/api/dashboard.go:29-30`, `internal/api/audit.go:62-65`). The handlers themselves are the same render one-liner as `handleDashboard` / `handleQueriesPartial` (`internal/api/dashboard.go:11-23`).

- [ ] **Step 1: Write the failing test** — the failing assertions live in Task 4's `plan_test.go`. For this task, the failing state is that `handlePlanPage` / `handlePlanPartial` and the routes do not exist. Verify:

```
go vet ./internal/api/ 2>&1 | head -5; grep -c 'handlePlanPage' internal/api/server.go || echo "ROUTE ABSENT (expected)"
```

Expected: `go vet` is clean (no references yet) and `ROUTE ABSENT (expected)` printed (the grep finds 0 matches → nonzero exit → the `echo` runs).

- [ ] **Step 2: Implement the handlers** — create `internal/api/plan.go` with the COMPLETE code:

```go
package api

import (
	"net/http"
	"time"

	"github.com/dobbo-ca/lynceus/web"
)

// handlePlanPage renders the full plan-visualization page for the
// (server, fingerprint) pair given in the query string.
func (s *Server) handlePlanPage(w http.ResponseWriter, r *http.Request) {
	vm := s.fetchPlan(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.PlanPage(vm).Render(r.Context(), w)
}

// handlePlanPartial renders just the plan-view fragment, for HTMX in-place
// swaps.
func (s *Server) handlePlanPartial(w http.ResponseWriter, r *http.Request) {
	vm := s.fetchPlan(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.PlanView(vm).Render(r.Context(), w)
}

// fetchPlan loads the most-recent stored plan for (?server, ?fp) and maps
// it to a view-model. A missing key, a read error, or zero rows all yield
// an Empty PlanVM that still echoes the requested identifiers, so the page
// can render its "no plan stored" branch.
func (s *Server) fetchPlan(r *http.Request) web.PlanVM {
	q := r.URL.Query()
	serverID := q.Get("server")
	fp := q.Get("fp")

	now := time.Now().UTC()
	since := now.AddDate(0, 0, -30) // same 30d window as fetchTop (dashboard.go:27)

	plans, err := s.stats.TopPlansByQuery(r.Context(), serverID, fp, since, now, 1)
	if err != nil || len(plans) == 0 {
		return web.ToPlanVM(serverID, nil) // Empty, identifiers preserved
	}
	return web.ToPlanVM(serverID, plans[0].Plan) // most-recent first
}
```

- [ ] **Step 3: Register the routes** — Edit `internal/api/server.go`. After the audit-partial line (`internal/api/server.go:44`), add the two plan routes:

  Replace:

```go
	s.mux.HandleFunc("GET /audit", s.handleAuditPage)
	s.mux.HandleFunc("GET /partial/audit", s.handleAuditPartial)
	s.mux.HandleFunc("GET /api/queries/top", s.handleTopQueries)
```

  With:

```go
	s.mux.HandleFunc("GET /audit", s.handleAuditPage)
	s.mux.HandleFunc("GET /partial/audit", s.handleAuditPartial)
	s.mux.HandleFunc("GET /plan", s.handlePlanPage)
	s.mux.HandleFunc("GET /partial/plan", s.handlePlanPartial)
	s.mux.HandleFunc("GET /api/queries/top", s.handleTopQueries)
```

- [ ] **Step 4: Build to verify it compiles** — exact command + expected output:

```
go build ./internal/api/ ./web/
```

Expected: exits 0, no output. (Full assertions run in Task 4.)

- [ ] **Step 5: Commit**

```
git add internal/api/plan.go internal/api/server.go
git commit -m "feat(api): /plan + /partial/plan handlers over TopPlansByQuery (ly-xqf.10)"
```

---

### Task 4: Integration tests — tree+grid render, partial fragment, recursion, missing-key empty state

**Files:**
- Test: `internal/api/plan_test.go` (Create)

Tests are `package api_test` and use the existing harness `setup(t, cfg)` (`internal/api/server_test.go:52-62`, which applies stats migrations and serves `NewServer(...).Handler()`). A new `seedPlans` helper mirrors `seedStats` (`internal/api/server_test.go:80-93`) but writes a `QueryPlan` with a `Seq Scan` child via `store.WriteQueryPlans` (`internal/store/plans.go:34`), using the fixture shape from `internal/store/plans_test.go:40-66`, with `CapturedAt = now-1h` so it falls inside the handler's 30-day window. The 401 + privacy patterns copy `internal/api/dashboard_test.go:11-51` and `internal/api/audit_test.go:87-99`.

- [ ] **Step 1: Write the failing test** — create `internal/api/plan_test.go` with the COMPLETE code:

```go
package api_test

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dobbo-ca/lynceus/internal/api"
	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
	"github.com/dobbo-ca/lynceus/internal/store"
)

// seedPlans writes one normalized plan for (srv, fp-plan) captured an hour
// ago, with a Seq Scan child under an Aggregate root so the tree has at
// least two levels and the grid has two rows. Mirrors seedStats
// (server_test.go:80) and the plans_test fixture (plans_test.go:40-66).
func seedPlans(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	s := store.NewStats(pool)
	now := time.Now().UTC().Add(-time.Hour)
	plan := &lynceusv1.QueryPlan{
		Fingerprint:       "fp-plan",
		CapturedAtUnix:    now.Unix(),
		FormatVersion:     1,
		TotalCost:         102.84,
		ActualTotalTimeMs: 0,
		Root: &lynceusv1.PlanNode{
			NodeType:  "Aggregate",
			TotalCost: 102.84,
			PlanRows:  1,
			Plans: []*lynceusv1.PlanNode{{
				NodeType:            "Seq Scan",
				RelationName:        "orders",
				TotalCost:           96.50,
				PlanRows:            2532,
				ActualRows:          2532,
				ActualLoops:         1,
				RowsRemovedByFilter: 88,
				NormalizedCondition: "(total > $1)",
			}},
		},
	}
	rows := []store.QueryPlanRow{{ServerID: "srv", Plan: plan, CapturedAt: now}}
	if err := s.WriteQueryPlans(ctx, rows); err != nil {
		t.Fatalf("seed plans: %v", err)
	}
}

func TestPlanPage_rendersTreeAndGrid(t *testing.T) {
	pool, srv := setup(t, api.Config{DevAuth: true})
	seedPlans(t, pool)

	resp, err := http.Get(srv.URL + "/plan?server=srv&fp=fp-plan")
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
		"<!doctype html>",       // full page (templ lowercases the doctype)
		`id="plan-view"`,        // HTMX swap target
		`class="plan-tree"`,     // the recursive tree container
		"Aggregate",             // root node type (tree + grid)
		"Seq Scan",              // child node type (recursion worked)
		"orders",                // relation identifier
		"Plan rows",             // grid header
		"(total &gt; $1)",       // normalized condition, HTML-escaped
	} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered HTML is missing %q", want)
		}
	}

	// PRIVACY: no raw literal may appear in the rendered surface.
	for _, forbidden := range []string{"leaky", "secret-value", "@example.com"} {
		if strings.Contains(html, forbidden) {
			t.Errorf("LITERAL LEAK in rendered HTML: contains %q", forbidden)
		}
	}
}

func TestPlanPartial_returnsFragmentOnly(t *testing.T) {
	pool, srv := setup(t, api.Config{DevAuth: true})
	seedPlans(t, pool)

	resp, err := http.Get(srv.URL + "/partial/plan?server=srv&fp=fp-plan")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	html := string(body)

	if strings.Contains(html, "<!doctype html>") || strings.Contains(html, "<!DOCTYPE html>") {
		t.Error("partial returned a full document; expected a fragment only")
	}
	if !strings.Contains(html, `id="plan-view"`) {
		t.Error("partial missing the swap-target id (HTMX outerHTML reswap would break)")
	}
	if !strings.Contains(html, "Seq Scan") {
		t.Error("partial missing seeded child node")
	}
}

func TestPlan_recursionRendersNestedTree(t *testing.T) {
	pool, srv := setup(t, api.Config{DevAuth: true})
	seedPlans(t, pool)

	resp, err := http.Get(srv.URL + "/partial/plan?server=srv&fp=fp-plan")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	html := string(body)

	// The recursive component nests <ul class="plan-tree"> inside the root
	// <ul class="plan-tree">, so the substring appears at least twice
	// (once for the root list, once for the child list).
	if got := strings.Count(html, `class="plan-tree"`); got < 2 {
		t.Errorf(`plan-tree count = %d, want >= 2 (recursion did not nest a child <ul>)`, got)
	}
}

func TestPlan_missingKey_rendersEmpty(t *testing.T) {
	pool, srv := setup(t, api.Config{DevAuth: true})
	seedPlans(t, pool) // seed a real plan so we know the empty branch is key-driven

	// A fingerprint that was never stored.
	u := srv.URL + "/plan?server=srv&fp=" + url.QueryEscape("does-not-exist")
	resp, err := http.Get(u)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	html := string(body)

	if !strings.Contains(html, "No plan stored") {
		t.Error("missing-key plan did not render the empty-state branch")
	}
	if strings.Contains(html, "Seq Scan") {
		t.Error("missing-key plan leaked the seeded plan's nodes")
	}
}

func TestPlan_withoutDevAuth_returns401(t *testing.T) {
	_, srv := setup(t, api.Config{DevAuth: false})
	resp, err := http.Get(srv.URL + "/plan?server=srv&fp=fp-plan")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}
```

- [ ] **Step 2: Run test to verify it fails (then passes after Tasks 1-3 land)** — exact command + expected. Because Tasks 1-3 are already implemented at this point, this suite should PASS on first run. To confirm the suite was wired correctly, first temporarily verify the build, then run:

```
go test ./internal/api/ -run 'TestPlan' -v
```

If Docker is unavailable, expect each test to be skipped (mirrors `internal/api/server_test.go:33` `t.Skipf`):

```
--- SKIP: TestPlanPage_rendersTreeAndGrid (0.NNs)
    server_test.go:33: docker/testcontainers unavailable: ...
```

If you want to SEE the red state first (recommended TDD discipline), stash the implementation files and run the suite before un-stashing:

```
git stash push internal/api/plan.go web/plan.templ web/plan_templ.go web/plan_vm.go
go test ./internal/api/ -run 'TestPlan' 2>&1 | head -5
git stash pop
```

Expected red output (with implementation stashed):

```
# github.com/dobbo-ca/lynceus/internal/api_test
internal/api/plan_test.go:NN: undefined: ... (or) no required module provides web.PlanPage
FAIL	github.com/dobbo-ca/lynceus/internal/api [build failed]
```

- [ ] **Step 3: Implement** — no new production code; Tasks 1-3 already provide it. (This task is test-only.)

- [ ] **Step 4: Run test to verify it passes** — exact command + expected PASS (with Docker available):

```
go test ./internal/api/ -run 'TestPlan' -v
```

Expected:

```
--- PASS: TestPlanPage_rendersTreeAndGrid (N.NNs)
--- PASS: TestPlanPartial_returnsFragmentOnly (N.NNs)
--- PASS: TestPlan_recursionRendersNestedTree (N.NNs)
--- PASS: TestPlan_missingKey_rendersEmpty (N.NNs)
--- PASS: TestPlan_withoutDevAuth_returns401 (N.NNs)
PASS
ok  	github.com/dobbo-ca/lynceus/internal/api	N.NNs
```

(If Docker is unavailable, all five report `SKIP` — that is an acceptable pass for this environment, matching the repo convention at `internal/api/server_test.go:33`.)

- [ ] **Step 5: Commit**

```
git add internal/api/plan_test.go
git commit -m "test(api): plan-viz tree/grid/recursion/empty-key integration tests (ly-xqf.10)"
```

---

### Task 5: Full-suite + generated-output verification

**Files:** none changed — verification only.

- [ ] **Step 1: Confirm generated templ output is current** — re-run the generator and confirm git sees no diff (CI assumes generated output is committed and current, per spec §4.3.5):

```
make templ && git diff --exit-code web/plan_templ.go
```

Expected: exits 0 with no diff output. (A nonzero exit means `web/plan_templ.go` was stale — re-commit it.)

- [ ] **Step 2: Build everything** — exact command + expected:

```
go build ./...
```

Expected: exits 0, no output.

- [ ] **Step 3: Run the web unit tests and the api integration tests** — exact command + expected:

```
go test ./web/ ./internal/api/
```

Expected: `ok` for both packages (api tests `SKIP` individually if Docker is unavailable, but the package still reports `ok`):

```
ok  	github.com/dobbo-ca/lynceus/web	0.0NNs
ok  	github.com/dobbo-ca/lynceus/internal/api	N.NNs
```

- [ ] **Step 4: Confirm no proto/store/contract changes were introduced** — this bead reuses `TopPlansByQuery` and adds no T1 message or field, so the proto contract test must be untouched and still green:

```
git diff --name-only origin/main...HEAD | grep -E 'proto|migrations|contract_test' && echo "UNEXPECTED proto/store change — investigate" || echo "OK: no proto/store/contract changes"
go test ./internal/proto/...
```

Expected: `OK: no proto/store/contract changes`, then `ok github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1`.

- [ ] **Step 5: Commit** — nothing new to commit if Step 1 showed no diff. If `make templ` produced a regeneration delta, commit it:

```
git add web/plan_templ.go
git commit -m "chore(web): regenerate plan templ output (ly-xqf.10)"
```

(If there is no diff, skip this commit — the work is already committed across Tasks 1-4.)
