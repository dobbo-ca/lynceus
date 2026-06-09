# Layer 1 — EXPLAIN Insights Bundle Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement task-by-task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Add five EXPLAIN-plan anti-pattern detectors to the live `internal/insight` engine — Disk Sort (`ly-u4t.1`), Hash Batches (`ly-u4t.2`), Inefficient Index (`ly-u4t.3`), Mis-Estimate (`ly-u4t.6`), Stale Stats (`ly-u4t.8`) — each one `Detector` + fixtures, plus the small set of count/enum `PlanNode` fields two of them need.

**Architecture:** Detectors are pure functions over a `*lynceusv1.QueryPlan`, registered in `insight.registry`, run by `DetectAll`/`DetectPlans` and surfaced unchanged on `/insights` (the view-model renders `Kind`/`Severity`/`Detail` generically — `web/insights.templ`). Disk Sort and Hash Batches need new **count/enum** `PlanNode` fields (same precedent as SlowScan's `rows_removed_by_filter`); the other three reuse existing `plan_rows`/`actual_rows`/`rows_removed_by_filter`. No new DB schema, no new store migration.

**Tech Stack:** Go, protobuf (`make proto`), testcontainers not needed (detectors are pure; fixtures are auto_explain JSON run through `planextract.Extract`).

**Privacy invariant (re-verify):** every new `PlanNode` field is an enum label or an aggregate count — never a literal. Add each to the contract allowlist in `internal/proto/lynceus/v1/contract_test.go` (`nodeAllowed`); `TestQueryPlanHasNoLiteralFields` must stay green. Each detector's `Detail`/`NodePath` must contain only identifiers + counts (mirror `TestSlowScan_detailHasNoLiteral`).

---

## File Structure

- `proto/lynceus/v1/plan.proto` — +6 `PlanNode` fields (18–23).
- `internal/proto/lynceus/v1/plan.pb.go` — regenerated via `make proto`.
- `internal/proto/lynceus/v1/contract_test.go` — +6 names in `nodeAllowed`.
- `internal/planextract/extract.go` — map the 6 new `rawNode` JSON fields → `PlanNode`.
- `internal/insight/insight.go` — +5 `Kind` consts; append 5 detectors to `registry`.
- `internal/insight/disksort.go`, `hashbatches.go`, `inefficientindex.go`, `misestimate.go`, `stalestats.go` — one detector each.
- `internal/insight/*_test.go` — per-detector table tests (or extend `insight_test.go`).
- `internal/insight/testdata/*.json` — new auto_explain fixtures (positive + negative per detector).

---

## Task 1: New PlanNode count/enum fields (proto + extractor + contract)

**Files:**
- Modify: `proto/lynceus/v1/plan.proto` (PlanNode message, after field 17)
- Modify: `internal/planextract/extract.go:32-59` (rawNode), `:91-114` (convert)
- Modify: `internal/proto/lynceus/v1/contract_test.go` (`nodeAllowed` map)
- Regenerate: `internal/proto/lynceus/v1/plan.pb.go`

- [ ] **Step 1: Add the proto fields.** In `plan.proto`, inside `message PlanNode`, after `rows_removed_by_filter = 17;` and before `repeated PlanNode plans = 16;` (proto field numbers need not be contiguous with declaration order; keep `plans = 16`):

```proto
  // Sort node spill telemetry (auto_explain "Sort Method"/"Sort Space Type"/
  // "Sort Space Used"). Enum-like labels + a kB COUNT — never a literal. Used
  // by the Disk Sort insight. Empty / 0 when the node is not a Sort or had no
  // ANALYZE actuals.
  string sort_method        = 18; // "quicksort" | "top-N heapsort" | "external merge" | "external sort"
  string sort_space_type    = 19; // "Memory" | "Disk"
  int64  sort_space_used_kb = 20;

  // Hash node spill telemetry (auto_explain "Hash Batches"/"Original Hash
  // Batches"/"Peak Memory Usage"). COUNTS only. Used by the Hash Batches
  // insight. 0 when the node is not a Hash.
  int64 hash_batches          = 21;
  int64 original_hash_batches = 22;
  int64 peak_memory_usage_kb  = 23;
```

- [ ] **Step 2: Extend the contract allowlist.** In `contract_test.go`, add to `nodeAllowed`:

```go
		"sort_method": {}, "sort_space_type": {}, "sort_space_used_kb": {},
		"hash_batches": {}, "original_hash_batches": {}, "peak_memory_usage_kb": {},
```

- [ ] **Step 3: Map them in the extractor.** In `extract.go` `rawNode`, add (auto_explain JSON keys — `Sort Space Used`/`Peak Memory Usage` are already kB in the JSON):

```go
	SortMethod          string `json:"Sort Method"`
	SortSpaceType       string `json:"Sort Space Type"`
	SortSpaceUsed       int64  `json:"Sort Space Used"`        // kB
	HashBatches         int64  `json:"Hash Batches"`
	OriginalHashBatches int64  `json:"Original Hash Batches"`
	PeakMemoryUsage     int64  `json:"Peak Memory Usage"`      // kB
```

In `convert()`, add to the `&lynceusv1.PlanNode{...}` literal:

```go
		SortMethod:          n.SortMethod,
		SortSpaceType:       n.SortSpaceType,
		SortSpaceUsedKb:     n.SortSpaceUsed,
		HashBatches:         n.HashBatches,
		OriginalHashBatches: n.OriginalHashBatches,
		PeakMemoryUsageKb:   n.PeakMemoryUsage,
```

- [ ] **Step 4: Regenerate + build.** Run: `make proto && go build ./...`  Expected: clean build (new getters `GetSortMethod`, `GetSortSpaceType`, `GetSortSpaceUsedKb`, `GetHashBatches`, `GetOriginalHashBatches`, `GetPeakMemoryUsageKb` exist).

- [ ] **Step 5: Verify contract + extractor tests.** Run: `go test ./internal/proto/... ./internal/planextract/... -count=1`  Expected: PASS (`TestQueryPlanHasNoLiteralFields` green with the 6 new names).

- [ ] **Step 6: Commit.** `git add -A && git commit -m "feat(plan): add Sort/Hash spill count+enum fields to T1 PlanNode (ly-u4t.1, ly-u4t.2)"`

---

## Task 2: Disk Sort detector (`ly-u4t.1`)

**Files:** Create `internal/insight/disksort.go`, `internal/insight/disksort_test.go`, fixtures `testdata/disksort_external.json`, `testdata/sort_inmemory.json`. Modify `insight.go` (Kind const + registry).

**Detection rule:** a `Sort` node whose `sort_space_type == "Disk"` (equivalently `sort_method` contains `"external"`) spilled to disk → `work_mem` too low. Severity by `sort_space_used_kb`: `>262144` (256 MB) → High; `>32768` (32 MB) → Medium; else Low.

- [ ] **Step 1: Kind const.** In `insight.go` add `KindDiskSort Kind = "disk_sort"` to the const block.

- [ ] **Step 2: Failing test.** `disksort_test.go`:

```go
package insight_test

import (
	"strings"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/insight"
)

func TestDiskSort_external_flagged(t *testing.T) {
	got := insight.DetectAll(planFromFixture(t, "disksort_external.json"))
	var ds *insight.Insight
	for i := range got {
		if got[i].Kind == insight.KindDiskSort {
			ds = &got[i]
		}
	}
	if ds == nil {
		t.Fatalf("no disk_sort insight: %+v", got)
	}
	if ds.Severity != insight.SeverityLow { // 24000 kB < 32 MB
		t.Errorf("severity = %q, want low", ds.Severity)
	}
	if !strings.Contains(ds.Detail, "work_mem") {
		t.Errorf("detail missing work_mem hint: %q", ds.Detail)
	}
	for _, banned := range []string{"'", "=", "::"} {
		if strings.Contains(ds.Detail, banned) {
			t.Errorf("possible literal %q in detail: %q", banned, ds.Detail)
		}
	}
}

func TestDiskSort_inMemory_notFlagged(t *testing.T) {
	for _, in := range insight.DetectAll(planFromFixture(t, "sort_inmemory.json")) {
		if in.Kind == insight.KindDiskSort {
			t.Errorf("in-memory sort flagged: %+v", in)
		}
	}
}
```

- [ ] **Step 3: Fixtures.** `testdata/disksort_external.json` (a Sort over a Seq Scan, ANALYZE actuals, external merge):

```json
[{"Plan":{"Node Type":"Sort","Actual Rows":100000,"Actual Loops":1,"Plan Rows":100000,
  "Total Cost":12000.0,"Actual Total Time":850.0,
  "Sort Method":"external merge","Sort Space Type":"Disk","Sort Space Used":24000,
  "Plans":[{"Node Type":"Seq Scan","Relation Name":"events","Alias":"events",
    "Actual Rows":100000,"Actual Loops":1,"Plan Rows":100000,"Total Cost":8000.0,"Actual Total Time":300.0}]}}]
```

`testdata/sort_inmemory.json` (identical shape but in-memory):

```json
[{"Plan":{"Node Type":"Sort","Actual Rows":500,"Actual Loops":1,"Plan Rows":500,
  "Total Cost":50.0,"Actual Total Time":2.0,
  "Sort Method":"quicksort","Sort Space Type":"Memory","Sort Space Used":48,
  "Plans":[{"Node Type":"Seq Scan","Relation Name":"events","Alias":"events",
    "Actual Rows":500,"Actual Loops":1,"Plan Rows":500,"Total Cost":40.0,"Actual Total Time":1.0}]}}]
```

- [ ] **Step 4: Implement `disksort.go`:**

```go
package insight

import (
	"fmt"

	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
)

// DiskSortDetector flags a Sort node that spilled to disk (Sort Space Type ==
// "Disk"), i.e. work_mem was too small to sort in memory.
type DiskSortDetector struct {
	HighKB   int64 // >= this many kB spilled -> high severity
	MediumKB int64
}

// DefaultDiskSort: 256 MB -> high, 32 MB -> medium.
var DefaultDiskSort = DiskSortDetector{HighKB: 262144, MediumKB: 32768}

func (d DiskSortDetector) Detect(qp *lynceusv1.QueryPlan) []Insight {
	if qp == nil {
		return nil
	}
	var out []Insight
	walkPath(qp.GetRoot(), "", func(n *lynceusv1.PlanNode, path string) {
		if n.GetNodeType() != "Sort" || n.GetSortSpaceType() != "Disk" {
			return
		}
		usedKB := n.GetSortSpaceUsedKb()
		out = append(out, Insight{
			Kind:        KindDiskSort,
			Severity:    d.severity(usedKB),
			Fingerprint: qp.GetFingerprint(),
			NodePath:    path,
			Detail: fmt.Sprintf(
				"Sort spilled to disk (%s, %d kB used); increasing work_mem would let it sort in memory.",
				n.GetSortMethod(), usedKB,
			),
		})
	})
	return out
}

func (d DiskSortDetector) severity(usedKB int64) Severity {
	switch {
	case usedKB >= d.HighKB:
		return SeverityHigh
	case usedKB >= d.MediumKB:
		return SeverityMedium
	default:
		return SeverityLow
	}
}
```

- [ ] **Step 5: Register.** In `insight.go` `registry`, append `DefaultDiskSort,`.

- [ ] **Step 6: Run.** `go test ./internal/insight/... -count=1` Expected: PASS.

- [ ] **Step 7: Commit.** `git commit -am "feat(insight): Disk Sort EXPLAIN insight (ly-u4t.1)"`

---

## Task 3: Hash Batches detector (`ly-u4t.2`)

**Files:** Create `internal/insight/hashbatches.go`, `hashbatches_test.go`, fixtures `testdata/hashjoin_batches.json`, `testdata/hashjoin_onebatch.json`. Modify `insight.go`.

**Detection rule:** a `Hash` node with `hash_batches > 1` batched to disk. Severity by batch count: `>= 64` High, `>= 8` Medium, else Low. If `original_hash_batches > 0 && hash_batches > original_hash_batches`, note the runtime re-batch (planner under-estimated).

- [ ] **Step 1:** `KindHashBatches Kind = "hash_batches"`.

- [ ] **Step 2: Failing test** `hashbatches_test.go`:

```go
package insight_test

import (
	"strings"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/insight"
)

func TestHashBatches_multiBatch_flagged(t *testing.T) {
	got := insight.DetectAll(planFromFixture(t, "hashjoin_batches.json"))
	var h *insight.Insight
	for i := range got {
		if got[i].Kind == insight.KindHashBatches {
			h = &got[i]
		}
	}
	if h == nil {
		t.Fatalf("no hash_batches insight: %+v", got)
	}
	if h.Severity != insight.SeverityMedium { // 8 batches
		t.Errorf("severity = %q, want medium", h.Severity)
	}
	if !strings.Contains(h.Detail, "work_mem") {
		t.Errorf("detail missing work_mem hint: %q", h.Detail)
	}
}

func TestHashBatches_singleBatch_notFlagged(t *testing.T) {
	for _, in := range insight.DetectAll(planFromFixture(t, "hashjoin_onebatch.json")) {
		if in.Kind == insight.KindHashBatches {
			t.Errorf("single-batch hash flagged: %+v", in)
		}
	}
}
```

- [ ] **Step 3: Fixtures.** `testdata/hashjoin_batches.json` (Hash Join → Hash node with 8 batches, re-batched from 1):

```json
[{"Plan":{"Node Type":"Hash Join","Join Type":"Inner","Actual Rows":50000,"Actual Loops":1,
  "Plan Rows":50000,"Total Cost":9000.0,"Actual Total Time":600.0,
  "Plans":[
    {"Node Type":"Seq Scan","Relation Name":"orders","Alias":"orders","Actual Rows":50000,"Actual Loops":1,"Plan Rows":50000,"Total Cost":3000.0,"Actual Total Time":120.0},
    {"Node Type":"Hash","Actual Rows":200000,"Actual Loops":1,"Plan Rows":200000,"Total Cost":4000.0,"Actual Total Time":250.0,
      "Hash Batches":8,"Original Hash Batches":1,"Peak Memory Usage":40960,
      "Plans":[{"Node Type":"Seq Scan","Relation Name":"customers","Alias":"customers","Actual Rows":200000,"Actual Loops":1,"Plan Rows":200000,"Total Cost":2500.0,"Actual Total Time":90.0}]}]}}]
```

`testdata/hashjoin_onebatch.json` (same but `Hash Batches:1`):

```json
[{"Plan":{"Node Type":"Hash Join","Join Type":"Inner","Actual Rows":500,"Actual Loops":1,
  "Plan Rows":500,"Total Cost":300.0,"Actual Total Time":12.0,
  "Plans":[
    {"Node Type":"Seq Scan","Relation Name":"orders","Alias":"orders","Actual Rows":500,"Actual Loops":1,"Plan Rows":500,"Total Cost":100.0,"Actual Total Time":3.0},
    {"Node Type":"Hash","Actual Rows":1000,"Actual Loops":1,"Plan Rows":1000,"Total Cost":150.0,"Actual Total Time":4.0,
      "Hash Batches":1,"Original Hash Batches":1,"Peak Memory Usage":512,
      "Plans":[{"Node Type":"Seq Scan","Relation Name":"customers","Alias":"customers","Actual Rows":1000,"Actual Loops":1,"Plan Rows":1000,"Total Cost":120.0,"Actual Total Time":3.0}]}]}}]
```

- [ ] **Step 4: Implement `hashbatches.go`:**

```go
package insight

import (
	"fmt"

	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
)

// HashBatchesDetector flags a Hash node that batched to disk (Hash Batches > 1)
// because the hash table did not fit in work_mem.
type HashBatchesDetector struct {
	HighBatches   int64
	MediumBatches int64
}

// DefaultHashBatches: >=64 batches high, >=8 medium.
var DefaultHashBatches = HashBatchesDetector{HighBatches: 64, MediumBatches: 8}

func (d HashBatchesDetector) Detect(qp *lynceusv1.QueryPlan) []Insight {
	if qp == nil {
		return nil
	}
	var out []Insight
	walkPath(qp.GetRoot(), "", func(n *lynceusv1.PlanNode, path string) {
		if n.GetNodeType() != "Hash" {
			return
		}
		batches := n.GetHashBatches()
		if batches <= 1 {
			return
		}
		detail := fmt.Sprintf(
			"Hash table batched to disk (%d batches, %d kB peak); increasing work_mem would keep it in memory.",
			batches, n.GetPeakMemoryUsageKb(),
		)
		if orig := n.GetOriginalHashBatches(); orig > 0 && batches > orig {
			detail += fmt.Sprintf(" Re-batched at runtime from %d (row estimate too low).", orig)
		}
		out = append(out, Insight{
			Kind:        KindHashBatches,
			Severity:    d.severity(batches),
			Fingerprint: qp.GetFingerprint(),
			NodePath:    path,
			Detail:      detail,
		})
	})
	return out
}

func (d HashBatchesDetector) severity(batches int64) Severity {
	switch {
	case batches >= d.HighBatches:
		return SeverityHigh
	case batches >= d.MediumBatches:
		return SeverityMedium
	default:
		return SeverityLow
	}
}
```

- [ ] **Step 5: Register** `DefaultHashBatches,` in `registry`.
- [ ] **Step 6: Run** `go test ./internal/insight/... -count=1` → PASS.
- [ ] **Step 7: Commit** `git commit -am "feat(insight): Hash Batches EXPLAIN insight (ly-u4t.2)"`

---

## Task 4: Inefficient Index detector (`ly-u4t.3`)

**Files:** Create `internal/insight/inefficientindex.go`, `inefficientindex_test.go`, fixtures `testdata/inefficient_index.json`, `testdata/index_selective.json`. Modify `insight.go`.

**Detection rule:** an index-access node — `"Index Scan"`, `"Index Only Scan"`, or `"Bitmap Heap Scan"` — whose `rows_removed_by_filter` is large relative to what it returned, i.e. the index matched a leading column but a non-indexed **Filter** then discarded most rows → a composite/covering index probably helps. Mirror SlowScan's per-loop math but gate on index node types. `MinRowsScanned=1000`, `MaxSelectivity=0.10`. (Seq Scan is deliberately excluded — that is SlowScan's job.)

- [ ] **Step 1:** `KindInefficientIndex Kind = "inefficient_index"`.

- [ ] **Step 2: Failing test** `inefficientindex_test.go`:

```go
package insight_test

import (
	"testing"

	"github.com/dobbo-ca/lynceus/internal/insight"
)

func TestInefficientIndex_flagged(t *testing.T) {
	got := insight.DetectAll(planFromFixture(t, "inefficient_index.json"))
	var ii *insight.Insight
	for i := range got {
		if got[i].Kind == insight.KindInefficientIndex {
			ii = &got[i]
		}
	}
	if ii == nil {
		t.Fatalf("no inefficient_index insight: %+v", got)
	}
	if ii.Relation != "orders" {
		t.Errorf("relation = %q, want orders", ii.Relation)
	}
	if ii.RowsScanned != 20000 || ii.RowsReturned != 100 {
		t.Errorf("scanned/returned = %d/%d, want 20000/100", ii.RowsScanned, ii.RowsReturned)
	}
}

func TestInefficientIndex_selectiveIndex_notFlagged(t *testing.T) {
	for _, in := range insight.DetectAll(planFromFixture(t, "index_selective.json")) {
		if in.Kind == insight.KindInefficientIndex {
			t.Errorf("selective index flagged: %+v", in)
		}
	}
}

// A Seq Scan with a discarding filter is SlowScan's job, never InefficientIndex.
func TestInefficientIndex_seqScan_notFlagged(t *testing.T) {
	for _, in := range insight.DetectAll(planFromFixture(t, "slowscan_events.json")) {
		if in.Kind == insight.KindInefficientIndex {
			t.Errorf("seq scan flagged as inefficient index: %+v", in)
		}
	}
}
```

- [ ] **Step 3: Fixtures.** `testdata/inefficient_index.json` — Index Scan on `orders` using `orders_status_idx`, reads 20000, filters out 19900, returns 100:

```json
[{"Plan":{"Node Type":"Index Scan","Relation Name":"orders","Alias":"orders","Index Name":"orders_status_idx",
  "Scan Direction":"Forward","Actual Rows":100,"Actual Loops":1,"Plan Rows":100,
  "Total Cost":900.0,"Actual Total Time":40.0,"Rows Removed by Filter":19900}}]
```

`testdata/index_selective.json` — Index Scan returning 100, removing only 5:

```json
[{"Plan":{"Node Type":"Index Scan","Relation Name":"orders","Alias":"orders","Index Name":"orders_pkey",
  "Scan Direction":"Forward","Actual Rows":100,"Actual Loops":1,"Plan Rows":100,
  "Total Cost":12.0,"Actual Total Time":1.0,"Rows Removed by Filter":5}}]
```

- [ ] **Step 4: Implement `inefficientindex.go`:**

```go
package insight

import (
	"fmt"

	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
)

// indexNodeTypes are the index-access nodes Inefficient Index inspects. Seq
// Scan is excluded on purpose — that is SlowScanDetector's domain.
var indexNodeTypes = map[string]bool{
	"Index Scan":      true,
	"Index Only Scan": true,
	"Bitmap Heap Scan": true,
}

// InefficientIndexDetector flags an index-access node whose Filter discards
// most of the rows the index returned — the index matched a leading column but
// lacks the filtered column(s); a composite index likely helps.
type InefficientIndexDetector struct {
	MinRowsScanned int64
	MaxSelectivity float64
}

// DefaultInefficientIndex: read >= 1000, return <= 10%.
var DefaultInefficientIndex = InefficientIndexDetector{MinRowsScanned: 1000, MaxSelectivity: 0.10}

func (d InefficientIndexDetector) Detect(qp *lynceusv1.QueryPlan) []Insight {
	if qp == nil {
		return nil
	}
	var out []Insight
	walkPath(qp.GetRoot(), "", func(n *lynceusv1.PlanNode, path string) {
		if !indexNodeTypes[n.GetNodeType()] {
			return
		}
		loops := n.GetActualLoops()
		removed := n.GetRowsRemovedByFilter()
		if loops <= 0 || removed <= 0 {
			return
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
			Kind:         KindInefficientIndex,
			Severity:     slowScanSeverity(sel), // reuse the selectivity bands
			Fingerprint:  qp.GetFingerprint(),
			Relation:     n.GetRelationName(),
			NodePath:     path,
			RowsReturned: totalReturned,
			RowsScanned:  totalScanned,
			Selectivity:  sel,
			Detail: fmt.Sprintf(
				"Index %s on %s returned %d rows then discarded %d via Filter (%.2f%% kept); "+
					"a composite index covering the filtered column(s) would likely help.",
				n.GetIndexName(), n.GetRelationName(), totalScanned, totalScanned-totalReturned, sel*100,
			),
		})
	})
	return out
}
```

- [ ] **Step 5: Register** `DefaultInefficientIndex,`.
- [ ] **Step 6: Run** `go test ./internal/insight/... -count=1` → PASS (incl. the SlowScan cross-check).
- [ ] **Step 7: Commit** `git commit -am "feat(insight): Inefficient Index EXPLAIN insight (ly-u4t.3)"`

---

## Task 5: Mis-Estimate + Stale Stats detectors (`ly-u4t.6`, `ly-u4t.8`)

These two share one estimate-divergence helper and partition the node space cleanly: **Stale Stats** owns **leaf** relation-scan nodes (`len(plans)==0` and `relation_name != ""`) and recommends `ANALYZE`; **Mis-Estimate** owns **non-leaf** nodes (joins/aggregates/nested loops — `len(plans) > 0`) where the planner mis-combined selectivities. This split guarantees they never both fire on the same node.

**Files:** Create `internal/insight/misestimate.go` (both detectors + shared helper), `misestimate_test.go`, fixtures `testdata/stalestats_seqscan.json`, `testdata/misestimate_join.json`, `testdata/estimate_accurate.json`. Modify `insight.go`.

**Divergence metric:** per-loop estimate `e = plan_rows`, per-loop actual `a = actual_rows`; `ratio = max(e,a) / max(min(e,a), 1)`. Flag when `ratio >= MinRatio` (default 100) and `max(e,a) >= MinRows` (default 1000, so tiny tables don't spam). Require `actual_loops > 0` (need ANALYZE actuals).

- [ ] **Step 1:** `KindMisEstimate Kind = "mis_estimate"`, `KindStaleStats Kind = "stale_stats"`.

- [ ] **Step 2: Failing test** `misestimate_test.go`:

```go
package insight_test

import (
	"strings"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/insight"
)

func find(got []insight.Insight, k insight.Kind) *insight.Insight {
	for i := range got {
		if got[i].Kind == k {
			return &got[i]
		}
	}
	return nil
}

func TestStaleStats_leafScan_flagged(t *testing.T) {
	got := insight.DetectAll(planFromFixture(t, "stalestats_seqscan.json"))
	ss := find(got, insight.KindStaleStats)
	if ss == nil {
		t.Fatalf("no stale_stats insight: %+v", got)
	}
	if ss.Relation != "events" {
		t.Errorf("relation = %q, want events", ss.Relation)
	}
	if !strings.Contains(ss.Detail, "ANALYZE") {
		t.Errorf("detail missing ANALYZE recommendation: %q", ss.Detail)
	}
	// A leaf scan must NOT also trip Mis-Estimate.
	if find(got, insight.KindMisEstimate) != nil {
		t.Errorf("leaf scan double-flagged as mis_estimate")
	}
}

func TestMisEstimate_join_flagged(t *testing.T) {
	got := insight.DetectAll(planFromFixture(t, "misestimate_join.json"))
	me := find(got, insight.KindMisEstimate)
	if me == nil {
		t.Fatalf("no mis_estimate insight: %+v", got)
	}
	if !strings.Contains(me.NodePath, "Nested Loop") {
		t.Errorf("node path = %q, want a Nested Loop", me.NodePath)
	}
}

func TestEstimate_accurate_notFlagged(t *testing.T) {
	got := insight.DetectAll(planFromFixture(t, "estimate_accurate.json"))
	if find(got, insight.KindStaleStats) != nil || find(got, insight.KindMisEstimate) != nil {
		t.Errorf("accurate estimates flagged: %+v", got)
	}
}
```

- [ ] **Step 3: Fixtures.**

`testdata/stalestats_seqscan.json` — leaf Seq Scan, planner expected 5 rows, got 50000, no filter (so it does NOT trip SlowScan):

```json
[{"Plan":{"Node Type":"Seq Scan","Relation Name":"events","Alias":"events",
  "Plan Rows":5,"Actual Rows":50000,"Actual Loops":1,"Total Cost":1200.0,"Actual Total Time":80.0}}]
```

`testdata/misestimate_join.json` — Nested Loop expected 1 row, produced 80000; children estimate accurately (so only the join node trips Mis-Estimate, and children are leaves owned by Stale Stats but accurate → no stale_stats):

```json
[{"Plan":{"Node Type":"Nested Loop","Join Type":"Inner","Plan Rows":1,"Actual Rows":80000,"Actual Loops":1,
  "Total Cost":5000.0,"Actual Total Time":400.0,
  "Plans":[
    {"Node Type":"Index Scan","Relation Name":"a","Alias":"a","Index Name":"a_pkey","Plan Rows":400,"Actual Rows":400,"Actual Loops":1,"Total Cost":50.0,"Actual Total Time":5.0},
    {"Node Type":"Index Scan","Relation Name":"b","Alias":"b","Index Name":"b_a_id_idx","Plan Rows":200,"Actual Rows":200,"Actual Loops":400,"Total Cost":4.0,"Actual Total Time":0.5}]}}]
```

`testdata/estimate_accurate.json` — leaf scan with matching estimate/actual:

```json
[{"Plan":{"Node Type":"Seq Scan","Relation Name":"events","Alias":"events",
  "Plan Rows":48000,"Actual Rows":50000,"Actual Loops":1,"Total Cost":1200.0,"Actual Total Time":80.0}}]
```

- [ ] **Step 4: Implement `misestimate.go`:**

```go
package insight

import (
	"fmt"

	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
)

// estimateRatio reports how far the planner's per-loop row estimate diverged
// from the actual, as a multiplicative factor >= 1, plus whether actuals exist.
func estimateRatio(n *lynceusv1.PlanNode) (ratio float64, est, act int64, ok bool) {
	if n.GetActualLoops() <= 0 {
		return 0, 0, 0, false
	}
	est = n.GetPlanRows()
	act = n.GetActualRows()
	hi, lo := est, act
	if act > est {
		hi, lo = act, est
	}
	if lo < 1 {
		lo = 1
	}
	return float64(hi) / float64(lo), est, act, true
}

func estimateSeverity(ratio float64) Severity {
	switch {
	case ratio >= 1000:
		return SeverityHigh
	case ratio >= 100:
		return SeverityMedium
	default:
		return SeverityLow
	}
}

// StaleStatsDetector flags a LEAF relation-scan node whose own row estimate is
// far from actual — the table's statistics are likely stale; ANALYZE it.
type StaleStatsDetector struct {
	MinRatio float64
	MinRows  int64
}

var DefaultStaleStats = StaleStatsDetector{MinRatio: 100, MinRows: 1000}

func (d StaleStatsDetector) Detect(qp *lynceusv1.QueryPlan) []Insight {
	if qp == nil {
		return nil
	}
	var out []Insight
	walkPath(qp.GetRoot(), "", func(n *lynceusv1.PlanNode, path string) {
		if len(n.GetPlans()) != 0 || n.GetRelationName() == "" {
			return // not a leaf relation scan
		}
		ratio, est, act, ok := estimateRatio(n)
		if !ok || ratio < d.MinRatio {
			return
		}
		if est < d.MinRows && act < d.MinRows {
			return
		}
		out = append(out, Insight{
			Kind:         KindStaleStats,
			Severity:     estimateSeverity(ratio),
			Fingerprint:  qp.GetFingerprint(),
			Relation:     n.GetRelationName(),
			NodePath:     path,
			RowsReturned: act,
			RowsScanned:  est,
			Detail: fmt.Sprintf(
				"Scan on %s estimated %d rows but read %d (off %.0fx); table statistics are likely stale — ANALYZE %s.",
				n.GetRelationName(), est, act, ratio, n.GetRelationName(),
			),
		})
	})
	return out
}

// MisEstimateDetector flags a NON-leaf node (join/aggregate/...) whose row
// estimate is far from actual — the planner mis-combined child selectivities;
// extended statistics or a rewrite may help.
type MisEstimateDetector struct {
	MinRatio float64
	MinRows  int64
}

var DefaultMisEstimate = MisEstimateDetector{MinRatio: 100, MinRows: 1000}

func (d MisEstimateDetector) Detect(qp *lynceusv1.QueryPlan) []Insight {
	if qp == nil {
		return nil
	}
	var out []Insight
	walkPath(qp.GetRoot(), "", func(n *lynceusv1.PlanNode, path string) {
		if len(n.GetPlans()) == 0 {
			return // leaves belong to StaleStatsDetector
		}
		ratio, est, act, ok := estimateRatio(n)
		if !ok || ratio < d.MinRatio {
			return
		}
		if est < d.MinRows && act < d.MinRows {
			return
		}
		out = append(out, Insight{
			Kind:        KindMisEstimate,
			Severity:    estimateSeverity(ratio),
			Fingerprint: qp.GetFingerprint(),
			Relation:    n.GetRelationName(),
			NodePath:    path,
			RowsReturned: act,
			RowsScanned:  est,
			Detail: fmt.Sprintf(
				"%s estimated %d rows but produced %d (off %.0fx); the planner mis-combined child selectivities — consider extended statistics or a rewrite.",
				n.GetNodeType(), est, act, ratio,
			),
		})
	})
	return out
}
```

- [ ] **Step 5: Register** both `DefaultStaleStats,` and `DefaultMisEstimate,`.
- [ ] **Step 6: Run** `go test ./internal/insight/... -count=1` → PASS.
- [ ] **Step 7: Commit** `git commit -am "feat(insight): Mis-Estimate + Stale Stats EXPLAIN insights (ly-u4t.6, ly-u4t.8)"`

---

## Task 6: Full-suite + arch verification

- [ ] **Step 1:** `go build ./...` → clean.
- [ ] **Step 2:** `go test ./... -count=1 -timeout 600s` → all packages PASS (incl. `internal/proto` contract, `internal/api` insights surfacing — the new Kinds render generically via `web.InsightRow`).
- [ ] **Step 3: Arch invariant.** `grep -rn "pgxpool.New" cmd/ internal/collector` → exactly one (monitored DB). Detectors are pure; no store/config-DB access added.
- [ ] **Step 4: Privacy grep.** `grep -rn "GetSortMethod\|GetSortSpaceType\|GetNormalizedCondition" internal/insight` — confirm no detector emits `normalized_condition` or any raw string into `Detail`/`NodePath`. Re-run `go test ./internal/proto/... ./internal/insight/...`.

## Self-Review notes
- Spec coverage: `ly-u4t.1` Task 2, `.2` Task 3, `.3` Task 4, `.6`+`.8` Task 5. ✔
- Type consistency: getters `GetSortSpaceUsedKb`/`GetPeakMemoryUsageKb` follow protoc camelCase of `sort_space_used_kb`/`peak_memory_usage_kb`. ✔
- No new DB schema/migration. ✔  No new literal-capable field (all enum/count, allowlisted). ✔
