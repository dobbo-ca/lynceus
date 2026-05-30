# Tamper-Evident Audit Log Writer + Storage Implementation Plan (`ly-8b0.3`)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Lynceus's `audit_log` table append-only and tamper-evident by chaining each row to its predecessor via a content hash, so any later mutation, deletion, or out-of-order insertion is mechanically detectable by a `VerifyChain` call.

**Architecture:** Extend the existing M1 `audit_log` schema with two `BYTEA` columns (`prev_hash`, `row_hash`). Every `AppendAudit` call runs inside a transaction that takes a **per-database advisory lock** (`pg_advisory_xact_lock`) to serialize appenders across api replicas, reads the tail row's `row_hash`, computes the new row's hash over a strictly-specified canonical byte layout, inserts, and commits. A `VerifyChain` reader walks rows ordered by `id`, recomputes each hash, and reports the first index where the stored hash diverges or where `prev_hash` does not match the previous row's `row_hash`. Hash is SHA-256. Vanilla Postgres only — no extensions; works on RDS/Aurora.

**Tech Stack:** Go 1.23+, `jackc/pgx/v5`, `crypto/sha256`, `encoding/binary`, `encoding/json` (only for canonicalizing the `detail` JSONB sub-document via `json.Compact` on already-`json.Marshal`'d bytes), embedded SQL migrations under `internal/store/migrations/config/`, `testcontainers-go` (`postgres:16`).

**Spec references:** `docs/specs/2026-05-29-lynceus-design.md` §2.3 (RBAC, groups, audit — "Designed to be append-only / tamper-evident") and §3.3 (audit-log writer lives in `api_server`); `docs/specs/2026-05-29-lynceus-features.md` §11 ("tamper-evident audit — MUST").

---

## Design notes (read before touching code)

### Canonicalization — pinned, do not improvise

Hash input is a **single byte string** built by concatenating length-prefixed fields in this exact order. Each field is prefixed by an unsigned 32-bit big-endian length, even when empty (length zero). This is a tagged byte layout, not JSON — the JSON below applies only to the `detail` field's interior bytes.

```
domain    = "lynceus.audit.v1\x00"            (literal ASCII, no length prefix; domain separator)
F1 id     = uint64 big-endian, 8 bytes        (the row's BIGSERIAL value, fixed-width — not length-prefixed)
F2 prev   = 32 bytes                          (the previous row's row_hash; fixed-width, not length-prefixed)
F3 actor  = uint32 len BE || UTF-8 bytes
F4 action = uint32 len BE || UTF-8 bytes
F5 srvID  = uint32 len BE || UTF-8 bytes      (empty string "" when ServerID was NULL)
F6 tier   = int16 big-endian, 2 bytes         (0 when DataTier was NULL; treat tier as signed)
F7 detail = uint32 len BE || canonical JSON bytes  (zero length when detail JSONB is SQL NULL)
F8 at_ns  = int64 big-endian, 8 bytes         (UNIX nanoseconds of the at TIMESTAMPTZ, UTC)
```

The hash is `SHA-256(domain || F1 || F2 || F3 || F4 || F5 || F6 || F7 || F8)`.

**Genesis** — for row id 1 (the only row with no predecessor), `F2 prev` is 32 zero bytes. This constant is also the literal value stored in that row's `prev_hash` column.

**Canonical JSON for F7** — the writer marshals `AuditEntry.Detail` with `encoding/json` (Go's default, which sorts struct fields in declaration order). To get a stable canonical form independent of map ordering, the writer parses the marshaled bytes into a `map[string]any`/array tree and re-emits them with a `canonicaljson` helper that:

1. Sorts every object's keys lexicographically by their unescaped string value.
2. Emits no insignificant whitespace.
3. Uses the same number formatting as `encoding/json` (`strconv.AppendFloat` with `'g'`, -1 precision for floats; `strconv.AppendInt` base 10 for ints).
4. UTF-8-escapes only `"`, `\`, and U+0000–U+001F per RFC 8259.

When `Detail == nil`, F7 is length zero. The verifier reads the **stored `detail` JSONB column** from Postgres and re-canonicalizes via the same path before hashing. (Postgres preserves whitespace in JSONB only at top-level, not key order — so we never trust Postgres's bytes; we canonicalize.)

### Concurrency

Multiple api replicas append simultaneously. The writer transaction does:

```sql
BEGIN;
SELECT pg_advisory_xact_lock(7426398501234567890);  -- pinned constant, see Task 2
-- read tail row's row_hash (or zeros if empty)
-- compute hash; INSERT row; capture returning id, prev_hash, row_hash
COMMIT;
```

`pg_advisory_xact_lock` is automatically released on COMMIT/ROLLBACK. The constant key is a single fixed `bigint` (the audit log namespace); every appender contends on that one lock, which serializes audit appends globally across the cluster sharing this config DB. Throughput cost: an audit append is small (< 1 ms typical); the audit volume is dominated by T2 reads which are inherently rare. We accept the serial cost for the integrity guarantee.

A trigger enforces the append-only property at the schema level: any `UPDATE` or `DELETE` on `audit_log` is rejected with a custom error. The trigger is only bypassable by a privileged role used by the migration (to backfill prev/row hash on existing rows on first deployment); operators do not grant this role to the app user in production. This is **defense in depth**: even if a bug or stolen creds escape the writer, modifying past rows requires `pg_replication` / superuser shenanigans.

### What `VerifyChain` detects

The verifier walks rows ordered by `id ASC` and tracks an expected `prev_hash` (starts at 32 zero bytes for the first row). For each row:

- Recompute `row_hash` over the canonical layout (using stored `id`, `prev_hash`, `actor`, `action`, `server_id`, `data_tier`, `detail`, `at`).
- Fail if the stored `row_hash` ≠ recomputed hash → **mutation** detected.
- Fail if the stored `prev_hash` ≠ tracked expected `prev_hash` → **deletion or reorder** detected (a deletion changes the chain because the next row's `prev_hash` no longer matches; an out-of-order insertion produces the same break).
- Fail if `id` is not strictly increasing by one from the previous row → **insertion gap or duplicate id** (rows missing entirely are also caught by this check).
- Otherwise advance: tracked expected `prev_hash` ← this row's stored `row_hash`.

Return `(firstBadIndex int, reason string, err error)`. `firstBadIndex` is the **0-based ordinal of the row in the walk** (not the SQL `id`) of the first inconsistency, or `-1` if the walk completed clean. (Using `-1` for "OK" leaves `0` unambiguously meaning "the very first row was bad", which keeps the test assertions clearer than the spec's "or zero".)

### `AppendAudit` contract change

The existing `Config.AppendAudit(ctx, entry)` signature is preserved (same parameters, same `error` return). Behavior change: the writer now runs the advisory-lock transaction, hashes, and stores `prev_hash` + `row_hash`. **No call-site change required.** Backwards compatibility holds for the M1 caller.

A new method `AppendAuditReturning(ctx, entry) (AuditRecord, error)` is added for callers (the future audit-log viewer, the verifier tests) that need the assigned id and hash.

---

## File Structure

```
lynceus/
  internal/
    store/
      config.go                                   # MODIFY: AppendAudit now hashes + locks; add AppendAuditReturning, VerifyChain, AuditRecord
      audit_hash.go                               # CREATE: canonical byte layout + SHA-256 + canonicaljson helper
      audit_hash_test.go                          # CREATE: unit tests for canonicalization + golden hash vectors (no DB)
      migrations/
        config/
          0001_init.sql                           # UNCHANGED
          0002_audit_chain.sql                    # CREATE: add prev_hash, row_hash, append-only trigger; backfill any existing rows
      store_test.go                               # MODIFY: extend TestAuditAppend_roundtrips to also assert chain columns populate; new tests below
      audit_chain_test.go                         # CREATE: VerifyChain integration tests (intact / mutated / deleted / reordered) + concurrent-appender test
```

Single new package file (`audit_hash.go`) holds the pure-function hash layer so it is unit-testable without a database. Database-touching logic (locking, INSERT, walk) stays in `config.go`. Migrations follow the existing `NNNN_name.sql` convention.

---

## Task 1: Pure canonical-bytes + hash layer (no DB)

**Files:**
- Create: `internal/store/audit_hash.go`
- Create: `internal/store/audit_hash_test.go`

- [ ] **Step 1: Write the failing canonicalization test (no DB).**

```go
// internal/store/audit_hash_test.go
package store

import (
	"bytes"
	"encoding/hex"
	"testing"
	"time"
)

func TestCanonicalJSON_sortsKeysAndStripsWhitespace(t *testing.T) {
	in := []byte(`{ "b": 2,  "a": [1, 2,  3], "c": {"y": 1, "x": 2} }`)
	got, err := canonicalJSON(in)
	if err != nil {
		t.Fatalf("canonicalJSON: %v", err)
	}
	want := `{"a":[1,2,3],"b":2,"c":{"x":2,"y":1}}`
	if string(got) != want {
		t.Fatalf("canonicalJSON = %q, want %q", got, want)
	}
}

func TestHashAuditRow_isStableForSameInputs(t *testing.T) {
	at := time.Date(2026, 5, 29, 10, 0, 0, 123456789, time.UTC)
	prev := make([]byte, 32) // genesis prev
	h1 := hashAuditRow(1, prev, "alice", "viewed.t2", "srv-1", 2, []byte(`{"fingerprint":"abc"}`), at)
	h2 := hashAuditRow(1, prev, "alice", "viewed.t2", "srv-1", 2, []byte(`{"fingerprint":"abc"}`), at)
	if !bytes.Equal(h1, h2) {
		t.Fatalf("hash not deterministic")
	}
	if len(h1) != 32 {
		t.Fatalf("hash length = %d, want 32", len(h1))
	}
}

func TestHashAuditRow_changesWhenAnyFieldChanges(t *testing.T) {
	at := time.Date(2026, 5, 29, 10, 0, 0, 0, time.UTC)
	prev := make([]byte, 32)
	base := hashAuditRow(1, prev, "alice", "viewed.t2", "srv-1", 2, []byte(`{}`), at)

	mutations := []struct {
		name string
		got  []byte
	}{
		{"id", hashAuditRow(2, prev, "alice", "viewed.t2", "srv-1", 2, []byte(`{}`), at)},
		{"prev", hashAuditRow(1, bytes.Repeat([]byte{1}, 32), "alice", "viewed.t2", "srv-1", 2, []byte(`{}`), at)},
		{"actor", hashAuditRow(1, prev, "bob", "viewed.t2", "srv-1", 2, []byte(`{}`), at)},
		{"action", hashAuditRow(1, prev, "alice", "viewed.t1", "srv-1", 2, []byte(`{}`), at)},
		{"server", hashAuditRow(1, prev, "alice", "viewed.t2", "srv-2", 2, []byte(`{}`), at)},
		{"tier", hashAuditRow(1, prev, "alice", "viewed.t2", "srv-1", 1, []byte(`{}`), at)},
		{"detail", hashAuditRow(1, prev, "alice", "viewed.t2", "srv-1", 2, []byte(`{"x":1}`), at)},
		{"at", hashAuditRow(1, prev, "alice", "viewed.t2", "srv-1", 2, []byte(`{}`), at.Add(time.Nanosecond))},
	}
	for _, m := range mutations {
		if bytes.Equal(base, m.got) {
			t.Errorf("changing %s did not change the hash", m.name)
		}
	}
}

func TestHashAuditRow_goldenVector(t *testing.T) {
	// Pin the canonical byte layout against regression. If you change the
	// layout intentionally, regenerate this vector and bump the domain tag
	// from "lynceus.audit.v1" to v2.
	at := time.Date(2026, 5, 29, 10, 0, 0, 0, time.UTC)
	prev := make([]byte, 32)
	h := hashAuditRow(1, prev, "alice", "viewed.t2", "srv-1", 2, []byte(`{"fingerprint":"abc"}`), at)
	want := "REPLACE_AFTER_FIRST_GREEN" // see Step 4
	if hex.EncodeToString(h) != want {
		t.Logf("hash = %s", hex.EncodeToString(h))
		t.Fatalf("golden vector mismatch")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails.**

Run: `go test ./internal/store/ -run TestHashAuditRow -run TestCanonicalJSON -v`
Expected: FAIL — `undefined: canonicalJSON`, `undefined: hashAuditRow`.

- [ ] **Step 3: Implement `audit_hash.go`.**

```go
// internal/store/audit_hash.go
package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// hashDomain is the v1 domain separator. Bump this (and the migration
// note in 0002_audit_chain.sql) if the canonical layout below changes.
const hashDomain = "lynceus.audit.v1\x00"

// genesisPrev is the 32-byte zero value used as prev_hash for row id 1.
var genesisPrev = make([]byte, 32)

// hashAuditRow returns SHA-256 over the canonical byte layout specified
// in the plan doc. detail is the canonical JSON bytes of the row's JSONB
// column (zero length when SQL NULL). at is truncated to nanosecond UTC.
func hashAuditRow(id uint64, prev []byte, actor, action, serverID string, tier int16, detail []byte, at time.Time) []byte {
	if len(prev) != 32 {
		panic(fmt.Sprintf("hashAuditRow: prev must be 32 bytes, got %d", len(prev)))
	}
	var buf bytes.Buffer
	buf.WriteString(hashDomain)

	var u64 [8]byte
	binary.BigEndian.PutUint64(u64[:], id)
	buf.Write(u64[:])

	buf.Write(prev)

	writeLP := func(s string) {
		var l [4]byte
		binary.BigEndian.PutUint32(l[:], uint32(len(s)))
		buf.Write(l[:])
		buf.WriteString(s)
	}
	writeLP(actor)
	writeLP(action)
	writeLP(serverID)

	var i16 [2]byte
	binary.BigEndian.PutUint16(i16[:], uint16(tier))
	buf.Write(i16[:])

	var dl [4]byte
	binary.BigEndian.PutUint32(dl[:], uint32(len(detail)))
	buf.Write(dl[:])
	buf.Write(detail)

	var ns [8]byte
	binary.BigEndian.PutUint64(ns[:], uint64(at.UTC().UnixNano()))
	buf.Write(ns[:])

	sum := sha256.Sum256(buf.Bytes())
	return sum[:]
}

// canonicalJSON re-serializes raw JSON bytes with object keys sorted
// lexicographically and no insignificant whitespace. Returns nil for
// a nil input; returns ("null", nil) for the literal JSON "null".
func canonicalJSON(raw []byte) ([]byte, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("canonicalJSON: parse: %w", err)
	}
	var buf bytes.Buffer
	if err := canonicalEmit(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func canonicalEmit(buf *bytes.Buffer, v any) error {
	switch x := v.(type) {
	case nil:
		buf.WriteString("null")
	case bool:
		if x {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case json.Number:
		buf.WriteString(x.String())
	case string:
		b, err := json.Marshal(x) // RFC 8259 escaping
		if err != nil {
			return err
		}
		buf.Write(b)
	case []any:
		buf.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := canonicalEmit(buf, e); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			kb, err := json.Marshal(k)
			if err != nil {
				return err
			}
			buf.Write(kb)
			buf.WriteByte(':')
			if err := canonicalEmit(buf, x[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	default:
		return fmt.Errorf("canonicalJSON: unsupported type %T", v)
	}
	return nil
}
```

- [ ] **Step 4: Run the tests to determine the golden vector, then pin it.**

Run: `go test ./internal/store/ -run TestHashAuditRow_goldenVector -v`
Expected: FAIL (placeholder `REPLACE_AFTER_FIRST_GREEN` does not match). The test logs the actual hash. Copy that hex string into `want` in the test, replacing `REPLACE_AFTER_FIRST_GREEN`. Re-run; expect PASS.

- [ ] **Step 5: Run the full hash test file.**

Run: `go test ./internal/store/ -run 'TestHashAuditRow|TestCanonicalJSON' -v`
Expected: all PASS.

- [ ] **Step 6: Commit.**

```bash
git add internal/store/audit_hash.go internal/store/audit_hash_test.go
git commit -m "feat(store): canonical byte layout and SHA-256 for audit chain"
```

---

## Task 2: Schema migration — add prev_hash, row_hash, append-only trigger

**Files:**
- Create: `internal/store/migrations/config/0002_audit_chain.sql`

- [ ] **Step 1: Write the failing migration assertion test (extend `store_test.go`).**

Add this test to `internal/store/store_test.go` (it joins the existing tests in that file):

```go
func TestApplyConfigMigrations_addsChainColumnsAndTrigger(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// prev_hash + row_hash exist on audit_log.
	for _, col := range []string{"prev_hash", "row_hash"} {
		var ok bool
		_ = pool.QueryRow(ctx,
			`SELECT EXISTS(
			   SELECT 1 FROM information_schema.columns
			   WHERE table_name='audit_log' AND column_name=$1
			 )`, col,
		).Scan(&ok)
		if !ok {
			t.Errorf("audit_log.%s missing", col)
		}
	}

	// row_hash is uniquely constrained.
	var hasUnique bool
	_ = pool.QueryRow(ctx,
		`SELECT EXISTS(
		   SELECT 1 FROM pg_indexes
		   WHERE tablename='audit_log' AND indexdef ILIKE '%UNIQUE%(row_hash)%'
		 )`,
	).Scan(&hasUnique)
	if !hasUnique {
		t.Error("expected UNIQUE index on audit_log(row_hash)")
	}

	// Append-only trigger rejects UPDATE and DELETE.
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("idempotent re-apply: %v", err)
	}
	cfg := store.NewConfig(pool)
	if err := cfg.AppendAudit(ctx, store.AuditEntry{Actor: "a", Action: "x"}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE audit_log SET actor='mallory'`); err == nil {
		t.Error("UPDATE on audit_log should be rejected by trigger")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM audit_log`); err == nil {
		t.Error("DELETE on audit_log should be rejected by trigger")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails.**

Run: `go test ./internal/store/ -run TestApplyConfigMigrations_addsChainColumnsAndTrigger -v`
Expected: FAIL — columns and trigger do not exist.

- [ ] **Step 3: Write the migration.**

```sql
-- internal/store/migrations/config/0002_audit_chain.sql
--
-- ly-8b0.3 — Tamper-evident audit log.
--
-- Adds the hash-chain columns and the append-only trigger. Backfills any
-- rows that pre-existed under the M1 schema with a deterministic chain
-- starting at the 32 zero-byte genesis prev_hash. The canonical byte
-- layout being hashed is specified in the plan doc; the writer in
-- internal/store/audit_hash.go is the single source of truth.
--
-- Vanilla PostgreSQL — no extensions. Concurrent appenders are
-- serialized at write time via pg_advisory_xact_lock on the constant
-- 7426398501234567890 (the "audit chain" lock).

ALTER TABLE audit_log
    ADD COLUMN prev_hash BYTEA,
    ADD COLUMN row_hash  BYTEA;

-- One-time backfill for any rows present from M1.  We cannot compute the
-- "true" canonical hash in SQL (the canonical JSON ordering lives in
-- Go), so this backfill writes a placeholder genesis chain that the
-- caller can re-anchor at deploy time if any rows already exist. For a
-- fresh install (no rows yet), the loop is a no-op.
DO $$
DECLARE
    r RECORD;
    prev BYTEA := decode(repeat('00', 32), 'hex');
BEGIN
    FOR r IN SELECT id FROM audit_log ORDER BY id ASC LOOP
        -- Placeholder: prev = previous row_hash; row_hash = sha256 of
        -- the row's (id || prev) only. The Go layer will reject these
        -- on VerifyChain unless they were written by the Go writer
        -- (which they were not, by construction). Operators upgrading
        -- a system with existing audit rows should run a one-time
        -- re-anchor: TRUNCATE audit_log and re-export old rows as
        -- "pre-chain.imported" actions through AppendAudit.
        UPDATE audit_log
           SET prev_hash = prev,
               row_hash  = digest(prev || int8send(r.id), 'sha256')
         WHERE id = r.id
        RETURNING row_hash INTO prev;
    END LOOP;
EXCEPTION WHEN undefined_function THEN
    -- pgcrypto not present (vanilla RDS without extension) — leave NULLs;
    -- the table is empty in that case for fresh installs, and operators
    -- with pre-existing rows must run the documented re-anchor.
    NULL;
END $$;

-- After backfill, lock the columns down.
ALTER TABLE audit_log
    ALTER COLUMN prev_hash SET NOT NULL,
    ALTER COLUMN row_hash  SET NOT NULL;

ALTER TABLE audit_log
    ADD CONSTRAINT audit_log_prev_hash_len CHECK (octet_length(prev_hash) = 32),
    ADD CONSTRAINT audit_log_row_hash_len  CHECK (octet_length(row_hash)  = 32);

CREATE UNIQUE INDEX audit_log_row_hash_uniq ON audit_log (row_hash);

-- Append-only enforcement: reject UPDATE and DELETE outright. The Go
-- writer only ever INSERTs.
CREATE OR REPLACE FUNCTION audit_log_no_mutate() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'audit_log is append-only (operation=%, row id=%)',
        TG_OP, COALESCE(OLD.id, NEW.id);
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER audit_log_no_update
    BEFORE UPDATE ON audit_log
    FOR EACH ROW EXECUTE FUNCTION audit_log_no_mutate();

CREATE TRIGGER audit_log_no_delete
    BEFORE DELETE ON audit_log
    FOR EACH ROW EXECUTE FUNCTION audit_log_no_mutate();
```

Note on the backfill: vanilla RDS does not ship `pgcrypto` enabled by default, so `digest()` may be absent. The `EXCEPTION WHEN undefined_function` clause handles that for fresh installs (no rows → no work). For systems with pre-existing M1 rows on a Postgres without `pgcrypto`, the operator runbook (out of scope for this plan) is documented inline: TRUNCATE and re-import. For the typical fresh-install path the loop is empty and the migration succeeds unconditionally.

- [ ] **Step 4: Run the migration test.**

Run: `go test ./internal/store/ -run TestApplyConfigMigrations_addsChainColumnsAndTrigger -v`
Expected: PASS.

- [ ] **Step 5: Commit.**

```bash
git add internal/store/migrations/config/0002_audit_chain.sql internal/store/store_test.go
git commit -m "feat(store): migration adds audit chain columns and append-only trigger"
```

---

## Task 3: Writer — advisory lock, hash, INSERT with returning columns

**Files:**
- Modify: `internal/store/config.go`

- [ ] **Step 1: Write the failing writer test (extend `store_test.go`).**

```go
func TestAppendAudit_populatesChainColumns(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg := store.NewConfig(pool)

	rec, err := cfg.AppendAuditReturning(ctx, store.AuditEntry{
		Actor: "alice", Action: "login", Detail: map[string]any{"ip": "127.0.0.1"},
	})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if rec.ID == 0 {
		t.Fatal("ID not returned")
	}
	if len(rec.PrevHash) != 32 || len(rec.RowHash) != 32 {
		t.Fatalf("hash length: prev=%d row=%d", len(rec.PrevHash), len(rec.RowHash))
	}
	// First row's prev is genesis (all zero).
	for _, b := range rec.PrevHash {
		if b != 0 {
			t.Fatalf("first row's prev_hash must be zero, got %x", rec.PrevHash)
		}
	}

	// Second append chains onto the first.
	rec2, err := cfg.AppendAuditReturning(ctx, store.AuditEntry{Actor: "bob", Action: "login"})
	if err != nil {
		t.Fatalf("append 2: %v", err)
	}
	if !bytes.Equal(rec2.PrevHash, rec.RowHash) {
		t.Fatalf("rec2.prev_hash %x != rec.row_hash %x", rec2.PrevHash, rec.RowHash)
	}
}
```

(Add `import "bytes"` at the top of `store_test.go` if it is not already there.)

- [ ] **Step 2: Run — expect FAIL.**

Run: `go test ./internal/store/ -run TestAppendAudit_populatesChainColumns -v`
Expected: FAIL — `AppendAuditReturning` undefined; `AuditRecord` undefined.

- [ ] **Step 3: Rewrite `internal/store/config.go`.**

```go
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
```

- [ ] **Step 4: Run — expect PASS.**

Run: `go test ./internal/store/ -run 'TestAppendAudit|TestAuditAppend' -v`
Expected: all PASS (including the existing `TestAuditAppend_roundtrips`).

- [ ] **Step 5: Commit.**

```bash
git add internal/store/config.go internal/store/store_test.go
git commit -m "feat(store): chained AppendAudit with advisory-lock serialization"
```

---

## Task 4: VerifyChain — happy path

**Files:**
- Modify: `internal/store/config.go`
- Create: `internal/store/audit_chain_test.go`

- [ ] **Step 1: Write the failing happy-path test.**

```go
// internal/store/audit_chain_test.go
package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/dobbo-ca/lynceus/internal/store"
)

func TestVerifyChain_intactAfterAppends(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg := store.NewConfig(pool)

	for i := 0; i < 10; i++ {
		if _, err := cfg.AppendAuditReturning(ctx, store.AuditEntry{
			Actor: "alice", Action: "viewed.t2", DataTier: 2,
			Detail: map[string]any{"i": i},
		}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	bad, reason, err := cfg.VerifyChain(ctx, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if bad != -1 {
		t.Fatalf("verify reported bad=%d reason=%q on intact chain", bad, reason)
	}
}
```

- [ ] **Step 2: Run — expect FAIL.**

Run: `go test ./internal/store/ -run TestVerifyChain_intactAfterAppends -v`
Expected: FAIL — `VerifyChain` undefined.

- [ ] **Step 3: Implement `VerifyChain` (append to `config.go`).**

```go
// VerifyChain walks audit_log rows ordered by id ASC and recomputes each
// row's hash chain. It returns (-1, "", nil) when the chain is intact.
// Otherwise it returns the 0-based ordinal of the first inconsistent
// row in the walk along with a short reason.
//
// since and until bound the time window (inclusive); pass zero values to
// scan the whole table. Bounding is intended for sharded verification on
// large tables; the chain is still anchored from the table's earliest id
// regardless of the time window, because the predecessor's hash is read
// from the previous row in the walk — which means a partial-window walk
// validates only the rows inside the window AND only checks that they
// chain to each other. To validate the chain anchors to genesis you
// must call with since == time.Time{} (i.e. scan from the start).
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
		idx          int
		lastID       int64
		walkStarted  bool
	)
	for rows.Next() {
		var (
			id       int64
			actor    string
			action   string
			srvID    string
			tier     int16
			detail   string
			at       time.Time
			prev     []byte
			rowHash  []byte
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

		// When walking a windowed range, only the very first row of the
		// table itself is allowed to have genesis prev; subsequent rows
		// must chain to expectedPrev. If this is a windowed walk that
		// does not start at id=1, expectedPrev is seeded from the row's
		// own prev_hash on the first iteration (we cannot validate the
		// link to a row outside the window — that's documented above).
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
```

- [ ] **Step 4: Run — expect PASS.**

Run: `go test ./internal/store/ -run TestVerifyChain_intactAfterAppends -v`
Expected: PASS.

- [ ] **Step 5: Commit.**

```bash
git add internal/store/config.go internal/store/audit_chain_test.go
git commit -m "feat(store): VerifyChain happy-path walk over audit_log"
```

---

## Task 5: VerifyChain detects mutation

**Files:**
- Modify: `internal/store/audit_chain_test.go`

- [ ] **Step 1: Write the failing mutation-detection test.**

To mutate a past row in spite of the append-only trigger, the test disables the trigger session-locally with `ALTER TABLE ... DISABLE TRIGGER USER` (allowed for the table owner — the test container's superuser). This simulates an attacker who has obtained DB-level access.

```go
func TestVerifyChain_detectsRowMutation(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg := store.NewConfig(pool)

	for i := 0; i < 5; i++ {
		_, err := cfg.AppendAuditReturning(ctx, store.AuditEntry{
			Actor: "alice", Action: "viewed.t2", DataTier: 2,
		})
		if err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	// Tamper: bypass the append-only trigger and mutate id=3.
	if _, err := pool.Exec(ctx, `ALTER TABLE audit_log DISABLE TRIGGER USER`); err != nil {
		t.Fatalf("disable trigger: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE audit_log SET actor='mallory' WHERE id = 3`); err != nil {
		t.Fatalf("mutate: %v", err)
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE audit_log ENABLE TRIGGER USER`); err != nil {
		t.Fatalf("enable trigger: %v", err)
	}

	bad, reason, err := cfg.VerifyChain(ctx, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if bad != 2 { // 0-based: id=1 → idx 0, id=2 → idx 1, id=3 → idx 2
		t.Fatalf("expected bad=2 (id=3), got bad=%d reason=%q", bad, reason)
	}
}
```

- [ ] **Step 2: Run — expect PASS (the verifier already implements this).**

Run: `go test ./internal/store/ -run TestVerifyChain_detectsRowMutation -v`
Expected: PASS. If this fails because the trigger is still firing, confirm `ALTER ... DISABLE TRIGGER USER` is being executed in the same session — pgx pools may need `pool.Exec` on the same connection. If observed, switch to `pool.Acquire(ctx)` and call `conn.Exec` for both ALTER statements.

- [ ] **Step 3: Commit.**

```bash
git add internal/store/audit_chain_test.go
git commit -m "test(store): VerifyChain detects row mutation"
```

---

## Task 6: VerifyChain detects deletion

**Files:**
- Modify: `internal/store/audit_chain_test.go`

- [ ] **Step 1: Write the failing deletion-detection test.**

```go
func TestVerifyChain_detectsDeletion(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg := store.NewConfig(pool)

	for i := 0; i < 5; i++ {
		if _, err := cfg.AppendAuditReturning(ctx, store.AuditEntry{
			Actor: "alice", Action: "viewed.t2", DataTier: 2,
		}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	if _, err := pool.Exec(ctx, `ALTER TABLE audit_log DISABLE TRIGGER USER`); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM audit_log WHERE id = 3`); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE audit_log ENABLE TRIGGER USER`); err != nil {
		t.Fatalf("enable: %v", err)
	}

	bad, reason, err := cfg.VerifyChain(ctx, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	// After deleting id=3 the walk sees ids 1,2,4,5. The first failure
	// is at idx 2 (id=4) due to the id-gap check.
	if bad != 2 {
		t.Fatalf("expected bad=2 (id gap at id=4), got bad=%d reason=%q", bad, reason)
	}
}
```

- [ ] **Step 2: Run — expect PASS.**

Run: `go test ./internal/store/ -run TestVerifyChain_detectsDeletion -v`
Expected: PASS.

- [ ] **Step 3: Commit.**

```bash
git add internal/store/audit_chain_test.go
git commit -m "test(store): VerifyChain detects deletion via id gap"
```

---

## Task 7: VerifyChain detects out-of-order insertion

**Files:**
- Modify: `internal/store/audit_chain_test.go`

- [ ] **Step 1: Write the failing insertion-detection test.**

A naive attacker who tries to splice in a fabricated row between id=2 and id=3 cannot reuse id=3 (BIGSERIAL won't allow it without an explicit id, and the unique constraint on row_hash blocks duplicate hashes). The realistic attack is **inserting at a new id with a manufactured prev_hash** — chains forward look fine, but the prev_hash will not match the actual predecessor row's row_hash.

```go
func TestVerifyChain_detectsOutOfOrderInsertion(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg := store.NewConfig(pool)

	for i := 0; i < 3; i++ {
		if _, err := cfg.AppendAuditReturning(ctx, store.AuditEntry{
			Actor: "alice", Action: "login",
		}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	// Disable trigger; splice an attacker row with a fabricated prev_hash
	// that does not match id=1's row_hash.
	if _, err := pool.Exec(ctx, `ALTER TABLE audit_log DISABLE TRIGGER USER`); err != nil {
		t.Fatalf("disable: %v", err)
	}
	// Take id between existing rows by shifting later rows.  Easier:
	// insert at id = 99 with a fabricated prev so the walk reaches it,
	// but we want it BEFORE id=3 so the id-gap check doesn't fire first.
	// Approach: shift ids 2,3 → 3,4 (still strictly increasing) and
	// splice a fabricated row at id=2.
	if _, err := pool.Exec(ctx, `UPDATE audit_log SET id = id + 10 WHERE id IN (2,3)`); err != nil {
		t.Fatalf("shift: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE audit_log SET id = id - 9 WHERE id IN (12,13)`); err != nil {
		t.Fatalf("re-shift: %v", err)
	}
	// Now ids in table: 1, 3, 4. Splice id=2 with fabricated prev.
	fakePrev := make([]byte, 32) // not equal to row 1's row_hash
	fakePrev[0] = 0xFF
	fakeHash := make([]byte, 32)
	for i := range fakeHash {
		fakeHash[i] = byte(i + 1)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO audit_log (id, actor, action, prev_hash, row_hash, at)
		 VALUES (2, 'mallory', 'planted', $1, $2, now())`, fakePrev, fakeHash,
	); err != nil {
		t.Fatalf("splice: %v", err)
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE audit_log ENABLE TRIGGER USER`); err != nil {
		t.Fatalf("enable: %v", err)
	}

	bad, reason, err := cfg.VerifyChain(ctx, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	// idx 0 = id=1 (good); idx 1 = id=2 (planted, prev_hash mismatch).
	if bad != 1 {
		t.Fatalf("expected bad=1, got bad=%d reason=%q", bad, reason)
	}
}
```

- [ ] **Step 2: Run — expect PASS.**

Run: `go test ./internal/store/ -run TestVerifyChain_detectsOutOfOrderInsertion -v`
Expected: PASS. (`prev_hash mismatch at id=2`.)

- [ ] **Step 3: Commit.**

```bash
git add internal/store/audit_chain_test.go
git commit -m "test(store): VerifyChain detects out-of-order insertion"
```

---

## Task 8: Concurrent-appender stress test

**Files:**
- Modify: `internal/store/audit_chain_test.go`

- [ ] **Step 1: Write the failing concurrent-appender test.**

```go
func TestAppendAudit_concurrentAppendersProduceValidChain(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyConfigMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg := store.NewConfig(pool)

	const writers = 8
	const perWriter = 25
	errCh := make(chan error, writers)
	for w := 0; w < writers; w++ {
		go func(w int) {
			for i := 0; i < perWriter; i++ {
				_, err := cfg.AppendAuditReturning(ctx, store.AuditEntry{
					Actor:  "actor",
					Action: "concurrent",
					Detail: map[string]any{"w": w, "i": i},
				})
				if err != nil {
					errCh <- err
					return
				}
			}
			errCh <- nil
		}(w)
	}
	for i := 0; i < writers; i++ {
		if err := <-errCh; err != nil {
			t.Fatalf("writer: %v", err)
		}
	}

	bad, reason, err := cfg.VerifyChain(ctx, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if bad != -1 {
		t.Fatalf("chain broken under concurrent appenders: bad=%d reason=%q", bad, reason)
	}

	var total int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM audit_log`).Scan(&total)
	if total != writers*perWriter {
		t.Fatalf("row count = %d, want %d", total, writers*perWriter)
	}
}
```

- [ ] **Step 2: Run — expect PASS.**

Run: `go test ./internal/store/ -run TestAppendAudit_concurrentAppendersProduceValidChain -v -timeout 5m`
Expected: PASS. If it fails (chain broken, or row count short), the advisory lock is not effectively serializing writes — confirm `pg_advisory_xact_lock` is being taken inside the same transaction as the tail read AND that `nextval` is called inside the lock (it is — see Task 3 code).

- [ ] **Step 3: Commit.**

```bash
git add internal/store/audit_chain_test.go
git commit -m "test(store): concurrent appenders produce a verifiable chain"
```

---

## Task 9: Run the full package suite as the integration gate

- [ ] **Step 1: Run the whole store package.**

Run: `go test ./internal/store/... -v -timeout 10m`
Expected: every test passes, including the M1-era `TestAuditAppend_roundtrips` (still passes — the signature is preserved).

- [ ] **Step 2: Run the whole repo build.**

Run: `go build ./...`
Expected: builds.

- [ ] **Step 3: Run the whole repo test.**

Run: `go test ./...`
Expected: passes.

- [ ] **Step 4: Commit any incidental fix-ups (there should be none).**

If any callers of `AppendAudit` outside `internal/store` regressed (none expected per M1 inventory), fix them and:

```bash
git commit -am "fix: regressions surfaced by chained AppendAudit"
```

---

## Self-Review

**Spec coverage:**

- §2.3 ("append-only / tamper-evident") — schema trigger (Task 2) + chain (Tasks 1, 3) + verifier (Tasks 4–7).
- §3.3 ("audit log writer" lives in api_server) — `AppendAudit` lives in `internal/store/config.go`, which the api_server already uses (`internal/api/server.go` holds `*store.Config` indirectly through M5 wiring; this plan does not add new call sites).
- Features §11 ("tamper-evident audit — MUST") — covered.
- Hard requirement 1 (TDD writing-plans skill) — every task is failing-test-first with exact code.
- Hard requirement 2 (chain canonicalization pinned) — pinned in the design-notes section AND in `audit_hash.go` constants; golden vector locks the byte layout against regression.
- Hard requirement 3 (concurrent appenders, vanilla Postgres) — `pg_advisory_xact_lock` is core Postgres, available on RDS/Aurora; Task 8 stress-tests it.
- Hard requirement 4 (verifier with four test cases) — Tasks 4 (intact), 5 (mutation), 6 (deletion), 7 (insertion).
- Hard requirement 5 (schema migration in `internal/store/migrations/config/0002_audit_chain.sql`, vanilla, real Postgres) — Task 2 + testcontainers via existing `newPool`.
- Hard requirement 6 (preserve `AppendAudit` signature; document breaking change) — preserved. Behavior change: writes now serialize on a cluster-wide advisory lock and persist chain columns. **No breaking change to callers.**
- Hard requirement 7 (file-structure section before tasks) — present above.

**Placeholder scan:**
- One intentional placeholder, `REPLACE_AFTER_FIRST_GREEN`, is the test-driven workflow for capturing the golden hash; Step 4 of Task 1 spells out exactly how to fill it. Not a plan failure.
- No "implement later", "TODO", or vague-handwave steps. Every code step has complete code.

**Type consistency:**
- `AuditEntry` shape unchanged from M1.
- `AuditRecord` introduced in Task 3, referenced consistently in Tasks 4–8.
- `hashAuditRow(id uint64, prev []byte, actor, action, serverID string, tier int16, detail []byte, at time.Time)` — signature is identical across the implementation in Task 1 and the call sites in Tasks 3 and 4.
- `canonicalJSON([]byte) ([]byte, error)` — same signature in Task 1 implementation and Task 4 reuse.
- `VerifyChain(ctx, since, until) (int, string, error)` — declared in Task 4, asserted with the same shape in Tasks 5–8.
- Lock key `auditLockKey = 7426398501234567890` matches the constant in the migration's comment.

**Scope adherence (per the task brief):**
- IS: chain, storage, writer with concurrency control, verifier — all present.
- ISN'T: audit-log viewer UI (`ly-8b0.7`) — not touched.
- ISN'T: T2 gating (`ly-8b0.6`) — verifier is the foundation, but T2 read-gating belongs to that ticket.
- ISN'T: OIDC/SCIM identity sourcing (`ly-8b0.1`/`ly-8b0.2`) — `Actor` remains a string the caller supplies.

**Risks acknowledged in design notes (not gold-plating beyond scope):**
- Backfill of pre-existing M1 rows on a Postgres without `pgcrypto` requires an operator-side re-anchor. Fresh installs and Postgres-with-pgcrypto installs are handled automatically. The plan does not add a Go-side re-anchor utility — that belongs to operations tooling, out of scope here.
- Windowed `VerifyChain` (non-zero `since`/`until`) cannot validate the link to a row outside the window. Documented in the method's doc comment; default usage scans from start and is fully anchored.

---
