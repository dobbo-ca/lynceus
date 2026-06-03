# Read/Write DSN Split for Postgres — Design

**Issue:** ly-lt9
**Date:** 2026-06-02
**Status:** Approved

## Problem

All Postgres access in Lynceus currently goes through a single connection pool per
database (`store.Config` → config DB, `store.Stats` → stats DB). Every read and
write therefore hits the primary. As read volume grows (the audit-log viewer, the
top-queries dashboard, capability-policy lookups), we want to serve standalone
reads from a read replica to keep load off the primary.

We want to establish this pattern **now**, across both stores, before more read
paths are added.

## Guiding principle: read-your-writes

Read replicas are assumed to replicate **asynchronously** and may lag. Therefore:

> A read is routed to the **primary** only when it must observe a write made in the
> same logical operation (read-your-writes). Every other read goes to the
> **replica**.

A pure, standalone `SELECT` that can tolerate slightly stale data goes to the
replica. A read that is part of a read-modify-write transaction — or that must
immediately reflect a just-committed write — goes to the primary.

## Architecture

### Pool pair per store

Each store gains an optional read pool alongside its existing primary pool. The
existing field name `pool` is retained as the **primary (read-write)** connection;
a new `ro` field holds the **read replica** pool and defaults to `pool` when no
replica is configured.

```go
type Config struct {
    pool *pgxpool.Pool // primary (read-write): writes + read-your-writes reads
    ro   *pgxpool.Pool // read replica; defaults to pool when not split
}

// NewConfig binds a Config to its primary pool. Reads fall back to the
// primary until a read pool is attached via WithReadPool.
func NewConfig(pool *pgxpool.Pool) *Config { return &Config{pool: pool, ro: pool} }

// WithReadPool attaches a read-replica pool used for standalone reads.
// A nil ro is ignored (reads keep using the primary). Returns the
// receiver for chaining.
func (c *Config) WithReadPool(ro *pgxpool.Pool) *Config {
    if ro != nil {
        c.ro = ro
    }
    return c
}
```

`store.Stats` gets the identical treatment (`pool` primary, `ro` replica,
`NewStats`, `WithReadPool`).

**Why this shape (vs. a shared `store.DB{RW, RO}` type):** it is backward
compatible. Every existing `NewConfig(pool)` / `NewStats(pool)` call keeps
compiling and behaves exactly as today (reads fall back to the primary). Only
`cmd/api` opts into a real replica via `.WithReadPool(...)`. It also avoids
churning the field name `pool`, so the concurrently-developed capability-policy
code (ly-xnk.2) that references `c.pool` for its writes is unaffected. A shared
`DB` type was rejected as over-engineering (YAGNI) — it would force every call
site to change for no functional gain.

### Operation routing

| Store | Primary (`pool`) | Replica (`ro`) |
|-------|------------------|----------------|
| **Config** | `AppendAudit` / `AppendAuditReturning` (transaction: advisory lock + tail-hash read + insert = read-your-writes), `SetCapabilityPolicy` (audited upsert), migrations (`ApplyConfigMigrations`) | `ListAudit`, `VerifyChain`, `GetCapabilityPolicy`, `EffectiveCapability`, `ListCapabilityPolicies` |
| **Stats** | `WriteQueryStats`, `EnsureWeeklyPartition` (DDL), `DropPartitionsOlderThan` (DDL), migrations (`ApplyStatsMigrations`) | `TopQueriesByTotalTime` |

Notes:
- **`AppendAudit*`** runs as a single transaction on the primary pool; its
  internal tail-hash `SELECT` is naturally read-your-writes and stays on the
  primary. No special handling needed beyond using `pool` for the transaction.
- **`VerifyChain`** is a pure read and goes to the replica. It verifies whatever
  the replica currently holds; on a lagging replica it validates a prefix of the
  chain. This is acceptable per the read-your-writes principle (verification is
  not a read-your-writes operation).
- **Migrations / DDL / partition management** always use the primary.

### Environment variables (cmd/api)

| Var | Role | Required |
|-----|------|----------|
| `LYNCEUS_CONFIG_DSN` | config DB primary | yes |
| `LYNCEUS_CONFIG_RO_DSN` | config DB replica | no → falls back to primary |
| `LYNCEUS_STATS_DSN` | stats DB primary | yes |
| `LYNCEUS_STATS_RO_DSN` | stats DB replica | no → falls back to primary |

When an `*_RO_DSN` is unset, no read pool is created and reads use the primary —
this keeps single-node dev (one Postgres per DB) working with no extra config.

`cmd/ingestion` is write-only (it only writes query stats), so it is left
unchanged — no read pool is wired there.

## Data flow

```
cmd/api startup
  ├─ pgxpool.New(LYNCEUS_STATS_DSN)        → statsRW
  ├─ pgxpool.New(LYNCEUS_STATS_RO_DSN?)    → statsRO   (skipped if unset)
  ├─ pgxpool.New(LYNCEUS_CONFIG_DSN)       → configRW
  ├─ pgxpool.New(LYNCEUS_CONFIG_RO_DSN?)   → configRO  (skipped if unset)
  │
  ├─ store.NewStats(statsRW).WithReadPool(statsRO)
  └─ store.NewConfig(configRW).WithReadPool(configRO)

request: GET /audit
  → Config.ListAudit → c.ro (replica)

request: T2 access (future)
  → Config.AppendAuditReturning → c.pool (primary, single tx)
```

## Error handling

- Each `*_RO_DSN` that is set is opened with `pgxpool.New`; a failure is fatal at
  startup (same treatment as the primary DSN) so a misconfigured replica is caught
  immediately rather than silently falling back.
- An unset `*_RO_DSN` is not an error — the read pool is simply not attached.
- No runtime failover between replica and primary is in scope. If the replica is
  down, reads fail; operators point the RO DSN at a healthy endpoint (or unset it
  to use the primary). Connection-level resilience is delegated to pgxpool /
  pgbouncer.

## Testing

Integration tests against real Postgres (testcontainers), no DB mocks:

1. **Read is served from the RO pool.** Construct a store with the primary pool
   and a *distinct, empty* RO pool (a second container/database with the schema
   migrated but no rows). Write a row via the primary, then call a read method and
   assert it returns the **empty replica's** view — proving the read used `ro`,
   not `pool`. (We simulate "infinite lag" with an empty replica; this
   deterministically distinguishes the two pools without depending on real
   replication timing.)
2. **Fallback when no RO is set.** Construct the store with only the primary
   (`NewConfig(pool)` / `NewStats(pool)`), write and read back, assert the read
   succeeds off the primary.
3. **Existing suite stays green.** All current tests use the fallback path
   unchanged.

## pgbouncer forward-compatibility (note, no code now)

The DSN seam makes this design pgbouncer-neutral — pgbouncer's pool mode is a
property of the connection, not of our store types, so it never forks the design.
When Postgres is fronted by pgbouncer in `transaction` or `statement` pool mode:

1. Set pgx's exec mode to `QueryExecModeExec` (or enable pgbouncer ≥ 1.21
   prepared-statement support) on **both** the RW and RO pools — a single
   connection-config setting, identical for both pools.
2. Reduce pgxpool `max_conns`, since pgbouncer does the real multiplexing.

No structural change is required: the audit chain uses **transaction-scoped**
advisory locks (`pg_advisory_xact_lock`), which are safe under transaction
pooling, and the codebase holds no session-level state (no `SET`, `LISTEN/NOTIFY`,
`WITH HOLD` cursors, or temp tables). This is a deploy-time concern for whenever
pgbouncer actually lands; it is intentionally out of scope for this change.

## Out of scope

- Runtime replica health-checking / automatic failover.
- Splitting `cmd/ingestion` (write-only).
- Multiple read replicas / load-balancing across replicas (a single RO DSN may
  itself point at a load balancer or pgbouncer that fans out).
- Implementing the pgbouncer exec-mode configuration (documented above as a
  future deploy concern).
