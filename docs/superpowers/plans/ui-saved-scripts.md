# Saved Scripts Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the Saved Scripts surface (list + detail) — global/team/personal scoped, owner/admin access-change and delete with audited scope changes, load-without-run, and a run-a-script flow that searches clusters/nodes/databases and requires node+database before handing off to the SQL console — plus the config-DB store/CRUD behind it.

**Architecture:** A new `saved_scripts` table in the config/metadata DB is fronted by CRUD + visibility methods on the existing `store.Config` seam (mirroring `capability_policy`'s audit-inside-write pattern for scope changes). templ+HTMX screens in package `web` render token-styled fragments; `internal/api` handlers resolve the viewer/owner/group, gate owner-or-admin writes, and hand off "load"/"run" to the SQL console (ly-ae6.8) via a documented query-param URL contract. All screens are built with design tokens (`var(--x)`), not legacy classes.

**Tech Stack:** Go 1.23, pgx v5, templ (a-h/templ), HTMX (self-hosted), PostgreSQL 16 (vanilla, no extensions), testcontainers-go for integration tests, `net/http/httptest` + rendered-HTML assertions for handler tests.

## Global Constraints

Every task's requirements implicitly include these project-wide rules. Copy them into your working context before each task:

- **Privacy T1/T2.** Only T1 (normalized, literal-free) monitored-database data may render. Saved-script SQL text is *user-authored org metadata* (like a config value), stored in the **config DB**, never the stats store — it is NOT monitored-database literal data and introduces no T1 wire-contract field. Do not add any monitored-DB literal (query sample, parameter value) to any saved-scripts path. Every saved-script **scope change and delete is audited** (`PRODUCT_INTENT.md:48` — "scope changes are audited").
- **No external hosts.** Never reference a CDN/font/script host. Add new css/js/svg under `web/static/` and reference `/static/...`. There is a contract test `web.TestLayout_NoExternalHosts`; new static assets get an analogous check.
- **Tokens, not legacy.** New screens must be styled with the design tokens in `web/static/css/tokens.css` (`var(--bg)`, `var(--acc2)`, `var(--line)`, …). Do NOT use `legacy.css` component classes on new screens.
- **templ regeneration.** After editing any `web/*.templ`, run `make templ` to regenerate the committed `_templ.go` files. CI checks they are in sync — commit the regenerated files.
- **testcontainers, no DB mocks.** Integration tests hit a real PostgreSQL via testcontainers (`internal/store`, `internal/api` patterns). Never mock the database.
- **Concurrent-session git hygiene.** Work on this session's own branch/worktree; verify `git branch --show-current` before every commit.

## Dependencies & Integration Contracts (build against, do NOT build here)

- **ly-ae6.2 (scope state + top bar)** and **ly-ae6.3 (scope-driven sidebar nav rebuild)** — not yet on this branch. Saved Scripts must appear as a **CONSOLE section nav entry at EVERY scope** (fleet CONSOLE = Saved Scripts only; cluster/node/db CONSOLE = SQL Console + Saved Scripts). This plan builds the screens + routes; ly-ae6.3 adds the nav `<a href="/scripts">Saved Scripts</a>` entry into every scope tree. **Contract:** the Saved Scripts page lives at the stable path `GET /scripts` and needs no scope param to function (it is scope-independent — visible at all scopes).
- **ly-ae6.8 (SQL Console, T2)** — not yet on this branch. The console is the **host surface**: scripts are saved FROM the console's `SAVE ▾` control and a "load"/"run" lands IN the console. **Contract (this plan defines and links to it; ly-ae6.8 consumes it):** the console is served at `GET /console` and reads these query params — `script=<id>` (preload the script's SQL into the editor, do NOT execute), `cluster=<name>`, `node=<name>`, `db=<name>` (preselect the target), `run=1` (execute immediately if a session grant is active, else show the grant gate — with `run` absent the console loads without running). The console also embeds this plan's saved-script search dropdown fragment (`GET /partial/scripts/search`) and posts new scripts to `POST /scripts`. **Privacy obligation on ly-ae6.8:** when resolving `script=<id>`, the console MUST call `store.GetVisibleScript(id, viewer, group)` (the visibility-gated read this plan adds in Task 1), NOT the ungated `store.GetScript`, and reject with 404 when it returns `found=false` — otherwise `/console?script=<id>` becomes an enumeration hole that leaks a non-owner's PERSONAL SQL, defeating the `/scripts/{id}` gate. Until ly-ae6.8 lands, `/console` may 404 — the load/run anchors this plan builds are still correct and are unit-tested on their emitted `href`.

## File Structure

- `internal/store/migrations/config/0006_saved_scripts.sql` — new `saved_scripts` table (config DB).
- `internal/store/saved_scripts.go` — `SavedScript` type, `CreateScript`, `ListVisibleScripts`, `GetScript` (ungated, write-path only), `GetVisibleScript` (visibility-gated read), `SetScriptScope` (audited, binds `audit_chain_id`), `DeleteScript` (audited), `ListScriptTargets`, sentinel errors, scope validation.
- `internal/store/saved_scripts_test.go` — testcontainer integration tests for all of the above.
- `internal/store/config.go:15-31` — add the six new methods to the `Config` interface.
- `web/scripts_vm.go` — view-model structs + pure helpers (`RelativeAge`, scope-color map, `ScriptVisibleTo`, target-value encode/decode). No I/O.
- `web/scripts_vm_test.go` — unit tests for the pure helpers.
- `web/scripts.templ` — `SavedScriptsPage`, `SavedScriptsTable`, `ScriptDetailPage`, `ScriptAccessCard`, `ScriptRunCard`, `ScriptSearchResults` (all token-styled).
- `web/static/css/scripts.css` — token-based classes for the Saved Scripts screens (grid, badges, chips, icon buttons, hover states).
- `web/layout.templ:26-27` — link `scripts.css`.
- `web/scripts_css_test.go` — assert `scripts.css` uses tokens and references no external host.
- `internal/api/scripts.go` — handlers + viewer/group/admin context helpers + hand-off URL builder.
- `internal/api/scripts_test.go` — handler integration tests.
- `internal/api/server.go:49-83` — register the new routes.

---

### Task 1: Store — `saved_scripts` schema, type, create/list/get

**Files:**
- Create: `internal/store/migrations/config/0006_saved_scripts.sql`
- Create: `internal/store/saved_scripts.go`
- Create: `internal/store/saved_scripts_test.go`
- Modify: `internal/store/config.go:15-31` (add methods to the `Config` interface)

**Interfaces:**
- Consumes: `pgxConfig` (`internal/store/config.go:36-43`), `store.ApplyConfigMigrations` (`internal/store/migrate.go:90-92`), `newPool(t)` test helper (`internal/store/store_test.go:24-50`).
- Produces:
  ```go
  type SavedScript struct {
      ID          int64
      Name        string
      Description string
      SQLText     string
      Scope       string // "GLOBAL" | "TEAM" | "PERSONAL"
      Owner       string
      OwnerGroup  string // group that TEAM scope is visible to (e.g. "dba-oncall")
      CreatedAt   time.Time
      UpdatedAt   time.Time
  }
  type CreateScriptInput struct {
      Name, Description, SQLText, Scope, Owner, OwnerGroup string
  }
  func ValidScriptScope(s string) bool
  func (c *pgxConfig) CreateScript(ctx context.Context, in CreateScriptInput) (SavedScript, error)
  func (c *pgxConfig) ListVisibleScripts(ctx context.Context, viewer, group string) ([]SavedScript, error)
  func (c *pgxConfig) GetScript(ctx context.Context, id int64) (SavedScript, bool, error)
  // GetVisibleScript is GetScript with the ListVisibleScripts visibility
  // predicate folded into the WHERE clause: found is false when the row
  // does not exist OR is not visible to the viewer. It is the read gate the
  // detail page + console load path use so a PERSONAL script cannot be read
  // by anyone but its owner via /scripts/{id} or /console?script=<id>.
  func (c *pgxConfig) GetVisibleScript(ctx context.Context, id int64, viewer, group string) (SavedScript, bool, error)
  ```

- [ ] **Step 1: Write the migration**

Create `internal/store/migrations/config/0006_saved_scripts.sql`:

```sql
-- internal/store/migrations/config/0006_saved_scripts.sql
--
-- ly-ae6.9 — Saved Scripts (global / team / personal).
--
-- User-authored SQL scripts saved for reuse. sql_text is *user-authored
-- org metadata* (like a saved report), NOT monitored-database data, so it
-- carries no T1/T2 stats-store classification. Visibility follows scope:
--   GLOBAL   -> everyone in the org
--   TEAM     -> members of owner_group (e.g. dba-oncall)
--   PERSONAL -> the owner only
-- Only the owner (or an admin) may change scope or delete; every such
-- change is recorded through the Go writer as a tamper-evident audit entry
-- (ly-8b0.3). audit_chain_id binds a live row to the audit entry for its
-- LAST scope change (mirrors capability_policy.audit_chain_id). A delete
-- leaves a standalone audit entry — the row is gone, so there is nothing
-- left to bind, which is why DeleteScript records the entry then removes
-- the row and does not carry the id anywhere.
--
-- Vanilla PostgreSQL — no extensions (must run on RDS / Aurora).

CREATE TABLE saved_scripts (
    id             BIGSERIAL   PRIMARY KEY,
    name           TEXT        NOT NULL,
    description    TEXT        NOT NULL DEFAULT '',
    sql_text       TEXT        NOT NULL,
    scope          TEXT        NOT NULL CHECK (scope IN ('GLOBAL','TEAM','PERSONAL')),
    owner          TEXT        NOT NULL,
    owner_group    TEXT        NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    audit_chain_id BIGINT      REFERENCES audit_log (id)
);

-- Visibility resolution scans by scope; ownership checks scan by owner.
CREATE INDEX saved_scripts_scope_idx ON saved_scripts (scope);
CREATE INDEX saved_scripts_owner_idx ON saved_scripts (owner);
```

- [ ] **Step 2: Write the failing test**

Create `internal/store/saved_scripts_test.go`:

```go
package store_test

import (
	"context"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/store"
)

func TestSavedScripts_CreateListGet_visibilityByScope(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg := store.NewConfig(pool)

	mk := func(name, scope, owner, group string) store.SavedScript {
		s, err := cfg.CreateScript(ctx, store.CreateScriptInput{
			Name: name, Description: name + " desc", SQLText: "SELECT 1",
			Scope: scope, Owner: owner, OwnerGroup: group,
		})
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		return s
	}
	mk("g", "GLOBAL", "m.chen", "")
	mk("t", "TEAM", "j.alvarez", "dba-oncall")
	mk("p-mine", "PERSONAL", "s.dobson", "")
	mk("p-theirs", "PERSONAL", "m.chen", "")

	// s.dobson in group dba-oncall sees: GLOBAL + TEAM(dba-oncall) + own PERSONAL.
	got, err := cfg.ListVisibleScripts(ctx, "s.dobson", "dba-oncall")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	names := map[string]bool{}
	for _, s := range got {
		names[s.Name] = true
	}
	if !names["g"] || !names["t"] || !names["p-mine"] {
		t.Errorf("visible set missing an expected script: %v", names)
	}
	if names["p-theirs"] {
		t.Error("leaked another user's PERSONAL script")
	}

	// GetScript round-trips fields.
	first := got[0]
	one, ok, err := cfg.GetScript(ctx, first.ID)
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if one.SQLText != "SELECT 1" || one.Scope == "" {
		t.Errorf("get returned %+v", one)
	}

	// Missing id => (_, false, nil).
	if _, ok, err := cfg.GetScript(ctx, 999999); err != nil || ok {
		t.Errorf("missing get: ok=%v err=%v", ok, err)
	}
}

func TestSavedScripts_Create_rejectsBadScope(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg := store.NewConfig(pool)
	if _, err := cfg.CreateScript(ctx, store.CreateScriptInput{
		Name: "x", SQLText: "SELECT 1", Scope: "WORLD", Owner: "a",
	}); err == nil {
		t.Error("expected error for invalid scope, got nil")
	}
}

func TestSavedScripts_GetVisibleScript_gatesPersonalByViewer(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg := store.NewConfig(pool)
	personal, err := cfg.CreateScript(ctx, store.CreateScriptInput{
		Name: "mine", SQLText: "SELECT secret FROM x", Scope: "PERSONAL", Owner: "s.dobson",
	})
	if err != nil {
		t.Fatalf("create personal: %v", err)
	}
	global, err := cfg.CreateScript(ctx, store.CreateScriptInput{
		Name: "shared", SQLText: "SELECT 1", Scope: "GLOBAL", Owner: "m.chen",
	})
	if err != nil {
		t.Fatalf("create global: %v", err)
	}

	// Owner sees their own PERSONAL script.
	if _, ok, err := cfg.GetVisibleScript(ctx, personal.ID, "s.dobson", "dba-oncall"); err != nil || !ok {
		t.Errorf("owner GetVisibleScript(personal): ok=%v err=%v, want ok=true", ok, err)
	}
	// A different viewer (even one who is otherwise privileged) does NOT.
	if _, ok, err := cfg.GetVisibleScript(ctx, personal.ID, "m.chen", "dba-oncall"); err != nil || ok {
		t.Errorf("non-owner GetVisibleScript(personal): ok=%v err=%v, want ok=false", ok, err)
	}
	// GLOBAL is visible to any viewer.
	if _, ok, err := cfg.GetVisibleScript(ctx, global.ID, "s.dobson", "dba-oncall"); err != nil || !ok {
		t.Errorf("GetVisibleScript(global): ok=%v err=%v, want ok=true", ok, err)
	}
	// Missing id => (_, false, nil).
	if _, ok, err := cfg.GetVisibleScript(ctx, 987654, "s.dobson", "dba-oncall"); err != nil || ok {
		t.Errorf("missing GetVisibleScript: ok=%v err=%v", ok, err)
	}
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./internal/store/ -run 'TestSavedScripts_(Create|GetVisibleScript)' -count=1`
Expected: FAIL — `undefined: store.CreateScript` / `store.SavedScript` / `cfg.GetVisibleScript` (compile error).

- [ ] **Step 4: Implement the store methods**

Create `internal/store/saved_scripts.go`:

```go
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
```

Add the four methods to the `Config` interface in `internal/store/config.go` (after line 30, `ServerT2Enabled(...)`):

```go
	CreateScript(ctx context.Context, in CreateScriptInput) (SavedScript, error)
	ListVisibleScripts(ctx context.Context, viewer, group string) ([]SavedScript, error)
	GetScript(ctx context.Context, id int64) (SavedScript, bool, error)
	GetVisibleScript(ctx context.Context, id int64, viewer, group string) (SavedScript, bool, error)
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./internal/store/ -run TestSavedScripts -count=1`
Expected: PASS (`TestSavedScripts_CreateListGet_visibilityByScope`, `TestSavedScripts_Create_rejectsBadScope`, and `TestSavedScripts_GetVisibleScript_gatesPersonalByViewer`).

- [ ] **Step 6: Commit**

```bash
git add internal/store/migrations/config/0006_saved_scripts.sql internal/store/saved_scripts.go internal/store/saved_scripts_test.go internal/store/config.go
git commit -m "feat(store): saved_scripts schema + create/list/get with scope visibility (ly-ae6.9)"
```

---

### Task 2: Store — audited `SetScriptScope` + `DeleteScript`

**Files:**
- Modify: `internal/store/saved_scripts.go` (append methods)
- Modify: `internal/store/saved_scripts_test.go` (append tests)
- Modify: `internal/store/config.go:15-31` (add two methods to the interface)

**Interfaces:**
- Consumes: `AppendAuditReturning` (`internal/store/config.go:186-206`), `AuditEntry` (`internal/store/config.go:69-75`), `GetScript`, `ValidScriptScope`, `ErrScriptNotFound`, `ErrScriptForbidden` (Task 1).
- Produces:
  ```go
  func (c *pgxConfig) SetScriptScope(ctx context.Context, id int64, newScope, actor string, isAdmin bool) (SavedScript, error)
  func (c *pgxConfig) DeleteScript(ctx context.Context, id int64, actor string, isAdmin bool) error
  ```
  Both return `ErrScriptNotFound` for a missing id and `ErrScriptForbidden` when `actor != script.Owner && !isAdmin`. `SetScriptScope` writes audit action `saved_script.scope.change`; `DeleteScript` writes `saved_script.delete`.

- [ ] **Step 1: Write the failing test**

Append to `internal/store/saved_scripts_test.go`:

```go
func TestSavedScripts_SetScope_auditedAndOwnerGated(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg := store.NewConfig(pool)
	s, err := cfg.CreateScript(ctx, store.CreateScriptInput{
		Name: "replica-lag", SQLText: "SELECT 1", Scope: "PERSONAL", Owner: "s.dobson",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Non-owner, non-admin is rejected — no change, no audit row.
	if _, err := cfg.SetScriptScope(ctx, s.ID, "GLOBAL", "m.chen", false); err != store.ErrScriptForbidden {
		t.Fatalf("non-owner set: err=%v, want ErrScriptForbidden", err)
	}

	// Owner changes scope; row updated and an audit entry recorded.
	updated, err := cfg.SetScriptScope(ctx, s.ID, "TEAM", "s.dobson", false)
	if err != nil {
		t.Fatalf("owner set: %v", err)
	}
	if updated.Scope != "TEAM" {
		t.Errorf("scope = %q, want TEAM", updated.Scope)
	}

	var auditCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE action = 'saved_script.scope.change'`,
	).Scan(&auditCount); err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if auditCount != 1 {
		t.Errorf("scope-change audit rows = %d, want 1", auditCount)
	}

	// The row is bound to its scope-change audit entry (audit_chain_id).
	var chainID *int64
	if err := pool.QueryRow(ctx,
		`SELECT sc.audit_chain_id
		   FROM saved_scripts sc WHERE sc.id = $1`, s.ID).Scan(&chainID); err != nil {
		t.Fatalf("read audit_chain_id: %v", err)
	}
	if chainID == nil {
		t.Error("scope change did not bind audit_chain_id to the audit entry")
	} else {
		var linked int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM audit_log WHERE id = $1 AND action = 'saved_script.scope.change'`,
			*chainID).Scan(&linked); err != nil {
			t.Fatalf("verify linkage: %v", err)
		}
		if linked != 1 {
			t.Errorf("audit_chain_id %d does not point at the scope-change audit entry", *chainID)
		}
	}

	// Invalid target scope rejected.
	if _, err := cfg.SetScriptScope(ctx, s.ID, "WORLD", "s.dobson", false); err == nil {
		t.Error("expected invalid-scope error, got nil")
	}
	// Missing id => ErrScriptNotFound.
	if _, err := cfg.SetScriptScope(ctx, 987654, "GLOBAL", "s.dobson", true); err != store.ErrScriptNotFound {
		t.Errorf("missing set: err=%v, want ErrScriptNotFound", err)
	}
}

func TestSavedScripts_Delete_ownerGatedAndAudited(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg := store.NewConfig(pool)
	s, err := cfg.CreateScript(ctx, store.CreateScriptInput{
		Name: "d", SQLText: "SELECT 1", Scope: "PERSONAL", Owner: "s.dobson",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Non-owner rejected.
	if err := cfg.DeleteScript(ctx, s.ID, "m.chen", false); err != store.ErrScriptForbidden {
		t.Fatalf("non-owner delete: err=%v, want ErrScriptForbidden", err)
	}
	// Admin (not owner) allowed.
	if err := cfg.DeleteScript(ctx, s.ID, "root", true); err != nil {
		t.Fatalf("admin delete: %v", err)
	}
	if _, ok, _ := cfg.GetScript(ctx, s.ID); ok {
		t.Error("script still present after delete")
	}
	var auditCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE action = 'saved_script.delete'`,
	).Scan(&auditCount); err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if auditCount != 1 {
		t.Errorf("delete audit rows = %d, want 1", auditCount)
	}
	// Deleting a missing id => ErrScriptNotFound.
	if err := cfg.DeleteScript(ctx, s.ID, "root", true); err != store.ErrScriptNotFound {
		t.Errorf("re-delete: err=%v, want ErrScriptNotFound", err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/store/ -run 'TestSavedScripts_(SetScope|Delete)' -count=1`
Expected: FAIL — `cfg.SetScriptScope undefined` / `cfg.DeleteScript undefined`.

- [ ] **Step 3: Implement the audited write methods**

Append to `internal/store/saved_scripts.go`:

```go
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
```

Add to the `Config` interface in `internal/store/config.go` (below the Task-1 additions):

```go
	SetScriptScope(ctx context.Context, id int64, newScope, actor string, isAdmin bool) (SavedScript, error)
	DeleteScript(ctx context.Context, id int64, actor string, isAdmin bool) error
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/store/ -run 'TestSavedScripts_(SetScope|Delete)' -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/saved_scripts.go internal/store/saved_scripts_test.go internal/store/config.go
git commit -m "feat(store): audited owner/admin-gated saved-script scope-change + delete (ly-ae6.9)"
```

---

### Task 3: Store — `ListScriptTargets` fleet target index

**Files:**
- Modify: `internal/store/saved_scripts.go` (append)
- Modify: `internal/store/saved_scripts_test.go` (append)
- Modify: `internal/store/config.go:15-31` (add one method)

**Interfaces:**
- Consumes: existing `servers` / `instance` / `cluster` tables (`CreateCluster`, `CreateInstance`, `AssignServerToInstance` — `internal/store/fleet.go:41-67`).
- Produces:
  ```go
  type ScriptTarget struct { Cluster, Node, Database string }
  func (c *pgxConfig) ListScriptTargets(ctx context.Context) ([]ScriptTarget, error)
  ```
  Returns one row per (cluster, instance, database) triple across the whole fleet, ordered by cluster, node, database. `Database` is `""` when the server stream has no database name yet. This is the searchable index the run-flow uses to offer cluster/node/database targets.

- [ ] **Step 1: Write the failing test**

Append to `internal/store/saved_scripts_test.go`:

```go
func TestListScriptTargets_returnsClusterNodeDatabaseTriples(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg := store.NewConfig(pool)

	cl, err := cfg.CreateCluster(ctx, "orders-prod")
	if err != nil {
		t.Fatalf("cluster: %v", err)
	}
	in, err := cfg.CreateInstance(ctx, cl.ID, "srv-orders-primary")
	if err != nil {
		t.Fatalf("instance: %v", err)
	}
	// A server stream with a database name, linked to the instance.
	if _, err := pool.Exec(ctx,
		`INSERT INTO servers (id, name, database_name, instance_id) VALUES ($1,$2,$3,$4)`,
		"srv-1", "srv-orders-primary", "orders", in.ID); err != nil {
		t.Fatalf("seed server: %v", err)
	}

	got, err := cfg.ListScriptTargets(ctx)
	if err != nil {
		t.Fatalf("targets: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("targets = %d, want 1", len(got))
	}
	if got[0].Cluster != "orders-prod" || got[0].Node != "srv-orders-primary" || got[0].Database != "orders" {
		t.Errorf("target = %+v", got[0])
	}
}
```

Note: `servers.database_name` and `servers.instance_id` are added by migration `0005_fleet.sql`; the seed INSERT above relies on them existing after `ApplyConfigMigrations`.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/store/ -run TestListScriptTargets -count=1`
Expected: FAIL — `cfg.ListScriptTargets undefined`.

- [ ] **Step 3: Implement `ListScriptTargets`**

Append to `internal/store/saved_scripts.go`:

```go
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
```

Add to the `Config` interface in `internal/store/config.go`:

```go
	ListScriptTargets(ctx context.Context) ([]ScriptTarget, error)
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/store/ -run TestListScriptTargets -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/saved_scripts.go internal/store/saved_scripts_test.go internal/store/config.go
git commit -m "feat(store): ListScriptTargets fleet cluster/node/database index (ly-ae6.9)"
```

---

### Task 4: View-model helpers + Saved Scripts list page

**Files:**
- Create: `web/scripts_vm.go`
- Create: `web/scripts_vm_test.go`
- Create: `web/scripts.templ` (list templ only in this task; detail/run/access added in Tasks 5–8)
- Create: `web/static/css/scripts.css`
- Create: `web/scripts_css_test.go`
- Modify: `web/layout.templ:26-27` (link `scripts.css`)
- Create: `internal/api/scripts.go` (list handler + context helpers)
- Modify: `internal/api/server.go:49-83` (routes)
- Create: `internal/api/scripts_test.go`

**Interfaces:**
- Consumes: `store.SavedScript`, `store.ListVisibleScripts` (Task 1); `Layout` (`web/layout.templ:18`); `actorFromContext` (`internal/api/capabilities.go:34`).
- Produces:
  ```go
  // web/scripts_vm.go
  func RelativeAge(t, now time.Time) string            // "just now" | "Nm ago" | "Nh ago" | "Nd ago"
  func ScriptScopeColor(scope string) string           // token var string
  func ScriptVisibleTo(scope, owner, ownerGroup string, mine bool) string
  type SavedScriptRow struct {
      ID int64; Name, Description, Scope, ScopeColor, VisibleTo, Owner, SavedAge string
      Mine bool; DetailHref, LoadHref string
  }
  type SavedScriptsVM struct { Query, SubLine string; Count int; Rows []SavedScriptRow }
  // web/scripts.templ
  templ SavedScriptsPage(vm SavedScriptsVM)
  templ SavedScriptsTable(vm SavedScriptsVM)
  // internal/api/scripts.go
  func (s *Server) handleSavedScriptsPage(w http.ResponseWriter, r *http.Request)
  func (s *Server) handleSavedScriptsTable(w http.ResponseWriter, r *http.Request)
  func viewerFromContext(r *http.Request) string   // == actorFromContext
  func groupFromContext(r *http.Request) string    // "dba-oncall" stub (Milestone-5 follow-up)
  func isAdminFromContext(r *http.Request) bool     // true under DevAuth
  ```

- [ ] **Step 1: Write the failing helper test**

Create `web/scripts_vm_test.go`:

```go
package web

import (
	"testing"
	"time"
)

func TestRelativeAge(t *testing.T) {
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		age  time.Duration
		want string
	}{
		{30 * time.Second, "just now"},
		{5 * time.Minute, "5m ago"},
		{3 * time.Hour, "3h ago"},
		{50 * time.Hour, "2d ago"},
	}
	for _, c := range cases {
		if got := RelativeAge(now.Add(-c.age), now); got != c.want {
			t.Errorf("RelativeAge(-%s) = %q, want %q", c.age, got, c.want)
		}
	}
}

func TestScriptScopeColorAndVisibleTo(t *testing.T) {
	if ScriptScopeColor("GLOBAL") != "var(--acc2)" {
		t.Errorf("GLOBAL color = %q", ScriptScopeColor("GLOBAL"))
	}
	if ScriptScopeColor("TEAM") != "var(--infoT)" {
		t.Errorf("TEAM color = %q", ScriptScopeColor("TEAM"))
	}
	if ScriptScopeColor("PERSONAL") != "var(--warnT)" {
		t.Errorf("PERSONAL color = %q", ScriptScopeColor("PERSONAL"))
	}
	if got := ScriptVisibleTo("GLOBAL", "m.chen", "", false); got != "everyone in the org" {
		t.Errorf("GLOBAL visibleTo = %q", got)
	}
	if got := ScriptVisibleTo("TEAM", "j.alvarez", "dba-oncall", false); got != "group dba-oncall" {
		t.Errorf("TEAM visibleTo = %q", got)
	}
	if got := ScriptVisibleTo("PERSONAL", "s.dobson", "", true); got != "only you" {
		t.Errorf("PERSONAL mine visibleTo = %q", got)
	}
	if got := ScriptVisibleTo("PERSONAL", "s.dobson", "", false); got != "only s.dobson" {
		t.Errorf("PERSONAL theirs visibleTo = %q", got)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./web/ -run 'TestRelativeAge|TestScriptScopeColorAndVisibleTo' -count=1`
Expected: FAIL — `undefined: RelativeAge` / `ScriptScopeColor` / `ScriptVisibleTo`.

- [ ] **Step 3: Implement the view-model helpers**

Create `web/scripts_vm.go`:

```go
package web

import (
	"fmt"
	"time"
)

// SavedScriptRow is the view-model for one row of the Saved Scripts list.
// Every field is user-authored metadata or a token color string — no
// monitored-database literal.
type SavedScriptRow struct {
	ID          int64
	Name        string
	Description string
	Scope       string // GLOBAL | TEAM | PERSONAL
	ScopeColor  string // e.g. "var(--acc2)"
	VisibleTo   string // "everyone in the org" | "group dba-oncall" | "only you" | "only <owner>"
	Owner       string
	SavedAge    string // "12d ago"
	Mine        bool   // owner == viewer; drives the delete button
	DetailHref  string // /scripts/<id>
	LoadHref    string // /console?script=<id>  (load-without-run hand-off)
}

// SavedScriptsVM is the view-model for the Saved Scripts list surface.
type SavedScriptsVM struct {
	Query   string // echoed search text
	SubLine string // "<n> SCRIPTS · GLOBAL — EVERYONE · TEAM — DBA-ONCALL · PERSONAL — OWNER ONLY"
	Count   int
	Rows    []SavedScriptRow
}

// RelativeAge renders a compact, coarse "time since" label matching the
// design ("just now", "5m ago", "3h ago", "2d ago").
func RelativeAge(t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d/time.Hour))
	default:
		return fmt.Sprintf("%dd ago", int(d/(24*time.Hour)))
	}
}

// ScriptScopeColor maps a scope to its token color (mirrors the prototype's
// scopeColors map: GLOBAL acc2, TEAM infoT, PERSONAL warnT).
func ScriptScopeColor(scope string) string {
	switch scope {
	case "GLOBAL":
		return "var(--acc2)"
	case "TEAM":
		return "var(--infoT)"
	case "PERSONAL":
		return "var(--warnT)"
	default:
		return "var(--mut)"
	}
}

// ScriptVisibleTo renders the "visible to …" copy for a scope.
func ScriptVisibleTo(scope, owner, ownerGroup string, mine bool) string {
	switch scope {
	case "GLOBAL":
		return "everyone in the org"
	case "TEAM":
		return "group " + ownerGroup
	default:
		if mine {
			return "only you"
		}
		return "only " + owner
	}
}
```

- [ ] **Step 4: Run to verify the helpers pass**

Run: `go test ./web/ -run 'TestRelativeAge|TestScriptScopeColorAndVisibleTo' -count=1`
Expected: PASS.

- [ ] **Step 5: Write the list templ**

Create `web/scripts.templ`:

```go
package web

// SavedScriptsPage is the full Saved Scripts list surface. It is
// scope-independent (visible at every scope) and lives at /scripts.
templ SavedScriptsPage(vm SavedScriptsVM) {
	@Layout("Lynceus — saved scripts", "saved scripts") {
		<div class="ss-page">
			<div class="ss-head">
				<span class="ss-title">Saved Scripts</span>
				<span class="ss-live">LIVE</span>
				<span class="ss-sub">{ vm.SubLine }</span>
			</div>
			<input class="ss-search" type="search" name="q" value={ vm.Query }
				placeholder="search saved scripts…"
				hx-get="/partial/scripts" hx-target="#scripts-table" hx-swap="outerHTML"
				hx-trigger="input changed delay:200ms, search"/>
			@SavedScriptsTable(vm)
			<div class="ss-foot">CLICK A SCRIPT TO MANAGE ACCESS OR RUN IT AGAINST A DATABASE. ONLY THE OWNER (OR AN ADMIN) CAN CHANGE ACCESS OR DELETE.</div>
		</div>
	}
}

// SavedScriptsTable is the swap target for HTMX filtering. The wrapping div
// carries the id used as the swap target.
templ SavedScriptsTable(vm SavedScriptsVM) {
	<div id="scripts-table" class="ss-card">
		<div class="ss-tablescroll">
			<div class="ss-table">
				<div class="ss-thead">
					<span>SCRIPT</span><span>ACCESS</span><span>OWNER</span><span>SAVED</span><span></span>
				</div>
				if len(vm.Rows) == 0 {
					<div class="ss-empty">NO SCRIPTS MATCH</div>
				} else {
					for _, sp := range vm.Rows {
						<div class="ss-row">
							<a class="ss-namecell" href={ templ.SafeURL(sp.DetailHref) }>
								<span class="ss-name">{ sp.Name }</span>
								<span class="ss-desc">{ sp.Description }</span>
							</a>
							<div class="ss-accesscell">
								<span class="scope-badge" style={ "color:" + sp.ScopeColor }>{ sp.Scope }</span>
								<span class="ss-visible">visible to { sp.VisibleTo }</span>
							</div>
							<span class="ss-owner">{ sp.Owner }</span>
							<span class="ss-when">{ sp.SavedAge }</span>
							<div class="ss-actions">
								<a class="ss-iconbtn ss-iconbtn--load" title="Load into console"
									href={ templ.SafeURL(sp.LoadHref) }>↪</a>
								if sp.Mine {
									<form method="post" action={ templ.SafeURL("/scripts/" + itoa(sp.ID) + "/delete") }
										hx-post={ "/scripts/" + itoa(sp.ID) + "/delete" } hx-target="#scripts-table" hx-swap="outerHTML"
										hx-confirm="Delete this saved script?">
										<button class="ss-iconbtn ss-iconbtn--del" title="Delete script" type="submit">✕</button>
									</form>
								}
							</div>
						</div>
					}
				}
			</div>
		</div>
	</div>
}
```

Append the small int-format helper to `web/scripts_vm.go` (used by the templ for path building):

```go
// itoa formats an int64 id for URL path segments.
func itoa(id int64) string { return fmt.Sprintf("%d", id) }
```

- [ ] **Step 6: Write the CSS**

Create `web/static/css/scripts.css`:

```css
/* Saved Scripts screens — token-styled (ly-ae6.9). No external hosts. */
.ss-page { padding:18px 22px 32px; display:flex; flex-direction:column; gap:14px; max-width:1400px; }
.ss-head { display:flex; align-items:center; gap:12px; }
.ss-title { font-size:17px; font-weight:600; }
.ss-live { font-family:var(--font-mono); font-size:10px; color:var(--acc); border:1px solid var(--acc); padding:0 5px; border-radius:var(--radius-badge); }
.ss-sub { font-family:var(--font-mono); font-size:10.5px; color:var(--faint); letter-spacing:.08em; }
.ss-search { align-self:flex-start; width:290px; max-width:100%; background:var(--raised); border:1px solid var(--line); border-radius:var(--radius); color:var(--text); font-family:var(--font-mono); font-size:11px; padding:6px 9px; box-sizing:border-box; }
.ss-card { border:1px solid var(--line); border-radius:var(--radius); background:var(--surface); }
.ss-tablescroll { overflow-x:auto; }
.ss-table { min-width:860px; }
.ss-thead, .ss-row { display:grid; grid-template-columns:minmax(240px,1fr) 230px 110px 90px 76px; gap:12px; align-items:center; }
.ss-thead { padding:8px 12px; border-bottom:1px solid var(--line); font-family:var(--font-mono); font-size:9.5px; letter-spacing:.1em; color:var(--faint); }
.ss-row { padding:9px 12px; border-bottom:1px solid var(--line2); }
.ss-namecell { display:flex; flex-direction:column; gap:2px; min-width:0; text-decoration:none; }
.ss-name { font-family:var(--font-mono); font-size:11.5px; font-weight:600; color:var(--acc2); overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
.ss-namecell:hover .ss-name { text-decoration:underline; }
.ss-desc { font-size:10.5px; color:var(--dim); }
.ss-accesscell { display:flex; flex-direction:column; gap:3px; }
.scope-badge { font-family:var(--font-mono); font-size:9px; border:1px solid var(--line); padding:2px 7px; border-radius:var(--radius); letter-spacing:.06em; align-self:flex-start; }
.ss-visible { font-size:10px; color:var(--faint); }
.ss-owner { font-family:var(--font-mono); font-size:10.5px; color:var(--dim); }
.ss-when { font-family:var(--font-mono); font-size:10px; color:var(--faint); }
.ss-actions { display:flex; gap:6px; justify-content:flex-end; }
.ss-actions form { margin:0; }
.ss-iconbtn { width:24px; height:24px; border:1px solid var(--line); border-radius:var(--radius); display:flex; align-items:center; justify-content:center; font-size:12px; cursor:pointer; user-select:none; background:transparent; }
.ss-iconbtn--load { color:var(--acc2); text-decoration:none; }
.ss-iconbtn--load:hover { border-color:var(--acc); background:var(--accdim); }
.ss-iconbtn--del { color:var(--dim); }
.ss-iconbtn--del:hover { color:var(--critT); border-color:var(--crit); }
.ss-empty { padding:12px; font-family:var(--font-mono); font-size:10px; color:var(--faint); text-align:center; letter-spacing:.06em; }
.ss-foot { font-family:var(--font-mono); font-size:10px; color:var(--faint); letter-spacing:.04em; }

/* ---- script detail ---- */
.sd-page { padding:18px 22px 32px; display:flex; flex-direction:column; gap:14px; max-width:1100px; }
.sd-head { display:flex; align-items:center; gap:12px; flex-wrap:wrap; }
.sd-back { font-family:var(--font-mono); font-size:11px; }
.sd-name { font-size:16px; font-weight:600; font-family:var(--font-mono); }
.sd-meta { font-family:var(--font-mono); font-size:10px; color:var(--faint); }
.sd-sqlcard, .sd-col { border:1px solid var(--line); border-radius:var(--radius); background:var(--surface); }
.sd-cardhdr { padding:8px 12px; border-bottom:1px solid var(--line); font-family:var(--font-mono); font-size:10px; letter-spacing:.1em; color:var(--dim); }
.sd-sql { padding:12px 14px; font-family:var(--font-mono); font-size:12.5px; line-height:1.7; color:var(--text); white-space:pre-wrap; }
.sd-grid { display:grid; grid-template-columns:1fr 1.4fr; gap:14px; align-items:start; }
.sd-cardbody { padding:12px; display:flex; flex-direction:column; gap:10px; }
.sd-scoperow { display:flex; gap:6px; flex-wrap:wrap; }
.sd-scopebtn { padding:4px 10px; border:1px solid var(--line); color:var(--faint); background:transparent; font-family:var(--font-mono); font-size:10px; cursor:pointer; border-radius:var(--radius); user-select:none; letter-spacing:.06em; }
.sd-scopebtn--active { border-color:var(--acc); color:var(--acc2); background:var(--accbg); }
.sd-managed { font-family:var(--font-mono); font-size:10px; border:1px solid var(--line); padding:3px 8px; border-radius:var(--radius); letter-spacing:.06em; align-self:flex-start; }
.sd-note { font-size:12px; color:var(--mut); }
.sd-legend { font-family:var(--font-mono); font-size:9.5px; color:var(--faint); letter-spacing:.04em; line-height:1.6; }
.sd-search { width:100%; box-sizing:border-box; background:var(--raised); border:1px solid var(--line); border-radius:var(--radius); color:var(--text); font-family:var(--font-mono); font-size:11px; padding:6px 9px; }
.sd-targets { border:1px solid var(--line2); border-radius:var(--radius); max-height:150px; overflow-y:auto; }
.sd-target { display:flex; align-items:center; gap:10px; padding:6px 10px; border-bottom:1px solid var(--line2); font-family:var(--font-mono); cursor:pointer; background:transparent; width:100%; text-align:left; border:none; border-bottom:1px solid var(--line2); }
.sd-target--active { background:var(--accbg); }
.sd-target:hover { background:var(--raised); }
.sd-targetlabel { font-size:11px; font-weight:600; color:var(--text); }
.sd-targetkind { font-size:9px; border:1px solid var(--line); padding:0 5px; border-radius:var(--radius-badge); letter-spacing:.06em; margin-left:auto; }
.sd-chiprow { display:flex; gap:8px; align-items:center; flex-wrap:wrap; }
.sd-chiplabel { font-family:var(--font-mono); font-size:9.5px; letter-spacing:.1em; color:var(--faint); width:70px; flex-shrink:0; }
.sd-chip { padding:3px 9px; border:1px solid var(--line); color:var(--mut); background:transparent; font-family:var(--font-mono); font-size:10px; cursor:pointer; border-radius:var(--radius); user-select:none; }
.sd-chip--active { border-color:var(--acc); color:var(--acc2); background:var(--accbg); }
.sd-run { display:block; text-align:center; padding:7px 14px; border-radius:var(--radius); font-family:var(--font-mono); font-size:11px; letter-spacing:.06em; user-select:none; border:1px solid var(--line); color:var(--faint); text-decoration:none; }
.sd-run--ready { border-color:var(--acc); color:var(--acc2); background:var(--accbg); cursor:pointer; }
.sd-runhint { font-family:var(--font-mono); font-size:9.5px; color:var(--faint); letter-spacing:.04em; line-height:1.6; }

/* ---- console saved-script search dropdown (embedded by ly-ae6.8) ---- */
.ssx-menu { width:290px; background:var(--surface); border:1px solid var(--line); border-radius:var(--radius); box-shadow:var(--shadow-pop); max-height:280px; overflow-y:auto; }
.ssx-item { display:flex; flex-direction:column; gap:2px; padding:7px 10px; border-bottom:1px solid var(--line2); text-decoration:none; }
.ssx-item:hover { background:var(--raised); }
.ssx-itemtop { display:flex; justify-content:space-between; gap:8px; align-items:center; }
.ssx-name { font-size:11px; font-weight:600; color:var(--text); overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
.ssx-scope { font-size:9px; border:1px solid var(--line); padding:0 4px; border-radius:var(--radius-badge); flex-shrink:0; }
.ssx-desc { font-size:10px; color:var(--dim); font-family:var(--font-ui); }
.ssx-empty { padding:12px 10px; font-size:10px; color:var(--faint); text-align:center; letter-spacing:.06em; }
.ssx-manage { display:block; padding:7px 10px; font-size:10px; color:var(--acc2); letter-spacing:.06em; text-decoration:none; }
.ssx-manage:hover { background:var(--raised); }
```

- [ ] **Step 7: Link the stylesheet in the layout**

In `web/layout.templ`, after line 27 (`<link rel="stylesheet" href="/static/css/legacy.css"/>`), add:

```html
				<link rel="stylesheet" href="/static/css/scripts.css"/>
```

- [ ] **Step 8: Write the CSS + layout guard test**

Create `web/scripts_css_test.go`:

```go
package web

import (
	"os"
	"strings"
	"testing"
)

func TestScriptsCSS_usesTokensNoExternalHosts(t *testing.T) {
	body, err := os.ReadFile("static/css/scripts.css")
	if err != nil {
		t.Fatalf("read scripts.css: %v", err)
	}
	css := string(body)
	if !strings.Contains(css, "var(--") {
		t.Error("scripts.css must be token-styled (no var(--...) references found)")
	}
	for _, host := range []string{"http://", "https://", "unpkg.com", "googleapis.com", "cdn."} {
		if strings.Contains(css, host) {
			t.Errorf("scripts.css references external host %q — assets must be self-hosted", host)
		}
	}
}

func TestLayout_LinksScriptsCSS(t *testing.T) {
	html := renderLayout(t)
	if !strings.Contains(html, `href="/static/css/scripts.css"`) {
		t.Error("layout is missing the scripts.css link")
	}
}
```

- [ ] **Step 9: Write the list handler + context helpers + route**

Create `internal/api/scripts.go`:

```go
package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/dobbo-ca/lynceus/web"
)

// viewerFromContext is the principal whose visibility + ownership the Saved
// Scripts surface resolves against. Every Saved Scripts handler only ever
// executes under DevAuth (withAuth 401s all non-static requests otherwise —
// see server.go), so the X-Dev-Actor impersonation header below is inherently
// a dev-only convenience: it lets tests exercise non-owner / non-visible
// paths without wiring real auth. Real OIDC actor resolution replaces this
// whole helper in Milestone 5 (see actorFromContext).
func viewerFromContext(r *http.Request) string {
	if a := r.Header.Get("X-Dev-Actor"); a != "" {
		return a
	}
	return actorFromContext(r)
}

// groupFromContext is the viewer's group for TEAM-scope visibility. Under
// DevAuth this is the fixed dba-oncall group the design references; real
// group membership arrives with OIDC/SCIM (Milestone 5).
func groupFromContext(_ *http.Request) string { return "dba-oncall" }

// isAdminFromContext reports whether the viewer may change access / delete
// scripts they do not own. Under DevAuth the dev principal is an admin, unless
// the dev-only X-Dev-Admin: false header drops the privilege — which is how
// tests reach the handler-level 403 (owner-or-admin) branch. Real RBAC
// arrives in Milestone 5.
func (s *Server) isAdminFromContext(r *http.Request) bool {
	if !s.cfg.DevAuth {
		return false
	}
	return r.Header.Get("X-Dev-Admin") != "false"
}

// handleSavedScriptsPage renders the full Saved Scripts list surface.
func (s *Server) handleSavedScriptsPage(w http.ResponseWriter, r *http.Request) {
	vm, err := s.savedScriptsVM(r)
	if err != nil {
		http.Error(w, "load scripts", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.SavedScriptsPage(vm).Render(r.Context(), w)
}

// handleSavedScriptsTable renders just the table fragment for HTMX filtering.
func (s *Server) handleSavedScriptsTable(w http.ResponseWriter, r *http.Request) {
	vm, err := s.savedScriptsVM(r)
	if err != nil {
		http.Error(w, "load scripts", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.SavedScriptsTable(vm).Render(r.Context(), w)
}

// savedScriptsVM loads the viewer's visible scripts, applies the free-text
// filter, and maps them to the list view-model. It returns the store error so
// a config-DB outage surfaces as a 500 (not a silent empty "no scripts
// match" list) — the caller decides the HTTP status.
func (s *Server) savedScriptsVM(r *http.Request) (web.SavedScriptsVM, error) {
	viewer := viewerFromContext(r)
	group := groupFromContext(r)
	q := r.URL.Query().Get("q")

	scripts, err := s.conf.ListVisibleScripts(r.Context(), viewer, group)
	if err != nil {
		return web.SavedScriptsVM{}, fmt.Errorf("list visible scripts: %w", err)
	}
	now := time.Now()
	rows := make([]web.SavedScriptRow, 0, len(scripts))
	for i := range scripts {
		sc := scripts[i]
		if q != "" && !scriptMatches(sc, q) {
			continue
		}
		mine := sc.Owner == viewer
		rows = append(rows, web.SavedScriptRow{
			ID:          sc.ID,
			Name:        sc.Name,
			Description: sc.Description,
			Scope:       sc.Scope,
			ScopeColor:  web.ScriptScopeColor(sc.Scope),
			VisibleTo:   web.ScriptVisibleTo(sc.Scope, sc.Owner, sc.OwnerGroup, mine),
			Owner:       sc.Owner,
			SavedAge:    web.RelativeAge(sc.CreatedAt, now),
			Mine:        mine,
			DetailHref:  fmt.Sprintf("/scripts/%d", sc.ID),
			LoadHref:    fmt.Sprintf("/console?script=%d", sc.ID),
		})
	}
	return web.SavedScriptsVM{
		Query:   q,
		SubLine: scriptsSubLine(len(scripts)),
		Count:   len(rows),
		Rows:    rows,
	}, nil
}

func scriptsSubLine(total int) string {
	return fmt.Sprintf("%d SCRIPTS · GLOBAL — EVERYONE · TEAM — DBA-ONCALL · PERSONAL — OWNER ONLY", total)
}

// scriptMatches is the free-text filter: case-insensitive substring over
// name, description, and scope (mirrors the prototype's scriptMatches).
func scriptMatches(sc store.SavedScript, q string) bool {
	q = toLower(q)
	return contains(sc.Name, q) || contains(sc.Description, q) || contains(sc.Scope, q)
}
```

Add the tiny case-insensitive helpers at the bottom of `internal/api/scripts.go`:

```go
func toLower(s string) string { return strings.ToLower(s) }
func contains(hay, needleLower string) bool {
	return strings.Contains(strings.ToLower(hay), needleLower)
}
```

Add `"strings"` to the import block of `internal/api/scripts.go`.

Register routes in `internal/api/server.go` `routes()` (after line 76, the checks routes):

```go
	s.mux.HandleFunc("GET /scripts", s.handleSavedScriptsPage)
	s.mux.HandleFunc("GET /partial/scripts", s.handleSavedScriptsTable)
```

- [ ] **Step 10: Regenerate templ**

Run: `make templ`
Expected: `web/scripts_templ.go` is created; no errors.

- [ ] **Step 11: Write the handler test**

Create `internal/api/scripts_test.go`:

```go
package api_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/store"
)

// seedScripts inserts a representative script set and returns s.dobson's
// PERSONAL script id.
func seedScripts(t *testing.T, pool *pgxpoolT) int64 {
	t.Helper()
	ctx := context.Background()
	cfg := store.NewConfig(pool)
	must := func(in store.CreateScriptInput) store.SavedScript {
		s, err := cfg.CreateScript(ctx, in)
		if err != nil {
			t.Fatalf("seed %s: %v", in.Name, err)
		}
		return s
	}
	must(store.CreateScriptInput{Name: "dead-tuples-by-table", Description: "Dead tuples per table",
		SQLText: "SELECT relname FROM pg_stat_user_tables", Scope: "GLOBAL", Owner: "m.chen"})
	must(store.CreateScriptInput{Name: "idle-in-transaction", Description: "Idle tx > 15m",
		SQLText: "SELECT pid FROM pg_stat_activity", Scope: "TEAM", Owner: "j.alvarez", OwnerGroup: "dba-oncall"})
	mine := must(store.CreateScriptInput{Name: "replica-lag-quick", Description: "Replay lag per replica",
		SQLText: "SELECT client_addr FROM pg_stat_replication", Scope: "PERSONAL", Owner: "dev-admin"})
	return mine.ID
}

func TestSavedScriptsPage_rendersRowsScopeBadgesAndTokens(t *testing.T) {
	pool, srv := setupAudit(t, apiConfigDevAuth())
	_ = seedScripts(t, pool)

	resp, err := http.Get(srv.URL + "/scripts")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	html := string(body)
	for _, want := range []string{
		"<!doctype html>",
		`id="scripts-table"`,
		`hx-get="/partial/scripts"`,
		`href="/static/css/scripts.css"`,
		"dead-tuples-by-table",   // GLOBAL script name
		"replica-lag-quick",      // owner's PERSONAL script
		"var(--acc2)",            // GLOBAL scope-badge token color
		"visible to everyone in the org",
		`href="/scripts/`,        // detail link
		`/delete`,                // delete form for owned script
	} {
		if !strings.Contains(html, want) {
			t.Errorf("scripts page missing %q", want)
		}
	}
}

func TestSavedScriptsPartial_filtersByQueryAndIsFragment(t *testing.T) {
	pool, srv := setupAudit(t, apiConfigDevAuth())
	_ = seedScripts(t, pool)

	resp, err := http.Get(srv.URL + "/partial/scripts?q=idle")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	html := string(body)
	if strings.Contains(html, "<!doctype html>") {
		t.Error("partial returned a full document; expected a fragment")
	}
	if !strings.Contains(html, "idle-in-transaction") {
		t.Error("q=idle missing the matching script")
	}
	if strings.Contains(html, "dead-tuples-by-table") {
		t.Error("q=idle leaked a non-matching script")
	}
}
```

Add these two small helpers to the TOP of `internal/api/scripts_test.go` so the file references the existing harness without duplicating it:

```go
// pgxpoolT aliases the pool type the shared harness (server_test.go) returns,
// keeping this file's helper signatures readable.
type pgxpoolT = pgxpool.Pool

func apiConfigDevAuth() api.Config { return api.Config{DevAuth: true} }
```

And extend the import block of `internal/api/scripts_test.go` with:

```go
	"github.com/dobbo-ca/lynceus/internal/api"
	"github.com/jackc/pgx/v5/pgxpool"
```

(The `setupAudit` helper — `internal/api/server_test.go:69` — applies the config migrations, which now include `0006_saved_scripts.sql`, and wires a real `store.Config`.)

- [ ] **Step 12: Run the handler + web tests to verify they pass**

Run: `go test ./web/ ./internal/api/ -run 'Scripts|Layout_LinksScriptsCSS|ScriptsCSS' -count=1`
Expected: PASS.

- [ ] **Step 13: Commit**

```bash
git add web/scripts_vm.go web/scripts_vm_test.go web/scripts.templ web/scripts_templ.go web/static/css/scripts.css web/scripts_css_test.go web/layout.templ web/layout_templ.go internal/api/scripts.go internal/api/scripts_test.go internal/api/server.go
git commit -m "feat(web): Saved Scripts list page + token stylesheet + filter (ly-ae6.9)"
```

---

### Task 5: Script detail page — SQL block, ACCESS card, RUN card scaffold

**Files:**
- Modify: `web/scripts_vm.go` (detail + run + access VM structs)
- Modify: `web/scripts.templ` (detail templ + ACCESS card + RUN card templ)
- Create: `internal/api/scripts_detail.go` (detail handler + run-card builder)
- Modify: `internal/api/server.go` (routes)
- Modify: `internal/api/scripts_test.go` (detail render test)

**Interfaces:**
- Consumes: `store.GetVisibleScript` (Task 1 — the visibility-gated read; NOT the ungated `GetScript`), `store.ListScriptTargets` (Task 3), `viewerFromContext`, `groupFromContext` (Task 4), `web.ScriptScopeColor`, `web.ScriptVisibleTo` (Task 4).
- **Privacy gate:** the detail page renders `SQLText`, so it MUST resolve the id through `GetVisibleScript(viewer, group)` — a missing id and a non-visible id both return 404 (indistinguishable), so a PERSONAL script's SQL cannot be read by a non-owner (even a DevAuth admin) via `/scripts/{id}`. Admin override applies only to the WRITE gate (Task 2/6), not this read.
- Produces:
  ```go
  // web/scripts_vm.go
  type ScriptScopeOption struct { Label string; Active bool }
  type ScriptTargetOption struct { Label, Kind, KindColor, Value string; Active bool }
  type ScriptTargetChip struct { Label, Value string; Active bool }
  type ScriptRunVM struct {
      ScriptID int64; TargetQuery string
      Targets []ScriptTargetOption
      Selected bool; NodeChips, DBChips []ScriptTargetChip
      RunReady bool; RunLabel, RunHint, RunHref string
      SelectedTarget string // opaque value, threaded into chip hrefs
  }
  type ScriptDetailVM struct {
      ID int64; Name, Description, SQLText, Scope, ScopeColor, Owner, SavedAge, VisibleTo string
      Mine bool; ScopeOptions []ScriptScopeOption; ManagedBy string
      Run ScriptRunVM
  }
  // web/scripts.templ
  templ ScriptDetailPage(vm ScriptDetailVM)
  templ ScriptAccessCard(vm ScriptDetailVM)
  templ ScriptRunCard(vm ScriptRunVM)
  // internal/api/scripts_detail.go
  func (s *Server) handleScriptDetailPage(w http.ResponseWriter, r *http.Request)
  func (s *Server) buildScriptRunVM(r *http.Request, scriptID int64) web.ScriptRunVM
  ```
  Run-flow value encoding (used by Task 7 too): a target `Value` is `"<kind>|<cluster>|<node-or-db-or-empty>"` where `kind ∈ {cluster,node,db}`.

- [ ] **Step 1: Write the failing detail render test**

Append to `internal/api/scripts_test.go`:

```go
func TestScriptDetailPage_ownerSeesScopeSwitchAndRunCard(t *testing.T) {
	pool, srv := setupAudit(t, apiConfigDevAuth())
	id := seedScripts(t, pool) // owner is dev-admin (the DevAuth viewer)

	resp, err := http.Get(srv.URL + "/scripts/" + itoaTest(id))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	html := string(body)
	for _, want := range []string{
		"replica-lag-quick",                       // script name
		"SELECT client_addr FROM pg_stat_replication", // SQL block (user metadata, not monitored-DB literal)
		`id="access-card"`,
		`id="run-card"`,
		"sd-scopebtn",                             // owner scope switch present
		"SEARCH & SELECT A TARGET",                // initial RUN label
		`hx-get="/partial/scripts/` + itoaTest(id) + `/run"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("detail page missing %q", want)
		}
	}
	// Owner must NOT see the "managed by" read-only badge.
	if strings.Contains(html, "MANAGED BY") {
		t.Error("owner should not see the read-only managed-by badge")
	}
}

func TestScriptDetailPage_nonOwnerSeesManagedBy(t *testing.T) {
	pool, srv := setupAudit(t, apiConfigDevAuth())
	ctx := context.Background()
	cfg := store.NewConfig(pool)
	// Owned by someone else; dev-admin is admin but not the owner.
	other, err := cfg.CreateScript(ctx, store.CreateScriptInput{
		Name: "blocking-lock-tree", Description: "who blocks whom",
		SQLText: "SELECT 1", Scope: "GLOBAL", Owner: "j.alvarez"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	resp, err := http.Get(srv.URL + "/scripts/" + itoaTest(other.ID))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	html := string(body)
	if !strings.Contains(html, "MANAGED BY j.alvarez") {
		t.Error("non-owner should see 'MANAGED BY j.alvarez'")
	}
	if strings.Contains(html, "sd-scopebtn") {
		t.Error("non-owner must not get the scope-switch buttons")
	}
}

func TestScriptDetailPage_nonVisiblePersonalReturns404(t *testing.T) {
	pool, srv := setupAudit(t, apiConfigDevAuth())
	ctx := context.Background()
	cfg := store.NewConfig(pool)
	// A PERSONAL script owned by someone else. The DevAuth viewer (dev-admin)
	// is an admin but NOT the owner, so the detail read must 404 and never
	// leak the SQL — proving the read is visibility-gated, not admin-gated.
	secret, err := cfg.CreateScript(ctx, store.CreateScriptInput{
		Name: "secret-personal", Description: "mine only",
		SQLText: "SELECT secret FROM vault", Scope: "PERSONAL", Owner: "j.alvarez"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	resp, err := http.Get(srv.URL + "/scripts/" + itoaTest(secret.ID))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for a non-visible PERSONAL script", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(body), "SELECT secret FROM vault") {
		t.Error("non-visible PERSONAL script leaked its SQL to a non-owner")
	}
}
```

Add the test-local int formatter at the top of `internal/api/scripts_test.go`:

```go
func itoaTest(id int64) string { return strconv.FormatInt(id, 10) }
```

Extend the import block with `"strconv"`.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/api/ -run TestScriptDetailPage -count=1`
Expected: FAIL — route `/scripts/{id}` not registered (404) / `ScriptDetailPage` undefined.

- [ ] **Step 3: Add the detail view-model structs**

Append to `web/scripts_vm.go`:

```go
// ScriptScopeOption is one selectable scope in the owner's ACCESS switch.
type ScriptScopeOption struct {
	Label  string // GLOBAL | TEAM | PERSONAL
	Active bool
}

// ScriptTargetOption is one match in the run-flow target search list.
type ScriptTargetOption struct {
	Label     string // cluster/node/database label
	Kind      string // CLUSTER | NODE | DATABASE
	KindColor string // token var
	Value     string // "<kind>|<cluster>|<node-or-db-or-empty>"
	Active    bool
}

// ScriptTargetChip is one node/database chip once a target is selected.
type ScriptTargetChip struct {
	Label  string
	Value  string // accumulated run-state value threaded into the chip href
	Active bool
}

// ScriptRunVM is the RUN card state (search → select target → pick node/db
// → RUN). It is re-rendered as a fragment on every selection.
type ScriptRunVM struct {
	ScriptID       int64
	TargetQuery    string
	Targets        []ScriptTargetOption
	Selected       bool
	SelectedTarget string // opaque target value threaded into chip hrefs
	NodeChips      []ScriptTargetChip
	DBChips        []ScriptTargetChip
	RunReady       bool
	RunLabel       string
	RunHint        string
	RunHref        string // /console?...  set only when RunReady
}

// ScriptDetailVM is the full script detail surface.
type ScriptDetailVM struct {
	ID           int64
	Name         string
	Description  string
	SQLText      string
	Scope        string
	ScopeColor   string
	Owner        string
	SavedAge     string
	VisibleTo    string
	Mine         bool
	ScopeOptions []ScriptScopeOption // populated only when Mine
	ManagedBy    string              // owner name, shown to non-owners
	Run          ScriptRunVM
}
```

- [ ] **Step 4: Write the detail + access + run templ**

Append to `web/scripts.templ`:

```go
// ScriptDetailPage renders one saved script: SQL block, ACCESS card, RUN card.
templ ScriptDetailPage(vm ScriptDetailVM) {
	@Layout("Lynceus — "+vm.Name, "saved script") {
		<div class="sd-page">
			<div class="sd-head">
				<a class="sd-back" href="/scripts">← SAVED SCRIPTS</a>
				<span class="sd-name">{ vm.Name }</span>
				<span class="scope-badge" style={ "color:" + vm.ScopeColor }>{ vm.Scope }</span>
				<span class="sd-meta">{ vm.Owner } · { vm.SavedAge }</span>
			</div>
			<div class="sd-sqlcard">
				<div class="sd-cardhdr">SQL — { vm.Description }</div>
				<div class="sd-sql">{ vm.SQLText }</div>
			</div>
			<div class="sd-grid">
				@ScriptAccessCard(vm)
				@ScriptRunCard(vm.Run)
			</div>
		</div>
	}
}

// ScriptAccessCard is the ACCESS panel; its wrapping div is the HTMX swap
// target for scope changes.
templ ScriptAccessCard(vm ScriptDetailVM) {
	<div id="access-card" class="sd-col">
		<div class="sd-cardhdr">ACCESS — WHO CAN SEE &amp; RUN THIS SCRIPT</div>
		<div class="sd-cardbody">
			if vm.Mine {
				<div class="sd-scoperow">
					for _, so := range vm.ScopeOptions {
						<button class={ "sd-scopebtn", templ.KV("sd-scopebtn--active", so.Active) }
							hx-post={ "/scripts/" + itoa(vm.ID) + "/scope" }
							hx-vals={ `{"scope":"` + so.Label + `"}` }
							hx-target="#access-card" hx-swap="outerHTML" type="button">{ so.Label }</button>
					}
				</div>
			} else {
				<span class="sd-managed" style={ "color:" + vm.ScopeColor }>{ vm.Scope } · MANAGED BY { vm.ManagedBy }</span>
			}
			<span class="sd-note">Visible to { vm.VisibleTo }.</span>
			<span class="sd-legend">GLOBAL — EVERYONE · TEAM — DBA-ONCALL · PERSONAL — OWNER ONLY. SCOPE CHANGES ARE AUDITED.</span>
		</div>
	</div>
}

// ScriptRunCard is the RUN panel; its wrapping div is the HTMX swap target
// for target-search / node / database selection.
templ ScriptRunCard(vm ScriptRunVM) {
	<div id="run-card" class="sd-col">
		<div class="sd-cardhdr">RUN — SEARCH A CLUSTER, NODE OR DATABASE</div>
		<div class="sd-cardbody">
			<input class="sd-search" type="search" name="q" value={ vm.TargetQuery }
				placeholder="search cluster / node / database…"
				hx-get={ "/partial/scripts/" + itoa(vm.ScriptID) + "/run" }
				hx-target="#run-card" hx-swap="outerHTML"
				hx-trigger="input changed delay:200ms, search"/>
			<div class="sd-targets">
				if len(vm.Targets) == 0 {
					<div class="ssx-empty">NOTHING MATCHES</div>
				} else {
					for _, tg := range vm.Targets {
						<button class={ "sd-target", templ.KV("sd-target--active", tg.Active) } type="button"
							hx-get={ "/partial/scripts/" + itoa(vm.ScriptID) + "/run?target=" + urlq(tg.Value) }
							hx-target="#run-card" hx-swap="outerHTML">
							<span class="sd-targetlabel" style={ "color:" + tg.KindColor }>{ tg.Label }</span>
							<span class="sd-targetkind" style={ "color:" + tg.KindColor }>{ tg.Kind }</span>
						</button>
					}
				}
			</div>
			if vm.Selected {
				<div class="sd-chiprow">
					<span class="sd-chiplabel">NODE</span>
					for _, c := range vm.NodeChips {
						<button class={ "sd-chip", templ.KV("sd-chip--active", c.Active) } type="button"
							hx-get={ "/partial/scripts/" + itoa(vm.ScriptID) + "/run?" + c.Value }
							hx-target="#run-card" hx-swap="outerHTML">{ c.Label }</button>
					}
				</div>
				<div class="sd-chiprow">
					<span class="sd-chiplabel">DATABASE</span>
					for _, c := range vm.DBChips {
						<button class={ "sd-chip", templ.KV("sd-chip--active", c.Active) } type="button"
							hx-get={ "/partial/scripts/" + itoa(vm.ScriptID) + "/run?" + c.Value }
							hx-target="#run-card" hx-swap="outerHTML">{ c.Label }</button>
					}
				</div>
			}
			if vm.RunReady {
				<a class="sd-run sd-run--ready" href={ templ.SafeURL(vm.RunHref) }>{ vm.RunLabel }</a>
			} else {
				<span class="sd-run">{ vm.RunLabel }</span>
			}
			<span class="sd-runhint">{ vm.RunHint }</span>
		</div>
	</div>
}
```

Add the URL-query escaper to `web/scripts_vm.go` (used by the templ):

```go
// urlq percent-encodes a value for use in a query string.
func urlq(v string) string { return url.QueryEscape(v) }
```

Add `"net/url"` to the imports of `web/scripts_vm.go`.

- [ ] **Step 5: Write the detail handler + run-VM builder scaffold**

Create `internal/api/scripts_detail.go`:

```go
package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/dobbo-ca/lynceus/web"
)

// parseScriptID extracts and validates the {id} path value.
func parseScriptID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// handleScriptDetailPage renders the full script detail surface.
func (s *Server) handleScriptDetailPage(w http.ResponseWriter, r *http.Request) {
	id, ok := parseScriptID(r)
	if !ok {
		http.Error(w, "bad script id", http.StatusBadRequest)
		return
	}
	viewer := viewerFromContext(r)
	group := groupFromContext(r)
	// Visibility-gated read: GetVisibleScript returns found=false for a
	// missing OR non-visible id, so a non-owner (even an admin) gets a 404 on
	// someone else's PERSONAL script and never sees its SQL. Do NOT use the
	// ungated GetScript here.
	sc, found, err := s.conf.GetVisibleScript(r.Context(), id, viewer, group)
	if err != nil {
		http.Error(w, "load script", http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(w, "script not found", http.StatusNotFound)
		return
	}

	mine := sc.Owner == viewer
	vm := web.ScriptDetailVM{
		ID:          sc.ID,
		Name:        sc.Name,
		Description: sc.Description,
		SQLText:     sc.SQLText,
		Scope:       sc.Scope,
		ScopeColor:  web.ScriptScopeColor(sc.Scope),
		Owner:       sc.Owner,
		SavedAge:    web.RelativeAge(sc.CreatedAt, timeNow()),
		VisibleTo:   web.ScriptVisibleTo(sc.Scope, sc.Owner, sc.OwnerGroup, mine),
		Mine:        mine,
		ManagedBy:   sc.Owner,
		Run:         s.buildScriptRunVM(r, sc.ID),
	}
	if mine {
		vm.ScopeOptions = scopeOptions(sc.Scope)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.ScriptDetailPage(vm).Render(r.Context(), w)
}

// scopeOptions builds the three owner scope-switch buttons, marking current.
func scopeOptions(current string) []web.ScriptScopeOption {
	out := make([]web.ScriptScopeOption, 0, 3)
	for _, sc := range []string{"GLOBAL", "TEAM", "PERSONAL"} {
		out = append(out, web.ScriptScopeOption{Label: sc, Active: sc == current})
	}
	return out
}

// errForbidden / errNotFound thread store sentinels to HTTP statuses.
func scriptWriteStatus(err error) int {
	switch {
	case errors.Is(err, store.ErrScriptForbidden):
		return http.StatusForbidden
	case errors.Is(err, store.ErrScriptNotFound):
		return http.StatusNotFound
	default:
		return http.StatusInternalServerError
	}
}

var _ = strings.TrimSpace // retained for Task 7 target parsing
```

Add a tiny time seam at the bottom of `internal/api/scripts.go` (so tests stay deterministic if later needed; default is real time):

```go
// timeNow is the clock used by the Saved Scripts handlers. A package var so
// tests can pin it if they need deterministic relative ages.
var timeNow = time.Now
```

For this task, `buildScriptRunVM` returns the initial (nothing-selected) state; Task 7 fills in target search + selection. Add the scaffold to `internal/api/scripts_detail.go`:

```go
// buildScriptRunVM assembles the RUN card. Task 7 wires target search and
// node/database selection from the ?target= / ?node= / ?db= / ?q= params;
// this scaffold renders the initial (no-selection) state so the detail page
// renders end-to-end.
func (s *Server) buildScriptRunVM(r *http.Request, scriptID int64) web.ScriptRunVM {
	q := r.URL.Query().Get("q")
	targets := s.scriptTargetOptions(r, q, "")
	return web.ScriptRunVM{
		ScriptID:    scriptID,
		TargetQuery: q,
		Targets:     targets,
		Selected:    false,
		RunReady:    false,
		RunLabel:    "SEARCH & SELECT A TARGET",
		RunHint:     "THE RUN LANDS IN THE SQL CONSOLE — EVERY STATEMENT IS AUDITED.",
	}
}
```

Add the target-options builder (used here and expanded in Task 7) to `internal/api/scripts_detail.go`:

```go
// scriptTargetOptions loads the fleet target index, filters by free-text q,
// and marks the option whose Value == selectedValue active.
func (s *Server) scriptTargetOptions(r *http.Request, q, selectedValue string) []web.ScriptTargetOption {
	targets, err := s.conf.ListScriptTargets(r.Context())
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []web.ScriptTargetOption
	add := func(label, kind, color, value string) {
		if seen[value] {
			return
		}
		seen[value] = true
		if q != "" && !contains(label, toLower(q)) && !contains(kind, toLower(q)) {
			return
		}
		out = append(out, web.ScriptTargetOption{
			Label: label, Kind: kind, KindColor: color, Value: value,
			Active: value == selectedValue,
		})
	}
	for _, tg := range targets {
		add(tg.Cluster, "CLUSTER", "var(--text)", "cluster|"+tg.Cluster+"|")
		add(tg.Node, "NODE", "var(--infoT)", "node|"+tg.Cluster+"|"+tg.Node)
		if tg.Database != "" {
			add(tg.Cluster+"/"+tg.Database, "DATABASE", "var(--mut)", "db|"+tg.Cluster+"|"+tg.Database)
		}
	}
	return out
}
```

Register routes in `internal/api/server.go` `routes()` (after the Task-4 script routes):

```go
	s.mux.HandleFunc("GET /scripts/{id}", s.handleScriptDetailPage)
```

Add `"time"` to the import block of `internal/api/scripts.go` if not already present (it is, from Task 4).

- [ ] **Step 6: Regenerate templ**

Run: `make templ`
Expected: `web/scripts_templ.go` regenerated with the new components; no errors.

- [ ] **Step 7: Run the detail tests to verify they pass**

Run: `go test ./internal/api/ -run TestScriptDetailPage -count=1`
Expected: PASS (owner scope-switch case, non-owner GLOBAL managed-by case, and the non-visible PERSONAL 404 case).

- [ ] **Step 8: Commit**

```bash
git add web/scripts_vm.go web/scripts.templ web/scripts_templ.go internal/api/scripts.go internal/api/scripts_detail.go internal/api/scripts_test.go internal/api/server.go
git commit -m "feat(web): saved-script detail page — SQL block, ACCESS card, RUN card scaffold (ly-ae6.9)"
```

---

### Task 6: Write endpoints — create, audited scope-change, delete

**Files:**
- Create: `internal/api/scripts_write.go` (create + scope-change + delete handlers)
- Modify: `internal/api/server.go` (routes)
- Modify: `internal/api/scripts_test.go` (write-path tests)

**Interfaces:**
- Consumes: `store.CreateScript`, `store.SetScriptScope`, `store.DeleteScript` (Tasks 1–2); `scriptWriteStatus`, `parseScriptID`, `scopeOptions` (Task 5); `viewerFromContext`, `groupFromContext`, `isAdminFromContext` (Task 4); `web.ScriptAccessCard`, `web.SavedScriptsTable` (Tasks 4–5).
- Produces:
  ```go
  func (s *Server) handleScriptCreate(w http.ResponseWriter, r *http.Request)      // POST /scripts
  func (s *Server) handleScriptScopeChange(w http.ResponseWriter, r *http.Request) // POST /scripts/{id}/scope
  func (s *Server) handleScriptDelete(w http.ResponseWriter, r *http.Request)      // POST /scripts/{id}/delete
  ```
  Create accepts form fields `name`, `description`, `sql`, `scope`; on success redirects to `/scripts/{newID}`. Scope-change re-renders the ACCESS card fragment. Delete re-renders the list-table fragment.

- [ ] **Step 1: Write the failing write-path tests**

Append to `internal/api/scripts_test.go`:

```go
func TestScriptScopeChange_ownerUpdatesAndAudits(t *testing.T) {
	pool, srv := setupAudit(t, apiConfigDevAuth())
	id := seedScripts(t, pool) // owner == dev-admin

	form := "scope=GLOBAL"
	resp, err := http.Post(srv.URL+"/scripts/"+itoaTest(id)+"/scope",
		"application/x-www-form-urlencoded", strings.NewReader(form))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	html := string(body)
	if strings.Contains(html, "<!doctype html>") {
		t.Error("scope change should return the access-card fragment, not a full page")
	}
	if !strings.Contains(html, `id="access-card"`) {
		t.Error("scope change response missing the access-card fragment")
	}
	if !strings.Contains(html, "everyone in the org") {
		t.Error("scope change did not reflect the new GLOBAL visibility")
	}

	ctx := context.Background()
	cfg := store.NewConfig(pool)
	sc, _, _ := cfg.GetScript(ctx, id)
	if sc.Scope != "GLOBAL" {
		t.Errorf("persisted scope = %q, want GLOBAL", sc.Scope)
	}
	var n int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action='saved_script.scope.change'`).Scan(&n)
	if n != 1 {
		t.Errorf("scope-change audit rows = %d, want 1", n)
	}
}

// TestScriptScopeChange_devAuthDisabledReturns401 pins the MIDDLEWARE gate:
// with DevAuth off, withAuth 401s before any script handler runs. (This is a
// distinct layer from the handler's own 403 owner/admin gate, covered below.)
func TestScriptScopeChange_devAuthDisabledReturns401(t *testing.T) {
	pool, srv := setupAudit(t, api.Config{DevAuth: false}) // no dev admin, unauthorized before handler
	_ = pool
	resp, err := http.Post(srv.URL+"/scripts/1/scope",
		"application/x-www-form-urlencoded", strings.NewReader("scope=GLOBAL"))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 (DevAuth off)", resp.StatusCode)
	}
}

// TestScriptScopeChange_handlerForbidsNonOwnerNonAdmin exercises the HANDLER's
// 403 path (scriptWriteStatus mapping store.ErrScriptForbidden ->
// http.StatusForbidden). It impersonates a principal who is neither the owner
// nor an admin via the dev-only X-Dev-Actor / X-Dev-Admin headers, so the
// request passes DevAuth's middleware but the store's owner-or-admin gate
// rejects it. Also asserts no scope change persisted and no audit row written.
func TestScriptScopeChange_handlerForbidsNonOwnerNonAdmin(t *testing.T) {
	pool, srv := setupAudit(t, apiConfigDevAuth())
	id := seedScripts(t, pool) // owner == dev-admin, scope == PERSONAL

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/scripts/"+itoaTest(id)+"/scope",
		strings.NewReader("scope=GLOBAL"))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Dev-Actor", "intruder") // not the owner
	req.Header.Set("X-Dev-Admin", "false")     // not an admin
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (handler owner/admin gate)", resp.StatusCode)
	}

	ctx := context.Background()
	cfg := store.NewConfig(pool)
	sc, _, _ := cfg.GetScript(ctx, id)
	if sc.Scope != "PERSONAL" {
		t.Errorf("scope changed to %q despite 403", sc.Scope)
	}
	var n int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action='saved_script.scope.change'`).Scan(&n)
	if n != 0 {
		t.Errorf("forbidden scope change wrote %d audit rows, want 0", n)
	}
}

func TestScriptDelete_removesRowAndReturnsTableFragment(t *testing.T) {
	pool, srv := setupAudit(t, apiConfigDevAuth())
	id := seedScripts(t, pool)

	resp, err := http.Post(srv.URL+"/scripts/"+itoaTest(id)+"/delete",
		"application/x-www-form-urlencoded", strings.NewReader(""))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `id="scripts-table"`) {
		t.Error("delete should return the refreshed scripts-table fragment")
	}
	ctx := context.Background()
	if _, ok, _ := store.NewConfig(pool).GetScript(ctx, id); ok {
		t.Error("script still present after delete")
	}
}

func TestScriptCreate_insertsAndRedirects(t *testing.T) {
	pool, srv := setupAudit(t, apiConfigDevAuth())

	// Do not auto-follow the redirect so we can assert on Location.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	form := "name=new-script&description=made+in+test&sql=SELECT+42&scope=TEAM"
	resp, err := client.Post(srv.URL+"/scripts",
		"application/x-www-form-urlencoded", strings.NewReader(form))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); !strings.HasPrefix(loc, "/scripts/") {
		t.Errorf("Location = %q, want /scripts/<id>", loc)
	}
	var n int
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM saved_scripts WHERE name='new-script'`).Scan(&n)
	if n != 1 {
		t.Errorf("created rows = %d, want 1", n)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/api/ -run 'TestScriptScopeChange|TestScriptDelete|TestScriptCreate' -count=1`
Expected: FAIL — routes not registered, so the handler cases 404 (the `handlerForbidsNonOwnerNonAdmin` case sees 404, not the eventual 403). The `devAuthDisabledReturns401` case passes even now, because the middleware 401s before any route lookup.

- [ ] **Step 3: Implement the write handlers**

Create `internal/api/scripts_write.go`:

```go
package api

import (
	"fmt"
	"net/http"

	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/dobbo-ca/lynceus/web"
)

// handleScriptCreate inserts a saved script from the console SAVE ▾ form
// (POST /scripts) and redirects to the new script's detail page. The console
// (ly-ae6.8) posts name/description/sql/scope; this is the CRUD create seam.
func (s *Server) handleScriptCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	scope := r.PostForm.Get("scope")
	if !store.ValidScriptScope(scope) {
		http.Error(w, "invalid scope", http.StatusBadRequest)
		return
	}
	viewer := viewerFromContext(r)
	group := ""
	if scope == "TEAM" {
		group = groupFromContext(r)
	}
	sc, err := s.conf.CreateScript(r.Context(), store.CreateScriptInput{
		Name:        r.PostForm.Get("name"),
		Description: r.PostForm.Get("description"),
		SQLText:     r.PostForm.Get("sql"),
		Scope:       scope,
		Owner:       viewer,
		OwnerGroup:  group,
	})
	if err != nil {
		http.Error(w, "create script", http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/scripts/%d", sc.ID), http.StatusSeeOther)
}

// handleScriptScopeChange changes a script's scope (owner or admin only) and
// re-renders the ACCESS card fragment. The store append-audits the change.
func (s *Server) handleScriptScopeChange(w http.ResponseWriter, r *http.Request) {
	id, ok := parseScriptID(r)
	if !ok {
		http.Error(w, "bad script id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	newScope := r.PostForm.Get("scope")
	sc, err := s.conf.SetScriptScope(r.Context(), id, newScope,
		viewerFromContext(r), s.isAdminFromContext(r))
	if err != nil {
		http.Error(w, "set scope", scriptWriteStatus(err))
		return
	}
	viewer := viewerFromContext(r)
	mine := sc.Owner == viewer
	vm := web.ScriptDetailVM{
		ID:         sc.ID,
		Scope:      sc.Scope,
		ScopeColor: web.ScriptScopeColor(sc.Scope),
		Owner:      sc.Owner,
		VisibleTo:  web.ScriptVisibleTo(sc.Scope, sc.Owner, sc.OwnerGroup, mine),
		Mine:       mine,
		ManagedBy:  sc.Owner,
	}
	if mine {
		vm.ScopeOptions = scopeOptions(sc.Scope)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.ScriptAccessCard(vm).Render(r.Context(), w)
}

// handleScriptDelete deletes a script (owner or admin only) and re-renders
// the list-table fragment so the row disappears in place.
func (s *Server) handleScriptDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := parseScriptID(r)
	if !ok {
		http.Error(w, "bad script id", http.StatusBadRequest)
		return
	}
	if err := s.conf.DeleteScript(r.Context(), id,
		viewerFromContext(r), s.isAdminFromContext(r)); err != nil {
		http.Error(w, "delete script", scriptWriteStatus(err))
		return
	}
	// Refresh the table for the HTMX swap.
	vm, err := s.savedScriptsVM(r)
	if err != nil {
		http.Error(w, "load scripts", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.SavedScriptsTable(vm).Render(r.Context(), w)
}
```

Register routes in `internal/api/server.go` `routes()` (after the detail route):

```go
	s.mux.HandleFunc("POST /scripts", s.handleScriptCreate)
	s.mux.HandleFunc("POST /scripts/{id}/scope", s.handleScriptScopeChange)
	s.mux.HandleFunc("POST /scripts/{id}/delete", s.handleScriptDelete)
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/api/ -run 'TestScriptScopeChange|TestScriptDelete|TestScriptCreate' -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/scripts_write.go internal/api/scripts_test.go internal/api/server.go
git commit -m "feat(api): saved-script create + audited scope-change + delete endpoints (ly-ae6.9)"
```

---

### Task 7: Run-flow — target resolution, node/database chips, console hand-off

**Files:**
- Modify: `internal/api/scripts_detail.go` (flesh out `buildScriptRunVM`, add `handleScriptRunCard`, hand-off URL builder)
- Modify: `internal/api/server.go` (route)
- Modify: `internal/api/scripts_test.go` (run-flow tests)

**Interfaces:**
- Consumes: `store.ListScriptTargets` (Task 3); `scriptTargetOptions` (Task 5); `web.ScriptRunVM`, `web.ScriptTargetChip`, `web.ScriptRunCard` (Task 5).
- Produces:
  ```go
  func (s *Server) handleScriptRunCard(w http.ResponseWriter, r *http.Request) // GET /partial/scripts/{id}/run
  func consoleHandoffURL(scriptID int64, cluster, node, db string, run bool) string
  ```
  Run-state query params on `/partial/scripts/{id}/run`: `q` (search text), `target` (`"<kind>|<cluster>|<node-or-db>"`), `node`, `db`. RUN is ready only when a target is selected AND both node and db resolve; `RunHref` = `consoleHandoffURL(id, cluster, node, db, true)` — the `true` emits `&run=1` (execute-intent), which is what distinguishes RUN from the list/search LOAD hand-off (`run=false`, no `run=1`).

- [ ] **Step 1: Write the failing run-flow test**

Append to `internal/api/scripts_test.go`:

```go
func TestScriptRun_targetSelectionThenNodeDBEnablesRun(t *testing.T) {
	pool, srv := setupAudit(t, apiConfigDevAuth())
	id := seedScripts(t, pool)
	seedRunFleet(t, pool) // one cluster / node / database

	base := srv.URL + "/partial/scripts/" + itoaTest(id) + "/run"

	// 1. No selection: RUN inert, prompts to select a target.
	html := getBody(t, base)
	if !strings.Contains(html, "SEARCH & SELECT A TARGET") {
		t.Error("initial run card should prompt to select a target")
	}
	if !strings.Contains(html, "orders-prod") {
		t.Error("target search should list the cluster")
	}

	// 2. Select the cluster target: NODE + DATABASE chip rows appear, RUN still inert.
	html = getBody(t, base+"?target="+url.QueryEscape("cluster|orders-prod|"))
	if !strings.Contains(html, "SELECT NODE &amp; DATABASE TO RUN") {
		t.Errorf("after cluster select, expected node/db prompt; got:\n%s", html)
	}
	if !strings.Contains(html, "srv-orders-primary") {
		t.Error("node chips missing after cluster select")
	}

	// 3. Pick node + database: RUN becomes a live console hand-off link.
	sel := "?target=" + url.QueryEscape("cluster|orders-prod|") +
		"&node=srv-orders-primary&db=orders"
	html = getBody(t, base+sel)
	if !strings.Contains(html, "RUN ON srv-orders-primary · orders →") {
		t.Errorf("ready run label missing; got:\n%s", html)
	}
	if !strings.Contains(html, `href="/console?`) {
		t.Error("ready RUN must link to the console hand-off URL")
	}
	if !strings.Contains(html, "script="+itoaTest(id)) ||
		!strings.Contains(html, "node=srv-orders-primary") ||
		!strings.Contains(html, "db=orders") {
		t.Error("hand-off URL missing script/node/db params")
	}
	// RUN carries execute-intent (run=1); the console decides grant-vs-gate.
	if !strings.Contains(html, "run=1") {
		t.Error("RUN hand-off must carry run=1 (execute-intent) — otherwise it is indistinguishable from a load")
	}
}

func TestScriptRun_loadHrefHasNoRunParam(t *testing.T) {
	// The list-row LOAD hand-off is load-without-run: /console?script=<id>
	// with NO run=1, so it can never be confused with the RUN card's
	// execute-intent link. (Guards the run=false branch of consoleHandoffURL.)
	pool, srv := setupAudit(t, apiConfigDevAuth())
	_ = seedScripts(t, pool)
	html := getBody(t, srv.URL+"/scripts")
	if !strings.Contains(html, `href="/console?script=`) {
		t.Error("list is missing the load-into-console link")
	}
	if strings.Contains(html, "run=1") {
		t.Error("the list LOAD link must NOT carry run=1 (load-without-run)")
	}
}
```

Add the fleet seed + `getBody` helpers to `internal/api/scripts_test.go`:

```go
func seedRunFleet(t *testing.T, pool *pgxpoolT) {
	t.Helper()
	ctx := context.Background()
	cfg := store.NewConfig(pool)
	cl, err := cfg.CreateCluster(ctx, "orders-prod")
	if err != nil {
		t.Fatalf("cluster: %v", err)
	}
	in, err := cfg.CreateInstance(ctx, cl.ID, "srv-orders-primary")
	if err != nil {
		t.Fatalf("instance: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO servers (id, name, database_name, instance_id) VALUES ($1,$2,$3,$4)`,
		"srv-run-1", "srv-orders-primary", "orders", in.ID); err != nil {
		t.Fatalf("seed server: %v", err)
	}
}

func getBody(t *testing.T, u string) string {
	t.Helper()
	resp, err := http.Get(u)
	if err != nil {
		t.Fatalf("GET %s: %v", u, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("GET %s status = %d, want 200", u, resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}
```

Add `"net/url"` to the import block of `internal/api/scripts_test.go`.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/api/ -run TestScriptRun_target -count=1`
Expected: FAIL — route `/partial/scripts/{id}/run` not registered (404).

- [ ] **Step 3: Implement the run-flow**

Replace the scaffold `buildScriptRunVM` in `internal/api/scripts_detail.go` with the full resolver, and add the run-card handler + hand-off builder. Change the file to:

```go
// handleScriptRunCard re-renders the RUN card fragment as the user searches,
// selects a target, and picks node/database. State is carried entirely in
// query params (?q= ?target= ?node= ?db=), keeping the flow stateless.
func (s *Server) handleScriptRunCard(w http.ResponseWriter, r *http.Request) {
	id, ok := parseScriptID(r)
	if !ok {
		http.Error(w, "bad script id", http.StatusBadRequest)
		return
	}
	vm := s.buildScriptRunVM(r, id)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.ScriptRunCard(vm).Render(r.Context(), w)
}

// buildScriptRunVM resolves the RUN card from the request's run-state params.
func (s *Server) buildScriptRunVM(r *http.Request, scriptID int64) web.ScriptRunVM {
	qv := r.URL.Query()
	q := qv.Get("q")
	target := qv.Get("target")

	vm := web.ScriptRunVM{
		ScriptID:       scriptID,
		TargetQuery:    q,
		SelectedTarget: target,
		Targets:        s.scriptTargetOptions(r, q, target),
		RunLabel:       "SEARCH & SELECT A TARGET",
		RunHint:        "THE RUN LANDS IN THE SQL CONSOLE — EVERY STATEMENT IS AUDITED.",
	}
	if target == "" {
		return vm
	}
	vm.Selected = true

	kind, cluster, fixed := parseTargetValue(target)
	nodes, dbs := s.clusterNodesAndDBs(r, cluster)

	nodeFixed, dbFixed := "", ""
	switch kind {
	case "node":
		nodeFixed, nodes = fixed, []string{fixed}
	case "db":
		dbFixed, dbs = fixed, []string{fixed}
	}

	node := chooseValue(qv.Get("node"), nodeFixed, nodes)
	db := chooseValue(qv.Get("db"), dbFixed, dbs)

	vm.NodeChips = runChips(target, "node", nodes, node, node, db)
	vm.DBChips = runChips(target, "db", dbs, db, node, db)

	if node != "" && db != "" {
		vm.RunReady = true
		vm.RunLabel = "RUN ON " + node + " · " + db + " →"
		// run=true == execute-intent. Per the prototype (runScript sets
		// consoleRan: runGranted, Lynceus.dc.html:3502) the console EXECUTES
		// immediately when a session grant is active on the target cluster and
		// otherwise shows the grant gate. That grant decision lives in the
		// console (ly-ae6.8), which owns live grant state; this surface always
		// signals the intent to run. The hint states both branches honestly
		// rather than claiming to know the live grant state here.
		vm.RunHint = "HANDS OFF TO THE SQL CONSOLE TO RUN — IF A SESSION GRANT IS ACTIVE ON THIS CLUSTER IT EXECUTES IMMEDIATELY, OTHERWISE THE CONSOLE ASKS YOU TO REQUEST ONE FIRST. EVERY STATEMENT IS AUDITED."
		vm.RunHref = consoleHandoffURL(scriptID, cluster, node, db, true)
	} else {
		vm.RunLabel = "SELECT NODE & DATABASE TO RUN"
	}
	return vm
}

// clusterNodesAndDBs returns the distinct node names and database names in a
// cluster from the fleet target index.
func (s *Server) clusterNodesAndDBs(r *http.Request, cluster string) (nodes, dbs []string) {
	targets, err := s.conf.ListScriptTargets(r.Context())
	if err != nil {
		return nil, nil
	}
	seenN, seenD := map[string]bool{}, map[string]bool{}
	for _, tg := range targets {
		if tg.Cluster != cluster {
			continue
		}
		if tg.Node != "" && !seenN[tg.Node] {
			seenN[tg.Node] = true
			nodes = append(nodes, tg.Node)
		}
		if tg.Database != "" && !seenD[tg.Database] {
			seenD[tg.Database] = true
			dbs = append(dbs, tg.Database)
		}
	}
	return nodes, dbs
}

// runChips builds node or database chips, each carrying the accumulated
// run-state (target + node + db) so a click advances the selection.
func runChips(target, dim string, values []string, active, curNode, curDB string) []web.ScriptTargetChip {
	out := make([]web.ScriptTargetChip, 0, len(values))
	for _, v := range values {
		node, db := curNode, curDB
		if dim == "node" {
			node = v
		} else {
			db = v
		}
		vals := url.Values{}
		vals.Set("target", target)
		if node != "" {
			vals.Set("node", node)
		}
		if db != "" {
			vals.Set("db", db)
		}
		out = append(out, web.ScriptTargetChip{Label: v, Value: vals.Encode(), Active: v == active})
	}
	return out
}

// parseTargetValue splits a "<kind>|<cluster>|<node-or-db>" target value.
func parseTargetValue(v string) (kind, cluster, fixed string) {
	parts := strings.SplitN(v, "|", 3)
	for len(parts) < 3 {
		parts = append(parts, "")
	}
	return parts[0], parts[1], parts[2]
}

// chooseValue returns fixed if set, else param when it is a member of list,
// else "".
func chooseValue(param, fixed string, list []string) string {
	if fixed != "" {
		return fixed
	}
	for _, v := range list {
		if v == param {
			return param
		}
	}
	return ""
}

// consoleHandoffURL builds the SQL-console hand-off URL (see the ly-ae6.8
// integration contract). run=false is a LOAD (preload SQL + preselect target,
// do NOT execute — the list/search LoadHref). run=true is the RUN card's
// execute-intent: it adds &run=1 so the console executes immediately when a
// session grant is active on the cluster, else shows the grant gate.
func consoleHandoffURL(scriptID int64, cluster, node, db string, run bool) string {
	vals := url.Values{}
	vals.Set("script", strconv.FormatInt(scriptID, 10))
	if cluster != "" {
		vals.Set("cluster", cluster)
	}
	if node != "" {
		vals.Set("node", node)
	}
	if db != "" {
		vals.Set("db", db)
	}
	if run {
		vals.Set("run", "1")
	}
	return "/console?" + vals.Encode()
}
```

Remove the now-unused `var _ = strings.TrimSpace` line from Task 5 (its `strings` import is now used by `parseTargetValue`). Delete the `buildScriptRunVM` scaffold and `var _ = strings.TrimSpace` added in Task 5; the versions above replace them.

Update the run/load hand-off in the detail page: the list-row "load" is already `/console?script=<id>` (Task 4). Nothing else changes.

Register the route in `internal/api/server.go` `routes()`:

```go
	s.mux.HandleFunc("GET /partial/scripts/{id}/run", s.handleScriptRunCard)
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/api/ -run 'TestScriptRun|TestScriptDetailPage' -count=1`
Expected: PASS (run-flow target selection, the run=1 execute-intent assertion, the load-without-run guard, and detail all green).

- [ ] **Step 5: Commit**

```bash
git add internal/api/scripts_detail.go internal/api/scripts_test.go internal/api/server.go
git commit -m "feat(api): saved-script run flow — target search, node/db chips, console hand-off (ly-ae6.9)"
```

---

### Task 8: Cross-scope console search fragment + integration contract

**Files:**
- Modify: `web/scripts.templ` (`ScriptSearchResults` dropdown fragment)
- Modify: `web/scripts_vm.go` (`ScriptSearchVM`, `ScriptSearchItem`)
- Create: `internal/api/scripts_search.go` (search handler)
- Modify: `internal/api/server.go` (route)
- Modify: `internal/api/scripts_test.go` (search fragment test)
- Modify: `docs/design/COMPARISON.md` (mark saved-scripts gaps addressed — optional bookkeeping, see note)

**Interfaces:**
- Consumes: `store.ListVisibleScripts` (Task 1); `web.ScriptScopeColor` (Task 4); `viewerFromContext`, `groupFromContext`, `scriptMatches` (Task 4).
- Produces:
  ```go
  // web/scripts_vm.go
  type ScriptSearchItem struct { Name, Description, Scope, ScopeColor, LoadHref string }
  type ScriptSearchVM struct { Items []ScriptSearchItem; Empty bool }
  // web/scripts.templ
  templ ScriptSearchResults(vm ScriptSearchVM)
  // internal/api/scripts_search.go
  func (s *Server) handleScriptSearch(w http.ResponseWriter, r *http.Request) // GET /partial/scripts/search
  ```
  This is the "search saved scripts…" dropdown the SQL console (ly-ae6.8) embeds: `focus to browse, type to filter, MANAGE SCRIPTS → link`.

- [ ] **Step 1: Write the failing search-fragment test**

Append to `internal/api/scripts_test.go`:

```go
func TestScriptSearchFragment_filtersAndLinksManage(t *testing.T) {
	pool, srv := setupAudit(t, apiConfigDevAuth())
	_ = seedScripts(t, pool)

	// Empty query: browse all visible scripts + MANAGE SCRIPTS link.
	html := getBody(t, srv.URL+"/partial/scripts/search")
	if strings.Contains(html, "<!doctype html>") {
		t.Error("search fragment must not be a full page")
	}
	if !strings.Contains(html, "MANAGE SCRIPTS") || !strings.Contains(html, `href="/scripts"`) {
		t.Error("search fragment missing MANAGE SCRIPTS link")
	}
	if !strings.Contains(html, "dead-tuples-by-table") {
		t.Error("empty search should browse all visible scripts")
	}

	// Typed query filters; a load link points at the console hand-off.
	html = getBody(t, srv.URL+"/partial/scripts/search?q=replica")
	if !strings.Contains(html, "replica-lag-quick") {
		t.Error("q=replica missing the matching script")
	}
	if strings.Contains(html, "dead-tuples-by-table") {
		t.Error("q=replica leaked a non-matching script")
	}
	if !strings.Contains(html, `href="/console?script=`) {
		t.Error("search item missing the load-into-console link")
	}

	// No matches shows the empty state.
	html = getBody(t, srv.URL+"/partial/scripts/search?q=zzzzz")
	if !strings.Contains(html, "NO SCRIPTS MATCH") {
		t.Error("no-match search should show the empty state")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/api/ -run TestScriptSearchFragment -count=1`
Expected: FAIL — route `/partial/scripts/search` not registered (404).

- [ ] **Step 3: Add the search VM + templ**

Append to `web/scripts_vm.go`:

```go
// ScriptSearchItem is one row in the console's saved-script search dropdown.
type ScriptSearchItem struct {
	Name        string
	Description string
	Scope       string
	ScopeColor  string
	LoadHref    string // /console?script=<id>
}

// ScriptSearchVM is the console saved-script search dropdown.
type ScriptSearchVM struct {
	Items []ScriptSearchItem
	Empty bool
}
```

Append to `web/scripts.templ`:

```go
// ScriptSearchResults is the compact dropdown the SQL console (ly-ae6.8)
// embeds under its "search saved scripts…" input. Selecting an item loads
// the script into the console editor via the /console hand-off.
templ ScriptSearchResults(vm ScriptSearchVM) {
	<div id="script-search-results" class="ssx-menu">
		for _, it := range vm.Items {
			<a class="ssx-item" href={ templ.SafeURL(it.LoadHref) }>
				<span class="ssx-itemtop">
					<span class="ssx-name">{ it.Name }</span>
					<span class="ssx-scope" style={ "color:" + it.ScopeColor }>{ it.Scope }</span>
				</span>
				<span class="ssx-desc">{ it.Description }</span>
			</a>
		}
		if vm.Empty {
			<div class="ssx-empty">NO SCRIPTS MATCH</div>
		}
		<a class="ssx-manage" href="/scripts">MANAGE SCRIPTS →</a>
	</div>
}
```

- [ ] **Step 4: Add the search handler + route**

Create `internal/api/scripts_search.go`:

```go
package api

import (
	"fmt"
	"net/http"

	"github.com/dobbo-ca/lynceus/web"
)

// handleScriptSearch renders the console's saved-script search dropdown:
// focus (empty q) browses every visible script; typing filters by name /
// description / scope. Cross-scope: GLOBAL + TEAM(viewer group) + own
// PERSONAL, per ListVisibleScripts.
func (s *Server) handleScriptSearch(w http.ResponseWriter, r *http.Request) {
	viewer := viewerFromContext(r)
	group := groupFromContext(r)
	q := r.URL.Query().Get("q")

	scripts, err := s.conf.ListVisibleScripts(r.Context(), viewer, group)
	if err != nil {
		http.Error(w, "search scripts", http.StatusInternalServerError)
		return
	}
	var items []web.ScriptSearchItem
	for i := range scripts {
		sc := scripts[i]
		if q != "" && !scriptMatches(sc, q) {
			continue
		}
		items = append(items, web.ScriptSearchItem{
			Name:        sc.Name,
			Description: sc.Description,
			Scope:       sc.Scope,
			ScopeColor:  web.ScriptScopeColor(sc.Scope),
			LoadHref:    fmt.Sprintf("/console?script=%d", sc.ID),
		})
	}
	vm := web.ScriptSearchVM{Items: items, Empty: len(items) == 0}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.ScriptSearchResults(vm).Render(r.Context(), w)
}
```

Register the route in `internal/api/server.go` `routes()`:

```go
	s.mux.HandleFunc("GET /partial/scripts/search", s.handleScriptSearch)
```

> **Route-ordering note:** Go 1.22+ `ServeMux` matches the most specific pattern, so `GET /partial/scripts/search` and `GET /partial/scripts/{id}/run` coexist with `GET /partial/scripts` without conflict. No ordering constraint applies, but keep all six script routes grouped together for readability.

- [ ] **Step 5: Regenerate templ**

Run: `make templ`
Expected: `web/scripts_templ.go` regenerated with `ScriptSearchResults`; no errors.

- [ ] **Step 6: Run to verify it passes**

Run: `go test ./internal/api/ -run TestScriptSearchFragment -count=1`
Expected: PASS.

- [ ] **Step 7: Full suite + fmt/vet gate**

Run: `go build ./... && go vet ./web/ ./internal/api/ ./internal/store/ && go test ./web/ ./internal/api/ ./internal/store/ -count=1`
Expected: build clean; vet clean; all tests PASS.

- [ ] **Step 8: Record the integration contract for ly-ae6.3 + ly-ae6.8**

Run (leave a durable note for the two dependent beads):

```bash
bd note ly-ae6.9 "UI built at GET /scripts (list) + GET /scripts/{id} (detail). ly-ae6.3 must add a CONSOLE-section nav entry <a href=\"/scripts\">Saved Scripts</a> at EVERY scope (fleet CONSOLE=scripts-only; cluster/node/db CONSOLE=SQL Console+Saved Scripts). ly-ae6.8 console contract: serve GET /console reading query params script=<id> (preload SQL; execute ONLY when run=1 present, else load-without-run), cluster/node/db (preselect target), run=1 (execute if a session grant is active on the cluster, else show the grant gate). PRIVACY: the console MUST resolve script=<id> via store.GetVisibleScript(id,viewer,group) (NOT GetScript) and 404 when not visible — otherwise it leaks a non-owner's PERSONAL SQL. Console embeds GET /partial/scripts/search (dropdown) and POSTs new scripts to POST /scripts (fields name,description,sql,scope)."
```

- [ ] **Step 9: Commit**

```bash
git add web/scripts_vm.go web/scripts.templ web/scripts_templ.go internal/api/scripts_search.go internal/api/server.go internal/api/scripts_test.go
git commit -m "feat(api): cross-scope saved-script search dropdown for console embed + integration contract (ly-ae6.9)"
```

---

## Self-Review

### 1. Spec coverage — COMPARISON.md `#### Saved Scripts` gaps → task map

| COMPARISON gap (docs/design/COMPARISON.md:341-352) | Task |
|---|---|
| No Saved Scripts list view (name→detail, description, ACCESS scope badge + 'visible to…', owner, saved date, load/delete icon buttons, delete owner-only) | Task 4 (`SavedScriptsPage`/`SavedScriptsTable`, `SavedScriptRow`, delete gated on `Mine`) |
| No Saved Scripts detail view (SQL block, ACCESS card owner GLOBAL/TEAM/PERSONAL switch, non-owner 'managed by owner', RUN card) | Task 5 (`ScriptDetailPage`, `ScriptAccessCard`, `ScriptRunCard`) |
| No scope model global(org)/team(group)/personal(owner) with owner/admin-only access change + delete | Tasks 1–2 (`scope` column + CHECK, `ListVisibleScripts`, owner/admin gate in `SetScriptScope`/`DeleteScript`) |
| No audit emission on scope/access changes | Task 2 (`saved_script.scope.change` + `saved_script.delete` via `AppendAuditReturning`) |
| No load-without-run behavior | Task 4 (list `LoadHref` = `/console?script=<id>`, no `run=1`); guarded by `TestScriptRun_loadHrefHasNoRunParam` (Task 7) |
| No 'Run a script' target-resolution flow (search clusters/nodes/databases, then require node+db, **then hand off to console scoped to target, executing if granted, else grant gate**) | Tasks 3 + 7 (`ListScriptTargets`, `handleScriptRunCard`; `consoleHandoffURL(..., true)` emits `run=1` execute-intent; the console — ly-ae6.8 — resolves the session-grant-vs-gate decision, matching prototype `runScript` → `consoleRan: runGranted`). `TestScriptRun_targetSelectionThenNodeDBEnablesRun` asserts `run=1` on the ready RUN href. Live grant-state-aware hint text is deferred to ly-ae6.8 (owner of grant state); the RUN hint here states both branches honestly. |
| No backend: data model, CRUD API, store, ownership/scope visibility query | Tasks 1–3 (migration + store CRUD) + Task 6 (`POST /scripts`) |
| No Saved Scripts nav entry at any scope | Dependency contract on ly-ae6.3 (documented; `GET /scripts` is stable + scope-independent) — Task 8 note |
| Host surface (SQL Console) absent — save FROM console, RUN lands IN console | Dependency contract on ly-ae6.8 (`/console` query-param contract; `POST /scripts` create seam; `/partial/scripts/search` embed) — Tasks 6, 8 |

### Bead ly-ae6.9 acceptance criteria → task map

| Acceptance criterion (bd show) | Task |
|---|---|
| Available at every scope | ly-ae6.3 nav contract; `GET /scripts` scope-independent (Task 8 note) |
| scopes global(org)/team(group)/personal(owner) | Task 1 (`scope` CHECK + visibility query) |
| owner/admin change-access/delete with audited scope changes | Tasks 2 + 6 (store owner-or-admin gate + `audit_chain_id`; handler 403 exercised by `TestScriptScopeChange_handlerForbidsNonOwnerNonAdmin`) |
| load-without-run | Task 4 (`LoadHref` = `/console?script=<id>`, NO `run=1`; guarded by `TestScriptRun_loadHrefHasNoRunParam`) |
| run-a-script searches clusters/nodes/databases then requires node+db before running, then hands off executing-if-granted-else-grant-gate | Tasks 3 + 7 (`run=1` execute-intent on RUN href; grant-vs-gate decision delegated to ly-ae6.8) |
| read-visibility (PERSONAL = owner only) enforced on the detail + console-load path | Task 1 `GetVisibleScript` + Task 5 404 (`TestScriptDetailPage_nonVisiblePersonalReturns404`) + ly-ae6.8 console privacy obligation |
| cross-scope search | Task 8 (`/partial/scripts/search`) + Task 4 (list filter) |

### COMPARISON nav gaps referenced (docs/design/COMPARISON.md:120-123)
- "Saved Scripts is not present at any scope" and "no CONSOLE section" are owned by ly-ae6.3 (nav rebuild). This plan supplies the stable route + link target and records the contract (Task 8). Not re-planned here.

### 2. Placeholder scan
- No "TBD"/"add error handling"/"similar to Task N" present. Every code step contains complete, compilable code. The Task-5 `buildScriptRunVM` scaffold is explicitly and fully replaced in Task 7 (the replacement code is shown in full, and the `var _ = strings.TrimSpace` placeholder line is explicitly removed) — not a dangling placeholder.
- Backend genuinely-missing note: there is **no separate tracked backend bead** for saved scripts (confirmed via `bd list`; the only related beads are ly-ae6.8 and ly-ae6.9). Per the plan rules, this plan therefore adds the minimal store + migration itself (Tasks 1–3) rather than deferring to a non-existent bead. The SQL-console host surface IS separately tracked (ly-ae6.8) and is referenced as a dependency, not re-planned.

### 3. Type consistency
- `SavedScript` fields (`ID int64`, `Name`, `Description`, `SQLText`, `Scope`, `Owner`, `OwnerGroup`, `CreatedAt`, `UpdatedAt`) are defined once in Task 1 and consumed unchanged in Tasks 2–8. The `saved_scripts.audit_chain_id` column (Task 1 migration) is written by `SetScriptScope` (Task 2) but intentionally NOT added to `savedScriptCols` / `scanSavedScript`, so every INSERT/SELECT/UPDATE...RETURNING stays column-stable.
- Store method signatures match between the `Config` interface additions and the `pgxConfig` implementations: `CreateScript`, `ListVisibleScripts`, `GetScript`, `GetVisibleScript` (Task 1); `SetScriptScope`, `DeleteScript` (Task 2); `ListScriptTargets` (Task 3). Read surfaces (detail page Task 5, plus the ly-ae6.8 console-load contract) consume `GetVisibleScript`; the audited write path (Task 2) consumes the ungated `GetScript` and enforces its own owner-or-admin gate.
- `savedScriptsVM` returns `(web.SavedScriptsVM, error)` (Task 4); all three callers — `handleSavedScriptsPage`, `handleSavedScriptsTable` (Task 4), `handleScriptDelete` (Task 6) — check the error and emit a 500, so a config-DB outage never renders as an empty "no scripts match" 200.
- Context helpers: `viewerFromContext(r)` is a package func reading the dev-only `X-Dev-Actor` header (Task 4); `groupFromContext(r)` returns the fixed dev group; `isAdminFromContext(r *http.Request) bool` is a `*Server` method reading `X-Dev-Admin` (Task 4). Only the two dev-only headers that a test actually needs (actor + admin, for the 403 path) are honored — no speculative impersonation surface. All Saved Scripts handlers call the same three, and all pass the `*http.Request`.
- Sentinel errors `ErrScriptNotFound` / `ErrScriptForbidden` defined in Task 1, returned in Task 2, mapped to HTTP via `scriptWriteStatus` (Task 5), used in Task 6.
- Target value encoding `"<kind>|<cluster>|<node-or-db>"` is produced by `scriptTargetOptions` (Task 5) and parsed by `parseTargetValue` (Task 7) — same three-field, pipe-delimited shape.
- View-model names align across templ + handlers: `SavedScriptsVM`/`SavedScriptRow` (Task 4), `ScriptDetailVM`/`ScriptRunVM`/`ScriptScopeOption`/`ScriptTargetOption`/`ScriptTargetChip` (Task 5), `ScriptSearchVM`/`ScriptSearchItem` (Task 8). Every templ component (`SavedScriptsPage`, `SavedScriptsTable`, `ScriptDetailPage`, `ScriptAccessCard`, `ScriptRunCard`, `ScriptSearchResults`) takes exactly the VM type its handler renders.
- Helper names are stable: `itoa` (web templ path building), `urlq` (web query escape), `RelativeAge`, `ScriptScopeColor`, `ScriptVisibleTo` (web helpers); `viewerFromContext`, `groupFromContext`, `isAdminFromContext`, `scriptMatches`, `contains`, `toLower`, `consoleHandoffURL`, `parseScriptID`, `parseTargetValue`, `chooseValue`, `scopeOptions`, `scriptWriteStatus` (api helpers).
- Route registrations (Task 4/5/6/7/8) reference only handlers defined in the same or an earlier task: `handleSavedScriptsPage`, `handleSavedScriptsTable`, `handleScriptDetailPage`, `handleScriptCreate`, `handleScriptScopeChange`, `handleScriptDelete`, `handleScriptRunCard`, `handleScriptSearch`.

### 4. Adversarial review resolution (every finding → fix, honestly)

| Review finding | Resolution in this revision |
|---|---|
| **RUN hand-off never emits `run=1`** (was `consoleHandoffURL(..., false)`; RUN was identical to LOAD) | Task 7 `buildScriptRunVM` now calls `consoleHandoffURL(scriptID, cluster, node, db, true)` → `&run=1`. `TestScriptRun_targetSelectionThenNodeDBEnablesRun` asserts `run=1` on the ready RUN href; `TestScriptRun_loadHrefHasNoRunParam` asserts the LIST load href has NO `run=1`. Matches prototype `runScript` (consoleRan: runGranted) + COMPARISON.md:350. |
| **Detail/load handlers apply no scope-visibility gate** (any user could GET another's PERSONAL script SQL by enumerating `/scripts/{id}`) | New store read gate `GetVisibleScript(id, viewer, group)` (Task 1) folds the visibility predicate into the SQL; `handleScriptDetailPage` (Task 5) uses it and returns 404 (indistinguishable from missing) for a non-visible id. `TestScriptDetailPage_nonVisiblePersonalReturns404` proves a non-owner (even a DevAuth admin) gets 404 and no SQL leak. The `/console?script=<id>` load path is covered by an explicit ly-ae6.8 privacy obligation (Dependencies + Task 8 note): the console MUST use `GetVisibleScript`, not `GetScript`. |
| **Handler-level 403 gate untested** (`scriptWriteStatus`→403 unreachable; `nonOwnerForbidden` test actually asserted a 401 from the middleware; DevAuth forced admin) | `isAdminFromContext(r)` now honors a dev-only `X-Dev-Admin: false` header and `viewerFromContext(r)` a dev-only `X-Dev-Actor` header (both inherently dev-only — handlers only run under DevAuth). New `TestScriptScopeChange_handlerForbidsNonOwnerNonAdmin` impersonates a non-owner non-admin, asserts **403**, and asserts no scope change + no audit row. The old test is renamed `TestScriptScopeChange_devAuthDisabledReturns401` with an honest comment (it pins the middleware layer, distinct from the handler gate). |
| **RUN hint was a static string, not grant-state-aware** | Combined with the `run=1` fix, the RUN hint now states BOTH branches honestly ("if a session grant is active it executes immediately, otherwise the console asks you to request one first"). Live grant-state-aware wording (which branch is active NOW) is explicitly deferred to ly-ae6.8, which owns live grant state — documented, not silently dropped. |
| **`savedScriptsVM` swallowed a DB error** (config-DB outage rendered as "no scripts match" with 200) | `savedScriptsVM` now returns `(vm, error)`; `handleSavedScriptsPage`, `handleSavedScriptsTable`, and `handleScriptDelete` map the error to a 500 instead of a silent empty list. |
| **Audit write dropped the row↔audit linkage** (used `_` for the returned record; no `audit_chain_id`, unlike `capability_policy`) | Task 1 migration adds `audit_chain_id BIGINT REFERENCES audit_log(id)`; `SetScriptScope` (Task 2) captures `rec.ID` and binds it in the UPDATE, mirroring `SetCapabilityPolicy`. `TestSavedScripts_SetScope_auditedAndOwnerGated` asserts the row's `audit_chain_id` points at the scope-change audit entry. `DeleteScript` documents why it does NOT carry the id (the row is removed; the standalone `saved_script.delete` entry is the record). The audit-before-write ordering (non-atomic, consistent with `capability_policy`) is retained and its ordering note kept. |

### Coverage checklist — is every gap now closed?

- [x] Run/grant-gate hand-off modeled end-to-end: `run=1` emitted on RUN, absent on LOAD, both tested; grant decision delegated to ly-ae6.8 with a documented contract.
- [x] Read visibility gate on `/scripts/{id}` (store `GetVisibleScript` + 404 + no-SQL-leak test) and a written privacy obligation for the `/console` load path.
- [x] Handler 403 owner/admin gate exercised over HTTP (impersonation seam + dedicated test), separate from the middleware 401 test (renamed).
- [x] DB-error observability: `savedScriptsVM` surfaces store errors as 500s.
- [x] Audit row↔record linkage via `audit_chain_id` (scope change), with the delete asymmetry documented.
- [x] No dangling placeholders (see §2).

> **COMPARISON.md bookkeeping (Task 8 file list):** editing COMPARISON.md to tick these gaps is optional and out of the code path; if the team keeps that doc as a living tracker, flip the `saved-scripts` row from "absent/none" once merged. Otherwise omit — it is not required for the feature to function.
