package store_test

import (
	"context"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/store"
)

// TestFleetMigration_createsEntitiesAndServerLink verifies 0005_fleet.sql adds
// the cluster + instance tables and the new servers columns, and that
// re-applying the config migrations is a no-op (Migrate tracks applied versions).
func TestFleetMigration_createsEntitiesAndServerLink(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// cluster + instance tables exist.
	for _, tbl := range []string{"cluster", "instance"} {
		var ok bool
		if err := pool.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_name = $1)`, tbl,
		).Scan(&ok); err != nil || !ok {
			t.Fatalf("table %q missing: ok=%v err=%v", tbl, ok, err)
		}
	}

	// servers gained instance_id + database_name.
	for _, col := range []string{"instance_id", "database_name"} {
		var ok bool
		if err := pool.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM information_schema.columns
			   WHERE table_name = 'servers' AND column_name = $1)`, col,
		).Scan(&ok); err != nil || !ok {
			t.Fatalf("servers.%s missing: ok=%v err=%v", col, ok, err)
		}
	}

	// instance.cluster_id FK enforces referential integrity.
	if _, err := pool.Exec(ctx,
		`INSERT INTO instance (id, cluster_id, name) VALUES ('i-x', 'no-such-cluster', 'x')`,
	); err == nil {
		t.Fatal("expected FK violation inserting instance with unknown cluster_id")
	}

	// idempotency: re-applying is a no-op.
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("re-apply: %v", err)
	}
}

func TestFleetStore_createResolveAndRollup(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg := store.NewConfig(pool)

	// Two server streams that will live under one instance, plus a second
	// instance under the same cluster with its own stream.
	for _, id := range []string{"srv-app", "srv-reporting", "srv-replica"} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO servers (id, name) VALUES ($1, $1)`, id); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}

	cl, err := cfg.CreateCluster(ctx, "prod-cluster")
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}
	primary, err := cfg.CreateInstance(ctx, cl.ID, "primary")
	if err != nil {
		t.Fatalf("CreateInstance primary: %v", err)
	}
	replica, err := cfg.CreateInstance(ctx, cl.ID, "replica")
	if err != nil {
		t.Fatalf("CreateInstance replica: %v", err)
	}
	if primary.Role != "unknown" {
		t.Fatalf("new instance role = %q, want default \"unknown\"", primary.Role)
	}

	// primary instance serves two databases; replica serves one.
	for _, sid := range []string{"srv-app", "srv-reporting"} {
		if err := cfg.AssignServerToInstance(ctx, sid, primary.ID); err != nil {
			t.Fatalf("assign %s: %v", sid, err)
		}
	}
	if err := cfg.AssignServerToInstance(ctx, "srv-replica", replica.ID); err != nil {
		t.Fatalf("assign replica: %v", err)
	}

	// ResolveServer returns the full chain.
	ss, inst, gotCl, err := cfg.ResolveServer(ctx, "srv-app")
	if err != nil {
		t.Fatalf("ResolveServer: %v", err)
	}
	if ss.ServerID != "srv-app" || ss.InstanceID != primary.ID ||
		inst.ID != primary.ID || inst.ClusterID != cl.ID || gotCl.ID != cl.ID {
		t.Fatalf("resolve chain wrong: ss=%+v inst=%+v cl=%+v", ss, inst, gotCl)
	}

	// Roll-up: instance -> its stream ids; cluster -> all stream ids.
	got, err := cfg.ServerIDsForInstance(ctx, primary.ID)
	if err != nil {
		t.Fatalf("ServerIDsForInstance: %v", err)
	}
	if len(got) != 2 || got[0] != "srv-app" || got[1] != "srv-reporting" {
		t.Fatalf("instance stream ids = %v, want [srv-app srv-reporting]", got)
	}
	all, err := cfg.ServerIDsForCluster(ctx, cl.ID)
	if err != nil {
		t.Fatalf("ServerIDsForCluster: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("cluster stream ids = %v, want 3", all)
	}

	// Listing helpers for the UI.
	clusters, err := cfg.ListClusters(ctx)
	if err != nil || len(clusters) != 1 || clusters[0].ID != cl.ID {
		t.Fatalf("ListClusters = %+v err=%v", clusters, err)
	}
	insts, err := cfg.ListInstances(ctx, cl.ID)
	if err != nil || len(insts) != 2 {
		t.Fatalf("ListInstances = %+v err=%v", insts, err)
	}
	streams, err := cfg.ListServerStreams(ctx, primary.ID)
	if err != nil || len(streams) != 2 {
		t.Fatalf("ListServerStreams = %+v err=%v", streams, err)
	}
}

func TestBackfillFleet_linksLegacyServers1to1AndIsIdempotent(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg := store.NewConfig(pool)

	// Two legacy server streams with no instance link.
	for _, r := range [][2]string{{"srv-1", "prod db"}, {"srv-2", "stage db"}} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO servers (id, name) VALUES ($1, $2)`, r[0], r[1]); err != nil {
			t.Fatalf("seed %s: %v", r[0], err)
		}
	}

	if err := cfg.BackfillFleet(ctx); err != nil {
		t.Fatalf("BackfillFleet: %v", err)
	}

	// Each legacy stream now resolves to its own cluster+instance, names kept.
	for _, sid := range []string{"srv-1", "srv-2"} {
		ss, inst, cl, err := cfg.ResolveServer(ctx, sid)
		if err != nil {
			t.Fatalf("ResolveServer %s after backfill: %v", sid, err)
		}
		if ss.InstanceID == "" || inst.ClusterID != cl.ID {
			t.Fatalf("%s not fully linked: ss=%+v inst=%+v cl=%+v", sid, ss, inst, cl)
		}
	}
	assertCounts := func(wantClusters, wantInstances int) {
		t.Helper()
		var nc, ni int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM cluster`).Scan(&nc); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM instance`).Scan(&ni); err != nil {
			t.Fatal(err)
		}
		if nc != wantClusters || ni != wantInstances {
			t.Fatalf("counts: clusters=%d instances=%d, want %d/%d", nc, ni, wantClusters, wantInstances)
		}
	}
	assertCounts(2, 2)

	// Idempotent: a second run links nothing new (no duplicate clusters/instances).
	if err := cfg.BackfillFleet(ctx); err != nil {
		t.Fatalf("BackfillFleet re-run: %v", err)
	}
	assertCounts(2, 2)
}
