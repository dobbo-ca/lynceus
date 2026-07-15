package api_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dobbo-ca/lynceus/internal/api"
	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/dobbo-ca/lynceus/internal/testpg"
)

// newDBPool returns a pool scoped to a fresh, isolated database on the shared
// per-package Postgres container (see internal/testpg). Shared across the
// api_test package (Clusters/Nodes/Databases verticals + cluster views/overview).
func newDBPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return testpg.Start(t)
}

// newVerticalFleet migrates the two stores and starts an httptest server with
// the real API handler wired, returning everything the per-screen setups need
// to seed. Seeding after NewServer is fine — handlers read the DB per request.
func newVerticalFleet(t *testing.T) (srv *httptest.Server, cfg store.Config, stats store.Stats, configPool, statsPool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	configPool = newDBPool(t)
	statsPool = newDBPool(t)
	if err := store.ApplyConfigMigrations(ctx, configPool); err != nil {
		t.Fatalf("config migrate: %v", err)
	}
	if err := store.ApplyStatsMigrations(ctx, statsPool); err != nil {
		t.Fatalf("stats migrate: %v", err)
	}
	cfg = store.NewConfig(configPool)
	stats = store.NewStats(statsPool)
	srv = httptest.NewServer(api.NewServer(api.Config{DevAuth: true}, stats, cfg).Handler())
	t.Cleanup(srv.Close)
	return srv, cfg, stats, configPool, statsPool
}

// body reads an *http.Response body to a string (used by shell_test.go and the
// cluster-view tests).
func body(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

// getBody GETs url and returns the response body as a string.
func getBody(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}
