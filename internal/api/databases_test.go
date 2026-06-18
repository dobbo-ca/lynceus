package api_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/dobbo-ca/lynceus/internal/api"
	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/dobbo-ca/lynceus/internal/testpg"
)

// newDBPool starts a fresh postgres:16 container and returns a connected pool.
// Skips the test if docker/testcontainers are unavailable.
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

// setupDatabases wires a server with two separate DBs (config + stats),
// seeds one cluster/instance/server + query stats so a card appears,
// and returns the httptest.Server.
func setupDatabases(t *testing.T) *httptest.Server {
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

	// Seed: one server in the servers table.
	if _, err := configPool.Exec(ctx, `INSERT INTO servers (id, name) VALUES ('srv-test', 'srv-test')`); err != nil {
		t.Fatalf("seed server: %v", err)
	}

	// Seed: cluster → instance → assign server.
	cl, err := cfg.CreateCluster(ctx, "prod-cluster")
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}
	inst, err := cfg.CreateInstance(ctx, cl.ID, "primary")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	if err := cfg.AssignServerToInstance(ctx, "srv-test", inst.ID); err != nil {
		t.Fatalf("AssignServerToInstance: %v", err)
	}

	// Seed: two query stats rows so QPSBuckets has data for the sparkline.
	now := time.Now().UTC()
	rows := []store.QueryStat{
		{ServerID: "srv-test", CollectedAt: now.Add(-2 * time.Hour), Fingerprint: "fp-1",
			NormalizedQuery: "SELECT $1", Calls: 720, TotalTimeMs: 144},
		{ServerID: "srv-test", CollectedAt: now.Add(-time.Hour), Fingerprint: "fp-1",
			NormalizedQuery: "SELECT $1", Calls: 3600, TotalTimeMs: 720},
	}
	if err := stats.WriteQueryStats(ctx, rows); err != nil {
		t.Fatalf("seed query stats: %v", err)
	}

	srv := httptest.NewServer(
		api.NewServer(api.Config{DevAuth: true}, stats, cfg).Handler(),
	)
	t.Cleanup(srv.Close)
	return srv
}

func body(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

// TestDatabasesPage asserts the full-page route returns 200 with DOCTYPE + cluster name + swap target.
func TestDatabasesPage(t *testing.T) {
	srv := setupDatabases(t)
	resp, err := http.Get(srv.URL + "/databases")
	if err != nil {
		t.Fatalf("GET /databases: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	b := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", resp.StatusCode, b)
	}
	if !strings.Contains(strings.ToLower(b), "<!doctype html>") {
		t.Error("body missing <!doctype html>")
	}
	if !strings.Contains(b, "prod-cluster") {
		t.Error("body missing cluster name 'prod-cluster'")
	}
	if !strings.Contains(b, "databases-body") {
		t.Error("body missing 'databases-body' id")
	}
}

// TestDatabasesPartial asserts the partial route returns the fragment (no DOCTYPE) with the cluster name.
func TestDatabasesPartial(t *testing.T) {
	srv := setupDatabases(t)
	resp, err := http.Get(srv.URL + "/partial/databases")
	if err != nil {
		t.Fatalf("GET /partial/databases: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	b := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", resp.StatusCode, b)
	}
	if strings.Contains(strings.ToLower(b), "<!doctype") {
		t.Error("partial body must NOT contain <!doctype")
	}
	if !strings.Contains(b, "prod-cluster") {
		t.Errorf("partial body missing cluster name 'prod-cluster'; got: %s", b)
	}
}

// TestDatabasesPartial_search asserts that a non-matching query filters out all cards (empty state).
func TestDatabasesPartial_search(t *testing.T) {
	srv := setupDatabases(t)
	resp, err := http.Get(srv.URL + "/partial/databases?q=zzz-no-match")
	if err != nil {
		t.Fatalf("GET /partial/databases?q=zzz-no-match: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	b := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(b, "No databases monitored yet") {
		t.Errorf("expected empty-state text; got: %s", b)
	}
}

// TestDatabasesPartial_listView asserts that view=list renders a <table>.
func TestDatabasesPartial_listView(t *testing.T) {
	srv := setupDatabases(t)
	resp, err := http.Get(srv.URL + "/partial/databases?view=list")
	if err != nil {
		t.Fatalf("GET /partial/databases?view=list: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	b := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(b, "<table>") {
		t.Errorf("expected <table> in list view; got: %s", b)
	}
}
