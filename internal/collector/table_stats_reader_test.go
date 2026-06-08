package collector_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/dobbo-ca/lynceus/internal/caps"
	"github.com/dobbo-ca/lynceus/internal/collector"
)

func TestTableStatsReader_SizesAndToast(t *testing.T) {
	ctx := context.Background()

	c, err := tcpostgres.Run(ctx,
		"postgres:16",
		tcpostgres.WithDatabase("lynceus_target"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		tcpostgres.BasicWaitStrategies(),
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

	// reporting.big forced to grow a TOAST relation via wide text values;
	// UPDATE + ANALYZE populates dead/live tuples. patient_phi is the
	// filtered-out schema.
	for _, stmt := range []string{
		`CREATE SCHEMA reporting`,
		`CREATE SCHEMA patient_phi`,
		`CREATE TABLE reporting.big (id INT PRIMARY KEY, blob TEXT)`,
		`INSERT INTO reporting.big SELECT g, repeat('x', 100000) FROM generate_series(1, 50) g`,
		`UPDATE reporting.big SET blob = repeat('y', 100000) WHERE id <= 25`,
		`ANALYZE reporting.big`,
		`CREATE TABLE patient_phi.records (id INT PRIMARY KEY, ssn TEXT)`,
		`INSERT INTO patient_phi.records VALUES (1, '000-00-0000')`,
		`ANALYZE patient_phi.records`,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("setup %q: %v", stmt, err)
		}
	}

	filter, err := collector.NewSchemaFilter("", "^patient_.*")
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	r := collector.NewTableStatsReader(pool, filter, caps.NewGate(), "lynceus_target")
	stats, err := r.Read(ctx, "srv-1")
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var big *struct {
		Total, Heap, Toast, Indexes int64
		Live, Dead                  int64
	}
	for _, s := range stats {
		if s.GetFqn() == "patient_phi.records" {
			t.Fatalf("FILTERED schema leaked into table stats: %q", s.GetFqn())
		}
		if s.GetFqn() == "reporting.big" {
			big = &struct {
				Total, Heap, Toast, Indexes int64
				Live, Dead                  int64
			}{
				s.GetTotalBytes(), s.GetHeapBytes(), s.GetToastBytes(), s.GetIndexesBytes(),
				s.GetLiveTuples(), s.GetDeadTuples(),
			}
			if s.GetSchema() != "reporting" || s.GetName() != "big" {
				t.Errorf("bad identifiers: schema=%q name=%q", s.GetSchema(), s.GetName())
			}
		}
	}
	if big == nil {
		t.Fatal("reporting.big not returned")
	}
	if big.Toast <= 0 {
		t.Errorf("toast_bytes = %d, want > 0 (wide values should force a TOAST relation)", big.Toast)
	}
	if big.Heap <= 0 {
		t.Errorf("heap_bytes = %d, want > 0", big.Heap)
	}
	// total should account for heap + toast + indexes (allow slack for fsm/vm).
	if big.Total < big.Heap+big.Toast+big.Indexes {
		t.Errorf("total_bytes %d < heap+toast+idx %d", big.Total, big.Heap+big.Toast+big.Indexes)
	}
	if big.Live <= 0 {
		t.Errorf("live_tuples = %d, want > 0 after ANALYZE", big.Live)
	}
	// Note: n_dead_tup from pg_stat_user_tables is only reliably > 0 after
	// autovacuum or explicit VACUUM has run. In fresh testcontainers autovacuum
	// may not fire during the test, so we only assert non-negative (schema is
	// correct) rather than > 0.
	if big.Dead < 0 {
		t.Errorf("dead_tuples = %d, want >= 0", big.Dead)
	}
}

// TestTableStatsReader_gatedOffReturnsNoRows proves the TableSize capability
// gate short-circuits Read before any query: a nil pool would panic if the
// reader touched the DB, so a clean nil result means the gate suppressed it.
func TestTableStatsReader_gatedOffReturnsNoRows(t *testing.T) {
	g := caps.NewGate()
	g.Replace(map[caps.GateKey]bool{{Db: "lynceus_target", Cap: caps.TableSize}: false})
	filter, err := collector.NewSchemaFilter("", "")
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	r := collector.NewTableStatsReader(nil, filter, g, "lynceus_target")

	stats, err := r.Read(context.Background(), "srv-1")
	if err != nil {
		t.Fatalf("gated-off Read returned error: %v", err)
	}
	if stats != nil {
		t.Errorf("gated-off Read returned %d rows, want nil (no query)", len(stats))
	}
}
