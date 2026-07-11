package web

import (
	"context"
	"strings"
	"testing"
)

func TestComputeDomainStatus(t *testing.T) {
	cases := []struct {
		name       string
		health     string
		unassigned int
		worstNode  string
		wantStatus DomainStatus
		wantReason string
	}{
		{"red primaries", "red", 1, "", DomainRed, "1 unassigned primary shards"},
		{"yellow with node", "yellow", 2, "os-data-2", DomainYellow, "2 unassigned replica shards on os-data-2"},
		{"yellow no node", "yellow", 0, "", DomainYellow, "replica shards not fully allocated"},
		{"green", "green", 0, "", DomainGreen, "all shards assigned"},
		{"green upper-case input", "GREEN", 0, "", DomainGreen, "all shards assigned"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotStatus, gotReason := ComputeDomainStatus(c.health, c.unassigned, c.worstNode)
			if gotStatus != c.wantStatus {
				t.Errorf("status = %q, want %q", gotStatus, c.wantStatus)
			}
			if gotReason != c.wantReason {
				t.Errorf("reason = %q, want %q", gotReason, c.wantReason)
			}
		})
	}
}

func TestDomainStatus_ClassTone(t *testing.T) {
	for _, c := range []struct {
		s         DomainStatus
		wantClass string
		wantTone  string
	}{
		{DomainGreen, "sd-status--GREEN", "ok"},
		{DomainYellow, "sd-status--YELLOW", "warn"},
		{DomainRed, "sd-status--RED", "crit"},
	} {
		if got := c.s.Class(); got != c.wantClass {
			t.Errorf("%s.Class() = %q, want %q", c.s, got, c.wantClass)
		}
		if got := c.s.Tone(); got != c.wantTone {
			t.Errorf("%s.Tone() = %q, want %q", c.s, got, c.wantTone)
		}
	}
}

func TestIsDedicatedManager(t *testing.T) {
	for _, c := range []struct {
		roles []string
		want  bool
	}{
		{[]string{"CLUSTER_MANAGER"}, true},
		{[]string{"CLUSTER_MANAGER", "DATA"}, false},
		{[]string{"DATA"}, false},
		{[]string{"COORDINATING"}, false},
		{nil, false},
	} {
		if got := IsDedicatedManager(c.roles); got != c.want {
			t.Errorf("IsDedicatedManager(%v) = %v, want %v", c.roles, got, c.want)
		}
	}
}

func TestSortSearchNodes(t *testing.T) {
	base := func() []SearchNodeRow {
		return []SearchNodeRow{
			{Name: "b", Heap: "20%"},
			{Name: "a", Heap: "80%"},
			{Name: "c", Heap: "50%"},
		}
	}
	byHeap := base()
	SortSearchNodes(byHeap, "heap")
	if got := []string{byHeap[0].Name, byHeap[1].Name, byHeap[2].Name}; got[0] != "a" || got[1] != "c" || got[2] != "b" {
		t.Errorf("heap sort order = %v, want [a c b]", got)
	}
	byName := base()
	SortSearchNodes(byName, "name")
	if got := []string{byName[0].Name, byName[1].Name, byName[2].Name}; got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Errorf("name sort order = %v, want [a b c]", got)
	}
}

func TestSearchNodesView_SortLabelNextSort(t *testing.T) {
	heap := SearchNodesView{Sort: "heap"}
	if heap.SortLabel() != "HEAP" || heap.NextSort() != "name" {
		t.Errorf("heap view: label=%q next=%q, want HEAP/name", heap.SortLabel(), heap.NextSort())
	}
	name := SearchNodesView{Sort: "name"}
	if name.SortLabel() != "NAME" || name.NextSort() != "heap" {
		t.Errorf("name view: label=%q next=%q, want NAME/heap", name.SortLabel(), name.NextSort())
	}
}

func TestSearchNodeRow_ShardsClass(t *testing.T) {
	ded := SearchNodeRow{DedicatedManager: true}
	if got := ded.ShardsClass(); got != "sn-shards--zero" {
		t.Errorf("dedicated ShardsClass() = %q, want sn-shards--zero", got)
	}
	data := SearchNodeRow{DedicatedManager: false}
	if got := data.ShardsClass(); got != "tone-mut" {
		t.Errorf("data ShardsClass() = %q, want tone-mut", got)
	}
}

func renderSearchDomains(t *testing.T, v SearchDomainsView) string {
	t.Helper()
	var sb strings.Builder
	if err := SearchDomainsBody(v).Render(context.Background(), &sb); err != nil {
		t.Fatalf("render domains: %v", err)
	}
	return sb.String()
}

func renderSearchNodes(t *testing.T, v SearchNodesView) string {
	t.Helper()
	var sb strings.Builder
	if err := SearchNodesBody(v).Render(context.Background(), &sb); err != nil {
		t.Fatalf("render nodes: %v", err)
	}
	return sb.String()
}

func sampleDomain() SearchDomainCard {
	return SearchDomainCard{
		Name: "search-logs", Version: "2.19", Provider: "SELF-HOSTED",
		Status: DomainYellow, StatusReason: "2 unassigned replica shards on os-data-2",
		RoleSummary: "3× CLUSTER_MANAGER · 2× DATA+INGEST · 1× COORDINATING",
		Stats: []DomainStat{
			{Label: "STATUS", Value: "YELLOW", Sub: "2 unassigned replica shards", Tone: "warn"},
			{Label: "INDICES", Value: "14", Sub: "86 shards (P+R)", Tone: "text"},
			{Label: "NODES", Value: "6", Sub: "3 mgr · 2 data+ingest · 1 coord", Tone: "text"},
			{Label: "JVM HEAP", Value: "58%", Sub: "fleet mean · GC healthy", Tone: "text"},
			{Label: "SEARCH RATE", Value: "1,840/s", Sub: "p95 latency 46 ms", Tone: "text"},
		},
	}
}

func TestSearchDomainsBody_parity(t *testing.T) {
	html := renderSearchDomains(t, SearchDomainsView{Domains: []SearchDomainCard{sampleDomain()}})
	for _, want := range []string{
		`/static/css/search.css`, // token component CSS linked
		`<circle cx="10.5"`,      // inlined eng-os magnifier glyph
		"search-logs", "v2.19", "SELF-HOSTED", // header identity
		"sd-status--YELLOW", "[YELLOW]", // status class + label
		"2 unassigned replica shards on os-data-2",              // status reason
		"STATUS", "INDICES", "NODES", "JVM HEAP", "SEARCH RATE", // stat labels
		"86 shards (P+R)", "1,840/s", // stat subs/values
		"tone-warn", "tone-text", // stat tones
		"3× CLUSTER_MANAGER · 2× DATA+INGEST · 1× COORDINATING", // role summary
		"NODES BY ROLE", `/search/nodes`,                        // link to Nodes
		"+ ADD DOMAIN", `/onboarding?kind=opensearch`,           // wizard hook (ly-ae6.12 contract)
		"LIVE", // live badge
	} {
		if !strings.Contains(html, want) {
			t.Errorf("domains body missing %q", want)
		}
	}
}

func TestSearchDomainsBody_empty(t *testing.T) {
	html := renderSearchDomains(t, SearchDomainsView{})
	if !strings.Contains(html, "No search domains monitored yet") {
		t.Errorf("expected empty state; got: %s", html)
	}
}

func sampleNodes() []SearchNodeRow {
	return []SearchNodeRow{
		{Name: "os-manager-1", Roles: []string{"CLUSTER_MANAGER"}, Version: "2.19", Heap: "38%", CPU: "8%", Disk: "31%", Shards: "0", DedicatedManager: true},
		{Name: "os-data-1", Roles: []string{"DATA", "INGEST"}, Version: "2.19", Heap: "61%", CPU: "42%", Disk: "54%", Shards: "42"},
		{Name: "os-coord-1", Roles: []string{"COORDINATING"}, Version: "2.19", Heap: "22%", CPU: "11%", Disk: "12%", Shards: "0"},
	}
}

func TestSearchNodesBody_parity(t *testing.T) {
	html := renderSearchNodes(t, SearchNodesView{Nodes: sampleNodes(), Sort: "heap"})
	for _, want := range []string{
		`/static/css/search.css`,
		"os-manager-1", "os-data-1", "os-coord-1",
		"CLUSTER_MANAGER", "DATA", "INGEST", "COORDINATING", // role chips
		"38%", "61%", "42", // heap + shards values
		"sn-shards--zero",                                // dedicated-manager 0-shard dim
		"SORT: HEAP",                                     // sort control label
		`/partial/search/nodes?sort=name`,                // toggle target
		"DEDICATED CLUSTER_MANAGER NODES HOLD NO SHARDS", // explanatory footer
		"search-nodes-body",                              // swap target id
	} {
		if !strings.Contains(html, want) {
			t.Errorf("nodes body missing %q", want)
		}
	}
}

func TestSearchNodesBody_sortLabelReflectsView(t *testing.T) {
	html := renderSearchNodes(t, SearchNodesView{Nodes: sampleNodes(), Sort: "name"})
	if !strings.Contains(html, "SORT: NAME") {
		t.Error("nodes body should show SORT: NAME when Sort==name")
	}
	if !strings.Contains(html, `/partial/search/nodes?sort=heap`) {
		t.Error("nodes body toggle should target sort=heap when Sort==name")
	}
}

func TestSearchNodesBody_empty(t *testing.T) {
	html := renderSearchNodes(t, SearchNodesView{Sort: "heap"})
	if !strings.Contains(html, "No search nodes monitored yet") {
		t.Errorf("expected empty state; got: %s", html)
	}
}

// TestSearchScreens_NoExternalHosts enforces the privacy-backbone rule on the
// SEARCH screens themselves. web/layout_test.go:TestLayout_NoExternalHosts only
// renders Layout, so it does NOT cover these fragments (or the inlined magnifier
// glyph); this test does. Every asset the screens reference must be relative
// (/static/…, /search/…) — no CDN host, no absolute URL.
func TestSearchScreens_NoExternalHosts(t *testing.T) {
	htmls := []string{
		renderSearchDomains(t, SearchDomainsView{Domains: []SearchDomainCard{sampleDomain()}}),
		renderSearchNodes(t, SearchNodesView{Nodes: sampleNodes(), Sort: "heap"}),
	}
	for _, html := range htmls {
		for _, bad := range []string{"unpkg.com", "googleapis.com", "gstatic.com", "cdn.jsdelivr.net", "http://", "https://"} {
			if strings.Contains(html, bad) {
				t.Errorf("search screen references external ref %q — assets must be self-hosted (privacy backbone)", bad)
			}
		}
	}
}
