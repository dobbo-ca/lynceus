package caps_test

import (
	"sort"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/caps"
)

// TestDeclared_listsAllKnownCapabilities pins the list. If a future bead
// adds a new Capability constant, the developer must extend this test —
// which also forces them to add a probe (otherwise Discover's completeness
// loop fills in a "bug" reason).
func TestDeclared_listsAllKnownCapabilities(t *testing.T) {
	want := []caps.Capability{
		caps.AutoExplain,
		caps.LogDestination,
		caps.PgBuffercache,
		caps.PgStatActivityFullRead,
		caps.PgStatStatements,
		caps.PgStatTuple,
		caps.PgWaitSampling,
		caps.RolePermissions,
		caps.ServerVersion,
	}
	got := append([]caps.Capability(nil), caps.Declared()...)
	sort.Slice(got, func(i, j int) bool { return string(got[i]) < string(got[j]) })
	sort.Slice(want, func(i, j int) bool { return string(want[i]) < string(want[j]) })
	if len(got) != len(want) {
		t.Fatalf("Declared length = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Declared[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSet_isMapStringStatus(t *testing.T) {
	s := caps.Set{}
	s[caps.PgStatStatements] = caps.Status{Available: true, Reason: "1.10"}
	got := s[caps.PgStatStatements]
	if !got.Available || got.Reason != "1.10" {
		t.Fatalf("Set assignment broken: %+v", got)
	}
}
