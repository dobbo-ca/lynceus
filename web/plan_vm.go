package web

import (
	"fmt"

	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
)

// PlanNodeVM is the view-model for one node in the plan tree. Every field
// is a structural identifier, a normalized (literal-free) condition, a
// count, a size, or a metric — never a query literal (mirrors the proto
// invariant, proto/lynceus/v1/plan.proto:9-13).
type PlanNodeVM struct {
	Depth               int    // 0 = root; used to indent the flat grid
	NodeType            string // "Seq Scan", "Hash Join", ...
	Relation            string // table identifier, "" if none
	Index               string // index identifier, "" if none
	JoinType            string // "Inner" | "Left" | "", structural
	ScanDirection       string // "Forward" | "Backward" | ""
	Condition           string // normalized condition ($n), "" if not provable
	PlanRows            int64
	ActualRows          int64
	ActualLoops         int64
	TotalCost           float64
	ActualTotalTimeMs   float64
	RowsRemovedByFilter int64
	Children            []*PlanNodeVM
	Idx                 int    // flat-list index (assigned by DecoratePlan)
	Problem             bool   // misestimated node (>10x plan vs actual)
	EstColorVar         string // est→act row color token
}

// PlanVariant is one plan variant tab for a fingerprint.
type PlanVariant struct {
	FP       string
	Label    string // "seen 12× · 4.2ms"
	Selected bool
	Href     string
}

// PlanVM is the full view-model for the /plan surface. Empty drives the
// "no plan stored" branch in the template.
type PlanVM struct {
	ServerID    string
	Fingerprint string
	Empty       bool
	Root        *PlanNodeVM   // nil when Empty
	Flat        []*PlanNodeVM // depth-first pre-order, nil when Empty
	Variants    []PlanVariant
	VariantIdx  int // selected plan-variant index (the ?plan= param)
	SelectedIdx int // selected node index within the variant (the ?node= param)
	Selected    *PlanNodeVM
}

// DecoratePlan assigns flat indices, flags misestimated nodes (actual vs plan
// rows off by >10x in either direction), colors the est→act, and selects the
// node at selectedIdx (clamped). Call after ToPlanVM builds Root+Flat.
func DecoratePlan(vm *PlanVM, selectedIdx int) {
	for i, n := range vm.Flat {
		n.Idx = i
		ratio := misestimateRatio(n.PlanRows, n.ActualRows)
		if ratio >= 10 {
			n.Problem = true
			n.EstColorVar = "var(--critT)"
		} else {
			n.EstColorVar = "var(--dim)"
		}
	}
	if len(vm.Flat) == 0 {
		return
	}
	if selectedIdx < 0 || selectedIdx >= len(vm.Flat) {
		selectedIdx = 0
	}
	vm.SelectedIdx = selectedIdx
	vm.Selected = vm.Flat[selectedIdx]
}

// misestimateRatio is the larger of plan/actual and actual/plan (guarding zero).
func misestimateRatio(plan, actual int64) float64 {
	p, a := float64(plan), float64(actual)
	if p < 1 {
		p = 1
	}
	if a < 1 {
		a = 1
	}
	if a > p {
		return a / p
	}
	return p / a
}

// ToPlanVM maps a stored QueryPlan into the view-model. It is nil-safe: a
// nil plan or a plan with no root node yields an Empty PlanVM that still
// carries the requested ServerID/Fingerprint so the page can echo them.
func ToPlanVM(serverID string, p *lynceusv1.QueryPlan) PlanVM {
	vm := PlanVM{ServerID: serverID, Fingerprint: p.GetFingerprint()}
	root := p.GetRoot()
	if p == nil || root == nil {
		vm.Empty = true
		return vm
	}
	vm.Root = toNodeVM(root, 0)
	flatten(vm.Root, &vm.Flat)
	return vm
}

// toNodeVM converts one proto node (and its subtree) to a PlanNodeVM. All
// getters are nil-safe (plan.pb.go:192-309).
func toNodeVM(n *lynceusv1.PlanNode, depth int) *PlanNodeVM {
	node := &PlanNodeVM{
		Depth:               depth,
		NodeType:            n.GetNodeType(),
		Relation:            n.GetRelationName(),
		Index:               n.GetIndexName(),
		JoinType:            n.GetJoinType(),
		ScanDirection:       n.GetScanDirection(),
		Condition:           n.GetNormalizedCondition(),
		PlanRows:            n.GetPlanRows(),
		ActualRows:          n.GetActualRows(),
		ActualLoops:         n.GetActualLoops(),
		TotalCost:           n.GetTotalCost(),
		ActualTotalTimeMs:   n.GetActualTotalTimeMs(),
		RowsRemovedByFilter: n.GetRowsRemovedByFilter(),
	}
	for _, c := range n.GetPlans() {
		node.Children = append(node.Children, toNodeVM(c, depth+1))
	}
	return node
}

// flatten appends nodes depth-first (pre-order: visit node, then recurse
// into children) so the flat grid lists a node before its descendants.
func flatten(n *PlanNodeVM, out *[]*PlanNodeVM) {
	if n == nil {
		return
	}
	*out = append(*out, n)
	for _, c := range n.Children {
		flatten(c, out)
	}
}

// FmtCost renders a cost/metric for the grid; kept here so the .templ has
// no fmt import beyond what it already uses.
func FmtCost(v float64) string { return fmt.Sprintf("%.2f", v) }
