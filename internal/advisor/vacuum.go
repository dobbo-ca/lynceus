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
	CatFreezing    VacuumCategory = "freezing"    // transaction-id / MultiXact wraparound risk
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

// TableFreezeInfo is the advisor-local projection of store.FreezeAgeRow the api
// handler feeds in for the Freezing view. Ages are counts only (no raw xids).
type TableFreezeInfo struct {
	Relation string
	XIDAge   int64
	MXIDAge  int64
}

const (
	freezeHigh   = 1_500_000_000 // ~75% of the ~2.0B wraparound budget
	freezeMedium = 500_000_000   // lagging past a freeze cycle
)

// FreezeAdvice computes CatFreezing recommendations from per-relation freeze
// ages — flagging transaction-id / MultiXact wraparound risk. It is a sibling
// of VacuumAdvice (separate input shape) so the existing signature is
// untouched. It reports the worse of xid_age / mxid_age per relation; Detail
// is counts only. now is accepted for signature parity with VacuumAdvice.
func FreezeAdvice(freezes []TableFreezeInfo, now time.Time) []VacuumRecommendation {
	_ = now
	var out []VacuumRecommendation
	for _, f := range freezes {
		age := f.XIDAge
		kind := "transaction-id"
		if f.MXIDAge > age {
			age, kind = f.MXIDAge, "MultiXact"
		}
		var sev VacuumSeverity
		switch {
		case age >= freezeHigh:
			sev = SevHigh
		case age >= freezeMedium:
			sev = SevMedium
		default:
			continue
		}
		out = append(out, VacuumRecommendation{
			Relation: f.Relation, Category: CatFreezing, Severity: sev,
			Detail: fmt.Sprintf("%s has %s freeze age %d approaching the ~2.1B wraparound ceiling; ensure (auto)VACUUM is freezing this relation.",
				f.Relation, kind, age),
		})
	}
	rank := map[VacuumSeverity]int{SevHigh: 0, SevMedium: 1, SevLow: 2}
	sort.SliceStable(out, func(i, j int) bool {
		if rank[out[i].Severity] != rank[out[j].Severity] {
			return rank[out[i].Severity] < rank[out[j].Severity]
		}
		return out[i].Relation < out[j].Relation
	})
	return out
}
