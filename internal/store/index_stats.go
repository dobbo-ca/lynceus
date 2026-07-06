package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// IndexStatRow is one T1 row of per-index scan counter + size + structural
// validity/uniqueness flags. DataTier zero is coerced to 1 (T1) on insert.
// It carries NO index expression or predicate — those are literal-bearing
// and belong to T2.
type IndexStatRow struct {
	ServerID    string
	CollectedAt time.Time
	SchemaName  string
	ObjectName  string
	FQN         string
	TableFQN    string

	IdxScan   int64
	SizeBytes int64
	IsValid   bool
	IsReady   bool
	IsUnique  bool
	IsPrimary bool

	DataTier int16 // 0 -> coerced to 1
}

// indexStatsColumns is the COPY column order for WriteIndexStats; it matches
// the 0012_index_stats.sql column order.
var indexStatsColumns = []string{
	"server_id", "collected_at", "schema_name", "object_name", "fqn", "table_fqn",
	"idx_scan", "size_bytes", "is_valid", "is_ready", "is_unique", "is_primary",
	"data_tier",
}

// WriteIndexStats appends a batch of per-index stat rows via the COPY protocol,
// creating any missing weekly partitions first. Empty input is a no-op.
// Mirrors WriteTableStats / WriteFreezeAges.
func (s *pgxStats) WriteIndexStats(ctx context.Context, rows []IndexStatRow) error {
	if len(rows) == 0 {
		return nil
	}

	weeks := map[string]time.Time{}
	for i := range rows {
		r := &rows[i]
		weeks[indexStatsPartitionName(r.CollectedAt)] = r.CollectedAt
	}
	for _, ts := range weeks {
		if err := s.EnsureIndexStatsWeeklyPartition(ctx, ts); err != nil {
			return err
		}
	}

	src := pgx.CopyFromSlice(len(rows), func(i int) ([]any, error) {
		r := rows[i]
		if r.DataTier == 0 {
			r.DataTier = 1
		}
		return []any{
			r.ServerID, r.CollectedAt, r.SchemaName, r.ObjectName, r.FQN, r.TableFQN,
			r.IdxScan, r.SizeBytes, r.IsValid, r.IsReady, r.IsUnique, r.IsPrimary,
			r.DataTier,
		}, nil
	})
	_, err := s.pool.CopyFrom(ctx, pgx.Identifier{"index_stats"}, indexStatsColumns, src)
	return err
}

// EnsureIndexStatsWeeklyPartition creates the weekly partition for ts on
// index_stats if it does not already exist. Idempotent.
func (s *pgxStats) EnsureIndexStatsWeeklyPartition(ctx context.Context, ts time.Time) error {
	name := indexStatsPartitionName(ts)
	from, to := isoWeekBounds(ts)
	_, err := s.pool.Exec(ctx, fmt.Sprintf(
		`CREATE TABLE IF NOT EXISTS %s PARTITION OF index_stats
		 FOR VALUES FROM ('%s') TO ('%s')`,
		name,
		from.Format("2006-01-02"),
		to.Format("2006-01-02"),
	))
	return err
}

func indexStatsPartitionName(ts time.Time) string {
	y, w := ts.UTC().ISOWeek()
	return fmt.Sprintf("index_stats_%04d_%02d", y, w)
}

const indexStatsSelect = `SELECT server_id, collected_at, schema_name, object_name, fqn, table_fqn,
        idx_scan, size_bytes, is_valid, is_ready, is_unique, is_primary, data_tier
   FROM index_stats`

func scanIndexStatRows(rows pgx.Rows) ([]IndexStatRow, error) {
	defer rows.Close()
	var out []IndexStatRow
	for rows.Next() {
		var r IndexStatRow
		if err := rows.Scan(
			&r.ServerID, &r.CollectedAt, &r.SchemaName, &r.ObjectName, &r.FQN, &r.TableFQN,
			&r.IdxScan, &r.SizeBytes, &r.IsValid, &r.IsReady, &r.IsUnique, &r.IsPrimary, &r.DataTier,
		); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// LatestIndexStats returns the most recent index_stats row per fqn for
// serverID at or before asOf. data_tier = 1 only (T1). Served from the read
// replica. Mirrors LatestTableStats.
func (s *pgxStats) LatestIndexStats(ctx context.Context, serverID string, asOf time.Time) ([]IndexStatRow, error) {
	rows, err := s.ro.Query(ctx,
		indexStatsSelect+`
		  WHERE server_id = $1 AND collected_at <= $2 AND data_tier = 1
		    AND (fqn, collected_at) IN (
		        SELECT fqn, max(collected_at) FROM index_stats
		         WHERE server_id = $1 AND collected_at <= $2 AND data_tier = 1
		         GROUP BY fqn)
		  ORDER BY fqn`,
		serverID, asOf,
	)
	if err != nil {
		return nil, err
	}
	return scanIndexStatRows(rows)
}
