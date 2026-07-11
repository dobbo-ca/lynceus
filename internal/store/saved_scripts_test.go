package store_test

import (
	"context"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/store"
)

func TestSavedScripts_CreateListGet_visibilityByScope(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg := store.NewConfig(pool)

	mk := func(name, scope, owner, group string) store.SavedScript {
		s, err := cfg.CreateScript(ctx, store.CreateScriptInput{
			Name: name, Description: name + " desc", SQLText: "SELECT 1",
			Scope: scope, Owner: owner, OwnerGroup: group,
		})
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		return s
	}
	mk("g", "GLOBAL", "m.chen", "")
	mk("t", "TEAM", "j.alvarez", "dba-oncall")
	mk("p-mine", "PERSONAL", "s.dobson", "")
	mk("p-theirs", "PERSONAL", "m.chen", "")

	// s.dobson in group dba-oncall sees: GLOBAL + TEAM(dba-oncall) + own PERSONAL.
	got, err := cfg.ListVisibleScripts(ctx, "s.dobson", "dba-oncall")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	names := map[string]bool{}
	for _, s := range got {
		names[s.Name] = true
	}
	if !names["g"] || !names["t"] || !names["p-mine"] {
		t.Errorf("visible set missing an expected script: %v", names)
	}
	if names["p-theirs"] {
		t.Error("leaked another user's PERSONAL script")
	}

	// GetScript round-trips fields.
	first := got[0]
	one, ok, err := cfg.GetScript(ctx, first.ID)
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if one.SQLText != "SELECT 1" || one.Scope == "" {
		t.Errorf("get returned %+v", one)
	}

	// Missing id => (_, false, nil).
	if _, ok, err := cfg.GetScript(ctx, 999999); err != nil || ok {
		t.Errorf("missing get: ok=%v err=%v", ok, err)
	}
}

func TestSavedScripts_Create_rejectsBadScope(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg := store.NewConfig(pool)
	if _, err := cfg.CreateScript(ctx, store.CreateScriptInput{
		Name: "x", SQLText: "SELECT 1", Scope: "WORLD", Owner: "a",
	}); err == nil {
		t.Error("expected error for invalid scope, got nil")
	}
}

func TestSavedScripts_GetVisibleScript_gatesPersonalByViewer(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg := store.NewConfig(pool)
	personal, err := cfg.CreateScript(ctx, store.CreateScriptInput{
		Name: "mine", SQLText: "SELECT secret FROM x", Scope: "PERSONAL", Owner: "s.dobson",
	})
	if err != nil {
		t.Fatalf("create personal: %v", err)
	}
	global, err := cfg.CreateScript(ctx, store.CreateScriptInput{
		Name: "shared", SQLText: "SELECT 1", Scope: "GLOBAL", Owner: "m.chen",
	})
	if err != nil {
		t.Fatalf("create global: %v", err)
	}

	// Owner sees their own PERSONAL script.
	if _, ok, err := cfg.GetVisibleScript(ctx, personal.ID, "s.dobson", "dba-oncall"); err != nil || !ok {
		t.Errorf("owner GetVisibleScript(personal): ok=%v err=%v, want ok=true", ok, err)
	}
	// A different viewer (even one who is otherwise privileged) does NOT.
	if _, ok, err := cfg.GetVisibleScript(ctx, personal.ID, "m.chen", "dba-oncall"); err != nil || ok {
		t.Errorf("non-owner GetVisibleScript(personal): ok=%v err=%v, want ok=false", ok, err)
	}
	// GLOBAL is visible to any viewer.
	if _, ok, err := cfg.GetVisibleScript(ctx, global.ID, "s.dobson", "dba-oncall"); err != nil || !ok {
		t.Errorf("GetVisibleScript(global): ok=%v err=%v, want ok=true", ok, err)
	}
	// Missing id => (_, false, nil).
	if _, ok, err := cfg.GetVisibleScript(ctx, 987654, "s.dobson", "dba-oncall"); err != nil || ok {
		t.Errorf("missing GetVisibleScript: ok=%v err=%v", ok, err)
	}
}

func TestSavedScripts_SetScope_auditedAndOwnerGated(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg := store.NewConfig(pool)
	s, err := cfg.CreateScript(ctx, store.CreateScriptInput{
		Name: "replica-lag", SQLText: "SELECT 1", Scope: "PERSONAL", Owner: "s.dobson",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Non-owner, non-admin is rejected — no change, no audit row.
	if _, err := cfg.SetScriptScope(ctx, s.ID, "GLOBAL", "m.chen", false); err != store.ErrScriptForbidden {
		t.Fatalf("non-owner set: err=%v, want ErrScriptForbidden", err)
	}

	// Owner changes scope; row updated and an audit entry recorded.
	updated, err := cfg.SetScriptScope(ctx, s.ID, "TEAM", "s.dobson", false)
	if err != nil {
		t.Fatalf("owner set: %v", err)
	}
	if updated.Scope != "TEAM" {
		t.Errorf("scope = %q, want TEAM", updated.Scope)
	}

	var auditCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE action = 'saved_script.scope.change'`,
	).Scan(&auditCount); err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if auditCount != 1 {
		t.Errorf("scope-change audit rows = %d, want 1", auditCount)
	}

	// The row is bound to its scope-change audit entry (audit_chain_id).
	var chainID *int64
	if err := pool.QueryRow(ctx,
		`SELECT sc.audit_chain_id
		   FROM saved_scripts sc WHERE sc.id = $1`, s.ID).Scan(&chainID); err != nil {
		t.Fatalf("read audit_chain_id: %v", err)
	}
	if chainID == nil {
		t.Error("scope change did not bind audit_chain_id to the audit entry")
	} else {
		var linked int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM audit_log WHERE id = $1 AND action = 'saved_script.scope.change'`,
			*chainID).Scan(&linked); err != nil {
			t.Fatalf("verify linkage: %v", err)
		}
		if linked != 1 {
			t.Errorf("audit_chain_id %d does not point at the scope-change audit entry", *chainID)
		}
	}

	// Invalid target scope rejected.
	if _, err := cfg.SetScriptScope(ctx, s.ID, "WORLD", "s.dobson", false); err == nil {
		t.Error("expected invalid-scope error, got nil")
	}
	// Missing id => ErrScriptNotFound.
	if _, err := cfg.SetScriptScope(ctx, 987654, "GLOBAL", "s.dobson", true); err != store.ErrScriptNotFound {
		t.Errorf("missing set: err=%v, want ErrScriptNotFound", err)
	}
}

func TestSavedScripts_Delete_ownerGatedAndAudited(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg := store.NewConfig(pool)
	s, err := cfg.CreateScript(ctx, store.CreateScriptInput{
		Name: "d", SQLText: "SELECT 1", Scope: "PERSONAL", Owner: "s.dobson",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Non-owner rejected.
	if err := cfg.DeleteScript(ctx, s.ID, "m.chen", false); err != store.ErrScriptForbidden {
		t.Fatalf("non-owner delete: err=%v, want ErrScriptForbidden", err)
	}
	// Admin (not owner) allowed.
	if err := cfg.DeleteScript(ctx, s.ID, "root", true); err != nil {
		t.Fatalf("admin delete: %v", err)
	}
	if _, ok, _ := cfg.GetScript(ctx, s.ID); ok {
		t.Error("script still present after delete")
	}
	var auditCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE action = 'saved_script.delete'`,
	).Scan(&auditCount); err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if auditCount != 1 {
		t.Errorf("delete audit rows = %d, want 1", auditCount)
	}
	// Deleting a missing id => ErrScriptNotFound.
	if err := cfg.DeleteScript(ctx, s.ID, "root", true); err != store.ErrScriptNotFound {
		t.Errorf("re-delete: err=%v, want ErrScriptNotFound", err)
	}
}

func TestListScriptTargets_returnsClusterNodeDatabaseTriples(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg := store.NewConfig(pool)

	cl, err := cfg.CreateCluster(ctx, "orders-prod")
	if err != nil {
		t.Fatalf("cluster: %v", err)
	}
	in, err := cfg.CreateInstance(ctx, cl.ID, "srv-orders-primary")
	if err != nil {
		t.Fatalf("instance: %v", err)
	}
	// A server stream with a database name, linked to the instance.
	if _, err := pool.Exec(ctx,
		`INSERT INTO servers (id, name, database_name, instance_id) VALUES ($1,$2,$3,$4)`,
		"srv-1", "srv-orders-primary", "orders", in.ID); err != nil {
		t.Fatalf("seed server: %v", err)
	}

	got, err := cfg.ListScriptTargets(ctx)
	if err != nil {
		t.Fatalf("targets: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("targets = %d, want 1", len(got))
	}
	if got[0].Cluster != "orders-prod" || got[0].Node != "srv-orders-primary" || got[0].Database != "orders" {
		t.Errorf("target = %+v", got[0])
	}
}
