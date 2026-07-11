package web

import (
	"net/url"
	"strings"

	"github.com/a-h/templ"
	"github.com/dobbo-ca/lynceus/internal/scope"
)

// DefaultRange is used when the ?range param is absent or invalid.
const DefaultRange = "24H"

// ValidRanges is the ordered time-range segmented control (15M/1H/24H/7D/30D).
var ValidRanges = []string{"15M", "1H", "24H", "7D", "30D"}

// ParseRange canonicalizes the ?range param; unknown -> DefaultRange.
func ParseRange(raw string) string {
	up := strings.ToUpper(strings.TrimSpace(raw))
	for _, r := range ValidRanges {
		if up == r {
			return r
		}
	}
	return DefaultRange
}

// ScopeHref is the canonical scope-set URL shared by the picker, the ⌖ row
// buttons, and deep links: it lands on the scope's OVERVIEW screen with the
// scope encoded in the single "scope" query param. Fleet -> "/". Each scope
// kind resolves to its per-scope Overview route via screenPath/NavHref (ly-ae6.6
// repoint): cluster -> /cluster, node/pooler -> /nodes, database -> /databases/all.
func ScopeHref(sc scope.Scope) templ.SafeURL {
	return templ.SafeURL(NavHref(sc, scopeOverviewScreen(sc)))
}

// scopeOverviewScreen maps a scope kind to the design screen id of its Overview
// destination — the same screen each scope's sidebar "Overview" nav item points
// at (see BuildNav in nav.go). Keeping the two in lockstep means the picker, the
// row buttons, and the sidebar all land on one screen per scope.
func scopeOverviewScreen(sc scope.Scope) string {
	switch sc.Kind {
	case scope.Cluster:
		return "clusterdetail"
	case scope.Node, scope.Pooler:
		return "nodes"
	case scope.Database:
		return "databases"
	default:
		return "fleet"
	}
}

// RangeOption is one segmented-control entry.
type RangeOption struct {
	Label    string
	Selected bool
	Href     templ.SafeURL
}

// RangeOptions builds the five range entries, preserving BOTH the active scope
// and the CURRENT screen on each href, and marking the selected one. screen is
// the design screen id of the page being rendered (threaded in by the shell
// builder); the range control therefore keeps the user on that screen instead of
// bouncing them back to the fleet landing (the ly-ae6.6 repoint, coordinated with
// ScopeHref). An unknown/empty screen falls back to the "/" base.
func RangeOptions(current string, sc scope.Scope, screen string) []RangeOption {
	current = ParseRange(current)
	base, ok := screenPath[screen]
	if !ok {
		base = "/"
	}
	out := make([]RangeOption, 0, len(ValidRanges))
	for _, r := range ValidRanges {
		v := url.Values{"range": {r}}
		if !sc.IsFleet() {
			v.Set("scope", sc.Encode())
		}
		out = append(out, RangeOption{
			Label:    r,
			Selected: r == current,
			Href:     templ.SafeURL(base + "?" + v.Encode()),
		})
	}
	return out
}

// ScopeOption is one row in the searchable SCOPE picker. Kind is
// CLUSTER|NODE|POOLER|DATABASE; Depth drives the indent — 0 for a cluster, 1
// for its children (nodes, poolers, and databases are all cluster-level, so
// they share the single indent, matching the design's flat pad-0/pad-1 layout);
// ScopeKey is scope.Scope.Encode() (used to mark the active option Current).
type ScopeOption struct {
	Label    string
	Kind     string
	Depth    int
	ScopeKey string
	Href     templ.SafeURL
	Current  bool
}

// ShellUser is the identity shown in the user menu. Until OIDC lands
// (ly-8b0.1) the handler supplies a static dev identity.
type ShellUser struct {
	Name      string
	Group     string
	T2Granted bool
}

// ShellView is the top-bar + shell view-model. Sidebar is the per-scope nav
// component supplied by ly-ae6.3; when nil the shell renders a placeholder.
type ShellView struct {
	Scope        scope.Scope
	ScopeLabel   string // "FLEET" or the resolved entity label
	Scoped       bool   // !Scope.IsFleet()
	ClearHref    templ.SafeURL
	LogoHref     templ.SafeURL
	Range        string
	Ranges       []RangeOption
	PollSecs     int
	Options      []ScopeOption
	OptionsQuery string
	User         ShellUser
	Sidebar      templ.Component
	Title        string
}

// userInitials returns up to two uppercase initials from a username like
// "s.dobson" -> "SD" for the user chip.
func userInitials(name string) string {
	parts := strings.FieldsFunc(name, func(r rune) bool { return r == '.' || r == ' ' || r == '_' || r == '-' })
	var out []rune
	for _, p := range parts {
		if p == "" {
			continue
		}
		out = append(out, []rune(strings.ToUpper(p))[0])
		if len(out) == 2 {
			break
		}
	}
	if len(out) == 0 {
		return "?"
	}
	return string(out)
}

// userMeta is the identity sub-line: "GROUP: <group> · T2 GRANTED|T2 OFF".
func userMeta(u ShellUser) string {
	t2 := "T2 OFF"
	if u.T2Granted {
		t2 = "T2 GRANTED"
	}
	return "GROUP: " + strings.ToUpper(u.Group) + " · " + t2
}
