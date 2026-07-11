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

// advisorCanary is a literal that lives only inside the seeded plan's
// normalized-condition field. The advisor emits only extracted column
// identifiers + counts (index.go), and IndexAdvisorRow has no condition field,
// so this string must never reach the rendered HTML.
const advisorCanary = "PHI-CANARY-ADVISOR-9d2c"

// seedAdvisorData writes one stored plan whose Seq Scan filters on a column
// (yielding a "status" index candidate) plus a matching table_stats row so the
// recommender can size/rank it. Mirrors seedPlans (insights_test.go:27) +
// seedStats (server_test.go:80).
func seedAdvisorData(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	s := store.NewStats(pool)
	capturedAt := time.Now().UTC().Add(-time.Hour)
	plan := &lynceusv1.QueryPlan{
		Fingerprint:    "fp-advisor",
		CapturedAtUnix: capturedAt.Unix(),
		FormatVersion:  1,
		Root: &lynceusv1.PlanNode{
			NodeType:            "Seq Scan",
			RelationName:        "orders_audit",
			ActualLoops:         1,
			ActualRows:          1000,
			NormalizedCondition: "(status = '" + advisorCanary + "')",
		},
	}
	planRows := []store.QueryPlanRow{{ServerID: "srv-1", Plan: plan, CapturedAt: capturedAt}}
	if err := s.WriteQueryPlans(ctx, planRows); err != nil {
		t.Fatalf("seed plans: %v", err)
	}
	tableRows := []store.TableStatRow{{
		ServerID:    "srv-1",
		CollectedAt: capturedAt,
		SchemaName:  "public",
		ObjectName:  "orders_audit",
		FQN:         "public.orders_audit",
		TotalBytes:  500 << 20,
		SeqScan:     9000,
	}}
	if err := s.WriteTableStats(ctx, tableRows); err != nil {
		t.Fatalf("seed table stats: %v", err)
	}
}

func TestIndexAdvisorPage_rendersRecommendations(t *testing.T) {
	pool, srv := setup(t, api.Config{DevAuth: true})
	seedAdvisorData(t, pool)

	resp, err := http.Get(srv.URL + "/index-advisor")
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
		"<!doctype html>",                    // full page (templ emits lowercase)
		`id="idx-list"`,                      // HTMX swap target
		`hx-get="/partial/index-advisor"`,    // poll target
		`data-screen-label="Index Advisor"`,  // retrofitted screen marker
		"CREATE INDEX ON orders_audit",       // DDL includes the seeded relation
		"status",                             // extracted index column
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index advisor page missing %q", want)
		}
	}

	// THE PRIVACY GUARANTEE on the rendered surface: the canary that lived only
	// in the plan's normalized-condition must NOT appear in the rendered HTML.
	if strings.Contains(html, advisorCanary) {
		t.Errorf("LITERAL LEAK in rendered HTML: contains %q", advisorCanary)
	}
}

func TestIndexAdvisorPartial_returnsFragmentOnly(t *testing.T) {
	pool, srv := setup(t, api.Config{DevAuth: true})
	seedAdvisorData(t, pool)

	resp, err := http.Get(srv.URL + "/partial/index-advisor")
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
	if !strings.Contains(html, `id="idx-list"`) {
		t.Error("partial missing the swap-target id (HTMX outerHTML reswap would break)")
	}
	if !strings.Contains(html, "CREATE INDEX ON orders_audit") {
		t.Error("partial missing seeded recommendation card")
	}
}

func TestIndexAdvisor_withoutDevAuth_returns401(t *testing.T) {
	_, srv := setup(t, api.Config{DevAuth: false})
	resp, err := http.Get(srv.URL + "/index-advisor")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}
