# Design: T2 raw-isolation via two CH identities + row-level security (ly-cwr.6)

- **Status:** Proposed (decision-ready)
- **Date:** 2026-07-15
- **Bead:** ly-cwr.6 (epic ly-cwr)
- **Supersedes (in part):** the *separate `lynceus_raw` database + dedicated gateway-role* boundary
  recorded as ADOPTED in the ly-cwr.6 bead note and ADR §4.4 / `docs/reference/clickhouse-schema.md`
  §"T2 access control & isolation". See §2 for the conscious override and why.

---

## 1. Problem

ClickHouse is now the sole stats store (ly-cwr.7/8). T1 (`query_stats`) and T2 (`query_stats_t2`)
share **one database (`lynceus_stats`) under one credential**. Nothing at the ClickHouse layer stops
a non-gateway credential from reading `query_stats_t2` literals directly, bypassing the audited
`T2Reader`. The guardrail therefore stands: **no production T2 literal may be written to ClickHouse
until this lands.** Today the collector produces T1 only, so the exposure is latent, not active.

### 1.1 Deployment reality that reshapes the fix

Lynceus does **not own** ClickHouse in production. ClickHouse is a shared, org-operated service that
also holds application logs/metrics and storage logs/metrics (e.g. CloudWatch / Log Insights).
Lynceus is **one tenant** of that service. Two consequences drive this design:

- **Org-owned RBAC for other consumers (Grafana, ad-hoc analysts, Bedrock) is out of Lynceus's
  scope.** Provisioning those roles is the org's responsibility, not Lynceus's.
- **A separate `lynceus_raw` database is not the right lever.** Lynceus cannot assume it can carve
  out and network-isolate a second database in a store it shares. What a tenant *can* control is:
  its own two credentials, a row policy on its own T2 table, per-statement `query_log` settings, and
  its own table TTL.

## 2. Decision (locked) — and the override

**Override.** The adopted "separate `lynceus_raw` DB + dedicated gateway role as the *primary*
boundary" is dropped. Rationale: it presumes Lynceus owns/controls ClickHouse, which is false in the
target deployment. The in-app boundary stays the audited `T2Reader` (code-discipline + audit); the
ClickHouse-layer boundary becomes **row-level security restricting T2 rows to a single Lynceus
runtime identity**. This is a conscious, user-directed override (2026-07-15), analogous in posture to
`pg-stats-retire-now-override`: a scoped risk is accepted to ship the guardrail that matters in the
real deployment.

**Accepted residual risk.** A leaked *Lynceus USER* credential can read T2 rows directly (there is no
separate gateway role to stop it). This is strictly narrower than today (one shared credential for
everything) and is accepted; the audited `T2Reader` remains the enforcement point for everything
Lynceus *serves*.

### 2.1 Two ClickHouse identities

Lynceus uses **two** provisioned CH identities (org-provisioned in prod; compose-provisioned in dev):

| Identity | Env var | Used for | Sees T2 rows? |
|---|---|---|---|
| **ADMIN** | `LYNCEUS_CLICKHOUSE_ADMIN_DSN` | DDL only: migrations, `ALTER … MODIFY TTL`, provisioning the T2 row policy + grants. Needs `access_management`. | No (default; harmless if it does) |
| **USER** | `LYNCEUS_CLICKHOUSE_USER_DSN` *(renamed from `LYNCEUS_CLICKHOUSE_DSN`)* | **All runtime** reads/writes incl. T1 reads/writes, T2 writes, and the `T2Reader` T2 read. | **Yes — the sole identity permitted T2 rows** |

Renaming `LYNCEUS_CLICKHOUSE_DSN` → `LYNCEUS_CLICKHOUSE_USER_DSN` makes the two-identity model explicit
at the config surface.

### 2.2 Row-level security is the ClickHouse boundary

A row policy on `query_stats_t2` that applies to **all** users and passes rows only for the Lynceus
USER:

```sql
CREATE ROW POLICY IF NOT EXISTS t2_lynceus_only ON query_stats_t2
  USING (currentUser() = '<lynceus_user>') TO ALL;
```

**Verified empirically (ClickHouse 25.8, spike 2026-07-15):** the naive `USING 1 TO <lynceus_user>`
does **not** work — ClickHouse does **not** default-deny users *not named* in a policy; `third`
(uncovered) still saw all rows. The construct that works is `USING (currentUser() = '<user>') TO
ALL`: because the policy applies to everyone, only the USER's rows pass and every other identity
(including a broadly-`GRANT`ed org user, and the ADMIN identity) sees **zero** `query_stats_t2` rows.
This behavior is re-pinned by test #1 (red-first). **T1 tables carry no policy → available to all
users**, per directive.

`<lynceus_user>` is derived at bootstrap from the *username* in `LYNCEUS_CLICKHOUSE_USER_DSN`
(`clickhouse.ParseDSN(...).Auth.Username`), so the policy always targets whatever runtime user is
configured, dev and prod alike.

**Column-level security is intentionally NOT implemented** (YAGNI). With other users seeing zero T2
rows, withholding the `normalized_query` column is redundant. It is documented as optional future
hardening in the reference doc, not built here.

### 2.3 `query_log` scrub on the T2 path

On the shared ClickHouse, an unscrubbed T2 statement can leak literals into `system.query_log`, which
**other tenants may read**. So the T2 read (`ReadQueryStatsTier2`) and T2 write (the `query_stats_t2`
insert) run with per-statement settings `log_queries=0` and `log_query_threads=0` (via the CH Go
driver's per-query settings on a `clickhouse.Context`). This keeps Lynceus's T2 literals out of the
shared query log. Server-side `system.query_log` TTL is noted as ops guidance, not a Lynceus control.

### 2.4 Configurable retention (ADR §7.3 resolved)

Retention = **short TTL, configurable, default 7 days** (not a `Null` engine — that would break the
retrospective T2 read-back the `T2Reader`/console export depend on). `LYNCEUS_CLICKHOUSE_T2_TTL_DAYS`
(default 7) is applied by **ADMIN** at bootstrap:

```sql
ALTER TABLE query_stats_t2 MODIFY TTL toDateTime(collected_at) + INTERVAL <N> DAY;
```

`Null`/ultra-short-engine remains a documented per-deployment opt-in for zero-literals-at-rest.

## 3. Architecture & data flow

```
Bootstrap (once per process start, ADMIN identity):
  OpenStats(ctx)
    ├─ open ADMIN conn (LYNCEUS_CLICKHOUSE_ADMIN_DSN; falls back to USER_DSN if unset — simple dev)
    ├─ ApplyClickHouseMigrations         (CREATE TABLE IF NOT EXISTS …, unchanged)
    ├─ ProvisionCHSecurity               (idempotent, tolerant of insufficient-privilege):
    │     • ALTER TABLE query_stats_t2 MODIFY TTL … (from LYNCEUS_CLICKHOUSE_T2_TTL_DAYS)
    │     • CREATE ROW POLICY IF NOT EXISTS t2_lynceus_only ON query_stats_t2
    │           USING (currentUser() = '<user>') TO ALL
    │     • GRANT SELECT, INSERT ON query_stats_t2 TO <user>   (dev/test; org may pre-grant in prod)
    ├─ close ADMIN conn
    └─ open USER conn (LYNCEUS_CLICKHOUSE_USER_DSN) → return NewCHStats(userConn)

Runtime (USER identity):
  T1 reads/writes ............ chStats.<T1 methods>            (unchanged)
  T2 write ................... chStats.WriteQueryStats → query_stats_t2 insert  [log_queries=0]
  T2 read (audited) .......... T2Reader.ReadT2QueryStats
                                 → fast-reject servers.t2_enabled       (config PG)
                                 → EffectiveCapability                  (config PG)
                                 → AppendAuditReturning FIRST, fail-closed (config PG hash-chain)
                                 → chStats.ReadQueryStatsTier2 → FROM query_stats_t2  [log_queries=0]
```

`T2Reader` ordering and the config-PG audit chain are **unchanged**. The single `FROM query_stats_t2`
choke point stays in `chstats.go` (guarded by `TestT2Read_OnlyOneTier2SelectInStoreSource`).

## 4. Components (units of work)

Each unit has one purpose, a defined interface, and independent tests.

1. **Env/DSN rename + ADMIN var** — `internal/store/open.go`: `LYNCEUS_CLICKHOUSE_DSN` →
   `LYNCEUS_CLICKHOUSE_USER_DSN` (required); add optional `LYNCEUS_CLICKHOUSE_ADMIN_DSN`.
   Depends on: nothing. Interface: env → two DSNs. Live references updated: `open.go`,
   `open_test.go`, `README.md`, `docs/reference/clickhouse-schema.md`. (Historical plan/spec docs
   under `docs/superpowers/{plans,specs}/2026-07-14-*` are records — **not** rewritten.)

2. **`ProvisionCHSecurity(ctx, adminConn, opts)`** — new file `internal/store/chsecurity.go`.
   Applies TTL `ALTER`, the T2 row policy, and USER grants. Idempotent (`IF NOT EXISTS`, `MODIFY
   TTL` is declarative). Tolerant: on insufficient-privilege, log a warning and continue (prod org
   may have pre-provisioned). Interface: `(ctx, driver.Conn, ProvisionOpts{UserName, T2TTLDays})
   error`. Depends on: 1.

3. **`OpenStats` two-conn flow** — `internal/store/open.go`: open ADMIN (or USER fallback) for
   migrate + provision, then open USER for the returned `chStats`. Interface unchanged
   (`OpenStats(ctx) (Stats, error)`). Depends on: 1, 2.

4. **`query_log` scrub on the T2 path** — `internal/store/chstats.go`: wrap the `ReadQueryStatsTier2`
   SELECT and the `query_stats_t2` insert in a `clickhouse.Context` carrying
   `log_queries=0, log_query_threads=0`. No signature changes. Depends on: nothing (independent).

5. **`testch` RBAC enablement** — `internal/testch/testch.go`: boot the container with
   `access_management=1` for the bootstrap user (via `tcclickhouse.WithConfigFile` XML), and add a
   helper to mint an extra CH user on the shared server for isolation tests
   (e.g. `testch.NewUser(t, conn, name)` returning a `driver.Conn` for that user). Per-test isolated
   database behavior stays. Depends on: nothing (independent, but tests 6 need it).

6. **Isolation-proof tests** — new `internal/store/chsecurity_test.go` (see §6). Depends on: 2, 5.

## 5. Privacy model (what enforces what)

| Threat | Control | Layer |
|---|---|---|
| Lynceus code reads T2 without audit | Single `FROM query_stats_t2` in `chstats.go`, only caller is `T2Reader`; `AppendAuditReturning` first, fail-closed | App (unchanged) |
| Another CH tenant/user reads T2 literals | Row policy `t2_lynceus_only` → non-USER identities see zero `query_stats_t2` rows | ClickHouse RLS (new) |
| T2 literal leaks into shared `system.query_log` | `log_queries=0`/`log_query_threads=0` on T2 statements | CH session setting (new) |
| Literal custody window unbounded | Configurable TTL (default 7d) on `query_stats_t2` | CH TTL (config'd, new) |
| Leaked Lynceus USER credential | **Accepted residual risk** (see §2) | — |

Out of scope (org-owned or deferred): Grafana/analyst/Bedrock roles and their grants; a separate
`lynceus_raw` database; column-level security; server-side `system.query_log` retention.

## 6. Testing (TDD, real ClickHouse via `internal/testch`, real Postgres via `internal/testpg`)

Red-first. Shared-container helpers only — never per-test `tcpostgres.Run`.

1. **`TestCHSecurity_RowPolicy_DeniesNonUser`** — provision via `ProvisionCHSecurity`; write T2 rows
   as USER; a **third-party** CH user (minted via `testch.NewUser`) with `SELECT` on the table reads
   `query_stats_t2` → **zero rows**; the USER reads → sees the rows. Pins the RLS boundary.
2. **`TestCHSecurity_T1_ReadableByAll`** — same third-party user reads a T1 table (`query_stats`) →
   sees rows (no restrictive policy on T1).
3. **`TestCHSecurity_QueryLogScrub_NoT2Literal`** — after a USER T2 read/write, assert
   `system.query_log` contains **no** `query_stats_t2` literal for those statements
   (`SYSTEM FLUSH LOGS`, then query `system.query_log`).
4. **`TestCHSecurity_TTLConfigurable`** — `ProvisionCHSecurity` with `T2TTLDays=3` sets the table's
   TTL to 3 days (assert via `SHOW CREATE TABLE query_stats_t2` / `system.tables`).
5. **`TestOpenStats_TwoIdentity`** — `open_test.go`: with `ADMIN_DSN` + `USER_DSN` set, `OpenStats`
   migrates+provisions via ADMIN and returns a USER-backed `Stats`; with only `USER_DSN`, falls back
   to USER for bootstrap.
6. **Unchanged, must stay green** — `t2_read_test.go` (fail-closed, happy path, fast-reject, auth
   deny, config-source-of-truth) and `TestT2Read_OnlyOneTier2SelectInStoreSource`.

## 7. Dev / ops wiring

- **`docker-compose.dev.yml`** — grant the dev CH bootstrap user `access_management` (users.d XML),
  and provide both DSNs to the services: `LYNCEUS_CLICKHOUSE_ADMIN_DSN` (the `lynceus` admin) and
  `LYNCEUS_CLICKHOUSE_USER_DSN` (a `lynceus_user` runtime identity, created by the app's
  `ProvisionCHSecurity` on first boot). Password(s) sourced from the gitignored `.env` as today.
- **`README.md`** — document the two env vars and the two-identity posture.
- **`docs/reference/clickhouse-schema.md`** — rewrite §"T2 access control & isolation" to describe
  this design (two identities + RLS + query_log scrub + configurable TTL), replacing the
  separate-`lynceus_raw` description; update the `LYNCEUS_CLICKHOUSE_DSN` mention.

## 8. Out of scope / follow-ups

- Grafana / Bedrock / analyst roles and grants (org-owned RBAC).
- Column-level security on T2 (documented as optional hardening).
- ly-cwr.5 (normalization materialized view + broader CH RBAC) — separate bead.
- ly-cwr.9 (route collector/caps/logparse tests through shared-container helpers) — separate bead.

## 9. Open questions

None blocking. The one behavior this design leans on — the correct ClickHouse RLS construct — was
**verified empirically** on 2026-07-15 (ClickHouse 25.8): `USING 1 TO <user>` does not isolate
(uncovered users are not denied); `USING (currentUser() = '<user>') TO ALL` does. The finding is
re-pinned by test #1 (red-first) so a future CH version change surfaces immediately.
