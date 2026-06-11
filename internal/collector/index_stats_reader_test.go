package collector_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/dobbo-ca/lynceus/internal/caps"
	"github.com/dobbo-ca/lynceus/internal/collector"
	"github.com/dobbo-ca/lynceus/internal/testpg"
)

func mustFilter(t *testing.T) *collector.SchemaFilter {
	t.Helper()
	f, err := collector.NewSchemaFilter("", "")
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	return f
}

func TestIndexStatsReaderReadsValidAndInvalidIndexes(t *testing.T) {
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

	// A table with a PK (unique+primary), a secondary index, and a
	// deliberately-invalidated index (set indisvalid=false directly in the
	// catalog — the supported way to simulate a failed CREATE INDEX
	// CONCURRENTLY in a test).
	if _, err := pool.Exec(ctx, `CREATE TABLE idx_demo(id int PRIMARY KEY, status text)`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `CREATE INDEX idx_demo_status ON idx_demo(status)`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE pg_index SET indisvalid = false
		   WHERE indexrelid = 'idx_demo_status'::regclass`); err != nil {
		t.Fatal(err)
	}

	r := collector.NewIndexStatsReader(pool, mustFilter(t), caps.NewGate(), "lynceus_target")
	rows, err := r.Read(ctx, "srv-a")
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var sawPK, sawInvalid bool
	for _, is := range rows {
		if is.GetName() == "idx_demo_pkey" {
			sawPK = true
			if !is.GetIsPrimary() || !is.GetIsUnique() || is.GetTableFqn() != "public.idx_demo" {
				t.Errorf("pkey flags wrong: %+v", is)
			}
		}
		if is.GetName() == "idx_demo_status" {
			sawInvalid = true
			if is.GetIsValid() {
				t.Errorf("idx_demo_status should be invalid: %+v", is)
			}
		}
		if is.GetSizeBytes() < 0 {
			t.Fatalf("negative size: %+v", is)
		}
	}
	if !sawPK || !sawInvalid {
		t.Fatalf("want pkey + invalid index; pk=%v invalid=%v rows=%d", sawPK, sawInvalid, len(rows))
	}
}

// TestIndexStatsReader_gatedOffReturnsNoRows proves the IndexStats capability
// gate short-circuits Read before any query: a nil pool would panic if the
// reader touched the DB, so a clean nil result means the gate suppressed it.
func TestIndexStatsReader_gatedOffReturnsNoRows(t *testing.T) {
	g := caps.NewGate()
	g.Replace(map[caps.GateKey]bool{{Db: "lynceus_target", Cap: caps.IndexStats}: false})
	r := collector.NewIndexStatsReader(nil, mustFilter(t), g, "lynceus_target")

	rows, err := r.Read(context.Background(), "srv-a")
	if err != nil {
		t.Fatalf("gated-off Read returned error: %v", err)
	}
	if rows != nil {
		t.Errorf("gated-off Read returned %d rows, want nil (no query)", len(rows))
	}
}
