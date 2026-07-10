#!/usr/bin/env bash
# File the "UI design parity" epic + workstream children in beads.
#
# Prereq: bd writes must be unblocked. As of 2026-07-10 the local beads DB had 4
# pending schema migrations (v49->v53) and bd refused writes on the remote-backed
# clone. Resolve first, on ONE designated migrator machine:
#     BD_ALLOW_REMOTE_MIGRATE=1 bd migrate && bd dolt push
# or, to adopt an already-migrated remote (drops unpushed local issues):
#     bd export --all -o backup.jsonl && bd bootstrap
#
# Idempotency: this script is NOT idempotent — running twice creates duplicate
# issues. Run once; if it fails partway, close the partial issues before retrying.
#
# Source of truth for scope/priorities/deps: docs/design/COMPARISON.md (Tracking plan).
set -euo pipefail

new() { # new <title> <priority> [extra bd args...]; echoes the created id
  local title="$1" prio="$2"; shift 2
  local out id
  out="$(bd create --title="$title" --priority="$prio" "$@")"
  id="$(printf '%s' "$out" | grep -oiE 'ly-[a-z0-9]+(\.[0-9]+)?' | head -1)"
  [ -n "$id" ] || { echo "FAILED to parse id from: $out" >&2; exit 1; }
  echo "$id"
}

EPIC="$(new '[epic] UI design parity — implement the design handoff (shell, scope, verticals)' 1 \
  --type=epic --label=needs-plan \
  --description='Track the frontend gap between current web/*.templ (early Postgres-cluster slice; functional but no design system / scope model) and the high-fidelity handoff at docs/design/. Verified parity map + per-area gaps: docs/design/COMPARISON.md (ui-design-parity-audit workflow, adversarially verified, 2026-07-10). Children are UI/design workstreams; backend data for most is tracked under M2-M6 and referenced as deps. Each child stays needs-plan until a plan lands under docs/superpowers/plans/.')"
echo "epic=$EPIC"

child() { # child <var-desc> <title> <priority> <description>
  new "$2" "$3" --type=feature --parent="$EPIC" --label=needs-plan --description="$4"
}

F1="$(child F1 'UI: Design-token CSS layer + self-hosted fonts + theme mechanism' 1 \
  'Foundation, blocks the rest. Replace hardcoded hex in web/layout.templ with a CSS custom-property token set matching docs/design/README.md dark+light palettes; self-host Work Sans (UI 13px) + JetBrains Mono (data/labels, tabular-nums, no CDN); dark-DEFAULT theme with light + system via data-theme + prefers-color-scheme. See COMPARISON.md design-system.')"
F2="$(child F2 'UI: Global top bar + scope model + searchable SCOPE picker' 1 \
  'The 48px top bar (logo->Fleet, time-range 15M/1H/24H/7D/30D, poll indicator, theme toggle, user menu) plus the scope model (fleet->cluster->node/pooler->database, set via picker or row select-scope buttons, deep-links to explanations, back-to-FLEET). See COMPARISON.md top-bar + scope-model.')"
F3="$(child F3 'UI: Scope-driven sidebar nav rebuild (per-scope nav trees)' 1 \
  'Rebuild the sidebar per scope level; low-level sections never at fleet scope; Saved Scripts everywhere, SQL Console only cluster/node/db. See COMPARISON.md sidebar-nav.')"
D="$(child D 'UI: Fleet dashboard — triage strips, needs-attention, problem-only cards, healthy/unhealthy' 1 \
  'At-a-glance triage: engine-neutral stat strips, computed needs-attention list that sets scope + opens the explanation, problem-only cluster cards with healthy hidden behind footer links, all-clear healthy state. See COMPARISON.md fleet-dashboard.')"
E="$(child E 'UI: Database vertical lists — Clusters / Nodes / Databases (+ blind spots)' 2 \
  'Clusters (sortable, engine/version/provider chips), Nodes (cluster groups, DB->NODE->CLUSTER rollup, data-source lines, per-node versions, dimmed BLIND SPOT for RDS Multi-AZ / Azure HA standby), Databases (cluster-qualified identity). See COMPARISON.md db-clusters/db-nodes/db-databases/provider-blindspots.')"
G="$(child G 'UI: Scoped Overview + Cluster/Node/Pooler detail' 2 \
  'Scoped Overview leading with OPEN ISSUES ON THIS <scope> (or green no-open strip); node cards with select-scope; tabs; Config + Capabilities per scope; pooler pgbouncer views. See COMPARISON.md cluster-detail + node-pooler-scope.')"
H="$(child H 'UI: Design-parity retrofit of existing scoped screens' 2 \
  'Retrofit queries/insights/advisors/waits/checks/plan/audit (present but designParity none/partial) to the token system + scope context once F1-F3 land. See COMPARISON.md queries/insights/advisors/activity-waits/checks-alerts/plan-viz/governance-audit.')"
I="$(child I 'UI: SQL Console (T2) — target picker, session-grant gate, results, strict audit' 2 \
  'Cluster/node/db scope only; target picker (RUN inert until resolved); per-cluster time-boxed session grant banner + request gate; editor with row-limit/timeout; paginated results with per-user rows-per-page + COPY/CSV/SQL export; strict per-statement audit to history + org audit log. See COMPARISON.md sql-console.')"
J="$(child J 'UI: Saved Scripts — global/team/personal scopes, load/run, cross-scope search' 2 \
  'Available at every scope; scopes global(org)/team(group)/personal(owner); owner/admin change-access/delete with audited scope changes; load-without-run; run-a-script searches clusters/nodes/databases then requires node+db before running. See COMPARISON.md saved-scripts.')"
K="$(child K 'UI: Search vertical (OpenSearch/ES) — Domains + Nodes-by-role' 2 \
  'Domain->nodes-with-roles (cluster_manager/data/ingest/coordinating; managers hold 0 shards); GREEN/YELLOW/RED status with reason; gated on enableElasticsearch||enableOpensearch. See COMPARISON.md search-vertical + docs/research/expand-opensearch.md.')"
L="$(child L 'UI: Cache vertical (Valkey/Redis) — Clusters/Replicasets/Nodes' 2 \
  'cluster(sentinel)->replicaset(1 primary+N replicas)->nodes; writes only to replicaset primary (READ-WRITE/READ-ONLY badges); gated on enableRedis||enableValkey. See COMPARISON.md cache-vertical + docs/research/expand-redis.md.')"
M="$(child M 'UI: Onboarding wizard (+ ADD) + Provider Setup guides' 2 \
  'Per-vertical modal: provider chips -> collector token -> copyable k8s Deployment YAML (TARGET_KIND) -> provider step -> self-register on first report. Provider Setup admin page: big block buttons; AWS 3-path template (direct agent w/ tiered env-placeholder role grants; RDS-scoped IAM; Firehose ingress w/ endpoint+auth+tenant) + Terraform variant; Azure + PlanetScale. See COMPARISON.md onboarding-wizard/provider-setup + PRODUCT_INTENT.md 8.')"
N="$(child N 'UI: Governance/Access/Settings — user menu, Audit Log parity, Appearance, Access & Roles' 2 \
  'Org governance/admin under the top-right user menu (not main nav): Audit Log (tier badges, T2 amber-striped, hash chain), Access & Roles, Settings. Settings Appearance = accent presets persisted per user with per-theme variants. See COMPARISON.md governance-audit/access-roles/settings-appearance.')"
O="$(child O 'UI: Accent presets + shape-language conformance' 3 \
  'Teal/Cyan/Indigo accent presets (per-theme bright/deep variants, matching bg/hover) driven by a data-accent attribute; shape pass: 2px radius (1px tiny badges), 1px borders, no shadows except dropdowns/modals, unrounded 8px severity squares, 24px icon buttons. See COMPARISON.md design-system.')"

echo "children: F1=$F1 F2=$F2 F3=$F3 D=$D E=$E G=$G H=$H I=$I J=$J K=$K L=$L M=$M N=$N O=$O"

# Dependencies (issue depends on depends-on). Internal foundation ordering + cross-refs
# to existing backend beads.
dep() { bd dep add "$1" "$2"; }
dep "$F2" "$F1"
dep "$F3" "$F2"
dep "$D"  "$F2"; dep "$D" "$F3"
dep "$E"  "$F3"
dep "$G"  "$F2"; dep "$G" "$F3"
dep "$H"  "$F3"
dep "$I"  "$F3"; dep "$I" ly-8b0.6   # T2 data-access gating + per-read audit
dep "$J"  "$F3"
dep "$K"  "$F3"
dep "$L"  "$F3"
dep "$M"  "$F2"; dep "$M" ly-8b0.8    # collector enrollment + scoped token issuance
dep "$N"  "$F2"; dep "$N" ly-8b0.4    # RBAC org->server->database scoping
dep "$O"  "$F1"

echo "Done. Review: bd show $EPIC"
