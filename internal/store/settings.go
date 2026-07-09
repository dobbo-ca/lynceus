package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// SettingRow is one T1 row of a curated pg_settings tuning GUC. value is a
// bounded config string (number/bool/enum) — the collector allowlist, not this
// struct, is the redaction boundary. DataTier zero is coerced to 1 (T1) on
// insert. Mirrors FreezeAgeRow.
type SettingRow struct {
	ServerID       string
	CollectedAt    time.Time
	Name           string
	Value          string
	Unit           string
	Source         string
	PendingRestart bool

	DataTier int16 // 0 -> coerced to 1
}

// settingsColumns is the COPY column order for WriteSettings; it matches the
// 0014_settings.sql column order.
var settingsColumns = []string{
	"server_id", "collected_at", "name", "value", "unit", "source",
	"pending_restart", "data_tier",
}

// WriteSettings appends a batch of settings rows via the COPY protocol,
// creating any missing weekly partitions first. Empty input is a no-op.
// Mirrors WriteFreezeAges.
func (s *pgxStats) WriteSettings(ctx context.Context, rows []SettingRow) error {
	if len(rows) == 0 {
		return nil
	}

	weeks := map[string]time.Time{}
	for i := range rows {
		r := &rows[i]
		weeks[settingsPartitionName(r.CollectedAt)] = r.CollectedAt
	}
	for _, ts := range weeks {
		if err := s.EnsureSettingsWeeklyPartition(ctx, ts); err != nil {
			return err
		}
	}

	src := pgx.CopyFromSlice(len(rows), func(i int) ([]any, error) {
		r := rows[i]
		if r.DataTier == 0 {
			r.DataTier = 1
		}
		return []any{
			r.ServerID, r.CollectedAt, r.Name, r.Value, r.Unit, r.Source,
			r.PendingRestart, r.DataTier,
		}, nil
	})
	_, err := s.pool.CopyFrom(ctx, pgx.Identifier{"settings"}, settingsColumns, src)
	return err
}

// EnsureSettingsWeeklyPartition creates the weekly partition for ts on
// settings if it does not already exist. Idempotent.
func (s *pgxStats) EnsureSettingsWeeklyPartition(ctx context.Context, ts time.Time) error {
	name := settingsPartitionName(ts)
	from, to := isoWeekBounds(ts)
	_, err := s.pool.Exec(ctx, fmt.Sprintf(
		`CREATE TABLE IF NOT EXISTS %s PARTITION OF settings
		 FOR VALUES FROM ('%s') TO ('%s')`,
		name,
		from.Format("2006-01-02"),
		to.Format("2006-01-02"),
	))
	return err
}

func settingsPartitionName(ts time.Time) string {
	y, w := ts.UTC().ISOWeek()
	return fmt.Sprintf("settings_%04d_%02d", y, w)
}

// settingsSelect is the shared column projection for the read methods, in
// SettingRow scan order.
const settingsSelect = `SELECT server_id, collected_at, name, value, unit, source,
        pending_restart, data_tier
   FROM settings`

func scanSettingRows(rows pgx.Rows) ([]SettingRow, error) {
	defer rows.Close()
	var out []SettingRow
	for rows.Next() {
		var r SettingRow
		if err := rows.Scan(
			&r.ServerID, &r.CollectedAt, &r.Name, &r.Value, &r.Unit, &r.Source,
			&r.PendingRestart, &r.DataTier,
		); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// LatestSettings returns the most recent settings row per name for serverID at
// or before asOf. data_tier = 1 only (T1). Served from the read replica.
// Mirrors LatestFreezeAges.
func (s *pgxStats) LatestSettings(ctx context.Context, serverID string, asOf time.Time) ([]SettingRow, error) {
	rows, err := s.ro.Query(ctx,
		settingsSelect+`
		  WHERE server_id = $1 AND collected_at <= $2 AND data_tier = 1
		    AND (name, collected_at) IN (
		        SELECT name, max(collected_at) FROM settings
		         WHERE server_id = $1 AND collected_at <= $2 AND data_tier = 1
		         GROUP BY name)
		  ORDER BY name`,
		serverID, asOf,
	)
	if err != nil {
		return nil, err
	}
	return scanSettingRows(rows)
}
