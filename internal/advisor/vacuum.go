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
