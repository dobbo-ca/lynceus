package store

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Stats is the reader/writer seam over the time-series stats database.
type Stats interface {
	WriteQueryStats(ctx context.Context, rows []QueryStat) error
	TopQueriesByTotalTime(ctx context.Context, since, until time.Time, limit int) ([]TopQuery, error)
	WaitEventHistogram(ctx context.Context, serverID string, since, until time.Time) ([]WaitEventCount, error)
	WriteActivityBuckets(ctx context.Context, rows []ActivityBucket) error
	RecentServerIDs(ctx context.Context, since time.Time) ([]string, error)
	ThroughputForServers(ctx context.Context, serverIDs []string, since, until time.Time) (Throughput, error)
	TopQueriesForServers(ctx context.Context, serverIDs []string, since, until time.Time, limit int) ([]TopQuery, error)
	QPSBucketsForServers(ctx context.Context, serverIDs []string, since, until time.Time) ([]QPSBucket, error)
	ActivitySummaryForServers(ctx context.Context, serverIDs []string, since, until time.Time) (ActivitySummary, error)
	WriteQueryPlans(ctx context.Context, rows []QueryPlanRow) error
	TopPlansByQuery(ctx context.Context, serverID, fingerprint string, since, until time.Time, limit int) ([]QueryPlanRow, error)
	ListPlanKeys(ctx context.Context, since, until time.Time, limit int) ([]PlanKey, error)
	ReadQueryStatsTier2(ctx context.Context, serverID string, since, until time.Time, limit int) ([]QueryStat, error)
	WriteInsights(ctx context.Context, rows []InsightRow) error
	InsightCountForServers(ctx context.Context, serverIDs []string, since, until time.Time) (int, error)
	TopInsightsForServers(ctx context.Context, serverIDs []string, since, until time.Time, limit int) ([]InsightRow, error)
	WriteTableStats(ctx context.Context, rows []TableStatRow) error
	LatestTableStats(ctx context.Context, serverID string, asOf time.Time) ([]TableStatRow, error)
	WriteIndexStats(ctx context.Context, rows []IndexStatRow) error
	LatestIndexStats(ctx context.Context, serverID string, asOf time.Time) ([]IndexStatRow, error)
	WriteFreezeAges(ctx context.Context, rows []FreezeAgeRow) error
	LatestFreezeAges(ctx context.Context, serverID string, asOf time.Time) ([]FreezeAgeRow, error)
	WriteXminHorizons(ctx context.Context, rows []XminHorizonRow) error
	LatestXminHorizon(ctx context.Context, serverID string, asOf time.Time) (XminHorizonRow, bool, error)
	WriteSettings(ctx context.Context, rows []SettingRow) error
	LatestSettings(ctx context.Context, serverID string, asOf time.Time) ([]SettingRow, error)
	WriteConnectionSamples(ctx context.Context, rows []ConnectionSampleRow) error
	WriteBlockingEdges(ctx context.Context, rows []BlockingEdgeRow) error
	LatestConnectionSamples(ctx context.Context, serverID string, asOf time.Time) ([]ConnectionSampleRow, error)
	LatestBlockingEdges(ctx context.Context, serverID string, asOf time.Time) ([]BlockingEdgeRow, error)
	WriteChecksResults(ctx context.Context, rows []ChecksResultRow) error
	LatestChecksResults(ctx context.Context, serverID string, since, until time.Time) ([]ChecksResultRow, error)
	SetMute(ctx context.Context, serverID, checkID, object string, until time.Time, reason string) error
	ClearMute(ctx context.Context, serverID, checkID, object string) error
	ListMutes(ctx context.Context, serverID string) ([]MuteRow, error)
	WriteLogEvents(ctx context.Context, rows []LogEventRow) error
	ParkDLQ(ctx context.Context, serverID, reason string, raw []byte) error
	WriteSchemaObjects(ctx context.Context, rows []SchemaObjectRow) error
}

var _ Stats = (*pgxStats)(nil)

// pgxStats is the pgxpool-backed Stats implementation.
type pgxStats struct {
	pool *pgxpool.Pool // primary (read-write): writes, DDL, migrations
	ro   *pgxpool.Pool // read replica; defaults to pool when not split
	// ensured is a per-process cache of weekly partition names already
	// created, so the hot write path skips the CREATE TABLE IF NOT EXISTS
	// round-trip. Both Ensure* funcs share it; keying by full name means
	// query_stats_* and activity_buckets_* never collide. Concurrency-safe
	// for the shared pool. DropPartitionsOlderThan evicts dropped names.
	ensured sync.Map
}

// NewStats returns a pgxStats bound to its primary pool. Standalone reads
// fall back to the primary until a replica is attached via WithReadPool.
func NewStats(pool *pgxpool.Pool) *pgxStats { return &pgxStats{pool: pool, ro: pool} }

// Pool returns the primary (read-write) pool. Used by the Checks
// scheduler to take pg advisory locks. Mirrors Config.Pool().
func (s *pgxStats) Pool() *pgxpool.Pool { return s.pool }

// WithReadPool attaches a read-replica pool used to serve standalone
// reads (TopQueriesByTotalTime). A nil ro is ignored. Returns the
// receiver for chaining.
func (s *pgxStats) WithReadPool(ro *pgxpool.Pool) *pgxStats {
	if ro != nil {
		s.ro = ro
	}
	return s
}

// QueryStat is one T1 row of per-fingerprint query statistics.
// DataTier zero is treated as 1 (T1) on insert — see package comment.
type QueryStat struct {
	ServerID        string
	CollectedAt     time.Time
	Fingerprint     string
	NormalizedQuery string
	DataTier        int16
	Calls           int64
	TotalTimeMs     float64
	MeanTimeMs      float64
	Rows            int64
	SharedBlksHit   int64
	SharedBlksRead  int64
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
func (s *pgxStats) WriteQueryStats(ctx context.Context, rows []QueryStat) error {
	if len(rows) == 0 {
		return nil
	}

	weeks := map[string]time.Time{}
	for i := range rows {
		r := &rows[i]
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
func (s *pgxStats) TopQueriesByTotalTime(ctx context.Context, since, until time.Time, limit int) ([]TopQuery, error) {
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
func (s *pgxStats) EnsureWeeklyPartition(ctx context.Context, ts time.Time) error {
	name := partitionName(ts)
	if _, ok := s.ensured.Load(name); ok {
		return nil
	}
	from, to := isoWeekBounds(ts)
	_, err := s.pool.Exec(ctx, fmt.Sprintf(
		`CREATE TABLE IF NOT EXISTS %s PARTITION OF query_stats
		 FOR VALUES FROM ('%s') TO ('%s')`,
		name,
		from.Format("2006-01-02"),
		to.Format("2006-01-02"),
	))
	if err != nil {
		return err
	}
	s.ensured.Store(name, struct{}{})
	return nil
}

// DropPartitionsOlderThan drops every weekly partition whose upper
// bound is at or before cutoff. Returns the number dropped.
func (s *pgxStats) DropPartitionsOlderThan(ctx context.Context, cutoff time.Time) (int, error) {
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
			s.ensured.Delete(p.name)
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
//
//	FOR VALUES FROM ('2026-05-25') TO ('2026-06-01')
//
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
func (s *pgxStats) WriteActivityBuckets(ctx context.Context, rows []ActivityBucket) error {
	if len(rows) == 0 {
		return nil
	}

	weeks := map[string]time.Time{}
	for i := range rows {
		r := &rows[i]
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
func (s *pgxStats) TopActivityBucketsByState(
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
func (s *pgxStats) WaitEventHistogram(
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
func (s *pgxStats) EnsureActivityWeeklyPartition(ctx context.Context, ts time.Time) error {
	name := activityPartitionName(ts)
	if _, ok := s.ensured.Load(name); ok {
		return nil
	}
	from, to := isoWeekBounds(ts)
	_, err := s.pool.Exec(ctx, fmt.Sprintf(
		`CREATE TABLE IF NOT EXISTS %s PARTITION OF activity_buckets
		 FOR VALUES FROM ('%s') TO ('%s')`,
		name,
		from.Format("2006-01-02"),
		to.Format("2006-01-02"),
	))
	if err != nil {
		return err
	}
	s.ensured.Store(name, struct{}{})
	return nil
}

func activityPartitionName(ts time.Time) string {
	y, w := ts.UTC().ISOWeek()
	return fmt.Sprintf("activity_buckets_%04d_%02d", y, w)
}
