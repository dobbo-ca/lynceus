package web

import (
	"context"
	"strings"
	"testing"
)

func TestPlanScreen_TwoPaneWithProblemNode(t *testing.T) {
	root := &PlanNodeVM{NodeType: "Seq Scan", Relation: "orders", PlanRows: 5, ActualRows: 5000, TotalCost: 1234}
	vm := PlanVM{ServerID: "srv-1", Fingerprint: "3f2affff", Root: root}
	flatten(root, &vm.Flat)
	DecoratePlan(&vm, 0)
	var sb strings.Builder
	_ = PlanScreen(vm).Render(context.Background(), &sb)
	html := sb.String()
	for _, want := range []string{
		"Plans", "badge--live", "PLAN TREE — CLICK A NODE", "NODE DETAIL",
		"PROBLEM NODE", "EST ROWS", "ACTUAL ROWS", "stripe-crit", "orders",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("PlanScreen missing %q", want)
		}
	}
}
