package api_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/api"
)

func TestAuditPage_rendersRowsAndNav(t *testing.T) {
	pool, srv := setupAudit(t, api.Config{DevAuth: true})
	seedAudit(t, pool)

	resp, err := http.Get(srv.URL + "/audit")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		t.Errorf("content-type = %q, want text/html...", resp.Header.Get("Content-Type"))
	}

	body, _ := io.ReadAll(resp.Body)
	html := string(body)
	for _, want := range []string{
		"<!doctype html>",  // full page (templ emits lowercase)
		`id="audit-table"`, // HTMX swap target
		`hx-get="/partial/audit"`,
		`href="/audit"`, // nav link
		"alice",         // seeded actor
		"config.toggle", // seeded action
		"srv-2",         // seeded server
	} {
		if !strings.Contains(html, want) {
			t.Errorf("audit page missing %q", want)
		}
	}
}

func TestAuditPartial_returnsFragmentOnly(t *testing.T) {
	pool, srv := setupAudit(t, api.Config{DevAuth: true})
	seedAudit(t, pool)

	resp, err := http.Get(srv.URL + "/partial/audit")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	html := string(body)
	if strings.Contains(html, "<!doctype html>") {
		t.Error("partial returned a full document; expected a fragment only")
	}
	if !strings.Contains(html, `id="audit-table"`) {
		t.Error("partial missing the swap-target id")
	}
}

func TestAuditPartial_filtersByActor(t *testing.T) {
	pool, srv := setupAudit(t, api.Config{DevAuth: true})
	seedAudit(t, pool)

	resp, err := http.Get(srv.URL + "/partial/audit?actor=bob")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	html := string(body)

	if !strings.Contains(html, "config.toggle") {
		t.Error("actor=bob result missing bob's action config.toggle")
	}
	if strings.Contains(html, "viewed.t2") {
		t.Error("actor=bob result leaked alice's action viewed.t2")
	}
}

func TestAuditPage_withoutDevAuth_returns401(t *testing.T) {
	_, srv := setupAudit(t, api.Config{DevAuth: false})
	resp, err := http.Get(srv.URL + "/audit")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}
