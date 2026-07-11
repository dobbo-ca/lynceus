package web

import (
	"testing"

	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
)

func TestToPlanVM_buildsTreeAndFlatDFS(t *testing.T) {
	plan := &lynceusv1.QueryPlan{
		Fingerprint:       "fp-1",
		FormatVersion:     1,
		TotalCost:         102.84,
		ActualTotalTimeMs: 12.5,
		Root: &lynceusv1.PlanNode{
			NodeType:  "Aggregate",
			TotalCost: 102.84,
			PlanRows:  1,
			Plans: []*lynceusv1.PlanNode{
				{
					NodeType:            "Hash Join",
					JoinType:            "Inner",
					TotalCost:           99.0,
					PlanRows:            2532,
					NormalizedCondition: "(a.id = b.id)",
					Plans: []*lynceusv1.PlanNode{
						{NodeType: "Seq Scan", RelationName: "orders", TotalCost: 50.0, PlanRows: 2532, ActualRows: 2532, ActualLoops: 1, RowsRemovedByFilter: 17, NormalizedCondition: "(total > $1)"},
						{NodeType: "Index Scan", RelationName: "customers", IndexName: "customers_pkey", TotalCost: 8.3, PlanRows: 1, ActualRows: 1, ActualLoops: 1},
					},
				},
			},
		},
	}

	vm := ToPlanVM("srv-1", plan)

	if vm.ServerID != "srv-1" || vm.Fingerprint != "fp-1" {
		t.Fatalf("header = (%q,%q), want (srv-1,fp-1)", vm.ServerID, vm.Fingerprint)
	}
	if vm.Empty {
		t.Fatal("vm.Empty = true, want false for a populated plan")
	}
	// Tree shape: root -> Hash Join -> [Seq Scan, Index Scan]
	if vm.Root == nil || vm.Root.NodeType != "Aggregate" {
		t.Fatalf("root node = %+v, want Aggregate", vm.Root)
	}
	if len(vm.Root.Children) != 1 || vm.Root.Children[0].NodeType != "Hash Join" {
		t.Fatalf("root children = %+v, want one Hash Join", vm.Root.Children)
	}
	hj := vm.Root.Children[0]
	if hj.JoinType != "Inner" || hj.Condition != "(a.id = b.id)" {
		t.Errorf("hash join = (join=%q,cond=%q)", hj.JoinType, hj.Condition)
	}
	if len(hj.Children) != 2 {
		t.Fatalf("hash join children = %d, want 2", len(hj.Children))
	}
	if hj.Children[0].Relation != "orders" || hj.Children[1].Index != "customers_pkey" {
		t.Errorf("join children rel/idx = (%q,%q)", hj.Children[0].Relation, hj.Children[1].Index)
	}
	// Flat is depth-first pre-order: Aggregate, Hash Join, Seq Scan, Index Scan.
	wantOrder := []string{"Aggregate", "Hash Join", "Seq Scan", "Index Scan"}
	if len(vm.Flat) != len(wantOrder) {
		t.Fatalf("flat len = %d, want %d", len(vm.Flat), len(wantOrder))
	}
	for i, w := range wantOrder {
		if vm.Flat[i].NodeType != w {
			t.Errorf("flat[%d] = %q, want %q", i, vm.Flat[i].NodeType, w)
		}
	}
	// Depth is set so the tree can indent.
	if vm.Flat[2].Depth != 2 {
		t.Errorf("Seq Scan depth = %d, want 2", vm.Flat[2].Depth)
	}
	// A count field round-trips (literal-free).
	if vm.Flat[2].RowsRemovedByFilter != 17 {
		t.Errorf("Seq Scan RowsRemovedByFilter = %d, want 17", vm.Flat[2].RowsRemovedByFilter)
	}
}

func TestToPlanVM_nilPlanIsEmpty(t *testing.T) {
	vm := ToPlanVM("srv-1", nil)
	if !vm.Empty {
		t.Fatal("nil plan: Empty = false, want true")
	}
	if vm.ServerID != "srv-1" {
		t.Errorf("ServerID = %q, want srv-1 (header preserved on empty)", vm.ServerID)
	}
	if vm.Root != nil || len(vm.Flat) != 0 {
		t.Errorf("nil plan should yield no root and no flat rows")
	}
}

func TestToPlanVM_nilRootIsEmpty(t *testing.T) {
	// A QueryPlan with no Root node (GetRoot() == nil) is also empty.
	vm := ToPlanVM("srv-1", &lynceusv1.QueryPlan{Fingerprint: "fp-x"})
	if !vm.Empty {
		t.Fatal("plan with nil root: Empty = false, want true")
	}
	if vm.Fingerprint != "fp-x" {
		t.Errorf("Fingerprint = %q, want fp-x", vm.Fingerprint)
	}
}

func TestDecoratePlan_FlagsProblemNodeAndSelects(t *testing.T) {
	root := &PlanNodeVM{NodeType: "Nested Loop", PlanRows: 10, ActualRows: 10}
	child := &PlanNodeVM{NodeType: "Seq Scan", Relation: "orders", PlanRows: 5, ActualRows: 5000}
	root.Children = []*PlanNodeVM{child}
	vm := PlanVM{Root: root}
	flatten(root, &vm.Flat)
	DecoratePlan(&vm, 1)
	if vm.Flat[0].Idx != 0 || vm.Flat[1].Idx != 1 {
		t.Fatalf("Idx not assigned: %d %d", vm.Flat[0].Idx, vm.Flat[1].Idx)
	}
	if !vm.Flat[1].Problem {
		t.Error("Seq Scan 5→5000 (1000x) should be flagged a problem node")
	}
	if vm.Flat[0].Problem {
		t.Error("Nested Loop 10→10 should not be a problem node")
	}
	if vm.Selected == nil || vm.Selected.NodeType != "Seq Scan" {
		t.Error("selected node should be Flat[1]")
	}
}
