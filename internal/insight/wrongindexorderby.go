package insight

import (
	"fmt"

	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
)

// orderByIndexNodeTypes are the ordered index-access nodes this detector inspects.
var orderByIndexNodeTypes = map[string]bool{
	"Index Scan":          true,
	"Index Scan Backward": true,
}

// WrongIndexOrderByDetector flags an Index Scan that was chosen to satisfy an
// ORDER BY (it walked the index in a specific scan_direction) rather than for
// selectivity, so it scans far more rows than it returns. This is keyed distinctly
// from InefficientIndex (ly-u4t.3): that detector keys on selectivity over a wider
// set of node types and ignores scan_direction; this one requires scan_direction
// != "" (the ORDER-BY signature). Both may fire on the same node — different
// framing — and that overlap is acceptable.
type WrongIndexOrderByDetector struct {
	HighRemoved   int64 // >= this many discarded rows -> high severity
	MediumRemoved int64
	MinRemoved    int64 // flag floor
}

// DefaultWrongIndexOrderBy: flag at >=1000 removed; 1000000 -> high, 100000 -> medium.
var DefaultWrongIndexOrderBy = WrongIndexOrderByDetector{HighRemoved: 1000000, MediumRemoved: 100000, MinRemoved: 1000}

func (d WrongIndexOrderByDetector) Detect(qp *lynceusv1.QueryPlan) []Insight {
	if qp == nil {
		return nil
	}
	var out []Insight
	walkPath(qp.GetRoot(), "", func(n *lynceusv1.PlanNode, path string) {
		if !orderByIndexNodeTypes[n.GetNodeType()] {
			return
		}
		if n.GetScanDirection() == "" {
			return // index not walked to satisfy an ORDER BY
		}
		loops := n.GetActualLoops()
		if loops <= 0 {
			return
		}
		removed := n.GetRowsRemovedByFilter() * loops
		kept := n.GetActualRows() * loops
		if removed < d.MinRemoved || removed <= kept {
			return // not the ordered-walk-discards-most signature
		}
		out = append(out, Insight{
			Kind:         KindWrongIndexOrderBy,
			Severity:     d.severity(removed),
			Fingerprint:  qp.GetFingerprint(),
			Relation:     n.GetRelationName(),
			NodePath:     path,
			RowsReturned: kept,
			RowsScanned:  kept + removed,
			Detail: fmt.Sprintf(
				"Index Scan on %s walked %s to satisfy ORDER BY but discarded %d of %d rows by filter; "+
					"an index covering the WHERE clause (or a LIMIT-aware plan) would avoid the ordered full-index walk.",
				n.GetIndexName(), n.GetScanDirection(), removed, removed+kept,
			),
		})
	})
	return out
}

func (d WrongIndexOrderByDetector) severity(removed int64) Severity {
	switch {
	case removed >= d.HighRemoved:
		return SeverityHigh
	case removed >= d.MediumRemoved:
		return SeverityMedium
	default:
		return SeverityLow
	}
}
