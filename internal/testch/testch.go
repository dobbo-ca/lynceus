// Package testch centralizes the ClickHouse testcontainer setup used by the
// integration tests, mirroring internal/testpg for Postgres.
//
// One ClickHouse container is booted per test binary (package) and reused by
// every test in that package; each Start/StartDSN call hands the test its own
// freshly-created database on that shared server, dropped on cleanup. This turns
// N per-test container boots (~15s each) into a single boot plus a cheap
// CREATE/DROP DATABASE per test. The container is intentionally not terminated —
// it is reaped by the testcontainers Reaper when the test process exits.
//
// Start/StartDSN keep their original signatures so existing call sites are
// unchanged. Container tests are skipped under `go test -short`.
package testch

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	tcclickhouse "github.com/testcontainers/testcontainers-go/modules/clickhouse"
)

// shared holds the process-wide container connection options, resolved once.
type shared struct {
	opts *clickhouse.Options
	err  error
}

var (
	bootOnce sync.Once
	base     shared
	dbSeq    atomic.Uint64
)

// boot starts the single shared ClickHouse container and resolves the base
// connection options (pointing at the default database). It waits until the
// native protocol answers Ping, since the module's HTTP wait can return before
// 9000 reliably serves queries.
func boot() {
	ctx := context.Background()
	c, err := tcclickhouse.Run(ctx,
		"clickhouse/clickhouse-server:25.8",
		tcclickhouse.WithDatabase("lynceus_stats"),
		tcclickhouse.WithUsername("test"),
		tcclickhouse.WithPassword("test"),
	)
	if err != nil {
		base.err = err
		return
	}
	// Intentionally not terminated: reused for the whole package, reaped by the
	// testcontainers Reaper (ryuk) on process exit.

	dsn, err := c.ConnectionString(ctx)
	if err != nil {
		base.err = err
		return
	}
	opts, err := clickhouse.ParseDSN(dsn)
	if err != nil {
		base.err = err
		return
	}
	admin, err := clickhouse.Open(opts)
	if err != nil {
		base.err = err
		return
	}
	defer func() { _ = admin.Close() }()
	var pingErr error
	for range 60 {
		if pingErr = admin.Ping(ctx); pingErr == nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if pingErr != nil {
		base.err = pingErr
		return
	}
	base.opts = opts
}

// Start boots (or reuses) the shared ClickHouse container and returns a ready
// driver.Conn scoped to a fresh, isolated database for this test. The database
// is dropped via t.Cleanup. If Docker/testcontainers is unavailable, the test
// is skipped; under `go test -short` the test is skipped without booting.
func Start(t *testing.T) driver.Conn {
	t.Helper()
	conn, _ := StartDSN(t)
	return conn
}

// StartDSN is like Start but also returns the native connection string
// (clickhouse://…@host:port/<isolated-db>), for callers that open their own
// connection from the DSN (e.g. exercising store.OpenStats).
func StartDSN(t *testing.T) (driver.Conn, string) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping ClickHouse container test in -short mode")
	}
	bootOnce.Do(boot)
	if base.err != nil {
		t.Skipf("docker/testcontainers unavailable: %v", base.err)
	}

	ctx := context.Background()
	db := fmt.Sprintf("test_%d", dbSeq.Add(1))

	// Create the isolated per-test database on the shared server.
	admin, err := clickhouse.Open(base.opts)
	if err != nil {
		t.Fatalf("open admin conn: %v", err)
	}
	if err := admin.Exec(ctx, "CREATE DATABASE "+db); err != nil {
		_ = admin.Close()
		t.Fatalf("create database %s: %v", db, err)
	}
	_ = admin.Close()

	// A connection scoped to the isolated database. Options is copied so the
	// per-test Database override does not mutate the shared base.
	opts := *base.opts
	opts.Auth.Database = db
	conn, err := clickhouse.Open(&opts)
	if err != nil {
		t.Fatalf("open %s: %v", db, err)
	}
	t.Cleanup(func() {
		if admin, err := clickhouse.Open(base.opts); err == nil {
			_ = admin.Exec(context.Background(), "DROP DATABASE IF EXISTS "+db)
			_ = admin.Close()
		}
		_ = conn.Close()
	})

	dsn := fmt.Sprintf("clickhouse://%s:%s@%s/%s",
		opts.Auth.Username, opts.Auth.Password, opts.Addr[0], db)
	return conn, dsn
}
