package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// InsightRow is one detected anti-pattern as stored in the stats DB. Every field
// is a structural identifier or an aggregate count (T1, literal-free) — it maps
// 1:1 to insight.Insight. DataTier zero is treated as 1 (T1) on insert.
type InsightRow struct {
	ServerID     string
	CapturedAt   time.Time
	Kind         string
	Severity     string
	Fingerprint  string
	Relation     string
	NodePath     string
	RowsReturned int64
	RowsScanned  int64
	Selectivity  float64
	Detail       string
	DataTier     int16
}

// insightsColumns is the COPY column order for WriteInsights.
var insightsColumns = []string{
	"server_id", "captured_at", "kind", "severity", "fingerprint",
	"relation", "node_path", "rows_returned", "rows_scanned",
	"selectivity", "detail", "data_tier",
}

// WriteInsights appends a batch of derived insights via COPY, creating any
// missing weekly partitions first. Mirrors WriteQueryPlans.
func (s *Stats) WriteInsights(ctx context.Context, rows []InsightRow) error {
	if len(rows) == 0 {
		return nil
	}
	weeks := map[string]time.Time{}
	for i := range rows {
		weeks[insightsPartitionName(rows[i].CapturedAt)] = rows[i].CapturedAt
	}
	for _, ts := range weeks {
		if err := s.EnsureInsightsWeeklyPartition(ctx, ts); err != nil {
			return err
		}
	}

	src := pgx.CopyFromSlice(len(rows), func(i int) ([]any, error) {
		r := rows[i]
		if r.DataTier == 0 {
			r.DataTier = 1
		}
		return []any{
			r.ServerID, r.CapturedAt, r.Kind, r.Severity, r.Fingerprint,
			r.Relation, r.NodePath, r.RowsReturned, r.RowsScanned,
			r.Selectivity, r.Detail, r.DataTier,
		}, nil
	})
	_, err := s.pool.CopyFrom(ctx, pgx.Identifier{"insights"}, insightsColumns, src)
	return err
}

// InsightCountForServers counts T1 insights for the given server_id set in
// [since, until). serverIDs is passed as a Postgres array (= ANY($1)).
func (s *Stats) InsightCountForServers(
	ctx context.Context, serverIDs []string, since, until time.Time,
) (int, error) {
	var n int
	err := s.ro.QueryRow(ctx,
		`SELECT count(*) FROM insights
		  WHERE server_id = ANY($1)
		    AND captured_at >= $2 AND captured_at < $3
		    AND data_tier = 1`,
		serverIDs, since, until,
	).Scan(&n)
	return n, err
}

// TopInsightsForServers returns up to limit T1 insights for the server_id set in
// [since, until), most recent first.
func (s *Stats) TopInsightsForServers(
	ctx context.Context, serverIDs []string, since, until time.Time, limit int,
) ([]InsightRow, error) {
	rows, err := s.ro.Query(ctx,
		`SELECT server_id, captured_at, kind, severity, fingerprint,
		        relation, node_path, rows_returned, rows_scanned,
		        selectivity, detail, data_tier
		   FROM insights
		  WHERE server_id = ANY($1)
		    AND captured_at >= $2 AND captured_at < $3
		    AND data_tier = 1
		  ORDER BY captured_at DESC
		  LIMIT $4`,
		serverIDs, since, until, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []InsightRow
	for rows.Next() {
		var r InsightRow
		if err := rows.Scan(
			&r.ServerID, &r.CapturedAt, &r.Kind, &r.Severity, &r.Fingerprint,
			&r.Relation, &r.NodePath, &r.RowsReturned, &r.RowsScanned,
			&r.Selectivity, &r.Detail, &r.DataTier,
		); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// EnsureInsightsWeeklyPartition creates the weekly partition for ts on insights
// if it does not already exist. Idempotent.
func (s *Stats) EnsureInsightsWeeklyPartition(ctx context.Context, ts time.Time) error {
	name := insightsPartitionName(ts)
	from, to := isoWeekBounds(ts)
	_, err := s.pool.Exec(ctx, fmt.Sprintf(
		`CREATE TABLE IF NOT EXISTS %s PARTITION OF insights
		 FOR VALUES FROM ('%s') TO ('%s')`,
		name,
		from.Format("2006-01-02"),
		to.Format("2006-01-02"),
	))
	return err
}

func insightsPartitionName(ts time.Time) string {
	y, w := ts.UTC().ISOWeek()
	return fmt.Sprintf("insights_%04d_%02d", y, w)
}
