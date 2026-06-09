# Wait-Event Histograms (`ly-xqf.3`) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development or superpowers:executing-plans. Checkbox steps.

**Goal:** A read path over the already-collected `activity_buckets` that aggregates sampled wait events into a historical breakdown, surfaced at `/waits`.

**Architecture:** `ly-xqf.1` already samples `pg_stat_activity` → 10s/60s `ActivityBucket`s with `wait_event_type` / `wait_event` labels + `count_sum`, persisted to the partitioned `activity_buckets` table. This adds one store read method that `GROUP BY` those labels and `SUM(count_sum)` over a window, and an api page that renders the breakdown. No collector changes, no new schema, no new proto.

**Tech Stack:** Go, templ, testcontainers (the store read test seeds `activity_buckets` via the existing `WriteActivityBuckets` + harness in `internal/store/store_test.go`).

**Privacy:** `activity_buckets` is already T1 (the `ActivityBucket` contract test guarantees no `query` text). Aggregating its labels is T1.

**On-CPU semantics:** a sampled backend that is active with `wait_event_type=''`/`wait_event=''` is **on CPU**. Surface those rows labelled `CPU` rather than dropping them — that is real signal (CPU-bound vs wait-bound).

---

## File Structure
- Modify `internal/store/stats.go` — add `WaitEventHistogram` + `WaitEventCount` type beside `TopActivityBucketsByState` (stats.go:283).
- Modify `internal/store/store_test.go` — add a round-trip test.
- Create `internal/api/waits.go`, `internal/api/waits_test.go`.
- Create `web/waits.templ` (+ generated).
- Modify `internal/api/server.go` — `GET /waits`, `GET /partial/waits`.
- Modify `web/layout.templ` nav — `<a href="/waits">Waits</a>`.

---

## Task 1: Store aggregation read

**Files:** `internal/store/stats.go`, `internal/store/store_test.go`.

- [ ] **Step 1: Failing test** in `store_test.go` (reuse the harness that `TestWriteActivityBuckets_createsPartitionAndRoundtrips` uses — same `newStatsTC(t)`/`ApplyStatsMigrations` setup):

```go
func TestWaitEventHistogram_aggregatesByEvent(t *testing.T) {
	ctx := context.Background()
	st := newTestStats(t) // same constructor the activity round-trip test uses
	base := time.Date(2024, 1, 8, 0, 0, 0, 0, time.UTC) // a Monday
	rows := []store.ActivityBucket{
		{ServerID: "s1", Database: "db", State: "active", WaitEventType: "IO", WaitEvent: "DataFileRead",
			BucketStart: base, BucketSeconds: 10, SampleCount: 1, CountSum: 30, CountMax: 5},
		{ServerID: "s1", Database: "db", State: "active", WaitEventType: "IO", WaitEvent: "DataFileRead",
			BucketStart: base.Add(time.Minute), BucketSeconds: 10, SampleCount: 1, CountSum: 20, CountMax: 4},
		{ServerID: "s1", Database: "db", State: "active", WaitEventType: "Lock", WaitEvent: "tuple",
			BucketStart: base, BucketSeconds: 10, SampleCount: 1, CountSum: 5, CountMax: 2},
		{ServerID: "s1", Database: "db", State: "active", WaitEventType: "", WaitEvent: "",
			BucketStart: base, BucketSeconds: 10, SampleCount: 1, CountSum: 40, CountMax: 8}, // on-CPU
	}
	if err := st.WriteActivityBuckets(ctx, rows); err != nil {
		t.Fatal(err)
	}
	got, err := st.WaitEventHistogram(ctx, "s1", base.Add(-time.Hour), base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	// Ordered by total desc: CPU(40), IO/DataFileRead(50)... wait: IO=30+20=50 > CPU 40 > Lock 5.
	if len(got) != 3 {
		t.Fatalf("rows = %d, want 3: %+v", len(got), got)
	}
	if got[0].WaitEventType != "IO" || got[0].Total != 50 {
		t.Errorf("top = %+v, want IO/50", got[0])
	}
	// on-CPU row preserved (empty type/event), not dropped.
	var sawCPU bool
	for _, g := range got {
		if g.WaitEventType == "" && g.Total == 40 {
			sawCPU = true
		}
	}
	if !sawCPU {
		t.Errorf("on-CPU bucket dropped: %+v", got)
	}
}
```

> Use whatever the existing activity round-trip test uses to obtain a `*store.Stats` against a testcontainer (grep `internal/store/store_test.go` for the constructor — likely `applyAndStats(t)` / `newStats(t)`). Match it exactly; do not introduce a new harness.

- [ ] **Step 2: Implement** in `stats.go` after `TopActivityBucketsByState`:

```go
// WaitEventCount is one aggregated wait-event class over a time window: a
// (type, event) label pair and the summed sample count. Empty type/event means
// the backend was active on CPU (no wait). T1 — labels + a count only.
type WaitEventCount struct {
	WaitEventType string
	WaitEvent     string
	Total         int64
	Buckets       int64 // how many buckets contributed (sampling depth)
}

// WaitEventHistogram aggregates activity_buckets for serverID in [since, until)
// into per-(wait_event_type, wait_event) totals, busiest first. data_tier = 1
// only (T1). Active-on-CPU samples (empty wait labels) are preserved as their
// own row, not dropped.
func (s *Stats) WaitEventHistogram(
	ctx context.Context, serverID string, since, until time.Time,
) ([]WaitEventCount, error) {
	rows, err := s.ro.Query(ctx,
		`SELECT wait_event_type, wait_event, SUM(count_sum)::bigint AS total, COUNT(*)::bigint AS buckets
		   FROM activity_buckets
		  WHERE server_id = $1
		    AND bucket_start >= $2 AND bucket_start < $3
		    AND data_tier = 1
		  GROUP BY wait_event_type, wait_event
		  ORDER BY total DESC, wait_event_type, wait_event`,
		serverID, since, until,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []WaitEventCount
	for rows.Next() {
		var w WaitEventCount
		if err := rows.Scan(&w.WaitEventType, &w.WaitEvent, &w.Total, &w.Buckets); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}
```

> Note `s.ro` is the read-replica pool used by the other read methods (`TopPlansByQuery`, `ListPlanKeys`). `TopActivityBucketsByState` uses `s.pool`; the read-only methods use `s.ro`. Use `s.ro` here to match the read-path convention. If `s.ro` is nil in some test path, the existing methods show the fallback — match whichever `ListPlanKeys` uses.

- [ ] **Step 3: Run** `go test ./internal/store/... -run TestWaitEventHistogram -count=1` → PASS (testcontainer).
- [ ] **Step 4: Commit** `git commit -am "feat(store): WaitEventHistogram aggregation over activity_buckets (ly-xqf.3)"`

---

## Task 2: API page

**Files:** `internal/api/waits.go`, `internal/api/waits_test.go`, `web/waits.templ`, modify `server.go`, `web/layout.templ`.

The page takes a `?server=<id>` query param (like `/plan?server=...&fp=...`). With no/blank server it renders the empty state.

- [ ] **Step 1: templ** `web/waits.templ`:

```go
package web

import "fmt"

type WaitRow struct {
	Class   string // "IO / DataFileRead" or "CPU"
	Total   int64
	Buckets int64
	Pct     string // share of total, "42%"
}

templ WaitsPage(server string, rows []WaitRow) {
	@Layout("Lynceus — wait events", "sampled wait-event breakdown") {
		<p>Server <code>{ server }</code></p>
		<p hx-get={ "/partial/waits?server=" + server } hx-trigger="every 30s" hx-target="#waits-table" hx-swap="outerHTML"></p>
		@WaitsTable(rows)
	}
}

templ WaitsTable(rows []WaitRow) {
	<div id="waits-table">
		if len(rows) == 0 {
			<p class="empty">No sampled wait events for this server in the window.</p>
		} else {
			<table>
				<thead><tr><th>Wait class</th><th class="num">Samples</th><th class="num">Share</th><th class="num">Buckets</th></tr></thead>
				<tbody>
					for _, r := range rows {
						<tr>
							<td><code>{ r.Class }</code></td>
							<td class="num">{ fmt.Sprint(r.Total) }</td>
							<td class="num">{ r.Pct }</td>
							<td class="num">{ fmt.Sprint(r.Buckets) }</td>
						</tr>
					}
				</tbody>
			</table>
		}
	</div>
}
```

- [ ] **Step 2: handler** `internal/api/waits.go`:

```go
package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/dobbo-ca/lynceus/web"
)

func (s *Server) handleWaitsPage(w http.ResponseWriter, r *http.Request) {
	server := r.URL.Query().Get("server")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.WaitsPage(server, s.fetchWaits(r, server)).Render(r.Context(), w)
}

func (s *Server) handleWaitsPartial(w http.ResponseWriter, r *http.Request) {
	server := r.URL.Query().Get("server")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.WaitsTable(s.fetchWaits(r, server)).Render(r.Context(), w)
}

func (s *Server) fetchWaits(r *http.Request, server string) []web.WaitRow {
	if server == "" {
		return nil
	}
	now := time.Now().UTC()
	since := now.AddDate(0, 0, -7) // 7-day wait window
	counts, err := s.stats.WaitEventHistogram(r.Context(), server, since, now)
	if err != nil {
		return nil
	}
	var total int64
	for _, c := range counts {
		total += c.Total
	}
	var out []web.WaitRow
	for _, c := range counts {
		out = append(out, web.WaitRow{
			Class:   waitClass(c),
			Total:   c.Total,
			Buckets: c.Buckets,
			Pct:     pct(c.Total, total),
		})
	}
	return out
}

func waitClass(c store.WaitEventCount) string {
	if c.WaitEventType == "" && c.WaitEvent == "" {
		return "CPU"
	}
	if c.WaitEvent == "" {
		return c.WaitEventType
	}
	return c.WaitEventType + " / " + c.WaitEvent
}

func pct(n, total int64) string {
	if total <= 0 {
		return "0%"
	}
	return fmt.Sprintf("%.0f%%", float64(n)/float64(total)*100)
}
```

- [ ] **Step 3: routes** `server.go`:

```go
	s.mux.HandleFunc("GET /waits", s.handleWaitsPage)
	s.mux.HandleFunc("GET /partial/waits", s.handleWaitsPartial)
```

- [ ] **Step 4: nav** `web/layout.templ`: `<a href="/waits">Waits</a>`.

- [ ] **Step 5: api test** `waits_test.go` — mirror `insights_test.go` harness: GET `/waits?server=s1` → 200 + nav + the `hx-get="/partial/waits?server=s1"` poll target; GET `/waits` (no server) → 200 + empty-state text.

- [ ] **Step 6:** `make templ && go build ./... && go test ./internal/api/... ./internal/store/... -count=1 -timeout 600s` → PASS.

- [ ] **Step 7: Commit** `git commit -am "feat(api): /waits wait-event breakdown page (ly-xqf.3)"`

---

## Task 3: Verify
- [ ] `go test ./... -count=1 -timeout 600s` → PASS.
- [ ] Arch grep: one `pgxpool.New` in collector — unchanged (this is a read path on the stats store, api-side).

## Self-Review
- Spec: "historical breakdown (sampled, bucketed)" — aggregates the sampled buckets by wait class over a window. ✔
- On-CPU samples preserved (real signal). ✔
- No new collector work — labels were already collected by `ly-xqf.1`. ✔
