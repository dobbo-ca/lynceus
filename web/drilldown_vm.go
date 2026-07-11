package web

// DrilldownStat is one cell in the 4-stat grid.
type DrilldownStat struct{ Label, Value string }

// DrilldownInsight is one detected anti-pattern on the drilldown.
type DrilldownInsight struct {
	KindLabel string
	Node      string
	SevClass  string // "crit"|"warn"|"info"
	Detail    string
	Rec       string
}

// DrilldownWait is one per-query wait-breakdown bar.
type DrilldownWait struct {
	Label    string
	Pct      string
	WidthPct int
	ColorVar string
}

// QuerySampleVM is the T2 gate. It NEVER carries a literal — Locked is the
// only state this plan renders; the reveal endpoint (ly-8b0.6) is out of scope.
type QuerySampleVM struct {
	Locked   bool
	Group    string // reveal-rights group label (identifier, not a literal)
	ServerID string
}

// DrilldownVM is the full Query Drilldown view-model. It carries no literal
// query-sample field — the raw sample is a T2 audited reveal (out of scope).
type DrilldownVM struct {
	ClusterID       string
	ServerID        string
	Fingerprint     string
	NormalizedQuery string
	HasPlan         bool
	Stats           []DrilldownStat
	Insights        []DrilldownInsight
	Waits           []DrilldownWait
	Sample          QuerySampleVM
	CallsTrend      string // "" until ly-xqf.10
	CallsArea       string
	MeanTrend       string
	Nav             ScreenNav // ← back + VIEW PLAN base paths (fleet default; ly-ae6.3 refills)
}
