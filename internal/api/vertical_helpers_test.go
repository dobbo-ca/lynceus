package api_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/dobbo-ca/lynceus/internal/api"
	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/dobbo-ca/lynceus/internal/testpg"
)

// newDBPool starts a fresh postgres:16 container and returns a connected pool.
// Skips the test if docker/testcontainers are unavailable. Shared across the
// api_test package (Clusters/Nodes/Databases verticals + cluster views/overview).
func newDBPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	c, err := tcpostgres.Run(ctx, "postgres:16",
		tcpostgres.WithDatabase("lynceus_test"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		testpg.ReadyWait(),
	)
	if err != nil {
		t.Skipf("docker/testcontainers unavailable: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(c) })
	url, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
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
