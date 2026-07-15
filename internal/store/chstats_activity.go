package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// chActivityBucketCols is the shared column order for activity_buckets writes,
// carried over from the removed Postgres backend. No reserved-word columns here.
const chActivityBucketCols = "server_id, database_name, state, wait_event_type, wait_event, " +
	"bucket_start, bucket_seconds, sample_count, count_sum, count_max, data_tier"

// WriteActivityBuckets batch-inserts connection-state histogram rows into
// activity_buckets. DataTier zero normalizes to 1 (T1), mirroring the pgx COPY
// path. Labels and aggregate counts only — never query text.
func (s *chStats) WriteActivityBuckets(ctx context.Context, rows []ActivityBucket) error {
	if len(rows) == 0 {
		return nil
	}
	batch, err := s.conn.PrepareBatch(ctx, "INSERT INTO activity_buckets ("+chActivityBucketCols+")")
	if err != nil {
		return err
	}
	for i := range rows {
		r := rows[i]
		if r.DataTier == 0 {
			r.DataTier = 1
		}
		if err := batch.Append(
			r.ServerID, r.Database, r.State, r.WaitEventType, r.WaitEvent,
			r.BucketStart, r.BucketSeconds, r.SampleCount, r.CountSum, r.CountMax, r.DataTier,
		); err != nil {
			_ = batch.Abort()
			return err
		}
	}
	return batch.Send()
}

// WaitEventHistogram aggregates activity_buckets for serverID in [since, until)
// into per-(wait_event_type, wait_event) totals, busiest first. T1 only.
// Active-on-CPU samples (empty wait labels) are preserved as their own row.
func (s *chStats) WaitEventHistogram(
	ctx context.Context, serverID string, since, until time.Time,
) ([]WaitEventCount, error) {
	rows, err := s.conn.Query(ctx,
		`SELECT wait_event_type, wait_event, SUM(count_sum) AS total, toInt64(COUNT(*)) AS buckets
		   FROM activity_buckets
		  WHERE server_id = ? AND bucket_start >= ? AND bucket_start < ? AND data_tier = 1
		  GROUP BY wait_event_type, wait_event
		  ORDER BY total DESC, wait_event_type, wait_event`,
		serverID, since, until,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []WaitEventCount
	for rows.Next() {
		var w WaitEventCount
		if err := rows.Scan(&w.WaitEventType, &w.WaitEvent, &w.Total, &w.Buckets); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// RecentServerIDs returns distinct server_ids that shipped any table_stats since
// `since`, T1 only. Reads table_stats (owned by the table_stats domain's CH
// migration); no activity table is involved.
func (s *chStats) RecentServerIDs(ctx context.Context, since time.Time) ([]string, error) {
	rows, err := s.conn.Query(ctx,
		`SELECT DISTINCT server_id FROM table_stats
		  WHERE collected_at >= ? AND data_tier = 1 ORDER BY server_id`,
		since,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ThroughputForServers sums calls + total_time_ms for the server_id set in
// [since, until), T1 only. Reads query_stats.
func (s *chStats) ThroughputForServers(
	ctx context.Context, serverIDs []string, since, until time.Time,
) (Throughput, error) {
	var t Throughput
	err := s.conn.QueryRow(ctx,
		`SELECT SUM(calls), SUM(total_time_ms)
		   FROM query_stats
		  WHERE server_id IN (?) AND collected_at >= ? AND collected_at < ? AND data_tier = 1`,
		serverIDs, since, until,
	).Scan(&t.Calls, &t.TotalTimeMs)
	return t, err
}

// TopQueriesForServers is TopQueriesByTotalTime scoped to a server_id set,
// ordered by total time descending. T1 only. Reads query_stats.
func (s *chStats) TopQueriesForServers(
	ctx context.Context, serverIDs []string, since, until time.Time, limit int,
) ([]TopQuery, error) {
	rows, err := s.conn.Query(ctx,
		`SELECT fingerprint, normalized_query, SUM(calls), SUM(total_time_ms)
		   FROM query_stats
		  WHERE server_id IN (?) AND collected_at >= ? AND collected_at < ? AND data_tier = 1
		  GROUP BY fingerprint, normalized_query
		  ORDER BY SUM(total_time_ms) DESC
		  LIMIT ?`,
		serverIDs, since, until, uint64(limit),
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []TopQuery
	for rows.Next() {
		var q TopQuery
		if err := rows.Scan(&q.Fingerprint, &q.NormalizedQuery, &q.Calls, &q.TotalTimeMs); err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

// QPSBucketsForServers returns hourly buckets of summed calls for the server_id
// set in [since, until), oldest first. T1 only. Reads query_stats.
func (s *chStats) QPSBucketsForServers(
	ctx context.Context, serverIDs []string, since, until time.Time,
) ([]QPSBucket, error) {
	rows, err := s.conn.Query(ctx,
		`SELECT toStartOfHour(collected_at) AS bucket, SUM(calls)
		   FROM query_stats
		  WHERE server_id IN (?) AND collected_at >= ? AND collected_at < ? AND data_tier = 1
		  GROUP BY bucket
		  ORDER BY bucket ASC`,
		serverIDs, since, until,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []QPSBucket
	for rows.Next() {
		var b QPSBucket
		if err := rows.Scan(&b.BucketStart, &b.Calls); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// ActivitySummaryForServers reads the latest active-connection peak and the top
// wait event for the server_id set in [since, until). T1 only. Reads
// activity_buckets.
func (s *chStats) ActivitySummaryForServers(
	ctx context.Context, serverIDs []string, since, until time.Time,
) (ActivitySummary, error) {
	var a ActivitySummary

	// Peak active connections in the most recent bucket within the window,
	// summed across the server set.
	if err := s.conn.QueryRow(ctx,
		`SELECT SUM(count_max)
		   FROM activity_buckets
		  WHERE server_id IN (?) AND state = 'active' AND data_tier = 1
		    AND bucket_start = (
		      SELECT max(bucket_start) FROM activity_buckets
		       WHERE server_id IN (?) AND bucket_start >= ? AND bucket_start < ?
		         AND data_tier = 1
		    )`,
		serverIDs, serverIDs, since, until,
	).Scan(&a.ActiveConns); err != nil {
		return a, err
	}

	// Dominant wait event over the whole window (most accumulated count).
	var wait string
	err := s.conn.QueryRow(ctx,
		`SELECT wait_event
		   FROM activity_buckets
		  WHERE server_id IN (?) AND wait_event_type <> '' AND data_tier = 1
		    AND bucket_start >= ? AND bucket_start < ?
		  GROUP BY wait_event
		  ORDER BY SUM(count_sum) DESC
		  LIMIT 1`,
		serverIDs, since, until,
	).Scan(&wait)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return a, err
	}
	a.TopWait = wait // "" when no rows (nothing waiting)
	return a, nil
}
