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

// seedChecksData writes one table_stats row (so the handler's
// RecentServerIDs server-discovery finds srv-a) plus one firing
// checks_results row. Mirrors seedVacuumData (vacuum_advisor_test.go).
func seedChecksData(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	s := store.NewStats(pool)
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
	pool, srv := setup(t, api.Config{DevAuth: true})
	seedChecksData(t, pool)

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
