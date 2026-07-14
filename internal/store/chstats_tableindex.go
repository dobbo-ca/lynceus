package store

import (
	"context"
	"time"
)

// chTableStatsCols is the shared column order for table_stats writes and the
// Latest read; it matches 0005_tableindex.sql and TableStatRow scan order. The
// last_* columns are Nullable(DateTime64) — a never-vacuumed/analyzed row
// persists as NULL (mirroring the pgx nullTime behaviour), not the epoch.
const chTableStatsCols = "server_id, collected_at, schema_name, object_name, fqn, " +
	"total_bytes, heap_bytes, toast_bytes, indexes_bytes, " +
	"row_estimate, live_tuples, dead_tuples, n_mod_since_analyze, " +
	"seq_scan, idx_scan, n_tup_ins, n_tup_upd, n_tup_del, n_tup_hot_upd, " +
	"last_vacuum, last_autovacuum, last_analyze, last_autoanalyze, " +
	"vacuum_count, autovacuum_count, data_tier"

// chIndexStatsCols is the shared column order for index_stats writes and the
// Latest read; it matches 0005_tableindex.sql and IndexStatRow scan order. The
// catalog booleans are stored as UInt8.
const chIndexStatsCols = "server_id, collected_at, schema_name, object_name, fqn, table_fqn, " +
	"idx_scan, size_bytes, is_valid, is_ready, is_unique, is_primary, data_tier"

// chTableIndexNullTime maps a zero time.Time to a nil *time.Time so a
// never-vacuumed/analyzed timestamp lands as SQL NULL in the Nullable column;
// a non-zero time is passed through by pointer. Mirrors the pgx nullTime helper
// (which returns any); named uniquely to avoid a package-level collision.
func chTableIndexNullTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// chTableIndexBool converts a Go bool to the UInt8 ClickHouse stores.
func chTableIndexBool(b bool) uint8 {
	if b {
		return 1
	}
	return 0
}

// WriteTableStats batch-inserts per-table stat rows into table_stats. DataTier
// zero is coerced to 1 (T1), matching the pgx impl. Empty input is a no-op.
func (s *chStats) WriteTableStats(ctx context.Context, rows []TableStatRow) error {
	if len(rows) == 0 {
		return nil
	}
	batch, err := s.conn.PrepareBatch(ctx, "INSERT INTO table_stats ("+chTableStatsCols+")")
	if err != nil {
		return err
	}
	for i := range rows {
		r := rows[i]
		if r.DataTier == 0 {
			r.DataTier = 1
		}
		if err := batch.Append(
			r.ServerID, r.CollectedAt, r.SchemaName, r.ObjectName, r.FQN,
			r.TotalBytes, r.HeapBytes, r.ToastBytes, r.IndexesBytes,
			r.RowEstimate, r.LiveTuples, r.DeadTuples, r.NModSinceAnalyze,
			r.SeqScan, r.IdxScan, r.NTupIns, r.NTupUpd, r.NTupDel, r.NTupHotUpd,
			chTableIndexNullTime(r.LastVacuum), chTableIndexNullTime(r.LastAutovacuum),
			chTableIndexNullTime(r.LastAnalyze), chTableIndexNullTime(r.LastAutoanalyze),
			r.VacuumCount, r.AutovacuumCount, r.DataTier,
		); err != nil {
			_ = batch.Abort()
			return err
		}
	}
	return batch.Send()
}

// LatestTableStats returns the most recent table_stats row per fqn for serverID
// at or before asOf, data_tier = 1 only, ordered by fqn. Mirrors the pgx
// DISTINCT-ON semantics via `ORDER BY fqn, collected_at DESC LIMIT 1 BY fqn`.
func (s *chStats) LatestTableStats(ctx context.Context, serverID string, asOf time.Time) ([]TableStatRow, error) {
	rows, err := s.conn.Query(ctx,
		`SELECT `+chTableStatsCols+`
		   FROM table_stats
		  WHERE server_id = ? AND collected_at <= ? AND data_tier = 1
		  ORDER BY fqn ASC, collected_at DESC
		  LIMIT 1 BY fqn`,
		serverID, asOf,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []TableStatRow
	for rows.Next() {
		var (
			r                TableStatRow
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

// WriteIndexStats batch-inserts per-index stat rows into index_stats. DataTier
// zero is coerced to 1 (T1), matching the pgx impl. Empty input is a no-op.
func (s *chStats) WriteIndexStats(ctx context.Context, rows []IndexStatRow) error {
	if len(rows) == 0 {
		return nil
	}
	batch, err := s.conn.PrepareBatch(ctx, "INSERT INTO index_stats ("+chIndexStatsCols+")")
	if err != nil {
		return err
	}
	for i := range rows {
		r := rows[i]
		if r.DataTier == 0 {
			r.DataTier = 1
		}
		if err := batch.Append(
			r.ServerID, r.CollectedAt, r.SchemaName, r.ObjectName, r.FQN, r.TableFQN,
			r.IdxScan, r.SizeBytes,
			chTableIndexBool(r.IsValid), chTableIndexBool(r.IsReady),
			chTableIndexBool(r.IsUnique), chTableIndexBool(r.IsPrimary),
			r.DataTier,
		); err != nil {
			_ = batch.Abort()
			return err
		}
	}
	return batch.Send()
}

// LatestIndexStats returns the most recent index_stats row per fqn for serverID
// at or before asOf, data_tier = 1 only, ordered by fqn. Mirrors the pgx
// DISTINCT-ON semantics via `ORDER BY fqn, collected_at DESC LIMIT 1 BY fqn`.
func (s *chStats) LatestIndexStats(ctx context.Context, serverID string, asOf time.Time) ([]IndexStatRow, error) {
	rows, err := s.conn.Query(ctx,
		`SELECT `+chIndexStatsCols+`
		   FROM index_stats
		  WHERE server_id = ? AND collected_at <= ? AND data_tier = 1
		  ORDER BY fqn ASC, collected_at DESC
		  LIMIT 1 BY fqn`,
		serverID, asOf,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []IndexStatRow
	for rows.Next() {
		var (
			r                                     IndexStatRow
			isValid, isReady, isUnique, isPrimary uint8
		)
		if err := rows.Scan(
			&r.ServerID, &r.CollectedAt, &r.SchemaName, &r.ObjectName, &r.FQN, &r.TableFQN,
			&r.IdxScan, &r.SizeBytes,
			&isValid, &isReady, &isUnique, &isPrimary,
			&r.DataTier,
		); err != nil {
			return nil, err
		}
		r.IsValid = isValid != 0
		r.IsReady = isReady != 0
		r.IsUnique = isUnique != 0
		r.IsPrimary = isPrimary != 0
		out = append(out, r)
	}
	return out, rows.Err()
}
