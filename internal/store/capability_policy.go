package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
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
//
//nolint:gocritic // hugeParam: cold admin-path API; SetCapabilityPolicyInput is a caller-owned value struct
func (c *pgxConfig) SetCapabilityPolicy(ctx context.Context, in SetCapabilityPolicyInput) (CapabilityPolicy, error) {
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

// GetCapabilityPolicy returns the exact policy row for the given key.
// databaseName == "" selects the server-wide default row (database_name
// IS NULL); a non-empty value selects that database's override row. It
// does NOT fall back between the two — use EffectiveCapability for
// resolution. found is false when no such row exists.
func (c *pgxConfig) GetCapabilityPolicy(ctx context.Context, serverID, databaseName, capability string) (CapabilityPolicy, bool, error) {
	var (
		out    CapabilityPolicy
		dbName *string
		row    pgx.Row
	)
	if databaseName == "" {
		row = c.ro.QueryRow(ctx,
			`SELECT server_id, database_name, capability, enabled,
			        set_by, set_at, reason, audit_chain_id
			   FROM capability_policy
			  WHERE server_id = $1 AND database_name IS NULL AND capability = $2`,
			serverID, capability)
	} else {
		row = c.ro.QueryRow(ctx,
			`SELECT server_id, database_name, capability, enabled,
			        set_by, set_at, reason, audit_chain_id
			   FROM capability_policy
			  WHERE server_id = $1 AND database_name = $2 AND capability = $3`,
			serverID, databaseName, capability)
	}
	err := row.Scan(&out.ServerID, &dbName, &out.Capability, &out.Enabled,
		&out.SetBy, &out.SetAt, &out.Reason, &out.AuditChainID)
	if err == pgx.ErrNoRows {
		return CapabilityPolicy{}, false, nil
	}
	if err != nil {
		return CapabilityPolicy{}, false, fmt.Errorf("get capability policy: %w", err)
	}
	if dbName != nil {
		out.DatabaseName = *dbName
	}
	return out, true, nil
}

// PolicySource identifies which row supplied an effective capability
// decision.
type PolicySource string

const (
	// PolicySourceServerDefault means the decision came from the
	// server-wide default row (database_name IS NULL).
	PolicySourceServerDefault PolicySource = "server-default"
	// PolicySourceDatabaseOverride means a database-specific row
	// overrode the server-wide default.
	PolicySourceDatabaseOverride PolicySource = "database-override"
)

// EffectiveCapability resolves whether a capability is enabled for a
// specific database on a server: a database-specific override row wins
// over the server-wide default. found is false when neither row exists
// (the caller decides the absent-policy default). The single query asks
// for both the override and the default and prefers the override via
// ORDER BY, so it is one round trip.
func (c *pgxConfig) EffectiveCapability(ctx context.Context, serverID, databaseName, capability string) (enabled bool, source PolicySource, found bool, err error) {
	var isOverride bool
	row := c.ro.QueryRow(ctx,
		`SELECT enabled, (database_name IS NOT NULL) AS is_override
		   FROM capability_policy
		  WHERE server_id = $1
		    AND capability = $2
		    AND (database_name = $3 OR database_name IS NULL)
		  ORDER BY (database_name IS NOT NULL) DESC
		  LIMIT 1`,
		serverID, capability, databaseName)
	scanErr := row.Scan(&enabled, &isOverride)
	if scanErr == pgx.ErrNoRows {
		return false, "", false, nil
	}
	if scanErr != nil {
		return false, "", false, fmt.Errorf("effective capability: %w", scanErr)
	}
	if isOverride {
		source = PolicySourceDatabaseOverride
	} else {
		source = PolicySourceServerDefault
	}
	return enabled, source, true, nil
}

// ListCapabilityPolicies returns every capability_policy row for one
// server, ordered for stable display (server-wide defaults first, then
// per-database overrides, by capability). Intended for the matrix API
// (ly-xnk.4).
func (c *pgxConfig) ListCapabilityPolicies(ctx context.Context, serverID string) ([]CapabilityPolicy, error) {
	rows, err := c.ro.Query(ctx,
		`SELECT server_id, database_name, capability, enabled,
		        set_by, set_at, reason, audit_chain_id
		   FROM capability_policy
		  WHERE server_id = $1
		  ORDER BY capability, (database_name IS NOT NULL), database_name`,
		serverID)
	if err != nil {
		return nil, fmt.Errorf("list capability policies: %w", err)
	}
	defer rows.Close()

	var out []CapabilityPolicy
	for rows.Next() {
		var p CapabilityPolicy
		var dbName *string
		if err := rows.Scan(&p.ServerID, &dbName, &p.Capability, &p.Enabled,
			&p.SetBy, &p.SetAt, &p.Reason, &p.AuditChainID); err != nil {
			return nil, fmt.Errorf("scan capability policy: %w", err)
		}
		if dbName != nil {
			p.DatabaseName = *dbName
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate capability policies: %w", err)
	}
	return out, nil
}

// CapabilityPolicyKey identifies one capability_policy row for delete/diff.
// DatabaseName == "" is the server-wide default (database_name IS NULL).
type CapabilityPolicyKey struct {
	ServerID     string
	DatabaseName string
	Capability   string
}

// ListCapabilityPoliciesForServers returns every capability_policy row for
// the given set of server ids in ONE query (WHERE server_id = ANY($1)), for
// the CRD operator's fleet reconcile. Ordered stably by server, capability,
// then server-wide defaults before per-database overrides. A nil/empty set
// returns an empty slice with no error.
func (c *pgxConfig) ListCapabilityPoliciesForServers(ctx context.Context, serverIDs []string) ([]CapabilityPolicy, error) {
	if len(serverIDs) == 0 {
		return nil, nil
	}
	rows, err := c.ro.Query(ctx,
		`SELECT server_id, database_name, capability, enabled,
		        set_by, set_at, reason, audit_chain_id
		   FROM capability_policy
		  WHERE server_id = ANY($1)
		  ORDER BY server_id, capability, (database_name IS NOT NULL), database_name`,
		serverIDs)
	if err != nil {
		return nil, fmt.Errorf("list capability policies for servers: %w", err)
	}
	defer rows.Close()

	var out []CapabilityPolicy
	for rows.Next() {
		var p CapabilityPolicy
		var dbName *string
		if err := rows.Scan(&p.ServerID, &dbName, &p.Capability, &p.Enabled,
			&p.SetBy, &p.SetAt, &p.Reason, &p.AuditChainID); err != nil {
			return nil, fmt.Errorf("scan capability policy: %w", err)
		}
		if dbName != nil {
			p.DatabaseName = *dbName
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate capability policies: %w", err)
	}
	return out, nil
}

// ApplyCapabilityPoliciesBatch upserts and deletes capability_policy rows in
// ONE transaction with ONE audit append, for a whole CRD fleet reconcile. It
// takes the audit advisory lock exactly once, appends a single
// 'capability_policy.bulk_set' audit row (audit-FIRST, so its id exists before
// any mutation), then runs ONE multi-row UPSERT and ONE multi-row DELETE
// carrying that id in audit_chain_id — collapsing thousands of serialized lock
// acquisitions into one. Callers supply already-flattened rows.
func (c *pgxConfig) ApplyCapabilityPoliciesBatch(ctx context.Context, upserts []SetCapabilityPolicyInput, deletes []CapabilityPolicyKey, actor string) error {
	if actor == "" {
		return fmt.Errorf("ApplyCapabilityPoliciesBatch: actor required")
	}
	for _, u := range upserts {
		if u.ServerID == "" || u.Capability == "" {
			return fmt.Errorf("ApplyCapabilityPoliciesBatch: upsert requires ServerID and Capability")
		}
	}
	for _, d := range deletes {
		if d.ServerID == "" || d.Capability == "" {
			return fmt.Errorf("ApplyCapabilityPoliciesBatch: delete requires ServerID and Capability")
		}
	}

	tx, err := c.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", auditLockKey); err != nil {
		return fmt.Errorf("advisory lock: %w", err)
	}

	rec, err := appendAuditTx(ctx, tx, AuditEntry{
		Actor:  actor,
		Action: "capability_policy.bulk_set",
		Detail: map[string]any{
			"row_count":    len(upserts) + len(deletes),
			"content_hash": batchContentHash(upserts, deletes),
		},
	})
	if err != nil {
		return fmt.Errorf("audit: %w", err)
	}

	if len(upserts) > 0 {
		serverIDs := make([]string, len(upserts))
		dbNames := make([]*string, len(upserts))
		caps := make([]string, len(upserts))
		enabled := make([]bool, len(upserts))
		setBy := make([]string, len(upserts))
		reasons := make([]string, len(upserts))
		for i, u := range upserts {
			serverIDs[i] = u.ServerID
			if u.DatabaseName != "" {
				db := u.DatabaseName
				dbNames[i] = &db
			}
			caps[i] = u.Capability
			enabled[i] = u.Enabled
			setBy[i] = u.SetBy
			reasons[i] = u.Reason
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO capability_policy
			   (server_id, database_name, capability, enabled, set_by, set_at, reason, audit_chain_id)
			 SELECT u.server_id, u.database_name, u.capability, u.enabled, u.set_by, now(), u.reason, $7::bigint
			   FROM unnest($1::text[], $2::text[], $3::text[], $4::bool[], $5::text[], $6::text[])
			        AS u(server_id, database_name, capability, enabled, set_by, reason)
			 ON CONFLICT (server_id, database_name, capability)
			 DO UPDATE SET
			   enabled        = EXCLUDED.enabled,
			   set_by         = EXCLUDED.set_by,
			   set_at         = EXCLUDED.set_at,
			   reason         = EXCLUDED.reason,
			   audit_chain_id = EXCLUDED.audit_chain_id`,
			serverIDs, dbNames, caps, enabled, setBy, reasons, rec.ID,
		); err != nil {
			return fmt.Errorf("upsert: %w", err)
		}
	}

	if len(deletes) > 0 {
		serverIDs := make([]string, len(deletes))
		dbNames := make([]*string, len(deletes))
		caps := make([]string, len(deletes))
		for i, d := range deletes {
			serverIDs[i] = d.ServerID
			if d.DatabaseName != "" {
				db := d.DatabaseName
				dbNames[i] = &db
			}
			caps[i] = d.Capability
		}
		// NULLS NOT DISTINCT: a NULL database_name key must match the
		// server-wide row, so compare with IS NOT DISTINCT FROM.
		if _, err := tx.Exec(ctx,
			`DELETE FROM capability_policy p
			  USING unnest($1::text[], $2::text[], $3::text[])
			        AS d(server_id, database_name, capability)
			  WHERE p.server_id = d.server_id
			    AND p.capability = d.capability
			    AND p.database_name IS NOT DISTINCT FROM d.database_name`,
			serverIDs, dbNames, caps,
		); err != nil {
			return fmt.Errorf("delete: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// batchContentHash is a stable SHA-256 over the upsert+delete payload, stored
// in the audit detail so the recorded batch is content-addressable.
func batchContentHash(upserts []SetCapabilityPolicyInput, deletes []CapabilityPolicyKey) string {
	h := sha256.New()
	enc := json.NewEncoder(h)
	_ = enc.Encode(struct {
		Upserts []SetCapabilityPolicyInput
		Deletes []CapabilityPolicyKey
	}{upserts, deletes})
	return hex.EncodeToString(h.Sum(nil))
}

// DeleteCapabilityPolicy removes one capability_policy row and records an
// audit entry for the removal. It appends the audit row FIRST (assigning the
// id) inside one advisory-locked tx, then deletes; deleting a non-existent key
// is a no-op that still records the attempt. DatabaseName == "" targets the
// server-wide default (database_name IS NULL).
func (c *pgxConfig) DeleteCapabilityPolicy(ctx context.Context, key CapabilityPolicyKey, actor, reason string) error {
	if key.ServerID == "" {
		return fmt.Errorf("DeleteCapabilityPolicy: ServerID required")
	}
	if key.Capability == "" {
		return fmt.Errorf("DeleteCapabilityPolicy: Capability required")
	}
	if actor == "" {
		return fmt.Errorf("DeleteCapabilityPolicy: actor required")
	}

	tx, err := c.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", auditLockKey); err != nil {
		return fmt.Errorf("advisory lock: %w", err)
	}

	if _, err := appendAuditTx(ctx, tx, AuditEntry{
		Actor:    actor,
		Action:   "capability_policy.delete",
		ServerID: key.ServerID,
		Detail: map[string]any{
			"database_name": dbNameDetail(key.DatabaseName),
			"capability":    key.Capability,
			"reason":        reason,
		},
	}); err != nil {
		return fmt.Errorf("audit: %w", err)
	}

	var dbArg any
	if key.DatabaseName != "" {
		dbArg = key.DatabaseName
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM capability_policy
		  WHERE server_id = $1
		    AND database_name IS NOT DISTINCT FROM $2
		    AND capability = $3`,
		key.ServerID, dbArg, key.Capability,
	); err != nil {
		return fmt.Errorf("delete: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}
