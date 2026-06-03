package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Config is typed access to the config/metadata database.
type Config struct{ pool *pgxpool.Pool }

// NewConfig returns a Config bound to pool.
func NewConfig(pool *pgxpool.Pool) *Config { return &Config{pool: pool} }

// auditLockKey is the bigint advisory-lock key used to serialize all
// audit appenders across the cluster. Treat it as a pinned constant —
// changing it would let two concurrent appenders briefly race during
// rollout.
const auditLockKey int64 = 7426398501234567890

// AuditEntry is one append-only row of the audit log. ServerID may be
// empty (for organization-level events) and DataTier may be zero (for
// non-data-access events such as auth).
type AuditEntry struct {
	Actor    string
	Action   string
	ServerID string
	DataTier int16
	Detail   any
}

// AuditRecord is the persisted form returned to callers that need the
// assigned id and chain hashes (the audit-log viewer; tests).
type AuditRecord struct {
	ID       int64
	Actor    string
	Action   string
	ServerID string
	DataTier int16
	Detail   []byte // canonical JSON bytes as stored
	At       time.Time
	PrevHash []byte // 32 bytes
	RowHash  []byte // 32 bytes
}

// AppendAudit records an entry in the audit log. Detail is JSON-encoded
// and the row is chained to its predecessor via SHA-256. The transaction
// holds an advisory lock so concurrent appenders are serialized cluster-
// wide. The signature is preserved from M1 for backwards compatibility;
// callers needing the assigned id/hash use AppendAuditReturning instead.
func (c *Config) AppendAudit(ctx context.Context, e AuditEntry) error {
	_, err := c.AppendAuditReturning(ctx, e)
	return err
}

// AppendAuditReturning appends and returns the persisted record.
func (c *Config) AppendAuditReturning(ctx context.Context, e AuditEntry) (AuditRecord, error) {
	// Canonicalize the detail JSONB sub-document, if any.
	var detail []byte
	if e.Detail != nil {
		raw, err := json.Marshal(e.Detail)
		if err != nil {
			return AuditRecord{}, fmt.Errorf("marshal detail: %w", err)
		}
		canon, err := canonicalJSON(raw)
		if err != nil {
			return AuditRecord{}, err
		}
		detail = canon
	}

	tx, err := c.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return AuditRecord{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", auditLockKey); err != nil {
		return AuditRecord{}, fmt.Errorf("advisory lock: %w", err)
	}

	// Read the tail row's row_hash. nextval() gives us the id that the
	// pending INSERT will assign, while staying inside the lock.
	var prev []byte
	err = tx.QueryRow(ctx,
		`SELECT row_hash FROM audit_log ORDER BY id DESC LIMIT 1`,
	).Scan(&prev)
	if err == pgx.ErrNoRows {
		prev = make([]byte, 32) // genesis
	} else if err != nil {
		return AuditRecord{}, fmt.Errorf("read tail: %w", err)
	}

	var nextID int64
	if err := tx.QueryRow(ctx,
		`SELECT nextval(pg_get_serial_sequence('audit_log','id'))`,
	).Scan(&nextID); err != nil {
		return AuditRecord{}, fmt.Errorf("nextval: %w", err)
	}

	// Capture the row's at timestamp ourselves so the hash matches what
	// we write. We round to nanosecond precision (Postgres TIMESTAMPTZ
	// stores microseconds, so we truncate accordingly to keep the
	// verifier reproducible).
	at := time.Now().UTC().Truncate(time.Microsecond)

	rowHash := hashAuditRow(uint64(nextID), prev, e.Actor, e.Action, e.ServerID, e.DataTier, detail, at)

	if _, err := tx.Exec(ctx,
		`INSERT INTO audit_log
		   (id, actor, action, server_id, data_tier, detail, at, prev_hash, row_hash)
		 VALUES
		   ($1, $2, $3, NULLIF($4, ''), NULLIF($5::SMALLINT, 0::SMALLINT), $6, $7, $8, $9)`,
		nextID, e.Actor, e.Action, e.ServerID, e.DataTier, detail, at, prev, rowHash,
	); err != nil {
		return AuditRecord{}, fmt.Errorf("insert: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return AuditRecord{}, fmt.Errorf("commit: %w", err)
	}

	return AuditRecord{
		ID: nextID, Actor: e.Actor, Action: e.Action, ServerID: e.ServerID,
		DataTier: e.DataTier, Detail: detail, At: at,
		PrevHash: prev, RowHash: rowHash,
	}, nil
}
