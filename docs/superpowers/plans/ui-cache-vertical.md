# Cache Vertical (Valkey/Redis) UI Implementation Plan

> For agentic workers: execute this plan with **superpowers:subagent-driven-development**. Each task is a self-contained unit — write the failing test first, run it to confirm the expected FAIL, implement, run to confirm PASS, then commit. Do not skip the RED step.

**Goal:** Ship the three fleet-scope Cache screens from the design handoff — Clusters (sentinel cards), Replicasets (sortable table), Nodes (sortable table with READ-WRITE/READ-ONLY ACCESS badges) — built entirely with design tokens and gated on `enableRedis || enableValkey`.

**Architecture:** Follows the established SSR pattern — view-model structs in package `web`, `templ` components (`XxxPage` full page wrapped in `@Layout` + swappable `XxxBody` fragment), HTTP handlers in `internal/api` that render them, routes registered in `internal/api/server.go`. Cache telemetry has **no collector/ingestion/store yet** (COMPARISON.md:378), so the handlers return the enabled flag with empty row slices and the screens render their empty state; the view-model structs defined here ARE the contract the future backend must fill. The engine-enable flags live on `api.Config` (mirroring the existing `DevAuth` env-gated field).

**Tech Stack:** Go 1.x, [a-h/templ](https://github.com/a-h/templ) (regenerated via `make templ`), HTMX (self-hosted), CSS custom properties (design tokens in `web/static/css/tokens.css`), `net/http` stdlib router, `httptest` + rendered-HTML assertions for unit tests, testcontainers for any DB-backed tests (none are needed here — the cache handlers are store-independent).

## Global Constraints

Copied verbatim — these are non-negotiable project rules:

- **Privacy T1/T2.** Only T1 (normalized, literal-free) data renders on these screens. The Cache screens are **T1 only** — counts, aggregates, labels, health. Per `docs/research/expand-redis.md` §B, key-level identity (the specific hot/big key) is **T2 by construction** and is explicitly OUT of scope for these screens. Never introduce a raw-literal field (key name, slowlog arg, command value) into any view-model here.
- **No external hosts.** All CSS/JS/fonts/SVG are self-hosted under `web/static/` and referenced as `/static/...`. Never add a CDN/font/script host. There is a contract test `TestLayout_NoExternalHosts` (`web/layout_test.go`) that fails the build if you do.
- **Tokens, not legacy.** New screens are built with design tokens (`var(--x)` from `tokens.css`), NOT the pre-design `legacy.css` component classes. All new CSS lives in a new token-based `web/static/css/cache.css` with `c-`-prefixed class names.
- **templ regen.** Any `.templ` edit requires `make templ` to regenerate the committed `_templ.go`; CI checks the generated files are in sync. Commit the regenerated `_templ.go` alongside the `.templ`.
- **testcontainers.** Integration tests hit real Postgres via testcontainers — never mock the DB. (Not needed in this plan; noted for completeness.)

## Design source references (read before implementing)

> Line numbers below are a snapshot from planning time and may drift as the design docs evolve. Each citation also gives a **grep anchor** (a stable string to search for) — prefer the anchor if the line number no longer matches.

- Prototype markup: `docs/design/Lynceus.dc.html` — grep ` CACHE CLUSTERS [LIVE]`, `CACHE REPLICASETS [LIVE]`, `CACHE NODES [LIVE]` (cache screens, ~`L609–720`); `id="eng-vk"` (`#eng-vk` sprite, ~`L61`); default sort keys `crsSort: 'health', cnSort: 'ops'` (grep `crsSort:`, ~`L2004`) and labels `S.cnSort.toUpperCase()` (grep `cnSortLabel`, ~`L3446`).
- Spec: `docs/design/README.md` — grep `### Cache vertical (Valkey/Redis)` (~`L60`); config flags `enableRedis`, `enableValkey` (grep `enableValkey`, ~`L87`); engine sprite slots `#eng-vk` (grep `#eng-vk`, ~`L98`).
- Gap map: `docs/design/COMPARISON.md` — grep `#### Cache vertical (Valkey/Redis) — impl:` (~`L368`); every listed gap maps to a task in the Self-Review coverage table.
- Backend feasibility (why key identity is T2): `docs/research/expand-redis.md` — grep `§B` / key-level identity.

---

### Task 1: Engine-enable gating on `api.Config`

Add the `enableRedis` / `enableValkey` fleet flags and a `CacheEnabled()` predicate to the server config, and wire them from env in `cmd/api/main.go`. This is the seam the Cache handlers (Tasks 4–6) and the future scoped nav (ly-ae6.3) both read to show/hide the Cache section.

**Files**
- Modify: `internal/api/server.go`
- Modify: `cmd/api/main.go`
- Create test: `internal/api/cache_test.go` (white-box `package api`; grows across Tasks 4–6)

**Interfaces**
- Produces:
  ```go
  // Config gains two fields + one method:
  type Config struct {
      DevAuth      bool
      EnableRedis  bool
      EnableValkey bool
  }
  func (c Config) CacheEnabled() bool // == c.EnableRedis || c.EnableValkey
  ```

**Steps**

- [ ] **Step 1: Write the failing test.** Create `internal/api/cache_test.go`:
  ```go
  package api

  import "testing"

  func TestConfig_CacheEnabled(t *testing.T) {
      cases := []struct {
          name string
          cfg  Config
          want bool
      }{
          {"none", Config{}, false},
          {"redis", Config{EnableRedis: true}, true},
          {"valkey", Config{EnableValkey: true}, true},
          {"both", Config{EnableRedis: true, EnableValkey: true}, true},
      }
      for _, c := range cases {
          if got := c.cfg.CacheEnabled(); got != c.want {
              t.Errorf("%s: CacheEnabled()=%v want %v", c.name, got, c.want)
          }
      }
  }
  ```

- [ ] **Step 2: Run it — expect FAIL (compile error: unknown fields / undefined method).**
  ```
  go test ./internal/api/ -run TestConfig_CacheEnabled
  ```
  Expected: `unknown field 'EnableRedis' in struct literal` / `c.cfg.CacheEnabled undefined`.

- [ ] **Step 3: Implement.** In `internal/api/server.go`, extend the `Config` struct and add the method. Replace:
  ```go
  type Config struct {
      // DevAuth, when true, bypasses authentication entirely and treats
      // every request as authenticated as a static dev admin. Only safe
      // in development — gated by the LYNCEUS_DEV_AUTH env var.
      DevAuth bool
  }
  ```
  with:
  ```go
  type Config struct {
      // DevAuth, when true, bypasses authentication entirely and treats
      // every request as authenticated as a static dev admin. Only safe
      // in development — gated by the LYNCEUS_DEV_AUTH env var.
      DevAuth bool

      // EnableRedis / EnableValkey gate the fleet-scope Cache section
      // (Clusters/Replicasets/Nodes). Sourced from LYNCEUS_ENABLE_REDIS /
      // LYNCEUS_ENABLE_VALKEY. Matches the design config model (README:87).
      EnableRedis  bool
      EnableValkey bool
  }

  // CacheEnabled reports whether the Cache vertical should be visible/reachable.
  func (c Config) CacheEnabled() bool { return c.EnableRedis || c.EnableValkey }
  ```

- [ ] **Step 4: Run it — expect PASS.**
  ```
  go test ./internal/api/ -run TestConfig_CacheEnabled
  ```

- [ ] **Step 5: Wire env in `cmd/api/main.go`.** After the existing `devAuth := os.Getenv("LYNCEUS_DEV_AUTH") == "true"` line (main.go:42), add:
  ```go
  enableRedis := os.Getenv("LYNCEUS_ENABLE_REDIS") == "true"
  enableValkey := os.Getenv("LYNCEUS_ENABLE_VALKEY") == "true"
  ```
  and change the `NewServer` call (main.go:65) from `api.Config{DevAuth: devAuth}` to:
  ```go
  srv := api.NewServer(api.Config{DevAuth: devAuth, EnableRedis: enableRedis, EnableValkey: enableValkey},
      store.NewStats(pool).WithReadPool(statsRO),
      store.NewConfig(configPool).WithReadPool(configRO))
  ```

- [ ] **Step 6: Build to confirm wiring compiles.**
  ```
  go build ./cmd/api/...
  ```
  Expected: no output (success).

- [ ] **Step 7: Commit.**
  ```
  git add internal/api/server.go cmd/api/main.go internal/api/cache_test.go
  git commit -m "cache-ui: add enableRedis/enableValkey gating on api.Config (ly-ae6.11)"
  ```

---

### Task 2: Cache stylesheet, `#eng-vk` sprite, and view-model helpers

Add the token-based `cache.css`, link it from the layout head, add the `#eng-vk` key sprite as a reusable `templ` component, and add the pure view-model helper funcs (severity/role/access → CSS class, sort-toggle). No screen renders yet — this task only lands the shared primitives so Tasks 3/5/6 have exact classes and helpers to reference.

**Files**
- Create: `web/static/css/cache.css`
- Modify: `web/layout.templ` (+ regenerate `web/layout_templ.go`)
- Create: `web/cache_vm.go`
- Create test: `web/cache_vm_test.go`
- Create test: `web/cache_test.go` (grows across Tasks 3/5/6)

**Interfaces**
- Produces (in `package web`):
  ```go
  func sevClass(sev string) string     // "crit"→"c-sev-crit", "warn"→"c-sev-warn",
                                        // "info"→"c-sev-info", "ok"→"c-sev-ok", else "c-sev-mut"
  func roleClass(role string) string   // "PRIMARY"→"c-role c-role-primary", else "c-role c-role-replica"
  func accessClass(access string) string // "READ-WRITE"→"c-access c-access-rw", else "c-access c-access-ro"
  func nextSort(cur, a, b string) string // cur==a ? b : a  (SORT-toggle target)
  ```

**Steps**

- [ ] **Step 1: Write the failing helper test.** Create `web/cache_vm_test.go`:
  ```go
  package web

  import "testing"

  func TestSevClass(t *testing.T) {
      for in, want := range map[string]string{
          "crit": "c-sev-crit", "warn": "c-sev-warn", "info": "c-sev-info",
          "ok": "c-sev-ok", "": "c-sev-mut", "bogus": "c-sev-mut",
      } {
          if got := sevClass(in); got != want {
              t.Errorf("sevClass(%q)=%q want %q", in, got, want)
          }
      }
  }

  func TestRoleAndAccessClass(t *testing.T) {
      if got := roleClass("PRIMARY"); got != "c-role c-role-primary" {
          t.Errorf("roleClass primary=%q", got)
      }
      if got := roleClass("REPLICA"); got != "c-role c-role-replica" {
          t.Errorf("roleClass replica=%q", got)
      }
      if got := accessClass("READ-WRITE"); got != "c-access c-access-rw" {
          t.Errorf("accessClass rw=%q", got)
      }
      if got := accessClass("READ-ONLY"); got != "c-access c-access-ro" {
          t.Errorf("accessClass ro=%q", got)
      }
  }

  func TestNextSort(t *testing.T) {
      if got := nextSort("health", "health", "name"); got != "name" {
          t.Errorf("nextSort from health=%q", got)
      }
      if got := nextSort("name", "health", "name"); got != "health" {
          t.Errorf("nextSort from name=%q", got)
      }
  }
  ```

- [ ] **Step 2: Run it — expect FAIL (undefined: sevClass, roleClass, accessClass, nextSort).**
  ```
  go test ./web/ -run 'TestSevClass|TestRoleAndAccessClass|TestNextSort'
  ```

- [ ] **Step 3: Implement helpers.** Create `web/cache_vm.go`:
  ```go
  package web

  // sevClass maps a T1 severity token to its cache CSS colour class.
  func sevClass(sev string) string {
      switch sev {
      case "crit":
          return "c-sev-crit"
      case "warn":
          return "c-sev-warn"
      case "info":
          return "c-sev-info"
      case "ok":
          return "c-sev-ok"
      default:
          return "c-sev-mut"
      }
  }

  // roleClass maps a cache node role to its chip classes.
  func roleClass(role string) string {
      if role == "PRIMARY" {
          return "c-role c-role-primary"
      }
      return "c-role c-role-replica"
  }

  // accessClass maps a cache node access mode to its badge classes.
  // PRIMARY nodes accept writes (READ-WRITE); replicas are READ-ONLY.
  func accessClass(access string) string {
      if access == "READ-WRITE" {
          return "c-access c-access-rw"
      }
      return "c-access c-access-ro"
  }

  // nextSort returns the sort key to toggle to, given the current key and the
  // two options a/b. Used to build the SORT button's hx-get target.
  func nextSort(cur, a, b string) string {
      if cur == a {
          return b
      }
      return a
  }
  ```

- [ ] **Step 4: Run helper test — expect PASS.**
  ```
  go test ./web/ -run 'TestSevClass|TestRoleAndAccessClass|TestNextSort'
  ```

- [ ] **Step 5: Create the stylesheet.** Create `web/static/css/cache.css` (token-based; matches prototype pixel values):
  ```css
  /* Cache vertical (Valkey/Redis) — Clusters / Replicasets / Nodes.
     Token-based; c- prefixed to avoid legacy.css collisions. */

  .c-page { padding: 18px 22px 32px; display: flex; flex-direction: column;
            gap: 14px; max-width: 1400px; }
  .c-head { display: flex; align-items: baseline; gap: 12px; }
  .c-title { font-size: 17px; font-weight: 600; }
  .c-live { font-family: var(--font-mono); font-size: 10px; color: var(--acc);
            border: 1px solid var(--acc); padding: 0 5px; border-radius: 1px; }
  .c-meta { font-family: var(--font-mono); font-size: 10.5px; color: var(--faint);
            letter-spacing: .08em; }
  .c-spacer { flex: 1; }
  .c-btn { font-family: var(--font-mono); font-size: 10.5px; color: var(--dim);
           border: 1px solid var(--line); padding: 4px 9px; border-radius: 2px;
           cursor: pointer; user-select: none; background: none; }
  .c-btn:hover { color: var(--text); }
  .c-btn-add { color: var(--acc2); border-color: var(--acc); }
  .c-btn-add:hover { background: var(--accdim); }

  .c-card { border: 1px solid var(--line); border-radius: 2px; background: var(--surface); }
  .c-card-head { padding: 10px 14px; border-bottom: 1px solid var(--line);
                 display: flex; align-items: center; gap: 12px;
                 font-family: var(--font-mono); }
  .c-eng { width: 20px; height: 20px; border: 1.5px solid var(--acc2);
           color: var(--acc2); display: flex; align-items: center;
           justify-content: center; border-radius: 2px; flex-shrink: 0; }
  .c-name { font-size: 13.5px; font-weight: 600; }
  .c-ver { font-size: 10px; color: var(--infoT); }
  .c-chip { font-size: 9.5px; border: 1px solid var(--line); padding: 1px 6px;
            border-radius: 1px; color: var(--infoT); letter-spacing: .06em; }

  .c-stats { display: grid; grid-template-columns: repeat(5, 1fr); }
  .c-stat { padding: 10px 14px; border-right: 1px solid var(--line2);
            display: flex; flex-direction: column; gap: 2px; }
  .c-stat-label { font-family: var(--font-mono); font-size: 9.5px;
                  letter-spacing: .1em; color: var(--faint); }
  .c-stat-val { font-family: var(--font-mono); font-size: 17px; font-weight: 600;
                font-variant-numeric: tabular-nums; }
  .c-stat-sub { font-size: 10.5px; color: var(--dim); }

  .c-rs-row { display: flex; align-items: center; gap: 12px; padding: 8px 14px;
              border-top: 1px solid var(--line2); font-family: var(--font-mono); }
  .c-rs-name { font-size: 11.5px; font-weight: 600; min-width: 130px; }
  .c-rs-topo { font-size: 10px; color: var(--faint); letter-spacing: .04em; }
  .c-note { padding: 9px 14px; border-top: 1px solid var(--line2); display: flex;
            gap: 12px; font-family: var(--font-mono); font-size: 10px;
            color: var(--dim); letter-spacing: .04em; }

  .c-table-wrap { border: 1px solid var(--line); border-radius: 2px;
                  background: var(--surface); overflow-x: auto; }
  .c-rs-min { min-width: 880px; }
  .c-node-min { min-width: 920px; }
  .c-rs-grid { display: grid; gap: 12px; padding: 8px 14px; align-items: center;
               grid-template-columns: 150px 130px 190px 80px 110px 90px 90px 90px; }
  .c-node-grid { display: grid; gap: 12px; padding: 8px 14px; align-items: center;
                 grid-template-columns: 74px 170px 130px 64px 110px 80px 80px 70px 110px; }
  .c-rs-head, .c-node-head { border-bottom: 1px solid var(--line); }
  .c-rs-head span, .c-node-head span { font-family: var(--font-mono);
      font-size: 9.5px; letter-spacing: .1em; color: var(--faint); }
  .c-rs-row2, .c-node-row { border-bottom: 1px solid var(--line2);
      font-family: var(--font-mono); font-variant-numeric: tabular-nums; }
  .c-rs-row2 span, .c-node-row span { font-size: 11.5px; }
  .c-node-name { font-weight: 600; }
  .c-num { text-align: right; }

  .c-dim { color: var(--dim); }
  .c-faint { color: var(--faint); }
  .c-mut { color: var(--mut); }

  .c-role { font-size: 9px; border: 1px solid var(--line); padding: 1px 4px;
            border-radius: 1px; text-align: center; letter-spacing: .04em; }
  .c-role-primary { color: var(--acc2); }
  .c-role-replica { color: var(--infoT); }
  .c-access { font-size: 9px; padding: 1px 6px; border-radius: 1px;
              text-align: center; letter-spacing: .06em; border: 1px solid var(--line); }
  .c-access-rw { color: var(--acc2); border-color: var(--acc2); }
  .c-access-ro { color: var(--dim); border-color: var(--dim); }

  .c-sev-crit { color: var(--critT); }
  .c-sev-warn { color: var(--warnT); }
  .c-sev-info { color: var(--infoT); }
  .c-sev-ok { color: var(--acc2); }
  .c-sev-mut { color: var(--mut); }

  .c-empty { border: 1px solid var(--line); border-radius: 2px;
             background: var(--surface); padding: 14px; font-family: var(--font-mono);
             font-size: 10.5px; color: var(--dim); letter-spacing: .04em; }
  ```

- [ ] **Step 6: Link the stylesheet + add the sprite component.** In `web/layout.templ`, add the cache stylesheet link immediately after the `legacy.css` link (line 27):
  ```html
  <link rel="stylesheet" href="/static/css/cache.css"/>
  ```
  Then append a new sprite component to the bottom of `web/layout.templ` (after the `Layout` templ):
  ```go
  // cacheEngineSprite renders the hidden #eng-vk key glyph once per page.
  // Original glyph (no official Redis/Valkey logo) — matches design sprite slot.
  templ cacheEngineSprite() {
      <svg width="0" height="0" style="position:absolute" aria-hidden="true">
          <symbol id="eng-vk" viewBox="0 0 24 24">
              <circle cx="8" cy="12" r="4" fill="none" stroke="currentColor" stroke-width="2"></circle>
              <line x1="12" y1="12" x2="20" y2="12" stroke="currentColor" stroke-width="2" stroke-linecap="round"></line>
              <line x1="17" y1="12" x2="17" y2="15.5" stroke="currentColor" stroke-width="2" stroke-linecap="round"></line>
              <line x1="20" y1="12" x2="20" y2="14" stroke="currentColor" stroke-width="2" stroke-linecap="round"></line>
          </symbol>
      </svg>
  }
  ```

- [ ] **Step 7: Write the failing layout/asset test.** Add to a new `web/cache_test.go`:
  ```go
  package web

  import (
      "context"
      "strings"
      "testing"
  )

  func TestLayout_LinksCacheStylesheet(t *testing.T) {
      var sb strings.Builder
      if err := Layout("t", "s").Render(context.Background(), &sb); err != nil {
          t.Fatalf("render: %v", err)
      }
      if !strings.Contains(sb.String(), `href="/static/css/cache.css"`) {
          t.Error("layout must link /static/css/cache.css")
      }
  }

  func TestCacheEngineSprite_HasKeyGlyph(t *testing.T) {
      var sb strings.Builder
      if err := cacheEngineSprite().Render(context.Background(), &sb); err != nil {
          t.Fatalf("render: %v", err)
      }
      if !strings.Contains(sb.String(), `id="eng-vk"`) {
          t.Error("sprite must define #eng-vk symbol")
      }
  }
  ```

- [ ] **Step 8: Regenerate templ + run — expect PASS.**
  ```
  make templ
  go test ./web/ -run 'TestLayout_LinksCacheStylesheet|TestCacheEngineSprite_HasKeyGlyph|TestLayout_'
  ```
  (Re-running the existing `TestLayout_*` suite confirms the new `<link>` did not break `TestLayout_NoExternalHosts` / `TestLayout_SelfHostedAssets`.)

- [ ] **Step 9: Verify the static asset is served.** `web/static/` is embedded via `web.StaticHandler()`. Confirm the file is picked up by the embed by building:
  ```
  go build ./web/...
  ```

- [ ] **Step 10: Commit.**
  ```
  git add web/static/css/cache.css web/layout.templ web/layout_templ.go web/cache_vm.go web/cache_vm_test.go web/cache_test.go
  git commit -m "cache-ui: token stylesheet + #eng-vk sprite + view-model helpers (ly-ae6.11)"
  ```

---

### Task 3: Cache Clusters screen (view-model + templ)

Sentinel-grouped cluster cards: header (engine box, name, `v<ver>`, mode chip, provider chip), a 5-cell stat strip (replicasets / memory / ops·s / hit rate / sentinels·quorum), replicaset rows, the "writes go to each replicaset's primary" note, and the `REPLICASETS →` link. Plus the empty state and the `+ ADD CLUSTER` button.

**Files**
- Create: `web/cache.templ` (+ regenerate `web/cache_templ.go`)
- Modify test: `web/cache_test.go`

**Interfaces**
- Produces (in `package web`, defined at the top of `cache.templ`):
  ```go
  type CacheStat struct {
      Label string // e.g. "REPLICASETS", "MEMORY", "OPS/S", "HIT RATE", "SENTINELS"
      Value string // display string, e.g. "6.2/16G", "94.1%"
      Sub   string // small sub-line, "" -> empty
      Sev   string // "crit"|"warn"|"info"|"ok"|"mut" -> colour class
  }
  type CacheClusterRS struct {
      Name   string
      Topo   string // "1 PRIMARY + 2 REPLICAS"
      Mem    string
      Ops    string
      Health string
      Sev    string
  }
  type CacheCluster struct {
      ID          string
      Name        string
      Version     string // rendered "v"+Version
      Mode        string // "SENTINEL" | "CLUSTER" | "STANDALONE"
      Provider    string // "SELF-HOSTED" | "AWS" | "AZ" | "PS"
      Engine      string // "VALKEY" | "REDIS" (glyph title only)
      Stats       []CacheStat // SENTINEL mode = 5 cells (replicasets/memory/ops·s/hit/sentinels·quorum);
                              // CLUSTER/STANDALONE modes omit the sentinels·quorum cell. Slice, not a
                              // fixed array, so the mode dictates the cell count (see Backend dependency).
      Replicasets []CacheClusterRS
  }
  type CacheClustersView struct {
      Enabled  bool
      Clusters []CacheCluster
  }
  templ CacheClustersPage(v CacheClustersView)
  templ CacheClustersBody(v CacheClustersView)
  ```

**Steps**

- [ ] **Step 1: Write the failing render test.** Append to `web/cache_test.go`:
  ```go
  func renderBody(t *testing.T, c interface {
      Render(context.Context, *strings.Builder) error
  }) string {
      t.Helper()
      var sb strings.Builder
      if err := c.Render(context.Background(), &sb); err != nil {
          t.Fatalf("render: %v", err)
      }
      return sb.String()
  }

  func TestCacheClustersBody_EmptyState(t *testing.T) {
      html := renderBody(t, CacheClustersBody(CacheClustersView{Enabled: true}))
      if !strings.Contains(html, "Cache Clusters") {
          t.Error("missing screen title")
      }
      if !strings.Contains(html, "NO CACHE CLUSTERS REPORTING YET") {
          t.Error("missing empty state")
      }
      if !strings.Contains(html, "+ ADD CLUSTER") {
          t.Error("missing + ADD CLUSTER button")
      }
  }

  func TestCacheClustersBody_Card(t *testing.T) {
      v := CacheClustersView{Enabled: true, Clusters: []CacheCluster{{
          Name: "cache-prod", Version: "8.1", Mode: "SENTINEL",
          Provider: "SELF-HOSTED", Engine: "VALKEY",
          Stats: []CacheStat{
              {Label: "REPLICASETS", Value: "3", Sev: "mut"},
              {Label: "MEMORY", Value: "6.2/16G", Sev: "mut"},
              {Label: "OPS/S", Value: "41,200", Sev: "mut"},
              {Label: "HIT RATE", Value: "94.1%", Sev: "ok"},
              {Label: "SENTINELS", Value: "3/3", Sub: "QUORUM OK", Sev: "ok"},
          },
          Replicasets: []CacheClusterRS{
              {Name: "rs-sessions", Topo: "1 PRIMARY + 2 REPLICAS", Mem: "2.1G", Ops: "18,400", Health: "● HEALTHY", Sev: "ok"},
          },
      }}}
      html := renderBody(t, CacheClustersBody(v))
      for _, want := range []string{
          "cache-prod", "v8.1", "SENTINEL", "SELF-HOSTED",
          `href="#eng-vk"`, "HIT RATE", "94.1%", "c-sev-ok",
          "rs-sessions", "1 PRIMARY + 2 REPLICAS",
          "WRITES GO TO EACH REPLICASET", // note text (assert the apostrophe-free prefix — templ escapes ')
          `href="/cache/replicasets"`,
      } {
          if !strings.Contains(html, want) {
              t.Errorf("clusters card missing %q", want)
          }
      }
  }
  ```

- [ ] **Step 2: Run it — expect FAIL (undefined: CacheClustersBody / CacheClustersView).**
  ```
  go test ./web/ -run TestCacheClustersBody
  ```

- [ ] **Step 3: Implement `web/cache.templ`.** Create the file with the view-model structs and the two templ components. **Do NOT add `import "strings"` yet** — nothing in Task 3 uses it, and templ passes imports straight through to the generated `web/cache_templ.go`, so an unused import makes `go build ./web/...` fail with `"strings" imported and not used`. The `strings` import is added in Task 5 Step 3, where `strings.ToUpper` is first used (same file).
  ```go
  package web

  type CacheStat struct {
      Label string
      Value string
      Sub   string
      Sev   string
  }

  type CacheClusterRS struct {
      Name   string
      Topo   string
      Mem    string
      Ops    string
      Health string
      Sev    string
  }

  type CacheCluster struct {
      ID          string
      Name        string
      Version     string
      Mode        string
      Provider    string
      Engine      string
      Stats       []CacheStat // cell count is mode-dependent (SENTINEL=5); see the interface note above
      Replicasets []CacheClusterRS
  }

  type CacheClustersView struct {
      Enabled  bool
      Clusters []CacheCluster
  }

  templ CacheClustersPage(v CacheClustersView) {
      @Layout("Lynceus — cache clusters", "cache clusters (Valkey / Redis)") {
          @cacheEngineSprite()
          @CacheClustersBody(v)
      }
  }

  templ CacheClustersBody(v CacheClustersView) {
      <div id="cache-body" class="c-page">
          <div class="c-head">
              <span class="c-title">Cache Clusters</span>
              <span class="c-live">LIVE</span>
              <span class="c-meta">VALKEY / REDIS — A CLUSTER (SENTINEL) GROUPS REPLICASETS</span>
              <span class="c-spacer"></span>
              <button type="button" class="c-btn c-btn-add" data-add-cache="valkey">+ ADD CLUSTER</button>
          </div>
          if len(v.Clusters) == 0 {
              <div class="c-empty">NO CACHE CLUSTERS REPORTING YET — DEPLOY A COLLECTOR WITH TARGET_KIND=VALKEY.</div>
          } else {
              for _, c := range v.Clusters {
                  @cacheClusterCard(c)
              }
          }
      </div>
  }

  templ cacheClusterCard(c CacheCluster) {
      <div class="c-card">
          <div class="c-card-head">
              <span class="c-eng" title={ c.Engine }>
                  <svg width="12" height="12" viewBox="0 0 24 24"><use href="#eng-vk"></use></svg>
              </span>
              <span class="c-name">{ c.Name }</span>
              <span class="c-ver">v{ c.Version }</span>
              <span class="c-chip">{ c.Mode }</span>
              <span class="c-chip">{ c.Provider }</span>
          </div>
          <div class="c-stats">
              for _, st := range c.Stats {
                  <div class="c-stat">
                      <span class="c-stat-label">{ st.Label }</span>
                      <span class={ "c-stat-val " + sevClass(st.Sev) }>{ st.Value }</span>
                      <span class="c-stat-sub">{ st.Sub }</span>
                  </div>
              }
          </div>
          for _, r := range c.Replicasets {
              <div class="c-rs-row">
                  <span class="c-rs-name">{ r.Name }</span>
                  <span class="c-rs-topo">{ r.Topo }</span>
                  <span class="c-spacer"></span>
                  <span class="c-mut">{ r.Mem }</span>
                  <span class="c-dim">{ r.Ops } OPS/S</span>
                  <span class={ sevClass(r.Sev) }>{ r.Health }</span>
              </div>
          }
          <div class="c-note">
              <span>WRITES GO TO EACH REPLICASET'S PRIMARY — REPLICAS ARE READ-ONLY</span>
              <span class="c-spacer"></span>
              <a href="/cache/replicasets">REPLICASETS →</a>
          </div>
      </div>
  }
  ```
  Note on the `strings` import: Task 3's file has **no** `import` block. `strings` is introduced by Task 5 Step 3 (its `strings.ToUpper(v.Sort)` in the SORT buttons), which is the first and only use. This keeps Task 3 a clean, independently-compilable RED→GREEN→commit unit: `go build ./web/...` and `go test ./web/` both pass after Task 3 alone.

- [ ] **Step 4: Regenerate + run — expect PASS.**
  ```
  make templ
  go test ./web/ -run TestCacheClustersBody
  ```

- [ ] **Step 5: Commit.**
  ```
  git add web/cache.templ web/cache_templ.go web/cache_test.go
  git commit -m "cache-ui: Cache Clusters screen (sentinel cards + stat strip) (ly-ae6.11)"
  ```

---

### Task 4: Cache Clusters handler, routes, gating

Wire `/cache/clusters` and `/partial/cache/clusters`, gated on `CacheEnabled()`. No cache store exists, so the handler returns an empty `CacheClustersView` (empty state renders). Handler/route tests are DB-free white-box tests in `package api`.

**Files**
- Create: `internal/api/cache.go`
- Modify: `internal/api/server.go` (register routes)
- Modify test: `internal/api/cache_test.go`

**Interfaces**
- Consumes: `web.CacheClustersView`, `web.CacheClustersPage`, `web.CacheClustersBody` (Task 3).
- Produces:
  ```go
  func (s *Server) handleCacheClusters(w http.ResponseWriter, r *http.Request)
  func (s *Server) handleCacheClustersPartial(w http.ResponseWriter, r *http.Request)
  func (s *Server) fetchCacheClusters() web.CacheClustersView
  ```

**Steps**

- [ ] **Step 1: Write the failing handler test.** Append to `internal/api/cache_test.go`:
  ```go
  // (add imports: "net/http", "net/http/httptest")

  func TestHandleCacheClusters_GatedOff(t *testing.T) {
      s := &Server{cfg: Config{}} // cache disabled
      req := httptest.NewRequest(http.MethodGet, "/cache/clusters", nil)
      w := httptest.NewRecorder()
      s.handleCacheClusters(w, req)
      if w.Code != http.StatusNotFound {
          t.Errorf("disabled cache: got %d want 404", w.Code)
      }
  }

  func TestHandleCacheClusters_On(t *testing.T) {
      s := &Server{cfg: Config{EnableValkey: true}}
      req := httptest.NewRequest(http.MethodGet, "/cache/clusters", nil)
      w := httptest.NewRecorder()
      s.handleCacheClusters(w, req)
      if w.Code != http.StatusOK {
          t.Fatalf("enabled cache: got %d want 200", w.Code)
      }
      if !strings.Contains(w.Body.String(), "Cache Clusters") {
          t.Error("body missing screen title")
      }
  }

  func TestCacheRoutes_Registered(t *testing.T) {
      s := &Server{cfg: Config{DevAuth: true, EnableValkey: true}, mux: http.NewServeMux()}
      s.routes()
      srv := httptest.NewServer(s.Handler())
      defer srv.Close()
      resp, err := http.Get(srv.URL + "/cache/clusters")
      if err != nil {
          t.Fatalf("get: %v", err)
      }
      defer resp.Body.Close()
      if resp.StatusCode != http.StatusOK {
          t.Errorf("GET /cache/clusters: got %d want 200", resp.StatusCode)
      }
  }
  ```
  (Add `"strings"` to the test imports.)

- [ ] **Step 2: Run it — expect FAIL (undefined: s.handleCacheClusters).**
  ```
  go test ./internal/api/ -run 'TestHandleCacheClusters|TestCacheRoutes'
  ```

- [ ] **Step 3: Implement `internal/api/cache.go`.**
  ```go
  package api

  import (
      "net/http"

      "github.com/dobbo-ca/lynceus/web"
  )

  func (s *Server) handleCacheClusters(w http.ResponseWriter, r *http.Request) {
      if !s.cfg.CacheEnabled() {
          http.NotFound(w, r)
          return
      }
      w.Header().Set("Content-Type", "text/html; charset=utf-8")
      _ = web.CacheClustersPage(s.fetchCacheClusters()).Render(r.Context(), w)
  }

  func (s *Server) handleCacheClustersPartial(w http.ResponseWriter, r *http.Request) {
      if !s.cfg.CacheEnabled() {
          http.NotFound(w, r)
          return
      }
      w.Header().Set("Content-Type", "text/html; charset=utf-8")
      _ = web.CacheClustersBody(s.fetchCacheClusters()).Render(r.Context(), w)
  }

  // fetchCacheClusters returns the cache-clusters view. Cache telemetry has no
  // collector/ingestion/store yet (COMPARISON.md:378, ly-ae6.11 backend note),
  // so the cluster list is empty and the screen renders its empty state. When
  // the redisnorm ingest path lands, replace the nil slice with a fleetview
  // query returning []web.CacheCluster.
  func (s *Server) fetchCacheClusters() web.CacheClustersView {
      return web.CacheClustersView{Enabled: true, Clusters: nil}
  }
  ```

- [ ] **Step 4: Register the routes.** In `internal/api/server.go` `routes()`, after the existing `/checks` routes (line 76), add:
  ```go
  s.mux.HandleFunc("GET /cache/clusters", s.handleCacheClusters)
  s.mux.HandleFunc("GET /partial/cache/clusters", s.handleCacheClustersPartial)
  ```

- [ ] **Step 5: Run — expect PASS.**
  ```
  go test ./internal/api/ -run 'TestHandleCacheClusters|TestCacheRoutes'
  ```

- [ ] **Step 6: Commit.**
  ```
  git add internal/api/cache.go internal/api/server.go internal/api/cache_test.go
  git commit -m "cache-ui: /cache/clusters handler + routes + gating (ly-ae6.11)"
  ```

---

### Task 5: Cache Replicasets + Cache Nodes screens (view-models + templ)

Two sortable tables. Replicasets: `REPLICASET · CLUSTER · TOPOLOGY · KEYS · MEMORY · OPS/S · EVICTIONS · HEALTH`, sort HEALTH/NAME. Nodes: `ROLE · NODE · REPLICASET · VER · MEMORY · OPS/S · CLIENTS · HIT · ACCESS`, sort OPS/NAME, with READ-WRITE (primary) / READ-ONLY (replica) ACCESS badges and PRIMARY/REPLICA role chips.

**Files**
- Modify: `web/cache.templ` (+ regenerate `web/cache_templ.go`)
- Modify test: `web/cache_test.go`

**Interfaces**
- Produces (append to `cache.templ`):
  ```go
  type CacheReplicasetRow struct {
      Name, Cluster, Topo, Keys, Mem, Ops, Evictions, Health, Sev string
      SevRank int // sort key for HEALTH (worst-first)
  }
  type CacheReplicasetsView struct {
      Enabled bool
      Rows    []CacheReplicasetRow
      Sort    string // "health" | "name"
  }
  type CacheNodeRow struct {
      Role, Name, Replicaset, Version, Mem, Ops, Clients, Hit, Access string
      OpsVal float64 // sort key for OPS
  }
  type CacheNodesView struct {
      Enabled bool
      Rows    []CacheNodeRow
      Sort    string // "ops" | "name"
  }
  templ CacheReplicasetsPage(v CacheReplicasetsView)
  templ CacheReplicasetsBody(v CacheReplicasetsView)
  templ CacheNodesPage(v CacheNodesView)
  templ CacheNodesBody(v CacheNodesView)
  ```

**Steps**

- [ ] **Step 1: Write the failing render tests.** Append to `web/cache_test.go`:
  ```go
  func TestCacheReplicasetsBody(t *testing.T) {
      v := CacheReplicasetsView{Enabled: true, Sort: "health", Rows: []CacheReplicasetRow{
          {Name: "rs-sessions", Cluster: "cache-prod", Topo: "1P + 2R",
           Keys: "1.2M", Mem: "2.1G", Ops: "18,400", Evictions: "0", Health: "● HEALTHY", Sev: "ok"},
      }}
      html := renderBody(t, CacheReplicasetsBody(v))
      for _, want := range []string{
          "Replicasets", "REPLICASET", "TOPOLOGY", "EVICTIONS",
          "rs-sessions", "cache-prod", "c-sev-ok",
          "SORT: HEALTH", `hx-get="/partial/cache/replicasets?sort=name"`,
      } {
          if !strings.Contains(html, want) {
              t.Errorf("replicasets body missing %q", want)
          }
      }
  }

  func TestCacheNodesBody_AccessBadges(t *testing.T) {
      v := CacheNodesView{Enabled: true, Sort: "ops", Rows: []CacheNodeRow{
          {Role: "PRIMARY", Name: "rs-sessions-0", Replicaset: "rs-sessions",
           Version: "8.1", Mem: "2.1G", Ops: "12,000", Clients: "340", Hit: "94%", Access: "READ-WRITE"},
          {Role: "REPLICA", Name: "rs-sessions-1", Replicaset: "rs-sessions",
           Version: "8.1", Mem: "2.0G", Ops: "6,400", Clients: "120", Hit: "95%", Access: "READ-ONLY"},
      }}
      html := renderBody(t, CacheNodesBody(v))
      for _, want := range []string{
          "Cache Nodes", "ROLE", "ACCESS",
          "c-role c-role-primary", "c-role c-role-replica",
          "c-access c-access-rw", "READ-WRITE",
          "c-access c-access-ro", "READ-ONLY",
          "SORT: OPS", `hx-get="/partial/cache/nodes?sort=name"`,
      } {
          if !strings.Contains(html, want) {
              t.Errorf("nodes body missing %q", want)
          }
      }
  }
  ```

- [ ] **Step 2: Run it — expect FAIL (undefined: CacheReplicasetsBody / CacheNodesBody).**
  ```
  go test ./web/ -run 'TestCacheReplicasetsBody|TestCacheNodesBody'
  ```

- [ ] **Step 3: Implement.** Task 3 created `web/cache.templ` with a `package web` line and **no import block**. Two edits in this step:
  1. **Insert an import at the top of the file**, immediately below `package web` (this is the first use of `strings` in the file — `strings.ToUpper` in the SORT buttons below):
     ```go
     package web

     import "strings"
     ```
  2. **Append** the following view-models + templ components to the end of `web/cache.templ`:
  ```go
  type CacheReplicasetRow struct {
      Name      string
      Cluster   string
      Topo      string
      Keys      string
      Mem       string
      Ops       string
      Evictions string
      Health    string
      Sev       string
      SevRank   int
  }

  type CacheReplicasetsView struct {
      Enabled bool
      Rows    []CacheReplicasetRow
      Sort    string
  }

  templ CacheReplicasetsPage(v CacheReplicasetsView) {
      @Layout("Lynceus — cache replicasets", "cache replicasets") {
          @cacheEngineSprite()
          @CacheReplicasetsBody(v)
      }
  }

  templ CacheReplicasetsBody(v CacheReplicasetsView) {
      <div id="cache-body" class="c-page">
          <div class="c-head">
              <span class="c-title">Replicasets</span>
              <span class="c-live">LIVE</span>
              <span class="c-meta">1 PRIMARY + N REPLICAS · WRITES LAND ON THE PRIMARY ONLY</span>
              <span class="c-spacer"></span>
              <button
                  type="button"
                  class="c-btn"
                  hx-get={ "/partial/cache/replicasets?sort=" + nextSort(v.Sort, "health", "name") }
                  hx-target="#cache-body"
                  hx-swap="outerHTML"
              >SORT: { strings.ToUpper(v.Sort) } ⇅</button>
          </div>
          <div class="c-table-wrap">
              <div class="c-rs-min">
                  <div class="c-rs-grid c-rs-head">
                      <span>REPLICASET</span><span>CLUSTER</span><span>TOPOLOGY</span>
                      <span class="c-num">KEYS</span><span class="c-num">MEMORY</span>
                      <span class="c-num">OPS/S</span><span class="c-num">EVICTIONS</span>
                      <span class="c-num">HEALTH</span>
                  </div>
                  if len(v.Rows) == 0 {
                      <div class="c-empty">NO REPLICASETS REPORTING YET.</div>
                  } else {
                      for _, r := range v.Rows {
                          <div class="c-rs-grid c-rs-row2">
                              <span class="c-rs-name">{ r.Name }</span>
                              <span class="c-dim">{ r.Cluster }</span>
                              <span class="c-faint">{ r.Topo }</span>
                              <span class="c-num c-mut">{ r.Keys }</span>
                              <span class="c-num c-mut">{ r.Mem }</span>
                              <span class="c-num c-mut">{ r.Ops }</span>
                              <span class="c-num c-mut">{ r.Evictions }</span>
                              <span class={ "c-num " + sevClass(r.Sev) }>{ r.Health }</span>
                          </div>
                      }
                  }
              </div>
          </div>
      </div>
  }

  type CacheNodeRow struct {
      Role       string
      Name       string
      Replicaset string
      Version    string
      Mem        string
      Ops        string
      Clients    string
      Hit        string
      Access     string
      OpsVal     float64
  }

  type CacheNodesView struct {
      Enabled bool
      Rows    []CacheNodeRow
      Sort    string
  }

  templ CacheNodesPage(v CacheNodesView) {
      @Layout("Lynceus — cache nodes", "cache nodes") {
          @cacheEngineSprite()
          @CacheNodesBody(v)
      }
  }

  templ CacheNodesBody(v CacheNodesView) {
      <div id="cache-body" class="c-page">
          <div class="c-head">
              <span class="c-title">Cache Nodes</span>
              <span class="c-live">LIVE</span>
              <span class="c-meta">PRIMARIES ACCEPT WRITES · REPLICAS ARE READ-ONLY UNTIL PROMOTED</span>
              <span class="c-spacer"></span>
              <button
                  type="button"
                  class="c-btn"
                  hx-get={ "/partial/cache/nodes?sort=" + nextSort(v.Sort, "ops", "name") }
                  hx-target="#cache-body"
                  hx-swap="outerHTML"
              >SORT: { strings.ToUpper(v.Sort) } ⇅</button>
          </div>
          <div class="c-table-wrap">
              <div class="c-node-min">
                  <div class="c-node-grid c-node-head">
                      <span>ROLE</span><span>NODE</span><span>REPLICASET</span><span>VER</span>
                      <span class="c-num">MEMORY</span><span class="c-num">OPS/S</span>
                      <span class="c-num">CLIENTS</span><span class="c-num">HIT</span>
                      <span class="c-num">ACCESS</span>
                  </div>
                  if len(v.Rows) == 0 {
                      <div class="c-empty">NO CACHE NODES REPORTING YET.</div>
                  } else {
                      for _, n := range v.Rows {
                          <div class="c-node-grid c-node-row">
                              <span class={ roleClass(n.Role) }>{ n.Role }</span>
                              <span class="c-node-name">{ n.Name }</span>
                              <span class="c-dim">{ n.Replicaset }</span>
                              <span class="c-dim">v{ n.Version }</span>
                              <span class="c-num c-mut">{ n.Mem }</span>
                              <span class="c-num c-mut">{ n.Ops }</span>
                              <span class="c-num c-mut">{ n.Clients }</span>
                              <span class="c-num c-mut">{ n.Hit }</span>
                              <span class={ accessClass(n.Access) }>{ n.Access }</span>
                          </div>
                      }
                  }
              </div>
          </div>
      </div>
  }
  ```

- [ ] **Step 4: Regenerate + run — expect PASS.**
  ```
  make templ
  go test ./web/ -run 'TestCacheReplicasetsBody|TestCacheNodesBody'
  ```

- [ ] **Step 5: Full web package test — expect PASS (no regressions).**
  ```
  go test ./web/
  ```

- [ ] **Step 6: Commit.**
  ```
  git add web/cache.templ web/cache_templ.go web/cache_test.go
  git commit -m "cache-ui: Replicasets + Nodes tables with READ-WRITE/READ-ONLY badges (ly-ae6.11)"
  ```

---

### Task 6: Replicasets/Nodes handlers + sort, final wiring, verification

Wire the four remaining routes with server-side sort, and finish with a full build/test/templ-sync verification. Sort is real, pure, and unit-tested even though the row source is currently empty.

**Files**
- Modify: `internal/api/cache.go`
- Modify: `internal/api/server.go` (register routes)
- Modify test: `internal/api/cache_test.go`

**Interfaces**
- Produces:
  ```go
  func (s *Server) handleCacheReplicasets(w http.ResponseWriter, r *http.Request)
  func (s *Server) handleCacheReplicasetsPartial(w http.ResponseWriter, r *http.Request)
  func (s *Server) fetchCacheReplicasets(r *http.Request) web.CacheReplicasetsView
  func sortCacheReplicasets(rows []web.CacheReplicasetRow, key string)
  func (s *Server) handleCacheNodes(w http.ResponseWriter, r *http.Request)
  func (s *Server) handleCacheNodesPartial(w http.ResponseWriter, r *http.Request)
  func (s *Server) fetchCacheNodes(r *http.Request) web.CacheNodesView
  func sortCacheNodes(rows []web.CacheNodeRow, key string)
  ```

**Steps**

- [ ] **Step 1: Write the failing sort + gating tests.** Append to `internal/api/cache_test.go`:
  ```go
  // (add import: "github.com/dobbo-ca/lynceus/web")

  func TestSortCacheReplicasets(t *testing.T) {
      rows := []web.CacheReplicasetRow{
          {Name: "b", SevRank: 0}, {Name: "a", SevRank: 2}, {Name: "c", SevRank: 1},
      }
      sortCacheReplicasets(rows, "health") // worst (highest SevRank) first
      if rows[0].Name != "a" || rows[1].Name != "c" || rows[2].Name != "b" {
          t.Errorf("health sort order = %v %v %v", rows[0].Name, rows[1].Name, rows[2].Name)
      }
      sortCacheReplicasets(rows, "name")
      if rows[0].Name != "a" || rows[1].Name != "b" || rows[2].Name != "c" {
          t.Errorf("name sort order = %v %v %v", rows[0].Name, rows[1].Name, rows[2].Name)
      }
  }

  func TestSortCacheNodes(t *testing.T) {
      rows := []web.CacheNodeRow{
          {Name: "x", OpsVal: 10}, {Name: "y", OpsVal: 30}, {Name: "z", OpsVal: 20},
      }
      sortCacheNodes(rows, "ops") // highest OPS first
      if rows[0].Name != "y" || rows[1].Name != "z" || rows[2].Name != "x" {
          t.Errorf("ops sort order = %v %v %v", rows[0].Name, rows[1].Name, rows[2].Name)
      }
      sortCacheNodes(rows, "name")
      if rows[0].Name != "x" || rows[2].Name != "z" {
          t.Errorf("name sort order = %v .. %v", rows[0].Name, rows[2].Name)
      }
  }

  func TestFetchCacheReplicasets_SortDefault(t *testing.T) {
      s := &Server{cfg: Config{EnableRedis: true}}
      req := httptest.NewRequest(http.MethodGet, "/cache/replicasets", nil)
      if v := s.fetchCacheReplicasets(req); v.Sort != "health" {
          t.Errorf("default sort = %q want health", v.Sort)
      }
      req2 := httptest.NewRequest(http.MethodGet, "/cache/replicasets?sort=name", nil)
      if v := s.fetchCacheReplicasets(req2); v.Sort != "name" {
          t.Errorf("explicit sort = %q want name", v.Sort)
      }
  }

  func TestHandleCacheNodes_Gating(t *testing.T) {
      off := &Server{cfg: Config{}}
      w := httptest.NewRecorder()
      off.handleCacheNodes(w, httptest.NewRequest(http.MethodGet, "/cache/nodes", nil))
      if w.Code != http.StatusNotFound {
          t.Errorf("nodes gated off: got %d want 404", w.Code)
      }
      on := &Server{cfg: Config{EnableValkey: true}}
      w2 := httptest.NewRecorder()
      on.handleCacheNodes(w2, httptest.NewRequest(http.MethodGet, "/cache/nodes", nil))
      if w2.Code != http.StatusOK || !strings.Contains(w2.Body.String(), "Cache Nodes") {
          t.Errorf("nodes on: got %d body=%q", w2.Code, w2.Body.String())
      }
  }
  ```

- [ ] **Step 2: Run it — expect FAIL (undefined: sortCacheReplicasets / handleCacheNodes / fetchCacheReplicasets).**
  ```
  go test ./internal/api/ -run 'TestSortCache|TestFetchCacheReplicasets|TestHandleCacheNodes'
  ```

- [ ] **Step 3: Implement.** Append to `internal/api/cache.go` (add `"sort"` to the import block):
  ```go
  func (s *Server) handleCacheReplicasets(w http.ResponseWriter, r *http.Request) {
      if !s.cfg.CacheEnabled() {
          http.NotFound(w, r)
          return
      }
      w.Header().Set("Content-Type", "text/html; charset=utf-8")
      _ = web.CacheReplicasetsPage(s.fetchCacheReplicasets(r)).Render(r.Context(), w)
  }

  func (s *Server) handleCacheReplicasetsPartial(w http.ResponseWriter, r *http.Request) {
      if !s.cfg.CacheEnabled() {
          http.NotFound(w, r)
          return
      }
      w.Header().Set("Content-Type", "text/html; charset=utf-8")
      _ = web.CacheReplicasetsBody(s.fetchCacheReplicasets(r)).Render(r.Context(), w)
  }

  // fetchCacheReplicasets returns the replicasets view. No cache store yet
  // (ly-ae6.11 backend note); rows are empty and the screen renders its empty
  // state. Sort is applied so it is correct the moment rows arrive.
  func (s *Server) fetchCacheReplicasets(r *http.Request) web.CacheReplicasetsView {
      sortKey := r.URL.Query().Get("sort")
      if sortKey != "name" {
          sortKey = "health"
      }
      var rows []web.CacheReplicasetRow // future: fleetview.ListCacheReplicasets(...)
      sortCacheReplicasets(rows, sortKey)
      return web.CacheReplicasetsView{Enabled: true, Rows: rows, Sort: sortKey}
  }

  // sortCacheReplicasets sorts in place: "name" ascending, else "health"
  // (worst SevRank first, ties broken by name).
  func sortCacheReplicasets(rows []web.CacheReplicasetRow, key string) {
      switch key {
      case "name":
          sort.SliceStable(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
      default:
          sort.SliceStable(rows, func(i, j int) bool {
              if rows[i].SevRank != rows[j].SevRank {
                  return rows[i].SevRank > rows[j].SevRank
              }
              return rows[i].Name < rows[j].Name
          })
      }
  }

  func (s *Server) handleCacheNodes(w http.ResponseWriter, r *http.Request) {
      if !s.cfg.CacheEnabled() {
          http.NotFound(w, r)
          return
      }
      w.Header().Set("Content-Type", "text/html; charset=utf-8")
      _ = web.CacheNodesPage(s.fetchCacheNodes(r)).Render(r.Context(), w)
  }

  func (s *Server) handleCacheNodesPartial(w http.ResponseWriter, r *http.Request) {
      if !s.cfg.CacheEnabled() {
          http.NotFound(w, r)
          return
      }
      w.Header().Set("Content-Type", "text/html; charset=utf-8")
      _ = web.CacheNodesBody(s.fetchCacheNodes(r)).Render(r.Context(), w)
  }

  // fetchCacheNodes returns the nodes view (empty rows until backend lands).
  func (s *Server) fetchCacheNodes(r *http.Request) web.CacheNodesView {
      sortKey := r.URL.Query().Get("sort")
      if sortKey != "name" {
          sortKey = "ops"
      }
      var rows []web.CacheNodeRow // future: fleetview.ListCacheNodes(...)
      sortCacheNodes(rows, sortKey)
      return web.CacheNodesView{Enabled: true, Rows: rows, Sort: sortKey}
  }

  // sortCacheNodes sorts in place: "name" ascending, else "ops"
  // (highest OpsVal first, ties broken by name).
  func sortCacheNodes(rows []web.CacheNodeRow, key string) {
      switch key {
      case "name":
          sort.SliceStable(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
      default:
          sort.SliceStable(rows, func(i, j int) bool {
              if rows[i].OpsVal != rows[j].OpsVal {
                  return rows[i].OpsVal > rows[j].OpsVal
              }
              return rows[i].Name < rows[j].Name
          })
      }
  }
  ```

- [ ] **Step 4: Register the routes.** In `internal/api/server.go` `routes()`, after the `/partial/cache/clusters` line added in Task 4, add:
  ```go
  s.mux.HandleFunc("GET /cache/replicasets", s.handleCacheReplicasets)
  s.mux.HandleFunc("GET /partial/cache/replicasets", s.handleCacheReplicasetsPartial)
  s.mux.HandleFunc("GET /cache/nodes", s.handleCacheNodes)
  s.mux.HandleFunc("GET /partial/cache/nodes", s.handleCacheNodesPartial)
  ```

- [ ] **Step 5: Run cache tests — expect PASS.**
  ```
  go test ./internal/api/ -run 'TestSortCache|TestFetchCacheReplicasets|TestHandleCacheNodes|TestHandleCacheClusters|TestCacheRoutes|TestConfig_CacheEnabled'
  ```

- [ ] **Step 6: Full verification — templ sync, build, tests.**
  ```
  make templ            # must produce no diff (generated files committed)
  git diff --exit-code -- web/*_templ.go   # expect: no output (in sync)
  go build ./...        # expect: success
  go test ./web/ ./internal/api/           # expect: PASS
  ```
  If `git diff --exit-code` reports a diff, commit the regenerated `_templ.go`.

- [ ] **Step 7: Commit.**
  ```
  git add internal/api/cache.go internal/api/server.go internal/api/cache_test.go web/cache_templ.go
  git commit -m "cache-ui: Replicasets/Nodes handlers + server-side sort + final wiring (ly-ae6.11)"
  ```

---

## Integration contracts (dependencies built elsewhere)

> **⚠ Merge-ordering / discoverability risk (coordinate before merge).** This plan compiles, tests, and passes CI **fully independently** — it does not touch the legacy `layout.templ` `<nav>` (that nav is ae6.3's rebuild). Consequence: after this bead merges, `/cache/clusters`, `/cache/replicasets`, `/cache/nodes` are reachable **only by typing the URL** until ly-ae6.3 renders the CACHE nav section. The bead formally **DEPENDS ON ly-ae6.3**, so the intended sequence is **ae6.3 lands first (or concurrently), then this**. If this bead must merge before ae6.3, the three screens ship with **no UI entry point** (functional but orphaned). Two acceptable resolutions — pick one at merge time with the ae6.3 owner: **(a)** land ae6.3's CACHE nav section first (preferred — this plan's routes/gating are the contract ae6.3 consumes), or **(b)** add a single temporary nav `<li>` to `layout.templ` guarded by `cfg.CacheEnabled()` and delete it when ae6.3's nav lands. This plan does **not** implement (b) by default (it would touch legacy nav the design says ae6.3 owns); it is the fallback only if merge order forces it. The 404-when-disabled gating means the orphaned routes are never reachable in a non-cache deployment regardless.

- **ly-ae6.2 (scope state + global top bar):** These are fleet-scope screens; they do not require a selected scope. When the top bar lands, its time-range control (15M/1H/24H/7D/30D) should propagate to `fetchCache*` as a `?range=` param the same way other screens receive it. No change to the view-model contract is needed — `fetchCache*` already reads `r.URL.Query()`.
- **ly-ae6.3 (scope-driven sidebar nav):** the nav must render a **CACHE** section with entries **Clusters → `/cache/clusters`**, **Replicasets → `/cache/replicasets`**, **Nodes → `/cache/nodes`**, shown **only when `Server.cfg.CacheEnabled()` is true** (README:32, :87; COMPARISON.md:374). This plan deliberately does NOT modify the legacy `layout.templ` `<nav>` (that is ae6.3's rebuild); the routes 404 when cache is disabled so the section is unreachable regardless. Nav gating is the single integration point ae6.3 must honour.
- **ly-ae6.12 (+ ADD wizard):** the `+ ADD CLUSTER` button carries a `data-add-cache="valkey"` hook and is inert in this bead. ae6.12 wires it to the onboarding wizard (`TARGET_KIND=valkey` path). No behaviour change required here.
- **ly-ae6.4 (fleet dashboard):** the CACHE stat cell, VALKEY placeholder cards, and cross-engine Needs-Attention (evictions) on the fleet dashboard are ae6.4's scope (COMPARISON.md:375), reusing the `#eng-vk` sprite and `cache.css` classes landed here.

## Backend dependency (untracked — file a bead)

Cache telemetry has **no collector/normalize/reader/wire/ingestion/store** (COMPARISON.md:378; feasibility in `docs/research/expand-redis.md`). There is currently **no backend bead** for it. Before these screens show live data, a backend workstream must be filed and built to produce:

- `fleetview.ListCacheClusters(ctx, ...) ([]web.CacheCluster, error)`
- `fleetview.ListCacheReplicasets(ctx, ...) ([]web.CacheReplicasetRow, error)`
- `fleetview.ListCacheNodes(ctx, ...) ([]web.CacheNodeRow, error)`

sourced from `redisnorm` (command-shape normalization), INFO/SLOWLOG/LATENCY/MEMORY/CLIENT readers, new T1 wire messages + contract test, and a cache stats-store schema. **Privacy constraint carried into that bead:** per research §B, key-level identity (hot/big key names) is **T2 by construction** and must NOT appear in any of the T1 structs above — these screens stay T1-only. When the backend lands, replace the three `var rows` / empty-slice stubs in `internal/api/cache.go` with the `fleetview.ListCache*` calls; the view-model contract does not change.

**`CacheCluster.Stats` cell-count is mode-dependent (contract for `ListCacheClusters`):** it is a `[]CacheStat` slice, not a fixed array, precisely so the backend can vary the stat strip by `CacheCluster.Mode`. SENTINEL mode fills 5 cells (`REPLICASETS · MEMORY · OPS/S · HIT RATE · SENTINELS`) — the shape the prototype renders. CLUSTER / STANDALONE modes have no sentinel quorum, so they must omit the `SENTINELS` cell (4 cells) rather than emit a placeholder. The templ ranges over whatever the slice contains, so no view-model change is needed when the backend begins emitting non-SENTINEL clusters. One open cosmetic detail for that future bead: `.c-stats` is a fixed 5-column grid (`repeat(5, 1fr)`), so a 4-cell strip would leave column 5 empty (and the 4th cell keeps its `border-right`). Deciding the per-mode grid template (e.g. `repeat(var, 1fr)` or `grid-template-columns: repeat(N, 1fr)` set from the cell count) is deferred to the non-SENTINEL rendering work, not this bead. The MVP here renders the empty state only, so only the SENTINEL 5-cell shape is exercised by the tests, and the CSS is authored for it.

## Self-Review

### Spec-coverage: every COMPARISON.md:368–379 gap → task

| COMPARISON gap (line) | Covered by |
|---|---|
| No Clusters screen: sentinel card + stat strip (replicasets/memory/ops-s/hit rate/sentinels-quorum) + replicaset rows + primary-write note + `+ ADD CLUSTER` (371) | Task 3 (`cacheClusterCard`, 5-cell `Stats`, `Replicasets`, `c-note`, `+ ADD CLUSTER`) |
| No Replicasets screen: sortable HEALTH/NAME table — topology, keys, memory, ops/s, evictions, health (372) | Task 5 (`CacheReplicasetsBody`) + Task 6 (`sortCacheReplicasets`) |
| No Nodes screen: sortable OPS/NAME — role, node, replicaset, version, memory, ops/s, clients, hit, READ-WRITE/READ-ONLY ACCESS badge (373) | Task 5 (`CacheNodesBody`, `roleClass`/`accessClass`) + Task 6 (`sortCacheNodes`) |
| No enableRedis/enableValkey flags or engine-neutral nav gating (374) | Task 1 (`Config.EnableRedis/EnableValkey/CacheEnabled`); nav gating contract → ly-ae6.3 (documented) |
| No cache dashboard integration (CACHE cell, placeholder cards, evictions) (375) | Out of scope — ly-ae6.4 (documented in Integration contracts); primitives (sprite, css) provided here |
| No `#eng-vk` key glyph / VALKEY text mark (376) | Task 2 (`cacheEngineSprite`, `.c-eng`) |
| No `+ ADD` wizard TARGET_KIND=valkey path (377) | Out of scope — ly-ae6.12; `data-add-cache="valkey"` hook provided (documented) |
| Backend entirely absent (378) | Out of scope — Backend dependency section defines the exact `fleetview.ListCache*` contract |
| Scope model is Postgres-entity-only; no replicaset scope target (379) | Not required — Cache screens are fleet-scope lists (README:60–64); no per-replicaset scope target introduced |

### Bead ly-ae6.11 acceptance criteria → task

| Criterion (from `bd show`) | Covered by |
|---|---|
| cluster(sentinel) → replicaset(1 primary + N replicas) → nodes hierarchy | Task 3 (cluster→replicaset rows) + Task 5 (replicaset→node tables) |
| writes only to replicaset primary; READ-WRITE/READ-ONLY badges | Task 5 (`accessClass` PRIMARY→READ-WRITE / REPLICA→READ-ONLY) + cluster note (Task 3) |
| gated on enableRedis \|\| enableValkey | Task 1 (`CacheEnabled`) + Tasks 4/6 (404 when disabled) |

### Placeholder scan

No `TBD`, `...`, `similar to Task N`, or code step without real code. Every templ component, handler, sort func, CSS rule, and test is written in full. The only intentional empty values are the row slices in `fetchCache*` — justified and documented (backend absent), and the sort funcs still run against them (unit-tested with hand-built rows in Task 6).

### Type-consistency check

- `Config.CacheEnabled() bool` — value receiver; called on `s.cfg` (value field) and on `Config{}` literals in tests. ✅
- View-model field types are display `string`s (T1, mono-rendered) with numeric sort companions `SevRank int` / `OpsVal float64`. `sortCacheReplicasets`/`sortCacheNodes` sort by the numeric companions, never by the display strings. ✅
- `[]CacheStat` slice is `range`-iterated in templ, so the rendered cell count follows the data: SENTINEL mode supplies 5 cells (matching the prototype), CLUSTER/STANDALONE supply fewer (no sentinels·quorum cell). No SENTINEL-only shape is baked into the view-model contract; the constraint is documented on the struct and in the Backend dependency section. ✅
- Helpers `sevClass/roleClass/accessClass/nextSort` are `func(string) string` / `func(string,string,string) string`; every call site passes strings and concatenates into `class=`/`hx-get=` attributes. ✅
- `strings.ToUpper(v.Sort)` requires `import "strings"` in `cache.templ` — added in Task 5 Step 3 (first use), and NOT in Task 3 (Task 3 uses nothing from `strings`, so importing it there would fail `go build`). ✅
- **Sort-label wording is intentional and prototype-faithful.** The Nodes SORT button renders `SORT: OPS` (from `strings.ToUpper("ops")`) while the Nodes table's throughput column header is `OPS/S`. This is not a mismatch: the prototype defines the sort *key* as `cnSort: 'ops'` and the button label as `S.cnSort.toUpperCase()` = `OPS` (`Lynceus.dc.html:2004`, `:3446`), separate from the `OPS/S` column label. Replicasets likewise: sort key `crsSort: 'health'` → `SORT: HEALTH`. The tests assert `SORT: OPS` / `SORT: HEALTH` accordingly. ✅
- Handlers return `web.Cache*View`; templ `Cache*Page`/`Cache*Body` consume the exact same struct types (same package `web`). ✅
- Routes use Go 1.22 method-prefixed patterns (`"GET /cache/clusters"`) consistent with existing `server.go` entries. ✅
- White-box `package api` tests construct `&Server{cfg: ...}` (and `mux` for the routing test) directly — cache handlers touch no store, so `stats`/`conf`/`disc` stay nil safely. ✅
