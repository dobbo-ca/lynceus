package insight

import (
	"fmt"

	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
)

// LossyBitmapDetector flags a Bitmap Heap Scan that rechecked-then-discarded more
// rows than it kept. When work_mem is too small to hold exact TIDs the bitmap is
// stored lossily (whole heap pages, not individual tuples), forcing every row on
// those pages to be rechecked against the filter. Faithful lossy-page counts are
// not in the T1 plan; the recheck-discard ratio is the available proxy.
type LossyBitmapDetector struct {
	HighRemoved   int64 // >= this many rechecked-and-discarded rows -> high severity
	MediumRemoved int64
	MinRemoved    int64 // flag floor
}

// DefaultLossyBitmap: flag at >=1000 removed; 1000000 -> high, 100000 -> medium.
var DefaultLossyBitmap = LossyBitmapDetector{HighRemoved: 1000000, MediumRemoved: 100000, MinRemoved: 1000}

func (d LossyBitmapDetector) Detect(qp *lynceusv1.QueryPlan) []Insight {
	if qp == nil {
		return nil
	}
	var out []Insight
	walkPath(qp.GetRoot(), "", func(n *lynceusv1.PlanNode, path string) {
		if n.GetNodeType() != "Bitmap Heap Scan" {
			return
		}
		loops := n.GetActualLoops()
		if loops <= 0 {
			return
		}
		removed := n.GetRowsRemovedByFilter() * loops
		kept := n.GetActualRows() * loops
		if removed < d.MinRemoved || removed <= kept {
			return // not the lossy-bitmap (recheck dominates) signature
		}
		out = append(out, Insight{
			Kind:         KindLossyBitmap,
			Severity:     d.severity(removed),
			Fingerprint:  qp.GetFingerprint(),
			Relation:     n.GetRelationName(),
			NodePath:     path,
			RowsReturned: kept,
			RowsScanned:  kept + removed,
			Detail: fmt.Sprintf(
				"Bitmap Heap Scan kept %d rows and rechecked-then-discarded %d "+
					"(lossy bitmap: work_mem too small to hold exact TIDs, so pages were stored lossily and every row rechecked).",
				kept, removed,
			),
		})
	})
	return out
}

func (d LossyBitmapDetector) severity(removed int64) Severity {
	switch {
	case removed >= d.HighRemoved:
		return SeverityHigh
	case removed >= d.MediumRemoved:
		return SeverityMedium
	default:
		return SeverityLow
	}
}
