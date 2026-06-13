package store

import (
	"context"
	"time"
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
func (s *Stats) ThroughputForServers(
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
func (s *Stats) TopQueriesForServers(
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
func (s *Stats) QPSBucketsForServers(
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
