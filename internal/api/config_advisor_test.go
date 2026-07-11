package api_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dobbo-ca/lynceus/internal/api"
	"github.com/dobbo-ca/lynceus/internal/store"
)

// seedConfigData writes a table_stats row (so the handler's server-discovery
// via RecentServerIDs finds srv-1) plus curated settings rows with fsync=off
// and shared_buffers at its default, so ConfigAdvice emits durability + memory
// findings.
func seedConfigData(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	s := store.NewStats(pool)
	at := time.Now().UTC().Add(-time.Hour)
	if err := s.WriteTableStats(ctx, []store.TableStatRow{{
		ServerID:    "srv-1",
		CollectedAt: at,
		SchemaName:  "public",
		ObjectName:  "t",
		FQN:         "public.t",
		LiveTuples:  1,
	}}); err != nil {
		t.Fatalf("seed table stats: %v", err)
	}
	if err := s.WriteSettings(ctx, []store.SettingRow{
		{ServerID: "srv-1", CollectedAt: at, Name: "fsync", Value: "off", Source: "configuration file"},
		{ServerID: "srv-1", CollectedAt: at, Name: "shared_buffers", Value: "16384", Unit: "8kB", Source: "default"},
	}); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
}

func TestConfigAdvisorPage_rendersRecommendations(t *testing.T) {
	pool, srv := setup(t, api.Config{DevAuth: true})
	seedConfigData(t, pool)

	resp, err := http.Get(srv.URL + "/config-advisor")
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
		"<!doctype html>",
		`id="config-table"`,
		`hx-get="/partial/config-advisor"`,
		`data-screen-label="Config Advisor"`,
		"fsync",
		"shared_buffers",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("config advisor page missing %q", want)
		}
	}
}

func TestConfigAdvisorPartial_returnsFragmentOnly(t *testing.T) {
	pool, srv := setup(t, api.Config{DevAuth: true})
	seedConfigData(t, pool)

	resp, err := http.Get(srv.URL + "/partial/config-advisor")
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
	if !strings.Contains(html, `id="config-table"`) {
		t.Error("partial missing the swap-target id (HTMX outerHTML reswap would break)")
	}
	if !strings.Contains(html, "fsync") {
		t.Error("partial missing seeded recommendation")
	}
}

func TestConfigAdvisor_withoutDevAuth_returns401(t *testing.T) {
	_, srv := setup(t, api.Config{DevAuth: false})
	resp, err := http.Get(srv.URL + "/config-advisor")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}
