-- ly-8b0.3 — Tamper-evident audit log.
--
-- Adds the hash-chain columns and the append-only trigger. Backfills any
-- rows that pre-existed under the M1 schema with a deterministic chain
-- starting at the 32 zero-byte genesis prev_hash. The canonical byte
-- layout being hashed is specified in the plan doc; the writer in
-- internal/store/audit_hash.go is the single source of truth.
--
-- Vanilla PostgreSQL — no extensions. Concurrent appenders are serialized
-- at write time via pg_advisory_xact_lock on the constant
-- 7426398501234567890 (the "audit chain" lock).

ALTER TABLE audit_log
    ADD COLUMN prev_hash BYTEA,
    ADD COLUMN row_hash  BYTEA;

-- One-time backfill for any rows present from M1. We cannot compute the
-- "true" canonical hash in SQL (canonical JSON ordering lives in Go), so
-- this writes a placeholder genesis chain. For a fresh install (no rows)
-- the loop is a no-op. Operators upgrading a system with existing audit
-- rows should run the documented re-anchor (TRUNCATE + re-import).
DO $$
DECLARE
    r RECORD;
    prev BYTEA := decode(repeat('00', 32), 'hex');
BEGIN
    FOR r IN SELECT id FROM audit_log ORDER BY id ASC LOOP
        UPDATE audit_log
           SET prev_hash = prev,
               row_hash  = digest(prev || int8send(r.id), 'sha256')
         WHERE id = r.id
        RETURNING row_hash INTO prev;
    END LOOP;
EXCEPTION WHEN undefined_function THEN
    -- pgcrypto digest() absent (vanilla RDS) — leave NULLs; the table is
    -- empty for fresh installs, and operators with pre-existing rows must
    -- run the documented re-anchor.
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
