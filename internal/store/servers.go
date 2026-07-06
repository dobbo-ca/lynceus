package store

import (
	"context"
	"time"
)

// RecentServerIDs returns distinct server_ids that have shipped any
// table_stats since `since`. Used by the Checks scheduler to enumerate
// targets. T1 only.
func (s *pgxStats) RecentServerIDs(ctx context.Context, since time.Time) ([]string, error) {
	rows, err := s.ro.Query(ctx,
		`SELECT DISTINCT server_id FROM table_stats
		  WHERE collected_at >= $1 AND data_tier = 1 ORDER BY server_id`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
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
