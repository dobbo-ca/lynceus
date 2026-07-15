package store

import (
	"context"
	"strings"
	"time"
)

// insights (chStats) — ClickHouse port of the removed Postgres insights methods
// (insights.go). Single T1 table; every read filters data_tier = 1. Column
// order reuses insightsColumns so writes stay in lock-step with the pgx COPY.

// WriteInsights batch-inserts derived insights into the insights table.
// DataTier 0 is normalized to 1 (T1) on insert, mirroring the pgx impl.
func (s *chStats) WriteInsights(ctx context.Context, rows []InsightRow) error {
	if len(rows) == 0 {
		return nil
	}
	batch, err := s.conn.PrepareBatch(ctx, "INSERT INTO insights ("+strings.Join(insightsColumns, ", ")+")")
	if err != nil {
		return err
	}
	for i := range rows {
		r := rows[i]
		if r.DataTier == 0 {
			r.DataTier = 1
		}
		if err := batch.Append(
			r.ServerID, r.CapturedAt, r.Kind, r.Severity, r.Fingerprint,
			r.Relation, r.NodePath, r.RowsReturned, r.RowsScanned,
			r.Selectivity, r.Detail, r.DataTier,
		); err != nil {
			_ = batch.Abort()
			return err
		}
	}
	return batch.Send()
}

// InsightCountForServers counts T1 insights for the given server_id set in
// [since, until). Mirrors the pgx = ANY($1) query with IN (?).
func (s *chStats) InsightCountForServers(
	ctx context.Context, serverIDs []string, since, until time.Time,
) (int, error) {
	var n uint64
	if err := s.conn.QueryRow(ctx,
		`SELECT count() FROM insights
		  WHERE server_id IN (?)
		    AND captured_at >= ? AND captured_at < ?
		    AND data_tier = 1`,
		serverIDs, since, until,
	).Scan(&n); err != nil {
		return 0, err
	}
	return int(n), nil
}

// TopInsightsForServers returns up to limit T1 insights for the server_id set in
// [since, until), most recent first.
func (s *chStats) TopInsightsForServers(
	ctx context.Context, serverIDs []string, since, until time.Time, limit int,
) ([]InsightRow, error) {
	rows, err := s.conn.Query(ctx,
		`SELECT server_id, captured_at, kind, severity, fingerprint,
		        relation, node_path, rows_returned, rows_scanned,
		        selectivity, detail, data_tier
		   FROM insights
		  WHERE server_id IN (?)
		    AND captured_at >= ? AND captured_at < ?
		    AND data_tier = 1
		  ORDER BY captured_at DESC
		  LIMIT ?`,
		serverIDs, since, until, uint64(limit),
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

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
