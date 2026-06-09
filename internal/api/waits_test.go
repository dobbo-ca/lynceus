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

// seedWaits writes a few activity buckets for server "s1" inside the page's
// 7-day window: an IO wait, a Lock wait, and an on-CPU sample (empty labels).
// Mirrors seedStats (server_test.go:80) but for activity_buckets.
func seedWaits(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	s := store.NewStats(pool)
	now := time.Now().UTC().Add(-time.Hour)
	rows := []store.ActivityBucket{
		{ServerID: "s1", Database: "db", State: "active", WaitEventType: "IO", WaitEvent: "DataFileRead",
			BucketStart: now, BucketSeconds: 10, SampleCount: 1, CountSum: 50, CountMax: 5},
		{ServerID: "s1", Database: "db", State: "active", WaitEventType: "Lock", WaitEvent: "tuple",
			BucketStart: now, BucketSeconds: 10, SampleCount: 1, CountSum: 5, CountMax: 2},
		{ServerID: "s1", Database: "db", State: "active", WaitEventType: "", WaitEvent: "",
			BucketStart: now, BucketSeconds: 10, SampleCount: 1, CountSum: 40, CountMax: 8}, // on-CPU
	}
	if err := s.WriteActivityBuckets(ctx, rows); err != nil {
		t.Fatalf("seed activity buckets: %v", err)
	}
}

func TestWaitsPage_rendersHistogram(t *testing.T) {
	pool, srv := setup(t, api.Config{DevAuth: true})
	seedWaits(t, pool)

	resp, err := http.Get(srv.URL + "/waits?server=s1")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		t.Errorf("content-type = %q, want text/html...", resp.Header.Get("Content-Type"))
	}

	body, _ := io.ReadAll(resp.Body)
	html := string(body)
	for _, want := range []string{
		"<!doctype html>",                   // full page (templ emits lowercase)
		`id="waits-table"`,                  // HTMX swap target
		`hx-get="/partial/waits?server=s1"`, // poll target
		`href="/waits"`,                     // nav link
		"IO / DataFileRead",                 // seeded wait class
		"CPU",                               // on-CPU samples preserved, not dropped
	} {
		if !strings.Contains(html, want) {
			t.Errorf("waits page missing %q", want)
		}
	}
}

func TestWaitsPartial_returnsFragmentOnly(t *testing.T) {
	pool, srv := setup(t, api.Config{DevAuth: true})
	seedWaits(t, pool)

	resp, err := http.Get(srv.URL + "/partial/waits?server=s1")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	html := string(body)
	if strings.Contains(html, "<!doctype html>") {
		t.Error("partial returned a full document; expected a fragment only")
	}
	if !strings.Contains(html, `id="waits-table"`) {
		t.Error("partial missing the swap-target id (HTMX outerHTML reswap would break)")
	}
	if !strings.Contains(html, "<table>") {
		t.Error("partial missing seeded wait-event table")
	}
}

func TestWaitsPage_noServer_rendersEmptyState(t *testing.T) {
	_, srv := setup(t, api.Config{DevAuth: true})

	resp, err := http.Get(srv.URL + "/waits")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	html := string(body)
	if !strings.Contains(html, "No sampled wait events") {
		t.Error("blank-server page missing empty-state text")
	}
}

func TestWaits_withoutDevAuth_returns401(t *testing.T) {
	_, srv := setup(t, api.Config{DevAuth: false})
	resp, err := http.Get(srv.URL + "/waits?server=s1")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}
