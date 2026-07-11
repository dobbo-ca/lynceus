package api_test

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dobbo-ca/lynceus/internal/api"
	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
	"github.com/dobbo-ca/lynceus/internal/store"
)

// seedPlanRows writes one normalized plan for (srv, fp-plan) captured an hour
// ago, with a Seq Scan child under an Aggregate root so the tree has at
// least two levels and the grid has two rows. Mirrors seedStats
// (server_test.go:80) and the plans_test fixture (plans_test.go:40-66).
func seedPlanRows(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	s := store.NewStats(pool)
	now := time.Now().UTC().Add(-time.Hour)
	plan := &lynceusv1.QueryPlan{
		Fingerprint:       "fp-plan",
		CapturedAtUnix:    now.Unix(),
		FormatVersion:     1,
		TotalCost:         102.84,
		ActualTotalTimeMs: 0,
		Root: &lynceusv1.PlanNode{
			NodeType:  "Aggregate",
			TotalCost: 102.84,
			PlanRows:  1,
			Plans: []*lynceusv1.PlanNode{{
				NodeType:            "Seq Scan",
				RelationName:        "orders",
				TotalCost:           96.50,
				PlanRows:            2532,
				ActualRows:          2532,
				ActualLoops:         1,
				RowsRemovedByFilter: 88,
				NormalizedCondition: "(total > $1)",
			}},
		},
	}
	rows := []store.QueryPlanRow{{ServerID: "srv", Plan: plan, CapturedAt: now}}
	if err := s.WriteQueryPlans(ctx, rows); err != nil {
		t.Fatalf("seed plans: %v", err)
	}
}

func TestPlanPage_rendersTreeAndGrid(t *testing.T) {
	pool, srv := setup(t, api.Config{DevAuth: true})
	seedPlanRows(t, pool)

	resp, err := http.Get(srv.URL + "/plan?server=srv&fp=fp-plan")
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
		"<!doctype html>",           // full page (templ lowercases the doctype)
		`id="plan-view"`,            // HTMX swap target
		"PLAN TREE — CLICK A NODE",  // two-pane tree header
		"NODE DETAIL",               // two-pane detail header
		"EST ROWS",                  // node-detail grid label
		"Aggregate",                 // root node type
		"Seq Scan",                  // child node type (flat list)
		"orders",                    // relation identifier
	} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered HTML is missing %q", want)
		}
	}

	// PRIVACY: no raw literal may appear in the rendered surface.
	for _, forbidden := range []string{"leaky", "secret-value", "@example.com"} {
		if strings.Contains(html, forbidden) {
			t.Errorf("LITERAL LEAK in rendered HTML: contains %q", forbidden)
		}
	}
}

func TestPlanPartial_returnsFragmentOnly(t *testing.T) {
	pool, srv := setup(t, api.Config{DevAuth: true})
	seedPlanRows(t, pool)

	resp, err := http.Get(srv.URL + "/partial/plan?server=srv&fp=fp-plan")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	html := string(body)

	if strings.Contains(html, "<!doctype html>") || strings.Contains(html, "<!DOCTYPE html>") {
		t.Error("partial returned a full document; expected a fragment only")
	}
	if !strings.Contains(html, `id="plan-view"`) {
		t.Error("partial missing the swap-target id (HTMX outerHTML reswap would break)")
	}
	if !strings.Contains(html, "Seq Scan") {
		t.Error("partial missing seeded child node")
	}
}

func TestPlan_flatListRendersAllNodes(t *testing.T) {
	pool, srv := setup(t, api.Config{DevAuth: true})
	seedPlanRows(t, pool)

	resp, err := http.Get(srv.URL + "/partial/plan?server=srv&fp=fp-plan")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	html := string(body)

	// The two-pane flat list renders every node as a clickable row: the root
	// Aggregate and its Seq Scan child both appear (depth-first pre-order).
	if !strings.Contains(html, "Aggregate") || !strings.Contains(html, "Seq Scan") {
		t.Error("two-pane flat list did not render all plan nodes")
	}
	// Each node row deep-links back to /plan with a ?node= selector.
	if !strings.Contains(html, "&amp;node=1") {
		t.Error("flat list node rows missing per-node ?node= selector")
	}
}

func TestPlan_missingKey_rendersEmpty(t *testing.T) {
	pool, srv := setup(t, api.Config{DevAuth: true})
	seedPlanRows(t, pool) // seed a real plan so we know the empty branch is key-driven

	// A fingerprint that was never stored.
	u := srv.URL + "/plan?server=srv&fp=" + url.QueryEscape("does-not-exist")
	resp, err := http.Get(u)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	html := string(body)

	if !strings.Contains(html, "No plan stored") {
		t.Error("missing-key plan did not render the empty-state branch")
	}
	if strings.Contains(html, "Seq Scan") {
		t.Error("missing-key plan leaked the seeded plan's nodes")
	}
}

func TestPlan_withoutDevAuth_returns401(t *testing.T) {
	_, srv := setup(t, api.Config{DevAuth: false})
	resp, err := http.Get(srv.URL + "/plan?server=srv&fp=fp-plan")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}
