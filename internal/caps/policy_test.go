package caps_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/dobbo-ca/lynceus/internal/caps"
	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/dobbo-ca/lynceus/internal/testpg"
)

// newConfigPool starts a postgres:16 container, applies config migrations,
// and returns a pool. Mirrors internal/api/server_test.go:21-47 + 71.
func newConfigPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	c, err := tcpostgres.Run(ctx,
		"postgres:16",
		tcpostgres.WithDatabase("lynceus_test"),
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
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate config: %v", err)
	}
	return pool
}

// configResolver adapts *store.Config to caps.PolicyResolver.
// It trims the PolicySource return value that EffectiveCapability
// exposes — Allowed only needs enabled + found.
type configResolver struct{ c *store.Config }

func (r configResolver) EffectiveCapabilityEnabled(ctx context.Context, serverID, databaseName, capability string) (bool, bool, error) {
	enabled, _, found, err := r.c.EffectiveCapability(ctx, serverID, databaseName, capability)
	return enabled, found, err
}

func TestAllowed_overrideBeatsDefault_andAbsentEnabled(t *testing.T) {
	pool := newConfigPool(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx,
		`INSERT INTO servers (id, name) VALUES ('srv-1', 'srv one')`,
	); err != nil {
		t.Fatalf("seed server: %v", err)
	}
	cfg := store.NewConfig(pool)
	resolver := configResolver{cfg}

	// Absent policy => fail-open enabled.
	ok, err := caps.Allowed(ctx, resolver, "srv-1", "appdb", caps.PgStatStatements)
	if err != nil {
		t.Fatalf("allowed (absent): %v", err)
	}
	if !ok {
		t.Fatal("absent policy must default to ENABLED (fail-open)")
	}

	// Server-wide default: disabled.
	if _, err := cfg.SetCapabilityPolicy(ctx, store.SetCapabilityPolicyInput{
		ServerID: "srv-1", Capability: "pg_stat_statements",
		Enabled: false, SetBy: "alice",
	}); err != nil {
		t.Fatalf("set default: %v", err)
	}
	ok, err = caps.Allowed(ctx, resolver, "srv-1", "appdb", caps.PgStatStatements)
	if err != nil {
		t.Fatalf("allowed (default): %v", err)
	}
	if ok {
		t.Fatal("server-default disabled must yield false")
	}

	// DB override re-enables for appdb. Override wins.
	if _, err := cfg.SetCapabilityPolicy(ctx, store.SetCapabilityPolicyInput{
		ServerID: "srv-1", DatabaseName: "appdb", Capability: "pg_stat_statements",
		Enabled: true, SetBy: "bob",
	}); err != nil {
		t.Fatalf("set override: %v", err)
	}
	ok, err = caps.Allowed(ctx, resolver, "srv-1", "appdb", caps.PgStatStatements)
	if err != nil {
		t.Fatalf("allowed (override): %v", err)
	}
	if !ok {
		t.Fatal("db override enabled must win over disabled default")
	}
}
