package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// T2Reader is the enforceable gateway for reading T2 (literal-capable)
// data. It is the ONLY path that selects data_tier=2 from the stats
// store: it fast-rejects on servers.t2_enabled, authorizes via
// EffectiveCapability (config-DB rows only), appends a per-read audit row
// FIRST, and fails closed (no literal returned) if the audit write fails.
type T2Reader struct {
	cfg   Config
	stats Stats
}

// NewT2Reader binds the gateway to its config and stats seams.
func NewT2Reader(cfg Config, stats Stats) *T2Reader { return &T2Reader{cfg: cfg, stats: stats} }

// T2ReadRequest identifies one authorized T2 literal read. All fields are
// structural — none carries a literal value.
type T2ReadRequest struct {
	ServerID     string
	DatabaseName string
	Capability   string
	Actor        string
	Since        time.Time
	Until        time.Time
	Limit        int
}

// ReadT2QueryStats returns the data_tier=2 query_stats rows for the
// request, gated and audited. Ordering mirrors SetCapabilityPolicy's
// fail-closed pattern (capability_policy.go:54-91): the audit append
// precedes and gates the literal-returning SELECT. On any gate failure or
// audit-append failure it returns a non-nil error and no rows.
//
//nolint:gocritic // hugeParam: cold audited-read path; T2ReadRequest is a caller-owned value struct
func (r *T2Reader) ReadT2QueryStats(ctx context.Context, req T2ReadRequest) ([]QueryStat, error) {
	// 1. Cheap fast-reject on the per-stream boolean, before anything else.
	enabled, found, err := r.cfg.ServerT2Enabled(ctx, req.ServerID)
	if err != nil {
		return nil, fmt.Errorf("t2 gate: %w", err)
	}
	if !found || !enabled {
		return nil, fmt.Errorf("t2 read denied: t2_enabled is false for server %s", req.ServerID)
	}

	// 2. Authorize against config-DB capability rows.
	capEnabled, _, capFound, err := r.cfg.EffectiveCapability(ctx, req.ServerID, req.DatabaseName, req.Capability)
	if err != nil {
		return nil, fmt.Errorf("t2 authz: %w", err)
	}
	if !capFound || !capEnabled {
		return nil, fmt.Errorf("t2 read denied: capability %q not authorized", req.Capability)
	}

	// 3. Audit FIRST — fail closed. Detail carries structural keys only.
	if _, err := r.cfg.AppendAuditReturning(ctx, AuditEntry{
		Actor:    req.Actor,
		Action:   "read",
		ServerID: req.ServerID,
		DataTier: 2,
		Detail: map[string]any{
			"database_name": dbNameDetail(req.DatabaseName),
			"capability":    req.Capability,
			"since":         req.Since.UTC().Format(time.RFC3339),
			"until":         req.Until.UTC().Format(time.RFC3339),
			"limit":         req.Limit,
		},
	}); err != nil {
		return nil, fmt.Errorf("t2 audit: %w", err)
	}

	// 4. Only now may the literal-returning SELECT run.
	return r.stats.ReadQueryStatsTier2(ctx, req.ServerID, req.Since, req.Until, req.Limit)
}

// ServerT2Enabled reports whether the servers row for serverID has
// t2_enabled=true. found is false when no such server row exists. It is
// the cheap per-stream fast-reject gate read by the T2 gateway.
func (c *pgxConfig) ServerT2Enabled(ctx context.Context, serverID string) (enabled, found bool, err error) {
	row := c.ro.QueryRow(ctx, `SELECT t2_enabled FROM servers WHERE id = $1`, serverID)
	switch scanErr := row.Scan(&enabled); {
	case scanErr == pgx.ErrNoRows:
		return false, false, nil
	case scanErr != nil:
		return false, false, fmt.Errorf("server t2_enabled: %w", scanErr)
	default:
		return enabled, true, nil
	}
}
