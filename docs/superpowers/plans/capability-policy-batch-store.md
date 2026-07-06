# Plan: Batch + bulk capability_policy store methods (ly-ovm)

## Goal
Add three concrete `*pgxConfig` (store) primitives so a CRD fleet apply collapses
from an N+1 read storm + ~2,600 serialized audit-lock acquisitions into ONE bulk
read + ONE audited batch write.

## Deliverables (methods on the concrete `*pgxConfig`, NOT the interface — no consumers yet)
1. `ListCapabilityPoliciesForServers(ctx, serverIDs []string) ([]CapabilityPolicy, error)`
   — one query, `WHERE server_id = ANY($1)`, stable order. Empty/nil -> empty slice, no error.
2. `ApplyCapabilityPoliciesBatch(ctx, upserts []SetCapabilityPolicyInput, deletes []CapabilityPolicyKey, actor string) error`
   — ONE tx: `pg_advisory_xact_lock(auditLockKey)` once -> ONE audit row
   (`capability_policy.bulk_set`, detail = {row_count, content_hash}) via an
   extracted private tx-step helper (reuses `hashAuditRow` + `canonicalJSON`) ->
   ONE multi-row UPSERT (unnest-driven, conflict target `capability_policy_uniq`)
   -> ONE multi-row DELETE (unnest-driven) -> commit. Every mutated row carries
   the single returned audit id in `audit_chain_id`.
3. `DeleteCapabilityPolicy(ctx, key CapabilityPolicyKey, actor, reason string) error`
   — audited single delete; audit-FIRST inside one advisory-locked tx, same
   tx-step helper.
4. `CapabilityPolicyKey {ServerID, DatabaseName, Capability}` delete/diff key type.

## Design notes
- Extract the audit-append body of `AppendAuditReturning` (config.go:184-256) into a
  private helper `appendAuditTx(ctx, tx, AuditEntry) (AuditRecord, error)` that takes a
  caller-supplied `pgx.Tx` and does NOT lock or commit (caller owns lock + commit).
  `AppendAuditReturning` becomes: begin tx, lock, call helper, commit.
- `DatabaseName == ""` -> SQL NULL (reuse dbArg nil handling). Multi-row UPSERT/DELETE
  use `unnest($1::text[], $2::text[], ...)` so `''` in the array must map to NULL — pass
  `[]*string` for database_name (nil for server-wide).
- Validation mirrors SetCapabilityPolicy: empty actor, or any upsert/delete with empty
  ServerID/Capability -> error BEFORE any tx / audit write.

## TDD sequence (all tests in internal/store/capability_policy_test.go, package store_test)
1. INVARIANT-2 test: batch with >=1 upsert + >=1 delete -> audit count delta == 1,
   every mutated row's audit_chain_id == the bulk_set audit id.
2. Audit-FIRST test: all affected rows carry the bulk_set audit id.
3. Idempotent re-apply test: apply identical batch twice -> row count unchanged, a NEW
   bulk_set audit row each apply.
4. Multi-row DELETE test: seed rows, delete a subset (server-wide NULL + per-db keys),
   assert only those gone; non-existent key = no-op.
5. ListCapabilityPoliciesForServers test: >=2 servers, returns all requested rows in one
   query, excludes non-requested; empty/nil -> empty slice.
6. DeleteCapabilityPolicy test: set a row, delete it, row gone, audit appended,
   VerifyChain intact.
7. Chain-integrity test: interleave batch + single-row + delete, VerifyChain == (-1,"",nil).
8. Validation test: empty actor / empty ServerID / empty Capability -> error, audit count
   unchanged.

## Verify
`go test ./internal/store/... && go build ./... && go vet ./...`
