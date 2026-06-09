# VACUUM Advisor (`ly-u4t.16`) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development or superpowers:executing-plans. Checkbox steps.

**Goal:** Surface VACUUM/ANALYZE recommendations — **Bloat**, **Performance** (stale stats), **Activity** (autovacuum lag) views — computed from `table_stats` dead-tuple/vacuum metrics, at `/vacuum-advisor`.

**Architecture:** Pure `internal/advisor` function over a slice of table-stat snapshots (advisor-local input struct, fed from `store.TableStatRow` by the api handler). Each table yields zero or more categorized recommendations. The api server reads `LatestTableStats`, calls the advisor, renders a templ page. Collector untouched.

**Tech Stack:** Go, templ. No new proto, no new DB migration. `internal/advisor` already exists (from `ly-u4t.12`) — add `vacuum.go` beside `index.go`.

**Scope note (record in bead):** the **Freezing / wraparound** view needs per-table `relfrozenxid` age, which `table_stats` does **not** carry today; it is owned by `ly-u4t.26` (TXID/MultiXact wraparound, with a txid-age reader) and is explicitly **out of scope here**. This plan delivers Bloat + Performance + Activity from existing metrics.

**Privacy:** inputs are aggregate counts + timestamps; relation names are schema identifiers. No literal path.

---

## File Structure
- Create `internal/advisor/vacuum.go`, `internal/advisor/vacuum_test.go`.
- Create `internal/api/vacuum_advisor.go`, `internal/api/vacuum_advisor_test.go`.
- Create `web/vacuum_advisor.templ` (+ generated).
- Modify `internal/api/server.go` — register `GET /vacuum-advisor`, `GET /partial/vacuum-advisor`.
- Modify `web/layout.templ` nav — `<a href="/vacuum-advisor">Vacuum advisor</a>`.

---

## Task 1: VACUUM recommender (pure)

**Files:** `internal/advisor/vacuum.go`, `internal/advisor/vacuum_test.go`.

**Rules** (per table snapshot):
- **Bloat:** `dead_ratio = dead/(live+dead)`. Flag when `dead_ratio >= 0.20` AND `dead >= 1000`. Severity: `>=0.40` High, `>=0.20` Medium. Recommend `VACUUM`.
- **Performance (stale stats):** `n_mod_since_analyze >= max(1000, 0.10*live)` → estimates drift. Severity: `>=0.50*live` High, else Medium. Recommend `ANALYZE`.
- **Activity (autovacuum lag):** `dead >= 10000` AND (`last_autovacuum` zero OR older than `now-24h`) → autovacuum is not keeping up. Severity High if `last_autovacuum` is zero, else Medium. Recommend reviewing autovacuum thresholds / manual `VACUUM`.

A table may produce more than one (e.g. Bloat + Activity). `now` is passed in (deterministic tests; do not call time in the pure fn).

- [ ] **Step 1: Types + failing test** `vacuum_test.go`:

```go
package advisor

import (
	"testing"
	"time"
)

func TestVacuumAdvice_bloat(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	in := []TableVacuumInfo{{
		Relation: "events", LiveTuples: 100000, DeadTuples: 60000, // 37.5% -> medium? 60000/160000=0.375
		NModSinceAnalyze: 0, LastAutovacuum: now.Add(-time.Hour),
	}}
	recs := VacuumAdvice(in, now)
	if r := findCat(recs, CatBloat); r == nil || r.Severity != SevMedium {
		t.Fatalf("bloat = %+v, want medium", r)
	}
}

func TestVacuumAdvice_staleStats_andActivity(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	in := []TableVacuumInfo{{
		Relation: "orders", LiveTuples: 50000, DeadTuples: 20000,
		NModSinceAnalyze: 40000,                // > 0.5*live -> high ANALYZE
		LastAutovacuum:   time.Time{},          // never -> activity high (dead<10000? 20000>=10000 yes)
	}}
	recs := VacuumAdvice(in, now)
	if r := findCat(recs, CatPerformance); r == nil || r.Severity != SevHigh {
		t.Fatalf("performance = %+v, want high", r)
	}
	if r := findCat(recs, CatActivity); r == nil || r.Severity != SevHigh {
		t.Fatalf("activity = %+v, want high", r)
	}
}

func TestVacuumAdvice_healthy_none(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	in := []TableVacuumInfo{{
		Relation: "small", LiveTuples: 10000, DeadTuples: 100,
		NModSinceAnalyze: 50, LastAutovacuum: now.Add(-time.Hour),
	}}
	if recs := VacuumAdvice(in, now); len(recs) != 0 {
		t.Errorf("healthy table flagged: %+v", recs)
	}
}

func findCat(recs []VacuumRecommendation, c VacuumCategory) *VacuumRecommendation {
	for i := range recs {
		if recs[i].Category == c {
			return &recs[i]
		}
	}
	return nil
}
```

- [ ] **Step 2: Implement** `vacuum.go`:

```go
package advisor

import (
	"fmt"
	"sort"
	"time"
)

type VacuumCategory string

const (
	CatBloat       VacuumCategory = "bloat"
	CatPerformance VacuumCategory = "performance" // stale stats -> ANALYZE
	CatActivity    VacuumCategory = "activity"    // autovacuum lag
)

type VacuumSeverity string

const (
	SevLow    VacuumSeverity = "low"
	SevMedium VacuumSeverity = "medium"
	SevHigh   VacuumSeverity = "high"
)

// TableVacuumInfo is the advisor-local projection of store.TableStatRow the
// api handler feeds in. Counts + timestamps only.
type TableVacuumInfo struct {
	Relation         string
	LiveTuples       int64
	DeadTuples       int64
	NModSinceAnalyze int64
	LastVacuum       time.Time // zero -> never
	LastAutovacuum   time.Time // zero -> never
}

// VacuumRecommendation is one categorized suggestion (T1-safe).
type VacuumRecommendation struct {
	Relation  string
	Category  VacuumCategory
	Severity  VacuumSeverity
	DeadRatio float64
	Detail    string
}

// VacuumAdvice computes Bloat / Performance / Activity recommendations from
// table-stat snapshots. now is injected for deterministic tests.
func VacuumAdvice(tables []TableVacuumInfo, now time.Time) []VacuumRecommendation {
	var out []VacuumRecommendation
	for _, t := range tables {
		total := t.LiveTuples + t.DeadTuples
		var ratio float64
		if total > 0 {
			ratio = float64(t.DeadTuples) / float64(total)
		}

		// Bloat
		if t.DeadTuples >= 1000 && ratio >= 0.20 {
			sev := SevMedium
			if ratio >= 0.40 {
				sev = SevHigh
			}
			out = append(out, VacuumRecommendation{
				Relation: t.Relation, Category: CatBloat, Severity: sev, DeadRatio: ratio,
				Detail: fmt.Sprintf("%s is %.0f%% dead tuples (%d dead / %d live); VACUUM to reclaim space.",
					t.Relation, ratio*100, t.DeadTuples, t.LiveTuples),
			})
		}

		// Performance (stale stats)
		threshold := int64(float64(t.LiveTuples) * 0.10)
		if threshold < 1000 {
			threshold = 1000
		}
		if t.NModSinceAnalyze >= threshold {
			sev := SevMedium
			if t.LiveTuples > 0 && t.NModSinceAnalyze >= int64(float64(t.LiveTuples)*0.50) {
				sev = SevHigh
			}
			out = append(out, VacuumRecommendation{
				Relation: t.Relation, Category: CatPerformance, Severity: sev, DeadRatio: ratio,
				Detail: fmt.Sprintf("%s has %d row modifications since last ANALYZE; statistics are drifting — ANALYZE %s.",
					t.Relation, t.NModSinceAnalyze, t.Relation),
			})
		}

		// Activity (autovacuum lag)
		stale := t.LastAutovacuum.IsZero() || now.Sub(t.LastAutovacuum) > 24*time.Hour
		if t.DeadTuples >= 10000 && stale {
			sev := SevMedium
			if t.LastAutovacuum.IsZero() {
				sev = SevHigh
			}
			out = append(out, VacuumRecommendation{
				Relation: t.Relation, Category: CatActivity, Severity: sev, DeadRatio: ratio,
				Detail: fmt.Sprintf("%s has %d dead tuples and autovacuum has not run recently; review autovacuum thresholds or VACUUM manually.",
					t.Relation, t.DeadTuples),
			})
		}
	}
	// Stable order: severity desc, then relation, then category.
	rank := map[VacuumSeverity]int{SevHigh: 0, SevMedium: 1, SevLow: 2}
	sort.SliceStable(out, func(i, j int) bool {
		if rank[out[i].Severity] != rank[out[j].Severity] {
			return rank[out[i].Severity] < rank[out[j].Severity]
		}
		if out[i].Relation != out[j].Relation {
			return out[i].Relation < out[j].Relation
		}
		return out[i].Category < out[j].Category
	})
	return out
}
```

- [ ] **Step 3: Run** `go test ./internal/advisor/... -run TestVacuum -count=1` → PASS.
- [ ] **Step 4: Commit** `git commit -am "feat(advisor): VACUUM advisor — bloat/performance/activity (ly-u4t.16)"`

---

## Task 2: API surfacing + page

**Files:** `internal/api/vacuum_advisor.go`, `internal/api/vacuum_advisor_test.go`, `web/vacuum_advisor.templ`, modify `server.go`, `web/layout.templ`.

- [ ] **Step 1: templ** `web/vacuum_advisor.templ` (mirror index advisor):

```go
package web

import "fmt"

type VacuumAdvisorRow struct {
	Relation  string
	Category  string
	Severity  string
	DeadPct   string // "37%"
	Detail    string
}

templ VacuumAdvisorPage(rows []VacuumAdvisorRow) {
	@Layout("Lynceus — vacuum advisor", "bloat / stats-freshness / autovacuum-lag findings") {
		<p hx-get="/partial/vacuum-advisor" hx-trigger="every 30s" hx-target="#vac-table" hx-swap="outerHTML"></p>
		@VacuumAdvisorTable(rows)
	}
}

templ VacuumAdvisorTable(rows []VacuumAdvisorRow) {
	<div id="vac-table">
		if len(rows) == 0 {
			<p class="empty">No vacuum findings — tables are healthy.</p>
		} else {
			<table>
				<thead><tr><th>Severity</th><th>Category</th><th>Relation</th><th class="num">Dead</th><th>Detail</th></tr></thead>
				<tbody>
					for _, r := range rows {
						<tr>
							<td><code>{ r.Severity }</code></td>
							<td><code>{ r.Category }</code></td>
							<td><code>{ r.Relation }</code></td>
							<td class="num">{ r.DeadPct }</td>
							<td>{ r.Detail }</td>
						</tr>
					}
				</tbody>
			</table>
		}
	</div>
}
```

- [ ] **Step 2: handler** `internal/api/vacuum_advisor.go`:

```go
package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/dobbo-ca/lynceus/internal/advisor"
	"github.com/dobbo-ca/lynceus/web"
)

func (s *Server) handleVacuumAdvisorPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.VacuumAdvisorPage(s.fetchVacuumAdvice(r)).Render(r.Context(), w)
}

func (s *Server) handleVacuumAdvisorPartial(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.VacuumAdvisorTable(s.fetchVacuumAdvice(r)).Render(r.Context(), w)
}

func (s *Server) fetchVacuumAdvice(r *http.Request) []web.VacuumAdvisorRow {
	now := time.Now().UTC()
	// Enumerate servers that have plan/stat data; reuse ListPlanKeys' server set,
	// or iterate discovered servers. Simplest: pull latest table stats per server
	// seen in plan keys (same pattern as index advisor).
	keys, err := s.stats.ListPlanKeys(r.Context(), now.AddDate(0, 0, -30), now, 200)
	if err != nil {
		return nil
	}
	servers := map[string]bool{}
	for _, k := range keys {
		servers[k.ServerID] = true
	}
	var in []advisor.TableVacuumInfo
	for srv := range servers {
		rows, err := s.stats.LatestTableStats(r.Context(), srv, now)
		if err != nil {
			continue
		}
		for _, t := range rows {
			in = append(in, advisor.TableVacuumInfo{
				Relation:         t.ObjectName,
				LiveTuples:       t.LiveTuples,
				DeadTuples:       t.DeadTuples,
				NModSinceAnalyze: t.NModSinceAnalyze,
				LastVacuum:       t.LastVacuum,
				LastAutovacuum:   t.LastAutovacuum,
			})
		}
	}
	var out []web.VacuumAdvisorRow
	for _, rec := range advisor.VacuumAdvice(in, now) {
		out = append(out, web.VacuumAdvisorRow{
			Relation: rec.Relation,
			Category: string(rec.Category),
			Severity: string(rec.Severity),
			DeadPct:  fmt.Sprintf("%.0f%%", rec.DeadRatio*100),
			Detail:   rec.Detail,
		})
	}
	return out
}
```

> If table stats are keyed by server differently (no plan keys yet), the page still renders the empty state — acceptable. A follow-up could enumerate servers from `s.disc`/`s.conf`; keep this MVP simple.

- [ ] **Step 3: routes** in `server.go`:

```go
	s.mux.HandleFunc("GET /vacuum-advisor", s.handleVacuumAdvisorPage)
	s.mux.HandleFunc("GET /partial/vacuum-advisor", s.handleVacuumAdvisorPartial)
```

- [ ] **Step 4: nav** `web/layout.templ`: `<a href="/vacuum-advisor">Vacuum advisor</a>`.

- [ ] **Step 5: api test** `vacuum_advisor_test.go` — mirror `insights_test.go` harness: GET `/vacuum-advisor` → 200 + nav + `hx-get="/partial/vacuum-advisor"`; GET partial → 200.

- [ ] **Step 6:** `make templ && go build ./... && go test ./internal/advisor/... ./internal/api/... -count=1` → PASS.

- [ ] **Step 7: Commit** `git commit -am "feat(api): /vacuum-advisor page (ly-u4t.16)"`

---

## Task 3: Verify
- [ ] `go test ./... -count=1 -timeout 600s` → PASS.
- [ ] Arch grep: one `pgxpool.New` in collector. Advisor is api-side.

## Self-Review
- Spec: Bloat ✔, Performance/stats-freshness ✔, Activity ✔. Freezing deferred to `ly-u4t.26` (documented). ✔
- Pure fn takes injected `now` (no time call inside) — deterministic tests. ✔
