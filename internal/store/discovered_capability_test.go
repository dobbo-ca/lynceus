// Integration tests for discovered-capability storage. Real Postgres via
// testcontainers (the newPool helper lives in store_test.go) — we never
// mock the database, because the NULLS NOT DISTINCT uniqueness and the
// FK to servers are part of what we're validating.
package store_test

import (
	"context"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/caps"
	"github.com/dobbo-ca/lynceus/internal/store"
)

func TestDiscoveredCapabilityMigration_createsTable(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for _, col := range []string{
		"server_id", "database_name", "capability", "available", "reason", "observed_at",
	} {
		var ok bool
		_ = pool.QueryRow(ctx,
			`SELECT EXISTS(
			   SELECT 1 FROM information_schema.columns
			   WHERE table_name='discovered_capability' AND column_name=$1
			 )`, col,
		).Scan(&ok)
		if !ok {
			t.Errorf("discovered_capability.%s missing", col)
		}
	}
	// idempotent re-apply.
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("re-apply: %v", err)
	}
}

func TestUpsertDiscoveredCapabilities_roundtripsAndIsIdempotent(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO servers (id, name) VALUES ('srv-1', 'srv one')`,
	); err != nil {
		t.Fatalf("seed server: %v", err)
	}
	dc := store.NewDiscoveredCapabilities(pool)

	set := caps.Set{
		caps.PgStatStatements: {Available: true, Reason: "1.10"},
		caps.AutoExplain:      {Available: false, Reason: "extension not installed"},
	}
	if err := dc.UpsertDiscoveredCapabilities(ctx, "srv-1", "appdb", set); err != nil {
		t.Fatalf("upsert #1: %v", err)
	}

	got, err := dc.ListDiscoveredCapabilities(ctx, "srv-1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2", len(got))
	}
	byCap := map[string]store.DiscoveredCapability{}
	for _, d := range got {
		byCap[d.Capability] = d
	}
	if d := byCap["pg_stat_statements"]; !d.Available || d.Reason != "1.10" || d.DatabaseName != "appdb" {
		t.Errorf("pg_stat_statements row = %+v, want available/1.10/appdb", d)
	}
	if d := byCap["auto_explain"]; d.Available || d.Reason != "extension not installed" {
		t.Errorf("auto_explain row = %+v, want unavailable/not-installed", d)
	}

	// Re-upsert the same key with a flipped verdict: idempotent, still 2 rows.
	set2 := caps.Set{
		caps.PgStatStatements: {Available: false, Reason: "revoked"},
		caps.AutoExplain:      {Available: false, Reason: "extension not installed"},
	}
	if err := dc.UpsertDiscoveredCapabilities(ctx, "srv-1", "appdb", set2); err != nil {
		t.Fatalf("upsert #2: %v", err)
	}
	got2, err := dc.ListDiscoveredCapabilities(ctx, "srv-1")
	if err != nil {
		t.Fatalf("list #2: %v", err)
	}
	if len(got2) != 2 {
		t.Fatalf("after re-upsert got %d rows, want 2 (upsert, not insert-twice)", len(got2))
	}
	for _, d := range got2 {
		if d.Capability == "pg_stat_statements" && (d.Available || d.Reason != "revoked") {
			t.Errorf("pg_stat_statements not updated: %+v", d)
		}
	}
}
