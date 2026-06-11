package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// ConnectionSampleRow is one T1 point-in-time pg_stat_activity backend
// observation: pid, fixed state label, integer durations — never query text.
// DataTier zero is coerced to 1 (T1) on insert.
type ConnectionSampleRow struct {
	ServerID      string
	ObservedAt    time.Time
	PID           int64
	State         string
	ActiveSeconds int64
	XactSeconds   int64
	StateSeconds  int64
	WaitEventType string
	DataTier      int16 // 0 -> coerced to 1
}

// BlockingEdgeRow is one T1 A→B lock-wait relationship from pg_blocking_pids().
type BlockingEdgeRow struct {
	ServerID           string
	ObservedAt         time.Time
	BlockedPID         int64
	BlockerPID         int64
	BlockedWaitSeconds int64
	DataTier           int16 // 0 -> coerced to 1
}

var connectionSamplesColumns = []string{
	"server_id", "observed_at", "pid", "state",
	"active_seconds", "xact_seconds", "state_seconds", "wait_event_type", "data_tier",
}

var blockingEdgesColumns = []string{
	"server_id", "observed_at", "blocked_pid", "blocker_pid", "blocked_wait_seconds", "data_tier",
}

// WriteConnectionSamples appends a batch via COPY, creating any missing weekly
// partitions first. Empty input is a no-op. Mirrors WriteFreezeAges.
func (s *Stats) WriteConnectionSamples(ctx context.Context, rows []ConnectionSampleRow) error {
	if len(rows) == 0 {
		return nil
	}
	weeks := map[string]time.Time{}
	for i := range rows {
		weeks[connectionSamplesPartitionName(rows[i].ObservedAt)] = rows[i].ObservedAt
	}
	for _, ts := range weeks {
		if err := s.ensureConnWeeklyPartition(ctx, "connection_samples", connectionSamplesPartitionName(ts), ts); err != nil {
			return err
		}
	}
	src := pgx.CopyFromSlice(len(rows), func(i int) ([]any, error) {
		r := rows[i]
		if r.DataTier == 0 {
			r.DataTier = 1
		}
		return []any{
			r.ServerID, r.ObservedAt, r.PID, r.State,
			r.ActiveSeconds, r.XactSeconds, r.StateSeconds, r.WaitEventType, r.DataTier,
		}, nil
	})
	_, err := s.pool.CopyFrom(ctx, pgx.Identifier{"connection_samples"}, connectionSamplesColumns, src)
	return err
}

// WriteBlockingEdges appends a batch via COPY, creating partitions first.
func (s *Stats) WriteBlockingEdges(ctx context.Context, rows []BlockingEdgeRow) error {
	if len(rows) == 0 {
		return nil
	}
	weeks := map[string]time.Time{}
	for i := range rows {
		weeks[blockingEdgesPartitionName(rows[i].ObservedAt)] = rows[i].ObservedAt
	}
	for _, ts := range weeks {
		if err := s.ensureConnWeeklyPartition(ctx, "blocking_edges", blockingEdgesPartitionName(ts), ts); err != nil {
			return err
		}
	}
	src := pgx.CopyFromSlice(len(rows), func(i int) ([]any, error) {
		r := rows[i]
		if r.DataTier == 0 {
			r.DataTier = 1
		}
		return []any{
			r.ServerID, r.ObservedAt, r.BlockedPID, r.BlockerPID, r.BlockedWaitSeconds, r.DataTier,
		}, nil
	})
	_, err := s.pool.CopyFrom(ctx, pgx.Identifier{"blocking_edges"}, blockingEdgesColumns, src)
	return err
}

// ensureConnWeeklyPartition creates the weekly partition `name` of `parent` for
// ts if absent. Idempotent. Shared by the two connections tables.
func (s *Stats) ensureConnWeeklyPartition(ctx context.Context, parent, name string, ts time.Time) error {
	from, to := isoWeekBounds(ts)
	_, err := s.pool.Exec(ctx, fmt.Sprintf(
		`CREATE TABLE IF NOT EXISTS %s PARTITION OF %s FOR VALUES FROM ('%s') TO ('%s')`,
		name, parent, from.Format("2006-01-02"), to.Format("2006-01-02"),
	))
	return err
}

func connectionSamplesPartitionName(ts time.Time) string {
	y, w := ts.UTC().ISOWeek()
	return fmt.Sprintf("connection_samples_%04d_%02d", y, w)
}

func blockingEdgesPartitionName(ts time.Time) string {
	y, w := ts.UTC().ISOWeek()
	return fmt.Sprintf("blocking_edges_%04d_%02d", y, w)
}

const connectionSamplesSelect = `SELECT server_id, observed_at, pid, state,
        active_seconds, xact_seconds, state_seconds, wait_event_type, data_tier
   FROM connection_samples`

// LatestConnectionSamples returns the most-recent observation batch (all rows
// sharing the max observed_at) for serverID at or before asOf. Served from the
// read replica. data_tier = 1 only (T1).
func (s *Stats) LatestConnectionSamples(ctx context.Context, serverID string, asOf time.Time) ([]ConnectionSampleRow, error) {
	rows, err := s.ro.Query(ctx,
		connectionSamplesSelect+`
		  WHERE server_id = $1 AND data_tier = 1
		    AND observed_at = (
		        SELECT max(observed_at) FROM connection_samples
		         WHERE server_id = $1 AND observed_at <= $2 AND data_tier = 1)
		  ORDER BY pid`,
		serverID, asOf,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
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

const blockingEdgesSelect = `SELECT server_id, observed_at, blocked_pid, blocker_pid, blocked_wait_seconds, data_tier
   FROM blocking_edges`

// LatestBlockingEdges returns the most-recent blocking batch for serverID at or
// before asOf. data_tier = 1 only (T1).
func (s *Stats) LatestBlockingEdges(ctx context.Context, serverID string, asOf time.Time) ([]BlockingEdgeRow, error) {
	rows, err := s.ro.Query(ctx,
		blockingEdgesSelect+`
		  WHERE server_id = $1 AND data_tier = 1
		    AND observed_at = (
		        SELECT max(observed_at) FROM blocking_edges
		         WHERE server_id = $1 AND observed_at <= $2 AND data_tier = 1)
		  ORDER BY blocked_pid, blocker_pid`,
		serverID, asOf,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
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
