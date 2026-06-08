// End-to-end test for the Milestone-1 vertical slice.
//
// It wires the real collector, real ingestion server, real api server,
// and a real Postgres for both the monitored target and the stats
// store. A literal-bearing query is executed against the target; the
// test then asserts that the literal NEVER appears either in the
// persisted stats row or in the rendered dashboard HTML.
//
// If this test ever fails, the privacy guarantee is broken — do not
// merge.
package e2e_test

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
	"github.com/dobbo-ca/lynceus/internal/caps"
	"github.com/dobbo-ca/lynceus/internal/collector"
	"github.com/dobbo-ca/lynceus/internal/ingest"
	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
	"github.com/dobbo-ca/lynceus/internal/store"
)

// The canary literal MUST NOT appear anywhere in the stored row or
// the rendered dashboard. It is a deliberately distinctive string so
// any leak is obvious.
const canaryLiteral = "PHI-CANARY-LEAK-9c2e3a"

func TestVerticalSlice_normalizedQueryRoundtripsAndCanaryNeverLeaks(t *testing.T) {
	ctx := context.Background()

	// --- target Postgres (the "monitored" instance) ---
	targetC, err := tcpostgres.Run(ctx,
		"postgres:16",
		tcpostgres.WithDatabase("target"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		testcontainers.WithCmd("postgres", "-c", "shared_preload_libraries=pg_stat_statements"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Skipf("docker/testcontainers unavailable: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(targetC) })

	targetURL, err := targetC.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	target, err := pgxpool.New(ctx, targetURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(target.Close)

	for _, stmt := range []string{
		`CREATE EXTENSION IF NOT EXISTS pg_stat_statements`,
		`CREATE TABLE patients (id INT PRIMARY KEY, email TEXT)`,
		`SELECT pg_stat_statements_reset()`,
	} {
		if _, err := target.Exec(ctx, stmt); err != nil {
			t.Fatalf("target setup %q: %v", stmt, err)
		}
	}

	// Run a literal-bearing query against the target — this is what
	// must not escape.
	if _, err := target.Exec(ctx,
		`SELECT id FROM patients WHERE email = '`+canaryLiteral+`@example.com'`,
	); err != nil {
		t.Fatalf("target query with canary: %v", err)
	}

	// --- stats Postgres + migrations ---
	statsC, err := tcpostgres.Run(ctx,
		"postgres:16",
		tcpostgres.WithDatabase("stats"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Skipf("stats container unavailable: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(statsC) })

	statsURL, err := statsC.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	statsPool, err := pgxpool.New(ctx, statsURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(statsPool.Close)
	if err := store.ApplyStatsMigrations(ctx, statsPool); err != nil {
		t.Fatalf("apply stats migrations: %v", err)
	}

	// --- ingestion server (in-process) ---
	ingSrv := httptest.NewServer(ingest.NewServer(
		ingest.Config{DevToken: "dev", RateLimit: 10, RateBurst: 10},
		store.NewStats(statsPool), statsPool,
	).Handler())
	t.Cleanup(ingSrv.Close)
	wsURL := "ws" + strings.TrimPrefix(ingSrv.URL, "http")

	// --- api server (in-process) ---
	apiSrv := httptest.NewServer(api.NewServer(
		api.Config{DevAuth: true},
		store.NewStats(statsPool),
		store.NewConfig(statsPool),
	).Handler())
	t.Cleanup(apiSrv.Close)

	// --- collector pass ---
	// A fresh gate is fail-open (empty => every capability enabled), so the
	// reader behaves as before the ly-xnk.3 capability gate landed.
	reader := collector.NewReader(target, caps.NewGate(), "e2e")
	rows, err := reader.Read(ctx)
	if err != nil {
		t.Fatalf("reader: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("collector read no rows from pg_stat_statements")
	}
	for _, r := range rows {
		if strings.Contains(r.NormalizedQuery, canaryLiteral) {
			t.Fatalf("READER LEAKED CANARY: %q", r.NormalizedQuery)
		}
	}

	snap := &lynceusv1.Snapshot{
		ServerId:        "srv-e2e",
		CollectedAtUnix: time.Now().Unix(),
		QueryStats:      rows,
	}
	if err := collector.NewShipper(wsURL, "dev").Send(ctx, snap); err != nil {
		t.Fatalf("shipper: %v", err)
	}

	// --- wait for ingestion to persist ---
	var persisted int
	for i := 0; i < 100 && persisted == 0; i++ {
		_ = statsPool.QueryRow(ctx,
			`SELECT count(*) FROM query_stats WHERE server_id = 'srv-e2e'`,
		).Scan(&persisted)
		if persisted > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if persisted == 0 {
		t.Fatal("nothing persisted to query_stats — pipeline broken")
	}

	// --- assert STORAGE has no canary ---
	storedRows, err := statsPool.Query(ctx,
		`SELECT normalized_query FROM query_stats WHERE server_id = 'srv-e2e'`,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer storedRows.Close()
	sawPatientsSelect := false
	for storedRows.Next() {
		var nq string
		if err := storedRows.Scan(&nq); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(nq, canaryLiteral) {
			t.Fatalf("STORAGE LEAKED CANARY: %q", nq)
		}
		if strings.Contains(nq, "patients") && strings.Contains(nq, "email") {
			sawPatientsSelect = true
		}
	}
	if !sawPatientsSelect {
		t.Error("did not find the patients-by-email query in storage; pipeline incomplete")
	}

	// --- assert RENDERED DASHBOARD has no canary ---
	resp, err := http.Get(apiSrv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	html := string(body)
	if strings.Contains(html, canaryLiteral) {
		t.Fatal("DASHBOARD LEAKED CANARY in rendered HTML")
	}
	if !strings.Contains(html, "patients") {
		t.Error("dashboard did not display the patients query (look for normalized text)")
	}
}
