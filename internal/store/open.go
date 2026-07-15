package store

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/ClickHouse/clickhouse-go/v2"
)

// OpenStats builds the stats backend selected by the REQUIRED
// LYNCEUS_STATS_BACKEND env var (there is no default — every deployment must
// choose). ClickHouse is the sole stats backend:
//
//   - "clickhouse": LYNCEUS_CLICKHOUSE_DSN
//
// Migrations are applied before the handle is returned. The underlying
// connection is owned for the process lifetime (closed by the OS on exit),
// matching the config-pool convention in the service mains.
//
// TLS: the ClickHouse DSN carries its own TLS setting
// (clickhouse://…?secure=true); it is not run through secure.CheckDatabaseDSN,
// which is libpq/sslmode-specific.
func OpenStats(ctx context.Context) (Stats, error) {
	switch backend := os.Getenv("LYNCEUS_STATS_BACKEND"); backend {
	case "clickhouse":
		dsn := os.Getenv("LYNCEUS_CLICKHOUSE_DSN")
		if dsn == "" {
			return nil, errors.New("LYNCEUS_CLICKHOUSE_DSN required for clickhouse backend")
		}
		opts, err := clickhouse.ParseDSN(dsn)
		if err != nil {
			return nil, fmt.Errorf("parse LYNCEUS_CLICKHOUSE_DSN: %w", err)
		}
		conn, err := clickhouse.Open(opts)
		if err != nil {
			return nil, err
		}
		if err := ApplyClickHouseMigrations(ctx, conn); err != nil {
			_ = conn.Close()
			return nil, err
		}
		return NewCHStats(conn), nil
	case "":
		return nil, errors.New("LYNCEUS_STATS_BACKEND required (clickhouse)")
	default:
		return nil, fmt.Errorf("unknown LYNCEUS_STATS_BACKEND %q (want clickhouse)", backend)
	}
}
