# EXPLAIN Insight — Slow Scan (ly-u4t.7) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Detect the "Slow Scan" anti-pattern — a Seq Scan that reads many rows and discards most of them via a Filter — from an extracted (T1, literal-free) auto_explain query plan, and surface it as a structured `Insight`.

**Architecture:** This is the **first** of M3's 8 EXPLAIN insights, so it also builds the reusable insight engine the other 12 beads (`ly-u4t.*`) plug into. A new pure package `internal/insight` walks a `*lynceusv1.QueryPlan` tree and returns `[]Insight` — no DB, no I/O, callable from either the collector or the server. Detection is safe to run server-side over stored plans because the T1 `QueryPlan` is already normalized literal-free. The canonical signal for "discarding many rows" is Postgres's `Rows Removed by Filter` plan counter, which the current extractor drops; Task 1 adds it to the T1 `PlanNode` as a **count** (no literal — privacy contract preserved). Tasks 2–3 build the engine + SlowScan detector. The read-path entry (`store.TopPlansByQuery` → `insight.DetectPlans`) is wired by a later surfacing bead (`ly-u4t.21` / `ly-xqf.10`); this plan delivers and unit-tests the engine, not an HTTP endpoint.

**Tech Stack:** Go, protobuf (protoc v35.0, `make proto`), `internal/planextract` (existing JSON plan extractor), table-driven tests over real PG16 auto_explain JSON fixtures.

**Privacy invariant (non-negotiable):** every field added in this plan is a structural identifier or an aggregate count. No raw predicate, sample, or literal may enter `PlanNode`, `Insight`, or any generated `Detail` string. The contract test `internal/proto/lynceus/v1/contract_test.go` enforces the `PlanNode` field allowlist; Task 3 adds an insight-layer test asserting `Detail`/`NodePath` carry no literal.

---

## File Structure

- `proto/lynceus/v1/plan.proto` — **modify**: add `rows_removed_by_filter` field to `PlanNode`.
- `internal/proto/lynceus/v1/plan.pb.go` — **regenerated** by `make proto` (do not hand-edit).
- `internal/proto/lynceus/v1/contract_test.go` — **modify**: add new field to `PlanNode` allowlist.
- `internal/planextract/extract.go` — **modify**: map `Rows Removed by Filter` → new field.
- `internal/planextract/extract_test.go` — **modify**: add extraction test for the new counter.
- `internal/insight/insight.go` — **create**: `Kind`, `Severity`, `Insight`, `Detector`, `DetectAll`, `DetectPlans`, `walkPath` helper.
- `internal/insight/slowscan.go` — **create**: `SlowScanDetector`, `DefaultSlowScan`, `slowScanSeverity`.
- `internal/insight/insight_test.go` — **create**: engine + detector + privacy tests.
- `internal/insight/testdata/*.json` — **create**: auto_explain JSON fixtures (positive + negatives + loops edge).

---

## Task 1: Add `rows_removed_by_filter` to the T1 plan node

The "Slow Scan" signal is the count of rows a Seq Scan read then threw away. Postgres exposes it as `Rows Removed by Filter` in the EXPLAIN JSON. It is a scalar count (like `actual_rows`), not a literal, so it belongs in T1.

**Files:**
- Modify: `internal/planextract/extract_test.go`
- Modify: `proto/lynceus/v1/plan.proto`
- Modify: `internal/planextract/extract.go`
- Modify: `internal/proto/lynceus/v1/contract_test.go`

- [ ] **Step 1: Write the failing extraction test**

Add to `internal/planextract/extract_test.go` (the `nestloop_analyze.json` fixture's Seq Scan on `orders` has `"Rows Removed by Filter": 233`):

```go
func TestExtract_rowsRemovedByFilter(t *testing.T) {
	qp, err := planextract.Extract(loadFixture(t, "nestloop_analyze.json"), "fp-nl", time.Unix(1700000000, 0))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	var seq *lynceusv1.PlanNode
	walk(qp.GetRoot(), func(n *lynceusv1.PlanNode) {
		if n.GetNodeType() == "Seq Scan" {
			seq = n
		}
	})
	if seq == nil {
		t.Fatal("no Seq Scan node found in nestloop_analyze.json")
	}
	if got := seq.GetRowsRemovedByFilter(); got != 233 {
		t.Errorf("rows_removed_by_filter = %d, want 233", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/planextract/ -run TestExtract_rowsRemovedByFilter`
Expected: COMPILE FAIL — `seq.GetRowsRemovedByFilter undefined (type *lynceusv1.PlanNode has no field or method GetRowsRemovedByFilter)`.

- [ ] **Step 3: Add the proto field**

In `proto/lynceus/v1/plan.proto`, inside `message PlanNode`, add field 17 immediately after `normalized_condition` (field 15) and before `repeated PlanNode plans = 16;`. Field number 17 keeps `plans` at 16 unchanged:

```proto
  // Rows the node read then discarded via its Filter, per loop (Postgres
  // "Rows Removed by Filter"). A COUNT, never a literal — used by the Slow
  // Scan insight to measure scan selectivity. 0 if absent / no ANALYZE.
  int64 rows_removed_by_filter = 17;
```

- [ ] **Step 4: Regenerate the Go bindings**

Run: `make proto`
Expected: regenerates `internal/proto/lynceus/v1/plan.pb.go` with a `RowsRemovedByFilter int64` field + `GetRowsRemovedByFilter()` accessor. No other proto files change.

Verify: `git diff --stat internal/proto/lynceus/v1/` shows only `plan.pb.go` modified.

- [ ] **Step 5: Map the raw JSON field in the extractor**

In `internal/planextract/extract.go`, add the JSON-tagged field to `rawNode` (alongside the other actuals, after `ActualLoops`):

```go
	ActualLoops       int64   `json:"Actual Loops"`
	RowsRemovedByFilter int64 `json:"Rows Removed by Filter"`
```

And map it in `convert`, after `ActualLoops: n.ActualLoops,`:

```go
		ActualLoops:         n.ActualLoops,
		RowsRemovedByFilter: n.RowsRemovedByFilter,
```

- [ ] **Step 6: Allowlist the field in the privacy contract test**

In `internal/proto/lynceus/v1/contract_test.go`, in `TestQueryPlanHasNoLiteralFields`, add the field to `nodeAllowed` (after `"actual_rows": {}, "actual_loops": {},`):

```go
		"actual_rows": {}, "actual_loops": {},
		"rows_removed_by_filter": {},
		"normalized_condition": {},
```

- [ ] **Step 7: Run the affected tests to verify they pass**

Run: `go test ./internal/planextract/ ./internal/proto/...`
Expected: PASS — extraction test green, contract test still green (new field is an allowlisted count).

- [ ] **Step 8: Commit**

```bash
git add proto/lynceus/v1/plan.proto internal/proto/lynceus/v1/plan.pb.go \
        internal/proto/lynceus/v1/contract_test.go \
        internal/planextract/extract.go internal/planextract/extract_test.go
git commit -m "feat(planextract): extract Rows Removed by Filter into T1 PlanNode

Adds rows_removed_by_filter (a count, not a literal) to PlanNode so the
Slow Scan insight can measure how many rows a Seq Scan discards. Allowlisted
in the privacy contract test. Refs ly-u4t.7."
```

---

## Task 2: Insight engine core (types + registry + tree walk)

Build the neutral, dependency-free package every EXPLAIN insight will use. No detector logic yet beyond the empty registry path — that keeps this task small and lets the SlowScan detector (Task 3) register into a tested skeleton.

**Files:**
- Create: `internal/insight/insight.go`
- Create: `internal/insight/insight_test.go`

- [ ] **Step 1: Write the failing engine test**

Create `internal/insight/insight_test.go`:

```go
package insight_test

import (
	"testing"

	"github.com/dobbo-ca/lynceus/internal/insight"
	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
)

func TestDetectAll_nilPlan_returnsNil(t *testing.T) {
	if got := insight.DetectAll(nil); got != nil {
		t.Errorf("DetectAll(nil) = %v, want nil", got)
	}
}

func TestDetectPlans_emptySlice_returnsNil(t *testing.T) {
	if got := insight.DetectPlans(nil); got != nil {
		t.Errorf("DetectPlans(nil) = %v, want nil", got)
	}
}

func TestDetectAll_planWithNoAntiPattern_returnsNil(t *testing.T) {
	// A trivial Index Scan plan trips no detector.
	qp := &lynceusv1.QueryPlan{
		Fingerprint: "fp-x",
		Root: &lynceusv1.PlanNode{
			NodeType:     "Index Scan",
			RelationName: "users",
			IndexName:    "users_pkey",
			ActualRows:   1,
			ActualLoops:  1,
		},
	}
	if got := insight.DetectAll(qp); got != nil {
		t.Errorf("DetectAll(index scan) = %v, want nil", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/insight/`
Expected: COMPILE FAIL — `package internal/insight` has no Go files / `undefined: insight.DetectAll`.

- [ ] **Step 3: Write the engine**

Create `internal/insight/insight.go`:

```go
// Package insight detects query anti-patterns from extracted (T1, literal-free)
// EXPLAIN plans. Detectors are pure functions over a *lynceusv1.QueryPlan and
// return structured Insight values; they perform no I/O and may run at the
// collector or server. Every Insight field is a structural identifier or an
// aggregate count — no literal from the monitored database ever appears here.
package insight

import (
	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
)

// Kind identifies an anti-pattern. One per M3 EXPLAIN insight bead.
type Kind string

const (
	KindSlowScan Kind = "slow_scan"
)

// Severity ranks how strongly an insight applies.
type Severity string

const (
	SeverityLow    Severity = "low"
	SeverityMedium Severity = "medium"
	SeverityHigh   Severity = "high"
)

// Insight is one detected anti-pattern. All fields are structural identifiers
// or aggregate counts — safe to surface broadly (T1). Detail is templated
// from these fields only and must never embed a literal.
type Insight struct {
	Kind         Kind
	Severity     Severity
	Fingerprint  string  // statement the plan belongs to
	Relation     string  // table the offending node scans
	NodePath     string  // e.g. "Nested Loop > Seq Scan(orders)"
	RowsReturned int64   // rows the node emitted, total across loops
	RowsScanned  int64   // rows the node read before filtering, total across loops
	Selectivity  float64 // RowsReturned / RowsScanned
	Detail       string  // human summary, identifiers + counts only
}

// Detector inspects a plan and returns any insights it finds.
type Detector interface {
	Detect(qp *lynceusv1.QueryPlan) []Insight
}

// registry is the ordered set of detectors DetectAll runs. Append new EXPLAIN
// insight detectors here as their beads land (ly-u4t.*).
var registry = []Detector{
	DefaultSlowScan,
}

// DetectAll runs every registered detector over one plan.
func DetectAll(qp *lynceusv1.QueryPlan) []Insight {
	if qp == nil {
		return nil
	}
	var out []Insight
	for _, d := range registry {
		out = append(out, d.Detect(qp)...)
	}
	return out
}

// DetectPlans runs DetectAll over a batch of plans (e.g. the result of
// store.TopPlansByQuery) and concatenates the insights.
func DetectPlans(plans []*lynceusv1.QueryPlan) []Insight {
	var out []Insight
	for _, qp := range plans {
		out = append(out, DetectAll(qp)...)
	}
	return out
}

// walkPath visits every node depth-first, passing each node and a readable
// path of node types (with relation names) from the root. Path segments are
// structural identifiers only — no literal can appear.
func walkPath(n *lynceusv1.PlanNode, prefix string, fn func(node *lynceusv1.PlanNode, path string)) {
	if n == nil {
		return
	}
	seg := n.GetNodeType()
	if r := n.GetRelationName(); r != "" {
		seg += "(" + r + ")"
	}
	path := seg
	if prefix != "" {
		path = prefix + " > " + seg
	}
	fn(n, path)
	for _, c := range n.GetPlans() {
		walkPath(c, path, fn)
	}
}
```

Note: `registry` references `DefaultSlowScan`, defined in Task 3. This task will not compile until Task 3 lands — so steps 3 and Task 3's step 3 are committed together. To keep this task independently runnable, temporarily use `var registry = []Detector{}` (empty), then switch to `[]Detector{DefaultSlowScan}` in Task 3 Step 4.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/insight/`
Expected: PASS (3 tests) with the temporary empty `registry`.

- [ ] **Step 5: Commit**

```bash
git add internal/insight/insight.go internal/insight/insight_test.go
git commit -m "feat(insight): query-plan insight engine skeleton

Adds internal/insight: Insight/Detector types, DetectAll/DetectPlans, and a
literal-free tree-walk helper. Registry is empty pending detectors. Refs ly-u4t.7."
```

---

## Task 3: Slow Scan detector

Flag a `Seq Scan` whose Filter discards most of the rows it read. Gate on a minimum scanned volume so tiny scans (where a Seq Scan is fine) are never flagged.

**Detection rule** (per node, requires ANALYZE actuals):
- `node_type == "Seq Scan"` and `actual_loops > 0` (else no measured rows → skip).
- `rows_removed_by_filter > 0` (else it discards nothing → skip).
- `scannedPerLoop = actual_rows + rows_removed_by_filter`; `totalScanned = scannedPerLoop * actual_loops`.
- `totalScanned >= MinRowsScanned` (default 1000) — ignore small scans.
- `selectivity = actual_rows / scannedPerLoop <= MaxSelectivity` (default 0.10) — most rows discarded.
- Severity by selectivity: `<= 0.01` high, `<= 0.05` medium, else low.

**Files:**
- Create: `internal/insight/slowscan.go`
- Modify: `internal/insight/insight.go` (swap registry to include `DefaultSlowScan`)
- Modify: `internal/insight/insight_test.go` (add detector + privacy tests)
- Create: `internal/insight/testdata/slowscan_events.json`
- Create: `internal/insight/testdata/seqscan_small.json`
- Create: `internal/insight/testdata/seqscan_fullread.json`
- Create: `internal/insight/testdata/seqscan_noanalyze.json`
- Create: `internal/insight/testdata/seqscan_loops.json`

- [ ] **Step 1: Create the test fixtures**

`internal/insight/testdata/slowscan_events.json` — ANALYZE Seq Scan, scanned 100000, returned 10, selectivity 0.0001 → HIGH:

```json
[
  {
    "Query Text": "SELECT * FROM events WHERE kind = 'error' AND created_at > '2024-01-01';",
    "Plan": {
      "Node Type": "Seq Scan",
      "Relation Name": "events",
      "Alias": "events",
      "Startup Cost": 0.00,
      "Total Cost": 1943.00,
      "Plan Rows": 12,
      "Plan Width": 64,
      "Actual Startup Time": 0.021,
      "Actual Total Time": 18.42,
      "Actual Rows": 10,
      "Actual Loops": 1,
      "Filter": "((kind = 'error'::text) AND (created_at > '2024-01-01'::timestamp without time zone))",
      "Rows Removed by Filter": 99990
    },
    "Planning Time": 0.10,
    "Execution Time": 18.55
  }
]
```

`internal/insight/testdata/seqscan_small.json` — scanned 243 (< 1000) → no insight:

```json
[
  {
    "Query Text": "SELECT * FROM orders WHERE status = 'pending';",
    "Plan": {
      "Node Type": "Seq Scan",
      "Relation Name": "orders",
      "Alias": "orders",
      "Startup Cost": 0.00,
      "Total Cost": 109.00,
      "Plan Rows": 10,
      "Plan Width": 8,
      "Actual Startup Time": 0.005,
      "Actual Total Time": 0.017,
      "Actual Rows": 10,
      "Actual Loops": 1,
      "Filter": "(status = 'pending'::text)",
      "Rows Removed by Filter": 233
    },
    "Planning Time": 0.20,
    "Execution Time": 0.05
  }
]
```

`internal/insight/testdata/seqscan_fullread.json` — big Seq Scan but discards nothing (Rows Removed 0) → no insight:

```json
[
  {
    "Query Text": "SELECT * FROM events;",
    "Plan": {
      "Node Type": "Seq Scan",
      "Relation Name": "events",
      "Alias": "events",
      "Startup Cost": 0.00,
      "Total Cost": 1693.00,
      "Plan Rows": 50000,
      "Plan Width": 64,
      "Actual Startup Time": 0.008,
      "Actual Total Time": 12.10,
      "Actual Rows": 50000,
      "Actual Loops": 1,
      "Rows Removed by Filter": 0
    },
    "Planning Time": 0.06,
    "Execution Time": 14.00
  }
]
```

`internal/insight/testdata/seqscan_noanalyze.json` — no ANALYZE (no Actual* fields → loops 0) → no insight:

```json
[
  {
    "Query Text": "SELECT * FROM events WHERE kind = 'error';",
    "Plan": {
      "Node Type": "Seq Scan",
      "Relation Name": "events",
      "Alias": "events",
      "Startup Cost": 0.00,
      "Total Cost": 1943.00,
      "Plan Rows": 12,
      "Plan Width": 64,
      "Filter": "(kind = 'error'::text)"
    },
    "Planning Time": 0.10
  }
]
```

`internal/insight/testdata/seqscan_loops.json` — Seq Scan as inner of a Nested Loop: per loop scanned 51, 100 loops → total scanned 5100, returned 100, selectivity ≈ 0.0196 → MEDIUM. Locks the loop multiplication and the nested NodePath:

```json
[
  {
    "Query Text": "SELECT * FROM a JOIN b ON b.k = a.k WHERE b.flag = 't';",
    "Plan": {
      "Node Type": "Nested Loop",
      "Join Type": "Inner",
      "Startup Cost": 0.15,
      "Total Cost": 5000.00,
      "Plan Rows": 100,
      "Plan Width": 16,
      "Actual Startup Time": 0.02,
      "Actual Total Time": 40.00,
      "Actual Rows": 100,
      "Actual Loops": 1,
      "Plans": [
        {
          "Node Type": "Seq Scan",
          "Parent Relationship": "Outer",
          "Relation Name": "a",
          "Alias": "a",
          "Startup Cost": 0.00,
          "Total Cost": 10.00,
          "Plan Rows": 100,
          "Plan Width": 8,
          "Actual Startup Time": 0.005,
          "Actual Total Time": 0.05,
          "Actual Rows": 100,
          "Actual Loops": 1
        },
        {
          "Node Type": "Seq Scan",
          "Parent Relationship": "Inner",
          "Relation Name": "b",
          "Alias": "b",
          "Startup Cost": 0.00,
          "Total Cost": 49.00,
          "Plan Rows": 1,
          "Plan Width": 8,
          "Actual Startup Time": 0.001,
          "Actual Total Time": 0.003,
          "Actual Rows": 1,
          "Actual Loops": 100,
          "Filter": "(flag = 't'::boolean)",
          "Rows Removed by Filter": 50
        }
      ]
    },
    "Planning Time": 0.30,
    "Execution Time": 41.00
  }
]
```

- [ ] **Step 2: Write the failing detector tests**

Append to `internal/insight/insight_test.go` (add `os`, `path/filepath`, `time`, and `planextract` imports):

```go
import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dobbo-ca/lynceus/internal/insight"
	"github.com/dobbo-ca/lynceus/internal/planextract"
	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
)

// planFromFixture extracts a T1 QueryPlan from an auto_explain JSON fixture,
// exercising the real planextract path (incl. rows_removed_by_filter).
func planFromFixture(t *testing.T, name string) *lynceusv1.QueryPlan {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	qp, err := planextract.Extract(b, "fp-"+name, time.Unix(1700000000, 0))
	if err != nil {
		t.Fatalf("extract %s: %v", name, err)
	}
	return qp
}

func TestSlowScan_positive_highSeverity(t *testing.T) {
	got := insight.DetectAll(planFromFixture(t, "slowscan_events.json"))
	if len(got) != 1 {
		t.Fatalf("insights = %d, want 1: %+v", len(got), got)
	}
	in := got[0]
	if in.Kind != insight.KindSlowScan {
		t.Errorf("kind = %q, want slow_scan", in.Kind)
	}
	if in.Severity != insight.SeverityHigh {
		t.Errorf("severity = %q, want high", in.Severity)
	}
	if in.Relation != "events" {
		t.Errorf("relation = %q, want events", in.Relation)
	}
	if in.RowsScanned != 100000 {
		t.Errorf("rows_scanned = %d, want 100000", in.RowsScanned)
	}
	if in.RowsReturned != 10 {
		t.Errorf("rows_returned = %d, want 10", in.RowsReturned)
	}
	if in.NodePath != "Seq Scan(events)" {
		t.Errorf("node_path = %q, want Seq Scan(events)", in.NodePath)
	}
}

func TestSlowScan_smallScan_noInsight(t *testing.T) {
	if got := insight.DetectAll(planFromFixture(t, "seqscan_small.json")); got != nil {
		t.Errorf("small scan flagged: %+v", got)
	}
}

func TestSlowScan_fullRead_noInsight(t *testing.T) {
	if got := insight.DetectAll(planFromFixture(t, "seqscan_fullread.json")); got != nil {
		t.Errorf("full-read scan flagged: %+v", got)
	}
}

func TestSlowScan_noAnalyze_noInsight(t *testing.T) {
	if got := insight.DetectAll(planFromFixture(t, "seqscan_noanalyze.json")); got != nil {
		t.Errorf("non-ANALYZE scan flagged: %+v", got)
	}
}

func TestSlowScan_loops_totalsAndSeverity(t *testing.T) {
	got := insight.DetectAll(planFromFixture(t, "seqscan_loops.json"))
	if len(got) != 1 {
		t.Fatalf("insights = %d, want 1: %+v", len(got), got)
	}
	in := got[0]
	if in.RowsScanned != 5100 {
		t.Errorf("rows_scanned = %d, want 5100 (51*100 loops)", in.RowsScanned)
	}
	if in.RowsReturned != 100 {
		t.Errorf("rows_returned = %d, want 100 (1*100 loops)", in.RowsReturned)
	}
	if in.Severity != insight.SeverityMedium {
		t.Errorf("severity = %q, want medium", in.Severity)
	}
	if in.NodePath != "Nested Loop > Seq Scan(b)" {
		t.Errorf("node_path = %q, want Nested Loop > Seq Scan(b)", in.NodePath)
	}
}

func TestSlowScan_detailHasNoLiteral(t *testing.T) {
	got := insight.DetectAll(planFromFixture(t, "slowscan_events.json"))
	if len(got) != 1 {
		t.Fatalf("insights = %d, want 1", len(got))
	}
	for _, banned := range []string{"'", "error", "2024-01-01", "kind ="} {
		if strings.Contains(got[0].Detail, banned) {
			t.Errorf("literal %q leaked into Detail: %q", banned, got[0].Detail)
		}
		if strings.Contains(got[0].NodePath, banned) {
			t.Errorf("literal %q leaked into NodePath: %q", banned, got[0].NodePath)
		}
	}
}

func TestDetectPlans_aggregatesAcrossPlans(t *testing.T) {
	plans := []*lynceusv1.QueryPlan{
		planFromFixture(t, "slowscan_events.json"),
		planFromFixture(t, "seqscan_small.json"),
		planFromFixture(t, "seqscan_loops.json"),
	}
	if got := insight.DetectPlans(plans); len(got) != 2 {
		t.Errorf("DetectPlans = %d insights, want 2", len(got))
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/insight/`
Expected: COMPILE FAIL — `undefined: insight.KindSlowScan` is already defined, but `DefaultSlowScan` referenced by registry is not yet defined → build fails on `slowscan.go` absence.

- [ ] **Step 4: Write the detector and wire the registry**

Create `internal/insight/slowscan.go`:

```go
package insight

import (
	"fmt"

	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
)

// SlowScanDetector flags a Seq Scan whose Filter discards most of the rows it
// reads — a case where an index on the filtered column(s) would likely help.
type SlowScanDetector struct {
	MinRowsScanned int64   // skip scans smaller than this (a Seq Scan is fine there)
	MaxSelectivity float64 // flag only when returned/scanned is at or below this
}

// DefaultSlowScan is the registered detector: flag Seq Scans that read >= 1000
// rows and return <= 10% of them.
var DefaultSlowScan = SlowScanDetector{MinRowsScanned: 1000, MaxSelectivity: 0.10}

// Detect implements Detector.
func (d SlowScanDetector) Detect(qp *lynceusv1.QueryPlan) []Insight {
	if qp == nil {
		return nil
	}
	var out []Insight
	walkPath(qp.GetRoot(), "", func(n *lynceusv1.PlanNode, path string) {
		if n.GetNodeType() != "Seq Scan" {
			return
		}
		loops := n.GetActualLoops()
		if loops <= 0 {
			return // no ANALYZE actuals — cannot measure discard
		}
		removed := n.GetRowsRemovedByFilter()
		if removed <= 0 {
			return // discards nothing
		}
		returnedPerLoop := n.GetActualRows()
		scannedPerLoop := returnedPerLoop + removed
		if scannedPerLoop <= 0 {
			return
		}
		totalScanned := scannedPerLoop * loops
		if totalScanned < d.MinRowsScanned {
			return
		}
		sel := float64(returnedPerLoop) / float64(scannedPerLoop)
		if sel > d.MaxSelectivity {
			return
		}
		totalReturned := returnedPerLoop * loops
		out = append(out, Insight{
			Kind:         KindSlowScan,
			Severity:     slowScanSeverity(sel),
			Fingerprint:  qp.GetFingerprint(),
			Relation:     n.GetRelationName(),
			NodePath:     path,
			RowsReturned: totalReturned,
			RowsScanned:  totalScanned,
			Selectivity:  sel,
			Detail: fmt.Sprintf(
				"Seq Scan on %s read %d rows and discarded %d (%.2f%% returned); "+
					"an index on the filtered column(s) would likely help.",
				n.GetRelationName(), totalScanned, totalScanned-totalReturned, sel*100,
			),
		})
	})
	return out
}

func slowScanSeverity(sel float64) Severity {
	switch {
	case sel <= 0.01:
		return SeverityHigh
	case sel <= 0.05:
		return SeverityMedium
	default:
		return SeverityLow
	}
}
```

Then in `internal/insight/insight.go`, switch the registry from the temporary empty slice to include the detector:

```go
var registry = []Detector{
	DefaultSlowScan,
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/insight/`
Expected: PASS — all engine + detector + privacy tests green.

- [ ] **Step 6: Run the full package set affected**

Run: `go test ./internal/insight/ ./internal/planextract/ ./internal/proto/...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/insight/slowscan.go internal/insight/insight.go \
        internal/insight/insight_test.go internal/insight/testdata/
git commit -m "feat(insight): Slow Scan detector (ly-u4t.7)

Flags Seq Scans that read >=1000 rows and return <=10% of them, with
severity by selectivity. Detection runs over T1 plans (identifiers + counts
only); a privacy test asserts no literal reaches Detail/NodePath. Closes ly-u4t.7."
```

---

## Task 4: Full-suite verification + bead lifecycle

- [ ] **Step 1: Run the full test suite**

Run: `go test ./...`
Expected: PASS — previous green baseline (147 tests) plus the new `internal/insight` package tests. No regressions.

- [ ] **Step 2: Build all binaries**

Run: `go build ./...`
Expected: success.

- [ ] **Step 3: Update GOALS**

In `docs/GOALS.md`, mark `ly-u4t.7` done in the M3 row / "Highest-leverage next moves" list, noting the new `internal/insight` engine other M3 insights reuse, and that `rows_removed_by_filter` was added to T1 `PlanNode`. Commit:

```bash
git add docs/GOALS.md
git commit -m "docs: record ly-u4t.7 Slow Scan insight + insight engine"
```

- [ ] **Step 4: Bead lifecycle**

```bash
bd close ly-u4t.7 --reason="Slow Scan EXPLAIN insight + reusable internal/insight engine; rows_removed_by_filter added to T1 PlanNode (count, contract-safe)"
```

(Plan-phase label swap `needs-plan`→`ready-impl` happens before implementation begins; see the handoff note below.)

- [ ] **Step 5: Hand off** — report changed files, test results, and propose PR per the conservative git profile.

---

## Self-Review

**Spec coverage** — Spec line 52: "Slow Scan (Seq Scan discarding many rows) — MUST." Task 3's rule keys exactly on a Seq Scan whose `Rows Removed by Filter` dominates `Actual Rows`, gated by volume. Covered. Bead description ("reading and discarding many rows — an index would likely help") matched by `Detail` text and the selectivity gate.

**Placeholder scan** — no TBD/TODO; every code step shows complete code; every command shows expected output.

**Type consistency** — `Insight` fields (`RowsReturned`, `RowsScanned`, `Selectivity`, `NodePath`, `Detail`) used identically in `slowscan.go` and the tests. `KindSlowScan`, `SeverityHigh/Medium/Low`, `DefaultSlowScan`, `slowScanSeverity`, `walkPath`, `DetectAll`, `DetectPlans` all defined before use. `GetRowsRemovedByFilter()` defined in Task 1, used in Task 3. Registry temporary-empty (Task 2) → populated (Task 3) noted explicitly so Task 2 runs independently.

**Privacy** — new proto field is a count (allowlisted, Task 1 Step 6); insight `Detail`/`NodePath` built only from relation name (identifier) + counts; Task 3 adds an explicit no-literal test. T1 contract preserved.

**Loop semantics** — Postgres EXPLAIN JSON reports `Actual Rows` / `Rows Removed by Filter` as per-loop averages; the detector multiplies by `Actual Loops` for totals and gates `MinRowsScanned` on the total. `seqscan_loops.json` locks this (5100 scanned, 100 returned).

---

## Handoff note (plan phase complete)

Before implementing, swap the bead label out of plan phase:

```bash
bd label remove ly-u4t.7 needs-plan && bd label add ly-u4t.7 ready-impl
git add docs/superpowers/plans/2026-06-06-explain-insight-slow-scan.md
git commit -m "docs(plan): EXPLAIN Slow Scan insight (ly-u4t.7) → ready-impl"
```

**Out of scope (separate beads):** surfacing insights over HTTP (`store.TopPlansByQuery` → `insight.DetectPlans` in an API handler) belongs to the checks/UI beads (`ly-u4t.21`, `ly-xqf.10`). The `internal/insight` package is built so those beads only add a thin caller. The other 7 EXPLAIN insights (`ly-u4t.1/.2/.3/.4/.5/.6/.8`) each add one `Detector` to the registry + fixtures, reusing this engine.
