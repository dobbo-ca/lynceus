// Package testch centralizes the ClickHouse testcontainer setup used by the
// integration tests, mirroring internal/testpg for Postgres.
//
// The clickhouse module's default wait strategy only checks HTTP 200 on 8123,
// which can go ready slightly before the native 9000 listener reliably serves
// queries, so Start polls Ping over the native protocol before returning.
package testch

import (
	"context"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/testcontainers/testcontainers-go"
	tcclickhouse "github.com/testcontainers/testcontainers-go/modules/clickhouse"
)

// Start boots a ClickHouse container (matching the dev image), waits until it
// accepts native queries, and returns a ready driver.Conn. The container and
// connection are torn down via t.Cleanup. If Docker/testcontainers is
// unavailable, the test is skipped.
func Start(t *testing.T) driver.Conn {
	t.Helper()
	ctx := context.Background()

	c, err := tcclickhouse.Run(ctx,
		"clickhouse/clickhouse-server:25.8",
		tcclickhouse.WithDatabase("lynceus_stats"),
		tcclickhouse.WithUsername("test"),
		tcclickhouse.WithPassword("test"),
	)
	if err != nil {
		t.Skipf("docker/testcontainers unavailable: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(c) })

	dsn, err := c.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	opts, err := clickhouse.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	conn, err := clickhouse.Open(opts)
	if err != nil {
		t.Fatalf("open clickhouse: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	var pingErr error
	for i := 0; i < 30; i++ {
		if pingErr = conn.Ping(ctx); pingErr == nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if pingErr != nil {
		t.Fatalf("clickhouse not ready: %v", pingErr)
	}
	return conn
}
