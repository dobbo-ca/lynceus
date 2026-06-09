# Index Advisor (`ly-u4t.12`) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development or superpowers:executing-plans. Checkbox steps.

**Goal:** Recommend missing single/multi-column indexes from stored EXPLAIN plans — a server-tier advisor, **no HypoPG** — surfaced at `/index-advisor`.

**Architecture:** A pure `internal/advisor` package walks normalized (literal-free) plan trees for **Seq Scan** nodes that carry a `normalized_condition`. A Seq Scan filtering on column(s) is direct evidence the planner found **no usable index** for that predicate, so each becomes an index candidate; candidates are aggregated per (relation, column-set), ranked by table size + seq-scan cost from `LatestTableStats`. The api server fetches plans (`ListPlanKeys`+`TopPlansByQuery`) and table stats, calls the pure advisor, renders a templ page. Collector untouched (api-side analysis over the stats store).

**Tech Stack:** Go, templ. No new proto, no new DB migration, no testcontainers (advisor is pure; api page test uses the existing in-memory `Server` test harness like `insights_test.go`).

**Privacy:** the advisor reads only `normalized_condition` (already proven literal-free at extraction) and aggregate counts. Recommended column names are schema identifiers, not literals. No literal can reach the page.

---

## File Structure
- Create `internal/advisor/index.go` — pure recommender + column extractor.
- Create `internal/advisor/index_test.go` — table tests.
- Create `internal/api/index_advisor.go` — handler `handleIndexAdvisorPage` / `handleIndexAdvisorPartial`, fetch + map.
- Create `internal/api/index_advisor_test.go` — page/partial test on the existing harness.
- Create `web/index_advisor.templ` (+ generated `_templ.go`) — page + table fragment.
- Modify `internal/api/server.go:50-55` — register `GET /index-advisor`, `GET /partial/index-advisor`.
- Modify `web/layout.templ:51-55` — nav link `<a href="/index-advisor">Index advisor</a>`.

---

## Task 1: Column extractor (pure)

**Files:** `internal/advisor/index.go`, `internal/advisor/index_test.go`.

The extractor pulls candidate index columns out of a normalized condition. Equality columns (`col = $n`, `col IN (...)`, `col = ANY(...)`) are preferred btree leading columns; range columns (`<`, `<=`, `>`, `>=`, `BETWEEN`) come after. Strip any `rel.` / `alias.` qualifier. Input only ever contains identifiers, operators, parens, `AND`/`OR`, and `$n` placeholders.

- [ ] **Step 1: Failing test:**

```go
package advisor

import (
	"reflect"
	"testing"
)

func TestFilterColumns_equalityBeforeRange(t *testing.T) {
	got := filterColumns("((orders.status = $1) AND (orders.created_at > $2))")
	want := []string{"status", "created_at"} // equality first, then range
	if !reflect.DeepEqual(got, want) {
		t.Errorf("filterColumns = %v, want %v", got, want)
	}
}

func TestFilterColumns_dedupesAndStripsQualifier(t *testing.T) {
	got := filterColumns("(a.user_id = $1) AND (user_id = $2)")
	want := []string{"user_id"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("filterColumns = %v, want %v", got, want)
	}
}

func TestFilterColumns_empty(t *testing.T) {
	if got := filterColumns(""); got != nil {
		t.Errorf("filterColumns(empty) = %v, want nil", got)
	}
}
```

- [ ] **Step 2: Implement** in `index.go`:

```go
package advisor

import "regexp"

// colCmp matches "<optional qualifier.>column <op>" in a normalized condition.
// The condition is already literal-free (only identifiers, ops, parens, $n).
var colCmp = regexp.MustCompile(`(?i)([a-z_][a-z0-9_]*)\s*(=|<>|!=|<=|>=|<|>|~~|between|in)\b`)

// filterColumns returns candidate index columns from a normalized predicate,
// equality/membership columns first (best btree leading columns) then range
// columns, de-duplicated, qualifiers stripped. The op-less left identifier of
// each comparison is the column (right side is always a $n placeholder).
func filterColumns(cond string) []string {
	var eq, rng []string
	seen := map[string]bool{}
	for _, m := range colCmp.FindAllStringSubmatch(cond, -1) {
		col, op := m[1], m[2]
		if seen[col] {
			continue
		}
		seen[col] = true
		switch op {
		case "<", ">", "<=", ">=", "between", "BETWEEN":
			rng = append(rng, col)
		default: // =, <>, !=, in, ~~ (LIKE) -> treat as equality-ish leading
			eq = append(eq, col)
		}
	}
	if len(eq)+len(rng) == 0 {
		return nil
	}
	return append(eq, rng...)
}
```

> Note: the regex's left identifier could in principle match an `AND`/`OR` keyword followed by an operator — impossible in valid normalized SQL, but the `seen` map + the fact that keywords are never immediately left of a comparison op make this safe in practice. Keep the regex anchored on the identifier-then-operator shape.

- [ ] **Step 3: Run** `go test ./internal/advisor/... -run TestFilterColumns -count=1` → PASS.
- [ ] **Step 4: Commit** `git commit -am "feat(advisor): normalized-condition column extractor (ly-u4t.12)"`

---

## Task 2: Index recommender (pure)

**Files:** `internal/advisor/index.go`, `internal/advisor/index_test.go`.

- [ ] **Step 1: Types + failing test:**

```go
// in index_test.go
func TestRecommendIndexes_aggregatesAndRanks(t *testing.T) {
	plans := []*lynceusv1.QueryPlan{
		planWithSeqScan("orders", "(status = $1)", "fp1"),
		planWithSeqScan("orders", "(status = $1)", "fp2"), // same candidate, 2 fps
		planWithSeqScan("tiny", "(flag = $1)", "fp3"),
	}
	tables := map[string]TableInfo{
		"orders": {TotalBytes: 500 << 20, SeqScans: 9000}, // big + hot
		"tiny":   {TotalBytes: 8 << 10, SeqScans: 3},       // small + cold
	}
	recs := RecommendIndexes(plans, tables)
	if len(recs) != 2 {
		t.Fatalf("recs = %d, want 2: %+v", len(recs), recs)
	}
	if recs[0].Relation != "orders" { // ranked first (bigger, hotter)
		t.Errorf("top relation = %q, want orders", recs[0].Relation)
	}
	if got := recs[0].Columns; len(got) != 1 || got[0] != "status" {
		t.Errorf("columns = %v, want [status]", got)
	}
	if recs[0].QueryCount != 2 {
		t.Errorf("query count = %d, want 2", recs[0].QueryCount)
	}
}

// planWithSeqScan builds a one-node QueryPlan with a Seq Scan carrying the
// given normalized condition (test helper).
func planWithSeqScan(rel, cond, fp string) *lynceusv1.QueryPlan {
	return &lynceusv1.QueryPlan{Fingerprint: fp, Root: &lynceusv1.PlanNode{
		NodeType: "Seq Scan", RelationName: rel, NormalizedCondition: cond,
		ActualRows: 1000, ActualLoops: 1,
	}}
}
```

- [ ] **Step 2: Implement:**

```go
import (
	"fmt"
	"sort"
	"strings"

	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
)

// TableInfo is the size + scan signal the advisor ranks candidates by, fed in
// from store.TableStatRow (the api handler maps it). Decoupled from store so
// the recommender stays pure + trivially testable.
type TableInfo struct {
	TotalBytes int64
	SeqScans   int64
}

// IndexRecommendation is one suggested index, T1-safe (identifiers + counts).
type IndexRecommendation struct {
	Relation     string
	Columns      []string
	QueryCount   int      // distinct fingerprints whose Seq Scan filters this way
	TotalBytes   int64
	SeqScans     int64
	Fingerprints []string
	Rationale    string
}

// RecommendIndexes walks each plan for Seq Scan nodes with a normalized filter,
// turns each (relation, columns) into a candidate, aggregates across plans, and
// ranks by table size * seq-scan frequency (biggest, hottest first).
func RecommendIndexes(plans []*lynceusv1.QueryPlan, tables map[string]TableInfo) []IndexRecommendation {
	type agg struct {
		cols []string
		fps  map[string]bool
	}
	cand := map[string]*agg{} // key: relation + "\x00" + strings.Join(cols,",")
	for _, qp := range plans {
		walk(qp.GetRoot(), func(n *lynceusv1.PlanNode) {
			if n.GetNodeType() != "Seq Scan" || n.GetRelationName() == "" {
				return
			}
			cols := filterColumns(n.GetNormalizedCondition())
			if len(cols) == 0 {
				return
			}
			key := n.GetRelationName() + "\x00" + strings.Join(cols, ",")
			a := cand[key]
			if a == nil {
				a = &agg{cols: cols, fps: map[string]bool{}}
				cand[key] = a
			}
			if fp := qp.GetFingerprint(); fp != "" {
				a.fps[fp] = true
			}
		})
	}

	var out []IndexRecommendation
	for key, a := range cand {
		rel := key[:strings.IndexByte(key, 0)]
		ti := tables[rel]
		out = append(out, IndexRecommendation{
			Relation:     rel,
			Columns:      a.cols,
			QueryCount:   len(a.fps),
			TotalBytes:   ti.TotalBytes,
			SeqScans:     ti.SeqScans,
			Fingerprints: sortedKeys(a.fps),
			Rationale: fmt.Sprintf(
				"%d quer(ies) seq-scan %s filtering on (%s); no usable index exists for that predicate.",
				len(a.fps), rel, strings.Join(a.cols, ", "),
			),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		si := out[i].TotalBytes * (out[i].SeqScans + 1)
		sj := out[j].TotalBytes * (out[j].SeqScans + 1)
		if si != sj {
			return si > sj
		}
		return out[i].Relation < out[j].Relation // stable tiebreak
	})
	return out
}

func walk(n *lynceusv1.PlanNode, fn func(*lynceusv1.PlanNode)) {
	if n == nil {
		return
	}
	fn(n)
	for _, c := range n.GetPlans() {
		walk(c, fn)
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
```

- [ ] **Step 3: Run** `go test ./internal/advisor/... -count=1` → PASS.
- [ ] **Step 4: Commit** `git commit -am "feat(advisor): index recommender over Seq Scan plan evidence (ly-u4t.12)"`

---

## Task 3: API surfacing + page

**Files:** `internal/api/index_advisor.go`, `internal/api/index_advisor_test.go`, `web/index_advisor.templ`, modify `server.go`, `web/layout.templ`.

- [ ] **Step 1: View-model + templ** `web/index_advisor.templ` (mirror `insights.templ`):

```go
package web

import "fmt"

// IndexAdvisorRow is the view-model for one recommendation. Identifiers + counts
// only — no literal.
type IndexAdvisorRow struct {
	Relation   string
	Columns    string // "col_a, col_b"
	QueryCount int
	SizePretty string // e.g. "500 MB"
	SeqScans   int64
	Rationale  string
}

templ IndexAdvisorPage(rows []IndexAdvisorRow) {
	@Layout("Lynceus — index advisor", "missing-index suggestions from plan evidence") {
		<p hx-get="/partial/index-advisor" hx-trigger="every 30s" hx-target="#idx-table" hx-swap="outerHTML"></p>
		@IndexAdvisorTable(rows)
	}
}

templ IndexAdvisorTable(rows []IndexAdvisorRow) {
	<div id="idx-table">
		if len(rows) == 0 {
			<p class="empty">No index suggestions — no seq-scanned filters in the last 30 days.</p>
		} else {
			<table>
				<thead><tr><th>Relation</th><th>Suggested columns</th><th class="num">Queries</th><th class="num">Table size</th><th class="num">Seq scans</th><th>Rationale</th></tr></thead>
				<tbody>
					for _, r := range rows {
						<tr>
							<td><code>{ r.Relation }</code></td>
							<td><code>{ r.Columns }</code></td>
							<td class="num">{ fmt.Sprint(r.QueryCount) }</td>
							<td class="num">{ r.SizePretty }</td>
							<td class="num">{ fmt.Sprint(r.SeqScans) }</td>
							<td>{ r.Rationale }</td>
						</tr>
					}
				</tbody>
			</table>
		}
	</div>
}
```

- [ ] **Step 2: Handler** `internal/api/index_advisor.go`:

```go
package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/dobbo-ca/lynceus/internal/advisor"
	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/dobbo-ca/lynceus/web"
)

func (s *Server) handleIndexAdvisorPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.IndexAdvisorPage(s.fetchIndexAdvice(r)).Render(r.Context(), w)
}

func (s *Server) handleIndexAdvisorPartial(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.IndexAdvisorTable(s.fetchIndexAdvice(r)).Render(r.Context(), w)
}

func (s *Server) fetchIndexAdvice(r *http.Request) []web.IndexAdvisorRow {
	now := time.Now().UTC()
	since := now.AddDate(0, 0, -30)
	keys, err := s.stats.ListPlanKeys(r.Context(), since, now, 200)
	if err != nil {
		return nil
	}
	var plans []*lynceusv1.QueryPlan
	tables := map[string]advisor.TableInfo{}
	servers := map[string]bool{}
	for _, k := range keys {
		ps, err := s.stats.TopPlansByQuery(r.Context(), k.ServerID, k.Fingerprint, since, now, 10)
		if err != nil {
			continue
		}
		for _, p := range ps {
			plans = append(plans, p.Plan)
		}
		servers[k.ServerID] = true
	}
	for srv := range servers {
		for _, ts := range latestTableStats(r, s, srv, now) {
			ti := tables[ts.ObjectName]
			ti.TotalBytes = ts.TotalBytes
			ti.SeqScans = ts.SeqScan
			tables[ts.ObjectName] = ti
		}
	}
	var out []web.IndexAdvisorRow
	for _, rec := range advisor.RecommendIndexes(plans, tables) {
		out = append(out, web.IndexAdvisorRow{
			Relation:   rec.Relation,
			Columns:    strings.Join(rec.Columns, ", "),
			QueryCount: rec.QueryCount,
			SizePretty: prettyBytes(rec.TotalBytes),
			SeqScans:   rec.SeqScans,
			Rationale:  rec.Rationale,
		})
	}
	return out
}

// latestTableStats is a thin wrapper so a missing reader degrades to empty.
func latestTableStats(r *http.Request, s *Server, serverID string, asOf time.Time) []store.TableStatRow {
	rows, err := s.stats.LatestTableStats(r.Context(), serverID, asOf)
	if err != nil {
		return nil
	}
	return rows
}
```

> `prettyBytes` — if no humanize helper exists in `internal/api`, add a tiny one in this file:

```go
func prettyBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
```
(add `"fmt"` to imports). If a byte-formatter already exists in `web` or `internal/api` (grep first: `grep -rn "func.*[Bb]ytes" internal/api web`), reuse it instead of adding this.

- [ ] **Step 3: Register routes** in `server.go` after the `/plan` lines:

```go
	s.mux.HandleFunc("GET /index-advisor", s.handleIndexAdvisorPage)
	s.mux.HandleFunc("GET /partial/index-advisor", s.handleIndexAdvisorPartial)
```

- [ ] **Step 4: Nav link** in `web/layout.templ` nav block:

```html
				<a href="/index-advisor">Index advisor</a>
```

- [ ] **Step 5: Failing/passing test** `internal/api/index_advisor_test.go` — mirror `insights_test.go`'s harness (construct `Server` with a test `*store.Stats`, GET `/index-advisor`, assert 200 + nav link + `hx-get="/partial/index-advisor"`; GET `/partial/index-advisor` → 200, contains `<table>` or the empty-state). Copy the exact harness constructor from `insights_test.go`.

- [ ] **Step 6: Generate + build + test:**

```
make templ && go build ./... && go test ./internal/advisor/... ./internal/api/... -count=1
```
Expected: PASS.

- [ ] **Step 7: Commit** `git commit -am "feat(api): /index-advisor page surfacing recommendations (ly-u4t.12)"`

---

## Task 4: Verify
- [ ] `go test ./... -count=1 -timeout 600s` → PASS.
- [ ] Arch grep: `grep -rn "pgxpool.New" cmd/ internal/collector` → exactly one. Advisor + handler are api-side reads only.
- [ ] Privacy: `grep -rn "NormalizedCondition\|filterColumns" internal/advisor` — advisor consumes the normalized field but emits only extracted column **identifiers**; confirm no `$` placeholder or operator leaks into `IndexRecommendation.Columns` (the extractor returns bare identifiers).

## Self-Review
- Spec: single + multi-column (extractor returns ordered column list), per-query (QueryCount/Fingerprints), server-tier, no HypoPG. ✔
- Note/limitation to record in bead: does not yet dedupe against existing indexes (needs per-table index list `ly-xqf.7`); Seq-Scan-as-evidence sidesteps most false positives. ✔
