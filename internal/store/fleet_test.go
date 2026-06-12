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
