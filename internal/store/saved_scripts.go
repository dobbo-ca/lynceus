package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// SavedScript is one user-authored SQL script saved for reuse. sql_text is
// org metadata (not monitored-DB data). Visibility follows Scope:
//   GLOBAL   -> everyone in the org
//   TEAM     -> members of OwnerGroup
//   PERSONAL -> Owner only
type SavedScript struct {
	ID          int64
	Name        string
	Description string
	SQLText     string
	Scope       string // GLOBAL | TEAM | PERSONAL
	Owner       string
	OwnerGroup  string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// CreateScriptInput is the request to CreateScript.
type CreateScriptInput struct {
	Name        string
	Description string
	SQLText     string
	Scope       string
	Owner       string
	OwnerGroup  string
}

// Sentinel errors for the write path. Handlers map them to HTTP status.
var (
	ErrScriptNotFound  = errors.New("saved script not found")
	ErrScriptForbidden = errors.New("saved script change not permitted (owner or admin only)")
)

// ValidScriptScope reports whether s is one of the three allowed scopes.
func ValidScriptScope(s string) bool {
	return s == "GLOBAL" || s == "TEAM" || s == "PERSONAL"
}

const savedScriptCols = `id, name, description, sql_text, scope, owner, owner_group, created_at, updated_at`

func scanSavedScript(row pgx.Row) (SavedScript, error) {
	var s SavedScript
	err := row.Scan(&s.ID, &s.Name, &s.Description, &s.SQLText, &s.Scope,
		&s.Owner, &s.OwnerGroup, &s.CreatedAt, &s.UpdatedAt)
	return s, err
}

// CreateScript inserts a saved script and returns it with its assigned id.
//
//nolint:gocritic // hugeParam: cold write path; CreateScriptInput is a caller-owned value struct
func (c *pgxConfig) CreateScript(ctx context.Context, in CreateScriptInput) (SavedScript, error) {
	if in.Name == "" {
		return SavedScript{}, fmt.Errorf("CreateScript: Name required")
	}
	if in.SQLText == "" {
		return SavedScript{}, fmt.Errorf("CreateScript: SQLText required")
	}
	if in.Owner == "" {
		return SavedScript{}, fmt.Errorf("CreateScript: Owner required")
	}
	if !ValidScriptScope(in.Scope) {
		return SavedScript{}, fmt.Errorf("CreateScript: invalid scope %q", in.Scope)
	}
	row := c.pool.QueryRow(ctx,
		`INSERT INTO saved_scripts (name, description, sql_text, scope, owner, owner_group)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING `+savedScriptCols,
		in.Name, in.Description, in.SQLText, in.Scope, in.Owner, in.OwnerGroup)
	return scanSavedScript(row)
}

// ListVisibleScripts returns every script visible to viewer (member of
// group), ordered by name. GLOBAL is visible to all; TEAM to owner_group ==
// group; PERSONAL to owner == viewer.
func (c *pgxConfig) ListVisibleScripts(ctx context.Context, viewer, group string) ([]SavedScript, error) {
	rows, err := c.ro.Query(ctx,
		`SELECT `+savedScriptCols+`
		   FROM saved_scripts
		  WHERE scope = 'GLOBAL'
		     OR (scope = 'TEAM' AND owner_group = $2 AND $2 <> '')
		     OR (scope = 'PERSONAL' AND owner = $1)
		  ORDER BY name, id`, viewer, group)
	if err != nil {
		return nil, fmt.Errorf("list visible scripts: %w", err)
	}
	defer rows.Close()
	var out []SavedScript
	for rows.Next() {
		s, err := scanSavedScript(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// GetScript returns the script by id. found is false when no such row. This
// is the UNGATED lookup used only by the audited write path (SetScriptScope /
// DeleteScript), which enforces its own owner-or-admin gate. Read surfaces
// (detail page, console load) MUST use GetVisibleScript instead.
func (c *pgxConfig) GetScript(ctx context.Context, id int64) (SavedScript, bool, error) {
	s, err := scanSavedScript(c.ro.QueryRow(ctx,
		`SELECT `+savedScriptCols+` FROM saved_scripts WHERE id = $1`, id))
	if err == pgx.ErrNoRows {
		return SavedScript{}, false, nil
	}
	if err != nil {
		return SavedScript{}, false, fmt.Errorf("get script: %w", err)
	}
	return s, true, nil
}

// GetVisibleScript is the read gate: it returns the script by id only when
// it is visible to viewer (member of group), applying the same predicate as
// ListVisibleScripts. found is false both when the row is missing AND when
// it exists but is not visible — the two are deliberately indistinguishable
// so a non-visible PERSONAL script's existence (let alone its SQL) does not
// leak via /scripts/{id} or /console?script=<id>.
func (c *pgxConfig) GetVisibleScript(ctx context.Context, id int64, viewer, group string) (SavedScript, bool, error) {
	s, err := scanSavedScript(c.ro.QueryRow(ctx,
		`SELECT `+savedScriptCols+`
		   FROM saved_scripts
		  WHERE id = $1
		    AND (scope = 'GLOBAL'
		         OR (scope = 'TEAM' AND owner_group = $3 AND $3 <> '')
		         OR (scope = 'PERSONAL' AND owner = $2))`, id, viewer, group))
	if err == pgx.ErrNoRows {
		return SavedScript{}, false, nil
	}
	if err != nil {
		return SavedScript{}, false, fmt.Errorf("get visible script: %w", err)
	}
	return s, true, nil
}

// SetScriptScope changes a script's scope after checking that actor is the
// owner or an admin. It appends the tamper-evident audit entry FIRST (which
// assigns the audit id), then updates the row and binds that id into
// audit_chain_id — mirroring SetCapabilityPolicy exactly (audit-before-write
// ordering AND the row<->audit linkage). Ordering note: if the UPDATE fails,
// the append-only audit chain stays valid — it records the attempted change.
// Returns ErrScriptNotFound / ErrScriptForbidden as appropriate.
func (c *pgxConfig) SetScriptScope(ctx context.Context, id int64, newScope, actor string, isAdmin bool) (SavedScript, error) {
	if !ValidScriptScope(newScope) {
		return SavedScript{}, fmt.Errorf("SetScriptScope: invalid scope %q", newScope)
	}
	cur, ok, err := c.GetScript(ctx, id)
	if err != nil {
		return SavedScript{}, err
	}
	if !ok {
		return SavedScript{}, ErrScriptNotFound
	}
	if cur.Owner != actor && !isAdmin {
		return SavedScript{}, ErrScriptForbidden
	}

	rec, err := c.AppendAuditReturning(ctx, AuditEntry{
		Actor:  actor,
		Action: "saved_script.scope.change",
		Detail: map[string]any{
			"script_id": id,
			"name":      cur.Name,
			"from":      cur.Scope,
			"to":        newScope,
		},
	})
	if err != nil {
		return SavedScript{}, fmt.Errorf("audit: %w", err)
	}

	row := c.pool.QueryRow(ctx,
		`UPDATE saved_scripts SET scope = $2, updated_at = now(), audit_chain_id = $3
		  WHERE id = $1
		 RETURNING `+savedScriptCols, id, newScope, rec.ID)
	return scanSavedScript(row)
}

// DeleteScript deletes a script after checking owner-or-admin. It appends the
// audit entry FIRST, then deletes the row. Unlike SetScriptScope it discards
// the returned audit record on purpose: the row is about to be removed, so
// there is nothing left to carry audit_chain_id. The standalone audit entry
// (action saved_script.delete, with script_id/name/scope in Detail) is the
// durable record. Returns ErrScriptNotFound / ErrScriptForbidden.
func (c *pgxConfig) DeleteScript(ctx context.Context, id int64, actor string, isAdmin bool) error {
	cur, ok, err := c.GetScript(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		return ErrScriptNotFound
	}
	if cur.Owner != actor && !isAdmin {
		return ErrScriptForbidden
	}
	if _, err := c.AppendAuditReturning(ctx, AuditEntry{
		Actor:  actor,
		Action: "saved_script.delete",
		Detail: map[string]any{"script_id": id, "name": cur.Name, "scope": cur.Scope},
	}); err != nil {
		return fmt.Errorf("audit: %w", err)
	}
	if _, err := c.pool.Exec(ctx, `DELETE FROM saved_scripts WHERE id = $1`, id); err != nil {
		return fmt.Errorf("delete script: %w", err)
	}
	return nil
}

// ScriptTarget is one (cluster, node, database) triple the run-a-script
// flow can target. Database is "" when the server stream has no database
// name yet.
type ScriptTarget struct {
	Cluster  string
	Node     string
	Database string
}

// ListScriptTargets returns every cluster/node/database triple across the
// fleet, ordered for stable display. It is the searchable target index the
// Saved Scripts run flow resolves against.
func (c *pgxConfig) ListScriptTargets(ctx context.Context) ([]ScriptTarget, error) {
	rows, err := c.ro.Query(ctx,
		`SELECT c.name, i.name, COALESCE(s.database_name, '')
		   FROM servers s
		   JOIN instance i ON i.id = s.instance_id
		   JOIN cluster  c ON c.id = i.cluster_id
		  ORDER BY c.name, i.name, s.database_name NULLS FIRST`)
	if err != nil {
		return nil, fmt.Errorf("list script targets: %w", err)
	}
	defer rows.Close()
	var out []ScriptTarget
	for rows.Next() {
		var tg ScriptTarget
		if err := rows.Scan(&tg.Cluster, &tg.Node, &tg.Database); err != nil {
			return nil, err
		}
		out = append(out, tg)
	}
	return out, rows.Err()
}
