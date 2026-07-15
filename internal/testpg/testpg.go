// Package testpg centralizes the Postgres testcontainer setup used by the
// integration tests.
//
// One Postgres container is booted per test binary (package) and reused by
// every test in that package; each Start/StartDSN call hands the test its own
// freshly-created database on that shared server, dropped on cleanup. This turns
// N per-test container boots (~15s each) into a single boot plus a cheap
// CREATE/DROP DATABASE per test. The container is intentionally not terminated —
// it is reaped by the testcontainers Reaper when the test process exits.
//
// ReadyWait remains available for callers that boot their own container (e.g.
// collector tests that need a monitored Postgres with bespoke state). Container
// tests are skipped under `go test -short`.
package testpg

import (
	"context"
	"fmt"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// ReadyWait is a drop-in replacement for tcpostgres.BasicWaitStrategies():
// it waits for the listening port and then for pg_isready (over TCP) to
// report the server is accepting connections. This avoids two CI-Docker
// failure modes: "connection reset by peer" on the first query (the mapped
// port opens during the entrypoint's temporary init server, before the final
// server accepts TCP), and log-line waits being unusable when
// logging_collector=on redirects the "ready" message away from stderr.
func ReadyWait() testcontainers.CustomizeRequestOption {
	return testcontainers.WithWaitStrategy(
		wait.ForListeningPort("5432/tcp").WithStartupTimeout(60*time.Second),
		wait.ForExec([]string{"pg_isready", "-h", "127.0.0.1", "-p", "5432"}).
			WithStartupTimeout(60*time.Second),
	)
}

// shared holds the process-wide admin DSN (pointing at the maintenance
// database), resolved once per test binary.
var (
	bootOnce sync.Once
	baseDSN  string
	bootErr  error
	dbSeq    atomic.Uint64
)

func boot() {
	ctx := context.Background()
	c, err := tcpostgres.Run(ctx,
		"postgres:16",
		tcpostgres.WithDatabase("postgres"), // maintenance db for CREATE DATABASE
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		ReadyWait(),
	)
	if err != nil {
		bootErr = err
		return
	}
	// Intentionally not terminated: reused for the whole package, reaped by the
	// testcontainers Reaper (ryuk) on process exit.
	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		bootErr = err
		return
	}
	baseDSN = dsn
}

// Start boots (or reuses) the shared Postgres container and returns a pool
// scoped to a fresh, isolated database for this test. The database is dropped
// via t.Cleanup. If Docker/testcontainers is unavailable, the test is skipped;
// under `go test -short` the test is skipped without booting.
func Start(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, _ := StartDSN(t)
	return pool
}

// StartDSN is like Start but also returns the connection string for the
// isolated database, for callers that open their own pool/connection.
func StartDSN(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping Postgres container test in -short mode")
	}
	bootOnce.Do(boot)
	if bootErr != nil {
		t.Skipf("docker/testcontainers unavailable: %v", bootErr)
	}

	ctx := context.Background()
	db := fmt.Sprintf("test_%d", dbSeq.Add(1))

	admin, err := pgxpool.New(ctx, baseDSN)
	if err != nil {
		t.Fatalf("admin pool: %v", err)
	}
	_, err = admin.Exec(ctx, "CREATE DATABASE "+db)
	admin.Close()
	if err != nil {
		t.Fatalf("create database %s: %v", db, err)
	}

	dsn := withDB(t, baseDSN, db)
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		if admin, err := pgxpool.New(context.Background(), baseDSN); err == nil {
			_, _ = admin.Exec(context.Background(), "DROP DATABASE IF EXISTS "+db+" WITH (FORCE)")
			admin.Close()
		}
	})
	return pool, dsn
}

// withDB returns dsn with its database path replaced by db.
func withDB(t *testing.T, dsn, db string) string {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	u.Path = "/" + db
	return u.String()
}
