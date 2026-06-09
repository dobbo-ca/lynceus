// Integration test for the collector reader. Spins up a real Postgres
// with pg_stat_statements loaded, executes a literal-bearing query
// against it, and asserts that the Reader returns the query in
// normalized form with no literal substring surviving.
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
	"github.com/dobbo-ca/lynceus/internal/testpg"
)

func TestReader_returnsNormalizedQueriesWithNoLiterals(t *testing.T) {
	ctx := context.Background()

	c, err := tcpostgres.Run(ctx,
		"postgres:16",
		tcpostgres.WithDatabase("lynceus_target"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		testcontainers.WithCmd("postgres", "-c", "shared_preload_libraries=pg_stat_statements"),
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

	// Activate the extension and seed a target table.
	for _, stmt := range []string{
		`CREATE EXTENSION IF NOT EXISTS pg_stat_statements`,
		`CREATE TABLE users (id INT PRIMARY KEY, email TEXT)`,
		`INSERT INTO users VALUES (1, 'seed@example.com')`,
		`SELECT pg_stat_statements_reset()`,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("setup %q: %v", stmt, err)
		}
	}

	// Execute a query containing a literal we want to prove never
	// reaches the wire.
	const secret = "leaky-canary@phi.example.com"
	if _, err := pool.Exec(ctx,
		`SELECT id FROM users WHERE email = $1`, secret,
	); err != nil {
		t.Fatal(err)
	}
	// Also run with an in-line literal (pg_stat_statements normalizes
	// parameter binds; this forces the literal path through our
	// normalizer too).
	if _, err := pool.Exec(ctx,
		`SELECT id FROM users WHERE email = 'leaky-inline@phi.example.com'`,
	); err != nil {
		t.Fatal(err)
	}

	r := collector.NewReader(pool, caps.NewGate(), "lynceus_target")
	stats, err := r.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(stats) == 0 {
		t.Fatal("reader returned no rows")
	}

	for _, s := range stats {
		if s.Fingerprint == "" {
			t.Error("missing fingerprint on a returned row")
		}
		if s.NormalizedQuery == "" {
			t.Error("missing normalized_query on a returned row")
		}
		// THE PRIVACY GUARANTEE: no literal substring from the
		// monitored database may appear in what the reader returns.
		for _, forbidden := range []string{
			"leaky-canary", "leaky-inline", "phi.example.com", "seed@",
		} {
			if strings.Contains(s.NormalizedQuery, forbidden) {
				t.Errorf("LITERAL LEAK from reader: %q contains %q",
					s.NormalizedQuery, forbidden)
			}
		}
	}
}
