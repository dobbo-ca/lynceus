package store

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/ClickHouse/clickhouse-go/v2"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dobbo-ca/lynceus/internal/secure"
)

// OpenStats builds the stats backend selected by the REQUIRED
// LYNCEUS_STATS_BACKEND env var (there is no default — every deployment must
// choose):
//
//   - "postgres":   LYNCEUS_STATS_DSN (+ optional LYNCEUS_STATS_RO_DSN read replica)
//   - "clickhouse": LYNCEUS_CLICKHOUSE_DSN
//
// Migrations are applied before the handle is returned. The underlying pool /
// connection is owned for the process lifetime (closed by the OS on exit),
// matching the config-pool convention in the service mains.
//
// TLS: the Postgres DSNs are validated by secure.CheckDatabaseDSN. That guard
// is libpq/sslmode-specific, so the ClickHouse DSN is not run through it —
// ClickHouse TLS is carried in the DSN itself (clickhouse://…?secure=true).
func OpenStats(ctx context.Context) (Stats, error) {
	switch backend := os.Getenv("LYNCEUS_STATS_BACKEND"); backend {
	case "postgres":
		dsn := os.Getenv("LYNCEUS_STATS_DSN")
		if dsn == "" {
			return nil, errors.New("LYNCEUS_STATS_DSN required for postgres backend")
		}
		if err := secure.CheckDatabaseDSN(dsn, secure.RequireTLS()); err != nil {
			return nil, err
		}
		pool, err := pgxpool.New(ctx, dsn)
		if err != nil {
			return nil, err
		}
		if err := ApplyStatsMigrations(ctx, pool); err != nil {
			pool.Close()
			return nil, err
		}
		s := NewStats(pool)
		if roDSN := os.Getenv("LYNCEUS_STATS_RO_DSN"); roDSN != "" {
			if err := secure.CheckDatabaseDSN(roDSN, secure.RequireTLS()); err != nil {
				pool.Close()
				return nil, err
			}
			ro, err := pgxpool.New(ctx, roDSN)
			if err != nil {
				pool.Close()
				return nil, err
			}
			s.WithReadPool(ro)
		}
		return s, nil
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
		return nil, errors.New("LYNCEUS_STATS_BACKEND required (postgres|clickhouse)")
	default:
		return nil, fmt.Errorf("unknown LYNCEUS_STATS_BACKEND %q (want postgres|clickhouse)", backend)
	}
}
