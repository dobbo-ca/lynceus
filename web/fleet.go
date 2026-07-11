package web

// FleetStat is one stat-strip cell. ValueClass is a fleet color utility class
// (e.g. "fl-crit"); "" -> default text color.
type FleetStat struct {
	Label      string
	Value      string
	Sub        string
	ValueClass string
}

// FleetAttentionRow is one Needs-Attention row (already formatted + linked).
type FleetAttentionRow struct {
	SevClass string // "fl-sq-crit" | "fl-sq-warn" | "fl-sq-info"
	ID       string
	Detail   string
	Server   string
	Age      string // "2d" | "4h" | "18m"
	Href     string // scope-aware deep link
}

// FleetClusterCard is one problem-only cluster card (already formatted).
type FleetClusterCard struct {
	Name         string
	Version      string // "" -> version chip hidden
	Provider     string // "" -> provider chip hidden
	ProviderName string
	Engine       string
	EngineIcon   string
	Health       string
	HealthClass  string // "fl-crit" | "fl-warn" | "fl-ok"
	QPS          string
	LatencyMs    string
	Conns        string
	TopWait      string
	Crit         int
	Warn         int
	Info         int
	Href         string
}

// FleetLink is a labeled navigation link. Used by the all-clear panel
// (per-vertical "healthy" links) and the hidden-healthy footer.
type FleetLink struct {
	Label string
	Href  string
}

// FleetView is the presentation view-model for the whole dashboard.
type FleetView struct {
	Row1          []FleetStat // engine-neutral counts (DATABASES [+SEARCH +CACHE])
	Row2          []FleetStat // OPEN CRITICAL / WARN / INFO
	Attention     []FleetAttentionRow
	AttnCrit      int // header "n CRIT"
	AttnWarn      int // header "n WARN"
	Cards         []FleetClusterCard // problem-only, already sorted
	HealthyLinks  []FleetLink        // all-clear panel per-vertical links ("N DATABASE CLUSTERS HEALTHY →")
	HiddenLinks   []FleetLink        // footer "N HEALTHY DB CLUSTERS NOT SHOWN →" (+ per-engine ALL links when enabled)
	Healthy       bool               // all-clear: no open checks/insights of any band
	LoadError     bool               // BuildFleetView failed — render an error panel, never a false all-clear
	RangeLabel    string             // header label, e.g. "24H"
	Range         string             // canonical range param ("24H") echoed into poll/toggle URLs
	Sort          string             // "health" | "name" (echoed into toggle + poll URL)
	EngineSummary string             // e.g. "5 DB CLUSTERS / RANGE 24H"
}

// fleetSortLabel renders the current sort mode for the SORT toggle.
func fleetSortLabel(sort string) string {
	if sort == "name" {
		return "NAME"
	}
	return "HEALTH"
}

// fleetOtherSort returns the mode the toggle switches to.
func fleetOtherSort(sort string) string {
	if sort == "name" {
		return "health"
	}
	return "name"
}

// fleetPartialURL builds the HTMX refresh URL, preserving the active sort +
// range so the 30s auto-poll and the SORT toggle never clobber the user's
// selection. Rendered from inside #fleet-body so every swap carries the live
// params. `&` is HTML-escaped to `&amp;` by templ in the attribute value. The
// range is the shell's canonical UPPER label (web.ParseRange), e.g. "24H".
func fleetPartialURL(sort, rng string) string {
	if rng == "" {
		rng = DefaultRange
	}
	return "/partial/fleet?sort=" + sort + "&range=" + rng
}
