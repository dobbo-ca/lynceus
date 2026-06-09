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
	KindSlowScan         Kind = "slow_scan"
	KindDiskSort         Kind = "disk_sort"
	KindHashBatches      Kind = "hash_batches"
	KindInefficientIndex Kind = "inefficient_index"
	KindMisEstimate      Kind = "mis_estimate"
	KindStaleStats       Kind = "stale_stats"
	KindLargeOffset      Kind = "large_offset"
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
	DefaultDiskSort,
	DefaultHashBatches,
	DefaultInefficientIndex,
	DefaultStaleStats,
	DefaultMisEstimate,
	DefaultLargeOffset,
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
