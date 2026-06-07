package insight

import (
	"fmt"

	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
)

// SlowScanDetector flags a Seq Scan whose Filter discards most of the rows it
// reads — a case where an index on the filtered column(s) would likely help.
type SlowScanDetector struct {
	MinRowsScanned int64   // skip scans smaller than this (a Seq Scan is fine there)
	MaxSelectivity float64 // flag only when returned/scanned is at or below this
}

// DefaultSlowScan is the registered detector: flag Seq Scans that read >= 1000
// rows and return <= 10% of them.
var DefaultSlowScan = SlowScanDetector{MinRowsScanned: 1000, MaxSelectivity: 0.10}

// Detect implements Detector.
func (d SlowScanDetector) Detect(qp *lynceusv1.QueryPlan) []Insight {
	if qp == nil {
		return nil
	}
	var out []Insight
	walkPath(qp.GetRoot(), "", func(n *lynceusv1.PlanNode, path string) {
		if n.GetNodeType() != "Seq Scan" {
			return
		}
		loops := n.GetActualLoops()
		if loops <= 0 {
			return // no ANALYZE actuals — cannot measure discard
		}
		removed := n.GetRowsRemovedByFilter()
		if removed <= 0 {
			return // discards nothing
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
			Kind:         KindSlowScan,
			Severity:     slowScanSeverity(sel),
			Fingerprint:  qp.GetFingerprint(),
			Relation:     n.GetRelationName(),
			NodePath:     path,
			RowsReturned: totalReturned,
			RowsScanned:  totalScanned,
			Selectivity:  sel,
			Detail: fmt.Sprintf(
				"Seq Scan on %s read %d rows and discarded %d (%.2f%% returned); "+
					"an index on the filtered column(s) would likely help.",
				n.GetRelationName(), totalScanned, totalScanned-totalReturned, sel*100,
			),
		})
	})
	return out
}

func slowScanSeverity(sel float64) Severity {
	switch {
	case sel <= 0.01:
		return SeverityHigh
	case sel <= 0.05:
		return SeverityMedium
	default:
		return SeverityLow
	}
}
