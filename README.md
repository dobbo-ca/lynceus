# Lynceus

> Lynceus was the Argonaut whose eyesight was so sharp he could see through earth and water. This project sees through your PostgreSQL workload — slow queries, missing indexes, vacuum problems, log signals — **without ever looking at your data**.

Lynceus is an open-source, Kubernetes-native, high-availability platform for monitoring and analyzing PostgreSQL. It is a privacy-first, clean-room reimagining of the capabilities offered by commercial tools such as pganalyze.

## What makes it different

**Analysis happens at the collector. Only normalized data ever leaves your infrastructure.**

Comparable products ship samples to a cloud backend and analyze them server-side. Lynceus does the analysis locally — next to your database — and transmits only normalized, literal-free results. This is a hard architectural guarantee, enforced by the wire contract itself, because Lynceus is built to run over health data subject to **PIPEDA/PHIPA (Canada), GDPR (EU), and HIPAA (USA)**.

- Query performance from normalized `pg_stat_statements`
- Index, EXPLAIN-plan, and VACUUM advisors — computed at the collector
- Connection & wait-event monitoring from `pg_stat_activity`
- Log analysis from local files, **AWS S3**, **Azure Blob Storage**, and filesystem directories
- OIDC login, SCIM 2.0 provisioning, RBAC by group, and a tamper-evident audit log

**Raw query text is opt-in, off by default, and fail-closed.** By default the collector ships
only normalized, literal-free query text. Raw (literal-bearing) `pg_stat_statements` text leaves
the edge **only** for a server whose per-server `servers.t2_enabled` kill switch is on **and** whose
`query_text_t2` capability policy allows it — both must be true. This gate is fail-closed: absent
policy means deny, never default-enabled. When enabled, the raw text is written to the
literal-bearing ClickHouse `query_stats_t2` (T2) table, from which a materialized view derives the
broadly-readable, literal-free `query_stats` (T1) rows by projection (raw text excluded). Reading
the raw text back is RBAC-gated and every read is audited in the Postgres hash-chain.

## Architecture

Three Go services, two databases, an SSR frontend — all Kubernetes-native and horizontally scalable.

| Component | Role |
|---|---|
| **collector** | Runs near Postgres, outbound-only, as a limited DB role. Reads stats/logs, normalizes + analyzes locally, ships normalized data over a websocket. |
| **ingestion_server** | Terminates collector websockets, rate-limits, dead-letter-queues, writes to the stats database. |
| **api_server** | OIDC/SCIM auth, RBAC, audit, collector token issuance, config API; serves the templ + HTMX frontend. |

The **config/metadata + tamper-evident audit** database is **vanilla PostgreSQL** — it runs on managed Postgres including **AWS RDS / Aurora** (no extensions required). Time-series **stats** live in **ClickHouse**, the sole stats store (behind the `store.Stats` interface).

ClickHouse is selected at startup by the **required** `LYNCEUS_STATS_BACKEND=clickhouse` and is reached with **two identities**: `LYNCEUS_CLICKHOUSE_ADMIN_DSN` (DDL + one-time T2 security provisioning) and `LYNCEUS_CLICKHOUSE_USER_DSN` (all runtime reads/writes — the only identity permitted to read the literal-bearing `query_stats_t2` rows). `LYNCEUS_CLICKHOUSE_T2_TTL_DAYS` (default 7) bounds T2 retention. The config + audit database always stays vanilla PostgreSQL.

- **Design (architecture/tech):** [`docs/specs/2026-05-29-lynceus-design.md`](docs/specs/2026-05-29-lynceus-design.md)
- **Feature parity catalog (what it does):** [`docs/specs/2026-05-29-lynceus-features.md`](docs/specs/2026-05-29-lynceus-features.md)
- **Implementation plans:** [`docs/superpowers/plans/`](docs/superpowers/plans/)

## Status

Early development. The first milestone is a thin vertical slice proving the full pipeline (collector → ingestion → TimescaleDB → dashboard) and the privacy contract end-to-end. Roadmap is tracked in [beads](https://github.com/steveyegge/beads) — run `bd ready` to see available work.

## Issue tracking

This project uses [beads](https://github.com/steveyegge/beads) (`bd`) with a Dolt backend.

```bash
bd ready                              # find available work
bd show <id>                          # view an issue
bd update <id> --status=in_progress   # claim work
bd close <id>                         # complete work
```

## License

[MIT](LICENSE)
