# Per-server / per-database Capability Policy Storage Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a config-DB `capability_policy` table plus a Go writer/reader API so operators can enable or disable each Lynceus capability per server (or per database within a server), with every toggle recording a tamper-evident audit entry.

**Architecture:** A new migration `0003_capability_policy.sql` creates the table on the existing config database. NULL `database_name` rows are server-wide defaults; a non-NULL `database_name` row overrides the default for that one database. Uniqueness across the NULL/non-NULL mix is enforced with `UNIQUE NULLS NOT DISTINCT` (core Postgres 15+, RDS/Aurora-safe, no extension). The writer (`SetCapabilityPolicy`) appends an audit entry via the existing `AppendAuditReturning` (ly-8b0.3) to get an audit row id, then upserts the policy row carrying that id in `audit_chain_id` (FK → `audit_log.id`). Readers expose exact-row lookup, server listing, and an effective-policy resolver (DB override → server default) for the downstream gate (ly-xnk.3) and matrix API (ly-xnk.4).

**Tech Stack:** Go 1.26, pgx/v5 + pgxpool, embedded SQL migrations (`store.Migrate`), testcontainers-go `postgres:16` for integration tests. No mocks — tests hit a real Postgres.

---

## File Structure

- **Create** `internal/store/migrations/config/0003_capability_policy.sql` — DDL for the `capability_policy` table, its `NULLS NOT DISTINCT` unique constraint, FK to `audit_log(id)`, and a lookup index. Embedded automatically by the existing `//go:embed migrations/config/*.sql` in `internal/store/migrate.go`.
- **Create** `internal/store/capability_policy.go` — the `CapabilityPolicy` struct, `SetCapabilityPolicyInput`, and methods on `*store.Config`: `SetCapabilityPolicy`, `GetCapabilityPolicy`, `ListCapabilityPolicies`, `EffectiveCapability`.
- **Create** `internal/store/capability_policy_test.go` — integration tests (package `store_test`), reusing the existing `newPool(t)` helper from `store_test.go`.

The migration runner (`internal/store/migrate.go`) and audit writer (`internal/store/config.go`) are **not** modified — they already provide everything needed.

---

## Background the engineer needs

- **Migration runner:** `store.ApplyConfigMigrations(ctx, pool)` applies every `migrations/config/*.sql` file in lexical order, once each, tracked in `schema_migrations`. Adding `0003_capability_policy.sql` is all that's required to register the new schema; it is picked up by the existing embed directive. Each file runs in its own transaction.
- **Audit writer (ly-8b0.3, already merged):** `(*Config).AppendAuditReturning(ctx, AuditEntry) (AuditRecord, error)`. `AuditEntry{Actor, Action, ServerID, DataTier, Detail}` — `Detail` is any JSON-marshalable value, canonicalized before hashing. `AuditRecord.ID` is the assigned `audit_log.id` (int64). The append takes a cluster-wide advisory lock and chains the row via SHA-256; we depend only on its returned `ID`.
- **`Config`:** `internal/store/config.go` defines `type Config struct{ pool *pgxpool.Pool }` and `func NewConfig(pool) *Config`. The new methods are added in the same package on the same receiver, so they share `c.pool`.
- **Capability identifiers:** `internal/caps` defines `Capability` (a `string`) and constants like `caps.PgStatStatements`. Storage uses the bare string form — the `capability` column is free text. Validation against `caps.Declared()` is the API layer's job (ly-xnk.4), **not** this layer's. The store package does **not** import `caps`.
- **`database_name` convention in Go:** the Go API uses the empty string `""` to mean "server-wide default" (stored as SQL `NULL`); a non-empty string is a specific database. Callers never pass a literal Go `nil`.

---

## Task 1: Create the `capability_policy` migration

**Files:**
- Create: `internal/store/migrations/config/0003_capability_policy.sql`
- Test: `internal/store/capability_policy_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/store/capability_policy_test.go`:

```go
// Integration tests for capability policy storage. Real Postgres via
// testcontainers (the newPool helper lives in store_test.go) — we never
// mock the database, because the NULLS NOT DISTINCT uniqueness semantics
// and the FK to audit_log are part of what we're validating.
package store_test

import (
	"context"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/store"
)

func TestCapabilityPolicyMigration_createsTableAndConstraints(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Table exists with the expected columns.
	for _, col := range []string{
		"server_id", "database_name", "capability", "enabled",
		"set_by", "set_at", "reason", "audit_chain_id",
	} {
		var ok bool
		_ = pool.QueryRow(ctx,
			`SELECT EXISTS(
			   SELECT 1 FROM information_schema.columns
			   WHERE table_name='capability_policy' AND column_name=$1
			 )`, col,
		).Scan(&ok)
		if !ok {
			t.Errorf("capability_policy.%s missing", col)
		}
	}

	// Seed the server first: capability_policy.server_id is a FK to
	// servers(id), so the rows below need the parent to exist.
	if _, err := pool.Exec(ctx,
		`INSERT INTO servers (id, name) VALUES ('srv-1', 'srv one')`,
	); err != nil {
		t.Fatalf("seed server: %v", err)
	}

	// The unique index is NULLS NOT DISTINCT, so two server-wide rows
	// (database_name IS NULL) for the same (server_id, capability) collide.
	if _, err := pool.Exec(ctx,
		`INSERT INTO capability_policy
		   (server_id, database_name, capability, enabled, set_by, audit_chain_id)
		 VALUES ('srv-1', NULL, 'pg_stat_statements', true, 'alice', NULL)`,
	); err != nil {
		t.Fatalf("first server-wide insert: %v", err)
	}
	_, err := pool.Exec(ctx,
		`INSERT INTO capability_policy
		   (server_id, database_name, capability, enabled, set_by, audit_chain_id)
		 VALUES ('srv-1', NULL, 'pg_stat_statements', false, 'bob', NULL)`,
	)
	if err == nil {
		t.Fatal("duplicate server-wide row should violate UNIQUE NULLS NOT DISTINCT")
	}

	// idempotent re-apply.
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("re-apply: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestCapabilityPolicyMigration_createsTableAndConstraints -v`
Expected: FAIL — the migration file does not exist yet, so `capability_policy` is missing and the column checks / insert error out. (If Docker is unavailable the test SKIPs — re-run where Docker is available; a skip is not a pass.)

- [ ] **Step 3: Write the migration**

Create `internal/store/migrations/config/0003_capability_policy.sql`:

```sql
-- internal/store/migrations/config/0003_capability_policy.sql
--
-- ly-xnk.2 — Per-server / per-database capability policy.
--
-- Operators enable or disable each Lynceus capability for a server, or
-- override the server-wide default for one database within that server.
--   database_name IS NULL  -> server-wide default
--   database_name = '<db>'  -> override for that database only
--
-- Every toggle is written through the Go writer, which records a
-- tamper-evident audit entry (ly-8b0.3) and stores its id in
-- audit_chain_id.
--
-- Vanilla PostgreSQL — no extensions (must run on RDS / Aurora).
-- NULLS NOT DISTINCT is core PostgreSQL 15+; it makes the unique index
-- treat NULL database_name rows as equal, so a server can hold at most
-- one server-wide default per capability.

CREATE TABLE capability_policy (
    server_id      TEXT        NOT NULL REFERENCES servers (id) ON DELETE CASCADE,
    database_name  TEXT,
    capability     TEXT        NOT NULL,
    enabled        BOOLEAN     NOT NULL,
    set_by         TEXT        NOT NULL,
    set_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    reason         TEXT        NOT NULL DEFAULT '',
    audit_chain_id BIGINT      REFERENCES audit_log (id),
    CONSTRAINT capability_policy_uniq
        UNIQUE NULLS NOT DISTINCT (server_id, database_name, capability)
);

-- Effective-policy resolution reads all rows for one server at a time
-- (DB override falls back to the server-wide default), so index by server.
CREATE INDEX capability_policy_server_idx
    ON capability_policy (server_id, capability);
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestCapabilityPolicyMigration_createsTableAndConstraints -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/migrations/config/0003_capability_policy.sql internal/store/capability_policy_test.go
git commit -m "feat(store): capability_policy table + uniqueness (ly-xnk.2)"
```

---

## Task 2: Writer — `SetCapabilityPolicy` with audited upsert

**Files:**
- Create: `internal/store/capability_policy.go`
- Test: `internal/store/capability_policy_test.go` (append)

- [ ] **Step 1: Write the failing test**

Append to `internal/store/capability_policy_test.go`:

```go
func TestSetCapabilityPolicy_insertsAndAudits(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// FK requires the server row to exist first.
	if _, err := pool.Exec(ctx,
		`INSERT INTO servers (id, name) VALUES ('srv-1', 'srv one')`,
	); err != nil {
		t.Fatalf("seed server: %v", err)
	}
	cfg := store.NewConfig(pool)

	got, err := cfg.SetCapabilityPolicy(ctx, store.SetCapabilityPolicyInput{
		ServerID:     "srv-1",
		DatabaseName: "", // server-wide default
		Capability:   "pg_stat_statements",
		Enabled:      true,
		SetBy:        "alice",
		Reason:       "extension confirmed installed",
	})
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	if !got.Enabled || got.ServerID != "srv-1" || got.Capability != "pg_stat_statements" {
		t.Fatalf("unexpected returned policy: %+v", got)
	}
	if got.DatabaseName != "" {
		t.Errorf("server-wide row should report empty DatabaseName, got %q", got.DatabaseName)
	}
	if got.AuditChainID == 0 {
		t.Fatal("AuditChainID not populated")
	}
	if got.SetAt.IsZero() {
		t.Error("SetAt not populated")
	}

	// An audit row exists with the assigned id and references the toggle.
	var action, actor, serverID string
	if err := pool.QueryRow(ctx,
		`SELECT action, actor, COALESCE(server_id,'') FROM audit_log WHERE id = $1`,
		got.AuditChainID,
	).Scan(&action, &actor, &serverID); err != nil {
		t.Fatalf("audit row missing: %v", err)
	}
	if action != "capability_policy.set" || actor != "alice" || serverID != "srv-1" {
		t.Errorf("audit row = (%q,%q,%q), want (capability_policy.set, alice, srv-1)",
			action, actor, serverID)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestSetCapabilityPolicy_insertsAndAudits -v`
Expected: FAIL to compile — `store.SetCapabilityPolicyInput` and `(*Config).SetCapabilityPolicy` do not exist yet.

- [ ] **Step 3: Write the writer**

Create `internal/store/capability_policy.go` (no `pgx` import yet — Task 3 adds it when the reader needs `pgx.ErrNoRows`):

```go
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestSetCapabilityPolicy_insertsAndAudits -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/capability_policy.go internal/store/capability_policy_test.go
git commit -m "feat(store): SetCapabilityPolicy audited upsert (ly-xnk.2)"
```

---

## Task 3: Reader — `GetCapabilityPolicy` exact-row lookup + upsert-overwrite test

**Files:**
- Modify: `internal/store/capability_policy.go`
- Test: `internal/store/capability_policy_test.go` (append)

- [ ] **Step 1: Write the failing test**

Append to `internal/store/capability_policy_test.go`:

```go
func TestGetCapabilityPolicy_exactRowAndUpsertOverwrite(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO servers (id, name) VALUES ('srv-1', 'srv one')`,
	); err != nil {
		t.Fatalf("seed server: %v", err)
	}
	cfg := store.NewConfig(pool)

	// Not found before any write.
	_, found, err := cfg.GetCapabilityPolicy(ctx, "srv-1", "", "pg_stat_statements")
	if err != nil {
		t.Fatalf("get (absent): %v", err)
	}
	if found {
		t.Fatal("expected not found before any write")
	}

	// First write: disabled.
	first, err := cfg.SetCapabilityPolicy(ctx, store.SetCapabilityPolicyInput{
		ServerID: "srv-1", Capability: "pg_stat_statements",
		Enabled: false, SetBy: "alice", Reason: "off by default",
	})
	if err != nil {
		t.Fatalf("set #1: %v", err)
	}

	// Second write to the same key flips it and is a single row (upsert).
	second, err := cfg.SetCapabilityPolicy(ctx, store.SetCapabilityPolicyInput{
		ServerID: "srv-1", Capability: "pg_stat_statements",
		Enabled: true, SetBy: "bob", Reason: "operator enabled",
	})
	if err != nil {
		t.Fatalf("set #2: %v", err)
	}
	if second.AuditChainID == first.AuditChainID {
		t.Error("second toggle should produce a new audit id")
	}

	got, found, err := cfg.GetCapabilityPolicy(ctx, "srv-1", "", "pg_stat_statements")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !found {
		t.Fatal("expected found after write")
	}
	if !got.Enabled || got.SetBy != "bob" || got.Reason != "operator enabled" {
		t.Errorf("got %+v, want enabled/bob/operator enabled", got)
	}
	if got.AuditChainID != second.AuditChainID {
		t.Errorf("got.AuditChainID=%d, want %d", got.AuditChainID, second.AuditChainID)
	}

	// Exactly one row for the key (upsert, not insert-twice).
	var n int
	_ = pool.QueryRow(ctx,
		`SELECT count(*) FROM capability_policy
		   WHERE server_id='srv-1' AND database_name IS NULL AND capability='pg_stat_statements'`,
	).Scan(&n)
	if n != 1 {
		t.Errorf("row count = %d, want 1", n)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestGetCapabilityPolicy_exactRowAndUpsertOverwrite -v`
Expected: FAIL to compile — `(*Config).GetCapabilityPolicy` does not exist yet.

- [ ] **Step 3: Add the reader**

In `internal/store/capability_policy.go`, add `"github.com/jackc/pgx/v5"` to the import block (it is now used for `pgx.Row` and `pgx.ErrNoRows`), then append these methods:

```go
// GetCapabilityPolicy returns the exact policy row for the given key.
// databaseName == "" selects the server-wide default row (database_name
// IS NULL); a non-empty value selects that database's override row. It
// does NOT fall back between the two — use EffectiveCapability for
// resolution. found is false when no such row exists.
func (c *Config) GetCapabilityPolicy(ctx context.Context, serverID, databaseName, capability string) (CapabilityPolicy, bool, error) {
	var (
		out    CapabilityPolicy
		dbName *string
		row    pgx.Row
	)
	if databaseName == "" {
		row = c.pool.QueryRow(ctx,
			`SELECT server_id, database_name, capability, enabled,
			        set_by, set_at, reason, audit_chain_id
			   FROM capability_policy
			  WHERE server_id = $1 AND database_name IS NULL AND capability = $2`,
			serverID, capability)
	} else {
		row = c.pool.QueryRow(ctx,
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestGetCapabilityPolicy_exactRowAndUpsertOverwrite -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/capability_policy.go internal/store/capability_policy_test.go
git commit -m "feat(store): GetCapabilityPolicy exact-row reader (ly-xnk.2)"
```

---

## Task 4: Reader — `EffectiveCapability` resolution + `ListCapabilityPolicies`

**Files:**
- Modify: `internal/store/capability_policy.go`
- Test: `internal/store/capability_policy_test.go` (append)

- [ ] **Step 1: Write the failing test**

Append to `internal/store/capability_policy_test.go`:

```go
func TestEffectiveCapability_overrideBeatsDefault(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO servers (id, name) VALUES ('srv-1', 'srv one')`,
	); err != nil {
		t.Fatalf("seed server: %v", err)
	}
	cfg := store.NewConfig(pool)

	// No policy at all: not found.
	_, src, found, err := cfg.EffectiveCapability(ctx, "srv-1", "appdb", "pg_stat_statements")
	if err != nil {
		t.Fatalf("effective (none): %v", err)
	}
	if found {
		t.Fatalf("expected not found, got source=%v", src)
	}

	// Server-wide default: enabled. With no DB override, effective uses it.
	if _, err := cfg.SetCapabilityPolicy(ctx, store.SetCapabilityPolicyInput{
		ServerID: "srv-1", Capability: "pg_stat_statements",
		Enabled: true, SetBy: "alice",
	}); err != nil {
		t.Fatalf("set default: %v", err)
	}
	enabled, src, found, err := cfg.EffectiveCapability(ctx, "srv-1", "appdb", "pg_stat_statements")
	if err != nil || !found {
		t.Fatalf("effective (default): found=%v err=%v", found, err)
	}
	if !enabled || src != store.PolicySourceServerDefault {
		t.Errorf("got enabled=%v source=%v, want true/server-default", enabled, src)
	}

	// DB-specific override: disabled for appdb. Override wins.
	if _, err := cfg.SetCapabilityPolicy(ctx, store.SetCapabilityPolicyInput{
		ServerID: "srv-1", DatabaseName: "appdb", Capability: "pg_stat_statements",
		Enabled: false, SetBy: "bob", Reason: "noisy on appdb",
	}); err != nil {
		t.Fatalf("set override: %v", err)
	}
	enabled, src, found, err = cfg.EffectiveCapability(ctx, "srv-1", "appdb", "pg_stat_statements")
	if err != nil || !found {
		t.Fatalf("effective (override): found=%v err=%v", found, err)
	}
	if enabled || src != store.PolicySourceDatabaseOverride {
		t.Errorf("got enabled=%v source=%v, want false/db-override", enabled, src)
	}

	// A different database with no override still sees the server default.
	enabled, src, found, err = cfg.EffectiveCapability(ctx, "srv-1", "otherdb", "pg_stat_statements")
	if err != nil || !found {
		t.Fatalf("effective (other db): found=%v err=%v", found, err)
	}
	if !enabled || src != store.PolicySourceServerDefault {
		t.Errorf("otherdb got enabled=%v source=%v, want true/server-default", enabled, src)
	}
}

func TestListCapabilityPolicies_returnsAllForServer(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO servers (id, name) VALUES ('srv-1','srv one'), ('srv-2','srv two')`,
	); err != nil {
		t.Fatalf("seed servers: %v", err)
	}
	cfg := store.NewConfig(pool)

	mustSet := func(in store.SetCapabilityPolicyInput) {
		t.Helper()
		if _, err := cfg.SetCapabilityPolicy(ctx, in); err != nil {
			t.Fatalf("set %+v: %v", in, err)
		}
	}
	mustSet(store.SetCapabilityPolicyInput{ServerID: "srv-1", Capability: "pg_stat_statements", Enabled: true, SetBy: "a"})
	mustSet(store.SetCapabilityPolicyInput{ServerID: "srv-1", DatabaseName: "appdb", Capability: "pg_stat_statements", Enabled: false, SetBy: "a"})
	mustSet(store.SetCapabilityPolicyInput{ServerID: "srv-2", Capability: "auto_explain", Enabled: true, SetBy: "a"})

	got, err := cfg.ListCapabilityPolicies(ctx, "srv-1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d rows for srv-1, want 2", len(got))
	}
	for _, p := range got {
		if p.ServerID != "srv-1" {
			t.Errorf("list returned foreign server row: %+v", p)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run 'TestEffectiveCapability_overrideBeatsDefault|TestListCapabilityPolicies_returnsAllForServer' -v`
Expected: FAIL to compile — `EffectiveCapability`, `ListCapabilityPolicies`, and the `PolicySource*` constants do not exist yet.

- [ ] **Step 3: Add the resolver and list reader**

Append to `internal/store/capability_policy.go`:

```go
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
func (c *Config) EffectiveCapability(ctx context.Context, serverID, databaseName, capability string) (enabled bool, source PolicySource, found bool, err error) {
	var isOverride bool
	row := c.pool.QueryRow(ctx,
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
func (c *Config) ListCapabilityPolicies(ctx context.Context, serverID string) ([]CapabilityPolicy, error) {
	rows, err := c.pool.Query(ctx,
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/store/ -run 'TestEffectiveCapability_overrideBeatsDefault|TestListCapabilityPolicies_returnsAllForServer' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/capability_policy.go internal/store/capability_policy_test.go
git commit -m "feat(store): EffectiveCapability resolver + ListCapabilityPolicies (ly-xnk.2)"
```

---

## Task 5: Full verification + audit-chain integrity guard

**Files:**
- Test: `internal/store/capability_policy_test.go` (append)

- [ ] **Step 1: Write the failing test**

Append to `internal/store/capability_policy_test.go` (note the extra import — add `"github.com/dobbo-ca/lynceus/internal/store"` already present; this test also needs `time` only if used — it does not, so no new imports):

```go
func TestSetCapabilityPolicy_keepsAuditChainIntact(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO servers (id, name) VALUES ('srv-1', 'srv one')`,
	); err != nil {
		t.Fatalf("seed server: %v", err)
	}
	cfg := store.NewConfig(pool)

	for i := 0; i < 5; i++ {
		if _, err := cfg.SetCapabilityPolicy(ctx, store.SetCapabilityPolicyInput{
			ServerID: "srv-1", Capability: "auto_explain",
			Enabled: i%2 == 0, SetBy: "alice", Reason: "toggle",
		}); err != nil {
			t.Fatalf("set %d: %v", i, err)
		}
	}

	// Every toggle wrote an audit row and the chain still verifies.
	bad, reason, err := cfg.VerifyChain(ctx, timeZero(), timeZero())
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if bad != -1 {
		t.Fatalf("audit chain broken after toggles: bad=%d reason=%q", bad, reason)
	}

	var auditCount int
	_ = pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE action = 'capability_policy.set'`,
	).Scan(&auditCount)
	if auditCount != 5 {
		t.Errorf("audit rows = %d, want 5", auditCount)
	}
}

// timeZero returns the zero time; VerifyChain treats a zero since/until as
// "scan the whole table from genesis".
func timeZero() time.Time { return time.Time{} }
```

Add `"time"` to the test file's import block if it is not already present.

- [ ] **Step 2: Run test to verify it fails (or compiles then passes)**

Run: `go test ./internal/store/ -run TestSetCapabilityPolicy_keepsAuditChainIntact -v`
Expected: PASS once `time` is imported (the implementation already exists from Tasks 2–4; this test guards the audit-chain invariant). If `time` import is missing, it FAILs to compile first — add the import and re-run.

- [ ] **Step 3: Run the whole store package**

Run: `go test ./internal/store/ -v`
Expected: PASS — all capability-policy tests plus the pre-existing audit/stats tests. (Tests SKIP only if Docker is unavailable; run where Docker is present.)

- [ ] **Step 4: Build everything and vet**

Run: `go build ./... && go vet ./internal/store/`
Expected: no output, exit 0.

- [ ] **Step 5: Commit**

```bash
git add internal/store/capability_policy_test.go
git commit -m "test(store): capability policy keeps audit chain intact (ly-xnk.2)"
```

---

## Acceptance Criteria (verify before closing ly-xnk.2)

- [ ] `capability_policy` table exists on the config DB with columns `server_id, database_name (NULL), capability, enabled, set_by, set_at, reason, audit_chain_id`.
- [ ] `UNIQUE NULLS NOT DISTINCT (server_id, database_name, capability)` — a server has at most one server-wide default per capability, and at most one override per (database, capability). Verified by `TestCapabilityPolicyMigration_createsTableAndConstraints`.
- [ ] Writer (`SetCapabilityPolicy`) upserts and writes a tamper-evident audit entry (`action = "capability_policy.set"`) whose id is stored in `audit_chain_id` (FK to `audit_log.id`). Verified by `TestSetCapabilityPolicy_insertsAndAudits` and `TestGetCapabilityPolicy_exactRowAndUpsertOverwrite`.
- [ ] Reader resolves effective policy (DB override beats server default; not-found when neither exists). Verified by `TestEffectiveCapability_overrideBeatsDefault`.
- [ ] Listing returns all rows for a server. Verified by `TestListCapabilityPolicies_returnsAllForServer`.
- [ ] Audit chain still verifies after repeated toggles. Verified by `TestSetCapabilityPolicy_keepsAuditChainIntact`.
- [ ] Migration is vanilla Postgres, no extensions, RDS/Aurora-safe (NULLS NOT DISTINCT is core PG15+).
- [ ] `go build ./...` and `go test ./internal/store/` pass.

---

## Notes for downstream beads

- **ly-xnk.3 (effective-policy gate):** call `(*Config).EffectiveCapability(ctx, serverID, databaseName, capability)`; treat `found == false` as the configured absent-policy default for that reader. `PolicySource` is available if the gate wants to log which row decided.
- **ly-xnk.4 (matrix API):** `ListCapabilityPolicies(serverID)` backs GET; `SetCapabilityPolicy(...)` backs POST toggle. The API layer is responsible for validating `Capability` against `caps.Declared()` and for authentication/authorization — the store layer stores whatever string it is given.
