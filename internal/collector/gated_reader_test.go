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

func TestReader_gatedOff_returnsNoRows(t *testing.T) {
	ctx := context.Background()
	c, err := tcpostgres.Run(ctx,
		"postgres:16",
		tcpostgres.WithDatabase("appdb"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		testcontainers.WithCmd("postgres", "-c", "shared_preload_libraries=pg_stat_statements"),
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

	for _, stmt := range []string{
		`CREATE EXTENSION IF NOT EXISTS pg_stat_statements`,
		`CREATE TABLE users (id INT PRIMARY KEY)`,
		`SELECT pg_stat_statements_reset()`,
		`SELECT id FROM users WHERE id = 1`,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("setup %q: %v", stmt, err)
		}
	}

	gate := caps.NewGate()
	// Disable pg_stat_statements for appdb.
	gate.Replace(map[caps.GateKey]bool{
		{Db: "appdb", Cap: caps.PgStatStatements}: false,
	})

	r := collector.NewReader(pool, gate, "appdb")
	stats, err := r.Read(ctx)
	if err != nil {
		t.Fatalf("read (gated off): %v", err)
	}
	if stats != nil {
		t.Fatalf("gated-off reader returned %d rows, want nil", len(stats))
	}

	// Flip on: rows now return (proves the gate, not a query failure,
	// suppressed output).
	gate.Replace(map[caps.GateKey]bool{
		{Db: "appdb", Cap: caps.PgStatStatements}: true,
	})
	stats, err = r.Read(ctx)
	if err != nil {
		t.Fatalf("read (gated on): %v", err)
	}
	if len(stats) == 0 {
		t.Fatal("gated-on reader returned no rows, want >0")
	}
}

func TestActivityReader_gatedOff_returnsNoRows(t *testing.T) {
	ctx := context.Background()
	c, err := tcpostgres.Run(ctx,
		"postgres:16",
		tcpostgres.WithDatabase("appdb"),
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

	gate := caps.NewGate()
	gate.Replace(map[caps.GateKey]bool{
		{Db: "appdb", Cap: caps.PgStatActivityFullRead}: false,
	})

	r := collector.NewActivityReader(pool, gate, "appdb")
	samples, err := r.Read(ctx)
	if err != nil {
		t.Fatalf("read (gated off): %v", err)
	}
	if samples != nil {
		t.Fatalf("gated-off activity reader returned %d samples, want nil", len(samples))
	}

	gate.Replace(map[caps.GateKey]bool{
		{Db: "appdb", Cap: caps.PgStatActivityFullRead}: true,
	})
	samples, err = r.Read(ctx)
	if err != nil {
		t.Fatalf("read (gated on): %v", err)
	}
	// At least this connection's own client backend is visible.
	if len(samples) == 0 {
		t.Fatal("gated-on activity reader returned no samples, want >0")
	}
}
