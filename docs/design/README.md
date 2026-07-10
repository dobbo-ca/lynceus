# Handoff: Lynceus — Multi-Engine Infrastructure Monitoring

## Overview
Lynceus is a monitoring product for data infrastructure: PostgreSQL today, expanding to Search (OpenSearch/Elasticsearch) and Cache (Valkey/Redis). This handoff contains a high-fidelity interactive prototype of the full app shell — fleet dashboard, scope-driven navigation, per-resource drilldowns, a permission-gated SQL console with saved scripts, provider onboarding guides, and governance surfaces.

Read `PRODUCT_INTENT.md` alongside this file — it captures the product decisions and reasoning from the design conversation and is the source of truth for *why* things work the way they do.

## About the Design Files
The files in this bundle are **design references created in HTML** (`Lynceus.dc.html` + its `support.js` runtime). They are prototypes showing intended look and behavior — **not production code to copy directly**. The task is to recreate these designs in the target codebase's environment (React/Vue/etc.) using its established patterns; if no frontend exists yet, choose the most appropriate framework and implement the designs there. All demo data in the prototype is hardcoded and must be replaced with real APIs.

## Fidelity
**High-fidelity.** Colors, typography, spacing, and interaction patterns are final direction. Recreate the UI faithfully, using the codebase's component library where equivalent.

## Global Shell

### Top bar (48px, JetBrains Mono)
- Logo mark: 20px square, 1.5px accent border, "L" monogram + "LYNCEUS" wordmark → returns to Fleet.
- **SCOPE control**: `SCOPE: <label> ⌄` button opening a searchable dropdown of every scopeable entity (fleet, clusters, nodes, poolers, cluster-qualified databases) with kind badges. Search matches names and kinds. Selecting an entity sets the scope (see Scope Model). When scoped, the chip border/label switch to the accent color and a `← FLEET` button appears to its left.
- Time-range segmented control: 15M / 1H / 24H / 7D / 30D.
- Poll indicator: `● POLL 3S · UPDATED 3S AGO` (pulsing dot).
- Theme toggle (☀/☾) — dark default, light variant, `system` initial preference.
- **User button** (SD, 26px square) opening a dropdown: identity header (username + `GROUP: DBA-ONCALL · T2 GRANTED`), GOVERNANCE section (Audit Log, Access & Roles), ADMIN section (Provider Setup, Collectors, Data & Retention, Settings), Sign out.

### Sidebar (208px, JetBrains Mono 12px)
Section headers are unnumbered, 10px, letter-spaced, faint. Items show SOON / T2 badges where applicable. Active item: accent text, tinted background, 2px right border. **The nav is rebuilt per scope level** — see Scope Model.

## Scope Model (core concept)
Hierarchy: **fleet → cluster → node/pooler → database**. Databases are identified by cluster + name; the same name in two clusters is a different database.

- Scope is set via the top-bar picker **or** the ⌖ icon buttons on any cluster/node/database row (rows themselves are not clickable). Cluster-detail node cards also carry ⌖.
- Setting scope navigates to that entity's Overview and rebuilds the nav:
  - **Fleet**: OVERVIEW (Fleet) · DATABASE (Clusters, Nodes, Databases) · SEARCH (Domains, Nodes) · CACHE (Clusters, Replicasets, Nodes) · CONSOLE (Saved Scripts only).
  - **Cluster**: CLUSTER: <name> (Overview, Nodes, Databases, Capabilities) · QUERIES · ADVISORS (Index, Vacuum, Config·per node) · ACTIVITY · CONSOLE (SQL Console + Saved Scripts) · CHECKS & ALERTS · SCHEMA · LOGS.
  - **Node**: NODE: <name> (Overview, Config, Capabilities) · QUERIES · ADVISORS (Index, Vacuum) · ACTIVITY · CONSOLE · CHECKS & ALERTS · LOGS.
  - **Pooler**: POOLER: <name> (Overview, Config·pgbouncer) · ACTIVITY (Connections) · CONSOLE (scripts only) · CHECKS · LOGS.
  - **Database**: DATABASE: <name> (Overview, Capabilities) · QUERIES · ADVISORS (Index, Vacuum) · CONSOLE · CHECKS · SCHEMA.
- Low-level sections (queries, advisors, checks, schema, logs, capabilities) **never appear at fleet scope**. Saved Scripts is available at every scope; SQL Console only at cluster/node/database scope.
- Every scoped Overview leads with an **"OPEN ISSUES ON THIS <CLUSTER|NODE|DATABASE>"** list filtered to that resource (same row anatomy as the fleet needs-attention list, deep-linking to explanations), or a green `● NO OPEN CHECKS OR INSIGHTS…` strip when clean.

## Screens

### Fleet (dashboard)
- Header: `Fleet` + LIVE badge + `5 DB CLUSTERS · 1 SEARCH DOMAIN · 1 CACHE CLUSTER / RANGE <range>`, SORT: HEALTH/NAME toggle.
- **Stat strip row 1** (engine-neutral, cells drop when an engine is disabled): DATABASES / SEARCH / CACHE with counts. **Row 2**: OPEN CRITICAL / OPEN WARN / OPEN INFO with per-engine breakdown subs.
- **Needs Attention** card: computed `■ n CRIT ■ n WARN` counts, rows = severity square (8px), check/insight id (mono, 230px), detail, server, age. Rows **set scope to the affected node and open the explanation** (checks screen with that check expanded; query drilldown for insights; vacuum advisor for vacuum items). Cross-engine items included (search shards unassigned, cache evictions).
- **Cluster cards** (2-col grid): only components with open crit/warn appear — healthy ones are hidden behind footer links (`3 HEALTHY CLUSTERS NOT SHOWN →`, `ALL SEARCH DOMAINS →`, `ALL CACHE CLUSTERS →`, gated per enabled engine). Card anatomy: name, `v<major.minor>` in accent, provider chip (AWS/AZ with tooltip) when managed, `[HEALTH]`, engine text mark (POSTGRESQL/OPENSEARCH/VALKEY) + engine icon (right side); metrics row QPS · P95 MS · CONNS · TOP WAIT (no graphs); footer strictly `n CRIT n WARN n INFO`.
- Search/cache placeholder cards follow the same anatomy with their own key metrics (search rate/shards/unassigned/heap; ops/memory/hit rate/evictions).
- **Healthy state** (`fleetState` prop): all-clear panel (`ALL CLEAR — NO OPEN CHECKS OR INSIGHTS ACROSS ANY ENGINE`) with per-vertical healthy links; zeroed stat strip; no cards.

### Database vertical
- **Clusters**: flat sortable list (HEALTH/NAME) — engine icon chip, name, version, meta, QPS, health line, ⌖. `+ ADD CLUSTER` opens the deploy wizard.
- **Nodes**: paginated (3 cluster groups/page), search across cluster/node/provider/engine metadata, SORT: HEALTH/NAME (issues first). Group header: engine chip, cluster name, version, provider badge with ⓘ tooltip (observability note), DB→NODE→CLUSTER health rollup line, ⌖. Node rows: role badge, name + per-node version (rolling upgrades make versions differ), source line (collector on host / CloudWatch / Azure Monitor / "no endpoint — blind spot"), CPU/MEM/DISK/IO WAIT, conns vs max_connections bar, health, ⌖. RDS Multi-AZ / Azure HA standbys render dimmed as `◌ BLIND SPOT`.
- **Databases**: grouped by cluster, sortable, cluster-qualified identity (`orders-prod/orders`) under each name; SIZE/QPS/CONNS/CACHE/TABLES; ⌖ per row. Info strip: identity is cluster+name; stats never merge across clusters. (No shared-name warning badges — backend concern only.)
- **Cluster detail**: back link, health, node cards (with ⌖), tabs OVERVIEW/QUERIES/INSIGHTS/ACTIVITY, QPS+latency charts, connection-state bar, recent insights.

### Search vertical (OpenSearch/Elasticsearch)
- **Domains**: domain card — engine icon, name, version, provider, status `[GREEN|YELLOW|RED]` with reason; stat strip (status, indices/shards, nodes by role, JVM heap, search rate); role summary + link to Nodes. `+ ADD DOMAIN` wizard.
- **Nodes**: sortable (HEAP/NAME) table — node, role chips (CLUSTER_MANAGER / DATA / INGEST / COORDINATING), version, heap/cpu/disk/shards. Dedicated managers hold 0 shards.

### Cache vertical (Valkey/Redis)
Hierarchy: cluster (sentinel) → replicasets (1 primary + N replicas) → nodes.
- **Clusters**: sentinel card with stat strip (replicasets, memory, ops/s, hit rate, sentinels/quorum) + replicaset rows + note "writes go to each replicaset's primary — replicas are read-only". `+ ADD CLUSTER` wizard.
- **Replicasets**: sortable (HEALTH/NAME) table — topology, keys, memory, ops/s, evictions, health.
- **Nodes**: sortable (OPS/NAME) — role, node, replicaset, version, memory, ops/s, clients, hit, ACCESS badge (READ-WRITE primary / READ-ONLY replica).

### SQL Console (T2, session-granted)
- Only at cluster/node/database scope. Target rules: cluster → pick node **and** database; node → node fixed, pick database; database → database fixed, pick node. Target picker card shows grant status; RUN is inert until both resolve.
- Session grant is per cluster: banner `● SESSION GRANT ACTIVE — GROUP dba-oncall · READ-ONLY — WRITES & DDL BLOCKED · EXPIRES IN <t> · REF <incident>` + audit-trail link. No grant → request-access gate linking to Capabilities.
- Editor: mono textarea, `ROW LIMIT 500 · STATEMENT TIMEOUT 5S`, RUN ⌘↵, SAVE ▾ (name + GLOBAL/TEAM/PERSONAL scope), compact saved-script search dropdown (focus to browse, type to filter, MANAGE SCRIPTS → link).
- Results: paginated with PREV/NEXT and rows-per-page 10/25/50/100 **persisted per user**; header has ⧉ COPY (full result to clipboard, size-guarded with "too large — use CSV" fallback), ↓ CSV, ↓ SQL (INSERT statements) — all operate on the full result; `T2 READ LOGGED · <hash>`.
- Statement history: **strict audit** — every run recorded with actor, target, timestamp, duration, hash; rows are click-to-retrieve (restores statement + cached result). Runs also append to the org Audit Log.

### Saved Scripts
- List: script name (click → detail), description, ACCESS (scope badge + "visible to …"), owner, saved date, ↪ load / ✕ delete icon buttons (delete only for owner).
- Detail: SQL block; ACCESS card (owner can switch GLOBAL/TEAM/PERSONAL — global=org, team=dba-oncall, personal=owner; changes audited; non-owners see "managed by owner"); RUN card — search across **clusters, nodes, and databases**, then complete the remaining node/database selection before RUN, which lands in the console scoped to the target (executing if granted, otherwise the grant gate).

### Onboarding
- **+ ADD wizard** (modal, per vertical): provider chips (SELF-HOSTED/AWS/AZURE) → collector token step → copyable Kubernetes Deployment YAML (`TARGET_KIND` postgres/opensearch/valkey) → provider-specific step → "appears when the collector first reports". AWS/Azure link to the full guide.
- **Provider Setup** page (user menu ▸ Admin): three **big block buttons** (AWS / Azure / PlanetScale — text-mark chips; swap in licensed logo SVGs when available); guide content renders only after selection (tabs are reserved for future capability-specific views). The AWS guide is the template — see PRODUCT_INTENT.md §8 for the three-path architecture (direct agent connection with tiered role grants and env-placeholder role names; RDS-scoped IAM; Firehose controlled ingress with endpoint/auth/tenant) plus the Terraform variant.

### Governance & Settings
- Audit Log: timestamped, actor, action (`console.query.execute`, `t2.read.query_sample`, …), target, server, tier badge (T2 rows amber-striped), hash chain.
- Capabilities: **database-level** concept — appears in scoped nav, never the org menu.
- Settings: APPEARANCE card — accent color presets (Teal `#2dd4bf`, Cyan `#22d3ee`, Indigo `#818cf8`), persisted per user, **per-theme variants** (dark uses the bright value; light uses deeper: `#0d9488` / `#0891b2` / `#4f46e5`, with matching bg/hover tints).

## Tweakable Props (prototype)
`fleetState` (unhealthy|healthy), `enablePostgres`, `enableRedis`, `enableValkey`, `enableElasticsearch`, `enableOpensearch` (Search section shows if ES||OS; Cache if Redis||Valkey), `accentColor`, `defaultTheme`.

## Design Tokens
Fonts: **Work Sans** (UI text, body 13px) + **JetBrains Mono** (all data, labels, badges; labels 9.5–10.5px letter-spaced, values tabular-nums).

Dark theme: bg `#0c1118`, rail `#0a0e14`, surface `#10161f`, raised `#141c28`, line `#26303d`, line2 `#1a2330`, text `#dbe4ee`, mut `#a3b1c4`, dim `#64748c`, faint `#4d5c73`, acc `#2dd4bf`, acc2 `#5eead4`, accbg `#131c29`, crit `#ef6351`/`#f28b7d`, warn `#e5a83b`/`#e5b45f`, info `#7d8fa8`/`#9fb3cc`.
Light theme: bg `#f2f4f8`, surface `#ffffff`, text `#131c29`, acc `#0d9488`, crit `#c93a28`, warn `#b97e14` (full set in the CSS `:root` / `[data-theme='light']` blocks of `Lynceus.dc.html`).

Shape language: 2px border radius everywhere (1px on tiny badges), 1px borders, no drop shadows except dropdowns/modals (`0 8px 24px rgba(0,0,0,.35)`). Icon buttons 24px square. Severity squares 8px, unrounded. Tables: grid columns with 10–12px gaps, 9.5px letter-spaced faint headers, `overflow-x:auto` wrappers with min-widths (~860–1020px).

## Assets
- Engine icon sprite (inline SVG symbols, `currentColor`): `#eng-pg` (database cylinder), `#eng-os` (magnifier), `#eng-vk` (key). These are **original glyphs** — official Postgres/OpenSearch/Redis/Valkey logos were deliberately not reproduced; substitute licensed brand SVGs into the same slots if/when obtained.
- Provider marks are text chips (AWS / AZ / PS) for the same reason.

## Files
- `Lynceus.dc.html` — the full prototype (template + logic + demo data).
- `support.js` — prototype runtime (reference only; not part of the implementation).
- `PRODUCT_INTENT.md` — decision log from the design conversation. **Read this first.**
