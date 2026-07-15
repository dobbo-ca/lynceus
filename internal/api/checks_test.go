package api_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/dobbo-ca/lynceus/internal/api"
	"github.com/dobbo-ca/lynceus/internal/store"
)

// seedChecksData writes one table_stats row (so the handler's
// RecentServerIDs server-discovery finds srv-a) plus one firing
// checks_results row. Mirrors seedVacuumData (vacuum_advisor_test.go).
func seedChecksData(t *testing.T, s store.Stats) {
	t.Helper()
	ctx := context.Background()
	at := time.Now().UTC().Add(-time.Hour)
	if err := s.WriteTableStats(ctx, []store.TableStatRow{{
		ServerID: "srv-a", CollectedAt: at, FQN: "public.t", SchemaName: "public", ObjectName: "t",
	}}); err != nil {
		t.Fatalf("seed table stats: %v", err)
	}
	if err := s.WriteChecksResults(ctx, []store.ChecksResultRow{{
		ServerID: "srv-a", EvaluatedAt: at, CheckID: "test.always", Category: "test",
		Severity: "critical", Status: "firing", Object: "obj1", Detail: "x",
	}}); err != nil {
		t.Fatalf("seed checks results: %v", err)
	}
}

func TestChecksPageRenders(t *testing.T) {
	stats, srv := setup(t, api.Config{DevAuth: true})
	seedChecksData(t, stats)

	resp, err := http.Get(srv.URL + "/checks")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	html := string(body)
	if !strings.Contains(html, "test.always") {
		t.Fatalf("checks page missing seeded check id; body=%s", html)
	}
}

func TestChecksMute_toggleRoundTrip(t *testing.T) {
	stats, srv := setup(t, api.Config{DevAuth: true})
	seedChecksData(t, stats)

	muteURL := srv.URL + "/partial/checks/mute?server=srv-a&check=test.always&object=obj1"

	resp, err := http.Post(muteURL, "", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !strings.Contains(string(body), "MUTED") {
		t.Fatalf("first POST should mute; body=%s", string(body))
	}

	resp2, err := http.Post(muteURL, "", nil)
	if err != nil {
		t.Fatalf("POST 2: %v", err)
	}
	body2, _ := io.ReadAll(resp2.Body)
	_ = resp2.Body.Close()
	// After un-muting, the button reads MUTE (not MUTED).
	if strings.Contains(string(body2), "MUTED") {
		t.Fatalf("second POST should un-mute; body=%s", string(body2))
	}
	if !strings.Contains(string(body2), "MUTE") {
		t.Fatalf("second POST should show MUTE button; body=%s", string(body2))
	}
}
