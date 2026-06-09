# Checks Bundle — Index Advisor Notification (`ly-u4t.27`) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development or executing-plans. Steps use `- [ ]`.

**Goal:** Add the Index-Advisor Checks bundle (`ly-u4t.27`) — a `Check` that turns the existing api-side Index Advisor's missing-index recommendations into persisted, notifiable check results, proving the Layer-2 framework handles advisor-backed notification bundles (the second real bundle after wraparound).

**Architecture:** Keep `internal/checks` proto-free. The **scheduler** (the I/O layer) gathers plans + table sizes per server and runs the existing pure `advisor.RecommendIndexes`, then puts the resulting `[]advisor.IndexRecommendation` into `checks.Input`. The new pure `IndexAdvisorCheck` thresholds those recommendations into `Result`s (severity from seq-scan volume / table size). Results persist + notify through the framework already built in `internal/checks`. No new schema, no proto change, no new store read (reuses `ListPlanKeys`/`TopPlansByQuery`/`LatestTableStats`/`advisor.RecommendIndexes`).

**Tech Stack:** Go; existing `internal/advisor` (RecommendIndexes), `internal/checks` engine + scheduler, `internal/store` reads.

---

## Read first
- `internal/checks/checks.go` (the `Input`, `Check`, `Result`, `Severity` info/warning/critical, `Register`).
- `internal/checks/scheduler.go` (`assembleInput` — you extend it; it already reads `LatestTableStats` + `LatestFreezeAges`).
- `internal/checks/wraparound.go` (the canonical bundle: `init(){Register(...)}` + pure `Eval`).
- `internal/api/index_advisor.go` `fetchIndexAdvice` (the exact reuse path: `ListPlanKeys(ctx,since,now,200)` → per key `TopPlansByQuery(ctx,ServerID,Fingerprint,since,now,10)` collecting `p.Plan`; per server `LatestTableStats` → `advisor.TableInfo{TotalBytes,SeqScans}` keyed by `ObjectName`; `advisor.RecommendIndexes(plans, tables)`).
- `internal/advisor/index.go` (`RecommendIndexes(plans []*lynceusv1.QueryPlan, tables map[string]TableInfo) []IndexRecommendation`; `IndexRecommendation{Relation, Columns []string, QueryCount int, TotalBytes int64, SeqScans int64, Fingerprints []string, Rationale string}`).

---

### Task 1: `Input.IndexRecs` + `IndexAdvisorCheck`

**Files:** Modify `internal/checks/checks.go` (add field); Create `internal/checks/indexadvisor.go`, `internal/checks/indexadvisor_test.go`.

- [ ] **Step 1:** In `internal/checks/checks.go`, add an import `advisor "github.com/dobbo-ca/lynceus/internal/advisor"` and a field to `Input`:
```go
	IndexRecs []advisor.IndexRecommendation // populated by the scheduler (ly-u4t.27)
```
(Confirm the real module path of the advisor package by reading its file header; match it.)

- [ ] **Step 2:** Write the failing test (`indexadvisor_test.go`):
```go
package checks

import (
	"testing"

	"github.com/dobbo-ca/lynceus/internal/advisor"
)

func TestIndexAdvisorWarnsOnHotSeqScan(t *testing.T) {
	in := Input{ServerID: "srv-a", IndexRecs: []advisor.IndexRecommendation{
		{Relation: "public.orders", Columns: []string{"customer_id"}, QueryCount: 12,
			TotalBytes: 800_000_000, SeqScans: 500_000, Rationale: "frequent seq scan"},
		{Relation: "public.tiny", Columns: []string{"k"}, QueryCount: 1,
			TotalBytes: 4096, SeqScans: 3, Rationale: "rare"},
	}}
	got := IndexAdvisorCheck{}.Eval(in)
	if len(got) != 2 {
		t.Fatalf("want 2 results, got %d: %+v", len(got), got)
	}
	var hot *Result
	for i := range got {
		if got[i].Object == "public.orders" {
			hot = &got[i]
		}
	}
	if hot == nil || hot.Severity != SeverityWarning || hot.CheckID != "queries.missing_index" {
		t.Fatalf("hot recommendation wrong: %+v", hot)
	}
	for _, banned := range []string{"'", "::"} {
		if containsAny(got, banned) {
			t.Fatalf("possible literal %q in details", banned)
		}
	}
}

func containsAny(rs []Result, sub string) bool {
	for _, r := range rs {
		if len(sub) > 0 && (indexOf(r.Detail, sub) >= 0) {
			return true
		}
	}
	return false
}
func indexOf(s, sub string) int { // tiny helper; or use strings.Contains
	return len([]byte(s)) - len([]byte(s)) // placeholder — replace with strings.Index in impl
}
```
> Use `strings.Contains` directly in the real test; the placeholder above just marks intent. Keep the banned-char assertion (Detail must be literal-free: no `'`, `::`).

- [ ] **Step 3:** Run `go test ./internal/checks/... -run TestIndexAdvisor -timeout 3m` → FAIL (undefined).

- [ ] **Step 4:** Implement `internal/checks/indexadvisor.go`:
```go
package checks

import "fmt"

func init() { Register(IndexAdvisorCheck{}) }

// IndexAdvisorCheck turns Index Advisor missing-index recommendations into
// check results. Advisory (not safety): info by default, warning when the
// table is large and heavily seq-scanned. Counts/identifiers only — T1.
type IndexAdvisorCheck struct{}

const (
	idxWarnSeqScans = 100_000      // heavy sequential scanning
	idxWarnBytes    = 100_000_000  // ~100 MB+ table
)

func (IndexAdvisorCheck) ID() string       { return "queries.missing_index" }
func (IndexAdvisorCheck) Category() string { return "queries" }

func (IndexAdvisorCheck) Eval(in Input) []Result {
	var out []Result
	for _, rec := range in.IndexRecs {
		sev := SeverityInfo
		if rec.SeqScans >= idxWarnSeqScans && rec.TotalBytes >= idxWarnBytes {
			sev = SeverityWarning
		}
		cols := ""
		for i, c := range rec.Columns {
			if i > 0 {
				cols += ", "
			}
			cols += c
		}
		out = append(out, Result{
			CheckID:  "queries.missing_index",
			Category: "queries",
			Severity: sev,
			Status:   StatusFiring,
			Object:   rec.Relation,
			Detail: fmt.Sprintf("missing index on (%s): %d queries seq-scan this %d-byte table %d times — an index would avoid the scans.",
				cols, rec.QueryCount, rec.TotalBytes, rec.SeqScans),
		})
	}
	return out
}
```
> `rec.Columns` are plan-derived column identifiers (already T1 / literal-free in the advisor). If you want to be defensive, the advisor guarantees these are identifiers, not literals.

- [ ] **Step 5:** Run `go test ./internal/checks/... -run TestIndexAdvisor -timeout 3m` → PASS.

- [ ] **Step 6:** Commit: `feat(checks): Index-Advisor missing-index notification bundle (ly-u4t.27)`.

### Task 2: Scheduler assembles IndexRecs

**Files:** Modify `internal/checks/scheduler.go`.

- [ ] **Step 1:** In `assembleInput`, after the freeze-age block, gather plans + table info and run the advisor (mirror `fetchIndexAdvice`, but server-scoped to `serverID`):
```go
	keys, err := sc.stats.ListPlanKeys(ctx, now.AddDate(0, 0, -30), now, 200)
	if err == nil {
		var plans []*lynceusv1.QueryPlan
		for _, k := range keys {
			if k.ServerID != serverID {
				continue
			}
			ps, err := sc.stats.TopPlansByQuery(ctx, serverID, k.Fingerprint, now.AddDate(0, 0, -30), now, 10)
			if err != nil {
				continue
			}
			for _, p := range ps {
				plans = append(plans, p.Plan)
			}
		}
		tables := map[string]advisor.TableInfo{}
		for _, t := range in.TableStats { // already gathered above; or re-read LatestTableStats
			// NOTE: in.TableStats is the checks projection (no TotalBytes/SeqScan exposed there).
			// Use the raw LatestTableStats rows instead — see Step 2.
			_ = t
		}
		_ = plans
		_ = tables
	}
```
> The above is a sketch. **Implement it cleanly:** because `in.TableStats` (checks projection) lacks `TotalBytes`, re-read `sc.stats.LatestTableStats(ctx, serverID, now)` once and build `map[string]advisor.TableInfo{ObjectName: {TotalBytes, SeqScans:SeqScan}}` exactly like `fetchIndexAdvice`. Then `in.IndexRecs = advisor.RecommendIndexes(plans, tables)`. Add imports `advisor` and `lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"`.

- [ ] **Step 2:** Final `assembleInput` shape for index recs:
```go
	idxKeys, err := sc.stats.ListPlanKeys(ctx, now.AddDate(0, 0, -30), now, 200)
	if err == nil {
		var plans []*lynceusv1.QueryPlan
		for _, k := range idxKeys {
			if k.ServerID != serverID {
				continue
			}
			ps, e := sc.stats.TopPlansByQuery(ctx, serverID, k.Fingerprint, now.AddDate(0, 0, -30), now, 10)
			if e != nil {
				continue
			}
			for _, p := range ps {
				plans = append(plans, p.Plan)
			}
		}
		idxTables := map[string]advisor.TableInfo{}
		for _, t := range tables { // `tables` = the LatestTableStats slice already read at the top of assembleInput
			ti := idxTables[t.ObjectName]
			ti.TotalBytes = t.TotalBytes
			ti.SeqScans = t.SeqScan
			idxTables[t.ObjectName] = ti
		}
		in.IndexRecs = advisor.RecommendIndexes(plans, idxTables)
	}
```
> Ensure the `LatestTableStats` rows are in scope (the existing `assembleInput` reads them into a local — reuse that local rather than re-reading). Adjust variable names to the real code.

- [ ] **Step 3:** Run the scheduler integration test that already exists: `go test ./internal/checks/... -timeout 10m`. It must still pass (the existing `TestSchedulerRunOncePersistsAndNotifies` seeds table_stats; with no plans, `IndexRecs` is empty → no extra results, so the test's assertions on the fake check still hold). If the existing test asserts an exact result count that the real registered checks (wraparound, index advisor) would inflate, note that those checks see empty freeze/plan data in that test → emit nothing, so counts are unaffected. Fix only if it actually breaks.

- [ ] **Step 4:** `go build ./...` → success.

- [ ] **Step 5:** Commit: `feat(checks): scheduler assembles Index Advisor recommendations into Input (ly-u4t.27)`.

---

## Final verification
- [ ] `go build ./...`
- [ ] `go test ./internal/checks/... ./internal/advisor/... -p 1 -timeout 12m` → all pass.
- [ ] Arch unaffected (no collector/proto change): a quick `git diff --stat` should touch only `internal/checks/*`.
- [ ] Close `ly-u4t.27` on merge.

## Self-review
- Reuses `advisor.RecommendIndexes` + existing store reads — zero new schema/proto.
- `internal/checks` now imports `internal/advisor` + `lynceusv1` (scheduler only for proto; the pure check imports only `advisor` types) — no import cycle (advisor/proto don't import checks).
- Severity capped at warning (advisory, not safety) — distinct from wraparound's critical.
- `.21` (new-slow-query regression) intentionally NOT bundled here: it needs two-window query_stats comparison + first-seen tracking — its own plan. The advisor-insight-notification half could later reuse this same scheduler-assembles-then-check pattern with `insight.DetectPlans`.
