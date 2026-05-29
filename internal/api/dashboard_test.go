package api_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/api"
)

func TestDashboard_rendersTableWithSeededRowsAndNoLiterals(t *testing.T) {
	pool, srv := setup(t, api.Config{DevAuth: true})
	seedStats(t, pool)

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		t.Errorf("content-type = %q, want text/html...", resp.Header.Get("Content-Type"))
	}

	body, _ := io.ReadAll(resp.Body)
	html := string(body)

	for _, want := range []string{
		"Lynceus",                          // brand
		`id="queries-table"`,               // HTMX swap target
		`hx-get="/partial/queries"`,        // poll target
		"fp-slow",                          // a seeded fingerprint
		"SELECT * FROM big WHERE x = $1",   // a seeded normalized query
	} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered HTML is missing %q", want)
		}
	}

	// THE PRIVACY GUARANTEE on the rendered surface: no literal
	// substring may appear in the rendered HTML.
	for _, forbidden := range []string{
		"leaky", "secret-value", "@example.com",
	} {
		if strings.Contains(html, forbidden) {
			t.Errorf("LITERAL LEAK in rendered HTML: contains %q", forbidden)
		}
	}
}

func TestQueriesPartial_returnsTableFragmentOnly(t *testing.T) {
	pool, srv := setup(t, api.Config{DevAuth: true})
	seedStats(t, pool)

	resp, err := http.Get(srv.URL + "/partial/queries")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	html := string(body)

	if strings.Contains(html, "<!DOCTYPE html>") {
		t.Error("partial returned a full document; expected a fragment only")
	}
	if !strings.Contains(html, `id="queries-table"`) {
		t.Error("partial missing the swap-target id (HTMX outerHTML reswap would break)")
	}
	if !strings.Contains(html, "fp-slow") {
		t.Error("partial missing seeded row")
	}
}
