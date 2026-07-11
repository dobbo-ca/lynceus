# Database-vertical list screens — integration contract (ly-ae6.5)

The three Database-vertical list screens (Clusters / Nodes / Databases) render
inside the landed design shell (`web.Shell` / `web.ShellView`, built by
`Server.buildShellView` → `Server.verticalShell`). Each handler builds the shell
view-model, sets the active sidebar screen, assembles the screen view-model from
`internal/fleetview` roll-ups, and renders `@Shell(vm){ @EngineSprites() @XxxBody(v) }`.

## Routes (Go 1.22 ServeMux)

Literal segments outrank the `/databases/{clusterID}` wildcard, so `/databases/all`
does not conflict with cluster detail (ly-ae6.6).

| Screen    | screenPath key | Page route            | HTMX fragment route            |
|-----------|----------------|-----------------------|--------------------------------|
| Clusters  | `clusters`     | `GET /databases`      | `GET /partial/databases`       |
| Nodes     | `nodes`        | `GET /nodes`          | `GET /partial/nodes`           |
| Databases | `databases`    | `GET /databases/all`  | `GET /partial/databases/all`   |

These match `web/nav.go`'s `screenPath` map (the single source of truth the
scope-driven sidebar links to). Fragments swap `outerHTML` on `#clusters-screen`
/ `#nodes-screen` / `#databases-screen`. Engine sprites (`web.EngineSprites`) and
`verticals.css` (linked once in the shell `<head>`) live OUTSIDE the swap target,
so `<use href="#eng-pg">` refs survive body swaps.

## Query params (stable; the shell may deep-link with them)

- Clusters, Databases: `?sort=health|name`.
- Nodes: `?sort=health|name&q=<text>&page=<n>` (3 groups/page).

## Scope-set button (`⌖`)

Every row/group carries a `⌖` link that sets the working scope, using the landed
scope encoder — `web.ScopeHref(scope.Scope{...})`, identical to the top-bar
picker. No bespoke `data-scope` attribute; the href IS the canonical scoped URL
(`/?scope=<scope.Encode()>`), so it works no-JS and pre-overview:

- cluster group → `scope.Scope{Kind: scope.Cluster, ClusterID}` → `/?scope=cluster:<id>`
- node row     → `scope.Scope{Kind: scope.Node, ClusterID, NodeID: instanceID}` → `/?scope=node:<clusterID>:<instanceID>`
- database row → `scope.Scope{Kind: scope.Database, ClusterID, Database}` → `/?scope=db:<clusterID>:<name>`

Rows themselves remain inert (no whole-row `<a>`), satisfying the design's
"scope only via `⌖`" rule. When the node/database Overview landings ship
(ly-ae6.6), `ScopeHref` can retarget those destinations without changing markup.

`+ ADD CLUSTER` links to `/onboarding?vertical=database` — the onboarding wizard
entry point (owned separately); it 404s until that lands. Documented stub.

## Backend gaps rendered as `—` / omitted (fields present in the view-models)

- Host metrics CPU/MEM/DISK/IO WAIT (Nodes) — no store/collector path yet.
- Provider identity, per-node data-source line, blind-spot detection (Nodes) —
  provider awareness bead **ly-7ck.3**. Provider chip omitted, `◌ BLIND SPOT`
  rendering path is present and unit-tested with a synthetic row.
- Per-database SIZE / CACHE / TABLES (Databases) — **ly-xqf.6 / ly-xqf.7** +
  per-db `pg_stat_database` blks_hit ratio.
- Version chip derives from `pg_settings server_version_num` (the allowlisted
  integer GUC), NOT the un-allowlisted `server_version` string.
- **Databases screen shows rows only once `servers.database_name` is populated**
  (written at collector enrollment, **ly-8b0.8**) — dark until then.
