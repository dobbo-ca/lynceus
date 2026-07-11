# Scope-Driven Sidebar Nav Rebuild Implementation Plan (`ly-ae6.3`)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. Write the failing test first, watch it fail, implement, watch it pass, commit.

**Goal:** Build the **scope-driven sidebar nav engine + component + design-system rail styling + integration seam** that the app shell uses to replace the single flat static `<nav>`. The nav rebuilds its tree per scope level (fleet → cluster → node → pooler → database), enforcing the design's gating rules (low-level sections never at fleet, Saved Scripts everywhere, SQL Console only at cluster/node/database) and the 208px JetBrains-Mono rail styling.

**Scope boundary (read this first — it resolves the adversarial-review "double-rail / weak end-state" finding):** This bead delivers the nav **engine** (`BuildNav`), the **component** (`Sidebar` + `SidebarFromContext`), the **rail stylesheet** (`nav.css`), and the **context seam** (`NavState`) that a shell threads a resolved scope through. It does **NOT** flip the live app onto the new rail. The live mount — replacing `web/layout.templ`'s flat `<nav>` and retiring the legacy `web/overview.templ`/`web/cluster_views.templ` `ClusterSidebar` — is **deliberately deferred** to a filed follow-up bead (Task 7) because mounting it now would **regress navigation**: the new tree's links point at flat routes carrying scope in the query string (`/waits?scope=cluster&cluster=…`), and until `ly-ae6.2`'s request resolver + the scoped destination screens exist, those links resolve to *unscoped fleet data*, and cluster pages would render *two rails*. The only live change here is a harmless `<link>` to the (currently unused) stylesheet and its serving. Every acceptance criterion of the bead is verifiable at the engine/component level without the live flip (see Self-Review).

**Architecture:** The nav is a **pure function of scope**, not of any store: `BuildNav(sc Scope, eng EngineFlags, active string) []NavGroup` (package `web`) mirrors the prototype's `navDef` builder exactly (`docs/design/Lynceus.dc.html` lines 2366–2420), and `Sidebar(sc, eng, active)` renders it into a token-styled rail. `SidebarFromContext()` reads a `NavState` (scope + engines + active) off the request context — the single seam a shell (`ly-ae6.2`) populates. Scoped section links carry the current scope as **separate, individually-escaped query params** produced by `NavHref`; `ScopeFromValues` is the exact, round-trip-tested inverse `ly-ae6.2`'s resolver calls.

**Tech Stack:** Go 1.26.3, `github.com/a-h/templ` v0.3.1020 (server-side rendered components; regenerate with `make templ`), `net/url` (`url.Values` scope-param escaping), token CSS custom properties in `web/static/css/` (embedded via `//go:embed static` in `web/static.go`), `net/http/httptest` + rendered-HTML string assertions for tests (no DB — the nav touches no store).

## Global Constraints

Copied verbatim; every task obeys these.

- **Privacy T1/T2.** Only T1 (normalized, literal-free) data renders unless a screen is explicitly T2 (SQL Console, audited reads). The nav renders **no data at all** — only static labels and scope identifiers (cluster/node/database *names*, which are T1 metadata). The nav must never introduce a raw-literal field. SQL Console and the `T2` badge are nav *entries*; the T2 gating/audit lives in `ly-ae6.8`, not here.
- **No external hosts.** Never add a CDN/font/script host. All CSS/JS/fonts are self-hosted under `web/static/` and referenced at `/static/...`. There is a contract test `TestLayout_NoExternalHosts` (`web/layout_test.go`) — it must stay green.
- **Tokens, not legacy.** New styling uses design tokens (`var(--x)` from `web/static/css/tokens.css`), never the pre-design `legacy.css` component classes and never hardcoded hex/`system-ui`. `TestLayout_NoInlineStyleBlock` forbids `system-ui, sans-serif` and `#2b6cb0` in the layout output. **`nav.css` must additionally neutralize legacy.css bare-element bleed** (see Task 4 / the review finding on `nav a`).
- **templ regen.** Any `*.templ` edit requires `make templ` to regenerate the committed `*_templ.go`; CI checks the generated files are in sync. Commit the `.templ` and its `_templ.go` together.
- **testcontainers for DB.** Integration tests hit real Postgres via testcontainers, never DB mocks. **This task needs no database** — the nav is derived purely from `Scope` + `EngineFlags`, so all tests are fast unit/render tests in package `web`.

## Dependencies & integration contract

- **Depends on `ly-ae6.2`** (Global top bar + scope model + searchable SCOPE picker). `ly-ae6.2` owns: the top-bar picker/⌖ buttons that *set* scope, `← FLEET`, the request→`Scope` **resolver**, and the eventual full 2-column shell that renders the sidebar slot.
- **Definitive symbol ownership of `web/scope.go`** (resolves the review's "ownership inversion" finding). `web/scope.go` is co-located but **owned per-symbol**, and the two beads never edit the same declaration:
  - **`ly-ae6.2` owns the type _declarations_** — `Scope`, `ScopeLevel`, `EngineFlags` (structs + fields) — because it is the earlier, foundational bead and its request resolver cannot compile without them. It also owns the request-side resolver.
  - **`ly-ae6.3` (this bead) owns the pure codec + display methods + context seam** — `Query`, `ScopeFromValues`, `HeaderLabel`, `FleetScope`, `DefaultEngines`, and the `NavState` context helpers — plus `NavHref`, `BuildNav`, and the `Sidebar` templ.
  - Because either bead may physically land first, **Task 1 Step 0 branches on whether `web/scope.go` already exists** (see Task 1) — extend if present, create if absent, and STOP-and-reconcile if the field shape diverges from the agreed shape below. This is a concrete implementation branch, not a prose note.
- **URL contract (co-owned, resolved here).** Scope travels in **separate, individually-escaped query params** (`scope=<level>`, plus `cluster=`, `node=`, `db=` as applicable) built by `Scope.Query() url.Values`. This is injective even when cluster/node/database names contain `/` or `:` (Postgres names legally can). `ly-ae6.2`'s resolver is exactly `web.ScopeFromValues(r.URL.Query())`, which `TestScope_QueryRoundTrip` proves is the inverse of `Query()`.
- **Shell seam.** `ly-ae6.2`'s shell mounts `@web.SidebarFromContext()`, having first put a resolved `web.NavState` on the request context via `web.WithNavState`. `SidebarFromContext` falls back to `FleetScope()` + `DefaultEngines()` when no `NavState` is present, so it is safe to render before `ly-ae6.2`'s middleware exists.
- **Blocks** `ly-ae6.4` (fleet dashboard), `ly-ae6.5/6/7/8/9/10/11`. Those verticals own the *destination routes* the nav links to. The `screenPath` map in Task 2 is the single source of truth for scoped-section URLs; verticals register their route to match. Links that point at a not-yet-built route (e.g. `/nodes`, `/console`, `/scripts`, `/search/*`, `/cache/*`) render correctly and 404 until the owning bead ships — that is expected and matches the design's forward-linking nav.
- No backend bead is required: COMPARISON lists `sidebar-nav` backend beads as **none**.

## Branch setup

This session must not commit on `main`. Before Task 1:

- [ ] Create an isolated branch in this worktree: `git switch -c ui-scope-nav-$(openssl rand -hex 2)`
- [ ] Confirm HEAD: `git branch --show-current` shows the new branch. Every commit below lands here.

---

### Task 1: Scope + EngineFlags value-objects + injective URL codec (integration contract with ly-ae6.2)

The pure data types the nav consumes plus the injective scope⇄URL codec. No store, no request parsing (that's `ly-ae6.2`).

**Files**
- create **OR extend**: `web/scope.go` (see Step 0 branch)
- create: `web/scope_test.go`

**Agreed type shape (must match whatever `ly-ae6.2` created, if it landed first):**
```go
type ScopeLevel string
const (
    LevelFleet    ScopeLevel = "fleet"
    LevelCluster  ScopeLevel = "cluster"
    LevelNode     ScopeLevel = "node"
    LevelPooler   ScopeLevel = "pooler"
    LevelDatabase ScopeLevel = "database"
)
type Scope struct {
    Level   ScopeLevel // which of the five levels
    Cluster string     // cluster name (cluster/node/pooler/database scopes)
    Node    string     // node/pooler name (node/pooler scopes)
    DB      string     // database name (database scope)
}
type EngineFlags struct { Postgres, Search, Cache bool }
```

**Interfaces owned by this bead (added here regardless of who declared the types):**
```go
func FleetScope() Scope
func DefaultEngines() EngineFlags                 // {Postgres:true} — the only real engine today
func (s Scope) HeaderLabel() string               // identity-group header, e.g. "CLUSTER: ORDERS-PROD"
func (s Scope) Query() url.Values                 // injective scope→params (scope/cluster/node/db)
func ScopeFromValues(v url.Values) Scope           // exact inverse of Query(); ly-ae6.2's resolver calls this
```

**Steps**

- [ ] **Step 0: Branch on existing ownership (resolves review finding #1).**
  Run `test -f web/scope.go && echo EXISTS || echo ABSENT`.
  - **ABSENT** → `ly-ae6.3` is landing first (or `ly-ae6.2` deferred the types). Create `web/scope.go` in full (Step 3, including the type declarations).
  - **EXISTS** → `ly-ae6.2` already created it. **Read it.** Confirm `Scope{Level ScopeLevel; Cluster, Node, DB string}`, the five `ScopeLevel` constants, and `EngineFlags{Postgres, Search, Cache bool}` match the shape above **exactly**. If they match, in Step 3 add **only** the methods this bead owns that are not already present (`FleetScope`, `DefaultEngines`, `HeaderLabel`, `Query`, `ScopeFromValues`) and do **not** re-declare the types. If any field name/type diverges, **STOP** — do not fork the type; record the mismatch and hand back for a human to reconcile the two beads' agreed shape.
- [ ] **Step 1: Write the failing test.** Create `web/scope_test.go`:
  ```go
  package web

  import (
      "net/url"
      "reflect"
      "testing"
  )

  func TestScope_QueryRoundTrip(t *testing.T) {
      // Round-tripping proves Query() is injective and ScopeFromValues is its
      // exact inverse — the co-owned URL contract with ly-ae6.2's resolver.
      // Includes names containing '/' and ':' (legal in Postgres identifiers).
      cases := []Scope{
          FleetScope(),
          {Level: LevelCluster, Cluster: "orders-prod"},
          {Level: LevelNode, Cluster: "orders-prod", Node: "srv-orders-1"},
          {Level: LevelPooler, Cluster: "orders-prod", Node: "pgbouncer-1"},
          {Level: LevelDatabase, Cluster: "orders-prod", DB: "orders"},
          {Level: LevelDatabase, Cluster: "a/b:c", DB: "weird/name:x"}, // adversarial names
          {Level: LevelNode, Cluster: "c:1", Node: "n/2"},
      }
      for _, sc := range cases {
          got := ScopeFromValues(sc.Query())
          if !reflect.DeepEqual(got, sc) {
              t.Errorf("round-trip: ScopeFromValues(%v.Query()) = %#v, want %#v", sc, got, sc)
          }
      }
  }

  func TestScope_QueryKeys(t *testing.T) {
      // Locks the exact param scheme ly-ae6.2's resolver reads back.
      cases := []struct {
          sc   Scope
          want url.Values
      }{
          {FleetScope(), url.Values{"scope": {"fleet"}}},
          {Scope{Level: LevelCluster, Cluster: "orders-prod"},
              url.Values{"scope": {"cluster"}, "cluster": {"orders-prod"}}},
          {Scope{Level: LevelNode, Cluster: "orders-prod", Node: "srv-orders-1"},
              url.Values{"scope": {"node"}, "cluster": {"orders-prod"}, "node": {"srv-orders-1"}}},
          {Scope{Level: LevelDatabase, Cluster: "orders-prod", DB: "orders"},
              url.Values{"scope": {"database"}, "cluster": {"orders-prod"}, "db": {"orders"}}},
      }
      for _, c := range cases {
          if got := c.sc.Query(); !reflect.DeepEqual(got, c.want) {
              t.Errorf("Query(%v) = %v, want %v", c.sc, got, c.want)
          }
      }
  }

  func TestScope_HeaderLabel(t *testing.T) {
      cases := []struct {
          sc   Scope
          want string
      }{
          {FleetScope(), "OVERVIEW"},
          {Scope{Level: LevelCluster, Cluster: "orders-prod"}, "CLUSTER: ORDERS-PROD"},
          {Scope{Level: LevelNode, Cluster: "orders-prod", Node: "srv-orders-1"}, "NODE: SRV-ORDERS-1"},
          {Scope{Level: LevelPooler, Node: "pgbouncer-1"}, "POOLER: PGBOUNCER-1"},
          {Scope{Level: LevelDatabase, DB: "orders"}, "DATABASE: ORDERS"},
      }
      for _, c := range cases {
          if got := c.sc.HeaderLabel(); got != c.want {
              t.Errorf("HeaderLabel(%v) = %q, want %q", c.sc, got, c.want)
          }
      }
  }

  func TestDefaultEngines_PostgresOnly(t *testing.T) {
      e := DefaultEngines()
      if !e.Postgres || e.Search || e.Cache {
          t.Errorf("DefaultEngines() = %+v, want only Postgres true", e)
      }
  }
  ```
- [ ] **Step 2: Run it — expect FAIL (compile error, undefined symbols).**
  `go test ./web/ -run 'TestScope|TestDefaultEngines'` → build fails: `undefined: FleetScope`, `undefined: ScopeFromValues`, etc.
- [ ] **Step 3: Implement.** Create (ABSENT branch) or extend (EXISTS branch) `web/scope.go`. Full ABSENT-branch content:
  ```go
  package web

  import (
      "net/url"
      "strings"
  )

  // ScopeLevel is one of the five scope tiers the whole UI is organised around
  // (PRODUCT_INTENT §1). See the scope model in docs/design/README.md.
  //
  // OWNERSHIP: the ScopeLevel/Scope/EngineFlags type declarations are owned by
  // ly-ae6.2 (foundational). This file also carries them only when ly-ae6.3
  // lands first (see the plan's Task 1 Step 0 branch). The codec + display
  // methods below are owned by ly-ae6.3.
  type ScopeLevel string

  const (
      LevelFleet    ScopeLevel = "fleet"
      LevelCluster  ScopeLevel = "cluster"
      LevelNode     ScopeLevel = "node"
      LevelPooler   ScopeLevel = "pooler"
      LevelDatabase ScopeLevel = "database"
  )

  // Scope is the current navigation scope. It is a pure value-object: ly-ae6.2
  // resolves an *http.Request into a Scope (via ScopeFromValues on the query
  // params produced by Query) and hands it to the shell/Sidebar. Databases are
  // identified by cluster + name, so the same DB name in two clusters is a
  // different scope.
  type Scope struct {
      Level   ScopeLevel
      Cluster string // cluster name; set for cluster/node/pooler/database scopes
      Node    string // node/pooler name; set for node/pooler scopes
      DB      string // database name; set for database scope
  }

  // EngineFlags gates the per-vertical fleet sections. Cache shows if Redis OR
  // Valkey is enabled, Search if Elasticsearch OR OpenSearch — the caller
  // collapses those pairs before constructing this (PRODUCT_INTENT §5).
  type EngineFlags struct {
      Postgres bool
      Search   bool
      Cache    bool
  }

  // ---- ly-ae6.3-owned codec + display helpers -----------------------------

  // FleetScope is the default (unscoped) scope.
  func FleetScope() Scope { return Scope{Level: LevelFleet} }

  // DefaultEngines is the only real configuration today: Postgres on, Search and
  // Cache off until those verticals ship (ly-ae6.10 / ly-ae6.11).
  func DefaultEngines() EngineFlags { return EngineFlags{Postgres: true} }

  // Query encodes the scope as separate, individually-escaped query params. Using
  // discrete params (rather than a single ':'/'/'-delimited token) keeps the
  // encoding injective even when cluster/node/database names contain '/' or ':',
  // which Postgres identifiers legally can. This is the URL contract; ly-ae6.2's
  // request resolver is ScopeFromValues(r.URL.Query()) — its exact inverse.
  func (s Scope) Query() url.Values {
      v := url.Values{"scope": {string(s.Level)}}
      if s.Cluster != "" {
          v.Set("cluster", s.Cluster)
      }
      if s.Node != "" {
          v.Set("node", s.Node)
      }
      if s.DB != "" {
          v.Set("db", s.DB)
      }
      return v
  }

  // ScopeFromValues is the exact inverse of Query(). Unknown/absent scope params
  // resolve to the fleet scope.
  func ScopeFromValues(v url.Values) Scope {
      switch ScopeLevel(v.Get("scope")) {
      case LevelCluster:
          return Scope{Level: LevelCluster, Cluster: v.Get("cluster")}
      case LevelNode:
          return Scope{Level: LevelNode, Cluster: v.Get("cluster"), Node: v.Get("node")}
      case LevelPooler:
          return Scope{Level: LevelPooler, Cluster: v.Get("cluster"), Node: v.Get("node")}
      case LevelDatabase:
          return Scope{Level: LevelDatabase, Cluster: v.Get("cluster"), DB: v.Get("db")}
      default:
          return FleetScope()
      }
  }

  // HeaderLabel is the identity-group header shown at the top of the scoped nav
  // tree, e.g. "CLUSTER: ORDERS-PROD" (design: cluster/node/pooler/database trees).
  func (s Scope) HeaderLabel() string {
      switch s.Level {
      case LevelCluster:
          return "CLUSTER: " + strings.ToUpper(s.Cluster)
      case LevelNode:
          return "NODE: " + strings.ToUpper(s.Node)
      case LevelPooler:
          return "POOLER: " + strings.ToUpper(s.Node)
      case LevelDatabase:
          return "DATABASE: " + strings.ToUpper(s.DB)
      default:
          return "OVERVIEW"
      }
  }
  ```
  (EXISTS branch: paste only the block below `// ---- ly-ae6.3-owned codec + display helpers ----`, plus the `net/url` and `strings` imports if missing. Do not re-declare `ScopeLevel`/`Scope`/`EngineFlags`.)
- [ ] **Step 4: Run it — expect PASS.**
  `go test ./web/ -run 'TestScope|TestDefaultEngines'` → `ok`.
- [ ] **Step 5: Commit.**
  `git add web/scope.go web/scope_test.go && git commit -m "ly-ae6.3: Scope codec (injective query params) + display helpers"`

---

### Task 2: NavHref + screenPath URL scheme + nav types + active-alias logic

The scoped-URL producer, the item/group data types `BuildNav` fills, and the active-state alias rules (resolves review finding #5).

**Files**
- create: `web/nav.go` (types + `screenPath` + `NavHref` + `NavItem.ItemClass` + `isActive`; `BuildNav` is added in Task 3)
- create: `web/nav_href_test.go`

**Interfaces**
- Produces:
  ```go
  type NavItem struct {
      Label  string // display text, e.g. "Wait Events"
      Screen string // design screen id, key into screenPath
      Href   string // NavHref(sc, Screen)
      Active bool
      Soon   bool   // SOON badge
      T2     bool   // T2 badge
  }
  func (it NavItem) ItemClass() string             // CSS class list for the <a>
  type NavGroup struct { Label string; Items []NavItem }
  func NavHref(sc Scope, screen string) string     // screenPath[screen] + "?" + sc.Query().Encode()
  func isActive(screen, active string, lvl ScopeLevel) bool // exact match + prototype aliases
  ```
- Consumes: `Scope` (Task 1).

**Active-alias rules (verbatim from `Lynceus.dc.html` line 2411).** The prototype marks a nav item active when `S.screen === id` OR one of these detail-screen aliases holds. `isActive` must encode them so parent items still highlight when the detail sub-screens ship (`ly-ae6.4/6/9`):
- `topqueries` is active when `active == "querydetail"` (query drilldown).
- `scripts` is active when `active == "scriptdetail"` (saved-script detail).
- `clusters` is active when `active == "clusterdetail"` **and level is fleet** (fleet Database › Clusters ↔ a fleet-scope cluster detail).

**Steps**

- [ ] **Step 1: Write the failing test.** Create `web/nav_href_test.go`:
  ```go
  package web

  import "testing"

  func TestNavHref_EncodesScope(t *testing.T) {
      // url.Values.Encode() sorts keys alphabetically: cluster < db < node < scope.
      cluster := Scope{Level: LevelCluster, Cluster: "orders-prod"}
      node := Scope{Level: LevelNode, Cluster: "orders-prod", Node: "srv-orders-1"}
      cases := []struct {
          name, screen string
          sc           Scope
          want         string
      }{
          {"fleet overview", "fleet", FleetScope(), "/?scope=fleet"},
          {"fleet clusters", "clusters", FleetScope(), "/databases?scope=fleet"},
          {"cluster waits", "waits", cluster, "/waits?cluster=orders-prod&scope=cluster"},
          {"cluster sql console", "console", cluster, "/console?cluster=orders-prod&scope=cluster"},
          {"node waits", "waits", node, "/waits?cluster=orders-prod&node=srv-orders-1&scope=node"},
      }
      for _, c := range cases {
          if got := NavHref(c.sc, c.screen); got != c.want {
              t.Errorf("%s: NavHref = %q, want %q", c.name, got, c.want)
          }
      }
  }

  func TestNavHref_UnknownScreenFallsBackToRoot(t *testing.T) {
      if got := NavHref(FleetScope(), "does-not-exist"); got != "/?scope=fleet" {
          t.Errorf("unknown screen: NavHref = %q, want /?scope=fleet", got)
      }
  }

  func TestNavItem_ItemClass(t *testing.T) {
      cases := []struct {
          it   NavItem
          want string
      }{
          {NavItem{}, "ln-nav-item"},
          {NavItem{Active: true}, "ln-nav-item ln-nav-item--active"},
          {NavItem{Soon: true}, "ln-nav-item ln-nav-item--soon"},
          {NavItem{Active: true, Soon: true}, "ln-nav-item ln-nav-item--active ln-nav-item--soon"},
      }
      for _, c := range cases {
          if got := c.it.ItemClass(); got != c.want {
              t.Errorf("ItemClass(%+v) = %q, want %q", c.it, got, c.want)
          }
      }
  }

  func TestIsActive_ExactAndAliases(t *testing.T) {
      cases := []struct {
          name, screen, active string
          lvl                  ScopeLevel
          want                 bool
      }{
          {"empty active highlights nothing", "waits", "", LevelCluster, false},
          {"exact match", "waits", "waits", LevelCluster, true},
          {"querydetail aliases topqueries", "topqueries", "querydetail", LevelCluster, true},
          {"scriptdetail aliases scripts", "scripts", "scriptdetail", LevelPooler, true},
          {"clusterdetail aliases clusters at fleet", "clusters", "clusterdetail", LevelFleet, true},
          {"clusterdetail does NOT alias clusters off fleet", "clusters", "clusterdetail", LevelCluster, false},
          {"no cross-alias", "waits", "querydetail", LevelCluster, false},
      }
      for _, c := range cases {
          if got := isActive(c.screen, c.active, c.lvl); got != c.want {
              t.Errorf("%s: isActive(%q,%q,%q) = %v, want %v", c.name, c.screen, c.active, c.lvl, got, c.want)
          }
      }
  }
  ```
- [ ] **Step 2: Run it — expect FAIL (compile error, undefined `NavHref`/`NavItem`/`NavGroup`/`isActive`).**
  `go test ./web/ -run 'TestNavHref|TestNavItem|TestIsActive'` → build fails.
- [ ] **Step 3: Implement.** Create `web/nav.go`:
  ```go
  package web

  // NavItem is one link in the scope-driven sidebar.
  type NavItem struct {
      Label  string
      Screen string // design screen id (key into screenPath)
      Href   string
      Active bool
      Soon   bool
      T2     bool
  }

  // ItemClass is the CSS class list for the item's <a>. Styling lives in
  // web/static/css/nav.css (tokens only).
  func (it NavItem) ItemClass() string {
      c := "ln-nav-item"
      if it.Active {
          c += " ln-nav-item--active"
      }
      if it.Soon {
          c += " ln-nav-item--soon"
      }
      return c
  }

  // NavGroup is a labelled section of the sidebar (e.g. "QUERIES").
  type NavGroup struct {
      Label string
      Items []NavItem
  }

  // screenPath maps a design screen id to its canonical route. It is the single
  // source of truth for scoped-section URLs — verticals (ly-ae6.4..11) register
  // their route to match these. Not-yet-built routes 404 until their owning bead
  // ships; the nav links ahead to them intentionally.
  var screenPath = map[string]string{
      // fleet + database vertical
      "fleet":     "/",              // fleet dashboard (ly-ae6.4)
      "clusters":  "/databases",     // Database › Clusters list (exists)
      "nodes":     "/nodes",         // Database › Nodes (ly-ae6.5)
      "databases": "/databases/all", // Database › Databases list (ly-ae6.5)
      // search vertical (ly-ae6.10)
      "searchdomains": "/search/domains",
      "searchnodes":   "/search/nodes",
      // cache vertical (ly-ae6.11)
      "cacheclusters":    "/cache/clusters",
      "cachereplicasets": "/cache/replicasets",
      "cachenodes":       "/cache/nodes",
      // scoped identity + capabilities
      "clusterdetail": "/cluster",      // scoped cluster overview (ly-ae6.6)
      "capabilities":  "/capabilities", // ly-ae6.6
      // queries
      "topqueries": "/",         // top-queries (exists at /)
      "insights":   "/insights", // exists
      "plans":      "/plan",     // exists
      // advisors (exist)
      "indexadvisor":  "/index-advisor",
      "vacuumadvisor": "/vacuum-advisor",
      "configadvisor": "/config-advisor",
      // activity
      "waits":       "/waits",       // exists
      "connections": "/connections", // SOON
      // console
      "console": "/console", // SQL Console T2 (ly-ae6.8)
      "scripts": "/scripts", // Saved Scripts (ly-ae6.9)
      // checks & alerts
      "checks": "/checks", // exists
      "alerts": "/alerts", // SOON
      // schema (all SOON)
      "inventory":   "/schema/inventory",
      "tablegrowth": "/schema/table-growth",
      "indexes":     "/schema/indexes",
      // logs (SOON)
      "loginsights": "/logs/insights",
  }

  // NavHref returns screen's canonical route with the current scope encoded as
  // discrete query params (see Scope.Query). ly-ae6.2's request resolver is the
  // inverse (ScopeFromValues).
  func NavHref(sc Scope, screen string) string {
      p, ok := screenPath[screen]
      if !ok {
          p = "/"
      }
      return p + "?" + sc.Query().Encode()
  }

  // isActive reports whether the nav item for `screen` should render active given
  // the shell's `active` screen id and the current scope level. It encodes the
  // exact match plus the prototype's detail-screen aliases (Lynceus.dc.html:2411):
  // querydetail→topqueries, scriptdetail→scripts, and (fleet only)
  // clusterdetail→clusters. An empty `active` highlights nothing.
  func isActive(screen, active string, lvl ScopeLevel) bool {
      if active == "" {
          return false
      }
      if screen == active {
          return true
      }
      switch {
      case screen == "topqueries" && active == "querydetail":
          return true
      case screen == "scripts" && active == "scriptdetail":
          return true
      case screen == "clusters" && active == "clusterdetail" && lvl == LevelFleet:
          return true
      }
      return false
  }
  ```
- [ ] **Step 4: Run it — expect PASS.**
  `go test ./web/ -run 'TestNavHref|TestNavItem|TestIsActive'` → `ok`.
- [ ] **Step 5: Commit.**
  `git add web/nav.go web/nav_href_test.go && git commit -m "ly-ae6.3: NavHref scoped-URL scheme + nav types + active-alias logic"`

---

### Task 3: BuildNav — per-scope nav tree engine + gating rules

The heart of this bead. Mirrors the prototype's `navDef` builder (`Lynceus.dc.html` lines 2366–2406) exactly, applying the three gating rules and the active aliases from `isActive`.

**Files**
- modify: `web/nav.go` (append `BuildNav` + the private `navItem` helper)
- create: `web/nav_build_test.go`

**Interfaces**
- Produces:
  ```go
  func BuildNav(sc Scope, eng EngineFlags, active string) []NavGroup
  ```
- Consumes: `Scope`, `EngineFlags` (Task 1); `NavItem`, `NavGroup`, `NavHref`, `isActive` (Task 2).

**Gating rules encoded (design README §Scope Model + PRODUCT_INTENT §1/§5/§6):**
1. Low-level sections (QUERIES, ADVISORS, ACTIVITY, CHECKS & ALERTS, SCHEMA, LOGS, capabilities) **never appear at fleet scope**.
2. **Saved Scripts** appears at every scope; **SQL Console** only at cluster/node/database scope.
3. Fleet verticals gate on `EngineFlags` (DATABASE if Postgres, SEARCH if Search, CACHE if Cache).

**Steps**

- [ ] **Step 1: Write the failing test.** Create `web/nav_build_test.go`:
  ```go
  package web

  import "testing"

  // helpers ------------------------------------------------------------------

  func groupLabels(gs []NavGroup) []string {
      out := make([]string, len(gs))
      for i, g := range gs {
          out[i] = g.Label
      }
      return out
  }

  func hasGroup(gs []NavGroup, label string) bool {
      for _, g := range gs {
          if g.Label == label {
              return true
          }
      }
      return false
  }

  func findGroup(gs []NavGroup, label string) (NavGroup, bool) {
      for _, g := range gs {
          if g.Label == label {
              return g, true
          }
      }
      return NavGroup{}, false
  }

  func hasScreen(gs []NavGroup, screen string) bool {
      for _, g := range gs {
          for _, it := range g.Items {
              if it.Screen == screen {
                  return true
              }
          }
      }
      return false
  }

  // fleet --------------------------------------------------------------------

  func TestBuildNav_FleetHidesLowLevelSections(t *testing.T) {
      gs := BuildNav(FleetScope(), EngineFlags{Postgres: true, Search: true, Cache: true}, "")
      for _, banned := range []string{"QUERIES", "ADVISORS", "ACTIVITY", "CHECKS & ALERTS", "SCHEMA", "LOGS"} {
          if hasGroup(gs, banned) {
              t.Errorf("fleet nav must not contain low-level group %q; groups = %v", banned, groupLabels(gs))
          }
      }
      if hasScreen(gs, "capabilities") {
          t.Error("fleet nav must not expose capabilities")
      }
      // CONSOLE at fleet is Saved Scripts only — no SQL Console.
      if hasScreen(gs, "console") {
          t.Error("fleet nav must not contain SQL Console (rule: cluster/node/db only)")
      }
      if !hasScreen(gs, "scripts") {
          t.Error("fleet nav must contain Saved Scripts (rule: every scope)")
      }
  }

  func TestBuildNav_FleetEngineGating(t *testing.T) {
      pgOnly := BuildNav(FleetScope(), EngineFlags{Postgres: true}, "")
      if !hasGroup(pgOnly, "DATABASE") || hasGroup(pgOnly, "SEARCH") || hasGroup(pgOnly, "CACHE") {
          t.Errorf("postgres-only fleet groups = %v, want DATABASE without SEARCH/CACHE", groupLabels(pgOnly))
      }
      all := BuildNav(FleetScope(), EngineFlags{Postgres: true, Search: true, Cache: true}, "")
      for _, want := range []string{"OVERVIEW", "DATABASE", "SEARCH", "CACHE", "CONSOLE"} {
          if !hasGroup(all, want) {
              t.Errorf("all-engines fleet missing group %q; groups = %v", want, groupLabels(all))
          }
      }
      none := BuildNav(FleetScope(), EngineFlags{}, "")
      if len(groupLabels(none)) != 2 || !hasGroup(none, "OVERVIEW") || !hasGroup(none, "CONSOLE") {
          t.Errorf("no-engines fleet groups = %v, want [OVERVIEW CONSOLE]", groupLabels(none))
      }
  }

  func TestBuildNav_FleetDatabaseSection(t *testing.T) {
      gs := BuildNav(FleetScope(), EngineFlags{Postgres: true}, "")
      g, ok := findGroup(gs, "DATABASE")
      if !ok {
          t.Fatal("no DATABASE group")
      }
      want := []string{"Clusters", "Nodes", "Databases"}
      if len(g.Items) != 3 {
          t.Fatalf("DATABASE items = %d, want 3", len(g.Items))
      }
      for i, w := range want {
          if g.Items[i].Label != w {
              t.Errorf("DATABASE item %d = %q, want %q", i, g.Items[i].Label, w)
          }
      }
  }

  // cluster ------------------------------------------------------------------

  func TestBuildNav_ClusterTree(t *testing.T) {
      sc := Scope{Level: LevelCluster, Cluster: "orders-prod"}
      gs := BuildNav(sc, DefaultEngines(), "")
      if gs[0].Label != "CLUSTER: ORDERS-PROD" {
          t.Errorf("identity header = %q, want CLUSTER: ORDERS-PROD", gs[0].Label)
      }
      for _, want := range []string{"QUERIES", "ADVISORS", "ACTIVITY", "CONSOLE", "CHECKS & ALERTS", "SCHEMA", "LOGS"} {
          if !hasGroup(gs, want) {
              t.Errorf("cluster nav missing group %q", want)
          }
      }
      // ADVISORS at cluster scope includes Config · per node.
      adv, _ := findGroup(gs, "ADVISORS")
      if len(adv.Items) != 3 || adv.Items[2].Label != "Config · per node" {
          t.Errorf("cluster ADVISORS = %+v, want Index/Vacuum/Config · per node", adv.Items)
      }
      // CONSOLE at cluster scope = SQL Console (T2) + Saved Scripts.
      con, _ := findGroup(gs, "CONSOLE")
      if len(con.Items) != 2 || con.Items[0].Label != "SQL Console" || !con.Items[0].T2 || con.Items[1].Label != "Saved Scripts" {
          t.Errorf("cluster CONSOLE = %+v, want SQL Console(T2)+Saved Scripts", con.Items)
      }
      // identity group has Overview/Nodes/Databases/Capabilities.
      if !hasScreen(gs, "clusterdetail") || !hasScreen(gs, "capabilities") {
          t.Error("cluster identity group missing Overview or Capabilities")
      }
  }

  // node ---------------------------------------------------------------------

  func TestBuildNav_NodeTree(t *testing.T) {
      sc := Scope{Level: LevelNode, Cluster: "orders-prod", Node: "srv-orders-1"}
      gs := BuildNav(sc, DefaultEngines(), "")
      if gs[0].Label != "NODE: SRV-ORDERS-1" {
          t.Errorf("identity header = %q", gs[0].Label)
      }
      // ADVISORS at node scope has NO Config item.
      adv, _ := findGroup(gs, "ADVISORS")
      if len(adv.Items) != 2 {
          t.Errorf("node ADVISORS items = %d, want 2 (Index, Vacuum)", len(adv.Items))
      }
      if hasGroup(gs, "SCHEMA") {
          t.Error("node nav must not have SCHEMA group")
      }
      if !hasGroup(gs, "LOGS") {
          t.Error("node nav must have LOGS group")
      }
      if !hasScreen(gs, "console") { // SQL Console valid at node scope
          t.Error("node nav must contain SQL Console")
      }
  }

  // pooler -------------------------------------------------------------------

  func TestBuildNav_PoolerTree(t *testing.T) {
      sc := Scope{Level: LevelPooler, Cluster: "orders-prod", Node: "pgbouncer-1"}
      gs := BuildNav(sc, DefaultEngines(), "")
      if gs[0].Label != "POOLER: PGBOUNCER-1" {
          t.Errorf("identity header = %q", gs[0].Label)
      }
      if hasScreen(gs, "console") {
          t.Error("pooler nav must NOT contain SQL Console (rule: cluster/node/db only)")
      }
      if !hasScreen(gs, "scripts") {
          t.Error("pooler nav must contain Saved Scripts (rule: every scope)")
      }
      if hasGroup(gs, "QUERIES") || hasGroup(gs, "ADVISORS") {
          t.Error("pooler nav has no QUERIES/ADVISORS groups")
      }
      act, ok := findGroup(gs, "ACTIVITY")
      if !ok || len(act.Items) != 1 || act.Items[0].Label != "Connections" {
          t.Errorf("pooler ACTIVITY = %+v, want single Connections", act.Items)
      }
  }

  // database -----------------------------------------------------------------

  func TestBuildNav_DatabaseTree(t *testing.T) {
      sc := Scope{Level: LevelDatabase, Cluster: "orders-prod", DB: "orders"}
      gs := BuildNav(sc, DefaultEngines(), "")
      if gs[0].Label != "DATABASE: ORDERS" {
          t.Errorf("identity header = %q", gs[0].Label)
      }
      if !hasScreen(gs, "console") {
          t.Error("database nav must contain SQL Console")
      }
      if hasGroup(gs, "ACTIVITY") || hasGroup(gs, "LOGS") {
          t.Error("database nav has no ACTIVITY/LOGS groups")
      }
      if !hasGroup(gs, "SCHEMA") {
          t.Error("database nav must have SCHEMA group")
      }
      // CHECKS & ALERTS at db scope = Checks only (no Alerts).
      chk, _ := findGroup(gs, "CHECKS & ALERTS")
      if len(chk.Items) != 1 || chk.Items[0].Label != "Checks" {
          t.Errorf("database CHECKS = %+v, want single Checks", chk.Items)
      }
  }

  // cross-cutting rules ------------------------------------------------------

  func TestBuildNav_SavedScriptsEverywhere(t *testing.T) {
      scopes := []Scope{
          FleetScope(),
          {Level: LevelCluster, Cluster: "c"},
          {Level: LevelNode, Cluster: "c", Node: "n"},
          {Level: LevelPooler, Cluster: "c", Node: "p"},
          {Level: LevelDatabase, Cluster: "c", DB: "d"},
      }
      for _, sc := range scopes {
          if !hasScreen(BuildNav(sc, DefaultEngines(), ""), "scripts") {
              t.Errorf("scope %s missing Saved Scripts", sc.Level)
          }
      }
  }

  func TestBuildNav_SQLConsoleOnlyClusterNodeDatabase(t *testing.T) {
      want := map[ScopeLevel]bool{
          LevelFleet: false, LevelCluster: true, LevelNode: true, LevelPooler: false, LevelDatabase: true,
      }
      scopes := map[ScopeLevel]Scope{
          LevelFleet:    FleetScope(),
          LevelCluster:  {Level: LevelCluster, Cluster: "c"},
          LevelNode:     {Level: LevelNode, Cluster: "c", Node: "n"},
          LevelPooler:   {Level: LevelPooler, Cluster: "c", Node: "p"},
          LevelDatabase: {Level: LevelDatabase, Cluster: "c", DB: "d"},
      }
      for lvl, sc := range scopes {
          if got := hasScreen(BuildNav(sc, DefaultEngines(), ""), "console"); got != want[lvl] {
              t.Errorf("SQL Console at %s = %v, want %v", lvl, got, want[lvl])
          }
      }
  }

  func TestBuildNav_ActiveMarksMatchingScreen(t *testing.T) {
      sc := Scope{Level: LevelCluster, Cluster: "orders-prod"}
      gs := BuildNav(sc, DefaultEngines(), "waits")
      var activeCount int
      for _, g := range gs {
          for _, it := range g.Items {
              if it.Active {
                  activeCount++
                  if it.Screen != "waits" {
                      t.Errorf("active item = %q, want waits", it.Screen)
                  }
              }
          }
      }
      if activeCount != 1 {
          t.Errorf("active items = %d, want exactly 1", activeCount)
      }
      // empty active highlights nothing.
      for _, g := range BuildNav(sc, DefaultEngines(), "") {
          for _, it := range g.Items {
              if it.Active {
                  t.Errorf("active=\"\" must highlight nothing; %q is active", it.Screen)
              }
          }
      }
  }

  func TestBuildNav_ActiveAliases(t *testing.T) {
      // querydetail highlights Top Queries at cluster scope.
      cl := Scope{Level: LevelCluster, Cluster: "c"}
      if !itemActive(BuildNav(cl, DefaultEngines(), "querydetail"), "topqueries") {
          t.Error("querydetail must highlight topqueries")
      }
      // scriptdetail highlights Saved Scripts (present at every scope; test pooler).
      pl := Scope{Level: LevelPooler, Cluster: "c", Node: "p"}
      if !itemActive(BuildNav(pl, DefaultEngines(), "scriptdetail"), "scripts") {
          t.Error("scriptdetail must highlight scripts")
      }
      // clusterdetail highlights the fleet Database › Clusters item, fleet only.
      if !itemActive(BuildNav(FleetScope(), DefaultEngines(), "clusterdetail"), "clusters") {
          t.Error("clusterdetail must highlight clusters at fleet scope")
      }
  }

  // itemActive reports whether the item for `screen` is marked active.
  func itemActive(gs []NavGroup, screen string) bool {
      for _, g := range gs {
          for _, it := range g.Items {
              if it.Screen == screen {
                  return it.Active
              }
          }
      }
      return false
  }

  func TestBuildNav_HrefsCarryScope(t *testing.T) {
      sc := Scope{Level: LevelCluster, Cluster: "orders-prod"}
      gs := BuildNav(sc, DefaultEngines(), "")
      for _, g := range gs {
          for _, it := range g.Items {
              if it.Href != NavHref(sc, it.Screen) {
                  t.Errorf("item %q href = %q, want %q", it.Screen, it.Href, NavHref(sc, it.Screen))
              }
          }
      }
  }
  ```
- [ ] **Step 2: Run it — expect FAIL (undefined `BuildNav`).**
  `go test ./web/ -run TestBuildNav` → build fails: `undefined: BuildNav`.
- [ ] **Step 3: Implement.** Append to `web/nav.go`:
  ```go
  // navItem builds a NavItem, resolving its href and active state (via isActive,
  // including the prototype's detail-screen aliases) and applying "soon"/"t2".
  func navItem(sc Scope, active, label, screen string, flags ...string) NavItem {
      it := NavItem{
          Label:  label,
          Screen: screen,
          Href:   NavHref(sc, screen),
          Active: isActive(screen, active, sc.Level),
      }
      for _, f := range flags {
          switch f {
          case "soon":
              it.Soon = true
          case "t2":
              it.T2 = true
          }
      }
      return it
  }

  // BuildNav returns the sidebar nav tree for the current scope. It mirrors the
  // prototype's navDef builder (docs/design/Lynceus.dc.html:2366-2406) and
  // enforces the three gating rules: low-level sections never appear at fleet;
  // Saved Scripts at every scope; SQL Console only at cluster/node/database scope.
  func BuildNav(sc Scope, eng EngineFlags, active string) []NavGroup {
      itm := func(label, screen string, flags ...string) NavItem {
          return navItem(sc, active, label, screen, flags...)
      }

      // shared low-level groups (never used at fleet scope)
      queries := NavGroup{Label: "QUERIES", Items: []NavItem{
          itm("Top Queries", "topqueries"),
          itm("Query Insights", "insights"),
          itm("Plans", "plans"),
      }}
      advisors := func(configLabel string) NavGroup {
          items := []NavItem{itm("Index", "indexadvisor"), itm("Vacuum", "vacuumadvisor")}
          if configLabel != "" {
              items = append(items, itm(configLabel, "configadvisor"))
          }
          return NavGroup{Label: "ADVISORS", Items: items}
      }
      activityFull := NavGroup{Label: "ACTIVITY", Items: []NavItem{
          itm("Wait Events", "waits"),
          itm("Connections", "connections", "soon", "t2"),
      }}
      consoleFull := NavGroup{Label: "CONSOLE", Items: []NavItem{
          itm("SQL Console", "console", "t2"),
          itm("Saved Scripts", "scripts"),
      }}
      consoleScriptsOnly := NavGroup{Label: "CONSOLE", Items: []NavItem{
          itm("Saved Scripts", "scripts"),
      }}
      checksFull := NavGroup{Label: "CHECKS & ALERTS", Items: []NavItem{
          itm("Checks", "checks"),
          itm("Alerts", "alerts", "soon"),
      }}
      schema := NavGroup{Label: "SCHEMA", Items: []NavItem{
          itm("Inventory", "inventory", "soon"),
          itm("Table Growth", "tablegrowth", "soon"),
          itm("Indexes", "indexes", "soon"),
      }}
      logs := NavGroup{Label: "LOGS", Items: []NavItem{
          itm("Log Insights", "loginsights", "soon"),
      }}

      switch sc.Level {
      case LevelCluster:
          return []NavGroup{
              {Label: sc.HeaderLabel(), Items: []NavItem{
                  itm("Overview", "clusterdetail"),
                  itm("Nodes", "nodes"),
                  itm("Databases", "databases"),
                  itm("Capabilities", "capabilities"),
              }},
              queries,
              advisors("Config · per node"),
              activityFull, consoleFull, checksFull, schema, logs,
          }
      case LevelNode:
          return []NavGroup{
              {Label: sc.HeaderLabel(), Items: []NavItem{
                  itm("Overview", "nodes"),
                  itm("Config", "configadvisor"),
                  itm("Capabilities", "capabilities"),
              }},
              queries,
              advisors(""),
              activityFull, consoleFull, checksFull, logs,
          }
      case LevelPooler:
          return []NavGroup{
              {Label: sc.HeaderLabel(), Items: []NavItem{
                  itm("Overview", "nodes"),
                  itm("Config · pgbouncer", "configadvisor"),
              }},
              {Label: "ACTIVITY", Items: []NavItem{itm("Connections", "connections", "soon", "t2")}},
              consoleScriptsOnly, checksFull, logs,
          }
      case LevelDatabase:
          return []NavGroup{
              {Label: sc.HeaderLabel(), Items: []NavItem{
                  itm("Overview", "databases"),
                  itm("Capabilities", "capabilities"),
              }},
              queries,
              advisors(""),
              consoleFull,
              {Label: "CHECKS & ALERTS", Items: []NavItem{itm("Checks", "checks")}},
              schema,
          }
      default: // LevelFleet — low-level sections suppressed; verticals gate on engines
          groups := []NavGroup{
              {Label: "OVERVIEW", Items: []NavItem{itm("Fleet", "fleet")}},
          }
          if eng.Postgres {
              groups = append(groups, NavGroup{Label: "DATABASE", Items: []NavItem{
                  itm("Clusters", "clusters"),
                  itm("Nodes", "nodes"),
                  itm("Databases", "databases"),
              }})
          }
          if eng.Search {
              groups = append(groups, NavGroup{Label: "SEARCH", Items: []NavItem{
                  itm("Domains", "searchdomains"),
                  itm("Nodes", "searchnodes"),
              }})
          }
          if eng.Cache {
              groups = append(groups, NavGroup{Label: "CACHE", Items: []NavItem{
                  itm("Clusters", "cacheclusters"),
                  itm("Replicasets", "cachereplicasets"),
                  itm("Nodes", "cachenodes"),
              }})
          }
          return append(groups, consoleScriptsOnly)
      }
  }
  ```
- [ ] **Step 4: Run it — expect PASS.**
  `go test ./web/ -run TestBuildNav` → `ok`. Then full package: `go test ./web/` → `ok`.
- [ ] **Step 5: Commit.**
  `git add web/nav.go web/nav_build_test.go && git commit -m "ly-ae6.3: BuildNav per-scope tree engine + gating rules + active aliases"`

---

### Task 4: Sidebar token stylesheet (208px mono rail) + legacy-bleed guard

The design-system styling for the rail: 208px JetBrains-Mono rail, faint letter-spaced headers, active accent-text + tinted-bg + 2px right border, SOON/T2 badges. Tokens only. **Also neutralizes the legacy.css bare-element `nav`/`nav a` rules that would otherwise leak onto the sidebar anchors** (resolves review finding #3).

**Legacy-bleed analysis (why the guard is required):** `web/static/css/legacy.css` contains bare-element rules `nav a { color: var(--acc); text-decoration: none; margin-right: 1rem; font-size: 0.9rem; }` and `nav { margin-bottom: 0.5rem; }`. The new Sidebar is `<nav class="ln-nav"> … <a class="ln-nav-item">`. A bare `nav a` selector (specificity 0,0,2) matches those anchors. `.ln-nav-item` (0,1,0) overrides `color` but, unless `nav.css` sets them, does **not** override `font-size` (the legacy `0.9rem`/14.4px would win over the rail's intended 12px because a rule targeting the anchor directly beats the container's inherited `font-size`) and lets a stray `margin-right: 1rem` leak. The guard below sets `font-size` on `.ln-nav-item` (class beats bare elements) and `margin: 0` on `.ln-nav a` (specificity 0,1,1 beats `nav a`'s 0,0,2). A string-content test asserts the guard is present.

**Files**
- create: `web/static/css/nav.css`
- create: `web/nav_css_test.go` (served + tokens-only + bleed-guard assertions)

**Interfaces**
- Produces: `/static/css/nav.css` (served automatically by the existing `//go:embed static` in `web/static.go` — no handler change).
- Consumes: tokens from `web/static/css/tokens.css` (`--rail --line --faint --mut --dim --text --acc --acc2 --accbg --warn --warnT --font-mono --border --radius-badge`, all confirmed present).

**Steps**

- [ ] **Step 1: Write the failing test.** Create `web/nav_css_test.go`:
  ```go
  package web

  import (
      "net/http"
      "net/http/httptest"
      "strings"
      "testing"
  )

  func navCSSBody(t *testing.T) string {
      t.Helper()
      req := httptest.NewRequest(http.MethodGet, "/static/css/nav.css", nil)
      rec := httptest.NewRecorder()
      StaticHandler().ServeHTTP(rec, req)
      if rec.Code != http.StatusOK {
          t.Fatalf("GET nav.css = %d, want 200", rec.Code)
      }
      return rec.Body.String()
  }

  func TestStaticHandler_ServesNavCSS(t *testing.T) {
      body := navCSSBody(t)
      for _, want := range []string{
          ".ln-nav",              // rail container
          ".ln-nav-head",         // section header
          ".ln-nav-item--active", // active item
          ".ln-nav-badge",        // SOON/T2 badge
          "width: 208px",         // design rail width
          "var(--font-mono)",     // mono font
          "var(--accbg)",         // active tinted bg
      } {
          if !strings.Contains(body, want) {
              t.Errorf("nav.css missing %q", want)
          }
      }
  }

  func TestNavCSS_TokensNotHardcoded(t *testing.T) {
      body := navCSSBody(t)
      for _, banned := range []string{"#2b6cb0", "system-ui", "#0c1118", "#2dd4bf"} {
          if strings.Contains(body, banned) {
              t.Errorf("nav.css hardcodes %q — use design tokens (var(--x))", banned)
          }
      }
  }

  // Guards against legacy.css bare 'nav a'/'nav' rules leaking font-size:0.9rem
  // and margin-right:1rem onto the sidebar anchors (see Task 4 analysis).
  func TestNavCSS_NeutralizesLegacyNavBleed(t *testing.T) {
      body := navCSSBody(t)
      for _, want := range []string{
          ".ln-nav a",     // anchor-level reset that outranks bare `nav a`
          "font-size: 12px", // explicit item font-size (not the legacy 0.9rem)
          "margin: 0",       // neutralize legacy nav/nav a margins
      } {
          if !strings.Contains(body, want) {
              t.Errorf("nav.css missing legacy-bleed guard %q", want)
          }
      }
  }
  ```
- [ ] **Step 2: Run it — expect FAIL (404, nav.css does not exist).**
  `go test ./web/ -run 'TestStaticHandler_ServesNavCSS|TestNavCSS'` → fail: `GET nav.css = 404`.
- [ ] **Step 3: Implement.** Create `web/static/css/nav.css`:
  ```css
  /* Scope-driven sidebar nav (ly-ae6.3). Tokens only — no hardcoded colors.
     Values mirror docs/design/Lynceus.dc.html sidebar markup. */

  .ln-shell { display: flex; min-height: 100vh; }
  .ln-main { flex: 1; min-width: 0; overflow-y: auto; }

  .ln-nav {
    width: 208px;
    flex-shrink: 0;
    box-sizing: border-box;
    margin: 0; /* legacy-bleed guard: outrank `nav { margin-bottom:.5rem }` */
    border-right: var(--border) solid var(--line);
    background: var(--rail);
    padding: 12px 0 20px;
    overflow-y: auto;
    font-family: var(--font-mono);
    font-size: 12px;
  }
  /* legacy-bleed guard: `.ln-nav a` (0,1,1) outranks bare `nav a` (0,0,2), so the
     legacy `margin-right:1rem` cannot leak onto sidebar anchors. */
  .ln-nav a { margin: 0; }

  .ln-nav-group { display: flex; flex-direction: column; }

  .ln-nav-head {
    padding: 12px 14px 5px;
    font-size: 10px;
    letter-spacing: .12em;
    color: var(--faint);
  }

  .ln-nav-item {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: 6px;
    padding: 5px 14px;
    font-size: 12px; /* legacy-bleed guard: outrank `nav a { font-size:.9rem }` */
    color: var(--mut);
    text-decoration: none;
    border-right: 2px solid transparent;
  }
  .ln-nav-item:hover { color: var(--text); }
  .ln-nav-item--soon { color: var(--dim); }
  .ln-nav-item--active {
    color: var(--acc2);
    background: var(--accbg);
    border-right-color: var(--acc);
  }

  .ln-nav-badge {
    font-size: 9px;
    padding: 0 4px;
    border-radius: var(--radius-badge);
    border: var(--border) solid var(--line);
    color: var(--faint);
  }
  .ln-nav-badge--t2 { border-color: var(--warn); color: var(--warnT); }
  ```
- [ ] **Step 4: Run it — expect PASS.**
  `go test ./web/ -run 'TestStaticHandler_ServesNavCSS|TestNavCSS'` → `ok`.
- [ ] **Step 5: Commit.**
  `git add web/static/css/nav.css web/nav_css_test.go && git commit -m "ly-ae6.3: token-styled 208px sidebar rail + legacy-bleed guard"`

---

### Task 5: Sidebar templ component

Renders `BuildNav` into the rail markup. templ regeneration required.

**Files**
- create: `web/nav_sidebar.templ`
- generated (commit alongside): `web/nav_sidebar_templ.go`
- create: `web/nav_sidebar_test.go`

**Interfaces**
- Produces:
  ```go
  templ Sidebar(sc Scope, eng EngineFlags, active string)
  ```
- Consumes: `BuildNav` (Task 3), `NavItem.ItemClass` (Task 2), `templ.SafeURL`.

**Steps**

- [ ] **Step 1: Write the failing test.** Create `web/nav_sidebar_test.go`:
  ```go
  package web

  import (
      "context"
      "strings"
      "testing"
  )

  func renderSidebar(t *testing.T, sc Scope, eng EngineFlags, active string) string {
      t.Helper()
      var sb strings.Builder
      if err := Sidebar(sc, eng, active).Render(context.Background(), &sb); err != nil {
          t.Fatalf("render sidebar: %v", err)
      }
      return sb.String()
  }

  func TestSidebar_FleetRendersVerticalGroupsAndScriptsOnly(t *testing.T) {
      html := renderSidebar(t, FleetScope(), DefaultEngines(), "")
      for _, want := range []string{`class="ln-nav"`, `class="ln-nav-head"`, ">OVERVIEW<", ">DATABASE<", ">CONSOLE<", ">Saved Scripts<", ">Clusters<"} {
          if !strings.Contains(html, want) {
              t.Errorf("fleet sidebar missing %q", want)
          }
      }
      // low-level + SQL Console suppressed at fleet
      for _, banned := range []string{">QUERIES<", ">SQL Console<", ">Wait Events<"} {
          if strings.Contains(html, banned) {
              t.Errorf("fleet sidebar must not contain %q", banned)
          }
      }
  }

  func TestSidebar_ClusterActiveItemGetsActiveClassAndScopedHref(t *testing.T) {
      sc := Scope{Level: LevelCluster, Cluster: "orders-prod"}
      html := renderSidebar(t, sc, DefaultEngines(), "waits")
      if !strings.Contains(html, "ln-nav-item--active") {
          t.Error("active cluster item missing ln-nav-item--active class")
      }
      // Robust href assertion (prefix): templ HTML-escapes '&' → '&amp;' in the
      // attribute, so assert the scope-carrying prefix rather than the full string.
      if !strings.Contains(html, `href="/waits?cluster=orders-prod`) {
          t.Error("Wait Events link missing scope-carrying href")
      }
      if !strings.Contains(html, "CLUSTER: ORDERS-PROD") {
          t.Error("cluster identity header not rendered")
      }
  }

  func TestSidebar_BadgesRendered(t *testing.T) {
      html := renderSidebar(t, Scope{Level: LevelCluster, Cluster: "c"}, DefaultEngines(), "")
      if !strings.Contains(html, "ln-nav-badge--t2") || !strings.Contains(html, ">T2<") {
          t.Error("cluster sidebar missing T2 badge on SQL Console")
      }
      if !strings.Contains(html, ">SOON<") {
          t.Error("cluster sidebar missing SOON badge (Alerts/Connections)")
      }
  }

  func TestSidebar_NoHardcodedColors(t *testing.T) {
      html := renderSidebar(t, FleetScope(), DefaultEngines(), "")
      for _, banned := range []string{"#2b6cb0", "system-ui"} {
          if strings.Contains(html, banned) {
              t.Errorf("sidebar output contains hardcoded %q — styling must be class/token based", banned)
          }
      }
  }
  ```
- [ ] **Step 2: Run it — expect FAIL (undefined `Sidebar`).**
  `go test ./web/ -run TestSidebar` → build fails: `undefined: Sidebar`.
- [ ] **Step 3: Implement.** Create `web/nav_sidebar.templ`:
  ```go
  package web

  // Sidebar renders the scope-driven navigation rail. The nav tree is a pure
  // function of scope (BuildNav); a shell (ly-ae6.2) supplies sc/eng/active —
  // typically via SidebarFromContext (see nav_context.go).
  templ Sidebar(sc Scope, eng EngineFlags, active string) {
      <nav class="ln-nav" aria-label="Scope navigation">
          for _, g := range BuildNav(sc, eng, active) {
              <div class="ln-nav-group">
                  <div class="ln-nav-head">{ g.Label }</div>
                  for _, it := range g.Items {
                      <a class={ it.ItemClass() } href={ templ.SafeURL(it.Href) }>
                          <span>{ it.Label }</span>
                          if it.Soon {
                              <span class="ln-nav-badge">SOON</span>
                          }
                          if it.T2 {
                              <span class="ln-nav-badge ln-nav-badge--t2">T2</span>
                          }
                      </a>
                  }
              </div>
          }
      </nav>
  }
  ```
- [ ] **Step 4: Regenerate templ.**
  `make templ` → produces `web/nav_sidebar_templ.go`. Then `go build ./...` → succeeds.
- [ ] **Step 5: Run it — expect PASS.**
  `go test ./web/ -run TestSidebar` → `ok`.
- [ ] **Step 6: Commit.**
  `git add web/nav_sidebar.templ web/nav_sidebar_templ.go web/nav_sidebar_test.go && git commit -m "ly-ae6.3: Sidebar templ renders scope-driven nav rail"`

---

### Task 6: NavState context seam + SidebarFromContext (the shell integration point)

The single seam a shell threads a resolved scope through. `ly-ae6.2`'s middleware/handlers call `WithNavState`; the shell renders `@SidebarFromContext()`, which reads the scope back at render time and falls back to fleet when absent. This is the mechanism that makes the eventual live mount (Task 7's filed bead) *thread the real resolved scope*, not a hardcoded one — resolving the review's "hardcoded fleet" finding at the seam level.

**Files**
- create: `web/nav_context.go`
- create: `web/nav_context_test.go`

**Interfaces**
- Produces:
  ```go
  type NavState struct { Scope Scope; Engines EngineFlags; Active string }
  func WithNavState(ctx context.Context, ns NavState) context.Context
  func NavStateFromContext(ctx context.Context) NavState // {FleetScope(), DefaultEngines(), ""} if absent
  func SidebarFromContext() templ.Component               // reads NavState off ctx at Render time
  ```
- Consumes: `Scope`, `EngineFlags`, `FleetScope`, `DefaultEngines` (Task 1); `Sidebar` (Task 5); `github.com/a-h/templ`.

**Steps**

- [ ] **Step 1: Write the failing test.** Create `web/nav_context_test.go`:
  ```go
  package web

  import (
      "context"
      "strings"
      "testing"
  )

  func renderFromContext(t *testing.T, ctx context.Context) string {
      t.Helper()
      var sb strings.Builder
      if err := SidebarFromContext().Render(ctx, &sb); err != nil {
          t.Fatalf("render: %v", err)
      }
      return sb.String()
  }

  func TestNavStateFromContext_DefaultsToFleet(t *testing.T) {
      ns := NavStateFromContext(context.Background())
      if ns.Scope.Level != LevelFleet || !ns.Engines.Postgres {
          t.Errorf("default NavState = %+v, want fleet scope + DefaultEngines", ns)
      }
  }

  func TestSidebarFromContext_DefaultRendersFleet(t *testing.T) {
      html := renderFromContext(t, context.Background())
      if !strings.Contains(html, ">OVERVIEW<") {
          t.Error("no-NavState context must render the fleet tree (OVERVIEW group)")
      }
      if strings.Contains(html, "CLUSTER:") {
          t.Error("no-NavState context must not render a scoped identity header")
      }
  }

  func TestSidebarFromContext_ThreadsResolvedScope(t *testing.T) {
      ctx := WithNavState(context.Background(), NavState{
          Scope:   Scope{Level: LevelCluster, Cluster: "orders-prod"},
          Engines: DefaultEngines(),
          Active:  "waits",
      })
      html := renderFromContext(t, ctx)
      if !strings.Contains(html, "CLUSTER: ORDERS-PROD") {
          t.Error("SidebarFromContext must render the scope put on the context (not hardcoded fleet)")
      }
      if !strings.Contains(html, "ln-nav-item--active") {
          t.Error("SidebarFromContext must thread the active screen from context")
      }
  }
  ```
- [ ] **Step 2: Run it — expect FAIL (undefined `NavState`/`WithNavState`/`SidebarFromContext`).**
  `go test ./web/ -run 'TestNavState|TestSidebarFromContext'` → build fails.
- [ ] **Step 3: Implement.** Create `web/nav_context.go`:
  ```go
  package web

  import (
      "context"
      "io"

      "github.com/a-h/templ"
  )

  // navStateKey is the private context key under which a shell (ly-ae6.2) stores
  // the resolved NavState for the current request.
  type navStateKey struct{}

  // NavState is everything the sidebar needs, resolved per request by the shell:
  // the current scope, which engines are enabled, and the active screen id.
  type NavState struct {
      Scope   Scope
      Engines EngineFlags
      Active  string
  }

  // WithNavState returns a context carrying ns. ly-ae6.2's resolver/middleware
  // (or a page handler that knows its own scope) calls this before rendering.
  func WithNavState(ctx context.Context, ns NavState) context.Context {
      return context.WithValue(ctx, navStateKey{}, ns)
  }

  // NavStateFromContext returns the NavState placed on ctx, or a safe default
  // (fleet scope, DefaultEngines, no active) when none is present — so the
  // sidebar renders correctly even before ly-ae6.2's middleware exists.
  func NavStateFromContext(ctx context.Context) NavState {
      if ns, ok := ctx.Value(navStateKey{}).(NavState); ok {
          return ns
      }
      return NavState{Scope: FleetScope(), Engines: DefaultEngines()}
  }

  // SidebarFromContext renders the sidebar for the NavState on the render-time
  // context. This is the one-liner a shell mounts: `@web.SidebarFromContext()`.
  // It reads the context at Render time (not construction), so the resolved
  // scope threads through correctly.
  func SidebarFromContext() templ.Component {
      return templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
          ns := NavStateFromContext(ctx)
          return Sidebar(ns.Scope, ns.Engines, ns.Active).Render(ctx, w)
      })
  }
  ```
- [ ] **Step 4: Run it — expect PASS.**
  `go test ./web/ -run 'TestNavState|TestSidebarFromContext'` → `ok`. Then `go build ./...` → succeeds.
- [ ] **Step 5: Commit.**
  `git add web/nav_context.go web/nav_context_test.go && git commit -m "ly-ae6.3: NavState context seam + SidebarFromContext shell mount point"`

---

### Task 7: Link the rail stylesheet + file the deferred live-mount bead

The one safe live change (link the served-but-unused `nav.css`), plus filing the concrete follow-up bead that performs the live mount + retires the flat nav / legacy `ClusterSidebar`. This is the plan's answer to the review's "weak interim end-state + un-tracked double-rail" finding: the double-rail/mount is **not attempted here** (it would regress navigation before `ly-ae6.2`'s resolver + scoped routes exist) and is **tracked by a filed bead**, not prose.

**Why linking `nav.css` now is safe:** the `.ln-*` selectors match nothing currently rendered (no page emits `.ln-nav` markup yet), so linking the stylesheet is a no-op visually and introduces no external host and no hardcoded hex — `TestLayout_NoExternalHosts` / `TestLayout_NoInlineStyleBlock` stay green. It pre-stages the styling so the mount bead only edits markup.

**Files**
- modify: `web/layout.templ` (add one `<link>` in `<head>`)
- generated (commit alongside): `web/layout_templ.go`
- modify: `web/layout_test.go` (one assertion)

**Steps**

- [ ] **Step 1: Write the failing test.** Append to `web/layout_test.go`:
  ```go
  func TestLayout_LinksNavCSS(t *testing.T) {
      html := renderLayout(t)
      if !strings.Contains(html, `href="/static/css/nav.css"`) {
          t.Error("layout must link the self-hosted sidebar stylesheet")
      }
  }
  ```
- [ ] **Step 2: Run it — expect FAIL.**
  `go test ./web/ -run TestLayout_LinksNavCSS` → fail (nav.css link absent).
- [ ] **Step 3: Implement.** Edit `web/layout.templ` — add the stylesheet link in `<head>` immediately after the legacy.css link (leave the flat `<nav>` and everything else untouched):
  ```go
  <link rel="stylesheet" href="/static/css/legacy.css"/>
  <link rel="stylesheet" href="/static/css/nav.css"/>
  ```
- [ ] **Step 4: Regenerate templ + build.**
  `make templ` → updates `web/layout_templ.go`. `go build ./...` → succeeds.
- [ ] **Step 5: Run the whole package — expect PASS.**
  `go test ./web/` → `ok` (new assertion passes; `TestLayout_NoExternalHosts`, `TestLayout_SelfHostedAssets`, `TestLayout_DarkDefaultAndBootstrap`, `TestLayout_NoInlineStyleBlock` all still green — nav.css adds no external host, no hardcoded hex, and the flat nav is unchanged so no existing layout assertion moves).
- [ ] **Step 6: Commit.**
  `git add web/layout.templ web/layout_templ.go web/layout_test.go && git commit -m "ly-ae6.3: link scope-rail stylesheet (mount deferred to shell)"`
- [ ] **Step 7: File the deferred live-mount bead (tracks the double-rail; not prose).** Once bd writes are unblocked (the local beads DB has pending schema migrations per `docs/design/README.md`'s Blocker note — see Task 8), run:
  ```bash
  bd create "Mount scope Sidebar into app shell + retire flat nav / legacy ClusterSidebar" \
    --type task -p 1 \
    -d "Depends on ly-ae6.2 (request→Scope resolver) + the scoped destination screens. Replace web/layout.templ's flat <nav> with @web.SidebarFromContext(); populate web.NavState via web.WithNavState in the resolver/handlers; migrate web/overview.templ + web/cluster_views.templ off the bespoke cluster-shell + ClusterSidebar onto the single Layout rail (fixes the transient double-rail); delete the orphaned ClusterSidebar/sidebarLink templ + legacy .sidebar/.sidebar-link CSS; update internal/api cluster tests to assert the grouped ln-nav markup. Must NOT land before ly-ae6.2's resolver, or cluster-tree links resolve to unscoped fleet data."
  bd dep add <new-id> ly-ae6.2   # blocked by the resolver bead
  bd label add <new-id> needs-plan
  ```
  If bd writes are still blocked, append this exact `bd create`/`bd dep`/`bd label` block to `docs/design/parity-beads.sh` (the ready-to-run script referenced in `docs/design/README.md`) instead, and report it in the handoff. Do **not** leave the mount as prose only.

---

### Task 8: Full-suite verification + bead handoff

- [ ] **Step 1: Full build + test.**
  `go build ./... && go test ./...` → all green. (No DB needed for this bead's tests; no other package is touched — `internal/api` cluster page tests and `web/overview.templ`/`web/cluster_views.templ` are deliberately untouched, so they stay green.)
- [ ] **Step 2: Confirm templ is in sync (CI gate).**
  `make templ && git diff --exit-code -- 'web/*_templ.go'` → no diff (generated files committed).
- [ ] **Step 3: Verify HEAD before any push.**
  `git branch --show-current` → the `ui-scope-nav-*` branch created in Branch setup. Do **not** push or open a PR unless the user asks (conservative git profile). Report the branch, changed files, and validation to the user.
- [ ] **Step 4: Bead bookkeeping.** After the user approves the plan landing:
  `bd label remove ly-ae6.3 needs-plan && bd label add ly-ae6.3 ready-impl` (only once this plan file is committed; see Feature Work Lifecycle in CLAUDE.md). Record next-worker context with `bd note ly-ae6.3 "…"`, including the deferred live-mount bead id from Task 7. If bd writes are blocked, report the exact commands + the blocker per the Session Completion protocol.

---

## Self-Review

### Spec-coverage checklist — every COMPARISON `sidebar-nav` gap → task

Source: `docs/design/COMPARISON.md` §"Sidebar + scope-driven nav rebuild" (lines 116–126).

| # | COMPARISON gap | Covered by | Live on a route in this bead? |
|---|---|---|---|
| 1 | No scope-driven sidebar engine (flat static list to every page) | Task 3 `BuildNav`; Task 5 `Sidebar`; Task 6 `SidebarFromContext` seam | **Engine + component: yes.** The live *flip* (removing `layout.templ`'s flat `<nav>`) is deferred to the Task 7-filed mount bead — see "Deferred-mount rationale" below. |
| 2 | Fleet-scope nav tree absent (OVERVIEW + DATABASE/SEARCH/CACHE + CONSOLE scripts-only; low-level suppressed) | Task 3 fleet case + `TestBuildNav_FleetHidesLowLevelSections`, `TestBuildNav_FleetDatabaseSection`, `TestBuildNav_FleetEngineGating`; Task 5 render `TestSidebar_FleetRendersVerticalGroupsAndScriptsOnly` | Rendered by `Sidebar`/`SidebarFromContext` (default) — proven by render tests. |
| 3 | Cluster-scope tree is a stub (needs grouped tree with Config·per node, CONSOLE, CHECKS, SCHEMA, LOGS) | Task 3 cluster case + `TestBuildNav_ClusterTree`; Task 6 `TestSidebarFromContext_ThreadsResolvedScope` renders the full cluster tree from a context scope | **Grouped tree built + rendered from a resolved cluster scope (tested).** The mount that swaps the legacy `ClusterSidebar` on the live `/databases/{id}` routes is the Task 7 bead (deferred to avoid the link regression below). This is the exact finding the reviewer flagged; it is now tracked by a filed bead, not prose. |
| 4 | Node/Pooler/Database trees do not exist | Task 3 node/pooler/database cases + `TestBuildNav_NodeTree/PoolerTree/DatabaseTree` | Built + render-tested. No live routes exist for these scopes yet (they are `ly-ae6.5`/G), so there is nothing to mount them onto — expected. |
| 5 | Nav gating rules unimplemented (Saved Scripts everywhere, SQL Console entry, low-level hidden at fleet) | Task 3 gating + `TestBuildNav_SavedScriptsEverywhere`, `TestBuildNav_SQLConsoleOnlyClusterNodeDatabase`, `TestBuildNav_FleetHidesLowLevelSections` | Enforced in `BuildNav` — proven by tests. |
| 6 | Sidebar design-system styling absent (208px mono rail, faint headers, SOON/T2 badges, accent+tinted-bg+2px active) | Task 4 `nav.css` (+ legacy-bleed guard) + `TestStaticHandler_ServesNavCSS`/`TestNavCSS_NeutralizesLegacyNavBleed`; Task 5 badge/active rendering + `TestSidebar_BadgesRendered`, `TestSidebar_ClusterActiveItemGetsActiveClassAndScopedHref` | Stylesheet served + linked (Task 7); applies once the mount bead emits `.ln-*` markup. |
| 7 | Per-engine section visibility (SEARCH if ES\|\|OS, CACHE if Redis\|\|Valkey) has no nav wiring | Task 1 `EngineFlags` (pairs pre-collapsed by caller per PRODUCT_INTENT §5); Task 3 fleet gating + `TestBuildNav_FleetEngineGating` | Enforced in `BuildNav` — proven by tests. |

**Deferred-mount rationale (honest end-state; resolves review findings #2 and coverage gap #3).** The bead's *engine, component, styling, and integration seam* are complete and fully tested. The **live flip** — replacing the flat `<nav>` and the legacy `ClusterSidebar` with `@SidebarFromContext()` — is intentionally **not** performed in this bead and is tracked by the Task 7 bead, for two concrete reasons the reviewer identified:
1. **Link regression.** The scope tree's links are flat routes carrying scope in the query string (`/waits?cluster=…&scope=cluster`). Until `ly-ae6.2`'s resolver reads those params **and** the scoped destination screens exist, clicking them lands on *unscoped fleet data* — a functional regression versus the current working `/databases/{id}/…` cluster sub-pages. So the flip must follow `ly-ae6.2` + the destination beads.
2. **Double-rail.** Mounting a rail in the global `Layout` while `web/overview.templ`/`web/cluster_views.templ` still embed their own `cluster-shell` + `ClusterSidebar` renders **two** rails on cluster pages. Avoiding that requires migrating those pages off their bespoke rail — which triggers (1). The migration + rail deletion + `internal/api` test updates are scoped into the Task 7 bead.
This matches the reviewer's endorsed path ("gate Task 6's live-wiring behind `ly-ae6.2`, and file a bead for the transient double-rail rather than tracking it only in prose"). The `NavState` seam (Task 6) is the mechanism that lets that bead thread the **real resolved** scope+active rather than a hardcoded fleet — so the flip, when it lands, is regression-free and never hardcoded.

Bead `ly-ae6.3` description criteria: "Rebuild the sidebar per scope level" → Task 3/5/6; "low-level sections never at fleet scope" → gap 5; "Saved Scripts everywhere, SQL Console only cluster/node/db" → gap 5 tests. All satisfied at the engine/component level (no acceptance criterion depends on the deferred live flip). No backend bead required (COMPARISON: none).

Integration-contract coverage: dependency on `ly-ae6.2` is stated with **definitive per-symbol ownership** of `web/scope.go` (Dependencies section + Task 1 Step 0 branch). The seam is `@web.SidebarFromContext()` fed by `web.WithNavState`, and the URL codec is `Scope.Query()` ⇄ `ScopeFromValues` (round-trip-tested, injective for names with `/`/`:`) — `ly-ae6.2`'s resolver is exactly `ScopeFromValues(r.URL.Query())`.

### Adversarial-review findings → resolution

| Review finding | Resolution |
|---|---|
| **Coverage gap #3** — cluster tree renders on no real route; double-rail; retrofit only in prose, no bead | Cluster tree now render-tested from a resolved scope (Task 6 `TestSidebarFromContext_ThreadsResolvedScope`); live mount + `ClusterSidebar` retirement + double-rail removal filed as a concrete bead (Task 7 Step 7), not prose. Deferred-mount rationale documents why the flip must follow `ly-ae6.2`. |
| **#1 Ownership inversion** on `web/scope.go` | Definitive per-symbol ownership stated (types → `ly-ae6.2`; codec/methods/seam → `ly-ae6.3`). Task 1 Step 0 is a concrete `test -f`/extend/create/STOP-and-reconcile branch, not a note. |
| **#2 Weak interim end-state + un-tracked double-rail** | Live flip removed from this bead; `NavState` seam threads real resolved scope (never hardcoded fleet); double-rail tracked by the Task 7 bead. Only safe live change is the `nav.css` `<link>`. |
| **#3 Legacy CSS bleed** (`nav a`/`nav`) onto `.ln-nav`/`.ln-nav-item` | `nav.css` sets explicit `font-size: 12px` on `.ln-nav-item` and `margin: 0` on `.ln-nav`/`.ln-nav a` (class specificity outranks bare `nav a`); `TestNavCSS_NeutralizesLegacyNavBleed` asserts the guard. |
| **#4 Scope param not injective** for names with `/`/`:` | Replaced the `':'/'/'`-delimited `Param()` with `Scope.Query() url.Values` (discrete, individually-escaped params) + `ScopeFromValues` inverse; `TestScope_QueryRoundTrip` includes adversarial `a/b:c` names. |
| **#5 Active-state aliases dropped** | `isActive(screen, active, lvl)` encodes the prototype's `querydetail→topqueries`, `scriptdetail→scripts`, and fleet-only `clusterdetail→clusters` aliases (Task 2); `TestIsActive_ExactAndAliases` + `TestBuildNav_ActiveAliases` cover them. |

### Placeholder scan

No step contains "TBD", "similar to Task N", "add error handling", or a code block with `…`/ellipsis. Every `.go`/`.templ`/`.css` body above is complete and compilable. Every screen id used in `BuildNav` has an entry in `screenPath` (Task 2): `fleet, clusters, nodes, databases, searchdomains, searchnodes, cacheclusters, cachereplicasets, cachenodes, clusterdetail, capabilities, topqueries, insights, plans, indexadvisor, vacuumadvisor, configadvisor, waits, connections, console, scripts, checks, alerts, inventory, tablegrowth, indexes, loginsights` — 27 ids, all mapped.

### Type-consistency check

- `Scope{Level, Cluster, Node, DB}` — field set identical everywhere it is constructed (Tasks 1, 3 tests, 5 tests, 6 tests). `Scope.Query()`/`HeaderLabel()` switch on all five `ScopeLevel` constants with a `default` (fleet) arm; `ScopeFromValues` is the exact inverse of `Query()` (round-trip-tested).
- `NavItem{Label, Screen, Href, Active, Soon, T2}` and `NavGroup{Label, Items}` — used consistently; `navItem()` is the only constructor, so `Href` is always `NavHref(sc, Screen)` (asserted by `TestBuildNav_HrefsCarryScope`) and `Active` is always `isActive(Screen, active, sc.Level)`.
- `BuildNav(Scope, EngineFlags, string) []NavGroup`, `Sidebar(Scope, EngineFlags, string)`, and `NavState{Scope, Engines, Active}` share the same trio; `Sidebar` calls `BuildNav` verbatim and `SidebarFromContext` calls `Sidebar` with the context `NavState`.
- `EngineFlags{Postgres, Search, Cache}` — read only in `BuildNav`'s fleet arm; `DefaultEngines()` returns `{Postgres:true}`, matching the PG-only reality and the fleet-gating tests.
- No import cycles: `web/scope.go` imports `net/url` + `strings`; `web/nav.go` imports nothing new; `web/nav_context.go` imports `context` + `io` + `github.com/a-h/templ`; `web/nav_sidebar.templ` uses only package-local symbols + `templ`. No new dependency on `internal/*` (the nav is store-free).
- `web.Layout` signature unchanged and the flat `<nav>` untouched (Task 7 only adds a `<link>`), so **no** `internal/api/*.go` handler or cluster page test needs editing in this bead; the legacy `ClusterSidebar` and its tests stay green. Their retirement is owned by the Task 7-filed mount bead.
