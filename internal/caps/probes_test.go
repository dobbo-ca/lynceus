// Integration tests for capability probes. Each probe spins up its own
// real Postgres via testcontainers — slow but honest. CLAUDE.md mandates
// real Postgres for integration tests (no mocks).
package caps_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/dobbo-ca/lynceus/internal/caps"
)

// pgConfig is one optional Postgres customization for runPG. Use
// withCmd to set extra `-c key=value` flags; use withLoggingCollector
// when the test enables logging_collector=on, which silences stderr
// (so BasicWaitStrategies times out) and forces a port-only wait.
type pgConfig struct {
	extraCmd  []string
	useLogCol bool
}

type pgOpt func(*pgConfig)

func withCmd(args ...string) pgOpt {
	return func(c *pgConfig) { c.extraCmd = args }
}

// withLoggingCollector swaps the wait strategy to port-only, because
// logging_collector=on captures all stderr into a log file so the
// "database system is ready" message never reaches the stderr stream
// BasicWaitStrategies watches for.
func withLoggingCollector() pgOpt {
	return func(c *pgConfig) { c.useLogCol = true }
}

func runPG(t *testing.T, opts ...pgOpt) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	cfg := pgConfig{}
	for _, o := range opts {
		o(&cfg)
	}

	tcOpts := []testcontainers.ContainerCustomizer{
		tcpostgres.WithDatabase("lynceus_target"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
	}
	if cfg.useLogCol {
		tcOpts = append(tcOpts,
			testcontainers.WithWaitStrategy(
				wait.ForListeningPort("5432/tcp").WithStartupTimeout(60*time.Second),
			),
		)
	} else {
		tcOpts = append(tcOpts, tcpostgres.BasicWaitStrategies())
	}
	if len(cfg.extraCmd) > 0 {
		tcOpts = append(tcOpts, testcontainers.WithCmd(cfg.extraCmd...))
	}

	c, err := tcpostgres.Run(ctx, "postgres:16", tcOpts...)
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

	// Port-only wait can return before Postgres has finished initdb /
	// the second start. Poll until SELECT 1 succeeds.
	if cfg.useLogCol {
		deadline := time.Now().Add(60 * time.Second)
		for {
			if _, err := pool.Exec(ctx, "SELECT 1"); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("pg never became queryable")
			}
			time.Sleep(500 * time.Millisecond)
		}
	}
	return pool
}

func TestProbeExtensions_pgStatStatementsInstalled(t *testing.T) {
	pool := runPG(t, withCmd("postgres", "-c", "shared_preload_libraries=pg_stat_statements"))
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

func TestProbeLogDestination_pickupValueAndCollector(t *testing.T) {
	pool := runPG(t,
		withCmd(
			"postgres",
			"-c", "log_destination=csvlog,stderr",
			"-c", "logging_collector=on",
		),
		withLoggingCollector(),
	)
	out := caps.Set{}
	caps.ProbeLogDestination(context.Background(), pool, out)
	st := out[caps.LogDestination]
	if !st.Available {
		t.Errorf("LogDestination expected Available with csvlog+collector, got %+v", st)
	}
	if !strings.Contains(st.Reason, "dest=csvlog,stderr") {
		t.Errorf("Reason missing dest value: %q", st.Reason)
	}
	if !strings.Contains(st.Reason, "collector=true") {
		t.Errorf("Reason missing collector=true: %q", st.Reason)
	}
}

func TestProbeLogDestination_stderrOnlyIsUnavailable(t *testing.T) {
	pool := runPG(t)
	out := caps.Set{}
	caps.ProbeLogDestination(context.Background(), pool, out)
	st := out[caps.LogDestination]
	if st.Available {
		t.Errorf("LogDestination should be unavailable with bare stderr, got %+v", st)
	}
}

func TestProbeAutoExplain_disabledWithoutPreload(t *testing.T) {
	pool := runPG(t)
	out := caps.Set{}
	caps.ProbeAutoExplain(context.Background(), pool, out)
	st := out[caps.AutoExplain]
	if st.Available {
		t.Errorf("AutoExplain should be unavailable without preload, got %+v", st)
	}
	if !strings.Contains(st.Reason, "not in shared_preload_libraries") {
		t.Errorf("Reason should explain preload absence, got %q", st.Reason)
	}
}

func TestProbeAutoExplain_enabledWhenPreloadAndThreshold(t *testing.T) {
	pool := runPG(t, withCmd(
		"postgres",
		"-c", "shared_preload_libraries=auto_explain",
		"-c", "auto_explain.log_min_duration=0",
	))
	out := caps.Set{}
	caps.ProbeAutoExplain(context.Background(), pool, out)
	st := out[caps.AutoExplain]
	if !st.Available {
		t.Errorf("AutoExplain expected Available with preload+threshold, got %+v", st)
	}
	if !strings.Contains(st.Reason, "log_min_duration=0") {
		t.Errorf("Reason should include threshold, got %q", st.Reason)
	}
}

func TestDiscover_returnsEntryForEveryDeclaredCapability(t *testing.T) {
	pool := runPG(t,
		withCmd(
			"postgres",
			"-c", "shared_preload_libraries=pg_stat_statements,auto_explain",
			"-c", "auto_explain.log_min_duration=0",
			"-c", "log_destination=csvlog,stderr",
			"-c", "logging_collector=on",
		),
		withLoggingCollector(),
	)
	if _, err := pool.Exec(context.Background(),
		`CREATE EXTENSION pg_stat_statements`); err != nil {
		t.Fatal(err)
	}

	d := caps.NewDiscoverer(pool)
	set, err := d.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	for _, c := range caps.Declared() {
		if _, ok := set[c]; !ok {
			t.Errorf("Discover missing key %q", c)
		}
	}

	for _, c := range []caps.Capability{
		caps.PgStatStatements, caps.AutoExplain,
		caps.LogDestination, caps.ServerVersion,
		caps.RolePermissions, caps.PgStatActivityFullRead,
	} {
		if !set[c].Available {
			t.Errorf("%s expected Available=true in fully-tooled container, got %+v", c, set[c])
		}
	}

	for _, c := range []caps.Capability{
		caps.PgBuffercache, caps.PgWaitSampling, caps.PgStatTuple,
	} {
		if set[c].Available {
			t.Errorf("%s expected Available=false (not installed), got %+v", c, set[c])
		}
	}
}

func TestDiscover_contextCancelledReturnsError(t *testing.T) {
	pool := runPG(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d := caps.NewDiscoverer(pool)
	if _, err := d.Discover(ctx); err == nil {
		t.Error("Discover with pre-cancelled context should error")
	}
}
