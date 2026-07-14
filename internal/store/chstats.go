package store

import (
	"context"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

var _ Stats = (*chStats)(nil)

// chStats is the ClickHouse-backed Stats implementation. T1 rows live in the
// query_stats table; T2 (literal-bearing) rows live in the separate
// query_stats_t2 table (see migrations/clickhouse and design spec §4). The
// per-domain methods live in the chstats_<domain>.go files alongside this one.
type chStats struct {
	conn driver.Conn
}

// NewCHStats binds a chStats to an open native ClickHouse connection.
func NewCHStats(conn driver.Conn) *chStats { return &chStats{conn: conn} }

// chQueryStatsCols is the shared column order for query_stats / query_stats_t2
// writes and the T2 read. `rows` is backticked (it collides with ClickHouse's
// contextual keyword); no bind placeholder appears inside the backticks.
const chQueryStatsCols = "server_id, collected_at, fingerprint, normalized_query, data_tier, " +
	"calls, total_time_ms, mean_time_ms, `rows`, shared_blks_hit, shared_blks_read"

// WriteQueryStats routes rows by data tier: DataTier==2 -> query_stats_t2,
// everything else (0 normalized to 1) -> query_stats. Two batches, one call.
func (s *chStats) WriteQueryStats(ctx context.Context, rows []QueryStat) error {
	if len(rows) == 0 {
		return nil
	}
	var t1, t2 []QueryStat
	for i := range rows {
		r := rows[i]
		if r.DataTier == 0 {
			r.DataTier = 1
		}
		if r.DataTier == 2 {
			t2 = append(t2, r)
		} else {
			t1 = append(t1, r)
		}
	}
	if err := s.insertQueryStats(ctx, "query_stats", t1); err != nil {
		return err
	}
	return s.insertQueryStats(ctx, "query_stats_t2", t2)
}

func (s *chStats) insertQueryStats(ctx context.Context, table string, rows []QueryStat) error {
	if len(rows) == 0 {
		return nil
	}
	batch, err := s.conn.PrepareBatch(ctx, "INSERT INTO "+table+" ("+chQueryStatsCols+")")
	if err != nil {
		return err
	}
	for i := range rows {
		r := &rows[i]
		if err := batch.Append(
			r.ServerID, r.CollectedAt, r.Fingerprint, r.NormalizedQuery, r.DataTier,
			r.Calls, r.TotalTimeMs, r.MeanTimeMs, r.Rows, r.SharedBlksHit, r.SharedBlksRead,
		); err != nil {
			_ = batch.Abort()
			return err
		}
	}
	return batch.Send()
}

// TopQueriesByTotalTime returns up to limit T1 queries in [since, until)
// ordered by total time descending. Reads query_stats only.
func (s *chStats) TopQueriesByTotalTime(ctx context.Context, since, until time.Time, limit int) ([]TopQuery, error) {
	rows, err := s.conn.Query(ctx,
		`SELECT fingerprint, normalized_query, SUM(calls), SUM(total_time_ms)
		   FROM query_stats
		  WHERE collected_at >= ? AND collected_at < ? AND data_tier = 1
		  GROUP BY fingerprint, normalized_query
		  ORDER BY SUM(total_time_ms) DESC
		  LIMIT ?`,
		since, until, uint64(limit),
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

// ReadQueryStatsTier2 is the ONLY read of the literal-bearing T2 table. It is
// unguarded on purpose: the T2Reader gateway is its sole caller and enforces
// fast-reject + authz + audit-before-read. Reads query_stats_t2 only.
func (s *chStats) ReadQueryStatsTier2(ctx context.Context, serverID string, since, until time.Time, limit int) ([]QueryStat, error) {
	rows, err := s.conn.Query(ctx,
		`SELECT `+chQueryStatsCols+`
		   FROM query_stats_t2
		  WHERE server_id = ? AND collected_at >= ? AND collected_at < ?
		  ORDER BY collected_at DESC
		  LIMIT ?`,
		serverID, since, until, uint64(limit),
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []QueryStat
	for rows.Next() {
		var q QueryStat
		if err := rows.Scan(
			&q.ServerID, &q.CollectedAt, &q.Fingerprint, &q.NormalizedQuery, &q.DataTier,
			&q.Calls, &q.TotalTimeMs, &q.MeanTimeMs, &q.Rows, &q.SharedBlksHit, &q.SharedBlksRead,
		); err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}
