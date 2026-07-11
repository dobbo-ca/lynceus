package web

import (
	"fmt"
	"strings"
)

// ScreenNav carries the base routes a retrofitted screen's page-navigation
// links resolve against. THIS plan's fleet handlers fill it with fleet routes;
// ly-ae6.3 refills it with the scoped "/databases/{clusterID}/…" prefix when it
// re-mounts the screen, so no in-component page link hardcodes (and silently
// drops) scope. Base is the screen's own full-page route; Plan is the
// plan/drilldown route used as the fleet fallback when a row has no ClusterID.
type ScreenNav struct {
	Base string
	Plan string
}

// SevClass maps any engine severity vocabulary (insight low/medium/high,
// checks/advisor severity strings) onto the design's three token roles.
func SevClass(sev string) string {
	switch strings.ToLower(strings.TrimSpace(sev)) {
	case "critical", "crit", "high", "error", "fatal":
		return "crit"
	case "warn", "warning", "medium", "mod", "moderate":
		return "warn"
	default:
		return "info"
	}
}

// SevLabel returns the uppercase CRIT/WARN/INFO label for a severity.
func SevLabel(sev string) string {
	switch SevClass(sev) {
	case "crit":
		return "CRIT"
	case "warn":
		return "WARN"
	default:
		return "INFO"
	}
}

// SevChartVar returns the CSS color token for a severity class.
func SevChartVar(class string) string {
	switch class {
	case "crit":
		return "var(--crit)"
	case "warn":
		return "var(--warn)"
	default:
		return "var(--info)"
	}
}

// MeanMs is total time / calls, guarding division by zero.
func MeanMs(totalMs float64, calls int64) float64 {
	if calls <= 0 {
		return 0
	}
	return totalMs / float64(calls)
}

// recommendations is the package-authored, literal-free remediation guidance
// per insight kind. Keys mirror internal/insight.Kind string values.
var recommendations = map[string]string{
	"slow_scan":            "Add an index covering the filtered columns; a seq scan reads every row.",
	"disk_sort":            "Raise work_mem for this workload or add an index matching the sort key.",
	"disk_spill":           "Raise work_mem; the node spilled its hash/sort to disk.",
	"hash_batches":         "Raise work_mem so the hash fits in one batch.",
	"inefficient_index":    "The index is scanned but most rows are discarded — reconsider its column order.",
	"mis_estimate":         "Run ANALYZE; the planner's row estimate is far from actual.",
	"stale_stats":          "Statistics are stale — ANALYZE the relation so estimates track reality.",
	"large_offset":         "Replace OFFSET pagination with keyset (WHERE id > last) pagination.",
	"lossy_bitmap":         "The bitmap heap scan went lossy — raise work_mem or narrow the predicate.",
	"nested_loop":          "A nested loop over many rows is costly — check join estimates and indexes.",
	"wrong_index_order_by": "The chosen index does not match the ORDER BY — add a matching composite index.",
}

// RecommendationFor returns literal-free guidance for an insight kind, or "".
func RecommendationFor(kind string) string { return recommendations[kind] }

// KindLabel humanizes an insight kind for display. Known kinds get a curated
// label; unknown kinds fall back to UPPER SNAKE with underscores as spaces.
func KindLabel(kind string) string {
	known := map[string]string{
		"slow_scan":            "SLOW SEQ SCAN",
		"disk_sort":            "DISK SORT",
		"disk_spill":           "DISK SPILL",
		"hash_batches":         "HASH BATCHES",
		"inefficient_index":    "INEFFICIENT INDEX",
		"mis_estimate":         "MIS-ESTIMATE",
		"stale_stats":          "STALE STATS",
		"large_offset":         "LARGE OFFSET",
		"lossy_bitmap":         "LOSSY BITMAP",
		"nested_loop":          "NESTED LOOP",
		"wrong_index_order_by": "WRONG INDEX FOR ORDER BY",
	}
	if l, ok := known[kind]; ok {
		return l
	}
	return strings.ToUpper(strings.ReplaceAll(kind, "_", " "))
}

// intToStr avoids a fmt import churn in templ files for plain ints.
func intToStr(n int) string { return fmt.Sprintf("%d", n) }
