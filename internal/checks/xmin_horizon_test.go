package checks

import "testing"

func TestXminHorizonCheck_severityLadder(t *testing.T) {
	// nil horizon => no result.
	if got := (XminHorizonCheck{}).Eval(&Input{}); len(got) != 0 {
		t.Fatalf("nil XminHorizon must not fire, got %+v", got)
	}

	cases := []struct {
		age      int64
		wantFire bool
		wantSev  Severity
	}{
		{age: 1_000_000, wantFire: false},                          // below warn
		{age: 50_000_000, wantFire: true, wantSev: SeverityWarning}, // at warn
		{age: 400_000_000, wantFire: true, wantSev: SeverityWarning},
		{age: 500_000_000, wantFire: true, wantSev: SeverityCritical}, // at critical
		{age: 900_000_000, wantFire: true, wantSev: SeverityCritical},
	}
	for _, tc := range cases {
		in := &Input{XminHorizon: &XminInfo{OldestXminAge: tc.age, HolderKind: "replication_slot"}}
		got := XminHorizonCheck{}.Eval(in)
		if !tc.wantFire {
			if len(got) != 0 {
				t.Fatalf("age %d must not fire, got %+v", tc.age, got)
			}
			continue
		}
		if len(got) != 1 {
			t.Fatalf("age %d want 1 result, got %d: %+v", tc.age, len(got), got)
		}
		r := got[0]
		if r.Severity != tc.wantSev {
			t.Fatalf("age %d severity = %q, want %q", tc.age, r.Severity, tc.wantSev)
		}
		if r.CheckID != "vacuum.xmin_horizon" || r.Category != "vacuum" || r.Status != StatusFiring {
			t.Fatalf("bad result shape: %+v", r)
		}
		if r.Object != "replication_slot" {
			t.Fatalf("Object = %q, want holder kind replication_slot", r.Object)
		}
	}
}
