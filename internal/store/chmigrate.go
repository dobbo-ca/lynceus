package store

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

//go:embed migrations/clickhouse/*.sql
var clickhouseMigrations embed.FS

// ApplyClickHouseMigrations applies every migrations/clickhouse/*.sql file in
// lexical order, idempotently. Applied versions (filename minus .sql) are
// recorded in a schema_migrations table so re-runs skip completed files.
// ClickHouse has no transactional DDL; the DDL is CREATE TABLE IF NOT EXISTS,
// so re-applying a file is harmless even if a crash lands between apply and
// record. Each file may contain multiple statements separated by ';' (CH
// executes one statement per query), so the body is split on ';'.
func ApplyClickHouseMigrations(ctx context.Context, conn driver.Conn) error {
	if err := conn.Exec(ctx,
		`CREATE TABLE IF NOT EXISTS schema_migrations (
			version String,
			at DateTime DEFAULT now()
		) ENGINE = MergeTree ORDER BY version`,
	); err != nil {
		return fmt.Errorf("init schema_migrations: %w", err)
	}

	entries, err := fs.ReadDir(clickhouseMigrations, "migrations/clickhouse")
	if err != nil {
		return fmt.Errorf("read clickhouse migrations: %w", err)
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)

	for _, name := range files {
		version := strings.TrimSuffix(name, ".sql")

		var n uint64
		if err := conn.QueryRow(ctx,
			"SELECT count() FROM schema_migrations WHERE version = ?", version,
		).Scan(&n); err != nil {
			return fmt.Errorf("check %s: %w", version, err)
		}
		if n > 0 {
			continue
		}

		body, err := fs.ReadFile(clickhouseMigrations, "migrations/clickhouse/"+name)
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}
		for _, stmt := range strings.Split(string(body), ";") {
			stmt = strings.TrimSpace(stmt)
			if stmt == "" {
				continue
			}
			if err := conn.Exec(ctx, stmt); err != nil {
				return fmt.Errorf("apply %s: %w", name, err)
			}
		}
		if err := conn.Exec(ctx,
			"INSERT INTO schema_migrations (version) VALUES (?)", version,
		); err != nil {
			return fmt.Errorf("record %s: %w", version, err)
		}
	}
	return nil
}
