package store

import (
	"context"
	"time"
)

// schemaObjectCHColumns is the INSERT column order for the ClickHouse
// schema_objects table (migrations/clickhouse/0012_schema_objects.sql).
const schemaObjectCHColumns = "server_id, kind, fqn, schema, name, size_bytes, " +
	"is_partition, parent_fqn, data_tier, first_seen_at, last_seen_at"

// WriteSchemaObjects appends the current-state inventory into the
// AggregatingMergeTree schema_objects table. first_seen_at and last_seen_at are
// stamped to the write time; min/max collapse them across re-observations so
// first_seen stays stable (mirrors the pgxStats now()-stamped upsert). Values
// are raw scalars — SimpleAggregateFunction columns accept them on INSERT.
func (s *chStats) WriteSchemaObjects(ctx context.Context, rows []SchemaObjectRow) error {
	if len(rows) == 0 {
		return nil
	}
	now := time.Now().UTC()
	batch, err := s.conn.PrepareBatch(ctx, "INSERT INTO schema_objects ("+schemaObjectCHColumns+")")
	if err != nil {
		return err
	}
	for i := range rows {
		r := rows[i]
		if err := batch.Append(
			r.ServerID, r.Kind, r.FQN, r.SchemaName, r.ObjectName, r.SizeBytes,
			chTableIndexBool(r.IsPartition), r.ParentFQN, int16(1), now, now,
		); err != nil {
			_ = batch.Abort()
			return err
		}
	}
	return batch.Send()
}
