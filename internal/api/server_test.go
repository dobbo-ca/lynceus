package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/dobbo-ca/lynceus/internal/api"
	"github.com/dobbo-ca/lynceus/internal/store"
)

func setup(t *testing.T, cfg api.Config) (*pgxpool.Pool, *httptest.Server) {
	t.Helper()
	ctx := context.Background()

	c, err := tcpostgres.Run(ctx,
		"postgres:16",
		tcpostgres.WithDatabase("lynceus_stats"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Skipf("docker/testcontainers unavailable: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(c) })

	url, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	if err := store.ApplyStatsMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	srv := httptest.NewServer(api.NewServer(cfg, store.NewStats(pool)).Handler())
	t.Cleanup(srv.Close)
	return pool, srv
}

func seedStats(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	s := store.NewStats(pool)
	now := time.Now().UTC().Add(-time.Hour)
	rows := []store.QueryStat{
		{ServerID: "srv", CollectedAt: now, Fingerprint: "fp-fast", NormalizedQuery: "SELECT 1", Calls: 100, TotalTimeMs: 10},
		{ServerID: "srv", CollectedAt: now, Fingerprint: "fp-slow", NormalizedQuery: "SELECT * FROM big WHERE x = $1", Calls: 3, TotalTimeMs: 999},
		{ServerID: "srv", CollectedAt: now, Fingerprint: "fp-mid", NormalizedQuery: "UPDATE t SET v = $1", Calls: 25, TotalTimeMs: 250},
	}
	if err := s.WriteQueryStats(ctx, rows); err != nil {
		t.Fatal(err)
	}
}

func TestTopQueries_devAuth_returnsRowsSortedByTotalTimeDesc(t *testing.T) {
	pool, srv := setup(t, api.Config{DevAuth: true})
	seedStats(t, pool)

	resp, err := http.Get(srv.URL + "/api/queries/top?limit=10")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q, want application/json", ct)
	}

	var got []struct {
		Fingerprint     string  `json:"fingerprint"`
		NormalizedQuery string  `json:"normalized_query"`
		Calls           int64   `json:"calls"`
		TotalTimeMs     float64 `json:"total_time_ms"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("rows = %d, want 3", len(got))
	}
	if got[0].Fingerprint != "fp-slow" {
		t.Errorf("got[0] = %q, want fp-slow (sorted by total_time_ms desc)", got[0].Fingerprint)
	}
	if got[2].Fingerprint != "fp-fast" {
		t.Errorf("got[2] = %q, want fp-fast", got[2].Fingerprint)
	}
}

func TestTopQueries_withoutDevAuth_returns401(t *testing.T) {
	_, srv := setup(t, api.Config{DevAuth: false})
	resp, err := http.Get(srv.URL + "/api/queries/top")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestSCIM_returns501_underDevAuth(t *testing.T) {
	_, srv := setup(t, api.Config{DevAuth: true})
	resp, err := http.Get(srv.URL + "/api/scim/v2/Users")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotImplemented {
		t.Errorf("SCIM status = %d, want 501", resp.StatusCode)
	}
}
