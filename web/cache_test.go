package web

import (
	"context"
	"strings"
	"testing"

	"github.com/a-h/templ"
)

func renderBody(t *testing.T, c templ.Component) string {
	t.Helper()
	var sb strings.Builder
	if err := c.Render(context.Background(), &sb); err != nil {
		t.Fatalf("render: %v", err)
	}
	return sb.String()
}

func TestShell_LinksCacheStylesheet(t *testing.T) {
	html := renderShell(t, fleetShellView())
	if !strings.Contains(html, `href="/static/css/cache.css"`) {
		t.Error("shell must link /static/css/cache.css")
	}
}

func TestCacheEngineSprite_HasKeyGlyph(t *testing.T) {
	html := renderBody(t, cacheEngineSprite())
	if !strings.Contains(html, `id="eng-vk"`) {
		t.Error("sprite must define #eng-vk symbol")
	}
}

func TestCacheClustersBody_EmptyState(t *testing.T) {
	html := renderBody(t, CacheClustersBody(CacheClustersView{Enabled: true}))
	if !strings.Contains(html, "Cache Clusters") {
		t.Error("missing screen title")
	}
	if !strings.Contains(html, "NO CACHE CLUSTERS REPORTING YET") {
		t.Error("missing empty state")
	}
	if !strings.Contains(html, "+ ADD CLUSTER") {
		t.Error("missing + ADD CLUSTER button")
	}
}

func TestCacheClustersBody_Card(t *testing.T) {
	v := CacheClustersView{Enabled: true, Clusters: []CacheCluster{{
		Name: "cache-prod", Version: "8.1", Mode: "SENTINEL",
		Provider: "SELF-HOSTED", Engine: "VALKEY",
		Stats: []CacheStat{
			{Label: "REPLICASETS", Value: "3", Sev: "mut"},
			{Label: "MEMORY", Value: "6.2/16G", Sev: "mut"},
			{Label: "OPS/S", Value: "41,200", Sev: "mut"},
			{Label: "HIT RATE", Value: "94.1%", Sev: "ok"},
			{Label: "SENTINELS", Value: "3/3", Sub: "QUORUM OK", Sev: "ok"},
		},
		Replicasets: []CacheClusterRS{
			{Name: "rs-sessions", Topo: "1 PRIMARY + 2 REPLICAS", Mem: "2.1G", Ops: "18,400", Health: "● HEALTHY", Sev: "ok"},
		},
	}}}
	html := renderBody(t, CacheClustersBody(v))
	for _, want := range []string{
		"cache-prod", "v8.1", "SENTINEL", "SELF-HOSTED",
		`href="#eng-vk"`, "HIT RATE", "94.1%", "c-sev-ok",
		"rs-sessions", "1 PRIMARY + 2 REPLICAS",
		"WRITES GO TO EACH REPLICASET",
		`href="/cache/replicasets"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("clusters card missing %q", want)
		}
	}
}

func TestCacheReplicasetsBody(t *testing.T) {
	v := CacheReplicasetsView{Enabled: true, Sort: "health", Rows: []CacheReplicasetRow{
		{Name: "rs-sessions", Cluster: "cache-prod", Topo: "1P + 2R",
			Keys: "1.2M", Mem: "2.1G", Ops: "18,400", Evictions: "0", Health: "● HEALTHY", Sev: "ok"},
	}}
	html := renderBody(t, CacheReplicasetsBody(v))
	for _, want := range []string{
		"Replicasets", "REPLICASET", "TOPOLOGY", "EVICTIONS",
		"rs-sessions", "cache-prod", "c-sev-ok",
		"SORT: HEALTH", `hx-get="/partial/cache/replicasets?sort=name"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("replicasets body missing %q", want)
		}
	}
}

func TestCacheReplicasetsBody_EmptyState(t *testing.T) {
	html := renderBody(t, CacheReplicasetsBody(CacheReplicasetsView{Enabled: true, Sort: "health"}))
	if !strings.Contains(html, "NO REPLICASETS REPORTING YET") {
		t.Error("missing replicasets empty state")
	}
}

func TestCacheNodesBody_AccessBadges(t *testing.T) {
	v := CacheNodesView{Enabled: true, Sort: "ops", Rows: []CacheNodeRow{
		{Role: "PRIMARY", Name: "rs-sessions-0", Replicaset: "rs-sessions",
			Version: "8.1", Mem: "2.1G", Ops: "12,000", Clients: "340", Hit: "94%", Access: "READ-WRITE"},
		{Role: "REPLICA", Name: "rs-sessions-1", Replicaset: "rs-sessions",
			Version: "8.1", Mem: "2.0G", Ops: "6,400", Clients: "120", Hit: "95%", Access: "READ-ONLY"},
	}}
	html := renderBody(t, CacheNodesBody(v))
	for _, want := range []string{
		"Cache Nodes", "ROLE", "ACCESS",
		"c-role c-role-primary", "c-role c-role-replica",
		"c-access c-access-rw", "READ-WRITE",
		"c-access c-access-ro", "READ-ONLY",
		"SORT: OPS", `hx-get="/partial/cache/nodes?sort=name"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("nodes body missing %q", want)
		}
	}
}
