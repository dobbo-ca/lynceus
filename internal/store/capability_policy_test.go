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

func TestSetCapabilityPolicy_insertsAndAudits(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// FK requires the server row to exist first.
	if _, err := pool.Exec(ctx,
		`INSERT INTO servers (id, name) VALUES ('srv-1', 'srv one')`,
	); err != nil {
		t.Fatalf("seed server: %v", err)
	}
	cfg := store.NewConfig(pool)

	got, err := cfg.SetCapabilityPolicy(ctx, store.SetCapabilityPolicyInput{
		ServerID:     "srv-1",
		DatabaseName: "", // server-wide default
		Capability:   "pg_stat_statements",
		Enabled:      true,
		SetBy:        "alice",
		Reason:       "extension confirmed installed",
	})
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	if !got.Enabled || got.ServerID != "srv-1" || got.Capability != "pg_stat_statements" {
		t.Fatalf("unexpected returned policy: %+v", got)
	}
	if got.DatabaseName != "" {
		t.Errorf("server-wide row should report empty DatabaseName, got %q", got.DatabaseName)
	}
	if got.AuditChainID == 0 {
		t.Fatal("AuditChainID not populated")
	}
	if got.SetAt.IsZero() {
		t.Error("SetAt not populated")
	}

	// An audit row exists with the assigned id and references the toggle.
	var action, actor, serverID string
	if err := pool.QueryRow(ctx,
		`SELECT action, actor, COALESCE(server_id,'') FROM audit_log WHERE id = $1`,
		got.AuditChainID,
	).Scan(&action, &actor, &serverID); err != nil {
		t.Fatalf("audit row missing: %v", err)
	}
	if action != "capability_policy.set" || actor != "alice" || serverID != "srv-1" {
		t.Errorf("audit row = (%q,%q,%q), want (capability_policy.set, alice, srv-1)",
			action, actor, serverID)
	}
}

func TestGetCapabilityPolicy_exactRowAndUpsertOverwrite(t *testing.T) {
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
	cfg := store.NewConfig(pool)

	// Not found before any write.
	_, found, err := cfg.GetCapabilityPolicy(ctx, "srv-1", "", "pg_stat_statements")
	if err != nil {
		t.Fatalf("get (absent): %v", err)
	}
	if found {
		t.Fatal("expected not found before any write")
	}

	// First write: disabled.
	first, err := cfg.SetCapabilityPolicy(ctx, store.SetCapabilityPolicyInput{
		ServerID: "srv-1", Capability: "pg_stat_statements",
		Enabled: false, SetBy: "alice", Reason: "off by default",
	})
	if err != nil {
		t.Fatalf("set #1: %v", err)
	}

	// Second write to the same key flips it and is a single row (upsert).
	second, err := cfg.SetCapabilityPolicy(ctx, store.SetCapabilityPolicyInput{
		ServerID: "srv-1", Capability: "pg_stat_statements",
		Enabled: true, SetBy: "bob", Reason: "operator enabled",
	})
	if err != nil {
		t.Fatalf("set #2: %v", err)
	}
	if second.AuditChainID == first.AuditChainID {
		t.Error("second toggle should produce a new audit id")
	}

	got, found, err := cfg.GetCapabilityPolicy(ctx, "srv-1", "", "pg_stat_statements")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !found {
		t.Fatal("expected found after write")
	}
	if !got.Enabled || got.SetBy != "bob" || got.Reason != "operator enabled" {
		t.Errorf("got %+v, want enabled/bob/operator enabled", got)
	}
	if got.AuditChainID != second.AuditChainID {
		t.Errorf("got.AuditChainID=%d, want %d", got.AuditChainID, second.AuditChainID)
	}

	// Exactly one row for the key (upsert, not insert-twice).
	var n int
	_ = pool.QueryRow(ctx,
		`SELECT count(*) FROM capability_policy
		   WHERE server_id='srv-1' AND database_name IS NULL AND capability='pg_stat_statements'`,
	).Scan(&n)
	if n != 1 {
		t.Errorf("row count = %d, want 1", n)
	}
}

func TestEffectiveCapability_overrideBeatsDefault(t *testing.T) {
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
	cfg := store.NewConfig(pool)

	// No policy at all: not found.
	_, src, found, err := cfg.EffectiveCapability(ctx, "srv-1", "appdb", "pg_stat_statements")
	if err != nil {
		t.Fatalf("effective (none): %v", err)
	}
	if found {
		t.Fatalf("expected not found, got source=%v", src)
	}

	// Server-wide default: enabled. With no DB override, effective uses it.
	if _, err := cfg.SetCapabilityPolicy(ctx, store.SetCapabilityPolicyInput{
		ServerID: "srv-1", Capability: "pg_stat_statements",
		Enabled: true, SetBy: "alice",
	}); err != nil {
		t.Fatalf("set default: %v", err)
	}
	enabled, src, found, err := cfg.EffectiveCapability(ctx, "srv-1", "appdb", "pg_stat_statements")
	if err != nil || !found {
		t.Fatalf("effective (default): found=%v err=%v", found, err)
	}
	if !enabled || src != store.PolicySourceServerDefault {
		t.Errorf("got enabled=%v source=%v, want true/server-default", enabled, src)
	}

	// DB-specific override: disabled for appdb. Override wins.
	if _, err := cfg.SetCapabilityPolicy(ctx, store.SetCapabilityPolicyInput{
		ServerID: "srv-1", DatabaseName: "appdb", Capability: "pg_stat_statements",
		Enabled: false, SetBy: "bob", Reason: "noisy on appdb",
	}); err != nil {
		t.Fatalf("set override: %v", err)
	}
	enabled, src, found, err = cfg.EffectiveCapability(ctx, "srv-1", "appdb", "pg_stat_statements")
	if err != nil || !found {
		t.Fatalf("effective (override): found=%v err=%v", found, err)
	}
	if enabled || src != store.PolicySourceDatabaseOverride {
		t.Errorf("got enabled=%v source=%v, want false/db-override", enabled, src)
	}

	// A different database with no override still sees the server default.
	enabled, src, found, err = cfg.EffectiveCapability(ctx, "srv-1", "otherdb", "pg_stat_statements")
	if err != nil || !found {
		t.Fatalf("effective (other db): found=%v err=%v", found, err)
	}
	if !enabled || src != store.PolicySourceServerDefault {
		t.Errorf("otherdb got enabled=%v source=%v, want true/server-default", enabled, src)
	}
}

func TestListCapabilityPolicies_returnsAllForServer(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO servers (id, name) VALUES ('srv-1','srv one'), ('srv-2','srv two')`,
	); err != nil {
		t.Fatalf("seed servers: %v", err)
	}
	cfg := store.NewConfig(pool)

	mustSet := func(in store.SetCapabilityPolicyInput) {
		t.Helper()
		if _, err := cfg.SetCapabilityPolicy(ctx, in); err != nil {
			t.Fatalf("set %+v: %v", in, err)
		}
	}
	mustSet(store.SetCapabilityPolicyInput{ServerID: "srv-1", Capability: "pg_stat_statements", Enabled: true, SetBy: "a"})
	mustSet(store.SetCapabilityPolicyInput{ServerID: "srv-1", DatabaseName: "appdb", Capability: "pg_stat_statements", Enabled: false, SetBy: "a"})
	mustSet(store.SetCapabilityPolicyInput{ServerID: "srv-2", Capability: "auto_explain", Enabled: true, SetBy: "a"})

	got, err := cfg.ListCapabilityPolicies(ctx, "srv-1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d rows for srv-1, want 2", len(got))
	}
	for _, p := range got {
		if p.ServerID != "srv-1" {
			t.Errorf("list returned foreign server row: %+v", p)
		}
	}
}
