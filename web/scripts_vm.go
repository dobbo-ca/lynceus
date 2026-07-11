package web

import (
	"fmt"
	"net/url"
	"time"
)

// SavedScriptRow is the view-model for one row of the Saved Scripts list.
// Every field is user-authored metadata or a token color string — no
// monitored-database literal.
type SavedScriptRow struct {
	ID          int64
	Name        string
	Description string
	Scope       string // GLOBAL | TEAM | PERSONAL
	ScopeColor  string // e.g. "var(--acc2)"
	VisibleTo   string // "everyone in the org" | "group dba-oncall" | "only you" | "only <owner>"
	Owner       string
	SavedAge    string // "12d ago"
	Mine        bool   // owner == viewer; drives the delete button
	DetailHref  string // /scripts/<id>
	LoadHref    string // /console?script=<id>  (load-without-run hand-off)
}

// SavedScriptsVM is the view-model for the Saved Scripts list surface.
type SavedScriptsVM struct {
	Query   string // echoed search text
	SubLine string // "<n> SCRIPTS · GLOBAL — EVERYONE · TEAM — DBA-ONCALL · PERSONAL — OWNER ONLY"
	Count   int
	Rows    []SavedScriptRow
}

// ScriptScopeOption is one selectable scope in the owner's ACCESS switch.
type ScriptScopeOption struct {
	Label  string // GLOBAL | TEAM | PERSONAL
	Active bool
}

// ScriptTargetOption is one match in the run-flow target search list.
type ScriptTargetOption struct {
	Label     string // cluster/node/database label
	Kind      string // CLUSTER | NODE | DATABASE
	KindColor string // token var
	Value     string // "<kind>|<cluster>|<node-or-db-or-empty>"
	Active    bool
}

// ScriptTargetChip is one node/database chip once a target is selected.
type ScriptTargetChip struct {
	Label  string
	Value  string // accumulated run-state value threaded into the chip href
	Active bool
}

// ScriptRunVM is the RUN card state (search → select target → pick node/db
// → RUN). It is re-rendered as a fragment on every selection.
type ScriptRunVM struct {
	ScriptID       int64
	TargetQuery    string
	Targets        []ScriptTargetOption
	Selected       bool
	SelectedTarget string // opaque target value threaded into chip hrefs
	NodeChips      []ScriptTargetChip
	DBChips        []ScriptTargetChip
	RunReady       bool
	RunLabel       string
	RunHint        string
	RunHref        string // /console?...  set only when RunReady
}

// ScriptDetailVM is the full script detail surface.
type ScriptDetailVM struct {
	ID           int64
	Name         string
	Description  string
	SQLText      string
	Scope        string
	ScopeColor   string
	Owner        string
	SavedAge     string
	VisibleTo    string
	Mine         bool
	ScopeOptions []ScriptScopeOption // populated only when Mine
	ManagedBy    string              // owner name, shown to non-owners
	Run          ScriptRunVM
}

// ScriptSearchItem is one row in the console's saved-script search dropdown.
type ScriptSearchItem struct {
	Name        string
	Description string
	Scope       string
	ScopeColor  string
	LoadHref    string // /console?script=<id>
}

// ScriptSearchVM is the console saved-script search dropdown.
type ScriptSearchVM struct {
	Items []ScriptSearchItem
	Empty bool
}

// RelativeAge renders a compact, coarse "time since" label matching the
// design ("just now", "5m ago", "3h ago", "2d ago").
func RelativeAge(t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d/time.Hour))
	default:
		return fmt.Sprintf("%dd ago", int(d/(24*time.Hour)))
	}
}

// ScriptScopeColor maps a scope to its token color (mirrors the prototype's
// scopeColors map: GLOBAL acc2, TEAM infoT, PERSONAL warnT).
func ScriptScopeColor(scope string) string {
	switch scope {
	case "GLOBAL":
		return "var(--acc2)"
	case "TEAM":
		return "var(--infoT)"
	case "PERSONAL":
		return "var(--warnT)"
	default:
		return "var(--mut)"
	}
}

// ScriptVisibleTo renders the "visible to …" copy for a scope.
func ScriptVisibleTo(scope, owner, ownerGroup string, mine bool) string {
	switch scope {
	case "GLOBAL":
		return "everyone in the org"
	case "TEAM":
		return "group " + ownerGroup
	default:
		if mine {
			return "only you"
		}
		return "only " + owner
	}
}

// itoa formats an int64 id for URL path segments.
func itoa(id int64) string { return fmt.Sprintf("%d", id) }

// urlq percent-encodes a value for use in a query string.
func urlq(v string) string { return url.QueryEscape(v) }
