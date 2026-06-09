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
			Kind:         KindMisEstimate,
			Severity:     estimateSeverity(ratio),
			Fingerprint:  qp.GetFingerprint(),
			Relation:     n.GetRelationName(),
			NodePath:     path,
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
