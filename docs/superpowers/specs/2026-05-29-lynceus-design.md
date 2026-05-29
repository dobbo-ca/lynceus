# Lynceus — Design Specification

**Date:** 2026-05-29
**Status:** Draft (initial design, pending review)
**License:** MIT

> Lynceus was the Argonaut whose eyesight was so sharp he could see through earth and water. The project sees through your Postgres workload — surfacing slow queries, missing indexes, vacuum problems, and log signals — without ever looking at your data.

## 1. Purpose & Scope

Lynceus is an open-source, Kubernetes-native, high-availability platform for monitoring and analyzing PostgreSQL workloads. It is a clean-room, privacy-first reimagining of the capabilities offered by commercial tools such as pganalyze.

The defining design choice that separates Lynceus from comparable tools:

> **Analysis happens at the collector. Only normalized data ever leaves the customer's infrastructure.**

Where comparable products ship raw-ish samples to a cloud backend and analyze them server-side, Lynceus performs analysis locally (at the collector, next to Postgres) and transmits only normalized, literal-free results. This is a hard requirement, not a configuration option, because Lynceus is designed to operate over **health data subject to PIPEDA/PHIPA (Canada), GDPR (EU), and HIPAA (USA)**.

### In scope (project, over multiple milestones)
- Query performance monitoring (normalized `pg_stat_statements`).
- Index recommendations (computed at the collector).
- EXPLAIN plan capture and insights (via `auto_explain`, normalized).
- VACUUM / bloat / wraparound monitoring.
- Connection & wait-event monitoring (`pg_stat_activity`).
- Log ingestion and analysis from local files, **AWS S3**, **Azure Blob Storage**, and filesystem directories.
- OIDC user authentication, SCIM 2.0 provisioning, RBAC by group, and a tamper-evident audit log.
- SSR web frontend.

### Explicitly out of scope (for now)
- Modifying the monitored database (Lynceus is strictly read-only against Postgres).
- Storing or transmitting query result rows or any unredacted user data.
- Multi-tenancy as a hosted SaaS (self-hosted, single-org first; multi-org is a later consideration).

## 2. Privacy & Data-Classification Model (the backbone)

Every other design decision is subordinate to this section.

### 2.1 Data classification tiers
- **T1 — Normalized metrics.** Query fingerprints with literals stripped (`WHERE id = $1`), aggregate statistics, schema metadata, plan shapes with constants removed, vacuum/bloat counters, wait-event histograms. Contains **no** literal values. Broadly viewable, subject to org/server scoping.
- **T2 — Potentially sensitive samples.** Query samples or EXPLAIN plans that may embed literal values (e.g. an unparseable query the collector could not normalize). **Disabled by default per server.** When explicitly enabled by an administrator, T2 data is gated behind group RBAC and **every read is written to the audit log**.

The wire protocol (collector → ingestion) is designed so that **T1 messages have no field capable of carrying a literal value**. The privacy guarantee is enforced by the schema itself, not by runtime checks alone.

### 2.2 Normalization & filtering at the collector
- Queries are normalized using `pg_stat_statements` fingerprints and a local SQL parser before anything is queued for transmission.
- Filtering knobs (modeled on proven prior art) applied locally:
  - **query-text filter** — redact queries the parser cannot normalize.
  - **query-sample filter** — `normalize` (strip literals) or `block` (drop entirely). Default `normalize`.
  - **log-secret filter** — strip credentials, bind params, constraint-violation values, and statement text from log lines. Default maximum.
- Analysis (index advice, plan insights, vacuum recommendations) is computed at the collector so that conclusions — not raw inputs — are transmitted.

### 2.3 RBAC, groups & audit
- Provisioning via **SCIM 2.0** (users + groups) from the IdP; login via **OIDC**.
- Access scoped along **org → server → database**, with **group → data-tier** grants.
- The **audit log** records every authentication, every configuration change, and every T2 data access (who, what, when, which server/database). Designed to be append-only / tamper-evident.

## 3. Architecture

Three Go services, two databases, an SSR frontend. Kubernetes-native and horizontally scalable.

```
                          ┌─────────────────────────────┐
   Postgres (customer)    │        Lynceus server        │
   ┌──────────────┐       │                              │
   │  pg_stat_*    │      │   ┌────────────┐  OIDC/SCIM   │
   │  auto_explain │◀──┐  │   │ api_server │◀───────────  IdP (Okta/...)
   │  logs (file/  │   │  │   │  - config  │              │
   │   S3/Azure)   │   │  │   │  - tokens  │──┐           │
   └──────────────┘   │  │   │  - RBAC    │  │ serves     │
          ▲           │  │   │  - audit   │  │ SSR pages  │──▶ Frontend (templ+HTMX)
          │ read-only │  │   └─────┬──────┘  │           │
   ┌──────┴───────┐   │  │         │ config/meta DB       │
   │  collector    │  │  │         ▼ (Postgres)           │
   │ - read stats  │  │  │   ┌────────────┐               │
   │ - normalize   │  └──┼── │ enrollment │               │
   │ - analyze     │     │   └────────────┘               │
   │ - ship T1     │─────┼─▶ ┌─────────────┐  rate-limit  │
   └──────────────┘ WS   │   │ ingestion_  │  + DLQ        │
     (outbound only,     │   │  server     │──▶ stats DB   │
      short-lived JWT)   │   └─────────────┘   (Timescale) │
                          └─────────────────────────────┘
```

### 3.1 collector
- Deployed near Postgres; **outbound connections only** (no listening ports).
- Runs as a **limited Postgres role** (read stats/metadata only; cannot read table data or modify the DB), using `SECURITY DEFINER` helper functions where available.
- Reads: `pg_stat_statements`, `pg_stat_activity`, `auto_explain` output, schema catalogs, and logs.
- **Log sources:** local file tail, filesystem directory, **AWS S3 bucket**, **Azure Blob Storage container**. (This multi-source log capability is a deliberate differentiator.)
- Normalizes + analyzes locally, then ships **T1** snapshots.
- **Snapshot cadence** (modeled on proven prior art):
  - *activity* every ~10s (connections, wait events),
  - *logs* every ~10–30s,
  - *full* every ~10min (query stats, schema, recommendations).
- **Transport:** authenticates to `api_server` over HTTPS with a per-server **enrollment key**, exchanges it for a **short-lived JWT**, opens a **websocket** to `ingestion_server` and streams versioned protobuf.

### 3.2 ingestion_server
- Terminates collector websockets; validates JWTs minted by `api_server`.
- **Rate-limited** to protect the stats database from overload.
- **Dead-letter queue**: failed/over-limit writes are parked durably (Postgres-backed DLQ for the MVP; revisit NATS JetStream if throughput demands) and retried.
- Writes normalized snapshots to the **stats DB** (TimescaleDB hypertables).
- Stateless; horizontally scalable. Collectors connect to any replica.

### 3.3 api_server
- **OIDC** login (Okta/generic). `LYNCEUS_DEV_AUTH=true` bypasses OIDC with a static dev admin for local development.
- **SCIM 2.0** endpoint for user/group provisioning, update, and deprovisioning.
- **Collector enrollment & token issuance** (enrollment key → short-lived JWT, with rotation).
- **Configuration API** for collectors and the frontend.
- **RBAC enforcement** and **audit log** writer.
- Serves the **SSR frontend**.
- Stateless; horizontally scalable.

### 3.4 Frontend
- **Server-side rendered** via Go **templ** templates with **HTMX** for dynamic fragments (auto-refreshing activity views, drill-downs). Charts via a lightweight JS charting library loaded per page.
- Single-language stack (Go), no separate Node runtime in the deploy.

### 3.5 Data stores
- **config/metadata DB** — plain PostgreSQL: orgs, servers, users, groups, RBAC grants, collector enrollment, audit log.
- **stats DB** — TimescaleDB: time-series snapshots (query stats, activity, wait events, vacuum, recommendations).
- Both deployed for HA via the **CloudNativePG** operator.

## 4. Wire Contract (collector ↔ server)

- **collector → api_server (HTTPS/REST):** enrollment, token exchange, config fetch.
- **collector → ingestion_server (websocket, protobuf):** versioned T1 snapshot messages.
- The protobuf schema lives in a shared `proto/` package and is the **single source of truth for the privacy guarantee**: T1 message types contain only normalized fields. Any T2 capability is a separate, explicitly-gated message type.
- Schema versioned from v1; collector and server negotiate version on connect.

## 5. Repository Layout (monorepo)

```
lynceus/
  cmd/
    collector/        # collector binary
    ingestion/        # ingestion_server binary
    api/              # api_server binary (also serves frontend)
  internal/
    proto/            # generated protobuf (the wire contract)
    normalize/        # query/log normalization + classification
    analyze/          # collector-side analysis (index, plan, vacuum)
    pgsource/         # pg_stat_* / auto_explain readers
    logsource/        # file / S3 / Azure Blob log readers
    auth/             # OIDC, SCIM, JWT, RBAC
    audit/            # audit log
    store/            # config DB + stats DB access
  web/                # templ templates + HTMX assets
  charts/             # Helm chart (HA k8s deploy)
  proto/              # .proto definitions
  docs/specs/         # this spec and successors
```

## 6. High Availability & Kubernetes

- `api_server` and `ingestion_server` are stateless and run multiple replicas behind Services.
- Databases run under the CloudNativePG operator (primary + replicas, failover).
- A **Helm chart** deploys the full stack; values gate optional features (T2 capture, log sources).
- Websockets are long-lived; any ingestion replica can accept any collector.

## 7. Milestone 1 — Thin Vertical Slice (MVP)

Proves the entire pipeline and the privacy contract end-to-end before deepening any component.

**Flow:** collector reads `pg_stat_statements` → normalizes → opens websocket with a dev token → `ingestion_server` rate-limits + writes to TimescaleDB → `api_server` query endpoint → **templ/HTMX dashboard listing top queries by total time**.

**Included:**
- Monorepo scaffold, `proto/` v1 with the T1 query-stats message, dev harness (docker-compose Postgres + Timescale).
- Collector: `pg_stat_statements` reader + normalization + websocket shipper.
- ingestion_server: websocket receiver + basic rate limit + Postgres DLQ + Timescale writer.
- api_server: dev-auth mode, query API, templ/HTMX top-queries dashboard.
- The **audit log table and data-classification column exist in the schema** even though the MVP only produces T1.

**Deferred to later milestones:** OIDC/SCIM/RBAC (stubbed in MVP), index advisor, EXPLAIN/plan insights, vacuum advisor, activity/wait events, log sources (file/S3/Azure), alerting, full Helm/HA hardening.

## 8. Subsequent Milestones (rough order)
2. **Collector depth** — `pg_stat_activity` + wait events; schema stats; `auto_explain` plan capture (normalized).
3. **Analysis** — index advisor, plan insights, vacuum/bloat/wraparound advisor (all collector-side).
4. **Log Insights** — file/dir/S3/Azure log sources, log parsing, log-derived events.
5. **Auth & governance** — OIDC, SCIM, RBAC by group, audit-gated T2 access.
6. **HA & ops** — Helm chart hardening, CloudNativePG, alerting (email/Slack/PagerDuty), retention.

## 9. Testing Strategy
- Unit tests for normalization/classification with adversarial inputs (assert no literals escape T1).
- A **contract test** asserting the protobuf T1 schema contains no free-text/literal-capable fields.
- Integration tests against a real Postgres (and Timescale) via docker-compose — no mocked DB.
- End-to-end test of the vertical slice in CI.

## 10. Open Questions (revisit per milestone)
- Stats DB: TimescaleDB vs. vanilla Postgres partitioning (chosen: Timescale; reconfirm at milestone 1).
- DLQ/buffer: Postgres-backed vs. NATS JetStream (chosen: Postgres for MVP; reconfirm if throughput requires).
- Charting library for the SSR frontend.
- Retention policy defaults and per-tier configurability.
