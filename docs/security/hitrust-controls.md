# HITRUST CSF Control → Evidence Mapping

> **Purpose:** audit-readiness artifact for Goal 4 ("ensure our code is secure and would survive a HITRUST audit"). Maps HITRUST CSF control areas to concrete Lynceus evidence — code, tests, CI, schema, and design. Status is honest: ✅ implemented · 🟡 partial · ⬜ planned (bead). Keep current as controls land.
>
> **Scope:** the Lynceus platform (collector, ingestion_server, api_server) and its two PostgreSQL stores. This is engineering evidence, not a certification; it maps to HITRUST CSF v11 control references and is intended to make a formal assessment tractable.
>
> **Last updated:** 2026-06-05.

## How privacy-by-design pre-satisfies several controls

Lynceus's core architecture is a data-minimization control: analysis happens at the collector and **only normalized, literal-free (T1) data leaves customer infrastructure**. The T1 wire contract physically cannot carry a literal value, enforced by a contract test. This materially reduces the PHI/PII blast radius that most HITRUST data-protection controls govern — for T1 data there is no customer literal to protect in transit or at rest.

- Evidence: [`internal/proto/lynceus/v1/contract_test.go`](../../internal/proto/lynceus/v1/contract_test.go) — `TestQueryStatHasOnlyNormalizedFields`, `TestLogEventHasOnlyClassificationFields`, `TestActivityBucketHasOnlyAggregateFields`.
- Two-tier classification: T1 (normalized, broadly viewable) vs T2 (may contain literals — off by default per server, group-RBAC gated, every read audited). The `data_tier` column and `audit_log` table exist from day one.

---

## Control mapping

### 01 — Access Control

| Ref | Control | Status | Evidence |
|-----|---------|--------|----------|
| 01.b | User registration / identity | 🟡 | OIDC login (`ly-8b0.1`), SCIM provisioning (`ly-8b0.2`) — M5, planned. |
| 01.c | Privilege management (least privilege) | 🟡 | Collector runs as a **read-only, limited DB role**, outbound-only — never modifies the monitored DB (design §2, `internal/collector/reader.go` SELECTs only). RBAC for T2 reads: M5 `ly-8b0`. |
| 01.d | Credential management | 🟡 | No plaintext secrets in repo — enforced by **gitleaks** CI (`.github/workflows/security.yml`). Collector token issuance + rotation: `ly-8b0.8` (planned). |
| 01.v | Remote access encryption | 🟡 | Collector ships over websocket; **TLS/wss enforcement is `ly-cli`** (planned). |

### 06 / 09 — Audit Logging & Monitoring

| Ref | Control | Status | Evidence |
|-----|---------|--------|----------|
| 09.aa | Audit logging of access to covered data | ✅ schema / 🟡 coverage | `audit_log` table exists from day one ([`internal/store/migrations/config`](../../internal/store/migrations/config), `internal/store/config.go` `AppendAudit`). Every T2 read is designed to append an audit entry. Roundtrip test: `TestAuditAppend_roundtrips`. |
| 09.aa | Audit trail integrity (tamper-evidence) | ⬜ | Tamper-evident (hash-chained) audit writer: `ly-8b0.3` (plan written). |
| 09.ab | Monitoring / vulnerability identification | ✅ | CI: CodeQL (SAST), gosec (SAST), govulncheck (SCA), Trivy (CVE), weekly scheduled scan. `.github/workflows/security.yml`. |

### 10 — Secure SDLC, Vulnerability & Change Management

| Ref | Control | Status | Evidence |
|-----|---------|--------|----------|
| 10.b | Secure coding / input validation | ✅ | All DB access uses **parameterized queries** (`pgx`, `$1` placeholders) — no string-built SQL with user input; partition DDL uses validated ISO-week-derived names only. SAST: CodeQL `security-extended` + gosec in CI. |
| 10.k | Change control | ✅ | All changes via PR; CI gates (`ci.yml` test+vet+race, `lint.yml`, `security.yml`) must pass. Generated code (proto/templ) verified in-sync in CI. |
| 10.m | Vulnerability & patch management | ✅ | govulncheck + Trivy in CI; Dependency Review blocks high-severity new deps on PRs (`dependency-review.yml`). Toolchain pinned to **go1.26.4** (`ly-17l`, PR #11) — `govulncheck ./...` reports no vulnerabilities. |
| 10.m | Supply-chain integrity | ✅ | `go.mod`/`go.sum` pinned; Dependency Review action on every PR. |

### Data Protection & Privacy (Encryption)

| Ref | Control | Status | Evidence |
|-----|---------|--------|----------|
| — | Data minimization | ✅ | Privacy-by-design (see top section) — T1 carries no literals; contract-test enforced. |
| — | Encryption in transit | 🟡 | **Enforced at startup** via `internal/secure` (`ly-cli`, PR #12): `CheckDatabaseDSN` rejects non-encrypting `sslmode`, `CheckWebsocketURL` requires `wss://`; wired into api + ingestion mains, default-on. Remaining (`ly-ckd`): collector wss wiring + TLS listener cert (Helm `ly-7ck.1`). |
| — | Encryption at rest | 🟡 | Delegated to RDS (KMS-encrypted storage) — a deployment control to assert in the Helm/runbook (`ly-7ck.1`); no app-managed at-rest keys. |
| — | Data segregation by sensitivity | ✅ | `data_tier` column on every data row; T2 off by default per server, RBAC-gated, audited. |

### Resilience / Availability

| Ref | Control | Status | Evidence |
|-----|---------|--------|----------|
| — | Rate limiting / DoS resistance | ✅ | Ingestion per-server token-bucket rate limiter + dead-letter queue ([`internal/ingest/server.go`](../../internal/ingest/server.go), `0002_dlq.sql`). |
| — | Backpressure on the monitored DB | 🟡 | Collector is read-only and bucketed (60s) to bound load; **bounded concurrent reader fan-out + global query budget**: `ly-awh` (planned, as M2 adds readers). |
| — | Data retention | 🟡 | Partition-DROP retention (`DropPartitionsOlderThan`) — DROP not DELETE, cheap on RDS. Configurable window: `ly-7ck.4`. |

---

## Open items to reach a clean assessment

Tracked under security epic **`ly-1g1`** and referenced milestones:

1. **`ly-cli`** ✅ partial (PR #12) / **`ly-ckd`** — finish TLS in transit (collector wss + TLS listener). *Encryption-in-transit control.*
2. ~~`ly-17l`~~ ✅ **done** (PR #11) — toolchain go1.26.4, govulncheck clean. *10.m.*
3. **`ly-8b0.3`** — tamper-evident (hash-chained) audit log. *09.aa integrity.*
4. **`ly-8b0.1/.2/.8`** — OIDC + SCIM + scoped token issuance/rotation. *01.b/.c/.d.*
5. **`ly-7ck.1`** — Helm chart asserting RDS KMS-at-rest, network policy, non-root/read-only-rootfs pod security. *Deployment controls.*

## Scan evidence (reproduce locally)

```bash
go install golang.org/x/vuln/cmd/govulncheck@latest && govulncheck ./...
go install github.com/securego/gosec/v2/cmd/gosec@latest && gosec -exclude-generated ./...
# CI runs CodeQL, Trivy, gitleaks, dependency-review additionally.
```

Latest local run (2026-06-05): govulncheck — 2 reachable Go **stdlib** vulns (`ly-17l`), no module/app findings. gosec — only protobuf-generated `unsafe` (excluded) + 1 LOW config-sourced log line. No high-severity application findings.
