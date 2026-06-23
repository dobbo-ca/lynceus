# Plan: `internal/policyresolve.Flatten` — pure capability-policy scope flattener (ly-3pb)

## Context

The CRD control-plane plan (`docs/superpowers/plans/crd-operator-control-plane.md` §3,
lines 199-241) introduces a four-tier capability-policy scope tree ABOVE the
server level — **fleet → group → instance → database** — that must flatten DOWN
to the exactly-two stored levels the existing resolver understands:

- a **server-wide default** row (`database_name` NULL / `""`), and
- a **per-database override** row (`database_name != ""`).

This bead delivers ONLY the pure-function core specified at §3 line 241:

```
internal/policyresolve.Flatten(policies []ScopedPolicy, graph EntityGraph)
    -> (rows []DesiredRow, conflicts []Conflict)
```

No k8s / store / DB / api wiring. The `DesiredRow -> store-upsert` adapter is
deferred to the later `CapabilityPolicyController` bead (F2). This package builds
and tests standalone with pure unit tests (no testcontainers).

### The load-bearing privacy invariant

A `servers` row == **one collector CONNECTION** == one `server_id` == one
`current_database()` gate key (crd plan §2.3 line 134; `caps/gate.go:5-13`). The
downstream resolvers only ever see:

1. the NULL server-wide default, and
2. an override whose `database_name == the connection's current_database`.

They **silently ignore** override rows for sibling datnames on the same
connection (`collector/policy_refresh.go:56-58`;
`store/capability_policy.go:172-178`). Therefore `Flatten` MUST:

- emit fleet/group/instance-tier winners as the **NULL-default row** per
  in-scope `server_id`;
- emit a **per-db override row ONLY** for database-tier winners AND only keyed to
  the stream whose `current_database == database_name`;
- emit **NO** override rows for sibling datnames (they would be dead rows);
- surface a **Warning-class signal** when a `databaseSelector` policy names a
  database that is no in-scope stream's `current_database` (the §3 line 226
  Warning event).

## Types (all plain structs declared in `internal/policyresolve`)

```go
// Scope is the policy tier, broadest -> narrowest.
type Scope string
const (
    ScopeFleet    Scope = "fleet"
    ScopeGroup    Scope = "group"     // clusterSelector by label
    ScopeInstance Scope = "instance"  // instanceSelector by label
    ScopeDatabase Scope = "database"  // databaseSelector, matched by current_database
)

// CapabilityToggle is one capability's desired state inside a policy.
type CapabilityToggle struct {
    Capability caps.Capability
    Enabled    bool
}

// ScopedPolicy is one CapabilityPolicy CR, already resolved to its scope +
// matching keys. Selection is expressed as plain data so Flatten stays pure:
//   - fleet:    applies to every server in the graph.
//   - group:    Selector matches a server's ClusterID-tier labels.
//   - instance: Selector matches a server's InstanceID-tier labels.
//   - database: DatabaseName names the target current_database (Selector also
//               narrows which servers are considered, mirroring the CR's
//               required selector).
type ScopedPolicy struct {
    Name         string            // for conflict/warning attribution (CR name)
    Scope        Scope
    Priority     int               // higher wins within a tier
    Selector     map[string]string // label match (group/instance/database)
    DatabaseName string            // database scope only: target current_database
    Capabilities []CapabilityToggle
}

// Entity is one in-scope server (one collector connection / one stream).
type Entity struct {
    InstanceID      string
    ClusterID       string
    CurrentDatabase string            // the connection's current_database()
    Labels          map[string]string // cluster+instance labels propagated to the stream
}

// EntityGraph maps in-scope server_id -> Entity.
type EntityGraph map[string]Entity

// DesiredRow mirrors store.CapabilityPolicy's storable shape (the F2 target).
type DesiredRow struct {
    ServerID     string
    DatabaseName string // "" == NULL server-wide default
    Capability   caps.Capability
    Enabled      bool
}

// Conflict is a refuse-and-surface signal: equal tier AND equal priority AND
// conflicting enabled for one capability, OR a database-tier policy targeting a
// database_name no in-scope stream serves (dead-row warning).
type Conflict struct {
    Capability caps.Capability
    ServerID   string
    Reason     string // "conflict: ..." | "dead-row: ..." (Warning-class)
}
```

## Precedence algorithm (§3 lines 214-238)

For each in-scope `server_id` (from the graph), for each `caps.Capability` that
any matching policy mentions:

1. Collect every **matching** policy/toggle for that `(server_id, capability)`.
   A policy matches a server when:
   - `fleet`: always;
   - `group`/`instance`: `Selector` is a subset of the entity's `Labels`;
   - `database`: `Selector` subset of labels AND `DatabaseName ==`
     entity's `CurrentDatabase` (the single-current_database rule). A
     database-tier policy whose `DatabaseName` matches NO entity's
     `CurrentDatabase` produces a `dead-row` Conflict and emits no row.
2. `resolvePrecedence(candidates)` picks the winner:
   - narrower tier wins (database > instance > group > fleet);
   - within a tier, higher `Priority` wins;
   - equal tier AND equal priority AND conflicting `Enabled` => **refuse**:
     return a `Conflict` for that `(server_id, capability)`, emit no row for it.
     Other capabilities still resolve.
3. Emit rows from the winner (do NOT pre-collapse default vs override — the read
   path picks the winner; we emit BOTH when both tiers won):
   - a **broad winner** (fleet/group/instance) => `DesiredRow{server_id, "",
     cap, enabled}` (the NULL default);
   - a **database winner** => `DesiredRow{server_id, currentDatabase, cap,
     enabled}` (the override), keyed to that one stream's server_id.
   - When both a broad tier and the database tier have winners for the same
     `(server_id, capability)`, BOTH rows are emitted (override-beats-default
     fidelity; the resolver picks override at read time).

`resolvePrecedence` is computed per tier: first reduce each tier to its single
winner (highest priority; conflict if tied-and-disagreeing). The broad-default
row uses the best of fleet/group/instance; the override row uses the database
tier winner. This naturally yields the two-row output without collapsing.

### Absence (§3 line 219)

A `(server_id, capability)` with no matching policy at any tier yields NO
`DesiredRow`, so the snapshot omits the key and `caps.Gate.Allowed` fails open
(default-enabled). We never emit a "default true" row.

### Determinism (§4.3 line 275)

Output `rows` are sorted canonically by `(ServerID, DatabaseName, Capability)`;
`conflicts` by `(ServerID, Capability, Reason)`. Flatten is a pure function of
its inputs, so two calls — including on shuffled input slices — produce
byte-identical output.

## Test plan (TDD — write `flatten_test.go` FIRST)

Clone `internal/planextract/normalize_cond_test.go` structure: one struct slice
of cases, `t.Run(c.name, ...)`, with an explicit negative case. One subtest per
acceptance criterion:

1. `tier_precedence` — fleet enabled=false + instance enabled=true on same
   server => winner enabled=true from instance tier (one NULL-default row,
   enabled=true).
2. `priority_tiebreak` — two same-tier policies, higher Priority wins; loser
   dropped.
3. `conflict_refusal` — equal tier + equal priority + conflicting enabled for
   capability A => a `Conflict` for A and NO row for A; capability B in the same
   set still resolves and emits a row.
4. `absence_no_row` — a capability with no policy at any tier => absent from
   rows.
5. `serverwide_flatten_target` — a group policy matching two server_ids =>
   exactly two NULL-default rows, one per server_id.
6. `database_tier_targeting` — database policy with DatabaseName == one stream's
   current_database => one override row (DatabaseName != "") keyed to that
   server_id.
7. `dead_row_suppression` — database policy with DatabaseName matching no
   stream's current_database => NO override row + a Warning-class Conflict; no
   sibling-datname rows.
8. `override_beats_default_two_rows` — fleet default enabled=false + database
   override enabled=true (matching current_database) => BOTH a NULL-default row
   (enabled=false) AND an override row (enabled=true). No pre-collapse.
9. `determinism_run_twice_and_shuffled` — same inputs and shuffled-slice inputs
   produce byte-identical sorted rows + conflicts.

All tests pure: no testcontainers, no DB, no network. Plus a verification that
the package imports only stdlib + `internal/caps` (grep, not a test).

## Implementation steps

1. `flatten_test.go` (failing) — table-driven, one subtest per criterion above.
2. `flatten.go` — the types, `resolvePrecedence` helper, `Flatten`. stdlib +
   `internal/caps` only.
3. Verify: `go build ./internal/policyresolve/...`, `go vet
   ./internal/policyresolve/...`, `go test ./internal/policyresolve/...` green;
   grep confirms no k8s/store/DB imports.

## Out of scope (independence — P0, edge-free vs F2-F5)

- No store/api/collector/k8s wiring.
- No `DesiredRow -> store.SetCapabilityPolicyInput` adapter (F2).
- No selector-from-`metav1.LabelSelector` parsing (operator's job; this package
  takes already-resolved `map[string]string`).
