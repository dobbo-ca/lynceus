package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Throughput is the aggregate query volume for a server set over a window.
type Throughput struct {
	Calls       int64
	TotalTimeMs float64
}

// QPSBucket is the summed calls for a server set in one hourly time bucket.
type QPSBucket struct {
	BucketStart time.Time
	Calls       int64
}

// ThroughputForServers sums calls + total_time_ms for the server_id set in
// [since, until). Used to derive combined q/s and call-weighted avg latency.
func (s *pgxStats) ThroughputForServers(
	ctx context.Context, serverIDs []string, since, until time.Time,
) (Throughput, error) {
	var t Throughput
	err := s.ro.QueryRow(ctx,
		`SELECT COALESCE(SUM(calls), 0), COALESCE(SUM(total_time_ms), 0)
		   FROM query_stats
		  WHERE server_id = ANY($1)
		    AND collected_at >= $2 AND collected_at < $3
		    AND data_tier = 1`,
		serverIDs, since, until,
	).Scan(&t.Calls, &t.TotalTimeMs)
	return t, err
}

// TopQueriesForServers is TopQueriesByTotalTime scoped to a server_id set —
// the per-cluster variant. Ordered by total time descending.
func (s *pgxStats) TopQueriesForServers(
	ctx context.Context, serverIDs []string, since, until time.Time, limit int,
) ([]TopQuery, error) {
	rows, err := s.ro.Query(ctx,
		`SELECT fingerprint, normalized_query, SUM(calls), SUM(total_time_ms)
		   FROM query_stats
		  WHERE server_id = ANY($1)
		    AND collected_at >= $2 AND collected_at < $3
		    AND data_tier = 1
		  GROUP BY fingerprint, normalized_query
		  ORDER BY SUM(total_time_ms) DESC
		  LIMIT $4`,
		serverIDs, since, until, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

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
// set in [since, until), oldest first — the data behind a q/s sparkline.
func (s *pgxStats) QPSBucketsForServers(
	ctx context.Context, serverIDs []string, since, until time.Time,
) ([]QPSBucket, error) {
	rows, err := s.ro.Query(ctx,
		`SELECT date_trunc('hour', collected_at) AS bucket, SUM(calls)
		   FROM query_stats
		  WHERE server_id = ANY($1)
		    AND collected_at >= $2 AND collected_at < $3
		    AND data_tier = 1
		  GROUP BY bucket
		  ORDER BY bucket ASC`,
		serverIDs, since, until,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

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

// ActivitySummary is the connection-state snapshot for a server set: peak active
// connections in the most recent bucket and the dominant wait event over the
// window.
type ActivitySummary struct {
	ActiveConns int64
	TopWait     string // "" if nothing was waiting
}

// ActivitySummaryForServers reads the latest active-connection peak and the
// top wait event for the server_id set in [since, until).
func (s *pgxStats) ActivitySummaryForServers(
	ctx context.Context, serverIDs []string, since, until time.Time,
) (ActivitySummary, error) {
	var a ActivitySummary

	// Peak active connections in the most recent bucket within the window,
	// summed across the server set.
	if err := s.ro.QueryRow(ctx,
		`SELECT COALESCE(SUM(count_max), 0)
		   FROM activity_buckets
		  WHERE server_id = ANY($1) AND state = 'active' AND data_tier = 1
		    AND bucket_start = (
		      SELECT max(bucket_start) FROM activity_buckets
		       WHERE server_id = ANY($1)
		         AND bucket_start >= $2 AND bucket_start < $3
		         AND data_tier = 1
		    )`,
		serverIDs, since, until,
	).Scan(&a.ActiveConns); err != nil {
		return a, err
	}

	// Dominant wait event over the whole window (most accumulated count).
	var wait string
	err := s.ro.QueryRow(ctx,
		`SELECT wait_event
		   FROM activity_buckets
		  WHERE server_id = ANY($1) AND wait_event_type <> '' AND data_tier = 1
		    AND bucket_start >= $2 AND bucket_start < $3
		  GROUP BY wait_event
		  ORDER BY SUM(count_sum) DESC
		  LIMIT 1`,
		serverIDs, since, until,
	).Scan(&wait)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return a, err
	}
	a.TopWait = wait // "" when ErrNoRows (nothing waiting)
	return a, nil
}
