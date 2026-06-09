package insight

import (
	"fmt"

	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
)

// HashBatchesDetector flags a Hash node that batched to disk (Hash Batches > 1)
// because the hash table did not fit in work_mem.
type HashBatchesDetector struct {
	HighBatches   int64
	MediumBatches int64
}

// DefaultHashBatches: >=64 batches high, >=8 medium.
var DefaultHashBatches = HashBatchesDetector{HighBatches: 64, MediumBatches: 8}

func (d HashBatchesDetector) Detect(qp *lynceusv1.QueryPlan) []Insight {
	if qp == nil {
		return nil
	}
	var out []Insight
	walkPath(qp.GetRoot(), "", func(n *lynceusv1.PlanNode, path string) {
		if n.GetNodeType() != "Hash" {
			return
		}
		batches := n.GetHashBatches()
		if batches <= 1 {
			return
		}
		detail := fmt.Sprintf(
			"Hash table batched to disk (%d batches, %d kB peak); increasing work_mem would keep it in memory.",
			batches, n.GetPeakMemoryUsageKb(),
		)
		if orig := n.GetOriginalHashBatches(); orig > 0 && batches > orig {
			detail += fmt.Sprintf(" Re-batched at runtime from %d (row estimate too low).", orig)
		}
		out = append(out, Insight{
			Kind:        KindHashBatches,
			Severity:    d.severity(batches),
			Fingerprint: qp.GetFingerprint(),
			NodePath:    path,
			Detail:      detail,
		})
	})
	return out
}

func (d HashBatchesDetector) severity(batches int64) Severity {
	switch {
	case batches >= d.HighBatches:
		return SeverityHigh
	case batches >= d.MediumBatches:
		return SeverityMedium
	default:
		return SeverityLow
	}
}
