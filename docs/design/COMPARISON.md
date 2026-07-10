# Lynceus UI — Design Reference & Parity Index

This directory holds the **UI design source of truth** for Lynceus, imported from the
claude.ai/design handoff (`design_handoff_lynceus_monitoring`) on **2026-07-10**.

Its purpose is durable, version-controlled reference so **any session can compare the
current frontend against the intended design** without re-importing the bundle.

## Contents & read order

1. **`PRODUCT_INTENT.md`** — the *why*: decision log from the design conversation.
   **Source of truth for intent.** Where the prototype and this doc disagree, this wins;
   flag the discrepancy.
2. **`README.md`** — the *what*: handoff overview, screen-by-screen spec, scope model,
   design tokens, tweakable props.
3. **`Lynceus.dc.html`** — the high-fidelity interactive prototype (template + logic +
   demo data). The pixel/token/interaction truth. Open in a browser to compare.
4. **`support.js`** — prototype runtime. **Reference only**, not code to port.

Read order for a fresh session: `PRODUCT_INTENT.md` → `README.md` → open `Lynceus.dc.html`.

## Ground rules (from the handoff)

- These are **design references**, not production code. Recreate faithfully in the
  codebase's stack (**templ + HTMX**, see `web/`), not by copying the HTML/JS.
- **Fidelity is high** — colors, typography, spacing, interaction patterns are final.
- All demo data in the prototype is hardcoded; replace with real APIs.
- Privacy backbone still governs: only T1 (normalized, literal-free) data renders unless
  a screen is explicitly T2 (SQL Console, audited reads). See root `CLAUDE.md`.

## How to open the prototype

```bash
open docs/design/Lynceus.dc.html   # macOS; support.js loads from the same dir
```

Toggle prototype props (in the HTML) to exercise states before comparing:
`fleetState` (`unhealthy`|`healthy`), `enablePostgres`/`enableRedis`/`enableValkey`/
`enableElasticsearch`/`enableOpensearch`, `accentColor`, `defaultTheme`.

## Design → current implementation map

Current frontend lives in `web/*.templ`. It covers **cluster-scoped Postgres screens
only**; the design is much broader. This table is the parity baseline — status reflects
`web/` as of 2026-07-10, verify before relying on it.

| Design screen (README §Screens)     | Current `web/` file(s)                          | Status |
|-------------------------------------|-------------------------------------------------|--------|
| Fleet dashboard (triage surface)    | —                                               | ❌ absent |
| Scope model + per-scope nav rebuild | `layout.templ` (flat nav, no scope)             | ❌ absent |
| Top-bar SCOPE picker / `← FLEET`    | —                                               | ❌ absent |
| Scoped Overview ("open issues on…") | `overview.templ` (cluster topology, no issues)  | ⚠️ partial |
| Database › Clusters list            | `databases.templ` (`DatabaseCard` grid)         | ⚠️ partial |
| Database › Nodes (rollup, sources)  | —                                               | ❌ absent |
| Database › Databases (cluster-qual) | `databases.templ`                               | ⚠️ partial |
| Cluster detail (tabs, charts)       | `overview.templ`, `cluster_views.templ`         | ⚠️ partial |
| Queries                             | `queries.templ`                                 | ✅ present |
| Insights                            | `insights.templ`, `cluster_views.templ`         | ✅ present |
| Query plan visualization            | `plan.templ`                                    | ✅ present (extra) |
| Advisors › Index                    | `index_advisor.templ`                           | ✅ present |
| Advisors › Vacuum                   | `vacuum_advisor.templ`                          | ✅ present |
| Advisors › Config (per node)        | `config_advisor.templ`                          | ✅ present |
| Activity / Waits                    | `waits.templ`                                   | ✅ present |
| Checks & Alerts                     | `checks.templ` (checks; alerts?)                | ⚠️ partial |
| Audit Log                           | `audit.templ`                                   | ✅ present |
| Capabilities (database-level)       | —                                               | ❌ absent |
| SQL Console (T2, session grant)     | —                                               | ❌ absent |
| Saved Scripts (global/team/personal)| —                                               | ❌ absent |
| Schema                              | —                                               | ❌ absent (roadmap) |
| Logs                                | —                                               | ❌ absent (roadmap) |
| Search vertical (OpenSearch/ES)     | —                                               | ❌ absent |
| Cache vertical (Valkey/Redis)       | —                                               | ❌ absent |
| Onboarding wizard (+ ADD)           | —                                               | ❌ absent |
| Provider Setup (AWS/Azure/PS guides)| —                                               | ❌ absent |
| Access & Roles                      | —                                               | ❌ absent |
| Settings › Appearance (accent)      | —                                               | ❌ absent |

### Cross-cutting gaps (present in design, not in `web/`)

- **Design system**: `layout.templ` uses `system-ui` + a light-only ad-hoc stylesheet.
  Design mandates **Work Sans** (UI) + **JetBrains Mono** (data/labels), a full **dark +
  light** token set, 2px radius / 1px border shape language. None applied yet.
- **Scope as the organizing principle**: nav, breadcrumbs, and screen availability are all
  scope-driven in the design; the implementation has a single flat nav.
- **Multi-engine + provider awareness**: engine icons/version chips, provider chips,
  blind-spot rendering — not started.

## Related repo docs

- `docs/specs/2026-05-29-lynceus-design.md` — full system design.
- `docs/specs/2026-05-29-lynceus-features.md` — feature specs (parity priority, data
  source, locality, privacy classification per feature).
- `docs/specs/2026-06-08-parity-ha-perf-roadmap.md` — parity roadmap.
</content>
</invoke>
