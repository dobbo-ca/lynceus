package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// ChecksResultRow is one persisted Checks engine result. DataTier zero is
// coerced to 1 (T1) on insert.
type ChecksResultRow struct {
	ServerID    string
	EvaluatedAt time.Time
	CheckID     string
	Category    string
	Severity    string
	Status      string
	Object      string
	Detail      string
	Muted       bool
	DataTier    int16
}

var checksResultsColumns = []string{
	"server_id", "evaluated_at", "check_id", "category", "severity",
	"status", "object", "detail", "muted", "data_tier",
}

// WriteChecksResults bulk-inserts results, creating weekly partitions as
// needed. Empty input is a no-op.
func (s *pgxStats) WriteChecksResults(ctx context.Context, rows []ChecksResultRow) error {
	if len(rows) == 0 {
		return nil
	}
	weeks := map[string]time.Time{}
	for i := range rows {
		r := &rows[i]
		weeks[checksResultsPartitionName(r.EvaluatedAt)] = r.EvaluatedAt
	}
	for _, ts := range weeks {
		if err := s.EnsureChecksResultsWeeklyPartition(ctx, ts); err != nil {
			return err
		}
	}
	src := pgx.CopyFromSlice(len(rows), func(i int) ([]any, error) {
		r := rows[i]
		if r.DataTier == 0 {
			r.DataTier = 1
		}
		return []any{
			r.ServerID, r.EvaluatedAt, r.CheckID, r.Category, r.Severity,
			r.Status, r.Object, r.Detail, r.Muted, r.DataTier,
		}, nil
	})
	_, err := s.pool.CopyFrom(ctx, pgx.Identifier{"checks_results"}, checksResultsColumns, src)
	return err
}

// EnsureChecksResultsWeeklyPartition creates the weekly partition for ts on
// checks_results if it does not already exist. Idempotent.
func (s *pgxStats) EnsureChecksResultsWeeklyPartition(ctx context.Context, ts time.Time) error {
	name := checksResultsPartitionName(ts)
	from, to := isoWeekBounds(ts)
	_, err := s.pool.Exec(ctx, fmt.Sprintf(
		`CREATE TABLE IF NOT EXISTS %s PARTITION OF checks_results
		 FOR VALUES FROM ('%s') TO ('%s')`,
		name,
		from.Format("2006-01-02"),
		to.Format("2006-01-02"),
	))
	return err
}

func checksResultsPartitionName(ts time.Time) string {
	y, w := ts.UTC().ISOWeek()
	return fmt.Sprintf("checks_results_%04d_%02d", y, w)
}

// LatestChecksResults returns the most recent result per (check_id, object)
// for server in [since, until). T1 only.
func (s *pgxStats) LatestChecksResults(ctx context.Context, serverID string, since, until time.Time) ([]ChecksResultRow, error) {
	rows, err := s.ro.Query(ctx,
		`SELECT server_id, evaluated_at, check_id, category, severity, status, object, detail, muted, data_tier
		   FROM checks_results
		  WHERE server_id = $1 AND evaluated_at >= $2 AND evaluated_at < $3 AND data_tier = 1
		    AND (check_id, object, evaluated_at) IN (
		        SELECT check_id, object, max(evaluated_at) FROM checks_results
		         WHERE server_id = $1 AND evaluated_at >= $2 AND evaluated_at < $3 AND data_tier = 1
		         GROUP BY check_id, object)
		  ORDER BY severity, check_id, object`,
		serverID, since, until)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ChecksResultRow
	for rows.Next() {
		var r ChecksResultRow
		if err := rows.Scan(&r.ServerID, &r.EvaluatedAt, &r.CheckID, &r.Category,
			&r.Severity, &r.Status, &r.Object, &r.Detail, &r.Muted, &r.DataTier); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// MuteRow is an operator suppression entry.
type MuteRow struct {
	ServerID   string
	CheckID    string
	Object     string
	MutedUntil time.Time
	Reason     string
}

// SetMute upserts a mute. object="" mutes every object of check on server.
func (s *pgxStats) SetMute(ctx context.Context, serverID, checkID, object string, until time.Time, reason string) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO check_mutes (server_id, check_id, object, muted_until, reason)
		 VALUES ($1,$2,$3,$4,$5)
		 ON CONFLICT (server_id, check_id, object)
		 DO UPDATE SET muted_until = EXCLUDED.muted_until, reason = EXCLUDED.reason`,
		serverID, checkID, object, until, reason)
	return err
}

// ClearMute deletes a mute.
func (s *pgxStats) ClearMute(ctx context.Context, serverID, checkID, object string) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM check_mutes WHERE server_id=$1 AND check_id=$2 AND object=$3`,
		serverID, checkID, object)
	return err
}

// ListMutes returns active (non-expired) mutes for server.
func (s *pgxStats) ListMutes(ctx context.Context, serverID string) ([]MuteRow, error) {
	rows, err := s.ro.Query(ctx,
		`SELECT server_id, check_id, object, muted_until, reason
		   FROM check_mutes WHERE server_id=$1 AND muted_until > now()`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MuteRow
	for rows.Next() {
		var m MuteRow
		if err := rows.Scan(&m.ServerID, &m.CheckID, &m.Object, &m.MutedUntil, &m.Reason); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
