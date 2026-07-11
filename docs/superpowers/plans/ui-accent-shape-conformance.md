# Accent Presets Picker + Shape-Language Conformance Implementation Plan

> For agentic workers: execute this plan with the **superpowers:subagent-driven-development** skill — one Task per subagent, TDD loop per step, commit at each step boundary.

**Goal:** Ship the Settings › Appearance accent-preset picker (Teal / Cyan / Indigo, driven by a `data-accent` attribute) and a reusable design-system shape-primitive stylesheet (2px card radius, 1px tiny-badge radius, 1px borders, unrounded 8px severity squares, 24px icon buttons, shadows only on popovers) — completing bead **ly-ae6.14**.

**What this bead does and does not close (be precise — this framing is load-bearing for the Self-Review):**
- **Accent-preset gap (COMPARISON L98):** *fully closed* — the picker UI, `data-accent` mechanism, and click→persist wiring ship here; the per-theme bright/deep variants themselves already shipped in ly-ae6.1's `ACCENTS` map, which this bead surfaces and wires.
- **Shape-language gap (COMPARISON L99):** *encoded, first-consumer applied — NOT app-wide conformant.* This bead **encodes** the shape language as reusable, token-driven primitives (`shape.css`) and **applies** them to the one new screen it ships (Settings › Appearance). It does **not** retrofit the existing `legacy.css` screens (`.db-card`/`.badge` at 9px radius, ad-hoc shadows) — that app-wide retrofit is owned by **ly-ae6.7** and is explicitly out of scope here. Saying otherwise would overstate the deliverable.
- **Settings › Appearance area (COMPARISON L428–437):** the area's page/route/picker gaps close here; its persistence and identity gaps split (localStorage interim = met; server-side per-user profile = deferred to M5). See the Self-Review coverage table for the full G1–G7 mapping.

**Architecture:** ly-ae6.1 already built the token layer (`web/static/css/tokens.css`), the no-flash theme bootstrap (`web/bootstrap.go` → inline `<head>` script with the per-theme `L.ACCENTS` map), and the interactive `window.Lynceus.setAccent(hex)` setter (`web/static/js/theme.js`). This plan builds **on top** of that foundation, not over it: (1) it additively reflects the live accent onto `<html data-accent="…">` so a pure-CSS active-swatch state and any future attribute-driven styling work; (2) it adds a cross-screen component stylesheet `web/static/css/shape.css` that encodes the shape language as reusable classes; (3) it adds a `web.SettingsPage()` templ screen hosting the picker, wired to `setAccent` by a small self-hosted `accent-picker.js`, routed at `GET /settings`.

**Tech Stack:** Go 1.x, `templ` (a-h/templ, pinned via `make templ`), HTMX (self-hosted), server-side-rendered `web` package, `net/http.ServeMux` routes in `internal/api`, self-hosted CSS/JS/fonts under `web/static/` (embedded via `//go:embed`). Tests: Go `testing` + `httptest`, rendered-HTML string assertions; no DB needed for any test in this plan.

## Global Constraints

Copy these verbatim into every subagent's context — they are project-wide invariants:

- **Privacy T1/T2.** Only T1 (normalized, literal-free) data may render unless a screen is explicitly T2 (audited, RBAC-gated). This feature renders **zero** database-derived data — the accent choice is a pure client-side UI preference (localStorage) and the shape primitives are static CSS. Never introduce a raw-literal field into a T1 path.
- **No external hosts.** All CSS/JS/fonts/images are self-hosted under `web/static/` and referenced as `/static/…`. Never add a CDN/font/script host. There is a contract test `TestLayout_NoExternalHosts` (`web/layout_test.go`) and the bootstrap self-containment guard in `web/bootstrap_test.go` — both must stay green.
- **Tokens, not legacy.** New screens are built with the design tokens (`var(--…)` from `tokens.css`) and the shape primitives from `shape.css`. Do **not** add classes to `web/static/css/legacy.css` (frozen for the ly-ae6.7 retrofit) and do **not** style new screens with legacy component classes.
- **templ regen.** Every `.templ` edit requires `make templ` to regenerate the committed `*_templ.go` files; CI fails if generated code is out of sync. Commit the regenerated `_templ.go` alongside the `.templ`.
- **testcontainers.** Integration tests hit real Postgres via testcontainers, never DB mocks. (Not exercised here — every test in this plan is a pure render/`httptest` test with no store — but the rule stands for any store touch.)

## Branch setup (do this once, before Task 1)

Per the concurrent-sessions rule, never commit on `main`. This worktree already has its own dedicated, session-isolated branch — confirm it before doing any work:

```bash
cd /Users/cdobbyn/work/dobbo-ca/lynceus/.claude/worktrees/ui-design-tokens-9k2f
git branch --show-current   # expect: worktree-ui-design-tokens-9k2f (a dedicated branch — NOT main)
```

If (and only if) that command prints `main`, stop and create a uniquely-named topic branch before continuing (`git switch -c ui-accent-shape-9k2f`); otherwise the existing worktree branch already satisfies the isolation rule and you commit directly on it. Verify `git branch --show-current` is not `main` before each commit below.

## Dependencies & integration contract

- **ly-ae6.1 (foundation, present on this branch):** provides `tokens.css` (shape tokens `--radius:2px`, `--radius-badge:1px`, `--border:1px`, `--shadow-pop:0 8px 24px …`, accent tokens `--acc/--acc2/--accbg/--accdim/--ok`), the inline bootstrap `themeBootstrapJS` with the per-theme `L.ACCENTS` map for `#2dd4bf`/`#22d3ee`/`#818cf8`, and `window.Lynceus.setAccent(hex)` in `theme.js`. This plan reuses all of it. **Do not** duplicate the `ACCENTS` map or re-derive per-theme variants — Task 4's presets must reference exactly the three hexes that map already knows (Task 4 has a test enforcing this).
- **ly-ae6.2 (top bar + user menu) — built separately, the ONLY shell dependency:** the Settings screen is reached from the top-bar **user button → ADMIN section → Settings** entry (design: `README.md:22` — the user-menu dropdown carries a GOVERNANCE section and an ADMIN section of *Provider Setup, Collectors, Data & Retention, Settings*). That user menu is built in ly-ae6.2. Until it ships, `GET /settings` is reachable directly by URL (and by the handler test here). **Integration contract this screen needs from ly-ae6.2:** one user-menu ADMIN entry linking to `GET /settings` (no scope param). This plan does **not** modify the shell nav (that is ly-ae6.2 territory); it only registers the route and renders inside the existing `@Layout`.
- **NOT a scoped-nav (ly-ae6.3) dependency — do not add one.** Settings is a top-bar **user-menu** ADMIN item, **not** a per-scope sidebar entry. Contrast Saved Scripts, which *is* a sidebar item at every scope (`README.md:37`); Settings is not, so the "appears at every scope like Saved Scripts" analogy is false and there is **no** dependency on ly-ae6.3 (scope-driven sidebar rebuild). The scope model (`←`FLEET, `⌖` row buttons, per-scope nav) never touches this screen: `GET /settings` is a single scope-independent route.
- **No backend bead:** the accent picker has no server-side persistence requirement in this bead — it persists to `localStorage` via the existing `setAccent`. Server-side per-user persistence has **no store to write to** until user identity lands (auth is unstarted, M5); the design's `accentColor` per-user server preference is deferred to M5 and has no tracked backend bead. Note the dependency but do not build it. (This is why the picker's caption must be interim-accurate — see Task 4.)

---

### Task 1: Reflect the live accent onto `<html data-accent>`

Extend the existing no-flash bootstrap so that whenever the accent is applied, the chosen hex is mirrored onto `document.documentElement.dataset.accent`. This is the "driven by a data-accent attribute" mechanism the bead calls for: the color **application** stays in ly-ae6.1's inline-prop path (do not reinvent it), and this attribute becomes the single DOM signal the picker's active state (Task 2 CSS) reads. Because it lives in `applyAccent`, both the synchronous bootstrap (first paint, no flash) and the interactive `theme.js` `setAccent` (which calls `applyAccent`) get it for free.

**Files:**
- Modify: `web/bootstrap.go` (the `themeBootstrapJS` const, function `applyAccent`)
- Modify (test): `web/bootstrap_test.go`

**Interfaces:**
- Consumes (existing, ly-ae6.1): JS `L.applyAccent = function(){ … }` in `themeBootstrapJS`; local `var a = L.accent || '#2dd4bf';`.
- Produces: DOM side effect `document.documentElement.dataset.accent = a;` (the canonical preset hex, e.g. `"#22d3ee"`). No Go signature change; `themeBootstrapTag() string` and `Layout(title, subtitle string)` are untouched.

Steps:

- [ ] **Step 1:** Add the failing assertion. Edit `web/bootstrap_test.go`, appending to the `want` slice in `TestThemeBootstrap_Contents`:

```go
	for _, want := range []string{
		"window.Lynceus",
		"localStorage.getItem('lynceus.theme')",
		"localStorage.getItem('lynceus.accent')",
		"prefers-color-scheme: light",
		"resolveTheme",
		"applyAccent",
		"'#2dd4bf'", // teal preset present in the accent variant map
		"'#22d3ee'", // cyan
		"'#818cf8'", // indigo
		"dataset.accent", // accent hex reflected onto <html> for the picker's active state
	} {
```

- [ ] **Step 2:** Run it, expect FAIL (bootstrap sets `dataset.theme` but not `dataset.accent`):

```bash
go test ./web/ -run TestThemeBootstrap_Contents
# EXPECT: FAIL — themeBootstrapJS missing "dataset.accent"
```

- [ ] **Step 3:** Implement. In `web/bootstrap.go`, inside `L.applyAccent`, add the reflection line as the last statement before the closing `};`:

```go
  L.applyAccent = function(){
    var a = L.accent || '#2dd4bf';
    var t = document.documentElement.dataset.theme || 'dark';
    var v = (L.ACCENTS[a] || L.ACCENTS['#2dd4bf'])[t==='light'?'light':'dark'];
    var st = document.documentElement.style;
    st.setProperty('--acc',v[0]); st.setProperty('--acc2',v[1]);
    st.setProperty('--accdim',v[2]); st.setProperty('--accbg',v[3]); st.setProperty('--ok',v[0]);
    document.documentElement.dataset.accent = a;
  };
```

- [ ] **Step 4:** Run it, expect PASS (and confirm the self-containment guard still passes):

```bash
go test ./web/ -run TestThemeBootstrap_Contents
# EXPECT: PASS
```

- [ ] **Step 5:** Commit:

```bash
git add web/bootstrap.go web/bootstrap_test.go
git commit -m "feat(ui): reflect live accent onto <html data-accent> (ly-ae6.14)"
```

---

### Task 2: Shape-language primitive stylesheet + conformance tests

Create `web/static/css/shape.css`, the cross-screen design-system component layer built on the ly-ae6.1 shape/type tokens: cards (2px radius, 1px border, no shadow), tiny badges (1px radius), unrounded 8px severity squares, 24px icon buttons, popovers (the only shadow, `--shadow-pop`), and the accent-swatch picker component whose active state is driven by the `<html data-accent>` from Task 1. Link it globally from `layout.templ` so every screen can compose these classes. Guard the shape language and the shadow policy with contract tests.

**Files:**
- Create: `web/static/css/shape.css`
- Create (test): `web/shape_test.go`
- Modify: `web/layout.templ` (add one `<link>` in `<head>`) → regenerate `web/layout_templ.go` via `make templ`
- Modify (test): `web/layout_test.go` (`TestLayout_SelfHostedAssets`)
- Modify (test): `web/static_test.go` (`TestStaticHandler_ServesThemeJSAndLegacyCSS`)

**Interfaces:**
- Consumes (existing, `tokens.css`): `--surface --line --radius --border --radius-badge --mut --info --infoT --infobg --crit --critT --critbg --warn --warnT --warnbg --dim --acc --acc2 --accdim --accbg --shadow-pop --font-mono`.
- Produces (CSS classes, consumed by this plan's Task 4 and future ly-ae6.x screens): `.card`, `.badge` (+`.badge--info/--crit/--warn`), `.sev-sq` (+`.sev-sq--crit/--warn/--info`), `.icon-btn` (+`.icon-btn--danger`), `.pop`, `.accent-picker`, `.accent-swatch` (+`.accent-swatch__chip`, `.accent-swatch__name`, active-state rules keyed on `html[data-accent='…']`).
- No Go signature changes; `Layout(title, subtitle string)` unchanged. `staticFS embed.FS` (package `web`, `static.go`) already embeds `static/**`, so `shape.css` is served with no code change once the file exists.

Steps:

- [ ] **Step 1:** Write the failing conformance tests. Create `web/shape_test.go`:

```go
package web

import (
	"strings"
	"testing"
)

// shapeCSS reads the embedded shape.css so assertions run against the exact
// bytes shipped to the browser.
func shapeCSS(t *testing.T) string {
	t.Helper()
	b, err := staticFS.ReadFile("static/css/shape.css")
	if err != nil {
		t.Fatalf("read shape.css: %v", err)
	}
	return string(b)
}

// The shape primitives must encode the design's shape language via tokens.
func TestShapeCSS_Primitives(t *testing.T) {
	css := shapeCSS(t)
	for _, want := range []string{
		".card",
		"border-radius: var(--radius)",       // 2px cards
		".badge",
		"border-radius: var(--radius-badge)", // 1px tiny badges
		"border: var(--border) solid",        // 1px borders via token
		".sev-sq",
		"width: 8px",
		"height: 8px",
		"border-radius: 0",                   // UNROUNDED severity squares
		".icon-btn",
		"width: 24px",
		"height: 24px",                       // 24px icon buttons
		".pop",
		"box-shadow: var(--shadow-pop)",      // shadow ONLY on popovers
		".accent-picker",
		".accent-swatch",
		"data-accent='#2dd4bf'",
		"data-accent='#22d3ee'",
		"data-accent='#818cf8'",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("shape.css missing %q", want)
		}
	}
}

// Shadow policy: the ONLY box-shadow in the primitives is the popover shadow
// (dropdowns/modals). Any bare shadow is a shape-language conformance break.
func TestShapeCSS_ShadowPolicy(t *testing.T) {
	css := shapeCSS(t)
	for _, line := range strings.Split(css, "\n") {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, "box-shadow:") &&
			!strings.Contains(l, "var(--shadow-pop)") &&
			!strings.Contains(l, "none") {
			t.Errorf("non-conforming shadow in shape.css: %q (only var(--shadow-pop) or none allowed)", l)
		}
	}
}
```

> **Test-scope note (honest limits of `TestShapeCSS_ShadowPolicy`).** This guard scans **`shape.css` only** and only flags lines whose trimmed prefix is `box-shadow:`. That is sufficient for this file (which is authored one property per line) and it is the correct scope for this bead — it proves the **primitive layer** encodes the "shadow only on popovers" policy. It deliberately does **not** enforce the app-wide "no shadows except dropdowns/modals" rule: it does not scan `legacy.css`, inline styles, or multi-property lines. App-wide shadow conformance (auditing/retrofitting `legacy.css` and existing screens) is owned by **ly-ae6.7**, not this bead. Do not oversell this test as an app-wide guarantee.

- [ ] **Step 2:** Run it, expect FAIL (file does not exist yet → `read shape.css` fatals):

```bash
go test ./web/ -run TestShapeCSS
# EXPECT: FAIL — read shape.css: open static/css/shape.css: file does not exist
```

- [ ] **Step 3:** Create `web/static/css/shape.css` with the full primitive set:

```css
/* Lynceus design-system component primitives (ly-ae6.14). Built on the shape /
   type tokens in tokens.css (--radius 2px, --radius-badge 1px, --border 1px,
   --shadow-pop). New screens compose these classes instead of the frozen
   pre-design legacy.css component styles. Shadow policy: NONE anywhere except
   .pop (dropdowns / menus / modals), which uses --shadow-pop. */

/* ---- Surfaces / cards: 2px radius, 1px border, no shadow. ---- */
.card {
  background: var(--surface);
  border: var(--border) solid var(--line);
  border-radius: var(--radius);
  box-shadow: none;
}

/* ---- Tiny badges: 1px radius, letter-spaced mono. ---- */
.badge {
  display: inline-flex;
  align-items: center;
  font-family: var(--font-mono);
  font-size: 10px;
  letter-spacing: .06em;
  line-height: 1.6;
  padding: 0 5px;
  border: var(--border) solid var(--line);
  border-radius: var(--radius-badge);
  color: var(--mut);
}
.badge--info { color: var(--infoT); border-color: var(--info); background: var(--infobg); }
.badge--crit { color: var(--critT); border-color: var(--crit); background: var(--critbg); }
.badge--warn { color: var(--warnT); border-color: var(--warn); background: var(--warnbg); }

/* ---- Severity squares: UNROUNDED 8px. ---- */
.sev-sq {
  display: inline-block;
  width: 8px;
  height: 8px;
  border-radius: 0;
  flex-shrink: 0;
}
.sev-sq--crit { background: var(--crit); }
.sev-sq--warn { background: var(--warn); }
.sev-sq--info { background: var(--info); }

/* ---- 24px square icon buttons. ---- */
.icon-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  flex-shrink: 0;
  border: var(--border) solid var(--line);
  border-radius: var(--radius);
  background: transparent;
  color: var(--dim);
  font-size: 13px;
  cursor: pointer;
  user-select: none;
}
.icon-btn:hover { border-color: var(--acc); background: var(--accdim); color: var(--acc2); }
.icon-btn--danger:hover { border-color: var(--crit); background: transparent; color: var(--critT); }

/* ---- Popovers: the ONLY elements that carry a shadow. ---- */
.pop {
  background: var(--surface);
  border: var(--border) solid var(--line);
  border-radius: var(--radius);
  box-shadow: var(--shadow-pop);
}

/* ---- Accent-preset picker (Settings > Appearance). Active swatch is the one
   whose data-accent matches the live <html data-accent> set by the bootstrap
   (web/bootstrap.go applyAccent). It glows in the current --acc/--accbg, so it
   automatically tracks the chosen preset and the light/dark theme. ---- */
.accent-picker { display: flex; gap: 10px; flex-wrap: wrap; }
.accent-swatch {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 12px;
  border: var(--border) solid var(--line);
  border-radius: var(--radius);
  background: transparent;
  cursor: pointer;
  user-select: none;
}
.accent-swatch:hover { border-color: var(--dim); }
.accent-swatch__chip { width: 14px; height: 14px; border-radius: var(--radius); }
.accent-swatch[data-accent='#2dd4bf'] .accent-swatch__chip { background: #2dd4bf; }
.accent-swatch[data-accent='#22d3ee'] .accent-swatch__chip { background: #22d3ee; }
.accent-swatch[data-accent='#818cf8'] .accent-swatch__chip { background: #818cf8; }
.accent-swatch__name {
  font-family: var(--font-mono);
  font-size: 10.5px;
  letter-spacing: .06em;
  color: var(--mut);
}
html[data-accent='#2dd4bf'] .accent-swatch[data-accent='#2dd4bf'],
html[data-accent='#22d3ee'] .accent-swatch[data-accent='#22d3ee'],
html[data-accent='#818cf8'] .accent-swatch[data-accent='#818cf8'] {
  border-color: var(--acc);
  background: var(--accbg);
}
html[data-accent='#2dd4bf'] .accent-swatch[data-accent='#2dd4bf'] .accent-swatch__name,
html[data-accent='#22d3ee'] .accent-swatch[data-accent='#22d3ee'] .accent-swatch__name,
html[data-accent='#818cf8'] .accent-swatch[data-accent='#818cf8'] .accent-swatch__name {
  color: var(--acc2);
}
```

- [ ] **Step 4:** Run the shape tests, expect PASS:

```bash
go test ./web/ -run TestShapeCSS
# EXPECT: PASS (TestShapeCSS_Primitives, TestShapeCSS_ShadowPolicy)
```

- [ ] **Step 5:** Link `shape.css` globally and extend the layout self-hosted-asset test. First edit `web/layout_test.go`, adding the shape.css ref to `TestLayout_SelfHostedAssets`:

```go
	for _, want := range []string{
		`href="/static/css/tokens.css"`,
		`href="/static/css/shape.css"`,
		`href="/static/css/legacy.css"`,
		`src="/static/js/htmx.min.js"`,
		`src="/static/js/theme.js"`,
	} {
```

Then add a serve assertion in `web/static_test.go` — extend the `cases` slice in `TestStaticHandler_ServesThemeJSAndLegacyCSS`:

```go
	cases := []struct {
		path, contains string
	}{
		{"/static/js/theme.js", "setTheme"},
		{"/static/js/theme.js", "cycleTheme"},
		{"/static/js/theme.js", "setAccent"},
		{"/static/css/legacy.css", ".db-card"},
		{"/static/css/shape.css", ".icon-btn"},
	}
```

- [ ] **Step 6:** Run both, expect FAIL (layout does not yet link shape.css; the serve case passes because the file exists, but the layout assertion fails):

```bash
go test ./web/ -run 'TestLayout_SelfHostedAssets|TestStaticHandler_ServesThemeJSAndLegacyCSS'
# EXPECT: FAIL — layout missing self-hosted asset ref "href=\"/static/css/shape.css\""
```

- [ ] **Step 7:** Add the `<link>` to `web/layout.templ`, immediately after the tokens.css link:

```html
				<link rel="stylesheet" href="/static/css/tokens.css"/>
				<link rel="stylesheet" href="/static/css/shape.css"/>
				<link rel="stylesheet" href="/static/css/legacy.css"/>
```

Regenerate the templ output:

```bash
make templ
```

> **Cascade & specificity caution (note for this and every future new screen).** `layout.templ` links `legacy.css` **after** `shape.css`, but link order is not the hazard — **selector specificity** is. `legacy.css` defines `.db-card .badge { border-radius: 9px }` (specificity 0,2,0), which outranks `shape.css`'s bare `.badge` (0,1,0) *regardless of link order*. The Settings screen is safe (its `.badge` has no `.db-card` ancestor, so only the 0,1,0 rule applies). **Rule for new screens: never nest a shape primitive (`.badge`, `.card`, …) inside a `legacy.css` container class (`.db-card` and friends)** — the primitives assume no legacy ancestor. New screens are built entirely from tokens + shape primitives (see Global Constraints), so this is automatically satisfied unless someone reaches into legacy markup. Untangling this in the existing screens is part of the ly-ae6.7 retrofit.

- [ ] **Step 8:** Run the web package tests, expect PASS:

```bash
go test ./web/
# EXPECT: PASS (layout, static, shape, bootstrap all green)
```

- [ ] **Step 9:** Commit:

```bash
git add web/static/css/shape.css web/shape_test.go web/layout.templ web/layout_templ.go web/layout_test.go web/static_test.go
git commit -m "feat(ui): shape-language primitives (card/badge/sev-sq/icon-btn/pop) + link globally (ly-ae6.14)"
```

---

### Task 3: Accent-picker click wiring (`accent-picker.js`)

Add a tiny self-hosted script that delegates clicks on `.accent-swatch[data-accent]` to `window.Lynceus.setAccent(hex)`. The setter (ly-ae6.1) persists to localStorage, re-applies the per-theme accent variables, and — via Task 1 — reflects `<html data-accent>`, which flips the active swatch. Event delegation on `document` keeps it robust across full page loads (and any future HTMX swap of the settings body).

**Files:**
- Create: `web/static/js/accent-picker.js`
- Modify (test): `web/static_test.go` (add a serve case)

**Interfaces:**
- Consumes (existing, ly-ae6.1 `theme.js`): `window.Lynceus.setAccent(hex string)` — no-op for unknown hexes.
- Produces: a self-invoking script that registers one `document`-level `click` listener; no globals, no exports.

Steps:

- [ ] **Step 1:** Add the failing serve assertion. Extend the `cases` slice in `web/static_test.go`'s `TestStaticHandler_ServesThemeJSAndLegacyCSS`:

```go
		{"/static/css/shape.css", ".icon-btn"},
		{"/static/js/accent-picker.js", "setAccent"},
	}
```

- [ ] **Step 2:** Run it, expect FAIL (file does not exist → 404, not 200):

```bash
go test ./web/ -run TestStaticHandler_ServesThemeJSAndLegacyCSS
# EXPECT: FAIL — GET /static/js/accent-picker.js = 404, want 200
```

- [ ] **Step 3:** Create `web/static/js/accent-picker.js`:

```js
// accent-picker.js (ly-ae6.14) — wires the Settings > Appearance swatches to
// the theme API. Each swatch carries data-accent="<hex>"; a click calls
// window.Lynceus.setAccent(hex), which persists the choice, re-applies the
// per-theme accent variables, and reflects data-accent onto <html> so the
// active swatch styles itself (see shape.css). Event delegation on document
// keeps this working regardless of when the picker markup mounts.
(function () {
  document.addEventListener('click', function (e) {
    var t = e.target;
    var el = t && t.closest ? t.closest('.accent-swatch[data-accent]') : null;
    if (!el) return;
    var hex = el.getAttribute('data-accent');
    if (window.Lynceus && window.Lynceus.setAccent) window.Lynceus.setAccent(hex);
  });
})();
```

- [ ] **Step 4:** Run it, expect PASS:

```bash
go test ./web/ -run TestStaticHandler_ServesThemeJSAndLegacyCSS
# EXPECT: PASS
```

- [ ] **Step 5:** Commit:

```bash
git add web/static/js/accent-picker.js web/static_test.go
git commit -m "feat(ui): accent-picker.js click delegation to setAccent (ly-ae6.14)"
```

---

### Task 4: Settings › Appearance screen with the accent picker (templ)

Create `web/settings.templ` — a `SettingsPage()` rendered inside the existing `@Layout`, with a live APPEARANCE accent-color card built from the shape primitives (`.card`, `.badge`, `.accent-picker`, `.accent-swatch`) and the fixed preset list. The presets must be exactly the three hexes the bootstrap `ACCENTS` map knows (a test enforces this so a click can never be a silent no-op). The page loads `accent-picker.js`.

**Files:**
- Create: `web/settings.templ` → regenerate `web/settings_templ.go` via `make templ`
- Create (test): `web/settings_test.go`

**Interfaces:**
- Produces:
  - `type AccentSwatch struct { Hex string; Name string }` — `Hex` is the canonical preset value and the `data-accent` key (e.g. `"#22d3ee"`); `Name` is the mono display label (e.g. `"CYAN"`).
  - `var AccentPresets []AccentSwatch` — the fixed, ordered preset set: Teal `#2dd4bf`, Cyan `#22d3ee`, Indigo `#818cf8`.
  - `func SettingsPage() templ.Component` — full page (wrapped in `@Layout`).
  - `func SettingsAppearanceCard() templ.Component` — the appearance card fragment.
- Consumes: `web.Layout(title, subtitle string)` (existing), the `.card/.badge/.accent-*` classes from Task 2's `shape.css`, and `/static/js/accent-picker.js` from Task 3. Cross-checks against `themeBootstrapJS` (package `web`, `bootstrap.go`) in the presets test.

Steps:

- [ ] **Step 1:** Write the failing render tests. Create `web/settings_test.go`:

```go
package web

import (
	"context"
	"strings"
	"testing"
)

func renderSettings(t *testing.T) string {
	t.Helper()
	var sb strings.Builder
	if err := SettingsPage().Render(context.Background(), &sb); err != nil {
		t.Fatalf("render: %v", err)
	}
	return sb.String()
}

func TestSettingsPage_AccentPicker(t *testing.T) {
	html := renderSettings(t)
	for _, want := range []string{
		`data-accent-picker`,
		`class="accent-swatch" data-accent="#2dd4bf"`,
		`class="accent-swatch" data-accent="#22d3ee"`,
		`class="accent-swatch" data-accent="#818cf8"`,
		">TEAL<",
		">CYAN<",
		">INDIGO<",
		"APPEARANCE — ACCENT COLOR",
		"SAVED IN THIS BROWSER", // interim-accurate copy (no user profile store until M5)
		`src="/static/js/accent-picker.js"`,
		`class="card"`, // uses the shape primitive
	} {
		if !strings.Contains(html, want) {
			t.Errorf("settings page missing %q", want)
		}
	}
}

// Every offered preset must be a hex the bootstrap ACCENTS map can apply, or a
// click would be a silent no-op (setAccent ignores unknown presets).
func TestAccentPresets_MatchBootstrap(t *testing.T) {
	if len(AccentPresets) != 3 {
		t.Fatalf("want 3 accent presets, got %d", len(AccentPresets))
	}
	for _, sw := range AccentPresets {
		if !strings.Contains(themeBootstrapJS, "'"+sw.Hex+"'") {
			t.Errorf("preset %s (%s) not present in themeBootstrapJS ACCENTS map", sw.Name, sw.Hex)
		}
	}
}
```

- [ ] **Step 2:** Run it, expect FAIL (compile error: `SettingsPage`, `AccentPresets`, `AccentSwatch` undefined):

```bash
go test ./web/ -run 'TestSettingsPage_AccentPicker|TestAccentPresets_MatchBootstrap'
# EXPECT: FAIL — undefined: SettingsPage / AccentPresets (build failure)
```

- [ ] **Step 3:** Create `web/settings.templ`:

```go
package web

// AccentSwatch is one selectable accent preset in Settings > Appearance.
// Hex is both the display chip value and the data-accent key the picker JS
// hands to window.Lynceus.setAccent.
type AccentSwatch struct {
	Hex  string
	Name string
}

// AccentPresets is the fixed, ordered set of accent presets the picker offers.
// The values MUST stay in lockstep with the ACCENTS map in the theme bootstrap
// (web/bootstrap.go); TestAccentPresets_MatchBootstrap guards this.
var AccentPresets = []AccentSwatch{
	{Hex: "#2dd4bf", Name: "TEAL"},
	{Hex: "#22d3ee", Name: "CYAN"},
	{Hex: "#818cf8", Name: "INDIGO"},
}

templ SettingsPage() {
	@Layout("Lynceus — settings", "appearance + workspace preferences") {
		<div style="padding:18px 22px 32px; display:flex; flex-direction:column; gap:14px; max-width:1400px;">
			<div style="display:flex; align-items:center; gap:12px;">
				<span style="font-size:17px; font-weight:600;">Settings</span>
				<span class="badge badge--info">ROADMAP</span>
			</div>
			@SettingsAppearanceCard()
		</div>
		<script src="/static/js/accent-picker.js"></script>
	}
}

templ SettingsAppearanceCard() {
	<div class="card" style="padding:12px 14px; display:flex; flex-direction:column; gap:10px;">
		<span style="font-family:var(--font-mono); font-size:10px; letter-spacing:.1em; color:var(--dim);">APPEARANCE — ACCENT COLOR</span>
		<div class="accent-picker" data-accent-picker>
			for _, sw := range AccentPresets {
				<div class="accent-swatch" data-accent={ sw.Hex } role="button" tabindex="0" aria-label={ "Accent " + sw.Name }>
					<span class="accent-swatch__chip"></span>
					<span class="accent-swatch__name">{ sw.Name }</span>
				</div>
			}
		</div>
		<span style="font-family:var(--font-mono); font-size:9.5px; letter-spacing:.04em; color:var(--faint);">SAVED IN THIS BROWSER · ADAPTS AUTOMATICALLY TO DARK AND LIGHT THEMES</span>
	</div>
}
```

> **Copy-fidelity note (interim-accurate caption — the one intentional divergence from prototype text).** The prototype (`Lynceus.dc.html:1969`) reads "SAVED TO YOUR PROFILE · ADAPTS…", but that is aspirational: this bead ships **localStorage-only** persistence (via `setAccent`), and there is **no user profile / users table until M5**, so a second browser or device would **not** see the choice. Shipping the prototype's copy verbatim against a localStorage-only picker would mislead the user, so the caption reads "SAVED IN THIS BROWSER · ADAPTS…" instead. When M5 adds the server-side profile store, a follow-up restores the prototype copy. Everything else on this card (label, 14px chip + mono name, 6/12px padding, 2px radius, ROADMAP `--info` badge) matches the prototype exactly. *(The rationale lives here in the plan, not as an inline comment in the templ body — Go `//` comments in the templ HTML region are avoided since no existing `.templ` uses them and support in the pinned templ version is unverified.)*

Regenerate templ:

```bash
make templ
```

- [ ] **Step 4:** Run it, expect PASS:

```bash
go test ./web/ -run 'TestSettingsPage_AccentPicker|TestAccentPresets_MatchBootstrap'
# EXPECT: PASS
```

- [ ] **Step 5:** Run the whole web package to confirm nothing regressed:

```bash
go test ./web/
# EXPECT: PASS
```

- [ ] **Step 6:** Commit:

```bash
git add web/settings.templ web/settings_templ.go web/settings_test.go
git commit -m "feat(ui): Settings > Appearance accent-preset picker screen (ly-ae6.14)"
```

---

### Task 5: Route the Settings page (`GET /settings`)

Register a handler that renders `web.SettingsPage()`. No store access — the accent choice lives in the browser. Test it with a bare `httptest` Server (no DB), mirroring `web/../static_test.go`'s `TestStatic_BypassesAuth` construction so no testcontainers spin-up is needed.

**Files:**
- Create: `internal/api/settings.go`
- Modify: `internal/api/server.go` (`routes()`)
- Create (test): `internal/api/settings_test.go`

**Interfaces:**
- Produces: `func (s *Server) handleSettingsPage(w http.ResponseWriter, r *http.Request)`.
- Consumes: `web.SettingsPage() templ.Component` (Task 4); existing `(*Server).routes()`, `(*Server).Handler()`, `Config{DevAuth bool}`.
- Route: `GET /settings` (distinct from the existing per-cluster `GET /databases/{clusterID}/settings` pg_settings screen — no collision).

Steps:

- [ ] **Step 1:** Write the failing handler test. Create `internal/api/settings_test.go`:

```go
package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The Settings page needs no store, so build a bare Server (mirrors
// static_test.go's TestStatic_BypassesAuth) and exercise the route directly.
func TestSettingsPage_Renders(t *testing.T) {
	s := &Server{cfg: Config{DevAuth: true}, mux: http.NewServeMux()}
	s.routes()

	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /settings = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"APPEARANCE — ACCENT COLOR",
		`data-accent="#22d3ee"`,
		`src="/static/js/accent-picker.js"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("settings page missing %q; body=%s", want, body)
		}
	}
}
```

- [ ] **Step 2:** Run it, expect FAIL (route unregistered → the `GET /settings` request falls through; `ServeMux` returns 404, and `handleSettingsPage` is undefined so it will not compile until Step 3 — expect a build failure):

```bash
go test ./internal/api/ -run TestSettingsPage_Renders
# EXPECT: FAIL — undefined: s.handleSettingsPage (build failure)
```

- [ ] **Step 3:** Create `internal/api/settings.go`:

```go
package api

import (
	"net/http"

	"github.com/dobbo-ca/lynceus/web"
)

// handleSettingsPage renders the workspace Settings screen. Only the
// APPEARANCE accent-color card is live today (persisted client-side via the
// theme API); the rest is roadmap. No store access — the accent choice lives
// in the browser, so this is a static render.
func (s *Server) handleSettingsPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.SettingsPage().Render(r.Context(), w)
}
```

Then register the route in `internal/api/server.go` `routes()`, after the checks routes and before the `/api/...` group:

```go
	s.mux.HandleFunc("GET /checks", s.handleChecksPage)
	s.mux.HandleFunc("GET /partial/checks", s.handleChecksPartial)
	s.mux.HandleFunc("GET /settings", s.handleSettingsPage)
	s.mux.HandleFunc("GET /api/queries/top", s.handleTopQueries)
```

- [ ] **Step 4:** Run it, expect PASS:

```bash
go test ./internal/api/ -run TestSettingsPage_Renders
# EXPECT: PASS
```

- [ ] **Step 5:** Full build + full package tests to confirm no regression:

```bash
go build ./...
go test ./web/ ./internal/api/
# EXPECT: PASS
```

- [ ] **Step 6:** Commit:

```bash
git add internal/api/settings.go internal/api/server.go internal/api/settings_test.go
git commit -m "feat(ui): route GET /settings -> Settings appearance page (ly-ae6.14)"
```

---

## Self-Review

### Spec-coverage checklist

This bead's COMPARISON gaps span **two** area sections: the cross-cutting `Design system & tokens` section (L98–L100) **and** the dedicated `Settings › Appearance` section (L428–L437). Both are mapped below; every listed gap maps to a task, to ly-ae6.1 (already met on this branch), or to an explicitly-named deferral.

**COMPARISON.md — `Design system & tokens` (`docs/design/COMPARISON.md` L98–L100):**

| Gap | Status / Task(s) |
|-----|---------|
| L98 "No accent presets: Teal/Cyan/Indigo with per-theme bright(dark)/deep(light) variants and matching bg/hover tints" — the picker + `data-accent` mechanism (per-theme variants themselves already ship in ly-ae6.1's `ACCENTS` map; this bead surfaces + wires them) | **CLOSED** — Task 1 (`data-accent` reflection), Task 3 (click→`setAccent`), Task 4 (picker UI with all 3 presets + `MatchBootstrap` guard), Task 5 (route) |
| L99 "Shape language non-conforming: 8/6/9px radii instead of 2px (1px tiny badges); no unrounded 8px severity squares; no 24px icon buttons; shadow policy (none except dropdowns/modals) not encoded" | **PARTIAL by design — encoded + first-consumer applied, NOT app-wide conformant.** Task 2 *encodes* the language as reusable primitives (`shape.css`: `.card` 2px, `.badge` 1px, `.sev-sq` unrounded 8px, `.icon-btn` 24px, `.pop` `--shadow-pop`) with `TestShapeCSS_Primitives` + `TestShapeCSS_ShadowPolicy`, and the Settings screen (Task 4) *applies* the card/badge subset. The existing non-conforming screens (`legacy.css` `.db-card`/`.badge` @ 9px, ad-hoc shadows) are **NOT** retrofitted here — that app-wide conformance is owned by **ly-ae6.7**. This bead does not claim the app conforms; it claims the shape language is now *encoded and available*. |
| L100 self-hosting stance (no external hosts) | **CLOSED** — Task 3 (`accent-picker.js` self-hosted); guarded by existing `TestLayout_NoExternalHosts` + `web/static_test.go` serve tests extended in Tasks 2–3 |

**COMPARISON.md — `Settings › Appearance` (`docs/design/COMPARISON.md` L428–L437) — the dedicated area this bead's screen implements:**

| # | Gap (verbatim area line) | Status / Task(s) |
|---|--------------------------|------------------|
| G1 | L431 "No Settings page or route exists at all (no `web/settings.templ`, no `/settings` handler)" | **CLOSED** — Task 4 (`web/settings.templ` → `SettingsPage()`) + Task 5 (`GET /settings` → `handleSettingsPage`) |
| G2 | L432 "No accent-color preset picker (Teal/Cyan/Indigo) UI" | **CLOSED** — Task 4 (`AccentPresets` + `.accent-picker`/`.accent-swatch` markup) |
| G3 | L433 "No design-token/theme foundation in `layout.templ` … no `--acc/--acc2/--accbg/--accdim`, no light/dark `data-theme`, no theme toggle" | **MET by ly-ae6.1** (present on this branch: `tokens.css` accent vars + `data-theme` + toggle). This bead consumes it; nothing to build. |
| G4 | L434 "No per-theme variant logic (dark bright value vs light deeper value + bg/hover tints)" | **MET by ly-ae6.1** (`ACCENTS` map in `bootstrap.go`). `TestAccentPresets_MatchBootstrap` (Task 4) enforces the picker stays in lockstep with it. |
| G5 | L435 "No persistence: no localStorage wiring (interim) **and** no server-side per-user profile store" | **SPLIT.** *localStorage interim = MET* — Task 3 wires clicks to `setAccent`, which persists to `localStorage` (ly-ae6.1). *Server-side per-user profile store = DEFERRED to M5* — there is no store to write to (see G6); the picker caption is made interim-accurate ("SAVED IN THIS BROWSER", Task 4) precisely because this half is not built. |
| G6 | L436 "No user identity to persist to (no users table/model; auth is unstarted M5)" | **DEFERRED to M5** — no users table exists; nothing in this bead can persist to a user. Bounds G5's server-side half. |
| G7 | L437 "Governance/admin … not relocated under a top-right user menu" | **DEFERRED to ly-ae6.2** — the top-bar user menu (with the ADMIN → Settings entry that reaches `GET /settings`) is built in ly-ae6.2, per the Dependencies section. Not this bead. |

**Bead ly-ae6.14 acceptance criteria** (description):

| Criterion | Task(s) |
|-----------|---------|
| Teal/Cyan/Indigo accent presets | Task 4 (`AccentPresets` = `#2dd4bf`/`#22d3ee`/`#818cf8`, labels TEAL/CYAN/INDIGO) |
| per-theme bright/deep variants, matching bg/hover | Reused from ly-ae6.1 `ACCENTS` map (bright dark / deep light + accbg/accdim); dependency noted, `TestAccentPresets_MatchBootstrap` enforces lockstep |
| driven by a `data-accent` attribute | Task 1 (`<html data-accent>` reflection) + Task 2 (active-swatch CSS keyed on `html[data-accent='…']`) + Task 4 (each swatch carries `data-accent`) |
| shape pass: 2px radius | Task 2 `.card { border-radius: var(--radius) }` (`--radius:2px`) |
| 1px tiny badges | Task 2 `.badge { border-radius: var(--radius-badge) }` (`--radius-badge:1px`) |
| 1px borders | Task 2 `border: var(--border) solid …` (`--border:1px`) |
| no shadows except dropdowns/modals | Task 2 `.pop { box-shadow: var(--shadow-pop) }` + `TestShapeCSS_ShadowPolicy` |
| unrounded 8px severity squares | Task 2 `.sev-sq { width:8px; height:8px; border-radius:0 }` |
| 24px icon buttons | Task 2 `.icon-btn { width:24px; height:24px }` |

### Reusable-primitive render-verification ownership

`shape.css` is a **reusable component library** (like any CSS design-system layer): the bead's acceptance for the shape primitives is that they are *defined, token-driven/conformant, and unit-tested*, and that at least one real screen consumes the accent subset. Verification therefore splits into two honest tiers. This table exists specifically so the plan does **not** overclaim that every primitive is visually render-verified in this bead.

| Primitive | CSS conformance test (this bead) | Rendered by a screen in THIS bead? | First render consumer (verification lands there) |
|-----------|----------------------------------|------------------------------------|--------------------------------------------------|
| `.card` | `TestShapeCSS_Primitives` (2px radius, 1px border, no shadow) | **Yes** — Settings card; asserted in `web/settings_test.go` (`class="card"`) and re-rendered through `GET /settings` (`internal/api/settings_test.go`) | Settings › Appearance (here) |
| `.badge`, `.badge--info` | `TestShapeCSS_Primitives` (1px radius) | **Yes** — ROADMAP chip is `.badge badge--info`; the card label/chips render it | Settings › Appearance (here) |
| `.accent-picker`, `.accent-swatch` (+`__chip`/`__name`, `data-accent` active state) | `TestShapeCSS_Primitives` (all 3 `data-accent` selectors present) | **Yes** — the picker; asserted in `web/settings_test.go` + `internal/api/settings_test.go` | Settings › Appearance (here) |
| `.sev-sq` (+`--crit/--warn/--info`) | `TestShapeCSS_Primitives` (8px, `border-radius:0`) | **No** — a Settings screen has no severity marks; forcing one would invent UI not in the prototype | **ly-ae6.4** (fleet triage strips / needs-attention rows) and **ly-ae6.7** retrofit |
| `.icon-btn` (+`--danger`) | `TestShapeCSS_Primitives` (24px) | **No** — the icon buttons in the design live in the top bar / on rows (`⌖` scope buttons), not on Settings | **ly-ae6.2** (top-bar controls) / **ly-ae6.4** (`⌖` row buttons) and **ly-ae6.7** retrofit |
| `.pop` (shadow) | `TestShapeCSS_Primitives` + `TestShapeCSS_ShadowPolicy` | **No** — no dropdown/modal on this screen | **ly-ae6.2** (SCOPE picker dropdown + user menu) |
| `.badge--crit`, `.badge--warn` | `TestShapeCSS_Primitives` | **No** — Settings shows only the info-tier ROADMAP badge | **ly-ae6.4** (fleet status badges) and **ly-ae6.7** retrofit |

**Why this is the correct scope, not a gap to paper over:** `.sev-sq`/`.icon-btn`/`.pop`/`.badge--crit/--warn` are bead acceptance *deliverables* (the bead literally lists "unrounded 8px severity squares, 24px icon buttons, shadow policy"), so they must be *defined and conformance-tested here* — and they are. But a Settings › Appearance screen has no faithful place to render a severity square or a critical badge; manufacturing one solely to satisfy a render assertion would violate the "surgical / faithful-to-prototype" constraint and invent product UI. The primitives are shipped as a tested library and each is render-verified by its first genuine consumer (named above). This is the standard, correct lifecycle for a shared component layer.

**Design-source fidelity checks** (`docs/design/Lynceus.dc.html`): accent-swatch anatomy (14px chip + mono name, 6px/12px pad, 2px radius, active = `--acc` border / `--accbg` bg / `--acc2` text) matches prototype lines 1959–1969 & 3340–3352; icon-button anatomy (24px, 1px `--line` border, 2px radius, hover `--acc`/`--accdim`) matches lines 369/417/443; severity square (8px, no radius) matches lines 249/396/484; "APPEARANCE — ACCENT COLOR" label matches line 1960; ROADMAP badge (1px radius, `--info`) matches line 1957. **One intentional copy divergence:** the caption ships as "SAVED IN THIS BROWSER · ADAPTS…" instead of the prototype's aspirational "SAVED TO YOUR PROFILE · ADAPTS…" (line 1969), because there is no per-user profile store until M5 — see Task 4's copy-fidelity note. This is the sole deliberate deviation from prototype text.

### Placeholder scan

No "TBD", "add error handling", "similar to Task N", "...", or elided code blocks. Every code step is the complete file/edit. The only non-implemented items are explicitly out of scope and named, each with an owner:
- **Server-side per-user `accentColor` persistence** → deferred to **M5** (no users table / identity to write to); localStorage via `setAccent` is the interim, and the caption ("SAVED IN THIS BROWSER") is made truthful about it.
- **Top-bar user-menu ADMIN → Settings entry** → **ly-ae6.2** (this bead only registers `GET /settings`; there is no ly-ae6.3 scoped-nav dependency — Settings is a user-menu item, not a per-scope sidebar entry).
- **App-wide shape retrofit of existing `legacy.css` screens** → **ly-ae6.7** (this bead encodes the primitives and applies them to the new screen only).
- **Render verification of the non-Settings primitives** (`.sev-sq`/`.icon-btn`/`.pop`/`.badge--crit/--warn`) → their first consumers **ly-ae6.2 / ly-ae6.4 / ly-ae6.7** (see the render-verification ownership table); they are CSS-conformance-tested here.
- The remaining roadmap Settings cards (shimmer placeholders in the prototype) are intentionally omitted — only the live Appearance card is built.

### Type-consistency check

- `AccentSwatch{Hex string, Name string}` and `AccentPresets []AccentSwatch` are defined in Task 4 and consumed only within `web/settings.templ`'s `SettingsAppearanceCard` loop and `web/settings_test.go` — consistent `string` fields throughout.
- `SettingsPage() templ.Component` / `SettingsAppearanceCard() templ.Component`: produced in Task 4, consumed by `handleSettingsPage` (Task 5) and `renderSettings` (Task 4 test) — matches templ's generated `func SettingsPage() templ.Component` signature.
- `handleSettingsPage(w http.ResponseWriter, r *http.Request)` matches `http.HandlerFunc` and the `s.mux.HandleFunc("GET /settings", …)` registration.
- `staticFS embed.FS` (existing, `web/static.go`) is read in `web/shape_test.go` via `staticFS.ReadFile("static/css/shape.css")` — same package `web`, valid.
- `themeBootstrapJS` (existing `const`, `web/bootstrap.go`, package `web`) is read in `web/settings_test.go` and `web/bootstrap_test.go` — same package, valid.
- `&Server{cfg: Config{DevAuth: true}, mux: http.NewServeMux()}` in `internal/api/settings_test.go` is package `api` (unexported fields accessible) and mirrors the proven `TestStatic_BypassesAuth` construction; `stats/conf/disc` stay nil but the `/settings` handler never dereferences them.
- No token names invented: every `var(--…)` used in `shape.css` (`--surface --line --radius --border --radius-badge --mut --info/--infoT/--infobg --crit/--critT/--critbg --warn/--warnT/--warnbg --dim --acc --acc2 --accdim --accbg --shadow-pop --font-mono`) exists in `web/static/css/tokens.css`.
