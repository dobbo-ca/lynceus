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
	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
	"github.com/dobbo-ca/lynceus/internal/store"
)

// seedVacuumData writes one stored plan (so the handler's server-discovery via
// ListPlanKeys finds srv-1) plus a table_stats row that is bloated, stale, and
// behind on autovacuum so the recommender emits Bloat + Performance + Activity
// findings. Mirrors seedAdvisorData (index_advisor_test.go:28).
func seedVacuumData(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	s := store.NewStats(pool)
	capturedAt := time.Now().UTC().Add(-time.Hour)
	plan := &lynceusv1.QueryPlan{
		Fingerprint:    "fp-vacuum",
		CapturedAtUnix: capturedAt.Unix(),
		FormatVersion:  1,
		Root: &lynceusv1.PlanNode{
			NodeType:     "Seq Scan",
			RelationName: "orders_audit",
			ActualLoops:  1,
			ActualRows:   1000,
		},
	}
	planRows := []store.QueryPlanRow{{ServerID: "srv-1", Plan: plan, CapturedAt: capturedAt}}
	if err := s.WriteQueryPlans(ctx, planRows); err != nil {
		t.Fatalf("seed plans: %v", err)
	}
	tableRows := []store.TableStatRow{{
		ServerID:         "srv-1",
		CollectedAt:      capturedAt,
		SchemaName:       "public",
		ObjectName:       "orders_audit",
		FQN:              "public.orders_audit",
		TotalBytes:       500 << 20,
		LiveTuples:       50000,
		DeadTuples:       40000, // 44% dead -> high bloat; >=10000 dead -> activity
		NModSinceAnalyze: 40000, // >0.5*live -> high analyze
		// LastAutovacuum zero -> activity flagged (never)
	}}
	if err := s.WriteTableStats(ctx, tableRows); err != nil {
		t.Fatalf("seed table stats: %v", err)
	}
}

func TestVacuumAdvisorPage_rendersRecommendations(t *testing.T) {
	pool, srv := setup(t, api.Config{DevAuth: true})
	seedVacuumData(t, pool)

	resp, err := http.Get(srv.URL + "/vacuum-advisor")
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
		"<!doctype html>",                  // full page (templ emits lowercase)
		`id="vac-table"`,                   // HTMX swap target
		`hx-get="/partial/vacuum-advisor"`, // poll target
		`href="/vacuum-advisor"`,           // nav link
		"orders_audit",                     // seeded relation
		"bloat",                            // bloat category
	} {
		if !strings.Contains(html, want) {
			t.Errorf("vacuum advisor page missing %q", want)
		}
	}
}

func TestVacuumAdvisorPartial_returnsFragmentOnly(t *testing.T) {
	pool, srv := setup(t, api.Config{DevAuth: true})
	seedVacuumData(t, pool)

	resp, err := http.Get(srv.URL + "/partial/vacuum-advisor")
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
	if !strings.Contains(html, `id="vac-table"`) {
		t.Error("partial missing the swap-target id (HTMX outerHTML reswap would break)")
	}
	if !strings.Contains(html, "<table>") {
		t.Error("partial missing seeded recommendation table")
	}
}

func TestVacuumAdvisor_withoutDevAuth_returns401(t *testing.T) {
	_, srv := setup(t, api.Config{DevAuth: false})
	resp, err := http.Get(srv.URL + "/vacuum-advisor")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}
