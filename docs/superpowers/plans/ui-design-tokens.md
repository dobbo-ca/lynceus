# UI Design-Token Layer + Self-Hosted Fonts + Theme Mechanism — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Land the design-token CSS layer, self-hosted fonts, and dark-default/light/system theme mechanism (bead **ly-ae6.1**, workstream **F1**) that every downstream UI bead hooks into.

**Architecture:** Introduce a net-new static-asset pipeline (`web/static/` embedded via `//go:embed`, served at `/static/` outside auth). Ship `tokens.css` (dark `:root` + `html[data-theme='light']` custom-property sets, exact prototype values, plus shape/type tokens and dark base element styles), self-hosted Work Sans + JetBrains Mono variable woff2, self-hosted htmx (replacing the unpkg CDN), and a two-part theme mechanism: a synchronous no-flash `<head>` bootstrap (resolves + applies persisted theme/accent before paint) plus `theme.js` (interactive `setTheme`/`cycleTheme`/`setAccent` API for the top bar and Settings picker to call later). The base shell flips to dark immediately; existing screens keep their component CSS (moved verbatim to `legacy.css` with a small legibility patch) until the retrofit epic ly-ae6.7.

**Tech Stack:** Go 1.22+ (`net/http` method+path routing, `embed.FS`), templ v0.3.1020 (`make templ` regenerates `_templ.go`), HTMX 2.0.4, vanilla CSS custom properties + vanilla JS (no framework, no build step, no CDN).

## Global Constraints

- **No external hosts / no CDN.** Every font, script, and stylesheet is self-hosted from `/static/`. The privacy backbone forbids external requests. `unpkg.com`, `googleapis.com`, `gstatic.com`, `cdn.jsdelivr.net` must not appear in rendered output. (COMPARISON.md:100; the current unpkg htmx at `web/layout.templ:25` is the offender being removed.)
- **Dark theme is the DEFAULT.** `<html data-theme="dark">` is the SSR default (correct even with JS disabled); light + `system` are variants.
- **Exact token values.** Copy the color hex/rgba values verbatim from `docs/design/Lynceus.dc.html` lines 16–37 (reproduced below). Do not invent or round values.
- **Fonts:** Work Sans (UI) + JetBrains Mono (data), weights 400–700, **variable woff2, latin subset**, `font-display: swap`. Both are OFL-licensed → vendor the license text.
- **Accent presets:** Teal `#2dd4bf`, Cyan `#22d3ee`, Indigo `#818cf8`, each with per-theme variants (bright on dark, deeper on light). The variant map is defined once, in the bootstrap.
- **Shape language:** radius 2px (1px tiny badges), 1px borders, no shadows except dropdowns/modals (`0 8px 24px rgba(0,0,0,.35)`).
- **templ regen:** after editing any `.templ`, run `make templ` (installs pinned templ v0.3.1020, runs `templ generate`). Committed `_templ.go` must be in sync.
- **TDD:** write the failing test first, watch it fail, implement minimally, watch it pass, commit.
- **Scope boundary:** F1 ships the token/font/theme *plumbing* and the `window.Lynceus` JS API. It does **not** render the theme-toggle button (ly-ae6.2), the accent picker UI or component shape-audit (ly-ae6.14), or retrofit existing screens to tokens (ly-ae6.7). Server-side per-user persistence arrives with auth (M5); F1 uses `localStorage`.

---

### Task 1: Token stylesheet + static-asset embed + file server

Creates the `/static/` serving pipeline (none exists today) and ships `tokens.css` with the full dark+light token sets, shape/type tokens, and dark base element styles.

**Files:**
- Create: `web/static/css/tokens.css`
- Create: `web/static.go` (embed + `StaticHandler`)
- Create: `web/static_test.go`
- Modify: `internal/api/server.go` (register `/static/` route; bypass auth for it)
- Test: `web/static_test.go`, `internal/api/static_test.go`

**Interfaces:**
- Produces: `web.StaticHandler() http.Handler` — serves the embedded `web/static/` tree under a `/static/`-stripped path with an immutable `Cache-Control` header. Consumed by `internal/api/server.go` `routes()`.

- [ ] **Step 1: Write the failing test for the token stylesheet being served**

Create `web/static_test.go`:

```go
package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStaticHandler_ServesTokensCSS(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/static/css/tokens.css", nil)
	rec := httptest.NewRecorder()
	StaticHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET tokens.css = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/css") {
		t.Errorf("Content-Type = %q, want text/css", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("Cache-Control = %q, want immutable", cc)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"--acc:#2dd4bf",          // dark default accent
		"[data-theme='light']",   // light override block
		"--acc:#0d9488",          // light accent
		"--radius:2px",           // shape token
		"@font-face",             // fonts declared here
	} {
		if !strings.Contains(body, want) {
			t.Errorf("tokens.css missing %q", want)
		}
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./web/ -run TestStaticHandler_ServesTokensCSS`
Expected: FAIL — `undefined: StaticHandler` (and the `web/static/` dir does not exist yet).

- [ ] **Step 3: Create `web/static/css/tokens.css`**

Create `web/static/css/tokens.css` with exactly this content:

```css
/* Lynceus design tokens — self-hosted, dark-default with light override.
   Source of truth: docs/design/ (PRODUCT_INTENT.md, README.md, Lynceus.dc.html
   lines 16-37). Only edit token VALUES here; component styling lives with each
   screen (see legacy.css during the ly-ae6.7 retrofit). */

/* ---- Self-hosted fonts (no CDN — privacy backbone). Files vendored in Task 2. ---- */
@font-face {
  font-family: 'Work Sans';
  font-style: normal;
  font-weight: 400 700;
  font-display: swap;
  src: url('/static/fonts/work-sans-latin.woff2') format('woff2');
}
@font-face {
  font-family: 'JetBrains Mono';
  font-style: normal;
  font-weight: 400 700;
  font-display: swap;
  src: url('/static/fonts/jetbrains-mono-latin.woff2') format('woff2');
}

/* ---- Color tokens: dark theme is the default ---- */
:root {
  --bg:#0c1118; --rail:#0a0e14; --surface:#10161f; --raised:#141c28;
  --line:#26303d; --line2:#1a2330;
  --text:#dbe4ee; --mut:#a3b1c4; --dim:#64748c; --faint:#4d5c73;
  --acc:#2dd4bf; --acc2:#5eead4; --accbg:#131c29; --accdim:rgba(45,212,191,.14);
  --crit:#ef6351; --critT:#f28b7d; --critbg:rgba(239,99,81,.1);
  --warn:#e5a83b; --warnT:#e5b45f; --warnbg:rgba(229,168,59,.1);
  --info:#7d8fa8; --infoT:#9fb3cc; --infobg:rgba(125,143,168,.12);
  --ok:#2dd4bf; --shimA:#151d29; --shimB:#1b2534;
  --chart-cpu:#5c6d85; --chart-io:#2dd4bf; --chart-lwlock:#60a5fa; --chart-lock:#e5a83b; --chart-client:#3d4c63;

  /* shape + type language */
  --radius:2px; --radius-badge:1px; --border:1px;
  --shadow-pop:0 8px 24px rgba(0,0,0,.35);
  --font-ui:'Work Sans', system-ui, sans-serif;
  --font-mono:'JetBrains Mono', ui-monospace, 'SF Mono', Menlo, monospace;
}
html[data-theme='light'] {
  --bg:#f2f4f8; --rail:#eaedf3; --surface:#ffffff; --raised:#f6f8fb;
  --line:#cfd8e3; --line2:#e3e8f0;
  --text:#131c29; --mut:#3d4c63; --dim:#64748c; --faint:#8a97ab;
  --acc:#0d9488; --acc2:#0f766e; --accbg:#e3f4f1; --accdim:rgba(13,148,136,.12);
  --crit:#c93a28; --critT:#b0301f; --critbg:rgba(201,58,40,.08);
  --warn:#b97e14; --warnT:#96660d; --warnbg:rgba(185,126,20,.09);
  --info:#5d7189; --infoT:#4c5f76; --infobg:rgba(93,113,137,.1);
  --ok:#0d9488; --shimA:#e7ebf1; --shimB:#dde3ec;
  --chart-cpu:#8a97ab; --chart-io:#0d9488; --chart-lwlock:#3b82f6; --chart-lock:#b97e14; --chart-client:#c3cdd9;
}

/* ---- Base element styles: the shell reads dark by default ---- */
html { background: var(--bg); }
body { margin:0; font-family: var(--font-ui); color: var(--text); font-size:13px; -webkit-font-smoothing:antialiased; }
a { color: var(--acc); text-decoration:none; }
a:hover { color: var(--acc2); text-decoration:underline; }
code, .mono { font-family: var(--font-mono); }
input:focus { border-color: var(--acc); }
*:focus-visible { outline:2px solid var(--acc); outline-offset:1px; }
::selection { background: rgba(45,212,191,.25); }
@keyframes pulse { 0%,100% { opacity:1; } 50% { opacity:.25; } }
@keyframes shimmer { 0% { background-position:-400px 0; } 100% { background-position:400px 0; } }
@media (prefers-reduced-motion: reduce) { * { animation:none !important; } }
```

- [ ] **Step 4: Create `web/static.go`**

Create `web/static.go`:

```go
package web

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed static
var staticFS embed.FS

// StaticHandler serves the embedded self-hosted assets (fonts, CSS, JS) under
// /static/. Assets are content-stable and safe to cache aggressively. Serving
// is intentionally auth-free (see server.withAuth) so unauthenticated pages can
// still style themselves.
func StaticHandler() http.Handler {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err) // embed guarantees web/static exists at build time
	}
	fileServer := http.FileServer(http.FS(sub))
	return http.StripPrefix("/static/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		fileServer.ServeHTTP(w, r)
	}))
}
```

- [ ] **Step 5: Run the web test to verify it passes**

Run: `go test ./web/ -run TestStaticHandler_ServesTokensCSS`
Expected: PASS.

- [ ] **Step 6: Write the failing auth-bypass test**

Create `internal/api/static_test.go`:

```go
package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Static assets must be reachable even when DevAuth is off (unauthenticated),
// so login/error pages can style themselves. Everything else 401s.
func TestStatic_BypassesAuth(t *testing.T) {
	s := &Server{cfg: Config{DevAuth: false}, mux: http.NewServeMux()}
	s.routes()

	req := httptest.NewRequest(http.MethodGet, "/static/css/tokens.css", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code == http.StatusUnauthorized {
		t.Fatal("static asset was 401'd — it must bypass auth")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /static/css/tokens.css = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "--acc:#2dd4bf") {
		t.Error("served body is not tokens.css")
	}
}
```

- [ ] **Step 7: Run it to verify it fails**

Run: `go test ./internal/api/ -run TestStatic_BypassesAuth`
Expected: FAIL — `/static/` route not registered (404 or 401).

Note: `s.routes()` on a literal `Server{}` is safe here — `routes()` only *registers* handlers, it never dereferences the nil stores; those are only touched when a page handler is *invoked*, which this test does not do.

- [ ] **Step 8: Wire the static route + auth bypass in `internal/api/server.go`**

Add `"strings"` to the import block (currently just `"net/http"` and the store import at `server.go:7-11`):

```go
import (
	"net/http"
	"strings"

	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/dobbo-ca/lynceus/web"
)
```

In `routes()` (after `server.go:47`), register the static file server as the first route:

```go
func (s *Server) routes() {
	s.mux.Handle("GET /static/", web.StaticHandler())
	s.mux.HandleFunc("GET /databases", s.handleDatabases)
	// ... existing routes unchanged ...
```

In `withAuth` (`server.go:86-94`), let `/static/` through before the auth check:

```go
func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/static/") {
			next.ServeHTTP(w, r)
			return
		}
		if !s.cfg.DevAuth {
			http.Error(w, "unauthorized (dev auth disabled and OIDC not yet implemented — see ly-8b0.1)", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
```

Note: `internal/api` already imports `github.com/dobbo-ca/lynceus/web` (its handlers call `web.XxxPage(...)`), so no new module dependency is introduced — confirm the import line is present; add it if goimports drops it.

- [ ] **Step 9: Run both tests + build to verify they pass**

Run: `go test ./web/ ./internal/api/ -run 'Static' && go build ./...`
Expected: PASS, build OK.

- [ ] **Step 10: Commit**

```bash
git add web/static/css/tokens.css web/static.go web/static_test.go internal/api/server.go internal/api/static_test.go
git commit -m "feat(web): design-token CSS layer + static-asset serving (ly-ae6.1)"
```

---

### Task 2: Self-hosted fonts (Work Sans + JetBrains Mono)

Vendors the two variable woff2 files + OFL licenses; the `@font-face` rules already reference them (added in Task 1).

**Files:**
- Create: `web/static/fonts/work-sans-latin.woff2`
- Create: `web/static/fonts/jetbrains-mono-latin.woff2`
- Create: `web/static/fonts/OFL-Work-Sans.txt`
- Create: `web/static/fonts/OFL-JetBrains-Mono.txt`
- Test: `web/static_test.go` (add cases)

**Interfaces:**
- Produces: `/static/fonts/work-sans-latin.woff2` and `/static/fonts/jetbrains-mono-latin.woff2`, served by the Task 1 handler.

- [ ] **Step 1: Write the failing test for fonts being served**

Add to `web/static_test.go`:

```go
func TestStaticHandler_ServesFonts(t *testing.T) {
	for _, path := range []string{
		"/static/fonts/work-sans-latin.woff2",
		"/static/fonts/jetbrains-mono-latin.woff2",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		StaticHandler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200 (font not vendored?)", path, rec.Code)
		}
		if rec.Body.Len() < 1000 {
			t.Errorf("GET %s served %d bytes, want a real woff2", path, rec.Body.Len())
		}
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./web/ -run TestStaticHandler_ServesFonts`
Expected: FAIL — 404, fonts not vendored.

- [ ] **Step 3: Vendor the woff2 files**

Download the latin variable-weight woff2 from the fontsource jsDelivr mirror (OFL, self-hostable). Network egress is a one-time read; if the sandbox blocks it, re-run with the sandbox disabled, or ask the user to run the `curl`s via a `!`-prefixed prompt.

```bash
mkdir -p web/static/fonts
curl -fsSL -o web/static/fonts/work-sans-latin.woff2 \
  'https://cdn.jsdelivr.net/fontsource/fonts/work-sans:vf@latest/latin-wght-normal.woff2'
curl -fsSL -o web/static/fonts/jetbrains-mono-latin.woff2 \
  'https://cdn.jsdelivr.net/fontsource/fonts/jetbrains-mono:vf@latest/latin-wght-normal.woff2'
# sanity: each should be tens-to-hundreds of KB and start with the woff2 magic "wOF2"
ls -l web/static/fonts/*.woff2
head -c 4 web/static/fonts/work-sans-latin.woff2 | xxd | grep -q "774f 4632" && echo "work-sans woff2 OK"
head -c 4 web/static/fonts/jetbrains-mono-latin.woff2 | xxd | grep -q "774f 4632" && echo "jetbrains woff2 OK"
```

Expected: two files present, both printing the `... woff2 OK` line.

- [ ] **Step 4: Vendor the OFL licenses**

```bash
curl -fsSL -o web/static/fonts/OFL-Work-Sans.txt \
  'https://raw.githubusercontent.com/googlefonts/worksans/master/OFL.txt'
curl -fsSL -o web/static/fonts/OFL-JetBrains-Mono.txt \
  'https://raw.githubusercontent.com/JetBrains/JetBrainsMono/master/OFL.txt'
grep -q "SIL Open Font License" web/static/fonts/OFL-Work-Sans.txt && echo "work-sans OFL OK"
grep -q "SIL Open Font License" web/static/fonts/OFL-JetBrains-Mono.txt && echo "jetbrains OFL OK"
```

Expected: both `... OFL OK` lines print. If a URL 404s (repo default-branch/path drift), fetch the OFL.txt from that font's GitHub repo root — do not skip the license file (OFL redistribution requires it).

- [ ] **Step 5: Run the font test to verify it passes**

Run: `go test ./web/ -run TestStaticHandler_ServesFonts`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add web/static/fonts/
git commit -m "feat(web): self-host Work Sans + JetBrains Mono woff2 (ly-ae6.1)"
```

---

### Task 3: Self-hosted htmx (replace unpkg CDN)

Vendors htmx 2.0.4 minified so the layout can drop the unpkg `<script>` (`web/layout.templ:25`).

**Files:**
- Create: `web/static/js/htmx.min.js`
- Test: `web/static_test.go` (add case)

**Interfaces:**
- Produces: `/static/js/htmx.min.js`, referenced by `layout.templ` in Task 5.

- [ ] **Step 1: Write the failing test**

Add to `web/static_test.go`:

```go
func TestStaticHandler_ServesHTMX(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/static/js/htmx.min.js", nil)
	rec := httptest.NewRecorder()
	StaticHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET htmx.min.js = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "htmx") {
		t.Error("served file does not look like htmx")
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./web/ -run TestStaticHandler_ServesHTMX`
Expected: FAIL — 404.

- [ ] **Step 3: Vendor htmx 2.0.4 (pin matches the version being removed)**

```bash
mkdir -p web/static/js
curl -fsSL -o web/static/js/htmx.min.js 'https://unpkg.com/htmx.org@2.0.4/dist/htmx.min.js'
grep -q "htmx" web/static/js/htmx.min.js && echo "htmx vendored OK"
```

Expected: `htmx vendored OK`. (This is the only time we touch unpkg — to copy the file locally; the running app never references it.)

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./web/ -run TestStaticHandler_ServesHTMX`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/static/js/htmx.min.js
git commit -m "feat(web): self-host htmx 2.0.4, drop unpkg CDN (ly-ae6.1)"
```

---

### Task 4: Theme bootstrap constant + theme.js + legacy.css

Ships the no-flash bootstrap (as a Go constant so it renders inline safely from templ), the interactive `theme.js` API, and `legacy.css` (existing component CSS moved verbatim + a small dark-legibility patch). No `.templ` edits yet — Task 5 wires them into the layout.

**Files:**
- Create: `web/bootstrap.go` (the `themeBootstrapJS` constant)
- Create: `web/bootstrap_test.go`
- Create: `web/static/js/theme.js`
- Create: `web/static/css/legacy.css`
- Test: `web/bootstrap_test.go`, `web/static_test.go` (add cases)

**Interfaces:**
- Produces: `web.themeBootstrapJS` (unexported package const) — inline `<head>` script source, rendered by `layout.templ` via `@templ.Raw` in Task 5. Creates `window.Lynceus` with `ACCENTS`, `resolveTheme`, `applyAccent`, and applies the persisted theme/accent before first paint.
- Produces: `/static/js/theme.js` — extends `window.Lynceus` with `setTheme(pref)`, `cycleTheme()` (returns new pref), `setAccent(hex)`. This is the API the top bar (ly-ae6.2) and Settings picker (ly-ae6.14) will call.

- [ ] **Step 1: Write the failing test for the bootstrap constant**

Create `web/bootstrap_test.go`:

```go
package web

import (
	"strings"
	"testing"
)

func TestThemeBootstrap_Contents(t *testing.T) {
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
	} {
		if !strings.Contains(themeBootstrapJS, want) {
			t.Errorf("themeBootstrapJS missing %q", want)
		}
	}
	// It must be self-contained (no external references) and set data-theme.
	if !strings.Contains(themeBootstrapJS, "dataset.theme") {
		t.Error("bootstrap must set documentElement.dataset.theme")
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./web/ -run TestThemeBootstrap_Contents`
Expected: FAIL — `undefined: themeBootstrapJS`.

- [ ] **Step 3: Create `web/bootstrap.go`**

Create `web/bootstrap.go` (a Go raw-string const — the JS uses only single quotes, so backtick delimiting is safe):

```go
package web

// themeBootstrapJS is the synchronous no-flash theme bootstrap. It is rendered
// inline in the document <head> (before the stylesheet) via @templ.Raw so it
// runs before first paint: it creates the window.Lynceus namespace, resolves
// the persisted theme preference (dark | light | system), sets
// documentElement.dataset.theme, and applies the persisted accent. It must be
// inline (an external script would load async and cause a flash) and
// self-contained (no external references). theme.js later extends the same
// namespace with the interactive setters.
const themeBootstrapJS = `
(function(){
  var L = window.Lynceus = window.Lynceus || {};
  // per-theme accent variants: [acc, acc2, accdim, accbg]; bright on dark, deeper on light.
  L.ACCENTS = {
    '#2dd4bf':{dark:['#2dd4bf','#5eead4','rgba(45,212,191,.14)','#131c29'],light:['#0d9488','#0f766e','rgba(13,148,136,.12)','#e3f4f1']},
    '#22d3ee':{dark:['#22d3ee','#67e8f9','rgba(34,211,238,.14)','#131c29'],light:['#0891b2','#0e7490','rgba(8,145,178,.12)','#e0f4f9']},
    '#818cf8':{dark:['#818cf8','#a5b4fc','rgba(129,140,248,.14)','#131c29'],light:['#4f46e5','#4338ca','rgba(79,70,229,.12)','#e9eafc']}
  };
  L.resolveTheme = function(pref){
    if(pref==='system'||!pref){ return (window.matchMedia && window.matchMedia('(prefers-color-scheme: light)').matches) ? 'light' : 'dark'; }
    return pref==='light' ? 'light' : 'dark';
  };
  L.applyAccent = function(){
    var a = L.accent || '#2dd4bf';
    var t = document.documentElement.dataset.theme || 'dark';
    var v = (L.ACCENTS[a] || L.ACCENTS['#2dd4bf'])[t==='light'?'light':'dark'];
    var st = document.documentElement.style;
    st.setProperty('--acc',v[0]); st.setProperty('--acc2',v[1]);
    st.setProperty('--accdim',v[2]); st.setProperty('--accbg',v[3]); st.setProperty('--ok',v[0]);
  };
  try {
    var acc = localStorage.getItem('lynceus.accent');
    if(['#2dd4bf','#22d3ee','#818cf8'].indexOf(acc) >= 0) L.accent = acc;
    L.pref = localStorage.getItem('lynceus.theme') || 'system';
    document.documentElement.dataset.theme = L.resolveTheme(L.pref);
    L.applyAccent();
  } catch(e){ /* localStorage blocked -> keep SSR data-theme="dark" */ }
})();
`
```

- [ ] **Step 4: Run the bootstrap test to verify it passes**

Run: `go test ./web/ -run TestThemeBootstrap_Contents`
Expected: PASS.

- [ ] **Step 5: Write the failing test for theme.js + legacy.css being served**

Add to `web/static_test.go`:

```go
func TestStaticHandler_ServesThemeJSAndLegacyCSS(t *testing.T) {
	cases := []struct {
		path, contains string
	}{
		{"/static/js/theme.js", "setTheme"},
		{"/static/js/theme.js", "cycleTheme"},
		{"/static/js/theme.js", "setAccent"},
		{"/static/css/legacy.css", ".db-card"},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, c.path, nil)
		rec := httptest.NewRecorder()
		StaticHandler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", c.path, rec.Code)
			continue
		}
		if !strings.Contains(rec.Body.String(), c.contains) {
			t.Errorf("GET %s missing %q", c.path, c.contains)
		}
	}
}
```

- [ ] **Step 6: Run it to verify it fails**

Run: `go test ./web/ -run TestStaticHandler_ServesThemeJSAndLegacyCSS`
Expected: FAIL — 404 for both files.

- [ ] **Step 7: Create `web/static/js/theme.js`**

```js
// Interactive theme + accent API. The synchronous no-flash bootstrap in the
// document <head> already created window.Lynceus (ACCENTS/resolveTheme/
// applyAccent) and applied the persisted preference; this deferred file adds
// the setters that the top-bar toggle (ly-ae6.2) and Settings picker
// (ly-ae6.14) call.
(function(){
  var L = window.Lynceus = window.Lynceus || {};
  function persist(k, v){ try { localStorage.setItem(k, v); } catch(e){} }

  // setTheme('dark'|'light'|'system'): resolve, apply, persist, re-apply accent
  // (the accent variant tracks the resolved theme).
  L.setTheme = function(pref){
    L.pref = pref;
    persist('lynceus.theme', pref);
    document.documentElement.dataset.theme = L.resolveTheme(pref);
    if (L.applyAccent) L.applyAccent();
  };

  // cycleTheme(): dark -> light -> system -> dark. Returns the new preference.
  L.cycleTheme = function(){
    var order = ['dark', 'light', 'system'];
    var next = order[(order.indexOf(L.pref) + 1) % order.length];
    L.setTheme(next);
    return next;
  };

  // setAccent('#hex'): persist + apply. No-op for unknown presets.
  L.setAccent = function(hex){
    if (!L.ACCENTS || !L.ACCENTS[hex]) return;
    L.accent = hex;
    persist('lynceus.accent', hex);
    if (L.applyAccent) L.applyAccent();
  };

  // Keep a 'system' preference live if the OS theme flips while the app is open.
  if (window.matchMedia) {
    window.matchMedia('(prefers-color-scheme: light)').addEventListener('change', function(){
      if (L.pref === 'system') {
        document.documentElement.dataset.theme = L.resolveTheme('system');
        if (L.applyAccent) L.applyAccent();
      }
    });
  }
})();
```

- [ ] **Step 8: Create `web/static/css/legacy.css`**

Move the existing component rules out of `web/layout.templ`'s inline `<style>` (`layout.templ:26-78`) verbatim, MINUS the base `body`/`code` rules that `tokens.css` now owns, and PREFIX a short transitional legibility patch so existing screens stay readable on the dark shell until the ly-ae6.7 retrofit. Create `web/static/css/legacy.css`:

```css
/* Legacy pre-design-system component styles, moved verbatim from
   layout.templ's inline <style> so existing screens keep working while the
   base shell is dark. FULL PARITY RETROFIT: ly-ae6.7. Do not extend this file
   — new screens use the tokens in tokens.css. */

/* --- Transitional dark-legibility patch (remove during ly-ae6.7) --- */
.subtitle { color: var(--mut); }
nav a { color: var(--acc); }
th { background: var(--surface); color: var(--mut); }
.tile, .topo-card, .db-card, .facts-list dd, td { color: var(--text); }

/* --- Original component rules --- */
h1 { margin-bottom: 0.25rem; }
nav { margin-bottom: 0.5rem; }
nav a { text-decoration: none; margin-right: 1rem; font-size: 0.9rem; }
nav a:hover { text-decoration: underline; }
.subtitle { margin-top: 0; margin-bottom: 1.5rem; }
table { border-collapse: collapse; width: 100%; }
th, td { text-align: left; padding: 0.5rem 0.75rem; border-bottom: 1px solid var(--line); vertical-align: top; }
th { font-size: 0.85rem; text-transform: uppercase; letter-spacing: 0.04em; }
td.num { text-align: right; font-variant-numeric: tabular-nums; white-space: nowrap; }
code { font-size: 0.85rem; }
.empty { color: var(--mut); font-style: italic; padding: 2rem 0; }
form.filters { display: flex; flex-wrap: wrap; gap: 0.75rem; align-items: flex-end; margin-bottom: 1.25rem; }
form.filters label { display: flex; flex-direction: column; font-size: 0.75rem; text-transform: uppercase; letter-spacing: 0.04em; color: var(--mut); gap: 0.2rem; }
form.filters input, form.filters select { padding: 0.35rem 0.5rem; font-size: 0.9rem; }
form.filters button { padding: 0.4rem 0.9rem; font-size: 0.9rem; cursor: pointer; }
.cards { display:grid; grid-template-columns:repeat(auto-fill,minmax(220px,1fr)); gap:0.75rem; }
.db-card { display:block; border:1px solid var(--line); border-radius:8px; padding:0.75rem; text-decoration:none; color:inherit; }
.db-card:hover { border-color:var(--acc); }
.db-card .badge { background:#fffbe6; border:1px solid #9e6a03; color:#8a5a00; border-radius:9px; padding:0 0.4rem; font-size:0.75rem; }
ul.plan-tree { list-style: none; padding-left: 1.25rem; margin: 0.25rem 0; border-left: 1px solid var(--line); }
ul.plan-tree > li { margin: 0.15rem 0; }
.node-type { font-weight: 600; }
.node-meta { color: var(--mut); font-size: 0.85rem; margin-left: 0.4rem; }
/* Overview layout */
.overview-layout { display: flex; gap: 1.5rem; align-items: flex-start; }
.sidebar { display: flex; flex-direction: column; min-width: 140px; gap: 0.25rem; padding-top: 0.5rem; }
.sidebar-link { display: block; padding: 0.35rem 0.6rem; border-radius: 4px; text-decoration: none; color: var(--acc); font-size: 0.9rem; }
.sidebar-link:hover { background: var(--accdim); }
.sidebar-link--active { background: var(--accbg); font-weight: 600; }
.overview-main { flex: 1; min-width: 0; }
.overview-section { margin-top: 1.5rem; }
.tiles { display: flex; flex-wrap: wrap; gap: 0.75rem; margin-bottom: 1rem; }
.tile { border: 1px solid var(--line); border-radius: 6px; padding: 0.6rem 1rem; min-width: 100px; }
.tile-label { font-size: 0.75rem; color: var(--mut); text-transform: uppercase; letter-spacing: 0.04em; }
.tile-value { font-size: 1.25rem; font-weight: 600; margin-top: 0.1rem; }
.tile-sparkline { min-width: 130px; }
.topo-cards { display: flex; flex-wrap: wrap; gap: 0.75rem; }
.topo-card { border: 1px solid var(--line); border-radius: 6px; padding: 0.6rem 0.9rem; min-width: 180px; }
.topo-name { font-weight: 600; margin: 0.2rem 0; }
.topo-meta { font-size: 0.8rem; color: var(--mut); }
.badge-role { border-radius: 9px; padding: 0 0.4rem; font-size: 0.75rem; border: 1px solid var(--line); }
.badge-role--primary { background: #e6ffed; border-color: #2d7a41; color: #1a4d28; }
.badge-role--replica { background: #e6f0ff; border-color: #2b5cb0; color: #1a3a70; }
.badge-role--high { background: #fff0f0; border-color: #c53030; color: #7b1d1d; }
.query-btn { background: none; border: none; cursor: pointer; text-align: left; padding: 0; color: inherit; }
.query-btn:hover code { text-decoration: underline; }
.facts-list { display: grid; grid-template-columns: max-content 1fr; gap: 0.2rem 1rem; }
.facts-list dt { font-weight: 600; font-size: 0.85rem; color: var(--mut); }
.facts-list dd { margin: 0; }
.insight-banner { border-radius: 4px; padding: 0.4rem 0.75rem; margin-bottom: 0.5rem; font-size: 0.9rem; background: var(--warnbg); border: 1px solid var(--warn); color: var(--text); }
```

Note: the patch reuses tokens (`var(--line)` etc.) so the legacy tables inherit theme-correct borders/text; the light-background status badges (`.badge-role--*`, `.db-card .badge`) are left as self-consistent light chips (light bg + dark text) — legible on dark, fully retokenized in ly-ae6.7.

- [ ] **Step 9: Run the serving test to verify it passes**

Run: `go test ./web/ -run TestStaticHandler_ServesThemeJSAndLegacyCSS`
Expected: PASS.

- [ ] **Step 10: Commit**

```bash
git add web/bootstrap.go web/bootstrap_test.go web/static/js/theme.js web/static/css/legacy.css
git commit -m "feat(web): theme bootstrap + theme.js API + legacy component CSS (ly-ae6.1)"
```

---

### Task 5: Wire the token layer + theme mechanism into `layout.templ`

Rewrites the layout `<head>`: dark default, inline bootstrap, self-hosted stylesheet + fonts + htmx + theme.js; removes the inline `<style>` block (now split into tokens.css + legacy.css).

**Files:**
- Modify: `web/layout.templ` (`layout.templ:18-97`)
- Regenerate: `web/layout_templ.go` (via `make templ`)
- Test: `web/layout_test.go`

**Interfaces:**
- Consumes: `themeBootstrapJS` (Task 4), `/static/css/tokens.css` (Task 1), `/static/css/legacy.css` (Task 4), `/static/js/htmx.min.js` (Task 3), `/static/js/theme.js` (Task 4).

- [ ] **Step 1: Write the failing layout contract test**

Create `web/layout_test.go`:

```go
package web

import (
	"context"
	"strings"
	"testing"
)

func renderLayout(t *testing.T) string {
	t.Helper()
	var sb strings.Builder
	if err := Layout("Test Title", "sub").Render(context.Background(), &sb); err != nil {
		t.Fatalf("render: %v", err)
	}
	return sb.String()
}

func TestLayout_NoExternalHosts(t *testing.T) {
	html := renderLayout(t)
	for _, host := range []string{"unpkg.com", "googleapis.com", "gstatic.com", "cdn.jsdelivr.net"} {
		if strings.Contains(html, host) {
			t.Errorf("layout references external host %q — assets must be self-hosted (privacy backbone)", host)
		}
	}
}

func TestLayout_SelfHostedAssets(t *testing.T) {
	html := renderLayout(t)
	for _, want := range []string{
		`href="/static/css/tokens.css"`,
		`href="/static/css/legacy.css"`,
		`src="/static/js/htmx.min.js"`,
		`src="/static/js/theme.js"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("layout missing self-hosted asset ref %q", want)
		}
	}
}

func TestLayout_DarkDefaultAndBootstrap(t *testing.T) {
	html := renderLayout(t)
	if !strings.Contains(html, `data-theme="dark"`) {
		t.Error(`layout <html> must default to data-theme="dark" (no-JS fallback)`)
	}
	// The no-flash bootstrap must be inlined verbatim.
	if !strings.Contains(html, "window.Lynceus") || !strings.Contains(html, "resolveTheme") {
		t.Error("layout is missing the inline theme bootstrap")
	}
}

// The inline <style> block must be gone — styling now lives in the served CSS.
func TestLayout_NoInlineStyleBlock(t *testing.T) {
	html := renderLayout(t)
	if strings.Contains(html, "system-ui, sans-serif") || strings.Contains(html, "#2b6cb0") {
		t.Error("layout still carries the old inline light stylesheet — it must move to tokens.css/legacy.css")
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./web/ -run TestLayout`
Expected: FAIL — old layout has no `data-theme`, still uses unpkg + inline `system-ui` styles.

- [ ] **Step 3: Rewrite `web/layout.templ`**

Replace the `Layout` templ component (`layout.templ:18-97`) with this. The `TopQuery` struct and the `package web` + doc comment above it stay unchanged.

```templ
templ Layout(title, subtitle string) {
	<!DOCTYPE html>
	<html lang="en" data-theme="dark">
		<head>
			<meta charset="UTF-8"/>
			<meta name="viewport" content="width=device-width, initial-scale=1"/>
			<title>{ title }</title>
			<script>
				@templ.Raw(themeBootstrapJS)
			</script>
			<link rel="stylesheet" href="/static/css/tokens.css"/>
			<link rel="stylesheet" href="/static/css/legacy.css"/>
			<script src="/static/js/htmx.min.js" defer></script>
			<script src="/static/js/theme.js" defer></script>
		</head>
		<body>
			<h1>Lynceus</h1>
			<nav>
				<a href="/databases">Databases</a>
				<a href="/">Top queries</a>
				<a href="/insights">Insights</a>
				<a href="/index-advisor">Index advisor</a>
				<a href="/vacuum-advisor">Vacuum advisor</a>
				<a href="/config-advisor">Config advisor</a>
				<a href="/waits">Waits</a>
				<a href="/checks">Checks</a>
				<a href="/audit">Audit log</a>
			</nav>
			<p class="subtitle">{ subtitle }</p>
			{ children... }
		</body>
	</html>
}
```

Note on `@templ.Raw` inside `<script>`: templ treats `<script>` contents as raw text, so the `{`/`}` in the JS would otherwise be fine, but rendering via `@templ.Raw(themeBootstrapJS)` keeps the bootstrap in Go (testable, single source) and guarantees no templ interpretation. If `templ generate` rejects a component call inside a raw `<script>` element, fall back to a script template: add `script themeBootstrap() { }` — instead, define the inline script through templ's script-element support by wrapping as `<script type="text/javascript">@templ.Raw(themeBootstrapJS)</script>`. Verify which form `templ generate` accepts in Step 4; the `@templ.Raw` form is expected to work.

- [ ] **Step 4: Regenerate templ + build**

Run: `make templ && go build ./...`
Expected: `templ generate` regenerates `web/layout_templ.go`, build OK. If `templ generate` errors on the `@templ.Raw` inside `<script>`, switch that block to:

```templ
<script type="text/javascript">
	@templ.Raw(themeBootstrapJS)
</script>
```

and re-run. If it still errors, render the bootstrap as an attribute-free raw node: `@templ.Raw("<script>" + themeBootstrapJS + "</script>")` placed directly in `<head>`. Land on the first form that generates cleanly.

- [ ] **Step 5: Run the layout tests to verify they pass**

Run: `go test ./web/ -run TestLayout`
Expected: PASS (all four tests).

- [ ] **Step 6: Run the full web + api suite (regression)**

Run: `go test ./web/... ./internal/api/...`
Expected: PASS. Existing page/handler tests must still pass — the layout still exposes `<h1>`, `<nav>`, `.subtitle`, and `{ children... }`, so no page component signature changed.

- [ ] **Step 7: Commit**

```bash
git add web/layout.templ web/layout_templ.go web/layout_test.go
git commit -m "feat(web): wire tokens + theme + self-hosted assets into layout (ly-ae6.1)"
```

---

### Task 6: Full-suite verification + manual browser check

**Files:** none (verification only).

- [ ] **Step 1: Full build + vet + generated-code sync**

Run: `go build ./... && go vet ./web/... ./internal/api/... && make templ && git diff --exit-code web/`
Expected: build OK, vet clean, `git diff --exit-code` shows no uncommitted regen drift (generated code in sync — CI enforces this).

- [ ] **Step 2: Fast unit tests**

Run: `go test ./web/... ./internal/api/...`
Expected: PASS.

- [ ] **Step 3: Manual browser verification (uses the `verify` / `run` skill)**

Start the dev databases and API, then exercise the theme mechanism:

```bash
make dev-up
# export the dev DSNs the compose file provisions (config :5432, stats :5433) and dev auth:
export LYNCEUS_CONFIG_DSN='postgres://lynceus:lynceus@localhost:5432/lynceus?sslmode=disable'
export LYNCEUS_STATS_DSN='postgres://lynceus:lynceus@localhost:5433/lynceus?sslmode=disable'
export LYNCEUS_DEV_AUTH=true
export LYNCEUS_REQUIRE_TLS=false   # dev only; localhost DSNs are non-TLS
go run ./cmd/api &
```

In a browser at `http://localhost:8080/databases`, confirm:
1. Page renders **dark** by default (bg `#0c1118`), UI text in **Work Sans**, any mono/code in **JetBrains Mono** (DevTools → Network shows both woff2 loaded from `/static/fonts/`, no `fonts.googleapis.com`).
2. DevTools → Network: `htmx.min.js` loads from `/static/js/`, not unpkg.
3. In the console: `Lynceus.setTheme('light')` → flips to light; `Lynceus.setTheme('system')` → matches OS; `Lynceus.cycleTheme()` cycles; reload preserves the last choice (localStorage `lynceus.theme`).
4. `Lynceus.setAccent('#22d3ee')` → focus rings / links turn cyan; reload preserves it (localStorage `lynceus.accent`). `Lynceus.setAccent('#818cf8')` → indigo.
5. View source: `<html lang="en" data-theme="dark">`, the inline bootstrap present, no external hosts anywhere.

Confirm the DSN/env names against `docs/` or `docker-compose.dev.yml` if the compose file uses different credentials; adjust the exported values to match. Stop with `kill %1 && make dev-down` when done (or leave running for the session demo).

- [ ] **Step 4: Final state — no commit needed** (Task 6 is verification only; all code committed in Tasks 1–5).

---

## Self-Review

**Spec coverage** (against the F1 design + COMPARISON.md "Design system & tokens" gaps):
- CSS design-token layer (dark `:root` + light override, exact values) → Task 1. ✓
- Work Sans + JetBrains Mono, self-hosted, font-role split (`--font-ui`/`--font-mono`) → Task 2 + tokens.css. ✓
- Dark theme as DEFAULT → Task 1 (base styles) + Task 5 (`data-theme="dark"`). ✓
- Theme mechanism (data-theme attr, prefers-color-scheme, `system` initial pref, toggle wiring API) → Task 4 (bootstrap + theme.js). ✓ (toggle *button* is ly-ae6.2, per scope boundary.)
- Accent presets Teal/Cyan/Indigo, per-theme bright/deep variants → Task 4 bootstrap `ACCENTS` map + `applyAccent`/`setAccent`. ✓ (picker UI is ly-ae6.14.)
- Shape-language tokens (2px radius, 1px border, shadow policy) → Task 1 tokens (`--radius`, `--radius-badge`, `--border`, `--shadow-pop`). ✓ (component conformance audit is ly-ae6.14.)
- Self-hosted, no CDN (fonts + htmx off external hosts) → Tasks 2, 3; enforced by `TestLayout_NoExternalHosts`. ✓

**Placeholder scan:** No TBD/TODO; every code step shows full content; every test shows real assertions. ✓

**Type consistency:** `StaticHandler` (Task 1) used verbatim in `server.go` (Task 1 Step 8) and every serving test. `themeBootstrapJS` const (Task 4) referenced by `bootstrap_test.go` (Task 4) and `layout.templ` (Task 5). `window.Lynceus` methods `resolveTheme`/`applyAccent`/`ACCENTS` (bootstrap) consumed by `setTheme`/`cycleTheme`/`setAccent` (theme.js). Consistent. ✓

**Known transitional state (intended, per approved design decision):** the base shell is dark while existing screens keep legacy component CSS (with a legibility patch) until ly-ae6.7. Not a gap.
