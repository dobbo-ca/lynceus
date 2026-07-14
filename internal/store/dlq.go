package store

import "context"

// ParkDLQ appends one failed ingest frame to the Postgres dlq table
// (migrations/stats/0002_dlq.sql). An empty serverID is stored as NULL. raw is
// the serialized Snapshot protobuf (T1, literal-free).
func (s *pgxStats) ParkDLQ(ctx context.Context, serverID, reason string, raw []byte) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO dlq (server_id, reason, raw) VALUES (NULLIF($1, ''), $2, $3)`,
		serverID, reason, raw,
	)
	return err
}
