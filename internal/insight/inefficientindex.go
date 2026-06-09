package insight

import (
	"fmt"

	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
)

// indexNodeTypes are the index-access nodes Inefficient Index inspects. Seq
// Scan is excluded on purpose — that is SlowScanDetector's domain.
var indexNodeTypes = map[string]bool{
	"Index Scan":       true,
	"Index Only Scan":  true,
	"Bitmap Heap Scan": true,
}

// InefficientIndexDetector flags an index-access node whose Filter discards
// most of the rows the index returned — the index matched a leading column but
// lacks the filtered column(s); a composite index likely helps.
type InefficientIndexDetector struct {
	MinRowsScanned int64
	MaxSelectivity float64
}

// DefaultInefficientIndex: read >= 1000, return <= 10%.
var DefaultInefficientIndex = InefficientIndexDetector{MinRowsScanned: 1000, MaxSelectivity: 0.10}

func (d InefficientIndexDetector) Detect(qp *lynceusv1.QueryPlan) []Insight {
	if qp == nil {
		return nil
	}
	var out []Insight
	walkPath(qp.GetRoot(), "", func(n *lynceusv1.PlanNode, path string) {
		if !indexNodeTypes[n.GetNodeType()] {
			return
		}
		loops := n.GetActualLoops()
		removed := n.GetRowsRemovedByFilter()
		if loops <= 0 || removed <= 0 {
			return
		}
		returnedPerLoop := n.GetActualRows()
		scannedPerLoop := returnedPerLoop + removed
		if scannedPerLoop <= 0 {
			return
		}
		totalScanned := scannedPerLoop * loops
		if totalScanned < d.MinRowsScanned {
			return
		}
		sel := float64(returnedPerLoop) / float64(scannedPerLoop)
		if sel > d.MaxSelectivity {
			return
		}
		totalReturned := returnedPerLoop * loops
		out = append(out, Insight{
			Kind:         KindInefficientIndex,
			Severity:     slowScanSeverity(sel), // reuse the selectivity bands
			Fingerprint:  qp.GetFingerprint(),
			Relation:     n.GetRelationName(),
			NodePath:     path,
			RowsReturned: totalReturned,
			RowsScanned:  totalScanned,
			Selectivity:  sel,
			Detail: fmt.Sprintf(
				"Index %s on %s returned %d rows then discarded %d via Filter (%.2f%% kept); "+
					"a composite index covering the filtered column(s) would likely help.",
				n.GetIndexName(), n.GetRelationName(), totalScanned, totalScanned-totalReturned, sel*100,
			),
		})
	})
	return out
}
