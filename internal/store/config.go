package store

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Config is typed access to the config/metadata database.
type Config struct{ pool *pgxpool.Pool }

// NewConfig returns a Config bound to pool.
func NewConfig(pool *pgxpool.Pool) *Config { return &Config{pool: pool} }

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

// AppendAudit records an entry in the audit log. Detail is JSON-encoded.
func (c *Config) AppendAudit(ctx context.Context, e AuditEntry) error {
	var detail []byte
	if e.Detail != nil {
		b, err := json.Marshal(e.Detail)
		if err != nil {
			return err
		}
		detail = b
	}
	_, err := c.pool.Exec(ctx,
		`INSERT INTO audit_log (actor, action, server_id, data_tier, detail)
		 VALUES ($1, $2, NULLIF($3, ''), NULLIF($4::SMALLINT, 0::SMALLINT), $5)`,
		e.Actor, e.Action, e.ServerID, e.DataTier, detail,
	)
	return err
}
