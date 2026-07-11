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
		"Cache Clusters",                 // screen title
		"NO CACHE CLUSTERS REPORTING YET", // empty state (backend absent)
		"LYNCEUS",                        // design shell top bar
		"/static/css/cache.css",          // cache stylesheet linked
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
