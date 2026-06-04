package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/dobbo-ca/lynceus/internal/store"
)

func TestConfig_ReadsRouteToReplica(t *testing.T) {
	primary := newPool(t)
	replica := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, primary); err != nil {
		t.Fatalf("migrate primary: %v", err)
	}
	if err := store.ApplyConfigMigrations(ctx, replica); err != nil {
		t.Fatalf("migrate replica: %v", err)
	}

	cfg := store.NewConfig(primary).WithReadPool(replica)

	// Write lands on the primary.
	if _, err := cfg.AppendAuditReturning(ctx, store.AuditEntry{Actor: "a", Action: "x"}); err != nil {
		t.Fatalf("append: %v", err)
	}

	// Read is served from the (empty) replica → sees nothing.
	got, err := cfg.ListAudit(ctx, store.AuditFilter{Limit: 100})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("read served from primary; want empty replica view, got %d rows", len(got))
	}

	// Sanity: the row really is on the primary.
	var n int
	if err := primary.QueryRow(ctx, `SELECT count(*) FROM audit_log`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("primary count = %d, want 1", n)
	}
}

func TestConfig_NoReplica_ReadsFromPrimary(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg := store.NewConfig(pool) // no WithReadPool → reads fall back to primary

	if _, err := cfg.AppendAuditReturning(ctx, store.AuditEntry{Actor: "a", Action: "x"}); err != nil {
		t.Fatalf("append: %v", err)
	}
	got, err := cfg.ListAudit(ctx, store.AuditFilter{Limit: 100})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("fallback read got %d rows, want 1", len(got))
	}
}

func TestStats_ReadsRouteToReplica(t *testing.T) {
	primary := newPool(t)
	replica := newPool(t)
	ctx := context.Background()
	if err := store.ApplyStatsMigrations(ctx, primary); err != nil {
		t.Fatalf("migrate primary: %v", err)
	}
	if err := store.ApplyStatsMigrations(ctx, replica); err != nil {
		t.Fatalf("migrate replica: %v", err)
	}

	s := store.NewStats(primary).WithReadPool(replica)
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	if err := s.WriteQueryStats(ctx, []store.QueryStat{
		{ServerID: "srv", CollectedAt: now, Fingerprint: "fp", NormalizedQuery: "SELECT 1", Calls: 1, TotalTimeMs: 1},
	}); err != nil {
		t.Fatalf("write: %v", err)
	}

	top, err := s.TopQueriesByTotalTime(ctx, now.Add(-time.Hour), now.Add(time.Hour), 10)
	if err != nil {
		t.Fatalf("top: %v", err)
	}
	if len(top) != 0 {
		t.Fatalf("read served from primary; want empty replica view, got %d rows", len(top))
	}
}

func TestStats_NoReplica_ReadsFromPrimary(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyStatsMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := store.NewStats(pool) // no WithReadPool
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	if err := s.WriteQueryStats(ctx, []store.QueryStat{
		{ServerID: "srv", CollectedAt: now, Fingerprint: "fp", NormalizedQuery: "SELECT 1", Calls: 1, TotalTimeMs: 1},
	}); err != nil {
		t.Fatalf("write: %v", err)
	}
	top, err := s.TopQueriesByTotalTime(ctx, now.Add(-time.Hour), now.Add(time.Hour), 10)
	if err != nil {
		t.Fatalf("top: %v", err)
	}
	if len(top) != 1 {
		t.Fatalf("fallback read got %d rows, want 1", len(top))
	}
}
