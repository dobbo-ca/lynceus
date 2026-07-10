# Lynceus — Product Intent & Decision Log

This document captures the intent behind the design decisions made during the design sessions, so the implementation preserves the *why*, not just the pixels. Where the prototype and this document disagree, treat this document as the intent and flag the discrepancy.

---

## 1. Scope is the organizing principle

- The user works at one of five levels: **fleet → cluster → node/pooler → database**. Everything in the app is presented relative to the current scope.
- **Fleet is an at-a-glance triage surface, not a workbench.** At fleet scope the nav shows only the overview lists per vertical plus Saved Scripts. Queries, advisors, activity, checks, schema, logs, capabilities, and the SQL console only exist once a scope is selected — "those things should only appear when the appropriate scope is selected."
- **Cluster scope sees everything bubbled up** from its nodes and databases, always presented with the context of which node/database each item belongs to (server column, per-node config picker, etc.).
- Setting scope is an **explicit action**: the ⌖ icon button on rows (not whole-row clicks) and the searchable top-bar picker. There must always be an obvious way back (`← FLEET`), and top-bar search must cover clusters, nodes, databases **and metadata** like provider (aws/azure) and engine.
- Deep links preserve this: clicking a "needs attention" item sets scope to the offending resource **and** lands on the issue's explanation (e.g. the specific check expanded), never on a generic list.

## 2. Health bubbles up

- Database health → node health → cluster health. Each cluster surfaces a rollup line making the chain visible.
- Dashboards are **problem-only**: healthy items never occupy dashboard space. In the unhealthy state, only components with open crit/warn appear; healthy ones collapse into per-vertical "not shown" links. In the healthy state the dashboard is intentionally near-empty: an all-clear panel with per-vertical links to the healthy inventories.
- At-a-glance severity is exactly three numbers: **CRIT / WARN / INFO**. No T1 badges, component counts, topology strings, or sparkline graphs on fleet cards — those were explicitly removed as noise. One extra lightweight metric per card is fine (QPS, P95, CONNS, TOP WAIT for Postgres).
- Both a **healthy** and **unhealthy** demo/UI state must exist (implemented as `fleetState`) so empty states are designed, not accidental.

## 3. Databases are unique per cluster

- A database is identified by **cluster + name**. `orders-prod/orders` and `analytics-stage/orders` are unrelated; stats, insights, and history are never merged across clusters. The UI always shows the cluster-qualified identity but does **not** warn users about shared names — handling that is purely a backend responsibility.

## 4. Multi-engine from the start

- Verticals: **Database** (Postgres now; MySQL possible later but explicitly not on the roadmap), **Search** (OpenSearch/Elasticsearch), **Cache** (Valkey/Redis).
- Per-engine enable flags (`enablePostgres`, `enableRedis`, `enableValkey`, `enableElasticsearch`, `enableOpensearch`). Cache UI shows if Redis **or** Valkey is enabled; Search if Elasticsearch **or** OpenSearch. Disabled verticals disappear from nav, dashboard cards, stat cells, and links.
- **Engine identity is always visible**: a type icon + major version chip on every cluster/domain row and card, and per-node versions on node lists — because insights and queries depend on the version being targeted, and rolling upgrades mean nodes within a cluster can differ (the prototype shows a replica on 16.4 ahead of its 16.3 primary).
- Engine-specific hierarchies are respected, not flattened:
  - **Cache**: cluster/sentinel → replicaset (1 primary + N replicas) → nodes. Writes go only to a replicaset's primary; replicas are read-only until promoted — the UI badges READ-WRITE vs READ-ONLY per node.
  - **Search**: domain → nodes with **roles** (cluster_manager, data, ingest, coordinating, per the OpenSearch/Elastic role model). Dedicated managers hold no shards. Domain status uses GREEN/YELLOW/RED semantics with the reason (e.g. unassigned replica shards).
- Fleet-level language is engine-neutral ("Monitored nodes", "Fleet ops/sec"), never "PG instances".

## 5. Provider awareness & observability blind spots

- Providers in play: self-hosted, AWS RDS, Azure Flexible Server, PlanetScale. Provider identity appears as a small chip (text marks: AWS / AZ / PS) with details on hover — providers are metadata, **not** prominent UI. The long-form observability explanation lives in an ⓘ tooltip on the provider badge, not a standing card.
- Known blind spots are first-class UI states: an RDS Multi-AZ standby (and Azure zone-redundant HA standby) has no endpoint and is unobservable until promoted — rendered as a dimmed `◌ BLIND SPOT` node with a "provider metrics only" source line. Read replicas have their own endpoints and are fully observable. PlanetScale exposes per-instance metrics via its org Prometheus endpoint — no blind spot, but no host shell.
- Node rows always state their **data source** (collector on host / remote SQL + CloudWatch / Azure Monitor / no endpoint).

## 6. SQL Console — power with guardrails

- The console is a **scoped tool**: available only at cluster/node/database scope. Target selection mirrors the scope: cluster scope → pick node + database; node scope → pick database; database scope → pick node. Nothing runs until the target is fully resolved.
- **Access is a per-cluster, time-boxed session grant** (T2): read-only by default (writes/DDL blocked by policy), tied to a group (dba-oncall), an approver, an expiry, and an incident reference. No grant → a request flow, not a silent failure.
- **Strict audit is non-negotiable**: every statement run against a live server produces an audit record (actor, target, statement, duration, hash) in both the session history and the org audit log.
- Results: paginated with a **user-persisted** rows-per-page setting; previous results are retrievable from history; export as CSV or SQL; copy-to-clipboard guarded by a size check with a "use CSV" fallback.
- **Saved scripts** are always available regardless of scope. Scopes: **global** (org), **team** (group), **personal** (owner). Only the owner (or admin) changes access or deletes; scope changes are audited. Scripts load into the editor without running. "Run a script" searches across all three levels (cluster/node/database) and then requires completing the node+database selection before running.

## 7. Governance placement

- Org-level governance/admin lives under the **user menu** (top-right avatar), not the main nav: Audit Log, Access & Roles; Provider Setup, Collectors, Data & Retention, Settings.
- **Capabilities are a database-level concept, not organizational** — they appear in the scoped nav (cluster/node/database), never in the org menu.
- Appearance (accent color) is a **user setting** with three curated presets, persisted per user, with distinct dark/light variants per preset (bright on dark, deeper on light for contrast).

## 8. Onboarding & data ingestion (AWS as the template)

- Adding a monitored component is a wizard from each vertical's list page: provider selection → collector token → a copyable **Kubernetes Deployment** for the agent → provider-specific access step → the component self-registers when the collector first reports.
- The AWS architecture has **three cooperating data paths** (this framing came directly from the product owner and should shape the collector/ingest design for every provider):
  1. **Direct agent connection** — the agent (in the customer's k8s) connects to the resource endpoint and runs queries against it (pg_stat_*, cluster APIs, INFO). The database role name is **never hardcoded**: it comes from an environment placeholder (`LYNCEUS_DB_ROLE`) so each environment sets its own. Grants are **tiered and optional**: required read-only monitoring (`pg_monitor`) → enable-extensions tier (`CREATE ON DATABASE`, trusted extensions on PG 13+) → maintenance tier (`pg_signal_backend`) → owner-level (apply advisor DDL). Each tier unlocks capabilities surfaced on the Capabilities screen.
  2. **Resource API access (IAM)** — a role for the Lynceus role to call the resource's control-plane APIs for metadata only (not logs/metrics), **scoped strictly to RDS**: RDS ARNs + `aws:ResourceTag/lynceus=true` condition; CloudWatch reads restricted to the `AWS/RDS` namespace.
  3. **Firehose-controlled ingress** — all AWS-side data ships to Lynceus through a **customer-owned Kinesis Firehose** delivery stream: the agent writes to it, CloudWatch Metric Streams (include-filter AWS/RDS) and log subscription filters feed it, and one HTTP delivery leaves the account. This single stream is the customer's ingress control point: buffering, optional Lambda transform (drop/redact before egress), and IAM over which producers may write. Delivery needs the **Lynceus ingest endpoint**, an **auth key** (ingest token), and a **tenant identifier** (`X-Lynceus-Tenant` common attribute).
- Every provider guide must ship a **Terraform version** alongside CLI steps.
- Azure and PlanetScale ship data very differently (Azure Monitor vs Prometheus scrape with API service discovery) and get their own guides following the same paths framing where applicable.
- Provider Setup UI: big block buttons for provider choice; guide content only after selection; tabs are reserved for future capability-specific views.
- **Known gap (explicit)**: agent config + provider config need substantially more work — authentication, tenant modeling, credential rotation, etc. The current guides are directional, not complete.

## 9. Branding constraints

- Official Postgres/OpenSearch/Redis/Valkey/AWS/Azure/PlanetScale logo artwork was deliberately **not** reproduced (copyright). The prototype uses original glyphs (database cylinder / magnifier / key) and text marks (POSTGRESQL, AWS, AZ, PS) rendered in `currentColor` so they theme automatically. If the company licenses official brand assets, they slot into the same sprite/chip positions.

## 10. Explicitly out of scope / not yet designed

- Scope-setting for search domains and cache replicasets (scope model is Postgres-entity-only today).
- Scope-aware data on queries/checks/waits screens (nav is scoped; the demo data is static).
- MySQL support (possible future, not on the roadmap).
- Alerts, schema inventory/growth/indexes, log insights, collectors, retention, access & roles — present as roadmap placeholders in the prototype.
