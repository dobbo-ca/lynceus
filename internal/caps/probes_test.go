// Integration tests for capability probes. Each probe spins up its own
// real Postgres via testcontainers — slow but honest. CLAUDE.md mandates
// real Postgres for integration tests (no mocks).
package caps_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/dobbo-ca/lynceus/internal/caps"
)

func runPG(t *testing.T, extraCmd ...string) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	opts := []testcontainers.ContainerCustomizer{
		tcpostgres.WithDatabase("lynceus_target"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		tcpostgres.BasicWaitStrategies(),
	}
	if len(extraCmd) > 0 {
		opts = append(opts, testcontainers.WithCmd(extraCmd...))
	}
	c, err := tcpostgres.Run(ctx, "postgres:16", opts...)
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
	return pool
}

func TestProbeExtensions_pgStatStatementsInstalled(t *testing.T) {
	pool := runPG(t, "postgres", "-c", "shared_preload_libraries=pg_stat_statements")
	if _, err := pool.Exec(context.Background(),
		`CREATE EXTENSION pg_stat_statements`); err != nil {
		t.Fatal(err)
	}

	out := caps.Set{}
	caps.ProbeExtensions(context.Background(), pool, out)

	if !out[caps.PgStatStatements].Available {
		t.Errorf("PgStatStatements expected Available, got %+v", out[caps.PgStatStatements])
	}
	if out[caps.PgStatStatements].Reason == "" {
		t.Error("PgStatStatements Available=true must include extversion in Reason")
	}

	for _, c := range []caps.Capability{
		caps.PgBuffercache, caps.PgWaitSampling, caps.PgStatTuple,
	} {
		if out[c].Available {
			t.Errorf("%s expected Available=false (not installed), got %+v", c, out[c])
		}
		if !strings.Contains(out[c].Reason, "not installed") {
			t.Errorf("%s Reason should explain absence, got %q", c, out[c].Reason)
		}
	}
}

func TestProbeExtensions_alwaysWritesEveryExtensionKey(t *testing.T) {
	pool := runPG(t)
	out := caps.Set{}
	caps.ProbeExtensions(context.Background(), pool, out)
	for _, c := range []caps.Capability{
		caps.PgStatStatements, caps.PgBuffercache,
		caps.PgWaitSampling, caps.PgStatTuple,
	} {
		if _, ok := out[c]; !ok {
			t.Errorf("ProbeExtensions did not write key %q", c)
		}
	}
}

func TestProbeServerVersion_picksUpVersionNum(t *testing.T) {
	pool := runPG(t)
	out := caps.Set{}
	caps.ProbeServerVersion(context.Background(), pool, out)
	st := out[caps.ServerVersion]
	if !st.Available {
		t.Fatalf("ServerVersion expected Available, got %+v", st)
	}
	if !strings.HasPrefix(st.Reason, "server_version_num=") {
		t.Errorf("Reason missing prefix: %q", st.Reason)
	}
}

func TestProbeRolePermissions_reportsPgMonitorGrant(t *testing.T) {
	pool := runPG(t)
	out := caps.Set{}
	caps.ProbeRolePermissions(context.Background(), pool, out)
	st := out[caps.RolePermissions]
	if !st.Available {
		t.Errorf("RolePermissions expected Available (superuser implies pg_monitor), got %+v", st)
	}
	if !strings.Contains(st.Reason, "rolsuper=true") {
		t.Errorf("Reason should mention rolsuper=true for superuser, got %q", st.Reason)
	}
	if !strings.Contains(st.Reason, "pg_monitor=true") {
		t.Errorf("Reason should mention pg_monitor=true, got %q", st.Reason)
	}
}

func TestProbeStatActivityFullRead_visibleAsSuperuser(t *testing.T) {
	pool := runPG(t)
	out := caps.Set{}
	caps.ProbeStatActivityFullRead(context.Background(), pool, out)
	st := out[caps.PgStatActivityFullRead]
	if !st.Available {
		t.Errorf("PgStatActivityFullRead expected Available as superuser, got %+v", st)
	}
}
