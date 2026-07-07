# Wire T2 audit-on-read (ly-06i)

## Goal

Build the enforceable T2 literal-read gateway: the ONLY code path that selects
`data_tier = 2` from the stats store. It fast-rejects on `servers.t2_enabled`,
authorizes via `EffectiveCapability` (config-DB rows only), writes a per-read
hash-chained `AppendAuditReturning(action="read", data_tier=2)` FIRST, and FAILS
CLOSED (returns no literal, non-nil error) if the audit write fails.

## Out of scope (explicit)

Group-RBAC over a `t2_grant` grantee-group table is DEFERRED to the later
T2AccessGrant feature. That table does NOT exist in the schema today. This bead
uses `store.EffectiveCapability` (capability_policy.go:169) as the authZ
primitive and does NOT create a `t2_grant`/RBAC table. The `t2_grant` group +
expiry RBAC and the OIDC/SCIM group check land in T2AccessGrant
(crd-operator-control-plane.md §6.1-6.2); this bead delivers only the
prerequisite gateway that blocks it.

## Deny-audit decision (recorded)

A denied attempt (t2_enabled=false OR EffectiveCapability not enabled/not found)
is NOT written as a `data_tier=2` audit row. Rationale: the audit chain records
successful literal DISCLOSURES; no literal leaves the store on a deny, so no
`data_tier=2` read row is appended. This keeps "exactly one `data_tier=2` audit
row per successful T2 read" exact and makes the tier-fast-reject assertion
(zero new `data_tier=2` rows) hold. The deny tests assert this behavior.

## Architecture

The gateway spans BOTH databases:
- config DB owns `servers.t2_enabled` (fast reject), `capability_policy`
  (`EffectiveCapability`), and the `audit_log` hash chain (`AppendAuditReturning`).
- stats DB owns the literal-capable `query_stats` rows written with `data_tier=2`.

So the gateway is a small standalone type `store.T2Reader{cfg Config; stats Stats}`
with one method. It uses ONLY interface methods, so the authZ + audit source of
truth is config-DB rows (no etcd/CR/in-memory snapshot).

### New Config interface method: ServerT2Enabled

There is no single-server `t2_enabled` getter today (`ResolveServer` inner-joins
instance+cluster, so it fails for unlinked servers). Add one tiny read:

```go
// ServerT2Enabled reports whether the servers row for serverID has
// t2_enabled=true. found is false when no such server row exists.
func (c *pgxConfig) ServerT2Enabled(ctx, serverID string) (enabled, found bool, err error)
```
Add it to the `Config` interface (config.go:15-30) and `var _ Config` stays valid.

### The gateway method

```go
type T2ReadRequest struct {
    ServerID     string
    DatabaseName string   // for EffectiveCapability resolution
    Capability   string   // authZ key (e.g. "query_text")
    Actor        string   // audit actor
    Since, Until time.Time
    Limit        int
}

// ReadT2QueryStats is the ONLY path that selects data_tier=2 from the stats
// store. Order (mirrors SetCapabilityPolicy fail-closed at capability_policy.go:54-91):
//   1. fast-reject on servers.t2_enabled (config DB) — before anything else.
//   2. EffectiveCapability (config DB) — deny if not enabled/not found.
//   3. AppendAuditReturning(action="read", data_tier=2) FIRST — fail closed on err.
//   4. only then the data_tier=2 SELECT from query_stats.
func (r *T2Reader) ReadT2QueryStats(ctx, req T2ReadRequest) ([]QueryStat, error)
```

`Detail` on the audit row carries STRUCTURAL keys only (server_id via ServerID
field, database_name, capability, since/until, limit) — NO literal.

The tier-2 SELECT is a new exported `ReadQueryStatsTier2` on `pgxStats` that the
gateway calls AFTER the audit append. The gateway is its sole caller, so it is the
single choke point. The guard test asserts exactly one `data_tier = 2` SQL literal
in non-test store code.

## TDD sequence

Write `internal/store/t2_read_test.go` (package `store_test`, real Postgres via
`newPool`, TWO pools: one config-migrated, one stats-migrated — mirrors
pool_routing_test.go which spins two containers).

1. **FAIL-CLOSED (headline, write FIRST):** config+stats migrated, seed
   `servers` row with `t2_enabled=true`, seed an enabled `capability_policy` via
   `SetCapabilityPolicy`, write a `data_tier=2` query_stats row. Then CLOSE the
   config pool (poisons `AppendAuditReturning`). Call `ReadT2QueryStats` →
   assert non-nil error AND zero rows returned. (Red first: type/method doesn't
   compile yet.)
2. **Happy-path audit:** t2_enabled=true + authorized + a `data_tier=2` row.
   Snapshot audit count via `ListAudit(AuditFilter{Action:"read", Tier:&two})`,
   call read, assert delta == 1 and rows returned, and
   `VerifyChain(ctx, zero, zero) == (-1,"",nil)`.
3. **Tier fast-reject:** t2_enabled=false. Assert error/empty AND zero new
   `data_tier=2` audit rows (append never reached).
4. **Authorization-deny:** t2_enabled=true but EffectiveCapability enabled=false
   (or no row). Assert denied/empty AND (per decision) zero new `data_tier=2`
   audit rows.
5. **Source-of-truth:** flip the config `servers.t2_enabled` row false→true and
   the capability row, assert the gate decision flips — proving config-DB rows
   are the only source.
6. **No-unaudited-path guard:** a Go test that greps non-test `internal/store`
   source and asserts exactly one `data_tier = 2` SELECT literal, located in the
   tier-2 read method the gateway calls.
7. Regression: existing `data_tier = 1` reads untouched — covered by the
   unchanged existing suite (`go test ./internal/store/...`).

## Files

- `internal/store/t2_read.go` — `T2Reader`, `T2ReadRequest`, `NewT2Reader`,
  `ReadT2QueryStats`; `ServerT2Enabled` on pgxConfig + Config interface;
  `ReadQueryStatsTier2` on pgxStats + Stats interface.
- `internal/store/t2_read_test.go` — the seven tests above.

## Done when

`go build ./...` and `go test ./internal/store/...` pass; all acceptance
criteria in ly-06i are green.
