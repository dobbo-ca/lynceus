# Governance / Access / Settings UI Implementation Plan

> For agentic workers: execute this plan with the **superpowers:subagent-driven-development** skill — one task per subagent, TDD, commit per task. Each task below is self-contained: it names the exact files, the exact Go/templ/CSS/JS to write, the exact test, and the exact commands. Reference only symbols defined in this plan or already present in the repo.

**Goal:** Bring the four governance/admin surfaces — the top-right **user menu**, the **Audit Log** (hash-chain banner, TARGET/HASH columns, T2 amber striping), the **Access & Roles** roadmap page, and the **Settings › Appearance** accent picker — to design-handoff parity, built on the F1 token/theme foundation.

**Architecture:** These are org-level (not scope-level) surfaces reached from the top-right avatar menu, not the scoped sidebar. Each is a templ component in `package web` (view-model = plain Go struct) rendered by a thin `internal/api` handler and wrapped in the existing `@Layout`. All markup uses design tokens via a single new stylesheet `web/static/css/governance.css` (semantic classes over `var(--…)`), never legacy component CSS. The Audit Log retrofits the existing `/audit` handler + templ; Access & Roles and Settings add two new routes; the user menu is a standalone fragment that the ly-ae6.2 top bar mounts.

**Tech Stack:** Go 1.x, `templ` (a-h/templ, regen via `make templ`), HTMX (self-hosted), server-rendered HTML, PostgreSQL config store via `pgx` (`internal/store`), testcontainers-backed integration tests (`postgres:16`), `net/http` + `httptest` handler tests, rendered-HTML string assertions for view logic.

## Global Constraints

Copied verbatim — every task obeys these:

- **Privacy (T1/T2).** Only T1 (normalized, literal-free) data renders unless a screen is explicitly T2. The audit **Detail** blob carries a **closed vocabulary of structural keys only** (`database_name`, `capability`, `enabled`, `reason`, `since`, `until`, `limit`) — never a literal from a monitored database. The derived TARGET column is built from those structural keys, so it stays T1-safe. Never introduce a raw-literal field into a T1 path. T2 rows are marked (`data_tier = 2`) and rendered amber-striped; they are already produced under audit-on-read by `internal/store/t2_read.go`.
- **No external hosts.** Never reference a CDN/font/script/style host. All CSS/JS/fonts are self-hosted under `web/static/` and served at `/static/…` by `web.StaticHandler()`. There is a contract test `web.TestLayout_NoExternalHosts`; keep it green.
- **Tokens, not legacy.** New screens are built with design tokens (`var(--acc)`, `var(--surface)`, `var(--line)`, `var(--font-mono)`, …) in `web/static/css/governance.css` and semantic classes — NOT the pre-design `legacy.css` component classes. Dark is the default; light is handled by tokens.css automatically.
- **templ regen.** Any `.templ` edit requires `make templ` to regenerate the committed `*_templ.go`. CI checks the generated files are in sync; commit them alongside the `.templ` source.
- **testcontainers.** Integration tests hit REAL Postgres via testcontainers (`newPGPool` / `setupAudit` in `internal/api/server_test.go`). Do not mock the database.

---

## Foundations already present (do NOT rebuild)

Verified on this branch:

- **Tokens/fonts/theme (F1, ly-ae6.1):** `web/static/css/tokens.css` defines every token used here — `--acc --acc2 --accbg --accdim --bg --rail --surface --raised --line --line2 --text --mut --dim --faint --crit --critT --critbg --warn --warnT --warnbg --info --infoT --infobg --ok --shimA --shimB`, shape `--radius:2px --radius-badge:1px --border:1px --shadow-pop:0 8px 24px rgba(0,0,0,.35)`, and fonts `--font-ui:'Work Sans',…` / `--font-mono:'JetBrains Mono',…`. `web/layout.templ` links tokens.css + legacy.css, inlines the no-flash theme bootstrap (`web/bootstrap.go`), and loads self-hosted `theme.js`.
- **Accent JS API (F1):** `window.Lynceus.setAccent('#2dd4bf'|'#22d3ee'|'#818cf8')` (in `web/static/js/theme.js`) persists to `localStorage['lynceus.accent']` and re-applies the per-theme accent variant map from `web/bootstrap.go` (`L.ACCENTS`). Task 4's picker CONSUMES this — it does not reimplement accent CSS (the CSS-variant refinement is ly-ae6.14).
- **Static embed:** `web/static.go` embeds `web/static/**` via `//go:embed static`; new files under `web/static/` are served automatically at `/static/…` with an immutable cache header.
- **Audit store (ly-8b0.3, closed):** `internal/store/config.go` — `pgxConfig.ListAudit(ctx, AuditFilter) ([]AuditRecord, error)` (id DESC, populates `RowHash`/`PrevHash`), `pgxConfig.VerifyChain(ctx, since, until) (int, string, error)` returning `(-1, "", nil)` when intact, `AppendAuditReturning`. Audit ids are contiguous from 1 (VerifyChain enforces the no-gap invariant), so `tip.ID == total event count`.
- **Existing audit UI (ly-8b0.7, closed):** `web/audit.templ` (`AuditPage`/`AuditFilterForm`/`AuditTable`, VM `AuditRow`/`AuditFilterValues`) and `internal/api/audit.go` (`handleAuditPage`/`handleAuditPartial`/`fetchAudit`, routes `/audit` + `/partial/audit`). The HTMX filter form is a bonus beyond the design — **keep it**.
- **Dev identity:** `internal/api/capabilities.go` has `actorFromContext(_ *http.Request) string { return "dev-admin" }` and `api.Config.DevAuth`. No real user/RBAC model exists yet (M5). Use `actorFromContext` for the user-menu identity; group/T2-status are dev placeholders until ly-8b0.4.

## Integration contract & dependencies

- **ly-ae6.2 (top bar) — REQUIRED entry point.** The user menu (Task 2) is a standalone fragment `@web.UserMenu(vm)`. The ly-ae6.2 top bar mounts it at the top-right, calling `web.NewUserMenuVM(actor, group, t2Granted)`. This plan does NOT edit `web/layout.templ` (ly-ae6.2 owns the top-bar shell); it delivers the fragment + VM + builder + styles so mounting is a one-liner. The `<details>`-based disclosure needs no JS from the top bar.
  - **No end-to-end / visual verification on this branch (accepted).** Because ly-ae6.2 is unbuilt, nothing on this branch renders `@web.UserMenu` inside `@Layout`/the top bar. The fragment is verified only in isolation (Task 2 `web/user_menu_test.go`, rendered-HTML asserts). It therefore ships as an **unmounted fragment** — correct and unit-tested, but with no live/visual confirmation until ly-ae6.2 mounts it. This is an inherent consequence of the dependency split, not a gap to close here.
  - **Flat-nav `/audit` link — removed by ly-ae6.2, NOT here.** Task 1 keeps the existing `web/layout.templ` flat-nav `href="/audit"` link (this plan deliberately doesn't touch `layout.templ`, and the retained handler test still asserts that link). Consequently, until ly-ae6.2 rebuilds the shell, Audit Log will appear in BOTH the flat nav AND the user-menu GOVERNANCE section. That is expected and transitional: relocating governance under the user menu (i.e. deleting the flat-nav link and updating any test that asserts it) is **ly-ae6.2's responsibility**, since ly-ae6.2 owns `layout.templ`.
- **Head asset injection — interim body placement, ly-ae6.2 to own.** New pages emit `<link rel="stylesheet" href="/static/css/governance.css">` and `<script src="/static/js/settings.js" defer>` inside the `@Layout` children (i.e. in `<body>`), because this plan does not edit `layout.templ`. This is valid HTML5 and renders correctly, but a body-loaded stylesheet is mildly FOUC-prone and is a new convention for this codebase. It is an accepted interim: when ly-ae6.2 rebuilds the shell it should own `<head>` asset injection (hoist governance.css into `<head>`), at which point these body `<link>`s can be dropped. No action required on this bead beyond this note.
- **ly-ae6.3 (scoped nav) — deliberately NOT a dependency.** Governance/admin is reached ONLY from the user menu, never the scoped sidebar (PRODUCT_INTENT: low-level sections are scope-only; governance is org-level). These pages render in `@Layout` and do not participate in scope.
- **ly-8b0.4 (RBAC org→server→db) — backend, OPEN.** Access & Roles (Task 3) renders the design's **ROADMAP preview** with illustrative group rows; the `AccessGroupRow` VM is the exact shape ly-8b0.4 will populate. The user-menu identity's `GROUP · T2 GRANTED/DENIED` line is also a placeholder until ly-8b0.4 + ly-8b0.2 (SCIM) + ly-8b0.1 (OIDC) land. No RBAC backend is planned here.
- **Persistence of accent (Task 4):** interim = `localStorage` via the F1 `setAccent`. Server-side per-user persistence needs a users table (M5, unbuilt); noted, deferred. No new store table is added.

---

### Task 1: Audit Log design-parity retrofit

Brings `web/audit.templ` + `internal/api/audit.go` to parity: LIVE badge, verified hash-chain banner (`✓ HASH CHAIN VERIFIED · TIP c4b7…2ef · N EVENTS`), tamper-evident subtitle, token grid with **TARGET** and **HASH** columns, tier badge, and **T2 amber striping**. Creates the shared `governance.css`.

**Files**
- create `web/static/css/governance.css` — shared token stylesheet for all four screens (audit + user-menu + access + settings classes, authored once).
- modify `web/audit.templ` — extend VMs, rebuild page/table markup to tokens.
- modify `web/audit_templ.go` — regenerated by `make templ` (commit it).
- modify `internal/store/config.go` — add `VerifyChain` to the `Config` interface (method already on `*pgxConfig`).
- modify `internal/api/audit.go` — compute chain banner + derive TARGET/HASH/T2, new `AuditPage` call.
- test: modify `web/audit_render_test.go` (new file) — web-layer render assertions.
- test: modify `internal/api/audit_test.go` — add chain/target/striping integration test.
- test: modify `web/static_test.go` — assert `governance.css` is served.

**Interfaces**

Consumes (existing, verified):
```go
// internal/store/config.go
func (c *pgxConfig) ListAudit(ctx context.Context, f AuditFilter) ([]AuditRecord, error)
func (c *pgxConfig) VerifyChain(ctx context.Context, since, until time.Time) (int, string, error) // (-1,"",nil)==intact
type AuditRecord struct { ID int64; Actor, Action, ServerID string; DataTier int16; Detail []byte; At time.Time; PrevHash, RowHash []byte }
type AuditFilter struct { Actor, Action, ServerID string; Since, Until time.Time; Tier *int16; Limit int }
// internal/api/capabilities.go
func actorFromContext(_ *http.Request) string // "dev-admin"
```

Produces:
```go
// web/audit.templ (package web)
type AuditChain struct {
	Verified bool   // whole-chain VerifyChain result
	TipShort string // "c4b7…2ef" (first4…last3 of tip RowHash) or "—"
	Count    int64  // tip.ID == total events; 0 when empty
}
type AuditRow struct { // extended
	ID        int64
	Actor     string
	Action    string
	ServerID  string
	DataTier  int16
	Detail    string
	At        string // pre-formatted RFC3339 UTC
	Target    string // derived from Detail structural keys; "" when none
	HashShort string // "a71f…9c2" from RowHash; "—" when absent
	IsT2      bool   // DataTier == 2
}
templ AuditPage(chain AuditChain, f AuditFilterValues, rows []AuditRow)
templ AuditTable(rows []AuditRow) // swap target; NO banner (page-level only)

// internal/store/config.go — Config interface gains:
VerifyChain(ctx context.Context, since, until time.Time) (int, string, error)

// internal/api/audit.go
func (s *Server) auditChain(ctx context.Context) web.AuditChain
func auditTarget(detail []byte) string
func hashShort(h []byte) string
func tierLabel(t int16) string
```

**Steps**

- [ ] **Step 1: Create the shared token stylesheet.** Write `web/static/css/governance.css` (full contents — used by Tasks 1–4):
```css
/* governance.css — design-token styling for the governance/admin/settings surfaces.
   Dark is the tokens.css default; light is handled by the token overrides. */

/* ---- shared page chrome ---- */
.gov-page { padding: 18px 22px 32px; display: flex; flex-direction: column; gap: 14px; max-width: 1400px; }
.gov-head { display: flex; align-items: center; gap: 12px; }
.gov-title { font-family: var(--font-ui); font-size: 17px; font-weight: 600; color: var(--text); }
.gov-spacer { flex: 1; }
.gov-note { font-family: var(--font-mono); font-size: 10px; color: var(--faint); letter-spacing: .04em; }
.gov-roadmap-strip { border: 1px solid var(--info); border-radius: 2px; background: var(--infobg); padding: 8px 12px; font-family: var(--font-mono); font-size: 10.5px; color: var(--infoT); }
.gov-roadmap-strip em { font-style: normal; font-family: var(--font-ui); font-size: 12px; color: var(--mut); }

.badge { font-family: var(--font-mono); font-size: 10px; padding: 0 5px; border-radius: 1px; border: 1px solid var(--line); color: var(--faint); letter-spacing: .04em; }
.badge--live { color: var(--acc); border-color: var(--acc); }
.badge--roadmap { color: var(--infoT); border-color: var(--info); }
.badge--soon { font-size: 9px; padding: 0 4px; color: var(--faint); border-color: var(--line); }

/* ---- audit log ---- */
.chain-banner { font-family: var(--font-mono); font-size: 10.5px; color: var(--acc2); border: 1px solid var(--acc); background: var(--accbg); padding: 4px 10px; border-radius: 2px; }
.chain-banner--broken { color: var(--critT); border-color: var(--crit); background: var(--critbg); }
.audit-card { border: 1px solid var(--line); border-radius: 2px; background: var(--surface); overflow-x: auto; }
.audit-grid { display: grid; grid-template-columns: 168px 110px 210px 1fr 150px 44px 84px; gap: 10px; align-items: center; font-family: var(--font-mono); min-width: 900px; }
.audit-grid--head { padding: 8px 12px; border-bottom: 1px solid var(--line); font-size: 9.5px; letter-spacing: .1em; color: var(--faint); }
.audit-row { padding: 7px 12px; border-bottom: 1px solid var(--line2); border-left: 3px solid transparent; }
.audit-row--t2 { border-left-color: var(--warn); background: var(--warnbg); }
.audit-row .c-ts { font-size: 10.5px; color: var(--dim); font-variant-numeric: tabular-nums; }
.audit-row .c-actor { font-size: 11px; color: var(--text); }
/* Action color is T2-driven, matching the design source exactly. The prototype
   computes actionColor per row as `a.t2 ? 'var(--warnT)' : 'var(--text)'`
   (Lynceus.dc.html line 3202) — a binary T2 rule, NOT a per-action-type color
   map. These two rules are a faithful, complete reproduction. */
.audit-row .c-action { font-size: 11px; color: var(--text); }
.audit-row--t2 .c-action { color: var(--warnT); }
.audit-row .c-target { font-size: 11px; color: var(--mut); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.audit-row .c-server { font-size: 10.5px; color: var(--dim); }
.audit-row .tier-badge { font-size: 9.5px; border: 1px solid var(--line); color: var(--faint); padding: 1px 4px; border-radius: 1px; text-align: center; }
.audit-row--t2 .tier-badge { border-color: var(--warn); color: var(--warnT); }
.audit-row .c-hash { font-size: 10.5px; color: var(--faint); }

/* ---- user menu (mounted by the ly-ae6.2 top bar) ---- */
.user-menu { position: relative; }
.user-menu > summary { list-style: none; width: 26px; height: 26px; border: 1px solid var(--line); border-radius: 2px; display: flex; align-items: center; justify-content: center; font-family: var(--font-mono); font-size: 10px; color: var(--mut); cursor: pointer; user-select: none; }
.user-menu > summary::-webkit-details-marker { display: none; }
.user-menu[open] > summary { border-color: var(--acc); color: var(--text); }
.user-menu__panel { position: absolute; top: 32px; right: 0; width: 236px; background: var(--surface); border: 1px solid var(--line); border-radius: 2px; box-shadow: var(--shadow-pop); z-index: 40; }
.user-menu__id { padding: 10px 12px; border-bottom: 1px solid var(--line); display: flex; flex-direction: column; gap: 3px; }
.user-menu__name { font-family: var(--font-ui); font-size: 12px; font-weight: 600; color: var(--text); }
.user-menu__meta { font-family: var(--font-mono); font-size: 9.5px; color: var(--dim); letter-spacing: .06em; }
.user-menu__section { padding: 8px 12px 3px; font-family: var(--font-mono); font-size: 9.5px; letter-spacing: .12em; color: var(--faint); }
.user-menu__section--admin { border-top: 1px solid var(--line2); margin-top: 5px; }
.user-menu__item { padding: 6px 12px; font-family: var(--font-ui); font-size: 11.5px; color: var(--mut); display: flex; justify-content: space-between; align-items: center; gap: 8px; text-decoration: none; }
a.user-menu__item:hover { background: var(--raised); color: var(--text); }
.user-menu__item--disabled { color: var(--faint); cursor: default; }
.user-menu__signout { border-top: 1px solid var(--line); margin-top: 5px; padding: 7px 12px 9px; }
.user-menu__signout a { font-family: var(--font-ui); font-size: 11.5px; color: var(--dim); text-decoration: none; }
.user-menu__signout a:hover { color: var(--critT); }

/* ---- access & roles ---- */
.access-grid { display: grid; grid-template-columns: 1fr 2fr; gap: 14px; align-items: start; }
.access-signin { border: 1px solid var(--line); border-radius: 2px; background: var(--surface); padding: 26px 22px; display: flex; flex-direction: column; gap: 14px; align-items: center; }
.access-logo { width: 26px; height: 26px; border: 1.5px solid var(--acc); border-radius: 2px; display: flex; align-items: center; justify-content: center; color: var(--acc); font-weight: 600; font-size: 13px; font-family: var(--font-mono); }
.access-signin__title { font-family: var(--font-ui); font-size: 13px; font-weight: 600; color: var(--text); }
.access-oidc { width: 100%; border: 1px solid var(--acc); color: var(--acc2); background: var(--accbg); text-align: center; padding: 8px 0; border-radius: 2px; font-family: var(--font-mono); font-size: 11px; cursor: pointer; }
.access-signin__note { font-family: var(--font-mono); font-size: 9.5px; color: var(--faint); text-align: center; line-height: 1.7; }
.access-card { border: 1px solid var(--line); border-radius: 2px; background: var(--surface); }
.access-card__head { padding: 8px 12px; border-bottom: 1px solid var(--line); font-family: var(--font-mono); font-size: 10px; letter-spacing: .1em; color: var(--dim); }
.access-row { display: flex; align-items: center; gap: 12px; padding: 9px 12px; border-bottom: 1px solid var(--line2); font-family: var(--font-mono); }
.access-row__name { font-size: 11.5px; width: 130px; color: var(--text); }
.access-row__members { font-size: 10px; color: var(--dim); width: 70px; }
.t2-tag { font-size: 10px; border: 1px solid var(--line); color: var(--faint); padding: 1px 6px; border-radius: 1px; }
.t2-tag--reveal { border-color: var(--warn); color: var(--warnT); }
.t2-tag--audit { border-color: var(--info); color: var(--infoT); }
.access-row__scope { font-size: 10.5px; color: var(--faint); flex: 1; text-align: right; }
@media (max-width: 720px) { .access-grid { grid-template-columns: 1fr; } }

/* ---- settings › appearance ---- */
.appearance-card { border: 1px solid var(--line); border-radius: 2px; background: var(--surface); padding: 12px 14px; display: flex; flex-direction: column; gap: 10px; }
.appearance-label { font-family: var(--font-mono); font-size: 10px; letter-spacing: .1em; color: var(--dim); }
.accent-row { display: flex; gap: 10px; flex-wrap: wrap; }
.accent-swatch { display: flex; gap: 8px; align-items: center; border: 1px solid var(--line); background: transparent; padding: 6px 12px; border-radius: 2px; cursor: pointer; }
.accent-swatch:hover { border-color: var(--dim); }
.accent-swatch.is-active { border-color: var(--acc); background: var(--accbg); }
.accent-dot { width: 14px; height: 14px; border-radius: 2px; }
.accent-swatch[data-accent="#2dd4bf"] .accent-dot { background: #2dd4bf; }
.accent-swatch[data-accent="#22d3ee"] .accent-dot { background: #22d3ee; }
.accent-swatch[data-accent="#818cf8"] .accent-dot { background: #818cf8; }
.accent-name { font-family: var(--font-mono); font-size: 10.5px; color: var(--mut); letter-spacing: .06em; }
.accent-swatch.is-active .accent-name { color: var(--acc2); }
.appearance-note { font-family: var(--font-mono); font-size: 9.5px; color: var(--faint); letter-spacing: .04em; }
.settings-cards { display: grid; grid-template-columns: repeat(3, 1fr); gap: 14px; }
.settings-card { border: 1px solid var(--line); border-radius: 2px; background: var(--surface); padding: 12px 14px; display: flex; flex-direction: column; gap: 9px; }
.settings-card__label { font-family: var(--font-mono); font-size: 10px; letter-spacing: .1em; color: var(--dim); }
.settings-card__skel { height: 10px; border-radius: 1px; background: linear-gradient(90deg, var(--shimA) 25%, var(--shimB) 50%, var(--shimA) 75%); background-size: 800px 100%; animation: gov-shimmer 1.6s linear infinite; }
.settings-card__skel.w75 { width: 75%; }
.settings-card__skel.w50 { width: 50%; }
.settings-card__skel.w62 { width: 62%; }
@keyframes gov-shimmer { 0% { background-position: -400px 0; } 100% { background-position: 400px 0; } }
@media (max-width: 720px) { .settings-cards { grid-template-columns: 1fr; } }
```

- [ ] **Step 2: Write the failing web render test.** Add `web/audit_render_test.go`:
```go
package web

import (
	"context"
	"strings"
	"testing"
)

func renderAuditPage(t *testing.T, chain AuditChain, rows []AuditRow) string {
	t.Helper()
	var sb strings.Builder
	if err := AuditPage(chain, AuditFilterValues{}, rows).Render(context.Background(), &sb); err != nil {
		t.Fatalf("render: %v", err)
	}
	return sb.String()
}

func TestAuditPage_bannerAndColumns(t *testing.T) {
	// NOTE: pass a non-empty row set. AuditTable renders the header grid
	// (>TARGET</>HASH<) only in the len(rows)>0 branch; with nil rows it emits
	// just the "No audit entries" note and the header asserts below would FAIL.
	rows := []AuditRow{
		{ID: 1, Actor: "alice", Action: "read", ServerID: "srv-1", DataTier: 2, Target: "orders.pg_stat_statements", HashShort: "a71f…9c2", IsT2: true, At: "2026-07-10T00:00:00Z"},
	}
	html := renderAuditPage(t, AuditChain{Verified: true, TipShort: "c4b7…2ef", Count: 42}, rows)
	for _, want := range []string{
		`href="/static/css/governance.css"`,
		`class="badge badge--live"`,
		"HASH CHAIN VERIFIED",
		"c4b7…2ef",
		"42 EVENTS",
		"TAMPER-EVIDENT",
		">TARGET<", ">HASH<", // new column headers (require ≥1 row to render)
	} {
		if !strings.Contains(html, want) {
			t.Errorf("audit page missing %q", want)
		}
	}
}

func TestAuditPage_brokenBanner(t *testing.T) {
	html := renderAuditPage(t, AuditChain{Verified: false, TipShort: "dead…f00", Count: 9}, nil)
	if !strings.Contains(html, "chain-banner--broken") || !strings.Contains(html, "HASH CHAIN BROKEN") {
		t.Error("broken chain must render the crit banner variant")
	}
}

func TestAuditTable_t2Striping(t *testing.T) {
	rows := []AuditRow{
		{ID: 2, Actor: "alice", Action: "read", ServerID: "srv-1", DataTier: 2, Target: "orders.pg_stat_statements", HashShort: "a71f…9c2", IsT2: true, At: "2026-07-10T00:00:00Z"},
		{ID: 1, Actor: "bob", Action: "login", ServerID: "srv-2", DataTier: 0, HashShort: "0011…abc", At: "2026-07-10T00:00:00Z"},
	}
	var sb strings.Builder
	if err := AuditTable(rows).Render(context.Background(), &sb); err != nil {
		t.Fatalf("render: %v", err)
	}
	html := sb.String()
	if !strings.Contains(html, "audit-row--t2") {
		t.Error("T2 row must carry the amber-stripe class")
	}
	if !strings.Contains(html, "orders.pg_stat_statements") {
		t.Error("TARGET column must render the derived target")
	}
	if !strings.Contains(html, "a71f…9c2") {
		t.Error("HASH column must render the short hash")
	}
	if !strings.Contains(html, ">T2<") || !strings.Contains(html, ">—<") {
		t.Error("tier badge must show T2 for tier 2 and — for tier 0")
	}
	if !strings.Contains(html, `id="audit-table"`) {
		t.Error("swap-target id must be preserved")
	}
}
```

- [ ] **Step 3: Run the test — expect FAIL (compile error).** `AuditChain`, the new `AuditRow` fields, and the new `AuditPage` signature do not exist yet.
```
go test ./web/ -run 'TestAudit' 2>&1 | head
```
Expected: build failure — `undefined: AuditChain` / `too many arguments in call to AuditPage` / `unknown field HashShort`.

- [ ] **Step 4: Rebuild `web/audit.templ`.** Replace the whole file with:
```templ
package web

import "fmt"

// AuditChain drives the page-level tamper-evidence banner. It reflects a
// whole-chain VerifyChain result plus the current chain tip; it is NOT part
// of the HTMX-swapped table fragment.
type AuditChain struct {
	Verified bool
	TipShort string
	Count    int64
}

// AuditRow is the view-model for one rendered audit-log row. Target is derived
// from the Detail structural keys (T1-safe — never a monitored-DB literal);
// HashShort is a short render of the chain row hash; IsT2 marks tier-2 reads.
type AuditRow struct {
	ID        int64
	Actor     string
	Action    string
	ServerID  string
	DataTier  int16
	Detail    string
	At        string
	Target    string
	HashShort string
	IsT2      bool
}

// AuditFilterValues echoes the active filter back into the form.
type AuditFilterValues struct {
	Actor    string
	Action   string
	ServerID string
	Since    string
	Until    string
	Tier     string
}

// AuditPage is the full filterable audit-log page: LIVE header, verified
// hash-chain banner, tamper subtitle, the (retained) filter form, and the grid.
templ AuditPage(chain AuditChain, f AuditFilterValues, rows []AuditRow) {
	@Layout("Lynceus — audit log", "tamper-evident audit log") {
		<link rel="stylesheet" href="/static/css/governance.css"/>
		<div class="gov-page">
			<div class="gov-head">
				<span class="gov-title">Audit Log</span>
				<span class="badge badge--live">LIVE</span>
				<span class="gov-spacer"></span>
				@auditBanner(chain)
			</div>
			<div class="gov-note">TAMPER-EVIDENT: EACH EVENT'S HASH COVERS THE PREVIOUS. EVERY T2 READ APPEARS HERE — NO EXCEPTIONS.</div>
			@AuditFilterForm(f)
			@AuditTable(rows)
		</div>
	}
}

templ auditBanner(chain AuditChain) {
	if chain.Verified {
		<span class="chain-banner">{ fmt.Sprintf("✓ HASH CHAIN VERIFIED · TIP %s · %d EVENTS", chain.TipShort, chain.Count) }</span>
	} else {
		<span class="chain-banner chain-banner--broken">{ fmt.Sprintf("✗ HASH CHAIN BROKEN · TIP %s · %d EVENTS", chain.TipShort, chain.Count) }</span>
	}
}

// AuditFilterForm — retained bonus (beyond the design). Restyle deferred to the
// shape pass (ly-ae6.14); functionally unchanged.
templ AuditFilterForm(f AuditFilterValues) {
	<form class="filters" action="/audit" method="get"
		hx-get="/partial/audit" hx-target="#audit-table" hx-swap="outerHTML">
		<label>
			Actor
			<input type="text" name="actor" value={ f.Actor } placeholder="any"/>
		</label>
		<label>
			Action
			<input type="text" name="action" value={ f.Action } placeholder="any"/>
		</label>
		<label>
			Server
			<input type="text" name="server" value={ f.ServerID } placeholder="any"/>
		</label>
		<label>
			Tier
			<select name="tier">
				<option value="" selected?={ f.Tier == "" }>Any</option>
				<option value="1" selected?={ f.Tier == "1" }>T1</option>
				<option value="2" selected?={ f.Tier == "2" }>T2</option>
			</select>
		</label>
		<label>
			From
			<input type="date" name="since" value={ f.Since }/>
		</label>
		<label>
			To
			<input type="date" name="until" value={ f.Until }/>
		</label>
		<button type="submit">Filter</button>
	</form>
}

// AuditTable renders just the results grid (the HTMX swap target).
templ AuditTable(rows []AuditRow) {
	<div id="audit-table">
		if len(rows) == 0 {
			<p class="gov-note">No audit entries match the current filter.</p>
		} else {
			<div class="audit-card">
				<div class="audit-grid audit-grid--head">
					<span>TIMESTAMP</span>
					<span>ACTOR</span>
					<span>ACTION</span>
					<span>TARGET</span>
					<span>SERVER</span>
					<span>TIER</span>
					<span>HASH</span>
				</div>
				for _, r := range rows {
					<div class={ auditRowClass(r) }>
						<span class="c-ts">{ r.At }</span>
						<span class="c-actor">{ r.Actor }</span>
						<span class="c-action">{ r.Action }</span>
						<span class="c-target">{ r.Target }</span>
						<span class="c-server">{ r.ServerID }</span>
						<span class="tier-badge">{ tierLabel(r.DataTier) }</span>
						<span class="c-hash">{ r.HashShort }</span>
					</div>
				}
			</div>
		}
	</div>
}

func auditRowClass(r AuditRow) string {
	if r.IsT2 {
		return "audit-grid audit-row audit-row--t2"
	}
	return "audit-grid audit-row"
}

func tierLabel(t int16) string {
	switch t {
	case 1:
		return "T1"
	case 2:
		return "T2"
	default:
		return "—"
	}
}
```

- [ ] **Step 5: Regenerate templ, run the web test — expect PASS.**
```
make templ
go test ./web/ -run 'TestAudit' -v
```
Expected: `TestAuditPage_bannerAndColumns`, `TestAuditPage_brokenBanner`, `TestAuditTable_t2Striping` PASS. (The handler `internal/api/audit.go` still calls the old 2-arg `AuditPage` and will NOT compile yet — that's Step 6/7.)

- [ ] **Step 6: Add `VerifyChain` to the `Config` interface.** In `internal/store/config.go`, inside `type Config interface { … }`, add after the `ListAudit` line:
```go
	VerifyChain(ctx context.Context, since, until time.Time) (int, string, error)
```
`*pgxConfig` already implements it; the only implementer is `pgxConfig` (`var _ Config = (*pgxConfig)(nil)`), so this compiles.

- [ ] **Step 7: Update the handler.** Rewrite `internal/api/audit.go`:
```go
package api

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/dobbo-ca/lynceus/web"
)

// dateLayout is the format produced by HTML <input type="date">.
const dateLayout = "2006-01-02"

func (s *Server) handleAuditPage(w http.ResponseWriter, r *http.Request) {
	values, rows := s.fetchAudit(r)
	chain := s.auditChain(r.Context())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.AuditPage(chain, values, rows).Render(r.Context(), w)
}

func (s *Server) handleAuditPartial(w http.ResponseWriter, r *http.Request) {
	_, rows := s.fetchAudit(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.AuditTable(rows).Render(r.Context(), w)
}

// auditChain computes the page-level tamper-evidence banner: whole-chain
// verification plus the current tip (hash + id-as-count, valid because audit
// ids are contiguous from 1).
func (s *Server) auditChain(ctx context.Context) web.AuditChain {
	idx, _, err := s.conf.VerifyChain(ctx, time.Time{}, time.Time{})
	ch := web.AuditChain{Verified: err == nil && idx == -1, TipShort: "—"}
	tip, err := s.conf.ListAudit(ctx, store.AuditFilter{Limit: 1})
	if err == nil && len(tip) > 0 {
		ch.TipShort = hashShort(tip[0].RowHash)
		ch.Count = tip[0].ID
	}
	return ch
}

func (s *Server) fetchAudit(r *http.Request) (web.AuditFilterValues, []web.AuditRow) {
	q := r.URL.Query()
	values := web.AuditFilterValues{
		Actor:    q.Get("actor"),
		Action:   q.Get("action"),
		ServerID: q.Get("server"),
		Since:    q.Get("since"),
		Until:    q.Get("until"),
		Tier:     q.Get("tier"),
	}

	filter := store.AuditFilter{
		Actor:    values.Actor,
		Action:   values.Action,
		ServerID: values.ServerID,
		Limit:    200,
	}
	if t, err := time.Parse(dateLayout, values.Since); err == nil {
		filter.Since = t
	}
	if t, err := time.Parse(dateLayout, values.Until); err == nil {
		filter.Until = t.Add(24*time.Hour - time.Nanosecond)
	}
	if n, err := strconv.Atoi(values.Tier); err == nil && (n == 1 || n == 2) {
		tier := int16(n)
		filter.Tier = &tier
	}

	recs, err := s.conf.ListAudit(r.Context(), filter)
	if err != nil {
		return values, nil
	}
	out := make([]web.AuditRow, 0, len(recs))
	for i := range recs {
		rec := &recs[i]
		out = append(out, web.AuditRow{
			ID:        rec.ID,
			Actor:     rec.Actor,
			Action:    rec.Action,
			ServerID:  rec.ServerID,
			DataTier:  rec.DataTier,
			Detail:    string(rec.Detail),
			At:        rec.At.UTC().Format(time.RFC3339),
			Target:    auditTarget(rec.Detail),
			HashShort: hashShort(rec.RowHash),
			IsT2:      rec.DataTier == 2,
		})
	}
	return values, out
}

// auditTarget builds the acted-upon object label from the audit Detail's
// closed-vocabulary structural keys. It never surfaces a monitored-DB literal.
func auditTarget(detail []byte) string {
	if len(detail) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(detail, &m); err != nil {
		return ""
	}
	db, _ := m["database_name"].(string)
	capName, _ := m["capability"].(string)
	switch {
	case db != "" && capName != "":
		return db + "." + capName
	case capName != "":
		return capName
	case db != "":
		return db
	default:
		return ""
	}
}

// hashShort renders a 32-byte chain hash as "abcd…f01" (first4…last3 hex).
func hashShort(h []byte) string {
	if len(h) == 0 {
		return "—"
	}
	s := hex.EncodeToString(h)
	if len(s) < 7 {
		return s
	}
	return s[:4] + "…" + s[len(s)-3:]
}
```

- [ ] **Step 8: Write the failing integration test.** Add to `internal/api/audit_test.go`:
```go
func TestAuditPage_showsChainBannerTargetAndT2Stripe(t *testing.T) {
	pool, srv := setupAudit(t, api.Config{DevAuth: true})
	seedAudit(t, pool) // login(T0), viewed.t2(T2), config.toggle(T1)

	// Append a T2 read whose Detail carries structural keys -> derived TARGET.
	cfg := store.NewConfig(pool)
	if _, err := cfg.AppendAuditReturning(context.Background(), store.AuditEntry{
		Actor: "carol", Action: "read", ServerID: "srv-9", DataTier: 2,
		Detail: map[string]any{"database_name": "orders", "capability": "pg_stat_statements"},
	}); err != nil {
		t.Fatalf("seed detail row: %v", err)
	}

	resp, err := http.Get(srv.URL + "/audit")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	html := string(body)

	for _, want := range []string{
		"HASH CHAIN VERIFIED",       // banner, whole-chain intact
		"4 EVENTS",                  // tip.ID == 4 after 3 seeds + 1 append
		"audit-row--t2",             // amber striping present
		"orders.pg_stat_statements", // derived TARGET (T1-safe)
		">T2<",                      // tier badge
	} {
		if !strings.Contains(html, want) {
			t.Errorf("audit page missing %q", want)
		}
	}
}
```
Add imports to the test file: `"context"` and `"github.com/dobbo-ca/lynceus/internal/store"` (the file already imports `io`, `net/http`, `strings`, `testing`, `github.com/dobbo-ca/lynceus/internal/api`).

- [ ] **Step 9: Build + run — expect PASS.**
```
go build ./...
go test ./web/ ./internal/store/ -run 'TestAudit|TestVerifyChain|Config' -count=1
go test ./internal/api/ -run 'Audit' -count=1 -v
```
Expected: new + existing audit tests PASS (`TestAuditPage_rendersRowsAndNav`, `TestAuditPartial_*`, `TestAuditPage_withoutDevAuth_returns401` still green; nav `href="/audit"` and seeded strings preserved).

- [ ] **Step 10: Add the static-asset test + commit.** In `web/static_test.go`, extend the cases slice in `TestStaticHandler_ServesThemeJSAndLegacyCSS` with:
```go
		{"/static/css/governance.css", ".audit-row--t2"},
		{"/static/css/governance.css", ".chain-banner"},
```
Run `go test ./web/ -run TestStaticHandler -count=1`, expect PASS. Then:
```
git add web/audit.templ web/audit_templ.go web/audit_render_test.go web/static/css/governance.css web/static_test.go internal/store/config.go internal/api/audit.go internal/api/audit_test.go
git commit -m "audit log: hash-chain banner, TARGET/HASH columns, T2 amber striping on design tokens (ly-ae6.13)"
```

---

### Task 2: User menu fragment (governance/admin dropdown)

Delivers the top-right avatar dropdown as a standalone, JS-free `<details>` fragment that the ly-ae6.2 top bar mounts: identity header (`username` + `GROUP · T2 GRANTED/DENIED`), GOVERNANCE section (Audit Log, Access & Roles), ADMIN section (Provider Setup, Collectors, Data & Retention, Settings), and Sign out. Uses the `.user-menu*` classes already in `governance.css`.

**Files**
- create `web/user_menu.templ` — `UserMenuVM`, `UserMenuItem`, `UserMenu`/`userMenuItem` components, `NewUserMenuVM`, `initials`, `userMenuMeta`.
- create `web/user_menu_templ.go` — regenerated by `make templ`.
- test: create `web/user_menu_test.go`.

**Interfaces**

Produces:
```go
// web/user_menu.templ (package web)
type UserMenuItem struct {
	Label string
	Href  string // "" => rendered disabled (page not built yet)
	Soon  bool   // adds the SOON badge
}
type UserMenuVM struct {
	Username   string
	Group      string
	T2Granted  bool
	Governance []UserMenuItem
	Admin      []UserMenuItem
}
templ UserMenu(vm UserMenuVM)
func NewUserMenuVM(username, group string, t2Granted bool) UserMenuVM
```

Integration (ly-ae6.2 top bar, one line at top-right): `@web.UserMenu(web.NewUserMenuVM(actorFromContext(r), group, t2Granted))` where `group`/`t2Granted` are dev placeholders until ly-8b0.4. No JS needed — the native `<details>` toggles the panel.

**Steps**

- [ ] **Step 1: Write the failing test.** Add `web/user_menu_test.go`:
```go
package web

import (
	"context"
	"strings"
	"testing"
)

func renderUserMenu(t *testing.T, vm UserMenuVM) string {
	t.Helper()
	var sb strings.Builder
	if err := UserMenu(vm).Render(context.Background(), &sb); err != nil {
		t.Fatalf("render: %v", err)
	}
	return sb.String()
}

func TestNewUserMenuVM_sectionsAndHrefs(t *testing.T) {
	vm := NewUserMenuVM("s.dobson", "dba-oncall", true)
	if len(vm.Governance) != 2 || vm.Governance[0].Href != "/audit" || vm.Governance[1].Href != "/access" {
		t.Fatalf("governance items wrong: %+v", vm.Governance)
	}
	// Settings is reachable; Provider Setup/Collectors/Retention are placeholders (no Href yet).
	var settings, providerSetup UserMenuItem
	for _, it := range vm.Admin {
		switch it.Label {
		case "Settings":
			settings = it
		case "Provider Setup":
			providerSetup = it
		}
	}
	if settings.Href != "/settings" {
		t.Errorf("Settings href = %q, want /settings", settings.Href)
	}
	if providerSetup.Href != "" {
		t.Errorf("Provider Setup must be a placeholder until ly-ae6.12; href = %q", providerSetup.Href)
	}
}

func TestUserMenu_rendersIdentityAndItems(t *testing.T) {
	html := renderUserMenu(t, NewUserMenuVM("s.dobson", "dba-oncall", true))
	for _, want := range []string{
		`<details class="user-menu">`,
		">SD<",                              // initials in the summary
		"s.dobson",                          // identity name
		"GROUP: DBA-ONCALL · T2 GRANTED",    // meta line, group upcased
		">GOVERNANCE<", ">ADMIN<",           // section headers
		`href="/audit"`, `href="/access"`, `href="/settings"`,
		"Sign out",
		`class="badge badge--soon"`,         // SOON badges present
	} {
		if !strings.Contains(html, want) {
			t.Errorf("user menu missing %q", want)
		}
	}
}

func TestUserMenu_t2DeniedAndPlaceholder(t *testing.T) {
	html := renderUserMenu(t, NewUserMenuVM("dev-admin", "platform", false))
	if !strings.Contains(html, "T2 DENIED") {
		t.Error("t2Granted=false must render T2 DENIED")
	}
	if !strings.Contains(html, "user-menu__item--disabled") {
		t.Error("items without an Href must render disabled (Provider Setup/Collectors/Retention)")
	}
	if !strings.Contains(html, ">DA<") {
		t.Error("initials for dev-admin should be DA")
	}
}
```

- [ ] **Step 2: Run — expect FAIL (compile error).**
```
go test ./web/ -run 'UserMenu' 2>&1 | head
```
Expected: `undefined: UserMenu` / `undefined: NewUserMenuVM`.

- [ ] **Step 3: Implement `web/user_menu.templ`.**
```templ
package web

import "strings"

// UserMenuItem is one entry in a user-menu section. An empty Href renders a
// disabled placeholder (the target page is not built on this bead).
type UserMenuItem struct {
	Label string
	Href  string
	Soon  bool
}

// UserMenuVM drives the top-right avatar dropdown. The ly-ae6.2 top bar mounts
// @UserMenu(vm); build vm with NewUserMenuVM.
type UserMenuVM struct {
	Username   string
	Group      string
	T2Granted  bool
	Governance []UserMenuItem
	Admin      []UserMenuItem
}

// NewUserMenuVM assembles the standard governance/admin menu. Group and
// t2Granted are dev placeholders until RBAC (ly-8b0.4) + SCIM/OIDC land.
//
// SOON-BADGE SEMANTICS (design-faithful, deliberate): the SOON badge marks
// "roadmap-preview / not-yet-complete content", NOT "unreachable". It is
// orthogonal to the link's Href. This matches the design source, where the
// reachable Access & Roles (go('access'), soon:true) and Settings
// (go('settings'), soon:true) entries BOTH navigate AND show SOON
// (Lynceus.dc.html lines 2543-2551). So on this bead:
//   - Access & Roles → Href "/access" (Task 3 builds it) + SOON (it is a
//     ROADMAP-preview page: illustrative RBAC rows, no live backend yet).
//   - Settings → Href "/settings" (Task 4 builds it) + SOON (the accent picker
//     is live, but org/integration cards are roadmap previews).
//   - Provider Setup / Collectors / Data & Retention → no Href (pages not built
//     on this bead) + SOON. These render disabled; the design has them
//     reachable, but their pages are separate beads (Provider Setup ly-ae6.12,
//     Collectors/Retention TBD), so here they are disabled placeholders.
func NewUserMenuVM(username, group string, t2Granted bool) UserMenuVM {
	return UserMenuVM{
		Username:  username,
		Group:     group,
		T2Granted: t2Granted,
		Governance: []UserMenuItem{
			{Label: "Audit Log", Href: "/audit"},                 // live, complete → no SOON
			{Label: "Access & Roles", Href: "/access", Soon: true}, // reachable ROADMAP preview
		},
		Admin: []UserMenuItem{
			{Label: "Provider Setup", Soon: true}, // page: ly-ae6.12 (disabled placeholder here)
			{Label: "Collectors", Soon: true},     // page: not yet built (disabled placeholder)
			{Label: "Data & Retention", Soon: true}, // page: not yet built (disabled placeholder)
			{Label: "Settings", Href: "/settings", Soon: true}, // reachable; accent picker live + roadmap cards
		},
	}
}

// initials derives up-to-2 uppercase letters from a username for the avatar.
func initials(name string) string {
	fields := strings.FieldsFunc(name, func(r rune) bool {
		return r == '.' || r == ' ' || r == '-' || r == '_'
	})
	var b strings.Builder
	for _, f := range fields {
		if f == "" {
			continue
		}
		b.WriteByte(f[0])
		if b.Len() == 2 {
			break
		}
	}
	s := strings.ToUpper(b.String())
	if s == "" {
		return "?"
	}
	return s
}

// userMenuMeta renders the "GROUP: X · T2 GRANTED/DENIED" identity subline.
func userMenuMeta(vm UserMenuVM) string {
	t2 := "T2 DENIED"
	if vm.T2Granted {
		t2 = "T2 GRANTED"
	}
	return "GROUP: " + strings.ToUpper(vm.Group) + " · " + t2
}

templ UserMenu(vm UserMenuVM) {
	<details class="user-menu">
		<summary>{ initials(vm.Username) }</summary>
		<div class="user-menu__panel">
			<div class="user-menu__id">
				<span class="user-menu__name">{ vm.Username }</span>
				<span class="user-menu__meta">{ userMenuMeta(vm) }</span>
			</div>
			<div class="user-menu__section">GOVERNANCE</div>
			for _, it := range vm.Governance {
				@userMenuItem(it)
			}
			<div class="user-menu__section user-menu__section--admin">ADMIN</div>
			for _, it := range vm.Admin {
				@userMenuItem(it)
			}
			<div class="user-menu__signout"><a href="/signout">Sign out</a></div>
		</div>
	</details>
}

// userMenuItem renders one entry. The SOON badge is driven ONLY by it.Soon and
// is orthogonal to reachability (matching the design, which shows SOON on the
// reachable Access & Roles and Settings entries — see the SOON-semantics note
// on NewUserMenuVM). Reachability is driven ONLY by it.Href: an empty Href
// renders a disabled span, a non-empty Href renders a real link.
templ userMenuItem(it UserMenuItem) {
	if it.Href != "" {
		<a class="user-menu__item" href={ templ.SafeURL(it.Href) }>
			<span>{ it.Label }</span>
			if it.Soon {
				<span class="badge badge--soon">SOON</span>
			}
		</a>
	} else {
		<span class="user-menu__item user-menu__item--disabled">
			<span>{ it.Label }</span>
			if it.Soon {
				<span class="badge badge--soon">SOON</span>
			}
		</span>
	}
}
```

- [ ] **Step 4: Regenerate + run — expect PASS.**
```
make templ
go test ./web/ -run 'UserMenu' -v
```
Expected: the three tests PASS.

- [ ] **Step 5: Commit.**
```
git add web/user_menu.templ web/user_menu_templ.go web/user_menu_test.go
git commit -m "user menu: JS-free governance/admin avatar dropdown fragment for the top bar (ly-ae6.13)"
```

---

### Task 3: Access & Roles page (ROADMAP preview)

Adds `GET /access`: the design's roadmap-preview screen — ROADMAP badge, OIDC sign-in card, and the `RBAC GROUPS → T2 RIGHTS` table. Data is an illustrative preview until ly-8b0.4 (RBAC) + ly-8b0.2 (SCIM) populate the `AccessGroupRow` shape.

**Files**
- create `web/access.templ` — `AccessRolesVM`, `AccessGroupRow`, `AccessRolesPage`, `t2TagClass`.
- create `web/access_templ.go` — regenerated by `make templ`.
- create `internal/api/access.go` — `handleAccessPage`.
- modify `internal/api/server.go` — register `GET /access`.
- test: create `web/access_test.go`.
- test: create `internal/api/access_test.go`.

**Interfaces**

Produces:
```go
// web/access.templ (package web)
type AccessGroupRow struct {
	Name    string
	Members string
	T2Label string
	T2Kind  string // "reveal" | "none" | "audit"
	Scope   string
}
type AccessRolesVM struct { Groups []AccessGroupRow }
templ AccessRolesPage(vm AccessRolesVM)
// internal/api/access.go
func (s *Server) handleAccessPage(w http.ResponseWriter, r *http.Request)
```

**Steps**

- [ ] **Step 1: Write the failing web render test.** Add `web/access_test.go`:
```go
package web

import (
	"context"
	"strings"
	"testing"
)

func TestAccessRolesPage_previewAndGroups(t *testing.T) {
	vm := AccessRolesVM{Groups: []AccessGroupRow{
		{Name: "dba-oncall", Members: "4 users", T2Label: "T2: REVEAL", T2Kind: "reveal", Scope: "orders-prod only"},
		{Name: "security-audit", Members: "2 users", T2Label: "T2: AUDIT VIEW", T2Kind: "audit", Scope: "audit log only"},
	}}
	var sb strings.Builder
	if err := AccessRolesPage(vm).Render(context.Background(), &sb); err != nil {
		t.Fatalf("render: %v", err)
	}
	html := sb.String()
	for _, want := range []string{
		`href="/static/css/governance.css"`,
		`class="badge badge--roadmap"`,
		"ROADMAP PREVIEW",
		"CONTINUE WITH OIDC SSO",
		"SCIM 2.0",
		"RBAC GROUPS → T2 RIGHTS",
		"dba-oncall", "T2: REVEAL",
		"t2-tag--reveal", "t2-tag--audit",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("access page missing %q", want)
		}
	}
}
```

- [ ] **Step 2: Run — expect FAIL (compile error).** `go test ./web/ -run 'AccessRoles' 2>&1 | head` → `undefined: AccessRolesVM`.

- [ ] **Step 3: Implement `web/access.templ`.**
```templ
package web

// AccessGroupRow is one RBAC group's T2 rights. Shape matches what ly-8b0.4
// (RBAC) + ly-8b0.2 (SCIM) will populate; today it drives a roadmap preview.
type AccessGroupRow struct {
	Name    string
	Members string
	T2Label string
	T2Kind  string
	Scope   string
}

type AccessRolesVM struct {
	Groups []AccessGroupRow
}

templ AccessRolesPage(vm AccessRolesVM) {
	@Layout("Lynceus — access & roles", "OIDC, SCIM users, and RBAC groups governing T2 access") {
		<link rel="stylesheet" href="/static/css/governance.css"/>
		<div class="gov-page">
			<div class="gov-head">
				<span class="gov-title">Access &amp; Roles</span>
				<span class="badge badge--roadmap">ROADMAP</span>
			</div>
			<div class="gov-roadmap-strip">◧ ROADMAP PREVIEW — <em>OIDC login, SCIM-provisioned users, and RBAC groups governing T2 access per server.</em></div>
			<div class="access-grid">
				<div class="access-signin">
					<div class="access-logo">L</div>
					<span class="access-signin__title">Sign in to Lynceus</span>
					<div class="access-oidc">CONTINUE WITH OIDC SSO →</div>
					<span class="access-signin__note">NO LOCAL PASSWORDS. USERS PROVISIONED VIA SCIM 2.0.</span>
				</div>
				<div class="access-card">
					<div class="access-card__head">RBAC GROUPS → T2 RIGHTS</div>
					for _, g := range vm.Groups {
						<div class="access-row">
							<span class="access-row__name">{ g.Name }</span>
							<span class="access-row__members">{ g.Members }</span>
							<span class={ t2TagClass(g.T2Kind) }>{ g.T2Label }</span>
							<span class="access-row__scope">{ g.Scope }</span>
						</div>
					}
				</div>
			</div>
		</div>
	}
}

func t2TagClass(kind string) string {
	switch kind {
	case "reveal":
		return "t2-tag t2-tag--reveal"
	case "audit":
		return "t2-tag t2-tag--audit"
	default:
		return "t2-tag"
	}
}
```

- [ ] **Step 4: Regenerate + run web test — expect PASS.**
```
make templ
go test ./web/ -run 'AccessRoles' -v
```

- [ ] **Step 5: Implement the handler `internal/api/access.go`.**
```go
package api

import (
	"net/http"

	"github.com/dobbo-ca/lynceus/web"
)

// handleAccessPage renders the Access & Roles roadmap preview. The group rows
// are illustrative until RBAC (ly-8b0.4) + SCIM (ly-8b0.2) provide real data.
func (s *Server) handleAccessPage(w http.ResponseWriter, r *http.Request) {
	vm := web.AccessRolesVM{
		Groups: []web.AccessGroupRow{
			{Name: "dba-oncall", Members: "4 users", T2Label: "T2: REVEAL", T2Kind: "reveal", Scope: "orders-prod only"},
			{Name: "platform", Members: "11 users", T2Label: "T2: NONE", T2Kind: "none", Scope: "fleet, read"},
			{Name: "security-audit", Members: "2 users", T2Label: "T2: AUDIT VIEW", T2Kind: "audit", Scope: "audit log only"},
		},
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.AccessRolesPage(vm).Render(r.Context(), w)
}
```

- [ ] **Step 6: Register the route.** In `internal/api/server.go` `routes()`, add next to the audit routes:
```go
	s.mux.HandleFunc("GET /access", s.handleAccessPage)
```

- [ ] **Step 7: Write the failing integration test.** Add `internal/api/access_test.go`:
```go
package api_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/api"
)

func TestAccessPage_devAuth_rendersPreview(t *testing.T) {
	_, srv := setup(t, api.Config{DevAuth: true})
	resp, err := http.Get(srv.URL + "/access")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	html := string(body)
	for _, want := range []string{"<!doctype html>", "Access &amp; Roles", "CONTINUE WITH OIDC SSO", "dba-oncall", "security-audit"} {
		if !strings.Contains(html, want) {
			t.Errorf("/access missing %q", want)
		}
	}
}

func TestAccessPage_withoutDevAuth_returns401(t *testing.T) {
	_, srv := setup(t, api.Config{DevAuth: false})
	resp, err := http.Get(srv.URL + "/access")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}
```

- [ ] **Step 8: Build + run — expect PASS.**
```
go build ./...
go test ./internal/api/ -run 'AccessPage' -count=1 -v
```

- [ ] **Step 9: Commit.**
```
git add web/access.templ web/access_templ.go web/access_test.go internal/api/access.go internal/api/access_test.go internal/api/server.go
git commit -m "access & roles: OIDC + RBAC-groups roadmap preview page at /access on design tokens (ly-ae6.13)"
```

---

### Task 4: Settings › Appearance page (accent picker)

Adds `GET /settings`: the `APPEARANCE — ACCENT COLOR` picker (Teal/Cyan/Indigo) wired to the F1 `window.Lynceus.setAccent`, the per-profile caption, and the ROADMAP preview cards. Persistence is interim (localStorage via F1); server-side per-user storage awaits the M5 users table.

> **ACCEPTANCE DEVIATION — accent is NOT "persisted per user" on this bead.** Bead ly-ae6.13's acceptance criterion reads "accent presets persisted **per user**". This bead delivers only **per-browser localStorage** persistence (via the F1 `setAccent` → `localStorage['lynceus.accent']`), NOT per-user server-side persistence. True per-user persistence requires a users table + authenticated identity to key on, which does not exist until M5 (auth is unstarted; `actorFromContext` returns a static `"dev-admin"`). No store table is added here. The picker UI, the three presets, the per-theme variant behavior, and the "SAVED TO YOUR PROFILE" caption are all delivered; the word "PROFILE" in that caption is aspirational until M5 wires a real profile store. **Reviewers must accept this reduced scope** — the literal "per user" clause is explicitly out of scope for ly-ae6.13 and is carried by the M5 users-model work.

**Files**
- create `web/settings.templ` — `SettingsVM`, `AccentSwatch`, `SettingsRoadmapCard`, `SettingsPage`.
- create `web/settings_templ.go` — regenerated by `make templ`.
- create `web/static/js/settings.js` — accent-picker enhancement (marks active, calls `setAccent`).
- create `internal/api/settings.go` — `handleSettingsPage`.
- modify `internal/api/server.go` — register `GET /settings`.
- test: create `web/settings_test.go`.
- test: modify `web/static_test.go` — assert `settings.js` served + contents.
- test: create `internal/api/settings_test.go`.

**Interfaces**

Produces:
```go
// web/settings.templ (package web)
type AccentSwatch struct { Hex, Name string }
type SettingsRoadmapCard struct { Label string }
type SettingsVM struct {
	Accents      []AccentSwatch
	RoadmapCards []SettingsRoadmapCard
}
templ SettingsPage(vm SettingsVM)
// internal/api/settings.go
func (s *Server) handleSettingsPage(w http.ResponseWriter, r *http.Request)
```
Consumes (F1, existing): `window.Lynceus.setAccent('#hex')` in `web/static/js/theme.js`; accent variant map in `web/bootstrap.go`.

**Steps**

- [ ] **Step 1: Write the failing web render test.** Add `web/settings_test.go`:
```go
package web

import (
	"context"
	"strings"
	"testing"
)

func TestSettingsPage_accentPickerAndRoadmap(t *testing.T) {
	vm := SettingsVM{
		Accents:      []AccentSwatch{{Hex: "#2dd4bf", Name: "TEAL"}, {Hex: "#22d3ee", Name: "CYAN"}, {Hex: "#818cf8", Name: "INDIGO"}},
		RoadmapCards: []SettingsRoadmapCard{{Label: "ORGANIZATION"}, {Label: "THEME DEFAULTS"}, {Label: "INTEGRATIONS"}},
	}
	var sb strings.Builder
	if err := SettingsPage(vm).Render(context.Background(), &sb); err != nil {
		t.Fatalf("render: %v", err)
	}
	html := sb.String()
	for _, want := range []string{
		`href="/static/css/governance.css"`,
		`src="/static/js/settings.js"`,
		"APPEARANCE — ACCENT COLOR",
		`data-accent="#2dd4bf"`, `data-accent="#22d3ee"`, `data-accent="#818cf8"`,
		">TEAL<", ">CYAN<", ">INDIGO<",
		"SAVED TO YOUR PROFILE",
		"ORGANIZATION", "THEME DEFAULTS", "INTEGRATIONS",
		`class="badge badge--roadmap"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("settings page missing %q", want)
		}
	}
}
```

- [ ] **Step 2: Run — expect FAIL (compile error).** `go test ./web/ -run 'SettingsPage' 2>&1 | head` → `undefined: SettingsVM`.

- [ ] **Step 3: Implement `web/settings.templ`.**
```templ
package web

// AccentSwatch is one selectable accent preset. Hex must be one of the F1
// presets (#2dd4bf/#22d3ee/#818cf8) whose per-theme variants live in bootstrap.go.
type AccentSwatch struct {
	Hex  string
	Name string
}

type SettingsRoadmapCard struct {
	Label string
}

type SettingsVM struct {
	Accents      []AccentSwatch
	RoadmapCards []SettingsRoadmapCard
}

templ SettingsPage(vm SettingsVM) {
	@Layout("Lynceus — settings", "appearance and org settings") {
		<link rel="stylesheet" href="/static/css/governance.css"/>
		<div class="gov-page">
			<div class="gov-head">
				<span class="gov-title">Settings</span>
				<span class="badge badge--roadmap">ROADMAP</span>
			</div>
			<div class="appearance-card">
				<span class="appearance-label">APPEARANCE — ACCENT COLOR</span>
				<div class="accent-row">
					for _, a := range vm.Accents {
						<button type="button" class="accent-swatch" data-accent={ a.Hex } aria-pressed="false">
							<span class="accent-dot"></span>
							<span class="accent-name">{ a.Name }</span>
						</button>
					}
				</div>
				<span class="appearance-note">SAVED TO YOUR PROFILE · ADAPTS AUTOMATICALLY TO DARK AND LIGHT THEMES</span>
			</div>
			<div class="gov-roadmap-strip">◧ ROADMAP PREVIEW — <em>org settings and integrations.</em></div>
			<div class="settings-cards">
				for _, c := range vm.RoadmapCards {
					<div class="settings-card">
						<span class="settings-card__label">{ c.Label }</span>
						<div class="settings-card__skel w75"></div>
						<div class="settings-card__skel w50"></div>
						<div class="settings-card__skel w62"></div>
					</div>
				}
			</div>
		</div>
		<script src="/static/js/settings.js" defer></script>
	}
}
```

- [ ] **Step 4: Write the accent-picker JS `web/static/js/settings.js`.**
```js
// Accent-picker enhancement for Settings › Appearance. Marks the active swatch
// from the persisted preference and, on click, calls the F1 setter
// window.Lynceus.setAccent (theme.js), which persists to localStorage and
// re-applies the per-theme accent variant. No external references.
(function () {
  var L = window.Lynceus || {};
  var PRESETS = ['#2dd4bf', '#22d3ee', '#818cf8'];
  function current() {
    try {
      var v = localStorage.getItem('lynceus.accent');
      return PRESETS.indexOf(v) >= 0 ? v : '#2dd4bf';
    } catch (e) {
      return '#2dd4bf';
    }
  }
  function init() {
    var cur = current();
    var btns = document.querySelectorAll('[data-accent]');
    function mark(active) {
      btns.forEach(function (x) {
        var on = x === active;
        x.classList.toggle('is-active', on);
        x.setAttribute('aria-pressed', on ? 'true' : 'false');
      });
    }
    var initial = null;
    btns.forEach(function (b) {
      if (b.getAttribute('data-accent') === cur) initial = b;
      b.addEventListener('click', function () {
        if (L.setAccent) L.setAccent(b.getAttribute('data-accent'));
        mark(b);
      });
    });
    if (initial) mark(initial);
  }
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init);
  else init();
})();
```

- [ ] **Step 5: Regenerate + run web render test — expect PASS.**
```
make templ
go test ./web/ -run 'SettingsPage' -v
```

- [ ] **Step 6: Add static-asset tests for settings.js.** In `web/static_test.go`, extend the `TestStaticHandler_ServesThemeJSAndLegacyCSS` cases with:
```go
		{"/static/js/settings.js", "setAccent"},
		{"/static/js/settings.js", "data-accent"},
		{"/static/js/settings.js", "lynceus.accent"},
```
Run and expect PASS:
```
go test ./web/ -run 'TestStaticHandler' -count=1 -v
```

- [ ] **Step 7: Implement the handler `internal/api/settings.go`.**
```go
package api

import (
	"net/http"

	"github.com/dobbo-ca/lynceus/web"
)

// handleSettingsPage renders Settings › Appearance. The accent picker persists
// via the F1 setter (localStorage); server-side per-user persistence awaits the
// M5 users model.
func (s *Server) handleSettingsPage(w http.ResponseWriter, r *http.Request) {
	vm := web.SettingsVM{
		Accents: []web.AccentSwatch{
			{Hex: "#2dd4bf", Name: "TEAL"},
			{Hex: "#22d3ee", Name: "CYAN"},
			{Hex: "#818cf8", Name: "INDIGO"},
		},
		RoadmapCards: []web.SettingsRoadmapCard{
			{Label: "ORGANIZATION"},
			{Label: "THEME DEFAULTS"},
			{Label: "INTEGRATIONS"},
		},
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.SettingsPage(vm).Render(r.Context(), w)
}
```

- [ ] **Step 8: Register the route.** In `internal/api/server.go` `routes()`, add near the audit/access routes:
```go
	s.mux.HandleFunc("GET /settings", s.handleSettingsPage)
```

- [ ] **Step 9: Write the failing integration test.** Add `internal/api/settings_test.go`:
```go
package api_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/api"
)

func TestSettingsPage_devAuth_rendersAccentPicker(t *testing.T) {
	_, srv := setup(t, api.Config{DevAuth: true})
	resp, err := http.Get(srv.URL + "/settings")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	html := string(body)
	for _, want := range []string{
		"<!doctype html>",
		"APPEARANCE — ACCENT COLOR",
		`data-accent="#2dd4bf"`,
		"SAVED TO YOUR PROFILE",
		`src="/static/js/settings.js"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("/settings missing %q", want)
		}
	}
}

func TestSettingsPage_withoutDevAuth_returns401(t *testing.T) {
	_, srv := setup(t, api.Config{DevAuth: false})
	resp, err := http.Get(srv.URL + "/settings")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}
```

- [ ] **Step 10: Build + run — expect PASS.**
```
go build ./...
go test ./internal/api/ -run 'SettingsPage' -count=1 -v
go test ./web/ -count=1
```

- [ ] **Step 11: Commit.**
```
git add web/settings.templ web/settings_templ.go web/settings_test.go web/static/js/settings.js web/static_test.go internal/api/settings.go internal/api/settings_test.go internal/api/server.go
git commit -m "settings › appearance: accent picker wired to F1 setAccent + roadmap preview at /settings (ly-ae6.13)"
```

---

## Self-Review

### Spec-coverage checklist — every COMPARISON gap → task

**`governance-audit` (Audit Log) gaps → Task 1:**
- No hash chain in UI (verified-tip banner + per-row HASH) → Step 4 `auditBanner` + `c-hash` column; Step 6/7 `VerifyChain` + `hashShort` + tip.ID count. ✔
- No TARGET column → Step 4 grid header + `c-target`; Step 7 `auditTarget` (T1-safe derivation from structural keys). ✔
- Tier not a badge, T2 not amber-striped → Step 1/4 `.tier-badge`, `.audit-row--t2`, `IsT2` + `tierLabel`. ✔
- Action not color-coded → base `.c-action { color: var(--text) }` + `.audit-row--t2 .c-action { color: var(--warnT) }`. This is the COMPLETE design behavior, verified against the design source: the prototype computes `actionColor` per row as `a.t2 ? 'var(--warnT)' : 'var(--text)'` (Lynceus.dc.html line 3202) — a binary T2 rule, NOT a per-action-type color map. The two CSS rules reproduce both arms exactly, so no `actionColor(action string)` helper is needed (and adding one would diverge from the design). ✔
- Wrong placement (flat nav, not user-menu GOVERNANCE) → Task 2 `NewUserMenuVM` Governance→Audit Log `/audit` adds the user-menu entry. PARTIAL by design: this bead does NOT remove the existing flat-nav `href="/audit"` link (it never touches `layout.templ`, and the retained handler test still asserts that link), so Audit Log transitionally appears in BOTH places. Deleting the flat-nav link is explicitly **ly-ae6.2's responsibility** (it owns `layout.templ`) — see the Integration contract. ✔ (entry added) / deferred-removal noted.
- Zero design-system adoption (tokens, LIVE badge, tamper subtitle) → Step 1 governance.css + Step 4 `badge--live`, `gov-note`. ✔
- Note: filter form is a keep-bonus → retained verbatim in Step 4. ✔

**`access-roles` gaps → Task 3 (+ Task 2):**
- No user-menu governance entry point → Task 2 Governance→Access & Roles `/access`. ✔
- No `/access` route/handler → Task 3 Step 5/6. ✔
- No Access & Roles templ (roadmap-preview: OIDC card + RBAC GROUPS→T2 RIGHTS) → Task 3 Step 3 `AccessRolesPage`. ✔
- No user-RBAC engine / OIDC-SCIM → out of scope (backend ly-8b0.4/.2/.1); `AccessGroupRow` VM is the exact contract, preview data noted. ✔
- Design-token gap → governance.css `.access-*` classes. ✔

**`settings-appearance` gaps → Task 4 (+ Task 2):**
- No Settings page/route → Task 4 Step 3/7/8 `SettingsPage` + `GET /settings`. ✔
- No accent-preset picker (Teal/Cyan/Indigo) → Step 3 swatches + Step 4 settings.js. ✔
- No token/theme foundation for accent → consumes F1 (bootstrap.go `L.ACCENTS`, theme.js `setAccent`); noted, not rebuilt. ✔
- No per-theme variant logic → provided by F1 accent map (dark bright / light deep); picker only selects. ✔
- No persistence → interim per-browser localStorage via `setAccent`. NOTE: this satisfies the COMPARISON "no localStorage wiring (interim)" gap but NOT the "server-side per-user profile store" clause — that is explicitly deferred to the M5 users table (see the ACCEPTANCE DEVIATION callout in Task 4). ✔ (interim) / server-side per-user deferred.
- Governance/admin not under a top-right user menu → Task 2 delivers the menu; ly-ae6.2 mounts it (contract stated). NOTE: the fragment ships UNMOUNTED and is unit-tested only in isolation — no end-to-end/visual verification of the menu inside `@Layout` is possible until ly-ae6.2 lands (accepted; see Integration contract). ✔

**Bead ly-ae6.13 acceptance criteria:**
- User menu (identity + GROUP + T2 status, GOVERNANCE, ADMIN, Sign out) → Task 2. ✔
- Audit Log parity (tier badges, T2 amber-striped, hash chain) → Task 1. ✔
- Access & Roles → Task 3. ✔
- Settings Appearance = accent presets persisted **per user** with per-theme variants → **PARTIAL, deliberate.** Presets + per-theme variants + picker: delivered (Task 4). The literal "per user" clause is NOT met — only per-browser localStorage persistence ships; per-user server-side persistence needs the M5 users table (auth unstarted; `actorFromContext` is a static `"dev-admin"`). See the ACCEPTANCE DEVIATION callout in Task 4. Reviewers must accept this reduced scope for ly-ae6.13. ⚠ (scope reduced, documented)
- Dependencies ly-ae6.2 / ly-8b0.4 → integration contract + backend-dep notes stated. ✔

### Known deviations & deferrals (adversarial-review resolutions)

Explicit, accepted, and documented — none is a hidden gap:

1. **Accent persistence is per-browser, not per-user.** Only localStorage ships; server-side per-user storage is M5 (needs a users table + real identity). Callout in Task 4; acceptance bullet marked ⚠. Reviewers accept reduced scope for ly-ae6.13.
2. **Flat-nav `/audit` link stays.** Audit Log appears in both the flat nav and the user-menu GOVERNANCE section until ly-ae6.2 rebuilds `layout.templ`; deleting the flat-nav link is ly-ae6.2's job. Documented in Integration contract + audit-placement bullet.
3. **User-menu SOON badges are semantic, not contradictory.** SOON = "roadmap-preview / not-yet-complete", orthogonal to reachability — matching the design, which shows SOON on the reachable Access & Roles and Settings entries (Lynceus.dc.html 2543-2551). Access & Roles (`/access`, ROADMAP page) and Settings (`/settings`, live picker + roadmap cards) are reachable AND carry SOON; Provider Setup/Collectors/Retention are disabled placeholders (no page on this bead) that also carry SOON. `userMenuItem` renders SOON strictly from `it.Soon` (fixed: the disabled branch now respects the flag instead of hardcoding the badge), and reachability strictly from `it.Href`.
4. **User menu ships unmounted; no e2e/visual verification here.** ly-ae6.2 is unbuilt, so `@web.UserMenu` is never rendered inside `@Layout` on this branch; it is unit-tested only in isolation. Accepted consequence of the dependency split.
5. **Head assets loaded in `<body>` (interim).** `governance.css` `<link>` and `settings.js` `<script defer>` sit in the `@Layout` children because this plan does not edit `layout.templ`; valid HTML5, mildly FOUC-prone. ly-ae6.2's shell should hoist head-asset injection later. Documented in Integration contract.
6. **`AuditFilterForm` keeps legacy `class="filters"`.** The sole non-token class on these otherwise token-pure surfaces; the form is a keep-bonus and its restyle is explicitly deferred to ly-ae6.14 (noted at the templ). Everything else uses `governance.css` tokens.
7. **Action color is a binary T2 rule, not a per-action-type map.** Verified against the design source (Lynceus.dc.html line 3202); base `.c-action`=`var(--text)` + T2 override=`var(--warnT)` is a complete reproduction. No `actionColor()` helper is warranted.

### Placeholder scan
No `TBD`, `...`, `similar to Task N`, or "add error handling" stand-ins. Every code block is complete and compilable. `auditTarget`, `hashShort`, `tierLabel`, `initials`, `userMenuMeta`, `t2TagClass`, `auditRowClass`, `NewUserMenuVM`, `auditChain` are all fully written. The only intentionally-static data are the Access/Settings preview VMs and the user-menu identity, each annotated with the backend bead that will replace it (ly-8b0.4/.2/.1; M5 users table).

### Type-consistency check
- `AuditPage(chain AuditChain, f AuditFilterValues, rows []AuditRow)` — new 3-arg signature; sole caller `handleAuditPage` updated (Step 7). `AuditTable(rows []AuditRow)` unchanged-arity; `handleAuditPartial` unchanged. ✔
- `store.Config` gains `VerifyChain(ctx, since, until time.Time) (int, string, error)` — matches the existing `*pgxConfig` method exactly (only implementer; `var _ Config = (*pgxConfig)(nil)` still holds). No fake implements `store.Config` (audit tests use the real `store.NewConfig`). ✔
- `AuditRow` new fields `Target/HashShort string`, `IsT2 bool` populated in `fetchAudit`; `DataTier int16` feeds `tierLabel(int16)`. ✔
- `AuditRecord.RowHash []byte` → `hashShort([]byte) string`; `AuditRecord.Detail []byte` → `auditTarget([]byte) string`; `tip.ID int64` → `AuditChain.Count int64`. ✔
- templ attribute forms verified against existing generated components: `class={ stringFn(...) }` (cluster_views.templ), `href={ templ.SafeURL(...) }` (databases.templ), literal `data-*` + `data-accent={ a.Hex }` string expression. ✔
- New handlers `handleAccessPage`/`handleSettingsPage` are `func(*Server)(http.ResponseWriter,*http.Request)`, matching `mux.HandleFunc` and the existing handler shape. ✔
- All tokens referenced in governance.css (`--acc --acc2 --accbg --accdim --surface --raised --rail --line --line2 --text --mut --dim --faint --warn --warnT --warnbg --info --infoT --infobg --crit --critT --critbg --shimA --shimB --shadow-pop --font-ui --font-mono --radius`) confirmed present in tokens.css. ✔
- New imports: `internal/api/audit.go` adds `encoding/hex`, `encoding/json`, `context`; `web/user_menu.templ` adds `strings`; `internal/api/audit_test.go` adds `context` + `internal/store`. No import cycles (web imported by api, not vice-versa). ✔

### Verification (whole-bead, after all tasks)
```
make templ && git diff --exit-code -- web/*_templ.go   # generated files in sync (CI gate)
go build ./...
go test ./web/ ./internal/api/ ./internal/store/ -count=1
```
Expected: clean build; all web render tests, static-asset tests, audit/access/settings handler tests, and existing store/config tests PASS.
