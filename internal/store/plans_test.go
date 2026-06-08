package store_test

import (
	"context"
	"testing"
	"time"

	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
	"github.com/dobbo-ca/lynceus/internal/store"
)

func TestApplyStatsMigrations_createsPartitionedQueryPlans(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyStatsMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var strategy string
	err := pool.QueryRow(ctx,
		`SELECT partstrat::text FROM pg_partitioned_table
		 WHERE partrelid = 'query_plans'::regclass`,
	).Scan(&strategy)
	if err != nil {
		t.Fatalf("query_plans not partitioned: %v", err)
	}
	if strategy != "r" {
		t.Fatalf("partition strategy = %q, want 'r' (range)", strategy)
	}
}

func TestWriteQueryPlans_createsPartitionAndRoundtrips(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyStatsMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := store.NewStats(pool)

	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC) // a Wednesday
	plan := &lynceusv1.QueryPlan{
		Fingerprint:       "fp-1",
		CapturedAtUnix:    now.Unix(),
		FormatVersion:     1,
		TotalCost:         102.84,
		ActualTotalTimeMs: 0,
		Root: &lynceusv1.PlanNode{
			NodeType:  "Aggregate",
			TotalCost: 102.84,
			PlanRows:  1,
			Plans: []*lynceusv1.PlanNode{{
				NodeType:            "Seq Scan",
				RelationName:        "orders",
				TotalCost:           96.50,
				PlanRows:            2532,
				NormalizedCondition: "(total > $1)",
			}},
		},
	}
	rows := []store.QueryPlanRow{
		{ServerID: "srv-1", Plan: plan, CapturedAt: now},
		{ServerID: "srv-1", Plan: plan, CapturedAt: now.Add(time.Minute)},
	}
	if err := s.WriteQueryPlans(ctx, rows); err != nil {
		t.Fatalf("write: %v", err)
	}

	var partCount int
	_ = pool.QueryRow(ctx,
		`SELECT count(*) FROM pg_inherits WHERE inhparent = 'query_plans'::regclass`,
	).Scan(&partCount)
	if partCount == 0 {
		t.Fatal("write did not create a weekly partition")
	}

	out, err := s.TopPlansByQuery(ctx, "srv-1", "fp-1",
		now.Add(-time.Hour), now.Add(time.Hour), 10)
	if err != nil {
		t.Fatalf("top: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("got %d plans, want 2", len(out))
	}
	got := out[0]
	if got.Plan.GetFingerprint() != "fp-1" {
		t.Errorf("fingerprint = %q, want fp-1", got.Plan.GetFingerprint())
	}
	if got.Plan.GetTotalCost() != 102.84 {
		t.Errorf("total_cost = %v, want 102.84", got.Plan.GetTotalCost())
	}
	root := got.Plan.GetRoot()
	if root.GetNodeType() != "Aggregate" || len(root.GetPlans()) != 1 {
		t.Fatalf("root tree not round-tripped: %+v", root)
	}
	child := root.GetPlans()[0]
	if child.GetRelationName() != "orders" || child.GetNormalizedCondition() != "(total > $1)" {
		t.Errorf("child not round-tripped: rel=%q cond=%q", child.GetRelationName(), child.GetNormalizedCondition())
	}
}

func TestWriteQueryPlans_emptyNoop(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyStatsMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := store.NewStats(pool).WriteQueryPlans(ctx, nil); err != nil {
		t.Fatalf("empty write should be a no-op, got %v", err)
	}
}

func TestListPlanKeys_returnsDistinctKeys(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyStatsMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := store.NewStats(pool)

	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC) // a Wednesday
	planFor := func(fp string) *lynceusv1.QueryPlan {
		return &lynceusv1.QueryPlan{
			Fingerprint:    fp,
			CapturedAtUnix: now.Unix(),
			FormatVersion:  1,
			Root:           &lynceusv1.PlanNode{NodeType: "Seq Scan", RelationName: "orders"},
		}
	}
	// Two plans on key (srv-1, fp-a) + one plan on key (srv-1, fp-b).
	rows := []store.QueryPlanRow{
		{ServerID: "srv-1", Plan: planFor("fp-a"), CapturedAt: now},
		{ServerID: "srv-1", Plan: planFor("fp-a"), CapturedAt: now.Add(time.Minute)},
		{ServerID: "srv-1", Plan: planFor("fp-b"), CapturedAt: now},
	}
	if err := s.WriteQueryPlans(ctx, rows); err != nil {
		t.Fatalf("write: %v", err)
	}

	keys, err := s.ListPlanKeys(ctx, now.Add(-time.Hour), now.Add(time.Hour), 100)
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
	if keys[1].Fingerprint != "fp-b" {
		t.Errorf("keys[1].Fingerprint = %q, want fp-b", keys[1].Fingerprint)
	}
}
