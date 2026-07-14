package store

import (
	"context"
	"time"
)

// ParkDLQ appends one failed ingest frame to the ClickHouse dlq table. raw is
// the serialized Snapshot protobuf (T1, literal-free) stored in a binary-safe
// String column. received_at is stamped server-side. Append-only; there is no
// retry consumer today (the table is TTL-bounded).
func (s *chStats) ParkDLQ(ctx context.Context, serverID, reason string, raw []byte) error {
	batch, err := s.conn.PrepareBatch(ctx, "INSERT INTO dlq (received_at, server_id, reason, raw)")
	if err != nil {
		return err
	}
	if err := batch.Append(time.Now().UTC(), serverID, reason, raw); err != nil {
		_ = batch.Abort()
		return err
	}
	return batch.Send()
}
