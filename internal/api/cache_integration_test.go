package api_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/api"
)

// The cache screens render inside the design Shell, so the enabled path exercises
// the full server (real config store for the scope picker) via setupAudit.
func TestCacheClusters_enabled_renders(t *testing.T) {
	_, srv := setupAudit(t, api.Config{DevAuth: true, EnableValkey: true})
	resp, err := http.Get(srv.URL + "/cache/clusters")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /cache/clusters: got %d want 200", resp.StatusCode)
	}
	html := body(t, resp)
	for _, want := range []string{
		"Cache Clusters",                  // screen title
		"NO CACHE CLUSTERS REPORTING YET", // empty state (backend absent)
		"LYNCEUS",                         // design shell top bar
		"/static/css/cache.css",           // cache stylesheet linked
	} {
		if !strings.Contains(html, want) {
			t.Errorf("/cache/clusters missing %q", want)
		}
	}
}

func TestCacheClusters_disabled_404(t *testing.T) {
	_, srv := setupAudit(t, api.Config{DevAuth: true})
	resp, err := http.Get(srv.URL + "/cache/clusters")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("disabled cache: got %d want 404", resp.StatusCode)
	}
}

func TestCacheReplicasetsAndNodes_enabled_renders(t *testing.T) {
	_, srv := setupAudit(t, api.Config{DevAuth: true, EnableRedis: true})
	for _, tc := range []struct {
		path  string
		title string
		empty string
	}{
		{"/cache/replicasets", "Replicasets", "NO REPLICASETS REPORTING YET"},
		{"/cache/nodes", "Cache Nodes", "NO CACHE NODES REPORTING YET"},
	} {
		resp, err := http.Get(srv.URL + tc.path)
		if err != nil {
			t.Fatalf("GET %s: %v", tc.path, err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: got %d want 200", tc.path, resp.StatusCode)
		}
		html := body(t, resp)
		for _, want := range []string{tc.title, tc.empty, "LYNCEUS"} {
			if !strings.Contains(html, want) {
				t.Errorf("%s missing %q", tc.path, want)
			}
		}
	}
}

// The sort partial swaps just the #cache-body fragment (no shell chrome).
func TestCacheNodesPartial_sortSwapsBodyOnly(t *testing.T) {
	_, srv := setupAudit(t, api.Config{DevAuth: true, EnableValkey: true})
	resp, err := http.Get(srv.URL + "/partial/cache/nodes?sort=name")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got %d want 200", resp.StatusCode)
	}
	html := body(t, resp)
	if !strings.Contains(html, `id="cache-body"`) {
		t.Error("partial must return the #cache-body fragment")
	}
	if strings.Contains(html, "LYNCEUS") {
		t.Error("partial must NOT include the shell top bar")
	}
	if !strings.Contains(html, "SORT: NAME") {
		t.Error("partial must reflect the requested sort key")
	}
}
