// Integration tests for capability policy storage. Real Postgres via
// testcontainers (the newPool helper lives in store_test.go) — we never
// mock the database, because the NULLS NOT DISTINCT uniqueness semantics
// and the FK to audit_log are part of what we're validating.
package store_test

import (
	"context"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/store"
)

func TestCapabilityPolicyMigration_createsTableAndConstraints(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Table exists with the expected columns.
	for _, col := range []string{
		"server_id", "database_name", "capability", "enabled",
		"set_by", "set_at", "reason", "audit_chain_id",
	} {
		var ok bool
		_ = pool.QueryRow(ctx,
			`SELECT EXISTS(
			   SELECT 1 FROM information_schema.columns
			   WHERE table_name='capability_policy' AND column_name=$1
			 )`, col,
		).Scan(&ok)
		if !ok {
			t.Errorf("capability_policy.%s missing", col)
		}
	}

	// Seed the server first: capability_policy.server_id is a FK to
	// servers(id), so the rows below need the parent to exist.
	if _, err := pool.Exec(ctx,
		`INSERT INTO servers (id, name) VALUES ('srv-1', 'srv one')`,
	); err != nil {
		t.Fatalf("seed server: %v", err)
	}

	// The unique index is NULLS NOT DISTINCT, so two server-wide rows
	// (database_name IS NULL) for the same (server_id, capability) collide.
	if _, err := pool.Exec(ctx,
		`INSERT INTO capability_policy
		   (server_id, database_name, capability, enabled, set_by, audit_chain_id)
		 VALUES ('srv-1', NULL, 'pg_stat_statements', true, 'alice', NULL)`,
	); err != nil {
		t.Fatalf("first server-wide insert: %v", err)
	}
	_, err := pool.Exec(ctx,
		`INSERT INTO capability_policy
		   (server_id, database_name, capability, enabled, set_by, audit_chain_id)
		 VALUES ('srv-1', NULL, 'pg_stat_statements', false, 'bob', NULL)`,
	)
	if err == nil {
		t.Fatal("duplicate server-wide row should violate UNIQUE NULLS NOT DISTINCT")
	}

	// idempotent re-apply.
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("re-apply: %v", err)
	}
}
