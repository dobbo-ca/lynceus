# Lynceus Kubernetes Operator + CRD Control Plane — Final Design

- **Status:** Final (decision-ready, supersedes `crd-operator-control-plane-draft.md`)
- **Date:** 2026-06-19
- **Author:** Principal architect
- **Scope:** The k8s operator + CRD set that becomes Lynceus's declarative control plane for fleet config, capability policy, discovery-driven onboarding, and T2 access grants — self-hosted single-org FIRST, with a deliberate SaaS-multi-org seam.
- **Settled context this builds on:** the HYBRID ADR (`docs/research/grafana-clickhouse-pivot.md`, bead ly-22u): full Grafana+ClickHouse pivot REJECTED; api_server remains the **sole audited T2 gateway**; both config and stats DBs stay vanilla Postgres on RDS/Aurora; the fleet entity model (PR #28) is `cluster → instance → stream` over `server_id` (`internal/store/fleet.go`, `0005_fleet.sql`).

This revision incorporates or explicitly rebuts every blocker and major critique from the five adversarial reviews (k8s-conventions, code-fidelity, scale, privacy-adr, saas-portability). The disposition of every must-fix is tabulated in **§13 (Critique disposition)**.

---

## 0. TL;DR

**Source of truth: the CONFIG DB, with CRDs as a typed, version-controlled INPUT layer the operator reconciles INTO it.** (Decision §1, unchanged from draft — every reviewer agreed.) But the *mechanics* are substantially changed by the reviews:

1. **The foundational entity mapping was wrong and is corrected (§2).** A `servers` row / `server_id` is **one collector CONNECTION** (one `current_database()` gate key), **not** one logical database. The data plane proves this: the snapshot is `server_id`-keyed (`snapshot.proto:29`), `pg_stat_activity` carries cluster-wide `datname` (`activity_reader.go:49` — one stream observes EVERY database), and `caps.Gate` only honors per-db policy for the connection's `current_database` (`gate.go:6-13`; `policy_refresh.go:56-58` applies overrides only where `e.DatabaseName == db`). `CapabilityPolicy.databaseSelector` is redefined against this: a per-db override is only effective for the stream whose `current_database` equals that name.

2. **`PostgresDatabase` is demoted from a CRD to a DB/UI-only entity (§2.3).** It is operator-owned and discovery-driven — 1 object per stream = 140-210+ etcd objects (the dominant scaling axis) carrying churning status, and it is NOT covered by the "policy-by-selector keeps objects small" argument. The CRD set drops to **four**: `PostgresCluster`, `PostgresInstance`, `CapabilityPolicy`, `T2AccessGrant`, plus the internal-only `CollectorRegistration`. Per-stream Discovered/Final truth lives in the config DB + the existing capability matrix UI, never etcd.

3. **A single audited BATCH write path is mandatory (§4).** The existing `AppendAuditReturning` takes a **single global advisory lock** (`config.go:43` `auditLockKey`) with one tx + one INSERT per call, and the draft routed every flattened row through it — a fleet apply = up to ~2,600 globally-serialized writes that also block per-read T2 audits. The operator instead calls a new `ApplyCapabilityPolicies` admin endpoint → one audit chain entry per apply + one multi-row UPSERT under one lock.

4. **`T2AccessGrant` is hard-sequenced AFTER the api_server T2 read gateway (§6).** There is **no T2 read path in the code today** (zero `DataTier:2` writes, no RBAC table; `t2_enabled` is a scanned boolean nobody gates on). Shipping a grant on-switch first would be an unaudited enablement vector. The grant CRD is BLOCKED on a prerequisite bead delivering the read gateway + group RBAC + per-read `data_tier=2` audit that fails closed.

5. **The discovery write-back is reclassified as a new, contract-tested wire boundary with reason CODES, not free text (§5).** `caps.Discover` is not wired into the collector at all today; `UpsertDiscoveredCapabilities` has zero non-test callers; and probe `Reason` strings interpolate `err.Error()` and `%q raw` (`probes.go:40,78,86`) — they are **not** literal-free. Cross-boundary reasons become a closed enum, enforced by a new contract test.

6. **`cluster.org_id` is honestly labeled an UNBUILT prerequisite (§7), not an existing seam.** It is a comment only (`0005_fleet.sql:8`); zero org_id code exists. SaaS reuse requires a tenancy migration + tenant-scoping every config-store method (all currently org-blind) — a hard blocker on the SaaS path, not a thin adapter.

The operator remains a **k8s-native reconcile front-end** for the config DB. It does **not** serve the policy snapshot, does **not** hold authoritative policy, and does **not** sit in the collector data path. Its only output is the existing flat `capability_policy` row set that `GET /api/servers/{id}/policy-snapshot` already serves and the zero-touch collector already pulls into `caps.Gate` via `FetchPolicySnapshot` — **byte-for-byte unchanged below the rows.**

---

## 1. SOURCE-OF-TRUTH DECISION (the spine)

### Decision

> **The config/metadata Postgres DB is the source of truth. CRDs are a declarative, GitOps-friendly INPUT that the operator reconciles INTO the config DB. The api_server keeps serving `/policy-snapshot` from `capability_policy` exactly as today.**

| Option | What it means | Verdict |
|---|---|---|
| **A. CRDs source of truth** | Operator owns policy in etcd; operator (or a generated ConfigMap) serves the snapshot; config DB shrinks. | **Rejected** — see four forces below. |
| **B. Config DB source of truth, operator reconciles INTO it** | CRs are desired-state input; controller flattens scope → `capability_policy` rows via the audit-first store API; api_server unchanged. | **Chosen.** |
| **C. Pure config DB, no operator** | Status quo + a richer multi-scope schema; no k8s control plane. | Rejected as the *primary* design (fails k8s-native / GitOps / fleet authoring, ly-4ov) — but B degrades gracefully to C if the operator is removed. |

This is the control-plane application of the already-settled HYBRID ADR. The authoritative state is unambiguously the config DB; the snapshot/Gate/audit paths are frozen; CRDs are upstream desired-state input, not a co-equal source — so it is **configdb-source-of-truth**, not a new hybrid.

### Justification against the four named forces

**1. Zero-touch collector + the existing `FetchPolicySnapshot` path.**
The collector pulls `GET /api/servers/{id}/policy-snapshot` and resolves a flat `[{capability, database_name, enabled}]` list into `map[caps.GateKey]bool` (`policy_refresh.go:28-62`). It has **no config-DB handle and no k8s API access** (`gate.go:18-21`). Under A, either the collector learns to read CRs (breaks zero-touch / outbound-only; the collector is frequently a sidecar/Deployment beside an *external* RDS and must NOT need cluster RBAC) OR the operator stands up a *second* snapshot server competing with `handlePolicySnapshot` (`capabilities.go:168-185`). Both are worse than keeping the one existing, already-literal-free producer. Under B the collector path is **untouched** — the operator's output is the same `capability_policy` rows api_server already reads.

**2. api_server's role as T2 gateway / UI / audit.**
`capability_policy.server_id` FKs `servers(id)` and `capability_policy.audit_chain_id` FKs `audit_log(id)` (`0003_capability_policy.sql`); every policy change goes through `SetCapabilityPolicy`'s **audit-first** ordering (`capability_policy.go:43-99`) into the hash-chained, append-only, advisory-lock-serialized `audit_log` (`0002_audit_chain.sql`) which **MUST stay in vanilla Postgres** (ADR) and cannot live in etcd. The capability matrix, fleetview roll-ups, and the (future) T2 read path are all api_server + config-DB code. Under B the operator is just another **authenticated, audited writer** funneling through the SAME path (via an internal admin API — §4). Note (corrected per code-fidelity review): `actorFromContext` is currently a hardcoded `"dev-admin"` stub (`capabilities.go:34`); the admin API populates `SetBy`/actor directly with the operator principal, so the claim is "audited identically to a *real-actor* UI write" once M5 actor wiring lands — today both share the stub. The audit *coverage* and *ordering* guarantee is what matters and it is preserved.

**3. SaaS-multi-org-later.**
A SaaS deployment **cannot expose customer-facing CRDs** yet needs the **same** policy-first reconcile logic over an API. The flatten-scope-to-rows core is extracted as a framework-free `internal/policyresolve` package (§3, §7). The operator is a thin CRD→package adapter; a future SaaS API is a thin HTTP→package adapter. **Corrected scope (saas-portability review):** `policyresolve.Flatten` is a *pure function* and cannot enforce tenant isolation; and `cluster.org_id` does not exist. So SaaS reuse is NOT a thin adapter — it requires a tenancy migration plus a shared tenant-scoped data-access layer (§7). The "one brain" claim is narrowed to the precedence math, with the security boundary called out as separate, shared, and unbuilt.

**4. Self-hosted-single-org is the FIRST target.**
B ships value immediately: GitOps a `CapabilityPolicy` scoped to `env: prod`; the operator flattens it to rows; collectors pick it up on their next ticker — no per-DB hand-editing across 140-210 streams. The config DB stays authoritative, so the **existing UI keeps working unchanged** during rollout (operator writes are additional audited writers), and removing the operator degrades to option C with zero data migration. Option A would make the operator a hard runtime dependency of the data plane on day one — unacceptable for v1.

### Consequence (the invariant for the rest of this doc)

> **INVARIANT 1 (frozen data plane):** Nothing in the collector data path changes. The `/policy-snapshot` wire shape and `caps.Gate` semantics are frozen. The operator's reach stops at `capability_policy` rows.

> **INVARIANT 2 (single audited writer — promoted from open question to non-negotiable, per privacy-adr review):** The operator's ONLY config-DB write path is api_server's audit-first admin API. The operator NEVER imports `store`, NEVER opens a config-DB pool, and NEVER mutates `capability_policy` / `t2_grant` / `servers.t2_enabled` without a corresponding `audit_log` append. A test asserts no operator-reachable code path mutates those tables without an audit row.

---

## 2. CRD SET (four CRDs + one internal input)

Group: `lynceus.dev`. Version: `v1alpha1` (the storage version; no conversion webhook until `v1beta1` — §2.7). All namespaced **except** `CollectorRegistration` reasoning in §2.6.

### 2.0 What changed from the draft, and why

| Draft | Final | Reason |
|---|---|---|
| 5 CRDs incl. `PostgresDatabase` | **4 CRDs**; `PostgresDatabase` is DB/UI-only, not a CRD | scale review: 210+ etcd objects with churning status, not policy-by-selector, dominant scaling axis |
| `spec.serverID` minted by operator | minted `server_id` lives ONLY in `status` (+ immutable annotation); humans never see operator-written spec | k8s-conventions: controllers own status, never mutate spec |
| custom `Condition` struct | `metav1.Condition` verbatim (`observedGeneration` + `lastTransitionTime`) | k8s-conventions: SSA/kubectl tooling contract |
| `status.matchedServerIDs[]`, `serverIDs[]`, `grantedServerIDs[]` | counts + `Ready` condition; no per-id enumerations | scale: unbounded lists bloat etcd + watch fan-out at 210 streams |
| bare-string refs | typed same-namespace `LocalObjectReference` + `ownerReferences` | k8s-conventions: GC, adoption, tree display |
| validation = open question | CEL validation + enum markers now; webhook only for `caps.Declared()` | k8s-conventions: reject garbage at apiserver |
| no finalizers | finalizers on every CRD that writes config-DB state | k8s-conventions: CR deletion must audited-revoke external state |

### Discovered / Policy / Final triad placement

The real triad lives in `capabilityCellDTO` (`capabilities.go:16-29`): `DiscoveredAvail`+`DiscoveredReason`, `PolicyEnabled`+`PolicySource`, `FinalEnabled = DiscoveredAvail && effective` (`capabilities.go:80`; absent policy ⇒ enabled). Placement:

- **Policy → `CapabilityPolicy.spec`.** The only human-edited axis.
- **Discovered → config DB (`discovered_capability`), surfaced via the existing capability matrix UI.** NOT in etcd (it churns on every ~10m collector report).
- **Final → config DB (resolved), surfaced via the matrix UI + `/policy-snapshot`.** NOT in etcd.

This is the corrected consequence of demoting `PostgresDatabase`: per-stream Discovered/Final never enter etcd, eliminating the status-churn antipattern (scale review must-fix).

---

### 2.1 PostgresCluster ↔ `cluster` (namespaced)

**Purpose:** logical grouping of instances (a primary + its replicas); top of the fleet hierarchy and the (future) SaaS tenancy anchor.

| | Field | Maps to / behavior |
|---|---|---|
| **spec** | `displayName` | `cluster.name` (arg to `CreateCluster(name)`) |
| | `labels` (CR `metadata.labels`; e.g. `env: prod`, `team: payments`) | scope selectors for CapabilityPolicy (§3) |
| | `orgRef` (optional, SaaS-only, **inert in v1alpha1**) | future `cluster.org_id` — see §7; NOT wired until the tenancy migration lands |
| **status** | `clusterID` | `cluster.id` minted by `CreateCluster` — operator-written only |
| | `instanceCount` | count from owned child CRs (bounded) |
| | `conditions[]` (`metav1.Condition`; positive `Ready`) | reconcile health |

> **Entity ownership (saas-portability must-fix):** the operator's `ClusterController` **reconciles-by-reference** — it calls `CreateCluster` ONLY if no row is bound, then pins `status.clusterID`. To make self-hosted and SaaS identical, the *eventual* unification is: api_server onboarding is the sole minter and the operator binds existing rows. For v1alpha1 (operator-only writer of cluster/instance rows; no SaaS), the operator may mint via the admin API, but `BackfillFleet` (`fleet.go:201`) is **disabled when the operator is deployed** (a deploy flag) so the data-plane onboarding path and the operator cannot both create cluster/instance rows for the same stream. This resolves the entity-ownership race the saas-portability review flagged.

### 2.2 PostgresInstance ↔ `instance` (namespaced)

**Purpose:** one Postgres endpoint within a cluster; `role` is primary/replica/unknown (ly-99s.3).

| | Field | Maps to / behavior |
|---|---|---|
| **spec** | `clusterRef` (typed `LocalObjectReference`, same namespace) | `instance.cluster_id`; sets `ownerReference` Instance→Cluster |
| | `displayName` | `instance.name` |
| | `role` (optional human pin; **immutable once set** via CEL `self == oldSelf`) | `instance.role`; coexists with `status.discoveredRole`. Drift avoided because spec `role` is a pin the operator never writes; if unset, only `status.discoveredRole` is populated. |
| **status** | `instanceID` | `instance.id`; operator-written only |
| | `discoveredRole` | role consolidation (ly-99s.3) — Discovered axis for topology |
| | `streamCount` | count, NOT a `serverIDs[]` list (scale: bounded) |
| | `conditions[]` | reconcile health |

### 2.3 PostgresDatabase — NOT a CRD (DB/UI-only entity)

**Decision (scale review must-fix):** the per-stream "monitored database" is **not** a CRD. It is the existing `servers` row (`ServerStream`, `fleet.go:11-38`), surfaced through the existing capability matrix UI and fleetview. Rationale:

- It is operator-OWNED and discovery-DRIVEN, never hand-authored desired state — exactly the case where a CRD adds churn without GitOps value.
- At 70+ instances × 2-3 streams = **140-210+ objects**, it is the dominant etcd object-count axis and is NOT covered by the "policy-by-selector keeps objects small" argument that justifies the rest of the design.
- Its natural status (`discoveredCapabilities`, `finalCapabilities`, `lastCollectorSeen`) is **volatile telemetry** refreshed on every ~10m collector report (`UpsertDiscoveredCapabilities` sets `observed_at = now()`, `discovered_capability.go:79`). Putting a per-10m timestamp on 210 etcd objects is a write-amplification antipattern.

**Corrected entity semantics (code-fidelity blocker):** a `servers` row == **one collector CONNECTION** == one `server_id` == one `current_database()` gate key — **not** one logical database. A single stream's `pg_stat_activity` stats span ALL datnames in the instance (`activity_reader.go:49`), but capability policy is gated only on the connection's `current_database` (`gate.go:6-13`, `policy_refresh.go:56-58`). Consequences threaded through the rest of the doc:

- `CapabilityPolicy.databaseSelector` targets the stream whose `current_database` matches; a per-db override row is only effective for that one stream (§3 flatten).
- Discovery/Final are per `(server_id, current_database, capability)`, surfaced in the matrix UI, not etcd.

Where the draft said "PostgresDatabase CR", read "the `servers`-row entity, surfaced in the UI and created via the onboarding flows in §5." `kubectl get` users see streams through `PostgresInstance.status.streamCount` + the UI; they do not get one CR per stream.

### 2.4 CapabilityPolicy (policy-first, scoped) → flattens to `capability_policy` rows (namespaced)

**Purpose:** the ly-4ov policy-first authoring object — the **Policy** axis. ONE CR targets a scope (fleet/group/instance/db) via label selector and sets many capabilities; the operator flattens precedence into per-`(server_id, database_name)` rows.

| | Field | Maps to / behavior |
|---|---|---|
| **spec** | `scope` (enum: `fleet`\|`clusterSelector`\|`instanceSelector`\|`databaseSelector`; CEL-validated) | which streams apply (§3) |
| | `selector` (`metav1.LabelSelector`; **required when scope != fleet** via CEL `self.scope=='fleet' \|\| has(self.selector)`) | matches Cluster/Instance CR labels (Database labels propagate from owning instance) |
| | `priority` (int, default 0) | deterministic tie-break within a tier (§3) |
| | `capabilities[]` (`{capability, enabled, reason}`; `MaxItems: 13`) | each → a `capability_policy` upsert; `capability` ∈ `caps.Declared()` (admission webhook, §9; admin-API backstop) |
| | `tunables` — **NOT present in v1alpha1** | reserved as a FUTURE *separate CRD/subresource* (§4.7), NOT a half-defined nested field (k8s-conventions: don't reserve unspecified nested fields in an alpha storage version) |
| **status** | `matchedCount` (int) | how many streams resolved into scope — count, NOT a list (scale) |
| | `appliedRows` (int), `lastAppliedAuditChainID` (int64) | proof the rows went through the audit chain |
| | `conditions[]` (`metav1.Condition`: positive `Ready`; negative-polarity `Stalled` with `reason=Conflict` / `reason=AdminAPIError`) | k8s-conventions: standard polarity, no positive-true "Conflict" |

> **No Discovered/Final on CapabilityPolicy** — it is pure intent spanning many streams. For a per-stream view of intent ⊓ reality, use the capability matrix UI.

### 2.5 T2AccessGrant → new `t2_grant` table (greenfield; **gated behind the read gateway, §6**) (namespaced)

**Purpose:** a **declarative, k8s-audited** authorization to ENABLE T2 for a scope. The CRD models the GRANT only. It **MUST NOT** be the per-read audit.

| | Field | Maps to / behavior |
|---|---|---|
| **spec** | `scope` + `selector` (same scoping + CEL rules as CapabilityPolicy) | streams the grant covers |
| | `tier` (enum: `T2`) | the data tier granted |
| | `granteeGroups[]` (`MaxItems` bounded) | OIDC/SCIM groups api_server checks **at read time** against the config-DB `t2_grant` row |
| | `expiresAt` (optional RFC3339) | time-boxed grant |
| | `justification` (`MaxLength: 512`; **operator-authored free text** — see §6 note) | recorded into the audit chain at apply |
| **status** | `grantedCount` (int) | streams now `t2_enabled=true` — count, NOT a list (scale) |
| | `lastAppliedAuditChainID` (int64) | the grant-creation audit row |
| | `conditions[]` (`Ready`; `Stalled` reason=`GatewayMissing` until §6 prerequisite lands) | |

> **HARD SEQUENCING (privacy-adr blocker):** `T2AccessGrant`, the `t2_grant` table, and `servers.t2_enabled` flips are **BLOCKED** on a prerequisite bead delivering the api_server T2 read path (§6.1). The CRD does not ship in the same release as, or ahead of, that gateway. Until then the controller sets `Stalled / reason=GatewayMissing` and writes nothing.

### 2.6 CollectorRegistration (internal input, not customer-authored) — bootstrap seam (**cluster-scoped**)

**Purpose:** mint/declare `server_id` identity + bootstrap params BEFORE first scrape (Flow A, §5). Insertion point the grounding identifies: nothing pre-registers a `server_id` today; `capability_policy.server_id` FKs `servers(id)`, so the row must exist first.

**Scope decision (k8s-conventions: don't ship "cluster-scoped or namespace" unresolved):** `CollectorRegistration` is **cluster-scoped**, because it mints *cluster-global* `server_id` identity and seeds `servers`/`cluster`/`instance` rows. It is created only by `lynceus-fleet-admin` / automation, never by tenant-namespace operators. (The other four CRDs are namespaced, matching the org=namespace self-hosted model.)

| | Field |
|---|---|
| **spec** | `instanceRef` (typed cross-namespace ref permitted here because it is cluster-scoped admin tooling), `databaseName` (expected `current_database`), `tokenSecretRef` (agent/license key Secret) |
| **status** | `serverID` (minted → seeds `servers` + default `capability_policy` rows via the audited admin API), `bootstrapConfigMapRef` (renders `LYNCEUS_SERVER_ID`/`LYNCEUS_INGESTION_URL`/`LYNCEUS_API_BASE_URL` — the existing env contract; **k8s-only convenience**, the underlying identity API is transport-neutral, §7) |

### 2.7 Finalizers, owner references, conditions, versioning (k8s-conventions must-fixes)

- **Finalizers.** `lynceus.dev/cleanup` on `CapabilityPolicy`, `T2AccessGrant`, `PostgresCluster`, `PostgresInstance`. On `deletionTimestamp`:
  - `CapabilityPolicy` → audited delete of its contributed rows (new audited `ApplyCapabilityPolicies` with the policy's rows removed from desired set) → remove finalizer.
  - `T2AccessGrant` → audited revoke: `t2_enabled=false` where no other grant covers the stream + delete the `t2_grant` row → remove finalizer. (Prevents the standing privacy hole the review flagged: CR deletion leaving T2 live.)
  - `PostgresInstance`/`PostgresCluster` → block deletion until children gone (owner refs), then audited unbind.
  - Note: DB-side `ON DELETE CASCADE`/`SET NULL` (`0005_fleet.sql`) does NOT substitute — the operator owns the *audited, sequenced* delete; the cascade is a backstop, not the path.
- **Owner references.** `controller: true` child→parent (Instance→Cluster) so k8s GC + the finalizer ordering + `kubectl tree` work.
- **Conditions.** `metav1.Condition` verbatim (`Type`, `Status`, `ObservedGeneration`, `LastTransitionTime`, `Reason`, `Message`) via `meta.SetStatusCondition`. Single positive `Ready`; abnormal state is `Stalled`/`Degraded` (negative polarity) with a `Reason` enum. `observedGeneration` is set on every status write so GitOps health checks detect staleness.
- **Versioning.** `v1alpha1` is the storage version. No conversion webhook until `v1beta1`. Forward-incompatible fields (tunables) are added in the version that implements them, as a separate CRD — not reserved as empty nested fields now.

---

## 3. SCOPE & PRECEDENCE (policy-first, ly-4ov)

Today there are exactly **two** stored levels (`capability_policy.go:169-193`): server-wide default (`database_name NULL`) and per-database override; precedence `per-db override » server-wide default » default-enabled`. The CRD layer introduces the richer scope tree ABOVE server and flattens it back DOWN to those two levels so `EffectiveCapability` and the snapshot are unchanged.

### Scope tiers (broadest → narrowest)

```
1. fleet            (scope: fleet)                       — every stream in the namespace
2. group            (scope: clusterSelector  by label)  — e.g. env=prod, team=payments
3. instance         (scope: instanceSelector by label)  — a specific endpoint
4. database         (scope: databaseSelector by label)  — a specific stream (matched by current_database)
```

### Deterministic precedence resolution

For each `(server_id, capability)` the operator computes the winner:

1. **Tier** — narrower wins (database > instance > group > fleet).
2. **Priority** — within a tier, higher `spec.priority` wins.
3. **Tie-break** — equal tier AND equal priority AND conflicting `enabled` ⇒ `Stalled / reason=Conflict` on both CRs; the operator **refuses to apply that capability** for the affected streams and leaves the prior row intact (no silent flip). *(Open Q resolved in body: refuse-and-surface, not hard-fail the whole CR — a conflict on one capability must not block the other capabilities in the same CR.)*
4. **Absence** — no policy at any tier ⇒ no row ⇒ snapshot omits the key ⇒ `caps.Gate` fails open / default-enabled (`gate.go:36-44`). Preserves today's semantics.

### Flatten algorithm — corrected to the real resolver (code-fidelity must-fix)

The consuming resolver knows only TWO levels **per server_id**: the NULL default vs an override matching the collector's single `current_database` (`EffectiveCapability` + `FetchPolicySnapshot` two-pass). There is no notion of "all databases of a server" at resolution time. Therefore:

- **group/instance/fleet-tier winners flatten to the server-wide default row (`database_name = NULL`) for each in-scope `server_id`.**
- **database-tier winners flatten to a per-database override row that is only effective for the stream whose `current_database == database_name`.** The operator emits the override row keyed to that stream's `server_id`; it does NOT emit override rows for sibling datnames on the same connection (those would be dead rows the resolver ignores). The operator surfaces a `Warning` event if a `databaseSelector` policy names a database that is not any stream's `current_database` (it would never apply).

```
for each in-scope server_id:
  for each capability:
    win = resolvePrecedence(tiers 1-4)   // pure fn
  // collapse to storable rows, honoring the single-current_database resolver:
  desired[server_id] = {
     NULL-default rows  (from fleet/group/instance winners),
     per-db override row ONLY where the winning policy is database-tier
                          AND database_name == this stream's current_database
  }
diff desired vs actual; emit ONE audited batch apply per reconcile (§4.3)
```

> **Pure-function core:** `internal/policyresolve.Flatten(scopedPolicies, entityGraph) → (rows []DesiredRow, conflicts []Conflict)`. No k8s imports. **Complexity:** `O(streams × caps × policies-per-scope)` ≈ `210 × 13 × small` = thousands of cheap comparisons, sub-millisecond (scale review: bound stated so reviewers can sanity-check it stays sub-second). The result is memoized keyed by `(policy generation vector, entity-graph hash)`; a resync short-circuits when nothing changed (§4.3).

---

## 4. OPERATOR RECONCILE

### 4.1 Topology

- **Deployment:** `lynceus-operator`, inside k8s, leader-elected for HA (single active reconciler).
- **Config-DB access:** the operator does **NOT** open a raw pgx pool (INVARIANT 2). It talks to api_server over a new **internal admin API** (mTLS / SA-token authenticated, cluster-internal Service only), so every change funnels through the audit-first path. Actor attribution is `operator:<crd-uid>` passed as `SetBy` directly into the admin endpoint (bypassing the `actorFromContext` UI stub, which only the SSR handler uses).
- **Boundary unification (saas-portability minor):** validation/conflict/attribution live in ONE in-process service function on api_server (e.g. `admin.ApplyCapabilityPolicies`). Both the operator's HTTP admin endpoint AND a future SaaS HTTP handler are thin transports in front of that same function — neither re-implements the rules.

### 4.2 Controllers

| Controller | Watches | Reconciles into |
|---|---|---|
| **ClusterController** | PostgresCluster | `cluster` rows (reconcile-by-reference; §2.1); binds `status.clusterID` |
| **InstanceController** | PostgresInstance (owned-by Cluster) | `instance` rows; binds `status.instanceID` |
| **CapabilityPolicyController** | CapabilityPolicy + indexed Cluster/Instance label changes | `policyresolve.Flatten` → **one audited batch apply** of `capability_policy` rows |
| **T2GrantController** | T2AccessGrant (gated, §6) | `t2_grant` rows + `servers.t2_enabled`; one audited batch grant apply |
| **DiscoveryController** | (NO collector contact) periodic poll of api_server's discovery feed | reads `discovered_capability`; writes `streamCount`/conditions on parent CRs only |

`server_id` minting + the per-stream entity now live in the config DB (not a CR), driven by §5 onboarding — there is no `DatabaseController`.

### 4.3 The reconcile loop (CapabilityPolicy — the load-bearing one), with the scale fixes

```
trigger: CR change | indexed entity-label change | drift-audit resync (default 30m, NOT 5m)
debounce/coalesce window: 3s   (collapse a GitOps relabel burst into ONE pass)

1. shortCircuit: if (policy generation vector + entityGraph hash) unchanged since last pass → return.
2. List CapabilityPolicy CRs in namespace; build/refresh cached EntityGraph from Cluster/Instance CRs.
3. desired = policyresolve.Flatten(policies, graph)         // pure, memoized (§3)
4. actual  = adminAPI.ListCapabilityPoliciesForServers(serverIDs)   // ONE bulk call, WHERE server_id = ANY($1)
5. diff desired vs actual.
6. adminAPI.ApplyCapabilityPolicies(batch{upserts, deletes, actor: operator:<uid>})
      → api_server: ONE AppendAuditReturning (action capability_policy.bulk_set,
        detail = {row_count, content_hash}) + ONE multi-row UPSERT/DELETE under ONE advisory-lock tx.
7. On AdminAPIError → exponential backoff with jitter (controller-runtime default workqueue limiter,
   base 5ms → max 1000s); apply is idempotent + resumable (re-running diffs to empty).
8. Write status: matchedCount, appliedRows, lastAppliedAuditChainID, conditions — ONLY if changed
   (compare-before-write); spec-watch predicate is GenerationChangedPredicate so status writes
   never re-trigger reconcile.
9. (No collector contact. Ever.)
```

**Scale fixes baked in (review must-fixes):**

1. **Batch audit (blocker).** New admin endpoint `ApplyCapabilityPolicies` → ONE audit chain entry per apply + ONE multi-row UPSERT under ONE `auditLockKey` tx (`config.go:43`). Collapses up to ~2,600 lock acquisitions to 1. New store function `ApplyCapabilityPoliciesBatch(ctx, rows, deletes, actor)` and the audited `DeleteCapabilityPolicy` it needs are **prerequisite beads** (neither exists today; `SetCapabilityPolicy` is single-row, there is no delete fn).
2. **Bulk read (major).** New `ListCapabilityPoliciesForServers([]serverID)` using `WHERE server_id = ANY($1)` — the exact pattern already in `rollup.go:32` / `insights.go:75`. Replaces the draft's per-server N+1 (400 queries at 200 streams) with O(1).
3. **Incremental resync (major).** The blind 5m full sweep becomes a 30m drift-audit, gated by the generation/graph short-circuit (step 1) so a quiet fleet pays nothing.
4. **Rate-limit posture (major).** 3s debounce; controller-runtime exponential-backoff-with-jitter on admin-API errors; per-reconcile write budget chunks very large applies and yields the lock between chunks; idempotent resumable applies so a retry re-diffs to empty rather than re-auditing.
5. **No volatile telemetry in etcd (major).** `lastCollectorSeen`/`observedAt` stay in the config/stats DB + UI (consequence of demoting `PostgresDatabase`); derived status is written only on content change with SSA + a dedicated field manager.

### 4.4 EXACTLY how CRs become the snapshot the zero-touch collector pulls

```
CapabilityPolicy CR (scoped, label-selectored)
        │  CapabilityPolicyController + policyresolve.Flatten + ApplyCapabilityPolicies (ONE audited batch)
        ▼
capability_policy rows  (server-wide default NULL  +  per-current_database override)   [config DB, audited]
        │  api_server handlePolicySnapshot → ListCapabilityPolicies      [UNCHANGED]
        ▼
GET /api/servers/{id}/policy-snapshot  →  [{capability, database_name, enabled}]   [UNCHANGED wire shape]
        │  collector FetchPolicySnapshot (two-pass: defaults then this current_database's overrides) [UNCHANGED]
        ▼
map[caps.GateKey]bool  →  gate.Replace on the full-snapshot ticker        [UNCHANGED]
```

The collector never learns the word "CRD". The operator's reach stops at `capability_policy` rows.

### 4.5 Why no new collector mechanism

Capability on/off needs **zero** payload change — it is already the snapshot's shape. Tunables (intervals, schema regexps, log settings — today env vars in `cmd/collector/main.go:299-360`) are the FUTURE expansion (§4.7).

### 4.6 Discovery write-back — RECLASSIFIED as a new contract-tested wire boundary (code-fidelity + privacy-adr blockers)

The draft mis-sold this as near-free reuse. The truth:

- `caps.Discover` is **not wired into `cmd/collector/main.go`** at all (no `NewDiscoverer` call).
- The only collector→server wire shape is the `Snapshot` proto (`snapshot.proto:29`), which has **no capability/discovery field** and is **frozen by the T1 contract test** (`internal/proto/lynceus/v1/contract_test.go`).
- `UpsertDiscoveredCapabilities` has **zero non-test callers** (`discovered_capability.go`).
- Probe `Reason` strings are **NOT literal-free**: `probes.go:40,78,86` interpolate `err.Error()` (a driver error can echo statement text/identifiers/hints from the monitored DB) and `%q raw` (raw = `SHOW server_version_num` output).

So this is a **prerequisite feature, not a reconcile convenience**, with these parts:

1. **A new T1 wire message** (new proto message or new `Snapshot` field) carrying `{capability (closed vocab), available (bool), reason_code (closed enum), observed_at}`. It MUST pass the proto T1 contract test. **`reason_code` is a closed enum, not free text** (privacy-adr blocker): the free-text `err.Error()`/`%q raw` reasons are stripped/mapped to codes (e.g. `PROBE_ERROR`, `NOT_INSTALLED`, `PARSE_ERROR`, `VERSION_BELOW_BASELINE`) **at the collector before egress**.
2. **Collector wiring** of `caps.NewDiscoverer`/`Discover` into the main loop.
3. **An api_server ingest handler** (the first non-test caller of `UpsertDiscoveredCapabilities`).
4. **A new contract test on the discovery report payload** asserting every field is a closed-vocab capability, a boolean, a timestamp, or a reason CODE — the JSON/wire boundary the existing proto T1 test does not cover.

The operator's `DiscoveryController` then only *reads* the resulting `discovered_capability` via api_server; it never touches the collector and never copies free-text reasons into etcd (it surfaces Discovered/Final in the UI, per §2.3).

### 4.7 Tunables expansion (FUTURE, separate CRD — not a reserved field)

When designed end-to-end: a separate `CollectorConfig`-style CRD (or subresource) → a `config_policy` row set mirroring `capability_policy` scope/precedence → a new `GET /api/servers/{id}/config-snapshot` → collector reads it on the same ticker, replacing env-var defaults. **Out of scope for v1alpha1.** Not reserved as an empty nested field (k8s-conventions: an unused struct field in a storage version is a compatibility liability).

---

## 5. ONBOARDING (keep collector zero-touch)

**Goal:** a collector deployed with only an agent/license key + bootstrap (`server_id` or registration token, `LYNCEUS_INGESTION_URL`, `LYNCEUS_API_BASE_URL`) results in: stream discovered → entity appears in the UI/config DB → admin opts capabilities in via a scoped `CapabilityPolicy`. No collector config files, ever.

**Flow A — GitOps-declared (pre-seed, "policy before first scrape").**
1. Author `PostgresCluster`/`PostgresInstance` + a `CollectorRegistration`.
2. Operator mints `server_id`, seeds `servers` + `cluster`/`instance` + default `capability_policy` rows via the audited admin API **before** any collector connects (the insertion point the grounding identifies; today `BackfillFleet` only runs AFTER first data).
3. Operator renders a bootstrap ConfigMap (`LYNCEUS_SERVER_ID`, URLs) + references the token Secret; the collector Deployment consumes them as env (existing contract).
4. Collector connects, pulls the already-populated snapshot, gates correctly on first scrape.

**Flow B — discovery-first (auto-onboard, fleet-scale default).**
1. Collector deployed with a **registration token** (not a pre-assigned `server_id`) + URLs.
2. api_server issues `server_id` on first authenticated connect (token→server_id exchange — greenfield; today only a dev `LYNCEUS_DEV_TOKEN` stub on ingestion, `cmd/ingestion/main.go:30`), creating the `servers` row. **`BackfillFleet` is disabled when the operator owns entity creation** (§2.1) to avoid the dual-writer race; api_server stamps the stream's cluster/instance.
3. Collector reports `current_database()` + discovery (§4.6).
4. The stream appears in the capability matrix UI / fleetview (no CR is created — `PostgresDatabase` is not a CRD).
5. **Opt-in:** an existing scoped `CapabilityPolicy` whose selector matches the stream's labels applies automatically; otherwise defaults apply (recommend a fleet-default policy disabling sensitive caps — the §8 `fleet-baseline` posture).

**Token/bootstrap (zero-touch preserved).** The registration-token→`server_id` exchange + a real collector-token issuance API are greenfield (only a dev stub exists) and are a **prerequisite bead for Flow B**. Flow A works on today's `LYNCEUS_SERVER_ID` env contract immediately. *(Open Q resolved: v1alpha1 ships Flow A; Flow B follows the token-issuance bead.)*

---

## 6. RBAC + T2 + AUDIT

### 6.0 Current reality (verified)

There is **no T2 read path in the code.** All ingest writes are `DataTier: 1`; no code writes `data_tier=2`; there is no RBAC/grantee-group table; `servers.t2_enabled` is a scanned boolean (`fleet.go:36`) nobody gates a read on. The draft's "preserve the existing audited T2 gateway" was aspirational. This section therefore designs the gateway as a **prerequisite**, and hard-sequences the grant CRD behind it.

### 6.1 PREREQUISITE: the api_server T2 read gateway (BLOCKS T2AccessGrant)

A prerequisite bead must deliver, BEFORE any grant on-switch ships:
1. A T2 read handler that checks `servers.t2_enabled`.
2. An OIDC/SCIM **group-RBAC check** against the config-DB `t2_grant` grantee groups.
3. A **per-read** `AppendAuditReturning` with `data_tier=2` that **fails CLOSED** if the audit write fails (no literal returned).
4. **Acceptance test:** with `t2_enabled=true` but the read gateway absent/misconfigured, NO T2 literal is served.

### 6.2 T2AccessGrant semantics — the grant, not the read

A grant **declares** that tier T2 is enabled for a scope and readable by certain groups. The operator reconciles it into: `servers.t2_enabled=true` for matched streams + a `t2_grant` row + an audited `t2_grant.set` (batched, §4.3 treatment, so a wide grant cannot starve per-read T2 audits). This is declarative authorization state, k8s-audited at apply.

**Resolution of the t2_enabled-vs-t2_grant open question (privacy-adr + code-fidelity):** `t2_grant` (greenfield table: `server_id`, `grantee_groups`, `expires_at`, `audit_chain_id`) is the **RBAC source of truth** consulted by the read gateway; `servers.t2_enabled` stays the cheap per-stream boolean the read path checks FIRST (fast reject). Both are config-DB rows. They are not collapsed: the boolean is the hot-path gate, the grant carries the group/expiry RBAC.

### 6.3 api_server is the SOLE T2 gateway; per-read audit is preserved

> **The CRD models the GRANT. api_server models the READ + the read-audit. They never merge.**

- Every T2 literal read goes **only** through api_server (§6.1 steps 1-3), which resolves authorization from the config-DB `t2_grant`/`t2_enabled` rows **ONLY — never from etcd/CR status** (privacy-adr must-fix), so the check is transactional with the per-read audit.
- The operator/CRD path NEVER writes a per-read audit and NEVER serves T2 data. There is no k8s/etcd path to T2 literals.
- **CR-drift rule (resolves open Q6):** the `t2_grant` row is authoritative for the read path; the CR is desired-state input only. A human editing the CR cannot widen access except through the audited admin-API writer; an out-of-band etcd edit that is not reconciled has NO effect on the read gateway (which reads the DB). The operator owns `t2_grant`; on CR change it audited-reconciles, on CR delete the finalizer audited-revokes (§2.7).
- **`justification` is operator-authored free text** (privacy-adr major): it is admin-authored grant metadata, NOT monitored-DB data and NOT a per-read record. It is bounded (`MaxLength: 512`), lands in the Postgres audit chain, AND is duplicated in etcd + the k8s API-server audit log. This is acceptable because it is admin intent, not customer-DB literals — but it is stated explicitly here rather than implying the chain stays purely package-authored.

### 6.4 k8s RBAC (who-edits-what)

- **Namespace = org/tenant boundary** (self-hosted single-org = one namespace, e.g. `lynceus`).
- `lynceus-fleet-admin` (namespaced): full CRUD on the four namespaced CRDs; sole author of `T2AccessGrant` (so "who can grant T2" is k8s-audited at apply, independent of the data-plane audit). `CollectorRegistration` (cluster-scoped) is granted via a separate ClusterRole to this principal only.
- `lynceus-db-operator` (namespaced): CRUD on database-scoped `CapabilityPolicy`; read-only on `T2AccessGrant`.
- `lynceus-viewer`: read-only on all CRs.
- `lynceus-operator` (controller SA): CRD status subresources + the internal admin API; NO direct config-DB superuser.

### 6.5 Classification note (privacy-adr minor)

`datname`/`database_name` (in `capability_policy.database_name`, the discovery report's `current_database`) is an operator/config IDENTIFIER (consistent with `capabilities.go` treatment), deliberately classified as config (T1-adjacent) and allowed in config-DB rows. Only monitored-DB row/column/query VALUES are the literals barred from crossing the boundary — and those are what §4.6's reason-code change removes.

---

## 7. SaaS-MULTI-ORG-LATER (honest prerequisites, not a thin adapter)

### The constraint
SaaS customers get no kubectl into the vendor cluster, so customer-facing CRDs are impossible; the policy-first reconcile logic must be reused, not reimplemented.

### What is actually reusable vs unbuilt (saas-portability must-fixes)

**Reusable (the easy part):** `internal/policyresolve.Flatten` — a pure function over inputs already filtered to a tenant. The operator (CRD adapter) and a future api_server SaaS adapter (HTTP) both call it; one precedence brain.

**Unbuilt prerequisites (the hard part — explicitly NOT thin):**
1. **`cluster.org_id` does not exist** — it is a comment (`0005_fleet.sql:8`); there is zero org_id code. SaaS requires a migration adding `org_id` to `cluster` (and a documented tenancy join path down to `servers`/`capability_policy`/`audit_log`), plus wiring `CreateCluster` and **tenant-scoping every Config store method** (all currently org-blind: `CreateCluster`, `ListClusters`, `ResolveServer`, `ListCapabilityPolicies`, `SetCapabilityPolicy`). This is a **hard blocker on the SaaS path**, marked as such.
2. **`policyresolve.Flatten` cannot enforce isolation.** In self-hosted, namespace=org + k8s RBAC + the operator listing only its namespace gives isolation for free. In SaaS there is no namespace; the security boundary must be a **shared, tested tenant-scoped data-access layer** (an org-scoped Config wrapper) that BOTH the operator admin-API path and the SaaS HTTP path call — so isolation is enforced ONCE, not duplicated. Today api_server has **no authz at all** (`server.go` DevAuth-or-401 stub; `/api/servers/{id}/*` unscoped), so this layer is greenfield. Test obligation: org A cannot read/flatten/grant against org B's servers.
3. **Identity minting must be transport-neutral.** `server_id` minting (greenfield — no production `INSERT INTO servers` exists; only test files) is the seam where org/tenant assignment must happen. It is an **api_server responsibility shared by both deploys**, taking an optional org scope from day one; `CollectorRegistration` is a thin k8s wrapper calling it, NOT a parallel minter. `bootstrapConfigMapRef` is a k8s-only convenience over a transport-neutral identity API.

### Diagram

```
                       ┌──────────────────────────────────────┐
self-hosted: CRDs ───▶ │ operator adapter (CR → resolve inputs)│──▶┐
                       └──────────────────────────────────────┘   │
                                                                   ├─▶ org-scoped Config (tenant isolation, SHARED)
                       ┌──────────────────────────────────────┐   │        │
SaaS-later: HTTP  ───▶ │ api_server SaaS adapter (JSON→inputs, │──▶┘        ▼
                       │ org-scoped via cluster.org_id)        │   policyresolve.Flatten (pure, SHARED)
                       └──────────────────────────────────────┘            │
                                                                            ▼
                                          ApplyCapabilityPolicies (audited batch) ─▶ /policy-snapshot
```

api_server is the constant for the *data plane*; the operator is an optional k8s front-end. The entity-creation ownership is unified (§2.1) so the model is identical across deploys.

### Non-k8s self-hosted (saas-portability major)

**Decision:** v1alpha1 is **k8s-deploy-required for fleet-scale authoring.** The only non-CRD writer today is `handleCapabilityToggle` (`capabilities.go:98`), a single-row setter with no scope/selector/precedence — it cannot express ly-4ov policy. We own this limitation explicitly. The path to non-k8s parity is the same as the SaaS path: build the scoped-policy CRUD into api_server (the HTTP adapter above). **Recommendation:** build that api_server adapter in the SAME milestone as the operator (M-crd.4) rather than deferring it — it de-risks the SaaS claim by forcing the shared layer to exist on day one, and gives non-k8s self-hosted a path. (See §11 milestones.)

---

## 8. Example CR YAMLs

```yaml
apiVersion: lynceus.dev/v1alpha1
kind: PostgresCluster
metadata:
  name: payments-prod
  namespace: lynceus
  labels: { env: prod, team: payments }
spec:
  displayName: "Payments (prod)"
status:
  clusterID: 7f3a-...                 # = cluster.id (operator-written only)
  instanceCount: 2
  conditions:
    - type: Ready
      status: "True"
      reason: Reconciled
      observedGeneration: 3
      lastTransitionTime: "2026-06-19T10:00:00Z"
---
apiVersion: lynceus.dev/v1alpha1
kind: PostgresInstance
metadata:
  name: payments-prod-primary
  namespace: lynceus
  labels: { env: prod, team: payments, role: primary }
  # ownerReferences set by operator → PostgresCluster payments-prod
spec:
  clusterRef: { name: payments-prod }   # typed, same-namespace
  displayName: "primary"
status:
  instanceID: a91c-...
  discoveredRole: primary
  streamCount: 2                         # count, NOT a serverIDs[] list
  conditions: [{ type: Ready, status: "True", reason: Reconciled, observedGeneration: 1, lastTransitionTime: "..." }]
---
apiVersion: lynceus.dev/v1alpha1
kind: CapabilityPolicy                   # fleet-default: privacy-forward posture
metadata: { name: fleet-baseline, namespace: lynceus }
spec:
  scope: fleet
  priority: 0
  capabilities:
    - { capability: pg_stat_activity_full_read, enabled: false, reason: "T2-adjacent; opt-in only" }
    - { capability: auto_explain,               enabled: false, reason: "opt-in per group" }
status:
  matchedCount: 140                      # count, NOT matchedServerIDs[]
  appliedRows: 280
  lastAppliedAuditChainID: 90211         # ONE chain entry for the whole batch
  conditions: [{ type: Ready, status: "True", reason: Applied, observedGeneration: 1, lastTransitionTime: "..." }]
---
apiVersion: lynceus.dev/v1alpha1
kind: CapabilityPolicy                   # group override: prod gets auto_explain on
metadata: { name: prod-explain, namespace: lynceus }
spec:
  scope: clusterSelector
  selector: { matchLabels: { env: prod } }   # required because scope != fleet (CEL)
  priority: 10
  capabilities:
    - { capability: auto_explain, enabled: true, reason: "prod EXPLAIN insights" }
status:
  matchedCount: 2
  appliedRows: 2
  conditions: [{ type: Ready, status: "True", reason: Applied, observedGeneration: 2, lastTransitionTime: "..." }]
---
apiVersion: lynceus.dev/v1alpha1
kind: T2AccessGrant                      # GATED on §6.1 read gateway; before it, status=Stalled/GatewayMissing
metadata: { name: payments-t2-oncall, namespace: lynceus }
spec:
  scope: clusterSelector
  selector: { matchLabels: { env: prod, team: payments } }
  tier: T2
  granteeGroups: [ "payments-oncall", "dba-leads" ]
  expiresAt: "2026-09-30T00:00:00Z"
  justification: "Incident INC-4821 deep-dive window"   # operator-authored free text, bounded 512 chars
status:
  grantedCount: 1                        # count, NOT grantedServerIDs[]
  lastAppliedAuditChainID: 90240
  conditions: [{ type: Ready, status: "True", reason: Granted, observedGeneration: 1, lastTransitionTime: "..." }]
```

---

## 9. kubebuilder-style Go API types sketch

```go
// api/v1alpha1/types.go   (package v1alpha1; group lynceus.dev)
import (
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    corev1 "k8s.io/api/core/v1"
)

type Scope string
const (
    ScopeFleet            Scope = "fleet"
    ScopeClusterSelector  Scope = "clusterSelector"
    ScopeInstanceSelector Scope = "instanceSelector"
    ScopeDatabaseSelector Scope = "databaseSelector"
)

// ---- PostgresCluster ----
type PostgresClusterSpec struct {
    DisplayName string `json:"displayName"`
    // +optional  -- SaaS only; INERT in v1alpha1 (cluster.org_id does not exist yet, §7)
    OrgRef string `json:"orgRef,omitempty"`
}
type PostgresClusterStatus struct {
    ClusterID     string             `json:"clusterID,omitempty"` // = cluster.id; operator-written ONLY
    InstanceCount int                `json:"instanceCount,omitempty"`
    Conditions    []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// ---- PostgresInstance ----
type PostgresInstanceSpec struct {
    ClusterRef  corev1.LocalObjectReference `json:"clusterRef"` // same-namespace, typed
    DisplayName string                      `json:"displayName"`
    // +optional  human pin; immutable once set via CEL self==oldSelf
    Role string `json:"role,omitempty"`
}
type PostgresInstanceStatus struct {
    InstanceID     string             `json:"instanceID,omitempty"` // operator-written ONLY
    DiscoveredRole string             `json:"discoveredRole,omitempty"`
    StreamCount    int                `json:"streamCount,omitempty"` // count, not a list (scale)
    Conditions     []metav1.Condition `json:"conditions,omitempty"`
}

// ---- CapabilityPolicy (Policy axis, scoped) ----
type CapDecl struct {
    Capability string `json:"capability"` // validated vs caps.Declared() (webhook §9)
    Enabled    bool   `json:"enabled"`
    Reason     string `json:"reason,omitempty"`
}
// +kubebuilder:validation:XValidation:rule="self.scope=='fleet' || has(self.selector)",message="selector required unless scope is fleet"
type CapabilityPolicySpec struct {
    // +kubebuilder:validation:Enum=fleet;clusterSelector;instanceSelector;databaseSelector
    Scope    Scope                 `json:"scope"`
    Selector *metav1.LabelSelector `json:"selector,omitempty"`
    // +kubebuilder:default=0
    Priority int `json:"priority,omitempty"`
    // +kubebuilder:validation:MaxItems=13
    // +kubebuilder:validation:MinItems=1
    Capabilities []CapDecl `json:"capabilities"`
    // NOTE: NO Tunables field in v1alpha1 (§4.7) — added as a separate CRD when implemented.
}
type CapabilityPolicyStatus struct {
    MatchedCount            int                `json:"matchedCount,omitempty"`  // count, not matchedServerIDs[]
    AppliedRows             int                `json:"appliedRows,omitempty"`
    LastAppliedAuditChainID int64              `json:"lastAppliedAuditChainID,omitempty"`
    Conditions              []metav1.Condition `json:"conditions,omitempty"` // Ready; Stalled(reason=Conflict|AdminAPIError)
}

// ---- T2AccessGrant (the grant, NOT the read-audit; GATED §6.1) ----
type T2AccessGrantSpec struct {
    // +kubebuilder:validation:Enum=fleet;clusterSelector;instanceSelector;databaseSelector
    Scope    Scope                 `json:"scope"`
    Selector *metav1.LabelSelector `json:"selector,omitempty"`
    // +kubebuilder:validation:Enum=T2
    Tier string `json:"tier"`
    // +kubebuilder:validation:MinItems=1
    GranteeGroups []string `json:"granteeGroups"`
    ExpiresAt     *metav1.Time `json:"expiresAt,omitempty"`
    // +kubebuilder:validation:MaxLength=512  operator-authored free text (§6.3)
    Justification string `json:"justification"`
}
type T2AccessGrantStatus struct {
    GrantedCount            int                `json:"grantedCount,omitempty"` // count, not a list (scale)
    LastAppliedAuditChainID int64              `json:"lastAppliedAuditChainID,omitempty"`
    Conditions              []metav1.Condition `json:"conditions,omitempty"`
}
```

```go
// internal/policyresolve/flatten.go  — framework-free reuse brain (§3, §7). NO k8s imports.
type ScopedPolicy struct {
    Tier     Scope      // fleet|cluster|instance|database
    Match    MatchSet   // resolved server_ids, k8s-free
    Priority int
    Caps     []CapDecl
}
type EntityGraph struct {
    // server_id -> {clusterID, instanceID, currentDatabase, labels}
    // (currentDatabase is the gate key — see corrected entity semantics §2.3)
}
type DesiredRow struct { // == one capability_policy upsert input
    ServerID, DatabaseName, Capability string // DatabaseName=="" => server-wide default (NULL)
    Enabled bool
}
type Conflict struct{ ServerID, Capability, PolicyA, PolicyB string }

// Flatten resolves precedence (tier > priority > conflict) and collapses to the
// two storable levels, emitting per-db overrides ONLY where database_name == the
// stream's current_database (matches the resolver, §3).
func Flatten(policies []ScopedPolicy, g EntityGraph) (rows []DesiredRow, conflicts []Conflict)
```

```go
// internal/store — NEW prerequisite functions (none exist today):
// 1) bulk read (pattern from rollup.go:32 / insights.go:75)
func (c *Config) ListCapabilityPoliciesForServers(ctx, serverIDs []string) ([]CapabilityPolicy, error)
// 2) audited batch apply — ONE AppendAuditReturning + ONE multi-row UPSERT/DELETE under ONE auditLockKey tx
func (c *Config) ApplyCapabilityPoliciesBatch(ctx, in BatchApplyInput) (auditChainID int64, err error)
// 3) audited delete (SetCapabilityPolicy has no delete sibling today)
//    folded into ApplyCapabilityPoliciesBatch's Deletes set.
```

---

## 10. Test strategy

- **`internal/policyresolve` unit tests (pure, no infra).** Precedence table tests across all four tiers + priority ties; conflict detection (equal tier+priority+disagreement ⇒ Conflict, prior row untouched); the **current_database flatten rule** (database-tier override emitted only for the matching stream; a `databaseSelector` naming a non-`current_database` name yields a warning + no row); fleet/group/instance ⇒ NULL default. This is the load-bearing logic and is fully testable without k8s or Postgres.
- **Store integration tests (testcontainers, real Postgres — never mocked).** `ApplyCapabilityPoliciesBatch` writes ONE audit row for N policy rows under ONE advisory lock; `VerifyChain` stays intact after a batch; `ListCapabilityPoliciesForServers` matches per-server reads; audited delete removes rows + appends an audit entry; **assert no operator-reachable mutation of `capability_policy`/`t2_grant`/`servers.t2_enabled` lacks a matching `audit_log` append** (INVARIANT 2).
- **Snapshot equivalence test.** After a CapabilityPolicy reconcile, `GET /policy-snapshot` for each stream returns exactly what `FetchPolicySnapshot` would resolve — proving INVARIANT 1 (frozen wire shape).
- **Discovery report contract test (NEW boundary, §4.6).** Mirror the proto T1 contract test's spirit over the discovery payload: every field ∈ {closed-vocab capability, bool, timestamp, reason CODE}; a fuzz/property test asserts no `err.Error()`/free-text path can populate a reason. Plus a collector-side test that probe errors map to codes before egress.
- **T2 gateway acceptance test (§6.1, prerequisite).** With `t2_enabled=true` and the gateway absent/misconfigured, NO literal served; with a valid grant, a read writes a `data_tier=2` audit row and fails closed if the audit append fails.
- **envtest controller tests** (`sigs.k8s.io/controller-runtime/pkg/envtest`, real apiserver + etcd, fake admin API):
  - CapabilityPolicy create/update/delete → expected `ApplyCapabilityPolicies` batch calls; finalizer audited-deletes contributed rows on CR deletion.
  - T2AccessGrant deletion finalizer audited-revokes `t2_enabled` + deletes `t2_grant` (no standing access).
  - CEL validation: scope enum, selector-required-unless-fleet, tier enum, MaxItems — rejected at the apiserver.
  - Status writes use `metav1.Condition` with `observedGeneration`; a status-only update does NOT re-trigger reconcile (GenerationChangedPredicate).
  - Scale guard: a fleet relabel coalesces into ONE reconcile within the debounce window; reconcile makes ONE bulk read + ONE batch apply (assert call counts), not O(N).
- **Admission webhook test.** Unknown capability string rejected at apply (mirrors `isDeclaredCapability`); admin API rejects as backstop.

---

## 11. Milestones / task breakdown (sequenced)

> Beads to be filed under the operator epic. Each is plan→impl→test per the M2-M6 lifecycle.

**M-crd.0 — Prerequisites (BLOCK the operator).**
1. `internal/policyresolve` pure package + exhaustive precedence/flatten tests (incl. current_database rule).
2. Store: `ListCapabilityPoliciesForServers` (bulk `= ANY($1)`).
3. Store: `ApplyCapabilityPoliciesBatch` (ONE audit entry + multi-row UPSERT/DELETE under ONE `auditLockKey` tx) + audited delete; INVARIANT-2 test.
4. api_server internal admin API (`ApplyCapabilityPolicies`, `ListCapabilityPoliciesForServers`) with real `SetBy` actor; mTLS/SA-token auth.

**M-crd.1 — Read-only CRDs + entity reconcile.**
5. CRD scaffolding (kubebuilder), `metav1.Condition`, CEL validation, finalizers, owner refs.
6. ClusterController + InstanceController (reconcile-by-reference; disable `BackfillFleet` when operator deployed).

**M-crd.2 — CapabilityPolicy (the load-bearing controller).**
7. CapabilityPolicyController: Flatten → batch apply; debounce; backoff; generation/graph short-circuit; status counts + conditions.
8. Admission webhook for `caps.Declared()` (extract the Declared() list into a dependency-free subpackage so the webhook doesn't import the pgx probe code — code-fidelity minor).
9. Snapshot-equivalence + envtest controller tests.

**M-crd.3 — Discovery write-back (new wire boundary).**
10. New T1 discovery message + proto/contract test; reason CODES (strip `err.Error()`/`%q raw` at collector).
11. Collector wiring of `caps.Discover`; api_server ingest handler (first non-test `UpsertDiscoveredCapabilities` caller).
12. DiscoveryController (poll feed; surface Discovered/Final in UI; NO collector contact, NO etcd telemetry).

**M-crd.4 — Onboarding + shared authoring layer (de-risks SaaS).**
13. Transport-neutral `server_id` minting API (optional org scope from day one) + collector-token issuance (unblocks Flow B).
14. `CollectorRegistration` (cluster-scoped) → Flow A pre-seed + bootstrap ConfigMap.
15. **api_server scoped-policy HTTP authoring adapter** in front of the shared `ApplyCapabilityPolicies` service (gives non-k8s self-hosted a path + forces the shared layer to exist).

**M-crd.5 — T2 (GATED).**
16. **Prerequisite (separate bead, BLOCKS 17):** api_server T2 read gateway — `t2_enabled` check + group RBAC + per-read `data_tier=2` audit failing closed + acceptance test (§6.1).
17. `t2_grant` table + T2GrantController (batched audited apply; finalizer audited-revoke) — ships only after 16.

**M-crd.X — SaaS (future, explicitly blocked).**
18. Tenancy migration: `cluster.org_id` + tenant-scoping every Config store method + shared org-scoped data-access layer + org-isolation test. (Hard prerequisite for any SaaS adapter; NOT in the self-hosted milestones.)

---

## 12. Open questions (genuinely unresolved; the rest were decided in-body)

1. **Admin API transport details** — mTLS vs SA-token-only for the cluster-internal admin Service; and whether `ApplyCapabilityPolicies` should be a single bulk RPC or a small streaming protocol for very large fleets (>1k streams beyond the current 140-210 target). The batch-under-one-lock decision (§4.3) stands; this is a transport-tuning question.
2. **Per-reconcile write-budget threshold** — the chunk-and-yield size for a single giant apply (§4.3 fix #4). Needs a load test against a 200-stream fleet to pick a value that keeps per-read T2 audit latency bounded once §6 lands; placeholder ~500 rows/chunk.
3. **`status.matchedCount`/`grantedCount` freshness** — counts are cheap but still re-derive on resync; confirm a count is enough for GitOps health, or whether a separate read-only "match preview" API/printer-column is needed (§2.4). Leaning: count + a `kubectl get` printer column, no per-id list.
4. **Discovery feed transport** — the operator's `DiscoveryController` polls api_server's discovery feed; confirm poll interval + whether a watch/SSE is worth it vs a 1-2m poll at fleet scale. (The collector→api_server discovery *report* transport is decided in §4.6: a new T1 message on the existing channel with reason codes + a contract test.)
5. **`role` pin immutability ergonomics** — CEL `self == oldSelf` makes `PostgresInstance.spec.role` immutable once set; confirm operators are OK re-creating the CR to change a mis-pinned role, or whether a deliberate clear-then-set is needed.

---

## 13. Critique disposition (every must-fix accounted for)

| Lens / must-fix | Disposition |
|---|---|
| **k8s:** minted server_id out of spec | **Fixed** — §2.3 (PostgresDatabase demoted; no spec.serverID anywhere); §2.1-2.2 status-only minting; §2.2 role pin immutable via CEL |
| **k8s:** finalizers + external cleanup | **Fixed** — §2.7 (cleanup finalizers; T2 audited-revoke; CapabilityPolicy audited-delete) |
| **k8s:** unbounded status lists | **Fixed** — §2.0/§2.4/§2.5 counts + conditions; per-id lists removed |
| **k8s:** metav1.Condition | **Fixed** — §2.7, §9 (verbatim, observedGeneration/lastTransitionTime) |
| **k8s:** typed refs + owner refs | **Fixed** — §2.2, §9 (LocalObjectReference, ownerReferences) |
| **k8s:** CEL validation + webhook | **Fixed** — §2.4, §9 (enum/CEL/MaxItems markers; webhook only for caps.Declared()) |
| **k8s:** versioning / tunables reservation | **Fixed** — §2.7, §4.7 (v1alpha1 storage version; tunables = future separate CRD, not reserved field) |
| **k8s:** namespaced-vs-cluster scope | **Fixed** — §2.6 (CollectorRegistration cluster-scoped; rest namespaced; decided, not "or") |
| **code-fidelity:** server_id == connection, not database | **Fixed (blocker)** — §2.3, §3 flatten tied to single current_database resolver |
| **code-fidelity:** cluster.org_id is a comment | **Fixed (blocker)** — §0, §7 (unbuilt prerequisite + migration) |
| **code-fidelity:** discovery write-back is new wire | **Fixed (blocker)** — §4.6 (new T1 message + contract test + collector wiring) |
| **code-fidelity:** flatten ↔ resolver | **Fixed (blocker)** — §3 (NULL default for group/instance; override only for matching current_database) |
| **code-fidelity:** dev-admin actor stub | **Rebutted+noted** — §1 force 2 (admin API sets SetBy directly; coverage/ordering preserved; M5 caveat stated) |
| **code-fidelity:** no T2 read gate exists | **Fixed** — §6.0-6.1 (gateway is a prerequisite bead) |
| **code-fidelity:** webhook pulls pgx probes | **Fixed** — M-crd.2 task 8 (extract Declared() to dep-free subpackage) |
| **scale:** per-row audit-lock storm | **Fixed (blocker)** — §4.3 batch (one audit entry + multi-row UPSERT under one lock) |
| **scale:** O(N) reconcile reads + 5m sweep | **Fixed** — §4.3 (bulk `= ANY($1)`; 30m drift resync; generation/graph short-circuit) |
| **scale:** volatile telemetry in etcd | **Fixed** — §2.3 (PostgresDatabase demoted; telemetry in DB/UI only) |
| **scale:** PostgresDatabase as CRD | **Fixed** — §2.3 (not a CRD) |
| **scale:** rate-limit posture | **Fixed** — §4.3 (debounce, backoff+jitter, write budget, idempotent resumable) |
| **scale:** T2 grant fan-out on audit lock | **Fixed** — §6.2 (same batch treatment + chunk-and-yield) |
| **privacy:** T2 grant before read gateway | **Fixed (blocker)** — §2.5, §6.1 (hard-sequenced; Stalled until gateway) |
| **privacy:** raw Reason into etcd/wire | **Fixed (blocker)** — §4.6 (reason CODES + contract test) |
| **privacy:** operator single audited writer | **Fixed** — INVARIANT 2 (promoted from open Q, with test) |
| **privacy:** read gateway reads DB only | **Fixed** — §6.3 (authoritative t2_grant/t2_enabled rows; never etcd) |
| **privacy:** discovery channel contract test | **Fixed** — §4.6, §10 |
| **privacy:** justification free text | **Acknowledged** — §6.3 (bounded; admin-authored; duplicated in etcd/k8s-audit — stated) |
| **privacy:** datname classification | **Noted** — §6.5 |
| **saas:** org_id unbuilt | **Fixed** — §7 (hard prerequisite + migration; M-crd.X) |
| **saas:** security boundary shared, not pure-fn | **Fixed** — §7 (shared org-scoped data-access layer; isolation test) |
| **saas:** entity-creation ownership unified | **Fixed** — §2.1 (reconcile-by-reference; BackfillFleet disabled under operator) |
| **saas:** non-k8s authoring | **Fixed** — §7 (k8s-required owned; api_server adapter in M-crd.4) |
| **saas:** single shared audited write path | **Fixed** — §4.1 (one in-process service; admin API + SaaS HTTP are thin transports) |
| **saas:** transport-neutral identity minting | **Fixed** — §7, M-crd.4 task 13 |
