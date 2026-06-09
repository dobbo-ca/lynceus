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

// insightCanary is a literal that lives only inside the seeded plan's
// normalized-condition field. The slow-scan Detail is built from identifiers +
// counts only (slowscan.go:61-65) and InsightRow has no condition field, so
// this string must never reach the rendered HTML.
const insightCanary = "PHI-CANARY-INSIGHT-7f3a"

// seedPlans writes one stored plan whose Seq Scan child trips DefaultSlowScan
// (MinRowsScanned=1000, MaxSelectivity=0.10, slowscan.go:18). Mirrors seedStats
// (server_test.go:80) but for query_plans via store.WriteQueryPlans.
func seedPlans(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	s := store.NewStats(pool)
	capturedAt := time.Now().UTC().Add(-time.Hour)
	plan := &lynceusv1.QueryPlan{
		Fingerprint:    "fp-slowscan",
		CapturedAtUnix: capturedAt.Unix(),
		FormatVersion:  1,
		Root: &lynceusv1.PlanNode{
			NodeType: "Aggregate",
			Plans: []*lynceusv1.PlanNode{{
				NodeType:            "Seq Scan",
				RelationName:        "orders_audit",
				ActualLoops:         1,
				ActualRows:          5,
				RowsRemovedByFilter: 9995, // scanned 10000, returned 5 => sel 0.0005
				NormalizedCondition: "(email = '" + insightCanary + "')",
			}},
		},
	}
	rows := []store.QueryPlanRow{{ServerID: "srv-1", Plan: plan, CapturedAt: capturedAt}}
	if err := s.WriteQueryPlans(ctx, rows); err != nil {
		t.Fatalf("seed plans: %v", err)
	}
}

func TestInsightsPage_rendersDetectedInsights(t *testing.T) {
	pool, srv := setup(t, api.Config{DevAuth: true})
	seedPlans(t, pool)

	resp, err := http.Get(srv.URL + "/insights")
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
		"<!doctype html>",            // full page (templ emits lowercase)
		`id="insights-table"`,        // HTMX swap target
		`hx-get="/partial/insights"`, // poll target
		`href="/insights"`,           // nav link
		"orders_audit",               // seeded relation
		"slow_scan",                  // KindSlowScan (insight.go:16)
		"high",                       // SeverityHigh (slowscan.go:73)
	} {
		if !strings.Contains(html, want) {
			t.Errorf("insights page missing %q", want)
		}
	}

	// THE PRIVACY GUARANTEE on the rendered surface: the canary that lived only
	// in the plan's normalized-condition must NOT appear in the rendered HTML.
	if strings.Contains(html, insightCanary) {
		t.Errorf("LITERAL LEAK in rendered HTML: contains %q", insightCanary)
	}
}

func TestInsightsPartial_returnsFragmentOnly(t *testing.T) {
	pool, srv := setup(t, api.Config{DevAuth: true})
	seedPlans(t, pool)

	resp, err := http.Get(srv.URL + "/partial/insights")
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
	if !strings.Contains(html, `id="insights-table"`) {
		t.Error("partial missing the swap-target id (HTMX outerHTML reswap would break)")
	}
	if !strings.Contains(html, "orders_audit") {
		t.Error("partial missing seeded insight row")
	}
}

func TestInsights_withoutDevAuth_returns401(t *testing.T) {
	_, srv := setup(t, api.Config{DevAuth: false})
	resp, err := http.Get(srv.URL + "/insights")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}
