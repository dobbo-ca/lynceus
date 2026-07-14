package store

import (
	"context"
	"time"
)

// chConnectionSamplesCols is the shared column order for connection_samples
// writes and the latest-batch read. All columns are T1 (no literal-bearing
// field); there is no separate T2 table for connections.
const chConnectionSamplesCols = "server_id, observed_at, pid, state, " +
	"active_seconds, xact_seconds, state_seconds, wait_event_type, data_tier"

// chBlockingEdgesCols is the shared column order for blocking_edges writes and
// the latest-batch read.
const chBlockingEdgesCols = "server_id, observed_at, blocked_pid, blocker_pid, blocked_wait_seconds, data_tier"

// WriteConnectionSamples appends a batch of T1 pg_stat_activity observations.
// DataTier 0 is coerced to 1 (mirrors the pgx impl). Empty input is a no-op.
func (s *chStats) WriteConnectionSamples(ctx context.Context, rows []ConnectionSampleRow) error {
	if len(rows) == 0 {
		return nil
	}
	batch, err := s.conn.PrepareBatch(ctx, "INSERT INTO connection_samples ("+chConnectionSamplesCols+")")
	if err != nil {
		return err
	}
	for i := range rows {
		r := rows[i]
		if r.DataTier == 0 {
			r.DataTier = 1
		}
		if err := batch.Append(
			r.ServerID, r.ObservedAt, r.PID, r.State,
			r.ActiveSeconds, r.XactSeconds, r.StateSeconds, r.WaitEventType, r.DataTier,
		); err != nil {
			_ = batch.Abort()
			return err
		}
	}
	return batch.Send()
}

// WriteBlockingEdges appends a batch of T1 A→B lock-wait relationships. DataTier
// 0 is coerced to 1. Empty input is a no-op.
func (s *chStats) WriteBlockingEdges(ctx context.Context, rows []BlockingEdgeRow) error {
	if len(rows) == 0 {
		return nil
	}
	batch, err := s.conn.PrepareBatch(ctx, "INSERT INTO blocking_edges ("+chBlockingEdgesCols+")")
	if err != nil {
		return err
	}
	for i := range rows {
		r := rows[i]
		if r.DataTier == 0 {
			r.DataTier = 1
		}
		if err := batch.Append(
			r.ServerID, r.ObservedAt, r.BlockedPID, r.BlockerPID, r.BlockedWaitSeconds, r.DataTier,
		); err != nil {
			_ = batch.Abort()
			return err
		}
	}
	return batch.Send()
}

// LatestConnectionSamples returns the most-recent observation batch (all rows
// sharing the max observed_at) for serverID at or before asOf, T1 only, ordered
// by pid. Mirrors the pgx latest-as-of semantics via a scalar max() subquery.
func (s *chStats) LatestConnectionSamples(ctx context.Context, serverID string, asOf time.Time) ([]ConnectionSampleRow, error) {
	rows, err := s.conn.Query(ctx,
		`SELECT `+chConnectionSamplesCols+`
		   FROM connection_samples
		  WHERE server_id = ? AND data_tier = 1
		    AND observed_at = (
		        SELECT max(observed_at) FROM connection_samples
		         WHERE server_id = ? AND observed_at <= ? AND data_tier = 1)
		  ORDER BY pid`,
		serverID, serverID, asOf,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []ConnectionSampleRow
	for rows.Next() {
		var r ConnectionSampleRow
		if err := rows.Scan(&r.ServerID, &r.ObservedAt, &r.PID, &r.State,
			&r.ActiveSeconds, &r.XactSeconds, &r.StateSeconds, &r.WaitEventType, &r.DataTier); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// LatestBlockingEdges returns the most-recent blocking batch for serverID at or
// before asOf, T1 only, ordered by (blocked_pid, blocker_pid).
func (s *chStats) LatestBlockingEdges(ctx context.Context, serverID string, asOf time.Time) ([]BlockingEdgeRow, error) {
	rows, err := s.conn.Query(ctx,
		`SELECT `+chBlockingEdgesCols+`
		   FROM blocking_edges
		  WHERE server_id = ? AND data_tier = 1
		    AND observed_at = (
		        SELECT max(observed_at) FROM blocking_edges
		         WHERE server_id = ? AND observed_at <= ? AND data_tier = 1)
		  ORDER BY blocked_pid, blocker_pid`,
		serverID, serverID, asOf,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []BlockingEdgeRow
	for rows.Next() {
		var r BlockingEdgeRow
		if err := rows.Scan(&r.ServerID, &r.ObservedAt, &r.BlockedPID, &r.BlockerPID,
			&r.BlockedWaitSeconds, &r.DataTier); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
