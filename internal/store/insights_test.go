package store_test

import (
	"context"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/store"
)

// TestApplyStatsMigrations_createsPartitionedInsights verifies 0005_insights.sql
// creates a range-partitioned insights table (mirrors the query_plans check).
func TestApplyStatsMigrations_createsPartitionedInsights(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyStatsMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var strategy string
	if err := pool.QueryRow(ctx,
		`SELECT partstrat::text FROM pg_partitioned_table
		   WHERE partrelid = 'insights'::regclass`,
	).Scan(&strategy); err != nil {
		t.Fatalf("insights not partitioned: %v", err)
	}
	if strategy != "r" {
		t.Fatalf("partition strategy = %q, want 'r' (range)", strategy)
	}
}
