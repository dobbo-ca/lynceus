// Integration tests for capability policy storage. Real Postgres via
// testcontainers (the newPool helper lives in store_test.go) — we never
// mock the database, because the NULLS NOT DISTINCT uniqueness semantics
// and the FK to audit_log are part of what we're validating.
package store_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

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

func TestSetCapabilityPolicy_keepsAuditChainIntact(t *testing.T) {
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

	for i := 0; i < 5; i++ {
		if _, err := cfg.SetCapabilityPolicy(ctx, store.SetCapabilityPolicyInput{
			ServerID: "srv-1", Capability: "auto_explain",
			Enabled: i%2 == 0, SetBy: "alice", Reason: "toggle",
		}); err != nil {
			t.Fatalf("set %d: %v", i, err)
		}
	}

	// Every toggle wrote an audit row and the chain still verifies.
	bad, reason, err := cfg.VerifyChain(ctx, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if bad != -1 {
		t.Fatalf("audit chain broken after toggles: bad=%d reason=%q", bad, reason)
	}

	var auditCount int
	_ = pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE action = 'capability_policy.set'`,
	).Scan(&auditCount)
	if auditCount != 5 {
		t.Errorf("audit rows = %d, want 5", auditCount)
	}
}

// auditCount returns the number of audit_log rows.
func auditCount(t *testing.T, pool *pgxpool.Pool, ctx context.Context) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_log`).Scan(&n); err != nil {
		t.Fatalf("count audit: %v", err)
	}
	return n
}

// policyAuditID returns audit_chain_id for one capability_policy row. dbName ""
// selects the server-wide (NULL database_name) row.
func policyAuditID(t *testing.T, pool *pgxpool.Pool, ctx context.Context, serverID, dbName, capability string) (int64, bool) {
	t.Helper()
	var id *int64
	var err error
	if dbName == "" {
		err = pool.QueryRow(ctx,
			`SELECT audit_chain_id FROM capability_policy
			  WHERE server_id=$1 AND database_name IS NULL AND capability=$2`,
			serverID, capability).Scan(&id)
	} else {
		err = pool.QueryRow(ctx,
			`SELECT audit_chain_id FROM capability_policy
			  WHERE server_id=$1 AND database_name=$2 AND capability=$3`,
			serverID, dbName, capability).Scan(&id)
	}
	if err == pgx.ErrNoRows {
		return 0, false
	}
	if err != nil {
		t.Fatalf("read policy audit id: %v", err)
	}
	if id == nil {
		return 0, true
	}
	return *id, true
}

func TestApplyCapabilityPoliciesBatch_invariant2_singleAuditLinksEveryRow(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO servers (id, name) VALUES ('srv-1','srv one'),('srv-2','srv two')`,
	); err != nil {
		t.Fatalf("seed servers: %v", err)
	}
	cfg := store.NewConfig(pool)

	// Pre-seed one row so we have something to delete in the same batch.
	if _, err := cfg.SetCapabilityPolicy(ctx, store.SetCapabilityPolicyInput{
		ServerID: "srv-1", DatabaseName: "gone", Capability: "auto_explain",
		Enabled: true, SetBy: "seed",
	}); err != nil {
		t.Fatalf("seed policy: %v", err)
	}

	before := auditCount(t, pool, ctx)

	err := cfg.ApplyCapabilityPoliciesBatch(ctx,
		[]store.SetCapabilityPolicyInput{
			{ServerID: "srv-1", Capability: "pg_stat_statements", Enabled: true, SetBy: "op"},
			{ServerID: "srv-1", DatabaseName: "appdb", Capability: "pg_stat_statements", Enabled: false, SetBy: "op"},
			{ServerID: "srv-2", Capability: "auto_explain", Enabled: true, SetBy: "op"},
		},
		[]store.CapabilityPolicyKey{
			{ServerID: "srv-1", DatabaseName: "gone", Capability: "auto_explain"},
		},
		"op",
	)
	if err != nil {
		t.Fatalf("batch: %v", err)
	}

	// Exactly one audit row was added, and it is the bulk_set row.
	if got := auditCount(t, pool, ctx) - before; got != 1 {
		t.Fatalf("audit delta = %d, want 1", got)
	}
	var bulkID int64
	var detail string
	if err := pool.QueryRow(ctx,
		`SELECT id, COALESCE(detail::text,'') FROM audit_log
		  WHERE action='capability_policy.bulk_set' ORDER BY id DESC LIMIT 1`,
	).Scan(&bulkID, &detail); err != nil {
		t.Fatalf("bulk_set audit row missing: %v", err)
	}
	if !strings.Contains(detail, "row_count") || !strings.Contains(detail, "content_hash") {
		t.Errorf("bulk_set detail = %q, want row_count + content_hash", detail)
	}

	// Every upserted row references that single audit id.
	for _, k := range []store.CapabilityPolicyKey{
		{ServerID: "srv-1", Capability: "pg_stat_statements"},
		{ServerID: "srv-1", DatabaseName: "appdb", Capability: "pg_stat_statements"},
		{ServerID: "srv-2", Capability: "auto_explain"},
	} {
		id, ok := policyAuditID(t, pool, ctx, k.ServerID, k.DatabaseName, k.Capability)
		if !ok {
			t.Fatalf("upserted row %+v missing", k)
		}
		if id != bulkID {
			t.Errorf("row %+v audit_chain_id=%d, want %d", k, id, bulkID)
		}
	}

	// The deleted row is gone.
	if _, ok := policyAuditID(t, pool, ctx, "srv-1", "gone", "auto_explain"); ok {
		t.Error("deleted row still present")
	}
}

func TestApplyCapabilityPoliciesBatch_idempotentReapply_newAuditEachTime(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO servers (id, name) VALUES ('srv-1','srv one')`,
	); err != nil {
		t.Fatalf("seed server: %v", err)
	}
	cfg := store.NewConfig(pool)

	batch := []store.SetCapabilityPolicyInput{
		{ServerID: "srv-1", Capability: "pg_stat_statements", Enabled: true, SetBy: "op"},
		{ServerID: "srv-1", DatabaseName: "appdb", Capability: "pg_stat_statements", Enabled: false, SetBy: "op"},
	}

	if err := cfg.ApplyCapabilityPoliciesBatch(ctx, batch, nil, "op"); err != nil {
		t.Fatalf("apply #1: %v", err)
	}
	if err := cfg.ApplyCapabilityPoliciesBatch(ctx, batch, nil, "op"); err != nil {
		t.Fatalf("apply #2: %v", err)
	}

	var rowN int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM capability_policy WHERE server_id='srv-1'`).Scan(&rowN)
	if rowN != 2 {
		t.Errorf("row count = %d, want 2 (idempotent)", rowN)
	}
	var bulkN int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action='capability_policy.bulk_set'`).Scan(&bulkN)
	if bulkN != 2 {
		t.Errorf("bulk_set audit rows = %d, want 2 (one per apply)", bulkN)
	}
}

func TestApplyCapabilityPoliciesBatch_deletesSubsetOnly(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO servers (id, name) VALUES ('srv-1','srv one')`,
	); err != nil {
		t.Fatalf("seed server: %v", err)
	}
	cfg := store.NewConfig(pool)

	// Seed: server-wide + two per-db rows.
	if err := cfg.ApplyCapabilityPoliciesBatch(ctx, []store.SetCapabilityPolicyInput{
		{ServerID: "srv-1", Capability: "auto_explain", Enabled: true, SetBy: "op"},
		{ServerID: "srv-1", DatabaseName: "db1", Capability: "auto_explain", Enabled: true, SetBy: "op"},
		{ServerID: "srv-1", DatabaseName: "db2", Capability: "auto_explain", Enabled: true, SetBy: "op"},
	}, nil, "op"); err != nil {
		t.Fatalf("seed batch: %v", err)
	}

	// Delete the server-wide row and db1, plus a non-existent key (no-op).
	if err := cfg.ApplyCapabilityPoliciesBatch(ctx, nil, []store.CapabilityPolicyKey{
		{ServerID: "srv-1", Capability: "auto_explain"}, // server-wide (NULL)
		{ServerID: "srv-1", DatabaseName: "db1", Capability: "auto_explain"},
		{ServerID: "srv-1", DatabaseName: "nope", Capability: "auto_explain"}, // no-op
	}, "op"); err != nil {
		t.Fatalf("delete batch: %v", err)
	}

	if _, ok := policyAuditID(t, pool, ctx, "srv-1", "", "auto_explain"); ok {
		t.Error("server-wide row should be deleted")
	}
	if _, ok := policyAuditID(t, pool, ctx, "srv-1", "db1", "auto_explain"); ok {
		t.Error("db1 row should be deleted")
	}
	if _, ok := policyAuditID(t, pool, ctx, "srv-1", "db2", "auto_explain"); !ok {
		t.Error("db2 row should remain")
	}
}

func TestListCapabilityPoliciesForServers(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO servers (id, name) VALUES ('srv-1','one'),('srv-2','two'),('srv-3','three')`,
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
	mustSet(store.SetCapabilityPolicyInput{ServerID: "srv-3", Capability: "auto_explain", Enabled: true, SetBy: "a"}) // not requested

	got, err := cfg.ListCapabilityPoliciesForServers(ctx, []string{"srv-1", "srv-2"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d rows, want 3", len(got))
	}
	for _, p := range got {
		if p.ServerID == "srv-3" {
			t.Errorf("returned non-requested server row: %+v", p)
		}
	}

	// Empty and nil return empty slice, no error, no panic.
	for _, ids := range [][]string{nil, {}} {
		out, err := cfg.ListCapabilityPoliciesForServers(ctx, ids)
		if err != nil {
			t.Fatalf("list(empty): %v", err)
		}
		if len(out) != 0 {
			t.Errorf("list(%v) = %d rows, want 0", ids, len(out))
		}
	}
}

func TestDeleteCapabilityPolicy_auditedAndChainIntact(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO servers (id, name) VALUES ('srv-1','one')`,
	); err != nil {
		t.Fatalf("seed server: %v", err)
	}
	cfg := store.NewConfig(pool)

	if _, err := cfg.SetCapabilityPolicy(ctx, store.SetCapabilityPolicyInput{
		ServerID: "srv-1", Capability: "auto_explain", Enabled: true, SetBy: "op",
	}); err != nil {
		t.Fatalf("set: %v", err)
	}

	before := auditCount(t, pool, ctx)
	if err := cfg.DeleteCapabilityPolicy(ctx,
		store.CapabilityPolicyKey{ServerID: "srv-1", Capability: "auto_explain"},
		"op", "no longer needed",
	); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if _, ok := policyAuditID(t, pool, ctx, "srv-1", "", "auto_explain"); ok {
		t.Error("row should be deleted")
	}
	if got := auditCount(t, pool, ctx) - before; got != 1 {
		t.Errorf("audit delta = %d, want 1", got)
	}
	var n int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action='capability_policy.delete'`).Scan(&n)
	if n != 1 {
		t.Errorf("delete audit rows = %d, want 1", n)
	}

	bad, reason, err := cfg.VerifyChain(ctx, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if bad != -1 {
		t.Fatalf("chain broken: bad=%d reason=%q", bad, reason)
	}
}

func TestBatchStore_chainIntactInterleaved(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO servers (id, name) VALUES ('srv-1','one'),('srv-2','two')`,
	); err != nil {
		t.Fatalf("seed servers: %v", err)
	}
	cfg := store.NewConfig(pool)

	if _, err := cfg.SetCapabilityPolicy(ctx, store.SetCapabilityPolicyInput{
		ServerID: "srv-1", Capability: "pg_stat_statements", Enabled: true, SetBy: "a",
	}); err != nil {
		t.Fatalf("single set: %v", err)
	}
	if err := cfg.ApplyCapabilityPoliciesBatch(ctx, []store.SetCapabilityPolicyInput{
		{ServerID: "srv-2", Capability: "auto_explain", Enabled: true, SetBy: "a"},
		{ServerID: "srv-1", DatabaseName: "appdb", Capability: "auto_explain", Enabled: false, SetBy: "a"},
	}, nil, "a"); err != nil {
		t.Fatalf("batch: %v", err)
	}
	if err := cfg.DeleteCapabilityPolicy(ctx,
		store.CapabilityPolicyKey{ServerID: "srv-1", Capability: "pg_stat_statements"},
		"a", "cleanup",
	); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := cfg.ApplyCapabilityPoliciesBatch(ctx, nil, []store.CapabilityPolicyKey{
		{ServerID: "srv-2", Capability: "auto_explain"},
	}, "a"); err != nil {
		t.Fatalf("batch delete: %v", err)
	}

	bad, reason, err := cfg.VerifyChain(ctx, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if bad != -1 {
		t.Fatalf("chain broken: bad=%d reason=%q", bad, reason)
	}
}

func TestBatchStore_validationRejectsWithoutWriting(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO servers (id, name) VALUES ('srv-1','one')`,
	); err != nil {
		t.Fatalf("seed server: %v", err)
	}
	cfg := store.NewConfig(pool)

	before := auditCount(t, pool, ctx)

	cases := []struct {
		name    string
		upserts []store.SetCapabilityPolicyInput
		deletes []store.CapabilityPolicyKey
		actor   string
	}{
		{"empty actor", []store.SetCapabilityPolicyInput{{ServerID: "srv-1", Capability: "c", Enabled: true, SetBy: "x"}}, nil, ""},
		{"upsert empty server", []store.SetCapabilityPolicyInput{{ServerID: "", Capability: "c", Enabled: true, SetBy: "x"}}, nil, "op"},
		{"upsert empty capability", []store.SetCapabilityPolicyInput{{ServerID: "srv-1", Capability: "", Enabled: true, SetBy: "x"}}, nil, "op"},
		{"delete empty server", nil, []store.CapabilityPolicyKey{{ServerID: "", Capability: "c"}}, "op"},
		{"delete empty capability", nil, []store.CapabilityPolicyKey{{ServerID: "srv-1", Capability: ""}}, "op"},
	}
	for _, tc := range cases {
		if err := cfg.ApplyCapabilityPoliciesBatch(ctx, tc.upserts, tc.deletes, tc.actor); err == nil {
			t.Errorf("%s: expected error, got nil", tc.name)
		}
	}

	// DeleteCapabilityPolicy validation too.
	if err := cfg.DeleteCapabilityPolicy(ctx, store.CapabilityPolicyKey{ServerID: "", Capability: "c"}, "op", ""); err == nil {
		t.Error("delete empty server: expected error")
	}
	if err := cfg.DeleteCapabilityPolicy(ctx, store.CapabilityPolicyKey{ServerID: "srv-1", Capability: "c"}, "", ""); err == nil {
		t.Error("delete empty actor: expected error")
	}

	if got := auditCount(t, pool, ctx); got != before {
		t.Errorf("audit count changed on error paths: before=%d after=%d", before, got)
	}
	var polN int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM capability_policy`).Scan(&polN)
	if polN != 0 {
		t.Errorf("capability_policy rows written on error path: %d", polN)
	}
}
