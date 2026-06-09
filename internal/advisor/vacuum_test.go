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
		NModSinceAnalyze: 40000,       // > 0.5*live -> high ANALYZE
		LastAutovacuum:   time.Time{}, // never -> activity high (dead<10000? 20000>=10000 yes)
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

func TestFreezeAdviceFlagsHighAge(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	in := []TableFreezeInfo{
		{Relation: "public.hot", XIDAge: 1_800_000_000},        // >=1.5e9 -> high
		{Relation: "public.warm", XIDAge: 600_000_000},         // >=0.5e9 -> medium
		{Relation: "public.cold", XIDAge: 50_000_000},          // healthy -> none
		{Relation: "public.mx", XIDAge: 10_000, MXIDAge: 1_700_000_000}, // MultiXact -> high
	}
	recs := FreezeAdvice(in, now)
	got := map[string]VacuumSeverity{}
	for _, r := range recs {
		if r.Category != CatFreezing {
			t.Fatalf("non-freezing category: %+v", r)
		}
		got[r.Relation] = r.Severity
	}
	if got["public.hot"] != SevHigh {
		t.Errorf("public.hot = %q, want high", got["public.hot"])
	}
	if got["public.warm"] != SevMedium {
		t.Errorf("public.warm = %q, want medium", got["public.warm"])
	}
	if _, ok := got["public.cold"]; ok {
		t.Errorf("public.cold flagged but is healthy: %+v", recs)
	}
	if got["public.mx"] != SevHigh {
		t.Errorf("public.mx = %q, want high (MultiXact age)", got["public.mx"])
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
