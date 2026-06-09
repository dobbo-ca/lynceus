package insight

import (
	"fmt"

	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
)

// LargeOffsetDetector flags a Limit node whose input produced far more rows than
// the Limit returned — the signature of large-offset pagination (OFFSET N), where
// Postgres scans and discards the skipped rows on every page.
type LargeOffsetDetector struct {
	HighDiscarded   int64 // >= this many discarded rows -> high severity
	MediumDiscarded int64
	MinDiscarded    int64 // flag floor: ignore Limits discarding fewer than this
}

// DefaultLargeOffset: flag at >=1000 discarded; 100000 -> high, 10000 -> medium.
var DefaultLargeOffset = LargeOffsetDetector{HighDiscarded: 100000, MediumDiscarded: 10000, MinDiscarded: 1000}

func (d LargeOffsetDetector) Detect(qp *lynceusv1.QueryPlan) []Insight {
	if qp == nil {
		return nil
	}
	var out []Insight
	walkPath(qp.GetRoot(), "", func(n *lynceusv1.PlanNode, path string) {
		if n.GetNodeType() != "Limit" {
			return
		}
		returned := n.GetActualRows() * n.GetActualLoops()
		if returned <= 0 {
			return // bare cap that returned nothing, not large-offset pagination
		}
		var childRows int64
		for _, c := range n.GetPlans() {
			childRows += c.GetActualRows() * c.GetActualLoops()
		}
		discarded := childRows - returned
		if discarded < d.MinDiscarded {
			return // not a large enough offset to matter
		}
		out = append(out, Insight{
			Kind:         KindLargeOffset,
			Severity:     d.severity(discarded),
			Fingerprint:  qp.GetFingerprint(),
			Relation:     firstDescendantRelation(n),
			NodePath:     path,
			RowsReturned: returned,
			RowsScanned:  childRows,
			Detail: fmt.Sprintf(
				"Limit returned %d rows after its input produced %d (%d discarded by OFFSET); "+
					"large-offset pagination scans and throws away the skipped rows — consider keyset pagination.",
				returned, childRows, discarded,
			),
		})
	})
	return out
}

func (d LargeOffsetDetector) severity(discarded int64) Severity {
	switch {
	case discarded >= d.HighDiscarded:
		return SeverityHigh
	case discarded >= d.MediumDiscarded:
		return SeverityMedium
	default:
		return SeverityLow
	}
}

// firstDescendantRelation returns the relation_name of the first node (DFS) that
// scans a relation, or "" if none — a structural identifier, never a literal.
func firstDescendantRelation(n *lynceusv1.PlanNode) string {
	var found string
	walkPath(n, "", func(node *lynceusv1.PlanNode, _ string) {
		if found == "" {
			if r := node.GetRelationName(); r != "" {
				found = r
			}
		}
	})
	return found
}
