package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Stats is typed access to the time-series stats database.
//
// The underlying query_stats table is range-partitioned by week.
// Stats.WriteQueryStats transparently creates the necessary weekly
// partitions before insert, so the caller never has to.
type Stats struct{ pool *pgxpool.Pool }

// NewStats returns a Stats bound to pool.
func NewStats(pool *pgxpool.Pool) *Stats { return &Stats{pool: pool} }

// QueryStat is one T1 row of per-fingerprint query statistics.
// DataTier zero is treated as 1 (T1) on insert — see package comment.
type QueryStat struct {
	ServerID         string
	CollectedAt      time.Time
	Fingerprint      string
	NormalizedQuery  string
	DataTier         int16
	Calls            int64
	TotalTimeMs      float64
	MeanTimeMs       float64
	Rows             int64
	SharedBlksHit    int64
	SharedBlksRead   int64
}

// WriteQueryStats inserts a batch of rows, creating any missing weekly
// partitions first. All inserts run in one transaction.
func (s *Stats) WriteQueryStats(ctx context.Context, rows []QueryStat) error {
	if len(rows) == 0 {
		return nil
	}

	weeks := map[string]time.Time{}
	for _, r := range rows {
		weeks[partitionName(r.CollectedAt)] = r.CollectedAt
	}
	for _, ts := range weeks {
		if err := s.EnsureWeeklyPartition(ctx, ts); err != nil {
			return err
		}
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	batch := &pgx.Batch{}
	for _, r := range rows {
		if r.DataTier == 0 {
			r.DataTier = 1
		}
		batch.Queue(
			`INSERT INTO query_stats
			   (server_id, collected_at, fingerprint, normalized_query, data_tier,
			    calls, total_time_ms, mean_time_ms, rows, shared_blks_hit, shared_blks_read)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
			r.ServerID, r.CollectedAt, r.Fingerprint, r.NormalizedQuery, r.DataTier,
			r.Calls, r.TotalTimeMs, r.MeanTimeMs, r.Rows, r.SharedBlksHit, r.SharedBlksRead,
		)
	}
	br := tx.SendBatch(ctx, batch)
	for range rows {
		if _, err := br.Exec(); err != nil {
			_ = br.Close()
			return err
		}
	}
	if err := br.Close(); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// TopQuery is one row returned by TopQueriesByTotalTime.
type TopQuery struct {
	Fingerprint     string
	NormalizedQuery string
	Calls           int64
	TotalTimeMs     float64
}

// TopQueriesByTotalTime returns up to limit T1 queries in [since, until)
// ordered by total time descending.
func (s *Stats) TopQueriesByTotalTime(ctx context.Context, since, until time.Time, limit int) ([]TopQuery, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT fingerprint, normalized_query, SUM(calls), SUM(total_time_ms)
		   FROM query_stats
		  WHERE collected_at >= $1 AND collected_at < $2 AND data_tier = 1
		  GROUP BY fingerprint, normalized_query
		  ORDER BY SUM(total_time_ms) DESC
		  LIMIT $3`,
		since, until, limit,
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

// EnsureWeeklyPartition creates the partition for ts's ISO week if it
// does not already exist. Idempotent.
func (s *Stats) EnsureWeeklyPartition(ctx context.Context, ts time.Time) error {
	name := partitionName(ts)
	from, to := isoWeekBounds(ts)
	_, err := s.pool.Exec(ctx, fmt.Sprintf(
		`CREATE TABLE IF NOT EXISTS %s PARTITION OF query_stats
		 FOR VALUES FROM ('%s') TO ('%s')`,
		name,
		from.Format("2006-01-02"),
		to.Format("2006-01-02"),
	))
	return err
}

// DropPartitionsOlderThan drops every weekly partition whose upper
// bound is at or before cutoff. Returns the number dropped.
func (s *Stats) DropPartitionsOlderThan(ctx context.Context, cutoff time.Time) (int, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT inhrelid::regclass::text,
		        pg_get_expr(c.relpartbound, c.oid)
		   FROM pg_inherits
		   JOIN pg_class c ON c.oid = inhrelid
		  WHERE inhparent = 'query_stats'::regclass`,
	)
	if err != nil {
		return 0, err
	}
	type partInfo struct{ name, bound string }
	var parts []partInfo
	for rows.Next() {
		var p partInfo
		if err := rows.Scan(&p.name, &p.bound); err != nil {
			rows.Close()
			return 0, err
		}
		parts = append(parts, p)
	}
	rows.Close()

	dropped := 0
	for _, p := range parts {
		up := parsePartitionUpper(p.bound)
		if up.IsZero() {
			continue
		}
		if !up.After(cutoff) {
			if _, err := s.pool.Exec(ctx, "DROP TABLE "+p.name); err != nil {
				return dropped, err
			}
			dropped++
		}
	}
	return dropped, nil
}

func partitionName(ts time.Time) string {
	y, w := ts.UTC().ISOWeek()
	return fmt.Sprintf("query_stats_%04d_%02d", y, w)
}

// isoWeekBounds returns [Monday 00:00 UTC, next Monday 00:00 UTC) for
// the ISO week containing ts.
func isoWeekBounds(ts time.Time) (time.Time, time.Time) {
	t := ts.UTC()
	wd := int(t.Weekday())
	if wd == 0 {
		wd = 7
	}
	monday := time.Date(t.Year(), t.Month(), t.Day()-(wd-1), 0, 0, 0, 0, time.UTC)
	return monday, monday.Add(7 * 24 * time.Hour)
}

// parsePartitionUpper extracts the upper-bound date from a partition
// bound expression such as
//   FOR VALUES FROM ('2026-05-25') TO ('2026-06-01')
// Returns the zero Time if it can't parse.
func parsePartitionUpper(bound string) time.Time {
	const marker = "TO ('"
	i := strings.LastIndex(bound, marker)
	if i < 0 {
		return time.Time{}
	}
	rest := bound[i+len(marker):]
	j := strings.Index(rest, "'")
	if j < 0 {
		return time.Time{}
	}
	raw := rest[:j]
	if t, err := time.Parse("2006-01-02", raw); err == nil {
		return t
	}
	if t, err := time.Parse("2006-01-02 15:04:05-07", raw); err == nil {
		return t
	}
	return time.Time{}
}
