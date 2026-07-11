package api_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dobbo-ca/lynceus/internal/store"
)

// setupClusters seeds two clusters: "aaa-clean" (healthy) and "zzz-degraded"
// (one firing critical check). Alphabetically aaa precedes zzz, so a correct
// HEALTH sort (degraded-first) must invert that order.
func setupClusters(t *testing.T) *httptest.Server {
	t.Helper()
	ctx := context.Background()
	srv, cfg, stats, configPool, _ := newVerticalFleet(t)
	now := time.Now().UTC()

	seed := func(clusterName, serverID string, degraded bool) {
		if _, err := configPool.Exec(ctx, `INSERT INTO servers (id, name) VALUES ($1, $1)`, serverID); err != nil {
			t.Fatalf("seed server %s: %v", serverID, err)
		}
		cl, err := cfg.CreateCluster(ctx, clusterName)
		if err != nil {
			t.Fatalf("CreateCluster: %v", err)
		}
		inst, err := cfg.CreateInstance(ctx, cl.ID, "primary")
		if err != nil {
			t.Fatalf("CreateInstance: %v", err)
		}
		if err := cfg.AssignServerToInstance(ctx, serverID, inst.ID); err != nil {
			t.Fatalf("AssignServerToInstance: %v", err)
		}
		if err := stats.WriteQueryStats(ctx, []store.QueryStat{
			{ServerID: serverID, CollectedAt: now.Add(-time.Hour), Fingerprint: "fp-1",
				NormalizedQuery: "SELECT $1", Calls: 3600, TotalTimeMs: 720},
		}); err != nil {
			t.Fatalf("seed query stats: %v", err)
		}
		if degraded {
			if err := stats.WriteChecksResults(ctx, []store.ChecksResultRow{{
				ServerID: serverID, EvaluatedAt: now, CheckID: "settings.fsync",
				Category: "settings", Severity: "critical", Status: "firing",
				Object: "server", DataTier: 1,
			}}); err != nil {
				t.Fatalf("seed check: %v", err)
			}
		}
	}
	seed("aaa-clean", "srv-clean", false)
	seed("zzz-degraded", "srv-degraded", true)
	return srv
}

func TestClusters_HealthSortAndRowAnatomy(t *testing.T) {
	srv := setupClusters(t)
	html := getBody(t, srv.URL+"/databases?sort=health")
	if !strings.Contains(html, `id="clusters-screen"`) || !strings.Contains(html, "SORT: HEALTH") {
		t.Fatal("clusters screen header missing")
	}
	if !strings.Contains(html, "[DEGRADED]") || !strings.Contains(html, `class="scope-btn"`) {
		t.Fatal("degraded health line or scope button missing")
	}
	// The screen renders inside the design shell (top bar + sidebar).
	if !strings.Contains(html, `class="topbar"`) {
		t.Fatal("clusters screen not wrapped in the design shell")
	}
	// HEALTH sort must place the degraded cluster before the clean one, inverting
	// the alphabetical (zzz after aaa) order. Assert on the body-only partial: the
	// full page also lists cluster names in the top-bar scope picker (name-ordered),
	// which would otherwise dominate strings.Index.
	frag := getBody(t, srv.URL+"/partial/databases?sort=health")
	if strings.Index(frag, "zzz-degraded") > strings.Index(frag, "aaa-clean") {
		t.Fatal("HEALTH sort did not put the degraded cluster first")
	}
}
