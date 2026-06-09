package insight

import (
	"fmt"

	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
)

// DiskSpillDetector is a query-level work_mem recommendation. It walks the whole
// plan, summing kB spilled to disk by every Sort (Sort Space Type == "Disk") and
// every batched Hash (Hash Batches > 1), and fires once per plan when the total
// is significant. It complements the per-node Disk Sort (ly-u4t.1) and Hash
// Batches (ly-u4t.2) detectors — those still fire independently; this one rolls
// the spills up into a single tuning suggestion.
type DiskSpillDetector struct {
	HighKB   int64 // total spilled >= this -> high severity (256 MB)
	MediumKB int64 // 32 MB
	MinKB    int64 // flag floor (1 MB)
}

// DefaultDiskSpill: flag at >=1 MB total spilled; 256 MB -> high, 32 MB -> medium.
var DefaultDiskSpill = DiskSpillDetector{HighKB: 262144, MediumKB: 32768, MinKB: 1024}

func (d DiskSpillDetector) Detect(qp *lynceusv1.QueryPlan) []Insight {
	if qp == nil {
		return nil
	}
	var spillKB, largestKB, spillOps int64
	walkPath(qp.GetRoot(), "", func(n *lynceusv1.PlanNode, _ string) {
		var nodeKB int64
		switch {
		case n.GetNodeType() == "Sort" && n.GetSortSpaceType() == "Disk":
			nodeKB = n.GetSortSpaceUsedKb()
		case n.GetNodeType() == "Hash" && n.GetHashBatches() > 1:
			nodeKB = n.GetPeakMemoryUsageKb()
		}
		if nodeKB <= 0 {
			return
		}
		spillKB += nodeKB
		spillOps++
		if nodeKB > largestKB {
			largestKB = nodeKB
		}
	})
	if spillKB < d.MinKB {
		return nil
	}
	recMB := nextPow2MB(largestKB)
	return []Insight{{
		Kind:        KindDiskSpill,
		Severity:    d.severity(spillKB),
		Fingerprint: qp.GetFingerprint(),
		NodePath:    "plan",
		Detail: fmt.Sprintf(
			"%d operator(s) spilled %d kB to disk; raising work_mem to ~%d MB would let them run in memory.",
			spillOps, spillKB, recMB,
		),
	}}
}

func (d DiskSpillDetector) severity(spillKB int64) Severity {
	switch {
	case spillKB >= d.HighKB:
		return SeverityHigh
	case spillKB >= d.MediumKB:
		return SeverityMedium
	default:
		return SeverityLow
	}
}

// nextPow2MB returns the smallest power-of-two MB whose byte size strictly
// exceeds kb — a derived count, never a literal.
func nextPow2MB(kb int64) int64 {
	mb := int64(1)
	for mb*1024 <= kb {
		mb *= 2
	}
	return mb
}
