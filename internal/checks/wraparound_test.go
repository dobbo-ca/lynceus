package checks

import "testing"

func TestWraparoundCriticalAboveHeadroom(t *testing.T) {
	in := Input{ServerID: "srv-a", FreezeAges: []FreezeInfo{
		{Scope: "table", Relation: "public.hot", XIDAge: 1_800_000_000, AutovacuumFreezeMaxAge: 200_000_000},
		{Scope: "table", Relation: "public.cool", XIDAge: 50_000_000, AutovacuumFreezeMaxAge: 200_000_000},
	}}
	got := WraparoundCheck{}.Eval(&in)
	if len(got) != 1 || got[0].Object != "public.hot" || got[0].Severity != SeverityCritical {
		t.Fatalf("want 1 critical for public.hot, got %+v", got)
	}
}

func TestWraparoundWarningInWarnBand(t *testing.T) {
	in := Input{ServerID: "srv-a", FreezeAges: []FreezeInfo{
		{Scope: "database", Relation: "appdb", XIDAge: 600_000_000, AutovacuumFreezeMaxAge: 200_000_000},
	}}
	got := WraparoundCheck{}.Eval(&in)
	if len(got) != 1 || got[0].Severity != SeverityWarning {
		t.Fatalf("want 1 warning, got %+v", got)
	}
}

// TestWraparoundReportsWorseOfXIDvsMXID confirms MultiXact age drives the
// verdict when it exceeds the transaction-id age.
func TestWraparoundReportsWorseOfXIDvsMXID(t *testing.T) {
	in := Input{ServerID: "srv-a", FreezeAges: []FreezeInfo{
		{Scope: "table", Relation: "public.mx", XIDAge: 10_000, MXIDAge: 1_600_000_000, AutovacuumFreezeMaxAge: 200_000_000},
	}}
	got := WraparoundCheck{}.Eval(&in)
	if len(got) != 1 || got[0].Severity != SeverityCritical {
		t.Fatalf("want 1 critical driven by MultiXact age, got %+v", got)
	}
}
