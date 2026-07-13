// Package web holds the templ/HTMX server-rendered UI for Lynceus.
// Components here render only T1 (normalized, literal-free) data —
// the privacy guarantee is enforced at the wire-contract layer
// (internal/proto/lynceus/v1) and the templ side only displays what
// the API hands it.
package web

// QuerySort is the active column-sort state for the Top Queries table.
// Nav carries the page-navigation base paths (Scope-Shell Integration
// Contract); the fleet handler fills it, ly-ae6.3 refills it under scope.
type QuerySort struct {
	Col string // "calls" | "total" | "mean" | "rows" | "hit"
	Dir string // "asc" | "desc"
	Nav ScreenNav
}

// TopQuery is the view-model for one row in the top-queries table.
type TopQuery struct {
	Fingerprint     string
	NormalizedQuery string
	Calls           int64
	TotalTimeMs     float64
	MeanTimeMs      float64 // computed via MeanMs
	Rows            int64   // 0 until ly-58w.8; render "—"
	CacheHitPct     float64 // <0 unknown (ly-xqf.3); render "—"
	InsightCount    int     // per-fingerprint, computed from fetchInsights
	ClusterID       string  // drilldown link target; "" at fleet scope
	SparkPoints     string  // SVG polyline points; "" hides sparkline (ly-xqf.10)
}
