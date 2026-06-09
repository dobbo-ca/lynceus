package checks

import "testing"

// fakeCheck emits one result iff trigger is true.
type fakeCheck struct {
	id      string
	cat     string
	trigger bool
	sev     Severity
}

func (f fakeCheck) ID() string       { return f.id }
func (f fakeCheck) Category() string { return f.cat }
func (f fakeCheck) Eval(in *Input) []Result {
	if !f.trigger {
		return nil
	}
	return []Result{{
		CheckID: f.id, Category: f.cat, Severity: f.sev,
		Status: StatusFiring, Object: "rel1", Detail: "boom",
	}}
}

func TestRunCollectsFiringResultsAndStampsServerTime(t *testing.T) {
	in := Input{ServerID: "srv-a"}
	got := Run(&in, []Check{
		fakeCheck{id: "c.fire", cat: "queries", trigger: true, sev: SeverityCritical},
		fakeCheck{id: "c.quiet", cat: "queries", trigger: false},
	})
	if len(got) != 1 {
		t.Fatalf("want 1 result, got %d", len(got))
	}
	r := got[0]
	if r.CheckID != "c.fire" || r.Severity != SeverityCritical || r.Status != StatusFiring {
		t.Fatalf("unexpected result: %+v", r)
	}
	if r.ServerID != "srv-a" {
		t.Fatalf("Run must stamp ServerID from Input, got %q", r.ServerID)
	}
}

func TestSeverityRankOrders(t *testing.T) {
	if SeverityCritical.rank() <= SeverityWarning.rank() ||
		SeverityWarning.rank() <= SeverityInfo.rank() {
		t.Fatal("rank order must be critical > warning > info")
	}
}
