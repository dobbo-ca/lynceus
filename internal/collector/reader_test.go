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
	stats, _, err := r.Read(ctx)
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

// startSeededReaderPG boots a monitored Postgres with pg_stat_statements
// preloaded, seeds one statement so pg_stat_statements has a row, and returns a
// pool bound to it plus the database name (the gate key). Mirrors the harness in
// TestReader_returnsNormalizedQueriesWithNoLiterals — the collector reader needs
// a monitored target with pg_stat_statements, which the shared testpg container
// does not load (see testpg package doc).
func startSeededReaderPG(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()
	ctx := context.Background()
	const db = "lynceus_target"
	c, err := tcpostgres.Run(ctx,
		"postgres:16",
		tcpostgres.WithDatabase(db),
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

	for _, stmt := range []string{
		`CREATE EXTENSION IF NOT EXISTS pg_stat_statements`,
		`CREATE TABLE users (id INT PRIMARY KEY, email TEXT)`,
		`INSERT INTO users VALUES (1, 'seed@example.com')`,
		`SELECT pg_stat_statements_reset()`,
		`SELECT id FROM users WHERE email = 'leaky-inline@phi.example.com'`,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("setup %q: %v", stmt, err)
		}
	}
	return pool, db
}

// TestRead_T2Gate_ShipsRawNotT1 pins the ly-cwr.5 collector branch: when the
// query_text_t2 capability is explicitly ON (fail-closed AllowedStrict), Read
// emits QueryStatRaw rows (raw + pg_query fingerprint + normalized) and NO T1
// QueryStat.
func TestRead_T2Gate_ShipsRawNotT1(t *testing.T) {
	ctx := context.Background()
	pool, db := startSeededReaderPG(t)

	gate := caps.NewGate()
	gate.Replace(map[caps.GateKey]bool{
		{Db: db, Cap: caps.PgStatStatements}: true,
		{Db: db, Cap: caps.QueryTextT2}:      true,
	})
	r := collector.NewReader(pool, gate, db)
	stats, raws, err := r.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 0 {
		t.Fatalf("T2 mode must not emit T1 QueryStat, got %d", len(stats))
	}
	if len(raws) == 0 {
		t.Fatal("T2 mode must emit QueryStatRaw")
	}
	if raws[0].RawQuery == "" || raws[0].Fingerprint == "" || raws[0].NormalizedQuery == "" {
		t.Fatal("raw must carry raw_query + pg_query fingerprint + normalized")
	}
}

// TestRead_NoGate_ShipsT1Only pins the fail-closed default: with only
// pg_stat_statements enabled (query_text_t2 absent => AllowedStrict false), Read
// emits T1 QueryStat only and never a raw payload.
func TestRead_NoGate_ShipsT1Only(t *testing.T) {
	ctx := context.Background()
	pool, db := startSeededReaderPG(t)

	gate := caps.NewGate()
	gate.Replace(map[caps.GateKey]bool{{Db: db, Cap: caps.PgStatStatements}: true})
	r := collector.NewReader(pool, gate, db)
	stats, raws, err := r.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(raws) != 0 {
		t.Fatalf("no gate must emit no raw, got %d", len(raws))
	}
	if len(stats) == 0 {
		t.Fatal("no gate must still emit T1")
	}
}
