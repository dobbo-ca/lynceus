package web

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// DomainStatus is the OpenSearch/Elasticsearch cluster-health rollup for a
// domain, surfaced as GREEN / YELLOW / RED. It is a T1 enum — never a literal.
type DomainStatus string

const (
	DomainGreen  DomainStatus = "GREEN"
	DomainYellow DomainStatus = "YELLOW"
	DomainRed    DomainStatus = "RED"
)

// Class returns the CSS class carrying this status's color (see search.css).
func (s DomainStatus) Class() string { return "sd-status--" + string(s) }

// Tone returns the tone-* class suffix used for the STATUS stat cell.
func (s DomainStatus) Tone() string {
	switch s {
	case DomainRed:
		return "crit"
	case DomainYellow:
		return "warn"
	default:
		return "ok"
	}
}

// DomainStat is one cell of a domain card's stat strip. Value/Sub are formatted
// T1 strings; Tone is a tone-* class suffix selecting a token color.
type DomainStat struct {
	Label string
	Value string
	Sub   string
	Tone  string
}

// SearchDomainCard is the view-model for one OpenSearch/ES domain. Every field
// is a structural identifier, a count/aggregate, an enum, or a package-authored
// reason string — no monitored-datastore literal.
type SearchDomainCard struct {
	Name         string
	Version      string
	Provider     string
	Status       DomainStatus
	StatusReason string
	Stats        []DomainStat
	RoleSummary  string
}

// SearchDomainsView is the Domains screen view-model.
type SearchDomainsView struct {
	Domains []SearchDomainCard
}

// SearchNodeRow is one row of the Nodes-by-role table.
type SearchNodeRow struct {
	Name             string
	Roles            []string
	Version          string
	Heap             string
	CPU              string
	Disk             string
	Shards           string
	DedicatedManager bool
}

// ShardsClass dims the shard count for a dedicated cluster-manager node (which
// holds no shards) and otherwise uses the muted metric color.
func (n SearchNodeRow) ShardsClass() string {
	if n.DedicatedManager {
		return "sn-shards--zero"
	}
	return "tone-mut"
}

// SearchNodesView is the Nodes screen view-model. Sort is "heap" (default) or
// "name"; the handler has already applied it to Nodes.
type SearchNodesView struct {
	Nodes []SearchNodeRow
	Sort  string
}

// SortLabel is the human label for the current sort.
func (v SearchNodesView) SortLabel() string {
	if v.Sort == "name" {
		return "NAME"
	}
	return "HEAP"
}

// NextSort returns the sort key the toggle should switch to.
func (v SearchNodesView) NextSort() string {
	if v.Sort == "name" {
		return "heap"
	}
	return "name"
}

// ComputeDomainStatus maps the raw cluster-health color (from _cluster/health.status:
// green|yellow|red) plus the unassigned-shard count into a display status and a
// package-authored, literal-free reason string.
func ComputeDomainStatus(health string, unassignedShards int, worstNode string) (DomainStatus, string) {
	switch strings.ToLower(strings.TrimSpace(health)) {
	case "red":
		return DomainRed, fmt.Sprintf("%d unassigned primary shards", unassignedShards)
	case "yellow":
		if unassignedShards > 0 && worstNode != "" {
			return DomainYellow, fmt.Sprintf("%d unassigned replica shards on %s", unassignedShards, worstNode)
		}
		return DomainYellow, "replica shards not fully allocated"
	default:
		return DomainGreen, "all shards assigned"
	}
}

// SortSearchNodes sorts nodes in place: by heap descending (default) or by name
// ascending. Heap is parsed leniently from its "NN%" string form.
func SortSearchNodes(nodes []SearchNodeRow, sort string) {
	if sort == "name" {
		slices.SortFunc(nodes, func(a, b SearchNodeRow) int { return strings.Compare(a.Name, b.Name) })
		return
	}
	slices.SortFunc(nodes, func(a, b SearchNodeRow) int { return heapPct(b.Heap) - heapPct(a.Heap) })
}

// heapPct parses "58%" (or " 0% ") into 58. Unparseable → 0.
func heapPct(s string) int {
	n, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimSpace(s), "%"))
	return n
}

// IsDedicatedManager reports whether a node's only role is CLUSTER_MANAGER, i.e.
// a dedicated manager that holds zero shards.
func IsDedicatedManager(roles []string) bool {
	return len(roles) == 1 && roles[0] == "CLUSTER_MANAGER"
}
