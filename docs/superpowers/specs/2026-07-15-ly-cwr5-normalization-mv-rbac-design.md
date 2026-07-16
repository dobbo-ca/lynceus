# Design: normalization materialized view + ClickHouse RBAC — the T1/T2 boundary (ly-cwr.5)

- **Status:** Proposed (decision-ready)
- **Date:** 2026-07-15
- **Bead:** ly-cwr.5 (epic ly-cwr)
- **Implements:** ADR #4 (`docs/research/2026-07-14-clickhouse-bedrock-architecture.md` §2.2/§4.1/§6),
  **reconciled** to the tenant reality established by ly-cwr.6
  (`docs/superpowers/specs/2026-07-15-ly-cwr6-t2-raw-isolation-design.md`). The ADR predates that
  override; §2 below records the reconciliation.
- **Empirically grounded:** the load-bearing ClickHouse behaviours were spiked on ClickHouse
  25.8.28.1 before this design was written (§9). This mirrors the ly-cwr.6 method, which caught a
  wrong RLS assumption before implementation.

---

## 1. Problem

The ADR moves normalization "off the collector edge" into a ClickHouse **materialized view**: the raw
table is T2 (literal-bearing), the MV's normalized target is T1 (broadly readable), and a *materialized*
(not plain) view is required so the T1 target can be granted while raw is denied. Two facts make the
ADR's wording unbuildable as written and force a reconciliation:

1. **ClickHouse cannot reproduce the collector's normalization.** The collector normalizes with
   `github.com/pganalyze/pg_query_go/v6` — a real Postgres grammar parser (`normalize.Normalize` +
   `normalize.Fingerprint`, `internal/collector/reader.go:71-85`). ClickHouse's native
   `normalizeQuery`/`normalizedQueryHash` strip literals but are **lossy on Postgres-specific syntax**
   and produce **non-pg_query fingerprints** (§9). A CH MV therefore cannot be the authoritative
   normalizer without breaking fingerprint parity with the rest of the system (`query_plans`,
   `insights`, detectors, advisors all key on pg_query fingerprints).

2. **Lynceus is a tenant, not the owner, of ClickHouse (ly-cwr.6).** The ADR's "grant the T1 target,
   deny raw to analysis/Bedrock roles" assumes Lynceus provisions those roles. It does not — Grafana /
   analyst / Bedrock RBAC is **org-owned, out of scope**. The boundary Lynceus controls is the ly-cwr.6
   **row-level security** on the T2 table (only the runtime USER can read its rows) + the audited
   `T2Reader` gateway + the MV.

Additionally, **nothing produces T2 in production today**: `internal/ingest/server.go:221` hardcodes
`DataTier: 1`, so every row lands in `query_stats` (T1). `query_stats_t2`, the ly-cwr.6 RLS policy, and
the `T2Reader` gateway are latent machinery with no producer. This design gives them one.

## 2. Decision (locked) — and the reconciliation

| ADR §2.2/§4.1 as written | Reconciled decision (this spec) |
|---|---|
| Edge normalization dropped; normalization = CH MV | **Edge keeps pg_query** as the authoritative fingerprint + normalized_query (parity with plans/insights preserved). The CH MV does **literal-free projection**, not normalization. `normalizeQuery` is retained as a **CH-native guardrail / where-necessary fallback**, not the normalizer (§4.3). |
| MV grants T1 target / denies raw to analysis/Bedrock roles | Those roles are **org-owned, out of Lynceus scope**. The enforced boundary is the ly-cwr.6 **RLS** on `query_stats_t2` (unchanged) + **no policy on `query_stats`** + the MV projection + the audited `T2Reader`. |
| raw table = T2; MV target = T1 | Kept. The literal lives in a **new `raw_query` column** on `query_stats_t2`; the MV projects the literal-free columns into `query_stats` (T1), **excluding `raw_query`**. |

**Raw egress is gated per server (user-confirmed).** Raw literals leave the collector **only** for a
server whose operator has enabled T2 (`servers.t2_enabled` ∧ capability policy). T2-disabled servers
are byte-for-byte unchanged (edge-normalized T1 only), so Lynceus's strong privacy claim still holds by
default. Enabling T2 for a server **is** the conscious per-server opt-in the ADR §7.4 rescoped-privacy
claim requires. (This scopes the §7.4 sign-off to a per-server toggle, not an unconditional fleet-wide
posture change.)

**Dual T1 write path (user-confirmed — "Model X").** `query_stats` (T1) has two writers, mutually
exclusive per server:
- **T2 disabled →** collector ships the T1 `QueryStat`; ingestion writes `query_stats` **directly**
  (today's path, unchanged).
- **T2 enabled →** collector ships the T2 `QueryStatRaw` **instead**; ingestion writes
  `query_stats_t2`; the **MV** derives the `query_stats` (T1) row. The direct T1 write is **replaced by
  the MV path** for these servers (the ADR acceptance criterion). Exactly one T1 row per stat.

## 3. Architecture & data flow

```
T2 DISABLED server (default — strong privacy claim intact, unchanged):
  collector: pg_stat_statements → normalize (pg_query) → ship QueryStat (T1, literal-free)
  ingestion: WriteQueryStats → query_stats (direct)                 [no raw leaves the edge]

T2 ENABLED server (operator opted in via servers.t2_enabled ∧ policy):
  collector: gate query_text_t2 = Allowed(db) via policy snapshot
             → ship QueryStatRaw (T2): raw_query (literal) + pg_query fingerprint
                                       + pg_query normalized_query + aggregate stats
             → does NOT also ship the plain T1 QueryStat (avoids a duplicate T1 row)
  ingestion: WriteQueryStats routes DataTier==2 → query_stats_t2   [log_queries=0, ly-cwr.6 scrub]
             (raw_query + edge pg_query columns + stats)
  ClickHouse MATERIALIZED VIEW  (query_stats_t2 → query_stats):
             SELECT server_id, collected_at, fingerprint, normalized_query,
                    1 AS data_tier, calls, total_time_ms, mean_time_ms,
                    `rows`, shared_blks_hit, shared_blks_read
             FROM query_stats_t2                     -- raw_query EXCLUDED → T1 literal-free

  T2 read (literals, audited):
    api_server → T2Reader.ReadT2QueryStats  (ordering UNCHANGED)
       → fast-reject servers.t2_enabled            (config PG)
       → EffectiveCapability                       (config PG)
       → AppendAuditReturning FIRST, fail-closed   (config PG hash-chain)
       → chStats.ReadQueryStatsTier2 → SELECT raw_query … FROM query_stats_t2   [log_queries=0]
```

Notes that fall out of the spikes (§9):
- The MV target is the **existing** `query_stats` MergeTree table (`CREATE MATERIALIZED VIEW … TO
  query_stats`). No second T1 table.
- ly-cwr.6's RLS policy `t2_lynceus_only ON query_stats_t2 USING (currentUser()='<USER>') TO ALL`
  filters the **MV transform** by the **inserting** identity. Ingestion inserts as the runtime **USER**
  (the sole runtime writer), which matches the policy, so the MV fires. A non-USER insert would land in
  `query_stats_t2` but be silently dropped from the MV — pinned by a test (§6.4) so the coupling is
  explicit and regression-proof.

## 4. Components (units of work)

Each unit has one purpose, a defined interface, and independent tests. Filed as child beads under
ly-cwr.5 (§8); tightly coupled through the shared wire contract + `query_stats_t2` shape, so they share
one spec.

### 4.1 Proto — new T2 message + Snapshot field
- New message `QueryStatRaw` in `proto/lynceus/v1/snapshot.proto`: `raw_query` (string, literal-bearing,
  **T2**), `fingerprint`, `normalized_query` (both pg_query, literal-free), `calls`, `total_time_ms`,
  `mean_time_ms`, `rows`, `shared_blks_hit`, `shared_blks_read`.
- New `repeated QueryStatRaw query_stat_raws = 15;` on `Snapshot` (next free field number).
- `make proto` regenerates Go.
- **Contract tests:** `QueryStat`'s T1 allowlist stays **untouched** (still literal-free). The
  `Snapshot` envelope allowlist (`TestSnapshotCarriesLogEvents`) is **deliberately widened** by exactly
  one field, `query_stat_raws`, with a comment marking it the opt-in T2 payload — the contract test is
  designed to force this to be a conscious, reviewed change. A new positive test documents that
  `QueryStatRaw` is the **one** message permitted a `raw_query` field.
- **Depends on:** nothing.

### 4.2 caps + api gate — surface `t2_enabled ∧ policy` to the collector
- New capability constant `caps.QueryTextT2 = "query_text_t2"` (`internal/caps/caps.go`, added to
  `Declared()`).
- `internal/api/capabilities.go` `handlePolicySnapshot`: emit `query_text_t2 = true` **iff**
  `servers.t2_enabled` (from `store.ServerStream.T2Enabled`, `internal/store/fleet.go`) **AND**
  `EffectiveCapability(serverID, db, "query_text_t2")` allows it. `t2_enabled` is the master kill
  switch; the capability policy provides RBAC granularity. Both true → gate on.
- **Interface:** policy snapshot JSON already carries `map[GateKey]bool`; this adds one key.
- **Depends on:** nothing (independent of 4.1).

### 4.3 Collector — ship raw when gated, else unchanged
- `internal/collector/reader.go`: after computing `normText`/`fp` (pg_query, unchanged), branch on
  `r.gate.Allowed(r.db, caps.QueryTextT2)`:
  - **gate on:** append a `QueryStatRaw{RawQuery: raw, Fingerprint: fp, NormalizedQuery: normText, …}`
    to a new `[]*QueryStatRaw` return, and **do not** emit the plain `QueryStat` for that row.
  - **gate off:** current behaviour (emit `QueryStat`).
  - `TierBlocked` (unparseable) rows are still **dropped** in both modes (parity preserved; shipping
    raw-only-unparseable rows is a documented follow-up, not built here).
- The shipper/pipeline carries the new slice into `Snapshot.query_stat_raws`.
- **Guardrail** ("CH normalize where necessary" / "as a guarantee"): the production MV is a pure
  literal-free **projection** (raw_query excluded), so T1's literal-freeness rests on (1) edge pg_query
  normalization and (2) column exclusion. CH `normalizeQuery` is used as a **test-time defense-in-depth
  assertion** (§6.3) — `normalizeQuery(normalized_query) == normalized_query`, i.e. no stray literal
  survived edge normalization — and stands as the designated CH-native normalizer for any **future raw
  source that lacks an edge-computed normalized form**. A *runtime* CH-normalize backstop column is
  deferred (YAGNI; avoids parity drift on already-`$1`-normalized text — see §7).
- **Depends on:** 4.1, 4.2.

### 4.4 Ingestion — route raw → query_stats_t2, suppress direct T1
- `internal/ingest/server.go`: add `snapshotToRawRows(snap)` mapping `snap.QueryStatRaws` →
  `[]store.QueryStat` with `DataTier: 2` and the new `RawQuery` field; feed them to
  `WriteQueryStats` (which already routes `DataTier==2` → `query_stats_t2`). For a T2-enabled server the
  snapshot carries `query_stat_raws` and **no** `query_stats`, so no direct T1 write occurs — the MV
  produces T1.
- **Depends on:** 4.1, 4.5 (needs the `RawQuery` column/field).

### 4.5 Store (ClickHouse) — raw column, MV, T2Reader retarget
- **Migration** `internal/store/migrations/clickhouse/0013_query_stats_raw_mv.sql`:
  ```sql
  ALTER TABLE query_stats_t2 ADD COLUMN IF NOT EXISTS raw_query String;

  CREATE MATERIALIZED VIEW IF NOT EXISTS mv_query_stats_t2_to_t1 TO query_stats AS
  SELECT server_id, collected_at, fingerprint, normalized_query, 1 AS data_tier,
         calls, total_time_ms, mean_time_ms, `rows`, shared_blks_hit, shared_blks_read
  FROM query_stats_t2;
  ```
  (`raw_query` is deliberately absent from the `SELECT` — the T1 projection is literal-free by
  construction. `rows` is backticked, per the existing schema.)
- `internal/store/stats.go`: add `RawQuery string` to `QueryStat` (documented T2-only).
- `internal/store/chstats.go`:
  - the `query_stats_t2` INSERT includes `raw_query`; the `query_stats` INSERT does **not** (T1 has no
    such column) — split the shared `chQueryStatsCols` into a T1 list and a T2 list.
  - `ReadQueryStatsTier2` returns `raw_query` (the literal the operator asked for), preserving the
    single `FROM query_stats_t2` choke point guarded by
    `TestT2Read_OnlyOneTier2SelectInStoreSource`.
- ly-cwr.6 provisioning (`chsecurity.go`), TTL, `log_queries=0` scrub, and the RLS policy are
  **unchanged**. Migration order in `OpenStats` (migrate → provision) is unaffected.
- **Depends on:** 4.1 (field names).

### 4.6 Dev / ops + docs
- `docs/reference/clickhouse-schema.md`: add `raw_query` to `query_stats_t2`; document the MV
  (`query_stats_t2 → query_stats`) and that T1 is MV-derived for T2-enabled servers.
- `README.md`: note the `query_text_t2` capability + per-server `t2_enabled` raw-egress gate.
- Update this ADR's memory pointers on close.
- **Depends on:** 4.1–4.5.

## 5. Privacy model (what enforces what)

| Threat | Control | Layer |
|---|---|---|
| Raw literal reaches broadly-readable T1 | MV `SELECT` **excludes `raw_query`** → T1 literal-free by construction; edge pg_query keeps `normalized_query` literal-free; CH `normalizeQuery` test-time backstop (§6.3) | ClickHouse MV + edge (new) |
| Raw leaves the edge without opt-in | Collector ships raw **only** when `query_text_t2` gate on (= `servers.t2_enabled` ∧ policy) — the §7.4 per-server conscious opt-in | Collector gate + api policy (new) |
| Another CH tenant reads raw literals | ly-cwr.6 RLS `t2_lynceus_only` on `query_stats_t2` → non-USER sees zero rows | ClickHouse RLS (unchanged) |
| Lynceus reads literals without audit | `T2Reader` gateway: audit-first, fail-closed; single `FROM query_stats_t2` | App (unchanged) |
| Literal custody window unbounded | ly-cwr.6 configurable TTL on `query_stats_t2` | CH TTL (unchanged) |
| Leaked USER credential | Accepted residual risk (ly-cwr.6) | — |

Out of scope (org-owned or deferred): Grafana / analyst / Bedrock roles and grants; column-level
security; the ADR §7.4 product/marketing/legal sign-off itself (this design gives the *mechanism* +
the per-server gate that makes the opt-in enforceable); shipping raw-only unparseable (`TierBlocked`)
rows.

## 6. Testing (TDD, real ClickHouse via `internal/testch`, real Postgres via `internal/testpg`)

Red-first. Shared-container helpers only — never per-test `tcpostgres.Run` / per-test CH boot.

1. **Proto contract** — `QueryStat` T1 allowlist unchanged (still fails on any new field);
   `Snapshot` allows exactly `query_stat_raws` added; `QueryStatRaw` is the sole message permitted
   `raw_query`.
2. **api gate** — `handlePolicySnapshot` emits `query_text_t2=true` **iff** `t2_enabled ∧
   EffectiveCapability`; false when either is off (all four combinations).
3. **MV literal-free + parity + guardrail** — insert a `query_stats_t2` row with a literal in
   `raw_query` and a pg_query `$1` `normalized_query`; assert the MV-derived `query_stats` row (a) has
   **no** `raw_query` reachable and **no** literal (grep), (b) carries the **exact** pg_query
   fingerprint + normalized_query (parity), (c) `normalizeQuery(normalized_query) == normalized_query`
   (defense-in-depth: no stray literal in T1).
4. **RLS × MV insert-identity (load-bearing)** — an insert to `query_stats_t2` **by the USER** (matches
   the ly-cwr.6 policy) **populates** `query_stats` via the MV; document/assert that an insert by a
   non-policy identity does not. Pins the §3 coupling and re-pins the spiked CH behaviour so a future
   CH version change surfaces immediately.
5. **Collector** — with the gate on, `reader.go` emits `QueryStatRaw` (raw + pg_query fields) and **not**
   `QueryStat`; with it off, emits `QueryStat` only; `TierBlocked` dropped in both.
6. **Ingestion** — a snapshot with `query_stat_raws` writes `query_stats_t2` (raw_query populated) and
   no direct `query_stats` row; the MV then yields the T1 row.
7. **T2Reader** — `ReadQueryStatsTier2` returns `raw_query`; the gateway ordering tests
   (`t2_read_test.go`) and `TestT2Read_OnlyOneTier2SelectInStoreSource` stay green.
8. **Existing T1 reads** — `TopQueriesByTotalTime` etc. unchanged against the MV-populated table.

## 7. Open questions / deferred

1. **Runtime CH-normalize backstop.** This design uses `normalizeQuery` as a **test-time** guardrail and
   keeps stored T1 `normalized_query` = the edge pg_query value verbatim (parity-exact). A *runtime*
   guardrail (MV stores `normalizeQuery(normalized_query)`) would strip a stray literal an edge bug
   might ship, at a small risk of altering already-`$1`-normalized text. Deferred as YAGNI; escalate if
   runtime enforcement is wanted. Spike evidence: `normalizeQuery` preserves `$1`/`$2` and `::casts`
   unchanged (§9), so the parity risk is low but nonzero.
2. **`TierBlocked` raw rows.** Unparseable queries are dropped even in T2 mode (no fingerprint → no
   parity). Shipping them raw-only is a follow-up if operators want full literal coverage.
3. **§7.4 rescoped-claim sign-off.** Out of scope here; this design makes the opt-in *enforceable*
   per server. Product/marketing/legal acceptance remains a separate gate before external messaging.

## 8. Child beads (filed under ly-cwr.5)

- **ly-cwr.5a** — proto `QueryStatRaw` + `Snapshot.query_stat_raws` + contract tests (4.1).
- **ly-cwr.5b** — caps `QueryTextT2` + api policy-snapshot gate (`t2_enabled ∧ policy`) (4.2).
- **ly-cwr.5c** — collector: ship `QueryStatRaw` when gated, suppress T1 (4.3).
- **ly-cwr.5d** — store: `raw_query` column + MV + `ReadQueryStatsTier2` retarget + `QueryStat.RawQuery`
  (4.5).
- **ly-cwr.5e** — ingestion: route `query_stat_raws` → `query_stats_t2`, suppress direct T1 (4.4).
- **ly-cwr.5f** — dev/ops + docs (4.6).

Sequence: 5a → (5b ∥ 5d) → 5c → 5e → 5f. 5b and 5d are independent of each other.

## 9. Spike evidence (ClickHouse 25.8.28.1, 2026-07-15)

The load-bearing behaviours, verified before this design (throwaway container, torn down):

- **`normalizeQuery` strips literals** (strings, numbers, IN-lists → `(?..)`, `ARRAY[?..]`, JSON keys,
  dollar-quotes, E-strings) to `?`; preserves `$1`/`$2` and `::casts`; strips comments.
  `normalizedQueryHash` is a **stable skeleton hash** (two literal variants → same hash).
- **Never leaked a literal** across adversarial Postgres syntax (dollar-quotes, E-strings, `LIKE`,
  unterminated garbage) — it strips or **truncates** rather than passing literals through
  (privacy-fail-safe).
- **Not pg_query-equivalent:** placeholders are `?` not `$1`; the hash ≠ pg_query fingerprint; and it is
  **lossy on Postgres operators** — `j #>> '{a,b}'` **silently truncated the rest of the query** (CH's
  parser choked on `#>>`). → why the edge pg_query value stays authoritative for T1.
- **MV mechanics:** an MV `TO <existing T1 table>` auto-derives T1 on insert to the raw source,
  **literal-free by construction** when `raw_query` is excluded from the `SELECT` (0 leak on grep);
  edge pg_query columns and a CH-native normalize can coexist in the projection.
- **RLS × MV (decisive):** a row policy `USING (currentUser()='USER') TO ALL` on the MV **source**
  filters the **MV transform**, evaluated as the **inserting** identity — only inserts **by USER**
  propagate to the T1 target; a non-USER insert lands in the source but is dropped from the MV. Lynceus's
  USER is the sole runtime writer, so this holds; re-pinned by test §6.4.
