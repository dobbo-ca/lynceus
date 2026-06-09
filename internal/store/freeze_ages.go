package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// FreezeAgeRow is one T1 row of per-database / per-table transaction-id /
// MultiXact freeze AGES (counts only — never raw xids). Scope is "database"
// or "table". DataTier zero is coerced to 1 (T1) on insert.
type FreezeAgeRow struct {
	ServerID    string
	CollectedAt time.Time
	Scope       string // "database" | "table"
	SchemaName  string // "" for database scope
	ObjectName  string // table name or database name
	FQN         string // schema.name for tables; datname for db

	XIDAge                 int64
	MXIDAge                int64
	AutovacuumFreezeMaxAge int64

	DataTier int16 // 0 -> coerced to 1
}

// freezeAgesColumns is the COPY column order for WriteFreezeAges; it matches
// the 0010_freeze_ages.sql column order.
var freezeAgesColumns = []string{
	"server_id", "collected_at", "scope", "schema_name", "object_name", "fqn",
	"xid_age", "mxid_age", "autovacuum_freeze_max_age", "data_tier",
}

// WriteFreezeAges appends a batch of freeze-age rows via the COPY protocol,
// creating any missing weekly partitions first. Empty input is a no-op.
// Mirrors WriteTableStats.
func (s *Stats) WriteFreezeAges(ctx context.Context, rows []FreezeAgeRow) error {
	if len(rows) == 0 {
		return nil
	}

	weeks := map[string]time.Time{}
	for _, r := range rows {
		weeks[freezeAgesPartitionName(r.CollectedAt)] = r.CollectedAt
	}
	for _, ts := range weeks {
		if err := s.EnsureFreezeAgesWeeklyPartition(ctx, ts); err != nil {
			return err
		}
	}

	src := pgx.CopyFromSlice(len(rows), func(i int) ([]any, error) {
		r := rows[i]
		if r.DataTier == 0 {
			r.DataTier = 1
		}
		return []any{
			r.ServerID, r.CollectedAt, r.Scope, r.SchemaName, r.ObjectName, r.FQN,
			r.XIDAge, r.MXIDAge, r.AutovacuumFreezeMaxAge, r.DataTier,
		}, nil
	})
	_, err := s.pool.CopyFrom(ctx, pgx.Identifier{"freeze_ages"}, freezeAgesColumns, src)
	return err
}

// EnsureFreezeAgesWeeklyPartition creates the weekly partition for ts on
// freeze_ages if it does not already exist. Idempotent.
func (s *Stats) EnsureFreezeAgesWeeklyPartition(ctx context.Context, ts time.Time) error {
	name := freezeAgesPartitionName(ts)
	from, to := isoWeekBounds(ts)
	_, err := s.pool.Exec(ctx, fmt.Sprintf(
		`CREATE TABLE IF NOT EXISTS %s PARTITION OF freeze_ages
		 FOR VALUES FROM ('%s') TO ('%s')`,
		name,
		from.Format("2006-01-02"),
		to.Format("2006-01-02"),
	))
	return err
}

func freezeAgesPartitionName(ts time.Time) string {
	y, w := ts.UTC().ISOWeek()
	return fmt.Sprintf("freeze_ages_%04d_%02d", y, w)
}

// freezeAgesSelect is the shared column projection for the read methods, in
// FreezeAgeRow scan order.
const freezeAgesSelect = `SELECT server_id, collected_at, scope, schema_name, object_name, fqn,
        xid_age, mxid_age, autovacuum_freeze_max_age, data_tier
   FROM freeze_ages`

func scanFreezeAgeRows(rows pgx.Rows) ([]FreezeAgeRow, error) {
	defer rows.Close()
	var out []FreezeAgeRow
	for rows.Next() {
		var r FreezeAgeRow
		if err := rows.Scan(
			&r.ServerID, &r.CollectedAt, &r.Scope, &r.SchemaName, &r.ObjectName, &r.FQN,
			&r.XIDAge, &r.MXIDAge, &r.AutovacuumFreezeMaxAge, &r.DataTier,
		); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// LatestFreezeAges returns the most recent freeze_ages row per fqn for
// serverID at or before asOf. data_tier = 1 only (T1). Served from the read
// replica. Mirrors LatestTableStats.
func (s *Stats) LatestFreezeAges(ctx context.Context, serverID string, asOf time.Time) ([]FreezeAgeRow, error) {
	rows, err := s.ro.Query(ctx,
		freezeAgesSelect+`
		  WHERE server_id = $1 AND collected_at <= $2 AND data_tier = 1
		    AND (fqn, collected_at) IN (
		        SELECT fqn, max(collected_at) FROM freeze_ages
		         WHERE server_id = $1 AND collected_at <= $2 AND data_tier = 1
		         GROUP BY fqn)
		  ORDER BY fqn`,
		serverID, asOf,
	)
	if err != nil {
		return nil, err
	}
	return scanFreezeAgeRows(rows)
}
