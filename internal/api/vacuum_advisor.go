package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/dobbo-ca/lynceus/internal/advisor"
	"github.com/dobbo-ca/lynceus/web"
)

func (s *Server) handleVacuumAdvisorPage(w http.ResponseWriter, r *http.Request) {
	sv := s.shellViewFor(r, "vacuumadvisor")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.VacuumAdvisorPage(sv, s.fetchVacuumAdvice(r)).Render(r.Context(), w)
}

func (s *Server) handleVacuumAdvisorPartial(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.VacuumAdvisorView(s.fetchVacuumAdvice(r)).Render(r.Context(), w)
}

// fetchVacuumAdvice builds the 4-panel VM: BLOAT and ACTIVITY straight from the
// latest table stats per server; PERFORMANCE from the advisor's stale-stats
// (CatPerformance) category only; FREEZING directly from the latest freeze ages
// per server (per-row AutovacuumFreezeMaxAge; ly-u4t.26 tracks data breadth).
func (s *Server) fetchVacuumAdvice(r *http.Request) web.VacuumAdvisorVM {
	now := time.Now().UTC()
	since := now.AddDate(0, 0, -30)
	vm := web.VacuumAdvisorVM{ScopeLabel: r.URL.Query().Get("server")}
	keys, err := s.stats.ListPlanKeys(r.Context(), since, now, 200)
	if err != nil {
		return vm
	}
	servers := map[string]bool{}
	for _, k := range keys {
		servers[k.ServerID] = true
	}
	var tvi []advisor.TableVacuumInfo
	for srv := range servers {
		for _, ts := range latestTableStats(r, s, srv, now) {
			total := ts.LiveTuples + ts.DeadTuples
			pct := 0
			if total > 0 {
				pct = int(float64(ts.DeadTuples) / float64(total) * 100)
			}
			vm.Bloat = append(vm.Bloat, web.VacBloatRow{
				Relation: ts.ObjectName, PctLabel: fmt.Sprintf("%d%%", pct), WidthPct: pct,
				ColorVar: bloatColor(pct), Dead: fmt.Sprintf("%d dead", ts.DeadTuples),
				Wasted: prettyBytes(ts.TotalBytes),
			})
			vm.Activity = append(vm.Activity, web.VacActivityRow{
				Relation: ts.ObjectName, Last: agoLabel(ts.LastAutovacuum, now),
				LastColorVar: agoColor(ts.LastAutovacuum, now), Analyze: agoLabel(ts.LastVacuum, now),
			})
			tvi = append(tvi, advisor.TableVacuumInfo{
				Relation: ts.ObjectName, LiveTuples: ts.LiveTuples, DeadTuples: ts.DeadTuples,
				NModSinceAnalyze: ts.NModSinceAnalyze, LastVacuum: ts.LastVacuum, LastAutovacuum: ts.LastAutovacuum,
			})
		}
		if fz, err := s.stats.LatestFreezeAges(r.Context(), srv, now); err == nil {
			for _, f := range fz {
				// Per-row freeze budget from the collected GUC; fall back to PG's
				// 200M default if unset. The hard-wraparound guard is ~1.3× the
				// freeze tick, so the tick sits at 76.9% of the bar (fixed marker).
				freezeMax := f.AutovacuumFreezeMaxAge
				if freezeMax <= 0 {
					freezeMax = 200000000
				}
				hardLimit := freezeMax * 13 / 10
				pct := int(float64(f.XIDAge) / float64(hardLimit) * 100)
				vm.Freeze = append(vm.Freeze, web.VacFreezeRow{
					Name: f.FQN, Kind: "xid", AgeLabel: millions(f.XIDAge),
					PctLabel: fmt.Sprintf("%d%%", int(float64(f.XIDAge)/float64(freezeMax)*100)),
					WidthPct: pct, ColorVar: freezeColor(f.XIDAge, freezeMax),
				})
			}
		}
	}
	// Only the performance (stale-stats → ANALYZE) category feeds the PERFORMANCE
	// panel; BLOAT/ACTIVITY are already built from raw stats above.
	for _, rec := range advisor.VacuumAdvice(tvi, now) {
		if rec.Category != advisor.CatPerformance {
			continue
		}
		vm.Perf = append(vm.Perf, web.VacPerfRow{Label: rec.Relation, Value: string(rec.Category), Detail: rec.Detail})
	}
	return vm
}

func bloatColor(pct int) string {
	switch {
	case pct >= 40:
		return "var(--crit)"
	case pct >= 20:
		return "var(--warn)"
	default:
		return "var(--acc)"
	}
}

// freezeColor grades XID age against the per-row freeze budget: crit at/over the
// freeze tick, warn at 75% of it, else accent.
func freezeColor(age, freezeMax int64) string {
	switch {
	case age >= freezeMax:
		return "var(--crit)"
	case age >= freezeMax*3/4:
		return "var(--warn)"
	default:
		return "var(--acc)"
	}
}

func millions(n int64) string { return fmt.Sprintf("%dM", n/1000000) }

func agoLabel(t, now time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := now.Sub(t)
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func agoColor(t, now time.Time) string {
	if t.IsZero() || now.Sub(t) > 7*24*time.Hour {
		return "var(--critT)"
	}
	return "var(--dim)"
}
