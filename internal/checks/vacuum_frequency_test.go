package checks

import "testing"

func TestInsufficientVacuumFrequency_severityLadder(t *testing.T) {
	in := &Input{TableStats: []TableInfo{
		{Relation: "public.below", LiveTuples: 100_000, DeadTuples: 1_500}, // trigger 20050, dead below -> no fire
		{Relation: "public.warn", LiveTuples: 5_000, DeadTuples: 1_500},    // trigger 1050, dead in (1050,2100] -> warning
		{Relation: "public.crit", LiveTuples: 5_000, DeadTuples: 5_000},    // dead > 2100 -> critical
		{Relation: "public.tiny", LiveTuples: 10, DeadTuples: 500},         // dead < floor -> ignored
	}}
	got := InsufficientVacuumFrequencyCheck{}.Eval(in)
	if len(got) != 2 {
		t.Fatalf("want 2 firing results, got %d: %+v", len(got), got)
	}
	bySev := map[Severity]Result{}
	for _, r := range got {
		bySev[r.Severity] = r
		if r.CheckID != "vacuum.insufficient_frequency" || r.Category != "vacuum" || r.Status != StatusFiring {
			t.Fatalf("bad result shape: %+v", r)
		}
	}
	warn, ok := bySev[SeverityWarning]
	if !ok {
		t.Fatalf("missing warning result: %+v", got)
	}
	if warn.Object != "public.warn" {
		t.Fatalf("warning Object = %q, want public.warn", warn.Object)
	}
	crit, ok := bySev[SeverityCritical]
	if !ok {
		t.Fatalf("missing critical result: %+v", got)
	}
	if crit.Object != "public.crit" {
		t.Fatalf("critical Object = %q, want public.crit", crit.Object)
	}
}

func TestVacuumChecksRegistered(t *testing.T) {
	want := map[string]bool{
		"vacuum.insufficient_frequency": false,
		"vacuum.xmin_horizon":           false,
	}
	for _, c := range DefaultChecks() {
		if _, ok := want[c.ID()]; ok {
			want[c.ID()] = true
		}
	}
	for id, found := range want {
		if !found {
			t.Fatalf("check %q not registered", id)
		}
	}
}
