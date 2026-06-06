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

// auditLockKey is the bigint advisory-lock key used to serialize all audit
// appenders across the cluster. Treat it as a pinned constant — changing
// it would let two concurrent appenders briefly race during rollout.
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

	// Serialize all appenders cluster-wide; released on COMMIT/ROLLBACK.
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", auditLockKey); err != nil {
		return AuditRecord{}, fmt.Errorf("advisory lock: %w", err)
	}

	// Read the tail row's row_hash (genesis if the table is empty).
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

	// Capture the at timestamp ourselves so the hash matches what we
	// write. Postgres TIMESTAMPTZ stores microseconds, so truncate to
	// keep the verifier reproducible.
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

// VerifyChain walks audit_log rows ordered by id ASC and recomputes each
// row's hash chain. It returns (-1, "", nil) when the chain is intact.
// Otherwise it returns the 0-based ordinal of the first inconsistent row
// in the walk along with a short reason.
//
// since and until bound the time window (inclusive); pass zero values to
// scan the whole table. A windowed walk validates only that the in-window
// rows chain to each other; to validate the anchor to genesis, scan from
// the start (since == time.Time{}).
func (c *Config) VerifyChain(ctx context.Context, since, until time.Time) (int, string, error) {
	var (
		q    string
		args []any
	)
	switch {
	case since.IsZero() && until.IsZero():
		q = `SELECT id, actor, action, COALESCE(server_id,''), COALESCE(data_tier,0),
		            COALESCE(detail::text, ''), at, prev_hash, row_hash
		       FROM audit_log ORDER BY id ASC`
	default:
		q = `SELECT id, actor, action, COALESCE(server_id,''), COALESCE(data_tier,0),
		            COALESCE(detail::text, ''), at, prev_hash, row_hash
		       FROM audit_log
		      WHERE at >= $1 AND at <= $2
		      ORDER BY id ASC`
		if since.IsZero() {
			since = time.Unix(0, 0)
		}
		if until.IsZero() {
			until = time.Now().Add(24 * time.Hour)
		}
		args = []any{since, until}
	}

	rows, err := c.pool.Query(ctx, q, args...)
	if err != nil {
		return 0, "", fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	expectedPrev := make([]byte, 32) // genesis
	var (
		idx         int
		lastID      int64
		walkStarted bool
	)
	for rows.Next() {
		var (
			id      int64
			actor   string
			action  string
			srvID   string
			tier    int16
			detail  string
			at      time.Time
			prev    []byte
			rowHash []byte
		)
		if err := rows.Scan(&id, &actor, &action, &srvID, &tier, &detail, &at, &prev, &rowHash); err != nil {
			return idx, "scan", err
		}

		if walkStarted && id != lastID+1 {
			return idx, fmt.Sprintf("id gap: expected %d, got %d", lastID+1, id), nil
		}

		var detailBytes []byte
		if detail != "" {
			canon, err := canonicalJSON([]byte(detail))
			if err != nil {
				return idx, "detail not canonicalizable", err
			}
			detailBytes = canon
		}

		// On a windowed walk that does not start at id=1, seed expectedPrev
		// from the first row's own prev_hash (the link to the row before
		// the window cannot be validated — documented above).
		if !walkStarted && id != 1 {
			expectedPrev = prev
		}

		if !bytesEqual(prev, expectedPrev) {
			return idx, fmt.Sprintf("prev_hash mismatch at id=%d", id), nil
		}

		recomputed := hashAuditRow(uint64(id), prev, actor, action, srvID, tier, detailBytes, at.UTC().Truncate(time.Microsecond))
		if !bytesEqual(recomputed, rowHash) {
			return idx, fmt.Sprintf("row_hash mismatch at id=%d", id), nil
		}

		expectedPrev = rowHash
		lastID = id
		walkStarted = true
		idx++
	}
	if err := rows.Err(); err != nil {
		return idx, "rows.Err", err
	}
	return -1, "", nil
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
