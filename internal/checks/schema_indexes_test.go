package checks

import "testing"

func TestInvalidIndexCheck_firesOnInvalidOnly(t *testing.T) {
	in := &Input{Indexes: []IndexInfo{
		{FQN: "public.good", TableFQN: "public.t", IsValid: true, IsReady: true},
		{FQN: "public.bad", TableFQN: "public.t", IsValid: false, IsReady: true},
	}}
	got := InvalidIndexCheck{}.Eval(in)
	if len(got) != 1 {
		t.Fatalf("want 1 firing result, got %d: %+v", len(got), got)
	}
	r := got[0]
	if r.CheckID != "schema.invalid_index" || r.Category != "schema" ||
		r.Severity != SeverityWarning || r.Status != StatusFiring || r.Object != "public.bad" {
		t.Fatalf("bad result shape: %+v", r)
	}
}

func TestUnusedIndexCheck_severityLadderAndSuppressions(t *testing.T) {
	in := &Input{Indexes: []IndexInfo{
		// primary key: suppressed even though unused.
		{FQN: "public.t_pkey", TableFQN: "public.t", IsValid: true, IsPrimary: true, IsUnique: true, IdxScan: 0, SizeBytes: 1 << 30},
		// unique (constraint-backing): suppressed.
		{FQN: "public.t_uq", TableFQN: "public.t", IsValid: true, IsUnique: true, IdxScan: 0, SizeBytes: 1 << 30},
		// invalid: owned by the invalid check, suppressed here.
		{FQN: "public.t_invalid", TableFQN: "public.t", IsValid: false, IdxScan: 0, SizeBytes: 1 << 30},
		// scanned above threshold: not unused.
		{FQN: "public.t_hot", TableFQN: "public.t", IsValid: true, IdxScan: 5000, SizeBytes: 1 << 30},
		// unused but trivially small: below the byte floor, suppressed.
		{FQN: "public.t_tiny", TableFQN: "public.t", IsValid: true, IdxScan: 0, SizeBytes: 4096},
		// unused, medium: INFO.
		{FQN: "public.t_med", TableFQN: "public.t", IsValid: true, IdxScan: 3, SizeBytes: 5 << 20},
		// unused, large: WARNING.
		{FQN: "public.t_big", TableFQN: "public.t", IsValid: true, IdxScan: 0, SizeBytes: 500 << 20},
	}}
	got := UnusedIndexCheck{}.Eval(in)
	bySev := map[Severity]Result{}
	for _, r := range got {
		if r.CheckID != "schema.unused_index" || r.Category != "schema" || r.Status != StatusFiring {
			t.Fatalf("bad result shape: %+v", r)
		}
		bySev[r.Severity] = r
	}
	if len(got) != 2 {
		t.Fatalf("want exactly 2 results (med=info, big=warning), got %d: %+v", len(got), got)
	}
	if bySev[SeverityInfo].Object != "public.t_med" {
		t.Fatalf("info Object = %q, want public.t_med", bySev[SeverityInfo].Object)
	}
	if bySev[SeverityWarning].Object != "public.t_big" {
		t.Fatalf("warning Object = %q, want public.t_big", bySev[SeverityWarning].Object)
	}
}

func TestSchemaChecksRegistered(t *testing.T) {
	want := map[string]bool{
		"schema.invalid_index": false,
		"schema.unused_index":  false,
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
