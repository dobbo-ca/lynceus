package store

import (
	"context"
	"fmt"
	"time"
)

// CapabilityPolicy is one row of capability_policy: whether a capability
// is enabled for a server (DatabaseName == "") or for a specific database
// within that server (DatabaseName != ""). A row's last change is linked
// to its tamper-evident audit entry via AuditChainID.
type CapabilityPolicy struct {
	ServerID     string
	DatabaseName string // "" means server-wide default (stored as SQL NULL)
	Capability   string
	Enabled      bool
	SetBy        string
	SetAt        time.Time
	Reason       string
	AuditChainID int64 // audit_log.id of the entry that last set this row
}

// SetCapabilityPolicyInput is the request to SetCapabilityPolicy.
type SetCapabilityPolicyInput struct {
	ServerID     string
	DatabaseName string // "" for the server-wide default
	Capability   string
	Enabled      bool
	SetBy        string
	Reason       string
}

// SetCapabilityPolicy creates or updates one capability policy row and
// records an audit entry for the change. It first appends the audit
// entry (which assigns the audit id), then upserts the policy row
// carrying that id in audit_chain_id. Ordering note: if the upsert
// fails, the append-only audit chain stays valid — it records the
// attempted toggle.
func (c *Config) SetCapabilityPolicy(ctx context.Context, in SetCapabilityPolicyInput) (CapabilityPolicy, error) {
	if in.ServerID == "" {
		return CapabilityPolicy{}, fmt.Errorf("SetCapabilityPolicy: ServerID required")
	}
	if in.Capability == "" {
		return CapabilityPolicy{}, fmt.Errorf("SetCapabilityPolicy: Capability required")
	}
	if in.SetBy == "" {
		return CapabilityPolicy{}, fmt.Errorf("SetCapabilityPolicy: SetBy required")
	}

	rec, err := c.AppendAuditReturning(ctx, AuditEntry{
		Actor:    in.SetBy,
		Action:   "capability_policy.set",
		ServerID: in.ServerID,
		Detail: map[string]any{
			"database_name": dbNameDetail(in.DatabaseName),
			"capability":    in.Capability,
			"enabled":       in.Enabled,
			"reason":        in.Reason,
		},
	})
	if err != nil {
		return CapabilityPolicy{}, fmt.Errorf("audit: %w", err)
	}

	var dbArg any
	if in.DatabaseName != "" {
		dbArg = in.DatabaseName
	}

	var out CapabilityPolicy
	var dbName *string
	err = c.pool.QueryRow(ctx,
		`INSERT INTO capability_policy
		   (server_id, database_name, capability, enabled, set_by, set_at, reason, audit_chain_id)
		 VALUES ($1, $2, $3, $4, $5, now(), $6, $7)
		 ON CONFLICT (server_id, database_name, capability)
		 DO UPDATE SET
		   enabled        = EXCLUDED.enabled,
		   set_by         = EXCLUDED.set_by,
		   set_at         = EXCLUDED.set_at,
		   reason         = EXCLUDED.reason,
		   audit_chain_id = EXCLUDED.audit_chain_id
		 RETURNING server_id, database_name, capability, enabled,
		           set_by, set_at, reason, audit_chain_id`,
		in.ServerID, dbArg, in.Capability, in.Enabled, in.SetBy, in.Reason, rec.ID,
	).Scan(&out.ServerID, &dbName, &out.Capability, &out.Enabled,
		&out.SetBy, &out.SetAt, &out.Reason, &out.AuditChainID)
	if err != nil {
		return CapabilityPolicy{}, fmt.Errorf("upsert: %w", err)
	}
	if dbName != nil {
		out.DatabaseName = *dbName
	}
	return out, nil
}

// dbNameDetail renders the database_name for the audit detail: a JSON
// null for the server-wide default, otherwise the database name.
func dbNameDetail(name string) any {
	if name == "" {
		return nil
	}
	return name
}
