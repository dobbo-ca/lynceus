package store

import (
	"context"
	"time"
)

// chChecksResultsCols is the shared column order for checks_results writes/reads.
const chChecksResultsCols = "server_id, evaluated_at, check_id, category, severity, " +
	"status, object, detail, muted, data_tier"

// WriteChecksResults bulk-inserts Checks engine results into checks_results.
// DataTier==0 is coerced to 1 (T1). The Postgres `muted` boolean is stored as a
// UInt8 (0/1). Empty input is a no-op.
func (s *chStats) WriteChecksResults(ctx context.Context, rows []ChecksResultRow) error {
	if len(rows) == 0 {
		return nil
	}
	batch, err := s.conn.PrepareBatch(ctx, "INSERT INTO checks_results ("+chChecksResultsCols+")")
	if err != nil {
		return err
	}
	for i := range rows {
		r := &rows[i]
		tier := r.DataTier
		if tier == 0 {
			tier = 1
		}
		var muted uint8
		if r.Muted {
			muted = 1
		}
		if err := batch.Append(
			r.ServerID, r.EvaluatedAt, r.CheckID, r.Category, r.Severity,
			r.Status, r.Object, r.Detail, muted, tier,
		); err != nil {
			_ = batch.Abort()
			return err
		}
	}
	return batch.Send()
}

// LatestChecksResults returns the most recent result per (check_id, object) for
// server in [since, until). T1 only. Ordered by severity, check_id, object —
// mirroring the Postgres implementation (tuple-IN against the per-key max).
func (s *chStats) LatestChecksResults(ctx context.Context, serverID string, since, until time.Time) ([]ChecksResultRow, error) {
	rows, err := s.conn.Query(ctx,
		`SELECT server_id, evaluated_at, check_id, category, severity, status, object, detail, muted, data_tier
		   FROM checks_results
		  WHERE server_id = ? AND evaluated_at >= ? AND evaluated_at < ? AND data_tier = 1
		    AND (check_id, object, evaluated_at) IN (
		        SELECT check_id, object, max(evaluated_at)
		          FROM checks_results
		         WHERE server_id = ? AND evaluated_at >= ? AND evaluated_at < ? AND data_tier = 1
		         GROUP BY check_id, object)
		  ORDER BY severity, check_id, object`,
		serverID, since, until, serverID, since, until,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []ChecksResultRow
	for rows.Next() {
		var (
			r     ChecksResultRow
			muted uint8
		)
		if err := rows.Scan(
			&r.ServerID, &r.EvaluatedAt, &r.CheckID, &r.Category, &r.Severity,
			&r.Status, &r.Object, &r.Detail, &muted, &r.DataTier,
		); err != nil {
			return nil, err
		}
		r.Muted = muted == 1
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetMute records (or replaces) a mute for (serverID, checkID, object).
// object="" mutes every object of the check on the server. ClickHouse has no
// UPDATE, so this appends a live version (deleted=0) with updated_at=now;
// ListMutes reads the latest version per key. See migration 0009 for the model.
func (s *chStats) SetMute(ctx context.Context, serverID, checkID, object string, until time.Time, reason string) error {
	return s.appendMute(ctx, serverID, checkID, object, until, reason, 0)
}

// ClearMute tombstones a mute by appending a deleted=1 version with a newer
// updated_at, so the latest version per key becomes the tombstone and ListMutes
// hides it.
func (s *chStats) ClearMute(ctx context.Context, serverID, checkID, object string) error {
	return s.appendMute(ctx, serverID, checkID, object, time.Time{}, "", 1)
}

// appendMute writes one version row into the check_mutes ReplacingMergeTree.
// updated_at is a Go-generated wall-clock instant (DateTime64(9)) so successive
// mutations of the same key order deterministically. For tombstones (deleted=1)
// muted_until is irrelevant (the row is filtered out by ListMutes); it is set to
// now to stay within the DateTime64 range rather than the Go zero time.
func (s *chStats) appendMute(ctx context.Context, serverID, checkID, object string, until time.Time, reason string, deleted uint8) error {
	now := time.Now().UTC()
	if until.IsZero() {
		until = now
	}
	batch, err := s.conn.PrepareBatch(ctx,
		"INSERT INTO check_mutes (server_id, check_id, object, muted_until, reason, deleted, updated_at)")
	if err != nil {
		return err
	}
	if err := batch.Append(serverID, checkID, object, until, reason, deleted, now); err != nil {
		_ = batch.Abort()
		return err
	}
	return batch.Send()
}

// ListMutes returns active (non-expired, non-tombstoned) mutes for server. It
// collapses the append-only version history to the latest version per
// (server_id, check_id, object) via argMax(updated_at), then keeps rows that are
// not tombstoned and whose muted_until is still in the future — mirroring the
// Postgres `muted_until > now()` filter.
func (s *chStats) ListMutes(ctx context.Context, serverID string) ([]MuteRow, error) {
	rows, err := s.conn.Query(ctx,
		`SELECT server_id, check_id, object, muted_until, reason
		   FROM (
		     SELECT server_id, check_id, object,
		            argMax(muted_until, updated_at) AS muted_until,
		            argMax(reason, updated_at)      AS reason,
		            argMax(deleted, updated_at)     AS deleted
		       FROM check_mutes
		      WHERE server_id = ?
		      GROUP BY server_id, check_id, object)
		  WHERE deleted = 0 AND muted_until > now64(3)
		  ORDER BY check_id, object`,
		serverID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

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
