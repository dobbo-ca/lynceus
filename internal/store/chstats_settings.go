package store

import (
	"context"
	"time"
)

// chSettingsCols is the shared column order for settings writes; it matches
// 0007_settings.sql (and the pgx settingsColumns order).
const chSettingsCols = "server_id, collected_at, name, value, unit, source, " +
	"pending_restart, data_tier"

// WriteSettings appends a batch of settings rows. DataTier zero is coerced to 1
// (settings are T1-only). pending_restart (bool) is stored as UInt8. Empty
// input is a no-op. Mirrors pgxStats.WriteSettings.
func (s *chStats) WriteSettings(ctx context.Context, rows []SettingRow) error {
	if len(rows) == 0 {
		return nil
	}
	batch, err := s.conn.PrepareBatch(ctx, "INSERT INTO settings ("+chSettingsCols+")")
	if err != nil {
		return err
	}
	for i := range rows {
		r := &rows[i]
		tier := r.DataTier
		if tier == 0 {
			tier = 1
		}
		var pending uint8
		if r.PendingRestart {
			pending = 1
		}
		if err := batch.Append(
			r.ServerID, r.CollectedAt, r.Name, r.Value, r.Unit, r.Source,
			pending, tier,
		); err != nil {
			_ = batch.Abort()
			return err
		}
	}
	return batch.Send()
}

// LatestSettings returns the most recent settings row per name for serverID at
// or before asOf, data_tier = 1 only, ordered by name. argMax(col, collected_at)
// picks each column from the row with the greatest collected_at per name — the
// idiomatic ClickHouse spelling of the pgx "(name, max(collected_at)) IN (…)"
// latest-as-of correlation. Mirrors pgxStats.LatestSettings.
func (s *chStats) LatestSettings(ctx context.Context, serverID string, asOf time.Time) ([]SettingRow, error) {
	rows, err := s.conn.Query(ctx,
		`SELECT server_id,
		        max(collected_at),
		        name,
		        argMax(value, collected_at),
		        argMax(unit, collected_at),
		        argMax(source, collected_at),
		        argMax(pending_restart, collected_at),
		        argMax(data_tier, collected_at)
		   FROM settings
		  WHERE server_id = ? AND collected_at <= ? AND data_tier = 1
		  GROUP BY server_id, name
		  ORDER BY name`,
		serverID, asOf,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []SettingRow
	for rows.Next() {
		var (
			r       SettingRow
			pending uint8
		)
		if err := rows.Scan(
			&r.ServerID, &r.CollectedAt, &r.Name, &r.Value, &r.Unit, &r.Source,
			&pending, &r.DataTier,
		); err != nil {
			return nil, err
		}
		r.PendingRestart = pending != 0
		out = append(out, r)
	}
	return out, rows.Err()
}
