package store_test

import (
	"context"
	"testing"
	"time"

	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/dobbo-ca/lynceus/internal/testch"
)

// plansMakePlan builds a small normalized (literal-free) QueryPlan whose root
// tree exercises nested plans, a relation name, and a normalized condition, so
// the round-trip asserts the whole plan_tree survives protojson serialization.
func plansMakePlan(fp string, cost float64) *lynceusv1.QueryPlan {
	return &lynceusv1.QueryPlan{
		Fingerprint:       fp,
		FormatVersion:     1,
		TotalCost:         cost,
		ActualTotalTimeMs: 0,
		Root: &lynceusv1.PlanNode{
			NodeType:  "Aggregate",
			TotalCost: cost,
			PlanRows:  1,
			Plans: []*lynceusv1.PlanNode{{
				NodeType:            "Seq Scan",
				RelationName:        "orders",
				TotalCost:           cost - 6,
				PlanRows:            2532,
				NormalizedCondition: "(total > $1)",
			}},
		},
	}
}

func TestCH_plans_WriteAndTopPlans_RoundTrip(t *testing.T) {
	ctx := context.Background()
	conn := testch.Start(t)
	if err := store.ApplyClickHouseMigrations(ctx, conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := store.NewCHStats(conn)

	// An empty write is a no-op.
	if err := s.WriteQueryPlans(ctx, nil); err != nil {
		t.Fatalf("empty write should be a no-op: %v", err)
	}

	base := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	rows := []store.QueryPlanRow{
		{ServerID: "srv-1", Plan: plansMakePlan("fp-1", 102.84), CapturedAt: base},
		{ServerID: "srv-1", Plan: plansMakePlan("fp-1", 250.0), CapturedAt: base.Add(time.Minute)},
		// A T2 plan on the same key must never surface in the T1 read.
		{ServerID: "srv-1", Plan: plansMakePlan("fp-1", 999.0), CapturedAt: base, DataTier: 2},
		// A different fingerprint must not leak into the fp-1 read.
		{ServerID: "srv-1", Plan: plansMakePlan("fp-other", 5.0), CapturedAt: base},
	}
	if err := s.WriteQueryPlans(ctx, rows); err != nil {
		t.Fatalf("write: %v", err)
	}

	out, err := s.TopPlansByQuery(ctx, "srv-1", "fp-1",
		base.Add(-time.Hour), base.Add(time.Hour), 10)
	if err != nil {
		t.Fatalf("top: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("got %d plans, want 2 (T1 fp-1 only)", len(out))
	}

	// ORDER BY captured_at DESC => the base+1min plan (cost 250) first.
	got := out[0]
	if got.CapturedAt.Unix() != base.Add(time.Minute).Unix() {
		t.Errorf("out[0] captured_at = %v, want %v (most recent first)", got.CapturedAt, base.Add(time.Minute))
	}
	if got.Plan.GetCapturedAtUnix() != base.Add(time.Minute).Unix() {
		t.Errorf("captured_at_unix = %d, want %d", got.Plan.GetCapturedAtUnix(), base.Add(time.Minute).Unix())
	}
	if got.DataTier != 1 {
		t.Errorf("data_tier = %d, want 1", got.DataTier)
	}
	if got.Plan.GetFingerprint() != "fp-1" {
		t.Errorf("fingerprint = %q, want fp-1", got.Plan.GetFingerprint())
	}
	if got.Plan.GetTotalCost() != 250.0 {
		t.Errorf("total_cost = %v, want 250", got.Plan.GetTotalCost())
	}
	if out[1].Plan.GetTotalCost() != 102.84 {
		t.Errorf("out[1] total_cost = %v, want 102.84", out[1].Plan.GetTotalCost())
	}

	// The nested plan_tree survives serialization.
	root := got.Plan.GetRoot()
	if root.GetNodeType() != "Aggregate" || len(root.GetPlans()) != 1 {
		t.Fatalf("root tree not round-tripped: %+v", root)
	}
	child := root.GetPlans()[0]
	if child.GetRelationName() != "orders" || child.GetNormalizedCondition() != "(total > $1)" {
		t.Errorf("child not round-tripped: rel=%q cond=%q",
			child.GetRelationName(), child.GetNormalizedCondition())
	}

	// The window filter excludes plans outside [since, until).
	empty, err := s.TopPlansByQuery(ctx, "srv-1", "fp-1",
		base.Add(time.Hour), base.Add(2*time.Hour), 10)
	if err != nil {
		t.Fatalf("top (empty window): %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("out-of-window read returned %d plans, want 0", len(empty))
	}
}

func TestCH_plans_ListPlanKeys_Distinct(t *testing.T) {
	ctx := context.Background()
	conn := testch.Start(t)
	if err := store.ApplyClickHouseMigrations(ctx, conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := store.NewCHStats(conn)

	base := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	rows := []store.QueryPlanRow{
		// Two plans on key (srv-1, fp-a) collapse to one distinct key.
		{ServerID: "srv-1", Plan: plansMakePlan("fp-a", 10), CapturedAt: base},
		{ServerID: "srv-1", Plan: plansMakePlan("fp-a", 11), CapturedAt: base.Add(time.Minute)},
		{ServerID: "srv-1", Plan: plansMakePlan("fp-b", 12), CapturedAt: base},
		// A T2 plan must not contribute a key.
		{ServerID: "srv-2", Plan: plansMakePlan("fp-z", 13), CapturedAt: base, DataTier: 2},
	}
	if err := s.WriteQueryPlans(ctx, rows); err != nil {
		t.Fatalf("write: %v", err)
	}

	keys, err := s.ListPlanKeys(ctx, base.Add(-time.Hour), base.Add(time.Hour), 100)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("got %d distinct keys, want 2: %+v", len(keys), keys)
	}
	// ORDER BY server_id, fingerprint => fp-a then fp-b.
	if keys[0].ServerID != "srv-1" || keys[0].Fingerprint != "fp-a" {
		t.Errorf("keys[0] = %+v, want {srv-1 fp-a}", keys[0])
	}
	if keys[1].ServerID != "srv-1" || keys[1].Fingerprint != "fp-b" {
		t.Errorf("keys[1] = %+v, want {srv-1 fp-b}", keys[1])
	}
}
