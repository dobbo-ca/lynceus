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

// TestSettingsReader_shipsOnlyAllowlistedNames proves the reader NEVER does
// SELECT * FROM pg_settings: every returned name is one of the curated,
// safe-valued tuning GUCs, and the known free-form / infra-leaking settings
// (log_line_prefix, data_directory, search_path) are absent even though they
// exist in pg_settings on every server.
func TestSettingsReader_shipsOnlyAllowlistedNames(t *testing.T) {
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

	r := collector.NewSettingsReader(pool, caps.NewGate(), "lynceus_target")
	rows, err := r.Read(ctx, "srv-a")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("expected at least one allowlisted setting, got 0")
	}

	allow := map[string]struct{}{}
	for _, n := range collector.SettingsAllowlistForTest() {
		allow[n] = struct{}{}
	}
	var sawSharedBuffers, sawAutovacuum bool
	for _, s := range rows {
		if _, ok := allow[s.GetName()]; !ok {
			t.Errorf("returned non-allowlisted setting %q — possible SELECT * leak", s.GetName())
		}
		switch s.GetName() {
		case "shared_buffers":
			sawSharedBuffers = true
		case "autovacuum":
			sawAutovacuum = true
		case "log_line_prefix", "data_directory", "search_path", "archive_command":
			t.Errorf("free-form setting %q must never be shipped", s.GetName())
		}
	}
	if !sawSharedBuffers || !sawAutovacuum {
		t.Errorf("expected core tuning GUCs present: shared_buffers=%v autovacuum=%v", sawSharedBuffers, sawAutovacuum)
	}
}

// TestSettingsReader_gatedOffReturnsNoRows proves the Settings capability gate
// short-circuits Read before any query: a nil pool would panic if the reader
// touched the DB, so a clean nil result means the gate suppressed it.
func TestSettingsReader_gatedOffReturnsNoRows(t *testing.T) {
	g := caps.NewGate()
	g.Replace(map[caps.GateKey]bool{{Db: "lynceus_target", Cap: caps.Settings}: false})
	r := collector.NewSettingsReader(nil, g, "lynceus_target")

	rows, err := r.Read(context.Background(), "srv-a")
	if err != nil {
		t.Fatalf("gated-off Read returned error: %v", err)
	}
	if rows != nil {
		t.Errorf("gated-off Read returned %d rows, want nil (no query)", len(rows))
	}
}
