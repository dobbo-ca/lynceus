package insight

import (
	"fmt"

	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
)

// DiskSortDetector flags a Sort node that spilled to disk (Sort Space Type ==
// "Disk"), i.e. work_mem was too small to sort in memory.
type DiskSortDetector struct {
	HighKB   int64 // >= this many kB spilled -> high severity
	MediumKB int64
}

// DefaultDiskSort: 256 MB -> high, 32 MB -> medium.
var DefaultDiskSort = DiskSortDetector{HighKB: 262144, MediumKB: 32768}

func (d DiskSortDetector) Detect(qp *lynceusv1.QueryPlan) []Insight {
	if qp == nil {
		return nil
	}
	var out []Insight
	walkPath(qp.GetRoot(), "", func(n *lynceusv1.PlanNode, path string) {
		if n.GetNodeType() != "Sort" || n.GetSortSpaceType() != "Disk" {
			return
		}
		usedKB := n.GetSortSpaceUsedKb()
		out = append(out, Insight{
			Kind:        KindDiskSort,
			Severity:    d.severity(usedKB),
			Fingerprint: qp.GetFingerprint(),
			NodePath:    path,
			Detail: fmt.Sprintf(
				"Sort spilled to disk (%s, %d kB used); increasing work_mem would let it sort in memory.",
				n.GetSortMethod(), usedKB,
			),
		})
	})
	return out
}

func (d DiskSortDetector) severity(usedKB int64) Severity {
	switch {
	case usedKB >= d.HighKB:
		return SeverityHigh
	case usedKB >= d.MediumKB:
		return SeverityMedium
	default:
		return SeverityLow
	}
}
