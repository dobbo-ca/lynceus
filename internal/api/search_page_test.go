package api_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/api"
)

// The enabled Search pages render inside the design Shell (ly-ae6.2), whose
// buildShellView reads the config store to enumerate scope options. These tests
// therefore go through setupAudit (a real, empty config DB via testcontainers)
// rather than the nil-store gate handler in search_gate_test.go — they skip
// gracefully when Docker is unavailable. No store is mocked; the config DB is
// simply empty (no topology seeded), so the screens render their empty state.

func TestSearchDomains_enabledEmptyState(t *testing.T) {
	_, srv := setupAudit(t, api.Config{DevAuth: true, EnableOpensearch: true})
	resp, err := http.Get(srv.URL + "/search/domains")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	html := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("enabled /search/domains = %d, want 200; body: %s", resp.StatusCode, html)
	}
	got := strings.ToLower(html)
	for _, want := range []string{"<!doctype html>", "domains", "opensearch", "no search domains monitored yet", "/static/css/search.css"} {
		if !strings.Contains(got, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

func TestSearchNodes_sortParamEchoed(t *testing.T) {
	_, srv := setupAudit(t, api.Config{DevAuth: true, EnableElasticsearch: true})

	// full page, default sort → SORT: HEAP
	resp, err := http.Get(srv.URL + "/search/nodes")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	html := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/search/nodes = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(html, "SORT: HEAP") {
		t.Errorf("default nodes page should show SORT: HEAP; got: %s", html)
	}

	// partial with sort=name → SORT: NAME, no doctype
	resp2, err := http.Get(srv.URL + "/partial/search/nodes?sort=name")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	b2 := body(t, resp2)
	if strings.Contains(strings.ToLower(b2), "<!doctype") {
		t.Error("partial must not contain <!doctype")
	}
	if !strings.Contains(b2, "SORT: NAME") {
		t.Errorf("partial should echo SORT: NAME; got: %s", b2)
	}
}
