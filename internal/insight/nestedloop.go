package insight

import (
	"fmt"

	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
)

// NestedLoopDetector flags a Nested Loop whose inner side was re-executed a large
// number of times. Each outer row drives one inner execution, so a high inner
// actual_loops count means many repeated index lookups — for large outer inputs a
// hash or merge join is usually cheaper.
type NestedLoopDetector struct {
	HighLoops   int64 // >= this many inner loops -> high severity
	MediumLoops int64
	MinLoops    int64 // flag floor
}

// DefaultNestedLoop: flag at >=1000 inner loops; 1000000 -> high, 100000 -> medium.
var DefaultNestedLoop = NestedLoopDetector{HighLoops: 1000000, MediumLoops: 100000, MinLoops: 1000}

func (d NestedLoopDetector) Detect(qp *lynceusv1.QueryPlan) []Insight {
	if qp == nil {
		return nil
	}
	var out []Insight
	walkPath(qp.GetRoot(), "", func(n *lynceusv1.PlanNode, path string) {
		if n.GetNodeType() != "Nested Loop" {
			return
		}
		var innerLoops int64
		var innerRelation string
		for _, c := range n.GetPlans() {
			if l := c.GetActualLoops(); l > innerLoops {
				innerLoops = l
				innerRelation = c.GetRelationName()
			}
		}
		if innerLoops < d.MinLoops {
			return
		}
		if n.GetActualRows()*n.GetActualLoops() < 1 {
			return // join emitted nothing — not the costly-repeat signature
		}
		out = append(out, Insight{
			Kind:        KindNestedLoop,
			Severity:    d.severity(innerLoops),
			Fingerprint: qp.GetFingerprint(),
			Relation:    innerRelation,
			NodePath:    path,
			Detail: fmt.Sprintf(
				"Nested Loop re-executed its inner side %d times; "+
					"for large outer inputs a hash or merge join is usually cheaper than %d index lookups.",
				innerLoops, innerLoops,
			),
		})
	})
	return out
}

func (d NestedLoopDetector) severity(innerLoops int64) Severity {
	switch {
	case innerLoops >= d.HighLoops:
		return SeverityHigh
	case innerLoops >= d.MediumLoops:
		return SeverityMedium
	default:
		return SeverityLow
	}
}
