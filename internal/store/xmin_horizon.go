package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// XminHorizonRow is one T1 row of the cluster-global oldest-xmin observation
// (ly-32k): the AGE (in transactions) of the oldest xid still pinned by some
// backend / replication slot / prepared xact, plus a fixed HolderKind label.
// Counts + a bounded label only — never a slot name or gid. DataTier zero is
// coerced to 1 (T1) on insert.
type XminHorizonRow struct {
	ServerID      string
	CollectedAt   time.Time
	OldestXminAge int64
	HolderKind    string // "backend" | "replication_slot" | "prepared_xact"

	DataTier int16 // 0 -> coerced to 1
}

// xminHorizonColumns is the COPY column order for WriteXminHorizons; it matches
// the 0013_xmin_horizon.sql column order.
var xminHorizonColumns = []string{
	"server_id", "collected_at", "oldest_xmin_age", "holder_kind", "data_tier",
}

// WriteXminHorizons appends a batch of xmin-horizon rows via the COPY protocol,
// creating any missing weekly partitions first. Empty input is a no-op.
// Mirrors WriteFreezeAges.
func (s *pgxStats) WriteXminHorizons(ctx context.Context, rows []XminHorizonRow) error {
	if len(rows) == 0 {
		return nil
	}

	weeks := map[string]time.Time{}
	for i := range rows {
		r := &rows[i]
		weeks[xminHorizonPartitionName(r.CollectedAt)] = r.CollectedAt
	}
	for _, ts := range weeks {
		if err := s.EnsureXminHorizonWeeklyPartition(ctx, ts); err != nil {
			return err
		}
	}

	src := pgx.CopyFromSlice(len(rows), func(i int) ([]any, error) {
		r := rows[i]
		if r.DataTier == 0 {
			r.DataTier = 1
		}
		return []any{
			r.ServerID, r.CollectedAt, r.OldestXminAge, r.HolderKind, r.DataTier,
		}, nil
	})
	_, err := s.pool.CopyFrom(ctx, pgx.Identifier{"xmin_horizon"}, xminHorizonColumns, src)
	return err
}

// EnsureXminHorizonWeeklyPartition creates the weekly partition for ts on
// xmin_horizon if it does not already exist. Idempotent.
func (s *pgxStats) EnsureXminHorizonWeeklyPartition(ctx context.Context, ts time.Time) error {
	name := xminHorizonPartitionName(ts)
	from, to := isoWeekBounds(ts)
	_, err := s.pool.Exec(ctx, fmt.Sprintf(
		`CREATE TABLE IF NOT EXISTS %s PARTITION OF xmin_horizon
		 FOR VALUES FROM ('%s') TO ('%s')`,
		name,
		from.Format("2006-01-02"),
		to.Format("2006-01-02"),
	))
	return err
}

func xminHorizonPartitionName(ts time.Time) string {
	y, w := ts.UTC().ISOWeek()
	return fmt.Sprintf("xmin_horizon_%04d_%02d", y, w)
}

// LatestXminHorizon returns the single most recent xmin_horizon row for
// serverID at or before asOf. data_tier = 1 only (T1). The bool is false when
// no row exists. Served from the read replica. Cluster-global — one row, no fqn.
func (s *pgxStats) LatestXminHorizon(ctx context.Context, serverID string, asOf time.Time) (XminHorizonRow, bool, error) {
	var r XminHorizonRow
	err := s.ro.QueryRow(ctx,
		`SELECT server_id, collected_at, oldest_xmin_age, holder_kind, data_tier
		   FROM xmin_horizon
		  WHERE server_id = $1 AND collected_at <= $2 AND data_tier = 1
		  ORDER BY collected_at DESC LIMIT 1`,
		serverID, asOf,
	).Scan(&r.ServerID, &r.CollectedAt, &r.OldestXminAge, &r.HolderKind, &r.DataTier)
	if errors.Is(err, pgx.ErrNoRows) {
		return XminHorizonRow{}, false, nil
	}
	if err != nil {
		return XminHorizonRow{}, false, err
	}
	return r, true, nil
}
