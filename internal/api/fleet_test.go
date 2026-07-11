package api_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dobbo-ca/lynceus/internal/api"
	"github.com/dobbo-ca/lynceus/internal/store"
)

// setupFleet wires a server over config+stats DBs, seeds one cluster/instance/
// 2 streams, one firing critical check, and one high insight. Returns the server
// plus the seeded clusterID, a serverID, and an insight fingerprint.
func setupFleet(t *testing.T) (srv *httptest.Server, clusterID, serverID, fp string) {
	t.Helper()
	ctx := context.Background()
	configPool := newDBPool(t)
	statsPool := newDBPool(t)
	if err := store.ApplyConfigMigrations(ctx, configPool); err != nil {
		t.Fatalf("config migrate: %v", err)
	}
	if err := store.ApplyStatsMigrations(ctx, statsPool); err != nil {
		t.Fatalf("stats migrate: %v", err)
	}
	cfg := store.NewConfig(configPool)
	stats := store.NewStats(statsPool)

	for _, id := range []string{"fl-srv-a", "fl-srv-b"} {
		if _, err := configPool.Exec(ctx, `INSERT INTO servers (id, name) VALUES ($1,$1)`, id); err != nil {
			t.Fatalf("seed server: %v", err)
		}
	}
	cl, err := cfg.CreateCluster(ctx, "orders-prod")
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}
	inst, err := cfg.CreateInstance(ctx, cl.ID, "srv-orders-primary")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	for _, id := range []string{"fl-srv-a", "fl-srv-b"} {
		if err := cfg.AssignServerToInstance(ctx, id, inst.ID); err != nil {
			t.Fatalf("assign: %v", err)
		}
	}
	now := time.Now().UTC()
	fingerprint := "f41b7d09"
	if err := stats.WriteChecksResults(ctx, []store.ChecksResultRow{
		{ServerID: "fl-srv-a", EvaluatedAt: now.Add(-2 * time.Hour), CheckID: "settings.fsync",
			Category: "settings", Severity: "critical", Status: "firing", Object: "fsync",
			Detail: "fsync = off — a crash can lose committed transactions"},
	}); err != nil {
		t.Fatalf("seed checks: %v", err)
	}
	if err := stats.WriteInsights(ctx, []store.InsightRow{
		{ServerID: "fl-srv-a", CapturedAt: now.Add(-4 * time.Hour), Kind: "slow_scan", Severity: "high",
			Fingerprint: fingerprint, Relation: "orders_audit", NodePath: "Seq Scan(orders_audit)",
			RowsReturned: 1, RowsScanned: 1200000, Selectivity: 0.0000008, Detail: "seq scan on orders_audit"},
	}); err != nil {
		t.Fatalf("seed insights: %v", err)
	}
	httpSrv := httptest.NewServer(api.NewServer(api.Config{DevAuth: true}, stats, cfg).Handler())
	t.Cleanup(httpSrv.Close)
	return httpSrv, cl.ID, "fl-srv-a", fingerprint
}

func TestFleet_rootServesDashboardWithTriageAndDeepLinks(t *testing.T) {
	srv, clusterID, serverID, fp := setupFleet(t)
	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	html := string(body)

	for _, want := range []string{
		"Fleet", "LIVE",
		"DATABASES", "OPEN CRITICAL",
		"NEEDS ATTENTION", "settings.fsync", "insight: slow_scan",
		"orders-prod", "[DEGRADED]", "POSTGRESQL",
		// scope-aware deep links (contract with ly-ae6.2)
		"/checks?scope=" + serverID + "&amp;check=settings.fsync",
		"/databases/" + clusterID + "/insights?scope=" + serverID + "&amp;fp=" + fp,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("fleet dashboard missing %q", want)
		}
	}
	// privacy: no literal-looking leak (detail strings are T1 normalized already)
	for _, forbidden := range []string{"@example.com", "secret-value"} {
		if strings.Contains(html, forbidden) {
			t.Errorf("LITERAL LEAK: %q", forbidden)
		}
	}
}

func TestFleetPartial_returnsBodyFragmentOnly(t *testing.T) {
	srv, _, _, _ := setupFleet(t)
	resp, err := http.Get(srv.URL + "/partial/fleet")
	if err != nil {
		t.Fatalf("GET /partial/fleet: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	html := string(body)
	if strings.Contains(html, "<!DOCTYPE html>") {
		t.Error("partial returned a full document; expected a fragment")
	}
	if !strings.Contains(html, `id="fleet-body"`) {
		t.Error("partial missing #fleet-body swap target")
	}
	// the poll trigger is rendered INSIDE the swapped fragment so it survives swaps
	if !strings.Contains(html, `hx-trigger="every 30s"`) {
		t.Error("partial must carry the self-refresh poll trigger inside #fleet-body")
	}
}

func TestFleet_rangeParamDrivesLabelAndPollPreservesIt(t *testing.T) {
	srv, _, _, _ := setupFleet(t)
	resp, err := http.Get(srv.URL + "/?range=1h&sort=name")
	if err != nil {
		t.Fatalf("GET /?range=1h: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	html := string(body)
	if !strings.Contains(html, "RANGE 1H") {
		t.Error("header must reflect the ?range= param label (1H)")
	}
	// the auto-poll trigger + SORT toggle must carry the chosen range+sort so a
	// 30s refresh doesn't revert them (contract with ly-ae6.2's range control).
	if !strings.Contains(html, `hx-get="/partial/fleet?sort=name&amp;range=1H"`) {
		t.Error("poll/toggle URL must preserve sort+range")
	}
}

func TestFleet_engineGateDefaultsOffNoSearchOrCacheCells(t *testing.T) {
	srv, _, _, _ := setupFleet(t)
	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	html := string(body)
	if !strings.Contains(html, "DATABASES") || !strings.Contains(html, "2 db") {
		t.Error("Row1 DATABASES cell + engine-neutral 'N db' severity sub expected")
	}
	// search/cache verticals are gated off (ly-ae6.10 / ly-ae6.11): no SEARCH/CACHE
	// stat cells and no '· 0 search · 0 cache' noise in the severity subs.
	for _, forbidden := range []string{">SEARCH<", ">CACHE<", "0 search", "0 cache"} {
		if strings.Contains(html, forbidden) {
			t.Errorf("engine gate off: unexpected %q in fleet HTML", forbidden)
		}
	}
}
