# Query-Advisor EXPLAIN Insights — Batch 2 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Add five EXPLAIN insight detectors to the live `internal/insight` engine — Large Offset (`ly-u4t.4`), Lossy Bitmaps (`ly-u4t.5`), Inefficient Nested Loops (`ly-u4t.9`), Wrong Index Due To ORDER BY (`ly-u4t.10`), Disk Spill Due To Low work_mem (`ly-u4t.11`) — each a pure `Detector` + fixtures, reusing existing `PlanNode` fields. **No new proto fields, no new schema.**

**Architecture:** Each detector mirrors the `ly-u4t.7` Slow Scan / `ly-u4t.1` Disk Sort template exactly: a `Kind` const, a `Default<Name>` detector value, a `Detect(qp *lynceusv1.QueryPlan) []Insight` that walks the plan via `walkPath`, and registration in the `registry` slice in `internal/insight/insight.go`. They surface automatically at `/insights` (the `DetectPlans` → registry path) — no UI/store/handler changes. Tests are fixture-driven (`internal/insight/testdata/*.json`, real-shaped PG EXPLAIN JSON) using the existing `planFromFixture(t, …)` helper.

**Tech Stack:** Go, the existing `internal/insight` engine, `internal/proto/lynceus/v1` `QueryPlan`/`PlanNode` (read-only — no edits).

**Privacy invariant:** `Insight.Detail` is templated from identifiers + counts only. The existing tests assert no `'`, `=`, or `::` appears in `Detail` — keep that. Never embed a `normalized_condition` value or any literal.

---

## Read first (the template)
- `internal/insight/insight.go` — `Kind`, `Severity` (low/medium/high), `Insight` struct, `Detector`, the `registry` slice, `walkPath`.
- `internal/insight/disksort.go` + `disksort_test.go` — the canonical detector + fixture-driven test shape.
- `internal/insight/slowscan.go` + `inefficientindex.go` — loop-aware counting (`actual_rows * actual_loops`), selectivity, and how `InefficientIndex` already keys (so the new ORDER BY detector does **not** duplicate it).
- `internal/insight/testdata/` — existing fixtures (`disksort_external.json`, `sort_inmemory.json`, …) for the exact JSON shape `planFromFixture` expects. Copy one, mutate it for each new pattern.

## PlanNode fields available (no new fields permitted)
`node_type, relation_name, index_name, alias, join_type, scan_direction, startup_cost, total_cost, plan_rows, plan_width, actual_startup_time_ms, actual_total_time_ms, actual_rows, actual_loops, normalized_condition, rows_removed_by_filter, sort_method, sort_space_type, sort_space_used_kb, hash_batches, original_hash_batches, peak_memory_usage_kb, plans[]`.

**Loop-aware totals:** a node's true row count is `actual_rows * actual_loops`; `rows_removed_by_filter` is **per loop** (multiply by `actual_loops`). Follow whatever `slowscan.go` does for consistency.

---

### Task 1: Large Offset (`ly-u4t.4`)

**Files:** Create `internal/insight/largeoffset.go`, `internal/insight/largeoffset_test.go`, `internal/insight/testdata/largeoffset.json`; Modify `internal/insight/insight.go` (Kind + registry).

**Detection rule:** walk for `node_type == "Limit"`. Compute `childRows = sum over direct children of (actual_rows * actual_loops)` and `returned = node.actual_rows * node.actual_loops`. `discarded = childRows - returned`. Flag when `discarded >= 1000` **and** `returned > 0` (pagination that skipped a large offset, not a bare cap that returned nothing). Severity: `>=100000` high, `>=10000` medium, else low. `Relation` = first descendant relation_name (via walk) or "". `Detail` (counts only): `fmt.Sprintf("Limit returned %d rows after its input produced %d (%d discarded by OFFSET); large-offset pagination scans and throws away the skipped rows — consider keyset pagination.", returned, childRows, discarded)`.

- [ ] **Step 1:** Add to `insight.go`: `KindLargeOffset Kind = "large_offset"` (in the const block) and `DefaultLargeOffset` to the `registry` slice.
- [ ] **Step 2:** Write `largeoffset_test.go` — a `*_flagged` test (fixture with a Limit over a child producing 50000 rows, returning 50 → medium) asserting `Kind==KindLargeOffset`, expected severity, `Detail` contains "OFFSET", and no banned literal chars; plus a `*_notFlagged` test (Limit returning all its input, discarded < 1000). Mirror `disksort_test.go`.
- [ ] **Step 3:** Run `go test ./internal/insight/... -run LargeOffset -timeout 3m` → FAIL (undefined / no fixture).
- [ ] **Step 4:** Create `testdata/largeoffset.json` (copy an existing fixture's envelope; root `Limit` node with a child Seq/Index Scan; set `Actual Rows`/`Actual Loops` so child=50000, limit=50). Implement `largeoffset.go` (struct `LargeOffsetDetector{HighDiscarded, MediumDiscarded int64}`, `DefaultLargeOffset = LargeOffsetDetector{100000, 10000}`, `Detect` per the rule).
- [ ] **Step 5:** Run `go test ./internal/insight/... -run LargeOffset -timeout 3m` → PASS.
- [ ] **Step 6:** Commit: `feat(insight): Large Offset EXPLAIN detector (ly-u4t.4)`.

### Task 2: Lossy Bitmaps (`ly-u4t.5`)

**Files:** Create `internal/insight/lossybitmap.go`, `_test.go`, `testdata/lossybitmap.json`; Modify `insight.go`.

**Detection rule:** walk for `node_type == "Bitmap Heap Scan"`. Let `removed = rows_removed_by_filter * actual_loops`, `kept = actual_rows * actual_loops`. Flag when `removed >= 1000` **and** `removed > kept` (the bitmap recheck discarded more than it kept — the lossy-bitmap / work_mem-pressure signature). Severity by `removed`: `>=1000000` high, `>=100000` medium, else low. `Detail`: `fmt.Sprintf("Bitmap Heap Scan kept %d rows and rechecked-then-discarded %d (lossy bitmap: work_mem too small to hold exact TIDs, so pages were stored lossily and every row rechecked).", kept, removed)`. (Faithful lossy-page counts aren't in the T1 plan; recheck-discard is the available proxy — note this in a code comment.)

- [ ] Steps mirror Task 1 (Kind `KindLossyBitmap = "lossy_bitmap"`, registry, fixture with a Bitmap Heap Scan kept=200 removed=200000 → medium, a notFlagged fixture/case where removed<kept, commit `feat(insight): Lossy Bitmaps EXPLAIN detector (ly-u4t.5)`).

### Task 3: Inefficient Nested Loops (`ly-u4t.9`)

**Files:** Create `internal/insight/nestedloop.go`, `_test.go`, `testdata/nestedloop.json`; Modify `insight.go`.

**Detection rule:** walk for `node_type == "Nested Loop"`. Inspect its children; let `innerLoops = max child actual_loops`. Flag when `innerLoops >= 1000` (inner side re-executed many times) **and** the loop emitted a non-trivial result (`node.actual_rows * node.actual_loops >= 1`). Severity by `innerLoops`: `>=1000000` high, `>=100000` medium, else low. `Relation` = inner child relation_name if present. `Detail`: `fmt.Sprintf("Nested Loop re-executed its inner side %d times; for large outer inputs a hash or merge join is usually cheaper than %d index lookups.", innerLoops, innerLoops)`.

- [ ] Steps mirror Task 1 (Kind `KindNestedLoop = "nested_loop"`, fixture: Nested Loop, inner child `Actual Loops`=200000 → medium; notFlagged: innerLoops < 1000; commit `feat(insight): Inefficient Nested Loops EXPLAIN detector (ly-u4t.9)`).

### Task 4: Wrong Index Due To ORDER BY (`ly-u4t.10`)

**Files:** Create `internal/insight/wrongindexorderby.go`, `_test.go`, `testdata/wrongindexorderby.json`; Modify `insight.go`.

**Detection rule (must NOT duplicate `InefficientIndex` `ly-u4t.3`):** First read `inefficientindex.go` to see exactly what it keys on. Then key this detector on the ORDER-BY signature: `node_type == "Index Scan"` or `"Index Scan Backward"` **with `scan_direction != ""`** (the planner walked the index in a specific order to satisfy ORDER BY) **and** high discard: `removed = rows_removed_by_filter * actual_loops >= 1000` **and** `removed > kept` (`kept = actual_rows*actual_loops`). The interpretation: the index was chosen to provide ordering, not selectivity, so it scans far more than it returns. Severity by `removed`: `>=1000000` high, `>=100000` medium, else low. `Relation` = relation_name; include `index_name` in Detail. `Detail`: `fmt.Sprintf("Index Scan on %s walked %s to satisfy ORDER BY but discarded %d of %d rows by filter; an index covering the WHERE clause (or a LIMIT-aware plan) would avoid the ordered full-index walk.", n.GetIndexName(), n.GetScanDirection(), removed, removed+kept)`. If `InefficientIndex` would also fire on the same node, that's acceptable (different framing) — but ensure the fixture's `scan_direction` is set so this detector keys distinctly; document the overlap in a comment.

- [ ] Steps mirror Task 1 (Kind `KindWrongIndexOrderBy = "wrong_index_order_by"`, fixture: Index Scan Backward, scan_direction "Backward", rows_removed_by_filter=200000 actual_rows=50 → medium; notFlagged: scan_direction "" or removed<kept; commit `feat(insight): Wrong Index (ORDER BY) EXPLAIN detector (ly-u4t.10)`).

### Task 5: Disk Spill Due To Low work_mem (`ly-u4t.11`)

**Files:** Create `internal/insight/diskspill.go`, `_test.go`, `testdata/diskspill.json`; Modify `insight.go`.

**Detection rule (complements per-node `ly-u4t.1` Disk Sort / `ly-u4t.2` Hash Batches — query-level work_mem recommendation, fires once per plan):** walk the whole tree, summing spilled kB: for each `Sort` with `sort_space_type == "Disk"` add `sort_space_used_kb`; for each `Hash` with `hash_batches > 1` add `peak_memory_usage_kb`. Let `spillKB = total`, `spillOps = count of spilling nodes`. Flag once (single Insight, `NodePath` = "plan") when `spillKB >= 1024` (≥1 MB spilled somewhere). Recommended work_mem = smallest power-of-two MB strictly greater than the largest single spilled node's kB (computed from counts — a derived count, literal-free). Severity by `spillKB`: `>=262144` (256 MB) high, `>=32768` (32 MB) medium, else low. `Detail`: `fmt.Sprintf("%d operator(s) spilled %d kB to disk; raising work_mem to ~%d MB would let them run in memory.", spillOps, spillKB, recMB)`. This is a query-level tuning insight; the per-node insights still fire independently.

- [ ] Steps mirror Task 1 (Kind `KindDiskSpill = "disk_spill"`, fixture: a plan with a Disk Sort (40000 kB) and a Hash with batches=4 peak=20000 kB → spillKB=60000 → medium, recMB next pow2>40000kB≈64MB; notFlagged: all in-memory; assert Detail contains "work_mem" and no banned chars; commit `feat(insight): Disk Spill (low work_mem) query-level detector (ly-u4t.11)`).

---

## Final verification
- [ ] `go build ./...` → success.
- [ ] `go test ./internal/insight/... -timeout 6m` → all pass (existing + 5 new).
- [ ] Confirm registry order in `insight.go` includes all five new `Default*` detectors.
- [ ] Sanity: `go test ./internal/api/... -run Insights -timeout 8m` (the `/insights` page still renders with the larger registry).
- [ ] Close beads `ly-u4t.4 ly-u4t.5 ly-u4t.9 ly-u4t.10 ly-u4t.11` on merge.

## Self-review
- All five reuse existing `PlanNode` fields — zero proto/schema change (honors the "no new schema" constraint).
- Each adds one `Kind` + one detector file + one fixture + one registry entry; no cross-task shared state except the `registry`/Kind const block in `insight.go` (edited additively per task — do tasks sequentially to avoid churn).
- `ly-u4t.10` explicitly differentiated from `ly-u4t.3` via the `scan_direction != ""` ORDER-BY key; `ly-u4t.11` differentiated from `.1`/`.2` as a query-level once-per-plan work_mem recommendation.
- `ly-u4t.18` (config tuning) is intentionally NOT here: faithful config recommendations need `pg_settings`/workload data the collector does not yet ship, so it needs its own advisor plan + a settings reader — out of scope for the "no new schema" detector batch.
