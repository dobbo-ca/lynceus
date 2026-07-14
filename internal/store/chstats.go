package store

import (
	"context"
	"errors"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

var _ Stats = (*chStats)(nil)

// errCHTodo marks a Stats method not yet ported to ClickHouse. The remaining
// methods land in ly-cwr.3 (chStats t3); until then they fail loudly rather
// than returning a misleading empty result.
var errCHTodo = errors.New("chStats: method not implemented yet (ly-cwr.3)")

// chStats is the ClickHouse-backed Stats implementation. T1 rows live in the
// query_stats table; T2 (literal-bearing) rows live in the separate
// query_stats_t2 table (see migrations/clickhouse and design spec §4).
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

// --- Not yet implemented (ly-cwr.3 / chStats t3) -------------------------
// Each stub mirrors a tested pgxStats method and is a mechanical CH-SQL
// translation; ported per domain group in ly-cwr.3.

func (s *chStats) WaitEventHistogram(ctx context.Context, serverID string, since, until time.Time) ([]WaitEventCount, error) {
	return nil, errCHTodo
}

func (s *chStats) WriteActivityBuckets(ctx context.Context, rows []ActivityBucket) error {
	return errCHTodo
}

func (s *chStats) RecentServerIDs(ctx context.Context, since time.Time) ([]string, error) {
	return nil, errCHTodo
}

func (s *chStats) ThroughputForServers(ctx context.Context, serverIDs []string, since, until time.Time) (Throughput, error) {
	return Throughput{}, errCHTodo
}

func (s *chStats) TopQueriesForServers(ctx context.Context, serverIDs []string, since, until time.Time, limit int) ([]TopQuery, error) {
	return nil, errCHTodo
}

func (s *chStats) QPSBucketsForServers(ctx context.Context, serverIDs []string, since, until time.Time) ([]QPSBucket, error) {
	return nil, errCHTodo
}

func (s *chStats) ActivitySummaryForServers(ctx context.Context, serverIDs []string, since, until time.Time) (ActivitySummary, error) {
	return ActivitySummary{}, errCHTodo
}

func (s *chStats) WriteQueryPlans(ctx context.Context, rows []QueryPlanRow) error {
	return errCHTodo
}

func (s *chStats) TopPlansByQuery(ctx context.Context, serverID, fingerprint string, since, until time.Time, limit int) ([]QueryPlanRow, error) {
	return nil, errCHTodo
}

func (s *chStats) ListPlanKeys(ctx context.Context, since, until time.Time, limit int) ([]PlanKey, error) {
	return nil, errCHTodo
}

func (s *chStats) WriteInsights(ctx context.Context, rows []InsightRow) error {
	return errCHTodo
}

func (s *chStats) InsightCountForServers(ctx context.Context, serverIDs []string, since, until time.Time) (int, error) {
	return 0, errCHTodo
}

func (s *chStats) TopInsightsForServers(ctx context.Context, serverIDs []string, since, until time.Time, limit int) ([]InsightRow, error) {
	return nil, errCHTodo
}

func (s *chStats) WriteTableStats(ctx context.Context, rows []TableStatRow) error {
	return errCHTodo
}

func (s *chStats) LatestTableStats(ctx context.Context, serverID string, asOf time.Time) ([]TableStatRow, error) {
	return nil, errCHTodo
}

func (s *chStats) WriteIndexStats(ctx context.Context, rows []IndexStatRow) error {
	return errCHTodo
}

func (s *chStats) LatestIndexStats(ctx context.Context, serverID string, asOf time.Time) ([]IndexStatRow, error) {
	return nil, errCHTodo
}

func (s *chStats) WriteFreezeAges(ctx context.Context, rows []FreezeAgeRow) error {
	return errCHTodo
}

func (s *chStats) LatestFreezeAges(ctx context.Context, serverID string, asOf time.Time) ([]FreezeAgeRow, error) {
	return nil, errCHTodo
}

func (s *chStats) WriteXminHorizons(ctx context.Context, rows []XminHorizonRow) error {
	return errCHTodo
}

func (s *chStats) LatestXminHorizon(ctx context.Context, serverID string, asOf time.Time) (XminHorizonRow, bool, error) {
	return XminHorizonRow{}, false, errCHTodo
}

func (s *chStats) WriteSettings(ctx context.Context, rows []SettingRow) error {
	return errCHTodo
}

func (s *chStats) LatestSettings(ctx context.Context, serverID string, asOf time.Time) ([]SettingRow, error) {
	return nil, errCHTodo
}

func (s *chStats) WriteConnectionSamples(ctx context.Context, rows []ConnectionSampleRow) error {
	return errCHTodo
}

func (s *chStats) WriteBlockingEdges(ctx context.Context, rows []BlockingEdgeRow) error {
	return errCHTodo
}

func (s *chStats) LatestConnectionSamples(ctx context.Context, serverID string, asOf time.Time) ([]ConnectionSampleRow, error) {
	return nil, errCHTodo
}

func (s *chStats) LatestBlockingEdges(ctx context.Context, serverID string, asOf time.Time) ([]BlockingEdgeRow, error) {
	return nil, errCHTodo
}

func (s *chStats) WriteChecksResults(ctx context.Context, rows []ChecksResultRow) error {
	return errCHTodo
}

func (s *chStats) LatestChecksResults(ctx context.Context, serverID string, since, until time.Time) ([]ChecksResultRow, error) {
	return nil, errCHTodo
}

func (s *chStats) SetMute(ctx context.Context, serverID, checkID, object string, until time.Time, reason string) error {
	return errCHTodo
}

func (s *chStats) ClearMute(ctx context.Context, serverID, checkID, object string) error {
	return errCHTodo
}

func (s *chStats) ListMutes(ctx context.Context, serverID string) ([]MuteRow, error) {
	return nil, errCHTodo
}

func (s *chStats) WriteLogEvents(ctx context.Context, rows []LogEventRow) error {
	return errCHTodo
}
