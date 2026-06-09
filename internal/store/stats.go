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
type Stats struct {
	pool *pgxpool.Pool // primary (read-write): writes, DDL, migrations
	ro   *pgxpool.Pool // read replica; defaults to pool when not split
}

// NewStats returns a Stats bound to its primary pool. Standalone reads
// fall back to the primary until a replica is attached via WithReadPool.
func NewStats(pool *pgxpool.Pool) *Stats { return &Stats{pool: pool, ro: pool} }

// Pool returns the primary (read-write) pool. Used by the Checks
// scheduler to take pg advisory locks. Mirrors Config.Pool().
func (s *Stats) Pool() *pgxpool.Pool { return s.pool }

// WithReadPool attaches a read-replica pool used to serve standalone
// reads (TopQueriesByTotalTime). A nil ro is ignored. Returns the
// receiver for chaining.
func (s *Stats) WithReadPool(ro *pgxpool.Pool) *Stats {
	if ro != nil {
		s.ro = ro
	}
	return s
}

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

// queryStatsColumns is the column order written by WriteQueryStats,
// shared by the COPY stream below.
var queryStatsColumns = []string{
	"server_id", "collected_at", "fingerprint", "normalized_query", "data_tier",
	"calls", "total_time_ms", "mean_time_ms", "rows", "shared_blks_hit", "shared_blks_read",
}

// WriteQueryStats appends a batch of rows using the COPY protocol,
// creating any missing weekly partitions first. COPY routes each row to
// its weekly partition and is markedly lighter on the storage database
// than per-row INSERTs (one round-trip, no per-row parse/plan).
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

	src := pgx.CopyFromSlice(len(rows), func(i int) ([]any, error) {
		r := rows[i]
		if r.DataTier == 0 {
			r.DataTier = 1
		}
		return []any{
			r.ServerID, r.CollectedAt, r.Fingerprint, r.NormalizedQuery, r.DataTier,
			r.Calls, r.TotalTimeMs, r.MeanTimeMs, r.Rows, r.SharedBlksHit, r.SharedBlksRead,
		}, nil
	})
	_, err := s.pool.CopyFrom(ctx, pgx.Identifier{"query_stats"}, queryStatsColumns, src)
	return err
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
	rows, err := s.ro.Query(ctx,
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

// ActivityBucket is one row of per-bucket connection-state histogram
// data. Labels and aggregate counts only — see the package comment and
// docs/specs/2026-05-29-lynceus-design.md §2.
type ActivityBucket struct {
	ServerID      string
	Database      string
	State         string
	WaitEventType string
	WaitEvent     string
	BucketStart   time.Time
	BucketSeconds int32
	SampleCount   int32
	CountSum      int64
	CountMax      int64
	DataTier      int16
}

// activityBucketColumns is the COPY column order for WriteActivityBuckets.
var activityBucketColumns = []string{
	"server_id", "database_name", "state", "wait_event_type", "wait_event",
	"bucket_start", "bucket_seconds", "sample_count", "count_sum", "count_max", "data_tier",
}

// WriteActivityBuckets appends a batch of activity buckets via the COPY
// protocol, creating any missing weekly partitions first. Mirrors
// WriteQueryStats: COPY routes each row to its weekly partition and is
// markedly lighter on the storage DB than per-row INSERTs.
func (s *Stats) WriteActivityBuckets(ctx context.Context, rows []ActivityBucket) error {
	if len(rows) == 0 {
		return nil
	}

	weeks := map[string]time.Time{}
	for _, r := range rows {
		weeks[activityPartitionName(r.BucketStart)] = r.BucketStart
	}
	for _, ts := range weeks {
		if err := s.EnsureActivityWeeklyPartition(ctx, ts); err != nil {
			return err
		}
	}

	src := pgx.CopyFromSlice(len(rows), func(i int) ([]any, error) {
		r := rows[i]
		if r.DataTier == 0 {
			r.DataTier = 1
		}
		return []any{
			r.ServerID, r.Database, r.State, r.WaitEventType, r.WaitEvent,
			r.BucketStart, r.BucketSeconds, r.SampleCount, r.CountSum, r.CountMax, r.DataTier,
		}, nil
	})
	_, err := s.pool.CopyFrom(ctx, pgx.Identifier{"activity_buckets"}, activityBucketColumns, src)
	return err
}

// TopActivityBucketsByState returns up to limit buckets in [since, until)
// for serverID, ordered by bucket_start ascending then state. data_tier =
// 1 only (T1).
func (s *Stats) TopActivityBucketsByState(
	ctx context.Context, serverID string, since, until time.Time, limit int,
) ([]ActivityBucket, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT server_id, database_name, state, wait_event_type, wait_event,
		        bucket_start, bucket_seconds, sample_count, count_sum, count_max, data_tier
		   FROM activity_buckets
		  WHERE server_id = $1
		    AND bucket_start >= $2 AND bucket_start < $3
		    AND data_tier = 1
		  ORDER BY bucket_start ASC, state ASC
		  LIMIT $4`,
		serverID, since, until, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ActivityBucket
	for rows.Next() {
		var b ActivityBucket
		if err := rows.Scan(
			&b.ServerID, &b.Database, &b.State, &b.WaitEventType, &b.WaitEvent,
			&b.BucketStart, &b.BucketSeconds, &b.SampleCount, &b.CountSum, &b.CountMax, &b.DataTier,
		); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// WaitEventCount is one aggregated wait-event class over a time window: a
// (type, event) label pair and the summed sample count. Empty type/event means
// the backend was active on CPU (no wait). T1 — labels + a count only.
type WaitEventCount struct {
	WaitEventType string
	WaitEvent     string
	Total         int64
	Buckets       int64 // how many buckets contributed (sampling depth)
}

// WaitEventHistogram aggregates activity_buckets for serverID in [since, until)
// into per-(wait_event_type, wait_event) totals, busiest first. data_tier = 1
// only (T1). Active-on-CPU samples (empty wait labels) are preserved as their
// own row, not dropped.
func (s *Stats) WaitEventHistogram(
	ctx context.Context, serverID string, since, until time.Time,
) ([]WaitEventCount, error) {
	rows, err := s.ro.Query(ctx,
		`SELECT wait_event_type, wait_event, SUM(count_sum)::bigint AS total, COUNT(*)::bigint AS buckets
		   FROM activity_buckets
		  WHERE server_id = $1
		    AND bucket_start >= $2 AND bucket_start < $3
		    AND data_tier = 1
		  GROUP BY wait_event_type, wait_event
		  ORDER BY total DESC, wait_event_type, wait_event`,
		serverID, since, until,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

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

// EnsureActivityWeeklyPartition creates the weekly partition for ts on
// activity_buckets if it does not already exist. Idempotent.
func (s *Stats) EnsureActivityWeeklyPartition(ctx context.Context, ts time.Time) error {
	name := activityPartitionName(ts)
	from, to := isoWeekBounds(ts)
	_, err := s.pool.Exec(ctx, fmt.Sprintf(
		`CREATE TABLE IF NOT EXISTS %s PARTITION OF activity_buckets
		 FOR VALUES FROM ('%s') TO ('%s')`,
		name,
		from.Format("2006-01-02"),
		to.Format("2006-01-02"),
	))
	return err
}

func activityPartitionName(ts time.Time) string {
	y, w := ts.UTC().ISOWeek()
	return fmt.Sprintf("activity_buckets_%04d_%02d", y, w)
}
