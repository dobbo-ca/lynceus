package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SchemaObjects provides typed access to the schema_objects table.
//
// Upserts preserve first_seen_at — the load-bearing semantic of this
// feature. Downstream consumers (M3 Index Advisor, "schema changed"
// insights) rely on first_seen_at being stable across collector
// restarts.
type SchemaObjects struct{ pool *pgxpool.Pool }

// NewSchemaObjects returns a SchemaObjects bound to pool.
func NewSchemaObjects(pool *pgxpool.Pool) *SchemaObjects {
	return &SchemaObjects{pool: pool}
}

// SchemaObjectRow is one row to upsert. Kind mirrors proto ObjectKind's
// numeric value. The caller is responsible for the collector-side
// schema-name filter — by the time a row reaches this struct, its
// schema is already approved for transmission.
type SchemaObjectRow struct {
	ServerID    string
	Kind        int16
	FQN         string
	SchemaName  string // stored in the "schema" column (matches proto field name)
	ObjectName  string // stored in the "name" column (matches proto field name)
	SizeBytes   int64
	IsPartition bool
	ParentFQN   string
}

// SchemaObjectRecord is one row returned by ListByServer.
type SchemaObjectRecord struct {
	ServerID    string
	Kind        int16
	FQN         string
	SchemaName  string
	ObjectName  string
	SizeBytes   int64
	IsPartition bool
	ParentFQN   string
	FirstSeenAt time.Time
	LastSeenAt  time.Time
}

// UpsertSchemaObjects inserts or updates the given rows. On conflict
// the size, partition fields, and last_seen_at are refreshed but
// first_seen_at is NEVER overwritten — that is the stability guarantee.
// All inserts run in a single transaction.
func (s *SchemaObjects) UpsertSchemaObjects(ctx context.Context, rows []SchemaObjectRow) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	batch := &pgx.Batch{}
	for _, r := range rows {
		batch.Queue(
			`INSERT INTO schema_objects
			   (server_id, kind, fqn, schema, name,
			    size_bytes_latest, is_partition, parent_fqn,
			    first_seen_at, last_seen_at)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now(), now())
			 ON CONFLICT (server_id, kind, fqn) DO UPDATE SET
			   size_bytes_latest = EXCLUDED.size_bytes_latest,
			   is_partition      = EXCLUDED.is_partition,
			   parent_fqn        = EXCLUDED.parent_fqn,
			   last_seen_at      = now()
			 -- first_seen_at intentionally omitted: stable across upserts.
			`,
			r.ServerID, r.Kind, r.FQN, r.SchemaName, r.ObjectName,
			r.SizeBytes, r.IsPartition, r.ParentFQN,
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

// ListByServer returns every schema_objects row for the given server,
// ordered by (kind, fqn) for deterministic test assertions.
func (s *SchemaObjects) ListByServer(ctx context.Context, serverID string) ([]SchemaObjectRecord, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT server_id, kind, fqn, schema, name,
		        size_bytes_latest, is_partition, parent_fqn,
		        first_seen_at, last_seen_at
		   FROM schema_objects
		  WHERE server_id = $1
		  ORDER BY kind, fqn`,
		serverID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SchemaObjectRecord
	for rows.Next() {
		var r SchemaObjectRecord
		if err := rows.Scan(
			&r.ServerID, &r.Kind, &r.FQN, &r.SchemaName, &r.ObjectName,
			&r.SizeBytes, &r.IsPartition, &r.ParentFQN,
			&r.FirstSeenAt, &r.LastSeenAt,
		); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// FirstSeenAt returns the stored first_seen_at for a single object, or
// the zero Time if the object has never been seen. Used by the
// collector to stamp outgoing SchemaObject messages with a stable
// timestamp; if zero, the collector uses the snapshot's collected_at.
func (s *SchemaObjects) FirstSeenAt(ctx context.Context, serverID string, kind int16, fqn string) (time.Time, error) {
	var t time.Time
	err := s.pool.QueryRow(ctx,
		`SELECT first_seen_at FROM schema_objects
		  WHERE server_id = $1 AND kind = $2 AND fqn = $3`,
		serverID, kind, fqn,
	).Scan(&t)
	if err == pgx.ErrNoRows {
		return time.Time{}, nil
	}
	return t, err
}

// WriteSchemaObjects satisfies store.Stats for the (soon-removed) Postgres
// backend by delegating to the existing upsert. Deleted in Task 8.
func (s *pgxStats) WriteSchemaObjects(ctx context.Context, rows []SchemaObjectRow) error {
	return NewSchemaObjects(s.pool).UpsertSchemaObjects(ctx, rows)
}
