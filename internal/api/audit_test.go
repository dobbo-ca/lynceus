package api_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/api"
	"github.com/dobbo-ca/lynceus/internal/store"
)

func TestAuditPage_showsChainBannerTargetAndT2Stripe(t *testing.T) {
	pool, srv := setupAudit(t, api.Config{DevAuth: true})
	seedAudit(t, pool) // login(T0), viewed.t2(T2), config.toggle(T1)

	// Append a T2 read whose Detail carries structural keys -> derived TARGET.
	cfg := store.NewConfig(pool)
	if _, err := cfg.AppendAuditReturning(context.Background(), store.AuditEntry{
		Actor: "carol", Action: "read", ServerID: "srv-9", DataTier: 2,
		Detail: map[string]any{"database_name": "orders", "capability": "pg_stat_statements"},
	}); err != nil {
		t.Fatalf("seed detail row: %v", err)
	}

	resp, err := http.Get(srv.URL + "/audit")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	html := string(body)

	for _, want := range []string{
		"HASH CHAIN VERIFIED",       // banner, whole-chain intact
		"4 EVENTS",                  // tip.ID == 4 after 3 seeds + 1 append
		"audit-row--t2",             // amber striping present
		"orders.pg_stat_statements", // derived TARGET (T1-safe)
		">T2<",                      // tier badge
	} {
		if !strings.Contains(html, want) {
			t.Errorf("audit page missing %q", want)
		}
	}
}

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
