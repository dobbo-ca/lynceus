// Integration test for the schema-object inventory reader. Spins up a
// real Postgres, creates two schemas (one sensitive), seeds objects in
// both, then asserts:
//
//  1. Inventory returns objects from the allowed schema with sizes.
//  2. The ignore_schema_regexp filter PREVENTS objects in the excluded
//     schema from appearing in the reader output at all — across every
//     ObjectKind. This is the privacy guarantee.
package collector_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/dobbo-ca/lynceus/internal/caps"
	"github.com/dobbo-ca/lynceus/internal/collector"
	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
	"github.com/dobbo-ca/lynceus/internal/testpg"
)

//nolint:gocyclo // scenario-driven integration test; the assertions make complexity inherent
func TestInventory_ReturnsObjectsWithSizes(t *testing.T) {
	ctx := context.Background()

	c, err := tcpostgres.Run(ctx,
		"postgres:16",
		tcpostgres.WithDatabase("lynceus_target"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		testpg.ReadyWait(),
	)
	if err != nil {
		t.Skipf("docker/testcontainers unavailable: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(c) })

	url, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	for _, stmt := range []string{
		`CREATE SCHEMA reporting`,
		`CREATE SCHEMA patient_phi`,
		`CREATE TABLE reporting.orders (id INT PRIMARY KEY, total NUMERIC)`,
		`CREATE INDEX orders_total_idx ON reporting.orders (total)`,
		`CREATE VIEW reporting.recent_orders AS SELECT id FROM reporting.orders`,
		`CREATE FUNCTION reporting.add_one(x INT) RETURNS INT
		   LANGUAGE sql IMMUTABLE AS $$ SELECT x + 1 $$`,
		`CREATE SEQUENCE reporting.order_seq`,
		// Insert rows so pg_total_relation_size returns > 0.
		`INSERT INTO reporting.orders
		   SELECT g, g::numeric FROM generate_series(1, 1000) g`,
		`ANALYZE reporting.orders`,

		// Sensitive schema — must be excluded entirely by the filter.
		`CREATE TABLE patient_phi.records (id INT PRIMARY KEY, name TEXT)`,
		`CREATE INDEX records_name_idx ON patient_phi.records (name)`,
		`INSERT INTO patient_phi.records VALUES (1, 'do-not-leak-this-name')`,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("setup %q: %v", stmt, err)
		}
	}

	filter, err := collector.NewSchemaFilter("", "^patient_.*")
	if err != nil {
		t.Fatal(err)
	}
	inv := collector.NewInventory(pool, filter, caps.NewGate(), "lynceus_target")

	objs, err := inv.Read(ctx)
	if err != nil {
		t.Fatalf("inventory: %v", err)
	}
	if len(objs) == 0 {
		t.Fatal("inventory returned no objects")
	}

	// Index by FQN for assertions.
	byFQN := map[string]*lynceusv1.SchemaObject{}
	for _, o := range objs {
		byFQN[o.Fqn] = o
	}

	// (1) The allowed schema yielded at least one object of each expected kind.
	wantKinds := map[lynceusv1.ObjectKind]string{
		lynceusv1.ObjectKind_OBJECT_KIND_SCHEMA:   "reporting.",
		lynceusv1.ObjectKind_OBJECT_KIND_TABLE:    "reporting.orders",
		lynceusv1.ObjectKind_OBJECT_KIND_INDEX:    "reporting.orders_total_idx",
		lynceusv1.ObjectKind_OBJECT_KIND_VIEW:     "reporting.recent_orders",
		lynceusv1.ObjectKind_OBJECT_KIND_FUNCTION: "reporting.add_one",
		lynceusv1.ObjectKind_OBJECT_KIND_SEQUENCE: "reporting.order_seq",
	}
	for kind, fqn := range wantKinds {
		got, ok := byFQN[fqn]
		if !ok {
			t.Errorf("missing expected object %q (kind=%v)", fqn, kind)
			continue
		}
		if got.Kind != kind {
			t.Errorf("object %q kind = %v, want %v", fqn, got.Kind, kind)
		}
	}

	// (2) Sizes — the table and its index must be > 0; functions/views/sequences = 0.
	if t1 := byFQN["reporting.orders"]; t1 == nil || t1.SizeBytes <= 0 {
		t.Errorf("reporting.orders size_bytes must be > 0, got %v", t1)
	}
	if ix := byFQN["reporting.orders_total_idx"]; ix == nil || ix.SizeBytes <= 0 {
		t.Errorf("reporting.orders_total_idx size_bytes must be > 0, got %v", ix)
	}
	if v := byFQN["reporting.recent_orders"]; v == nil || v.SizeBytes != 0 {
		t.Errorf("view size_bytes must be 0, got %v", v)
	}

	// (3) THE PRIVACY GUARANTEE: no object from patient_phi may appear.
	for _, o := range objs {
		if strings.HasPrefix(o.Schema, "patient_") {
			t.Errorf("LEAK: filtered schema %q surfaced object %q", o.Schema, o.Fqn)
		}
		if strings.Contains(o.Fqn, "patient_") {
			t.Errorf("LEAK: filtered schema appears in fqn %q", o.Fqn)
		}
		if o.Name == "records" || o.Name == "records_name_idx" {
			t.Errorf("LEAK: filtered object %q surfaced", o.Name)
		}
		// The collector must NOT stamp first-seen: it never touches the
		// stats DB. first_seen_at is resolved server-side by the
		// ingestion upsert (ON CONFLICT preserves it). Outgoing objects
		// carry 0 here.
		if o.FirstSeenAtUnix != 0 {
			t.Errorf("collector must not stamp first_seen_at_unix; got %d on %q", o.FirstSeenAtUnix, o.Fqn)
		}
	}
}

// TestInventory_gatedOffReturnsNoRows proves the SchemaInventory capability
// gate short-circuits Read BEFORE any query: a nil pool would panic if the
// reader touched the DB, so a clean nil result means the gate suppressed it.
func TestInventory_gatedOffReturnsNoRows(t *testing.T) {
	g := caps.NewGate()
	g.Replace(map[caps.GateKey]bool{{Db: "lynceus_target", Cap: caps.SchemaInventory}: false})
	filter, err := collector.NewSchemaFilter("", "")
	if err != nil {
		t.Fatal(err)
	}
	inv := collector.NewInventory(nil, filter, g, "lynceus_target")

	objs, err := inv.Read(context.Background())
	if err != nil {
		t.Fatalf("gated-off Read returned error: %v", err)
	}
	if objs != nil {
		t.Errorf("gated-off Read returned %d objects, want nil (no query)", len(objs))
	}
}
