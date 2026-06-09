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

func TestFreezeAgeReaderReadsDatabaseAndTableScopes(t *testing.T) {
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

	if _, err := pool.Exec(ctx, `CREATE TABLE froz_demo(id int)`); err != nil {
		t.Fatal(err)
	}

	filter, err := collector.NewSchemaFilter("", "")
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	r := collector.NewFreezeAgeReader(pool, filter, caps.NewGate(), "lynceus_target")
	rows, err := r.Read(ctx, "srv-a")
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var sawDB, sawTable bool
	for _, fa := range rows {
		if fa.GetScope() == "database" {
			sawDB = true
			if fa.GetFqn() != "lynceus_target" {
				t.Errorf("db-scope fqn = %q, want lynceus_target", fa.GetFqn())
			}
		}
		if fa.GetScope() == "table" && fa.GetFqn() == "public.froz_demo" {
			sawTable = true
		}
		if fa.GetXidAge() < 0 {
			t.Fatalf("negative xid age: %+v", fa)
		}
		if fa.GetAutovacuumFreezeMaxAge() <= 0 {
			t.Errorf("autovacuum_freeze_max_age not attached: %+v", fa)
		}
	}
	if !sawDB || !sawTable {
		t.Fatalf("want db+table scopes, db=%v table=%v rows=%d", sawDB, sawTable, len(rows))
	}
}

// TestFreezeAgeReader_gatedOffReturnsNoRows proves the FreezeAge capability
// gate short-circuits Read before any query: a nil pool would panic if the
// reader touched the DB, so a clean nil result means the gate suppressed it.
func TestFreezeAgeReader_gatedOffReturnsNoRows(t *testing.T) {
	g := caps.NewGate()
	g.Replace(map[caps.GateKey]bool{{Db: "lynceus_target", Cap: caps.FreezeAge}: false})
	filter, err := collector.NewSchemaFilter("", "")
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	r := collector.NewFreezeAgeReader(nil, filter, g, "lynceus_target")

	rows, err := r.Read(context.Background(), "srv-a")
	if err != nil {
		t.Fatalf("gated-off Read returned error: %v", err)
	}
	if rows != nil {
		t.Errorf("gated-off Read returned %d rows, want nil (no query)", len(rows))
	}
}
