package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// TableStatRow is one T1 row of per-table size/growth + TOAST/heap/index
// breakdown plus dead-tuple and vacuum/analyze metrics. Zero-valued
// Last* timestamps are written as SQL NULL. DataTier zero is treated as
// 1 (T1) on insert — see the Stats package comment.
type TableStatRow struct {
	ServerID    string
	CollectedAt time.Time
	SchemaName  string
	ObjectName  string
	FQN         string

	TotalBytes   int64
	HeapBytes    int64
	ToastBytes   int64
	IndexesBytes int64

	RowEstimate      int64
	LiveTuples       int64
	DeadTuples       int64
	NModSinceAnalyze int64

	SeqScan    int64
	IdxScan    int64
	NTupIns    int64
	NTupUpd    int64
	NTupDel    int64
	NTupHotUpd int64

	LastVacuum      time.Time // zero -> NULL
	LastAutovacuum  time.Time // zero -> NULL
	LastAnalyze     time.Time // zero -> NULL
	LastAutoanalyze time.Time // zero -> NULL
	VacuumCount     int64
	AutovacuumCount int64

	DataTier int16 // 0 -> coerced to 1
}

// tableStatsColumns is the COPY column order for WriteTableStats; it
// matches the 0006_table_stats.sql column order and the proto field order.
var tableStatsColumns = []string{
	"server_id", "collected_at", "schema_name", "object_name", "fqn",
	"total_bytes", "heap_bytes", "toast_bytes", "indexes_bytes",
	"row_estimate", "live_tuples", "dead_tuples", "n_mod_since_analyze",
	"seq_scan", "idx_scan", "n_tup_ins", "n_tup_upd", "n_tup_del", "n_tup_hot_upd",
	"last_vacuum", "last_autovacuum", "last_analyze", "last_autoanalyze",
	"vacuum_count", "autovacuum_count", "data_tier",
}

// nullTime returns t for a non-zero time and nil (SQL NULL) for the zero
// time, so "never vacuumed/analyzed" persists as NULL not the epoch.
func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

// WriteTableStats appends a batch of per-table stat rows via the COPY
// protocol, creating any missing weekly partitions first. Mirrors
// WriteActivityBuckets / WriteQueryPlans: COPY routes each row to its
// weekly partition and is lighter on the storage DB than per-row INSERTs.
func (s *Stats) WriteTableStats(ctx context.Context, rows []TableStatRow) error {
	if len(rows) == 0 {
		return nil
	}

	weeks := map[string]time.Time{}
	for _, r := range rows {
		weeks[tableStatsPartitionName(r.CollectedAt)] = r.CollectedAt
	}
	for _, ts := range weeks {
		if err := s.EnsureTableStatsWeeklyPartition(ctx, ts); err != nil {
			return err
		}
	}

	src := pgx.CopyFromSlice(len(rows), func(i int) ([]any, error) {
		r := rows[i]
		if r.DataTier == 0 {
			r.DataTier = 1
		}
		return []any{
			r.ServerID, r.CollectedAt, r.SchemaName, r.ObjectName, r.FQN,
			r.TotalBytes, r.HeapBytes, r.ToastBytes, r.IndexesBytes,
			r.RowEstimate, r.LiveTuples, r.DeadTuples, r.NModSinceAnalyze,
			r.SeqScan, r.IdxScan, r.NTupIns, r.NTupUpd, r.NTupDel, r.NTupHotUpd,
			nullTime(r.LastVacuum), nullTime(r.LastAutovacuum),
			nullTime(r.LastAnalyze), nullTime(r.LastAutoanalyze),
			r.VacuumCount, r.AutovacuumCount, r.DataTier,
		}, nil
	})
	_, err := s.pool.CopyFrom(ctx, pgx.Identifier{"table_stats"}, tableStatsColumns, src)
	return err
}

// EnsureTableStatsWeeklyPartition creates the weekly partition for ts on
// table_stats if it does not already exist. Idempotent.
func (s *Stats) EnsureTableStatsWeeklyPartition(ctx context.Context, ts time.Time) error {
	name := tableStatsPartitionName(ts)
	from, to := isoWeekBounds(ts)
	_, err := s.pool.Exec(ctx, fmt.Sprintf(
		`CREATE TABLE IF NOT EXISTS %s PARTITION OF table_stats
		 FOR VALUES FROM ('%s') TO ('%s')`,
		name,
		from.Format("2006-01-02"),
		to.Format("2006-01-02"),
	))
	return err
}

func tableStatsPartitionName(ts time.Time) string {
	y, w := ts.UTC().ISOWeek()
	return fmt.Sprintf("table_stats_%04d_%02d", y, w)
}

// tableStatsSelect is the shared column projection for the read methods,
// in TableStatRow scan order (NULL timestamps scanned via *time.Time).
const tableStatsSelect = `SELECT server_id, collected_at, schema_name, object_name, fqn,
        total_bytes, heap_bytes, toast_bytes, indexes_bytes,
        row_estimate, live_tuples, dead_tuples, n_mod_since_analyze,
        seq_scan, idx_scan, n_tup_ins, n_tup_upd, n_tup_del, n_tup_hot_upd,
        last_vacuum, last_autovacuum, last_analyze, last_autoanalyze,
        vacuum_count, autovacuum_count, data_tier
   FROM table_stats`

func scanTableStatRows(rows pgx.Rows) ([]TableStatRow, error) {
	defer rows.Close()
	var out []TableStatRow
	for rows.Next() {
		var (
			r              TableStatRow
			lv, lav, la, laa *time.Time
		)
		if err := rows.Scan(
			&r.ServerID, &r.CollectedAt, &r.SchemaName, &r.ObjectName, &r.FQN,
			&r.TotalBytes, &r.HeapBytes, &r.ToastBytes, &r.IndexesBytes,
			&r.RowEstimate, &r.LiveTuples, &r.DeadTuples, &r.NModSinceAnalyze,
			&r.SeqScan, &r.IdxScan, &r.NTupIns, &r.NTupUpd, &r.NTupDel, &r.NTupHotUpd,
			&lv, &lav, &la, &laa,
			&r.VacuumCount, &r.AutovacuumCount, &r.DataTier,
		); err != nil {
			return nil, err
		}
		if lv != nil {
			r.LastVacuum = *lv
		}
		if lav != nil {
			r.LastAutovacuum = *lav
		}
		if la != nil {
			r.LastAnalyze = *la
		}
		if laa != nil {
			r.LastAutoanalyze = *laa
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// LatestTableStats returns the most recent table_stats row per fqn for
// serverID at or before asOf. data_tier = 1 only (T1). Served from the
// read replica.
func (s *Stats) LatestTableStats(ctx context.Context, serverID string, asOf time.Time) ([]TableStatRow, error) {
	rows, err := s.ro.Query(ctx,
		tableStatsSelect+`
		  WHERE server_id = $1 AND collected_at <= $2 AND data_tier = 1
		    AND (fqn, collected_at) IN (
		        SELECT fqn, max(collected_at) FROM table_stats
		         WHERE server_id = $1 AND collected_at <= $2 AND data_tier = 1
		         GROUP BY fqn)
		  ORDER BY fqn`,
		serverID, asOf,
	)
	if err != nil {
		return nil, err
	}
	return scanTableStatRows(rows)
}

// TableSizeSeries returns every table_stats row for (serverID, fqn) captured
// in [since, until), oldest first — the growth series. data_tier = 1 only.
func (s *Stats) TableSizeSeries(ctx context.Context, serverID, fqn string, since, until time.Time) ([]TableStatRow, error) {
	rows, err := s.ro.Query(ctx,
		tableStatsSelect+`
		  WHERE server_id = $1 AND fqn = $2
		    AND collected_at >= $3 AND collected_at < $4
		    AND data_tier = 1
		  ORDER BY collected_at ASC`,
		serverID, fqn, since, until,
	)
	if err != nil {
		return nil, err
	}
	return scanTableStatRows(rows)
}
