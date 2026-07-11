package console

import (
	"context"
	"testing"
	"time"
)

func TestStubExecutor_deterministicRowsTruncatedToLimit(t *testing.T) {
	full, err := StubExecutor{}.Execute(context.Background(), Statement{RowLimit: 0})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := []string{"relname", "n_dead_tup", "last_autovacuum"}; len(full.Columns) != 3 ||
		full.Columns[0] != got[0] || full.Columns[1] != got[1] || full.Columns[2] != got[2] {
		t.Fatalf("columns = %v, want %v", full.Columns, got)
	}
	if full.RowCount() != 54 {
		t.Fatalf("full row count = %d, want 54 (deterministic dataset)", full.RowCount())
	}
	if full.DurationMs != 18.4 {
		t.Errorf("duration = %v, want 18.4", full.DurationMs)
	}
	limited, _ := StubExecutor{}.Execute(context.Background(), Statement{RowLimit: 10})
	if limited.RowCount() != 10 {
		t.Errorf("limited row count = %d, want 10", limited.RowCount())
	}
	again, _ := StubExecutor{}.Execute(context.Background(), Statement{RowLimit: 10})
	if again.Rows[0][0] != limited.Rows[0][0] {
		t.Error("stub executor is not deterministic across calls")
	}
}

func TestStubGrantReader_grantsOnlyOrdersProd(t *testing.T) {
	fixed := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	g := StubGrantReader{Now: func() time.Time { return fixed }}
	grant, ok, err := g.ActiveGrant(context.Background(), "c1", "orders-prod", "dev-admin")
	if err != nil || !ok {
		t.Fatalf("orders-prod: ok=%v err=%v, want granted", ok, err)
	}
	if grant.Group != "dba-oncall" || grant.Incident != "INC-2214" || !grant.ReadOnly {
		t.Errorf("grant = %+v, want dba-oncall/INC-2214/read-only", grant)
	}
	if !grant.ExpiresAt.After(fixed) {
		t.Error("grant must expire in the future")
	}
	if _, ok, _ := g.ActiveGrant(context.Background(), "c2", "staging", "dev-admin"); ok {
		t.Error("staging must NOT be granted by the stub")
	}
}

func TestSessions_appendCapAndGet(t *testing.T) {
	s := NewSessions(2)
	s.Append("dev-admin", "c1", Run{ID: "a", SQL: "SELECT 1"})
	s.Append("dev-admin", "c1", Run{ID: "b", SQL: "SELECT 2"})
	s.Append("dev-admin", "c1", Run{ID: "c", SQL: "SELECT 3"})
	recent := s.Recent("dev-admin", "c1")
	if len(recent) != 2 || recent[0].ID != "c" || recent[1].ID != "b" {
		t.Fatalf("recent = %v, want [c b] (capped, newest first)", recent)
	}
	latest, ok := s.Latest("dev-admin", "c1")
	if !ok || latest.ID != "c" {
		t.Fatalf("latest = %v,%v want c,true", latest.ID, ok)
	}
	if _, ok := s.Get("dev-admin", "c1", "a"); ok {
		t.Error("evicted run 'a' should be gone")
	}
	if got, ok := s.Get("dev-admin", "c1", "b"); !ok || got.SQL != "SELECT 2" {
		t.Errorf("Get(b) = %v,%v", got.SQL, ok)
	}
	if _, ok := s.Latest("nobody", "c1"); ok {
		t.Error("unknown actor should have no latest")
	}
	// Cross-cluster isolation: a run cached under c1 must NOT be visible under c2.
	if _, ok := s.Latest("dev-admin", "c2"); ok {
		t.Error("runs must be scoped per (actor, cluster) — no cross-cluster leak")
	}
	if _, ok := s.Get("dev-admin", "c2", "b"); ok {
		t.Error("Get must not return a run from a different cluster")
	}
}
