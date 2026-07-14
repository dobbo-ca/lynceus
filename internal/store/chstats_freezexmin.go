package store

import (
	"context"
	"time"
)

// chFreezeAgesCols is the shared column order for freeze_ages writes and reads.
// It matches migrations/clickhouse/0006_freezexmin.sql and FreezeAgeRow scan
// order.
const chFreezeAgesCols = "server_id, collected_at, scope, schema_name, object_name, fqn, " +
	"xid_age, mxid_age, autovacuum_freeze_max_age, data_tier"

// chXminHorizonCols is the shared column order for xmin_horizon writes and
// reads. It matches migrations/clickhouse/0006_freezexmin.sql and
// XminHorizonRow scan order.
const chXminHorizonCols = "server_id, collected_at, oldest_xmin_age, holder_kind, data_tier"

// WriteFreezeAges batch-inserts freeze-age rows into freeze_ages. All rows are
// T1; DataTier==0 is coerced to 1. Empty input is a no-op. Mirrors the pgx
// WriteFreezeAges.
func (s *chStats) WriteFreezeAges(ctx context.Context, rows []FreezeAgeRow) error {
	if len(rows) == 0 {
		return nil
	}
	batch, err := s.conn.PrepareBatch(ctx, "INSERT INTO freeze_ages ("+chFreezeAgesCols+")")
	if err != nil {
		return err
	}
	for i := range rows {
		r := rows[i]
		if r.DataTier == 0 {
			r.DataTier = 1
		}
		if err := batch.Append(
			r.ServerID, r.CollectedAt, r.Scope, r.SchemaName, r.ObjectName, r.FQN,
			r.XIDAge, r.MXIDAge, r.AutovacuumFreezeMaxAge, r.DataTier,
		); err != nil {
			_ = batch.Abort()
			return err
		}
	}
	return batch.Send()
}

// LatestFreezeAges returns the most recent freeze_ages row per fqn for serverID
// at or before asOf. data_tier = 1 only (T1). Ordered by fqn. Mirrors the pgx
// LatestFreezeAges (tuple latest-as-of via correlated max(collected_at)).
func (s *chStats) LatestFreezeAges(ctx context.Context, serverID string, asOf time.Time) ([]FreezeAgeRow, error) {
	rows, err := s.conn.Query(ctx,
		`SELECT `+chFreezeAgesCols+`
		   FROM freeze_ages
		  WHERE server_id = ? AND collected_at <= ? AND data_tier = 1
		    AND (fqn, collected_at) IN (
		        SELECT fqn, max(collected_at) FROM freeze_ages
		         WHERE server_id = ? AND collected_at <= ? AND data_tier = 1
		         GROUP BY fqn)
		  ORDER BY fqn`,
		serverID, asOf, serverID, asOf,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

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

// WriteXminHorizons batch-inserts xmin-horizon rows into xmin_horizon. All rows
// are T1; DataTier==0 is coerced to 1. Empty input is a no-op. Mirrors the pgx
// WriteXminHorizons.
func (s *chStats) WriteXminHorizons(ctx context.Context, rows []XminHorizonRow) error {
	if len(rows) == 0 {
		return nil
	}
	batch, err := s.conn.PrepareBatch(ctx, "INSERT INTO xmin_horizon ("+chXminHorizonCols+")")
	if err != nil {
		return err
	}
	for i := range rows {
		r := rows[i]
		if r.DataTier == 0 {
			r.DataTier = 1
		}
		if err := batch.Append(
			r.ServerID, r.CollectedAt, r.OldestXminAge, r.HolderKind, r.DataTier,
		); err != nil {
			_ = batch.Abort()
			return err
		}
	}
	return batch.Send()
}

// LatestXminHorizon returns the single most recent xmin_horizon row for
// serverID at or before asOf. data_tier = 1 only (T1). The bool is false when
// no row exists. Cluster-global — one row, no fqn. Mirrors the pgx
// LatestXminHorizon.
func (s *chStats) LatestXminHorizon(ctx context.Context, serverID string, asOf time.Time) (XminHorizonRow, bool, error) {
	rows, err := s.conn.Query(ctx,
		`SELECT `+chXminHorizonCols+`
		   FROM xmin_horizon
		  WHERE server_id = ? AND collected_at <= ? AND data_tier = 1
		  ORDER BY collected_at DESC
		  LIMIT 1`,
		serverID, asOf,
	)
	if err != nil {
		return XminHorizonRow{}, false, err
	}
	defer func() { _ = rows.Close() }()

	if !rows.Next() {
		return XminHorizonRow{}, false, rows.Err()
	}
	var r XminHorizonRow
	if err := rows.Scan(&r.ServerID, &r.CollectedAt, &r.OldestXminAge, &r.HolderKind, &r.DataTier); err != nil {
		return XminHorizonRow{}, false, err
	}
	return r, true, nil
}
