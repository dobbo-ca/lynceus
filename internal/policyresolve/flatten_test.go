package policyresolve

import (
	"reflect"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/caps"
)

// capA / capB are two declared capabilities used across the table so a
// conflict on one never masks resolution of the other.
const (
	capA = caps.PgStatStatements
	capB = caps.AutoExplain
)

// graph1 is a single-server graph: server s1 -> current_database "app".
func graph1() EntityGraph {
	return EntityGraph{
		"s1": {CurrentDatabase: "app", Labels: map[string]string{"env": "prod"}},
	}
}

// graph2 is a two-server graph in the same cluster, distinct current_databases.
func graph2() EntityGraph {
	return EntityGraph{
		"s1": {CurrentDatabase: "app", Labels: map[string]string{"env": "prod"}},
		"s2": {CurrentDatabase: "billing", Labels: map[string]string{"env": "prod"}},
	}
}

// TestFlatten is the table-driven suite: one subtest per acceptance criterion.
func TestFlatten(t *testing.T) {
	cases := []struct {
		name     string
		policies []ScopedPolicy
		graph    EntityGraph
		wantRows []DesiredRow
		wantConf []Conflict
	}{
		{
			// Tier precedence (narrower wins): fleet enabled=false vs instance
			// enabled=true on the same server => instance wins, one NULL-default
			// row enabled=true. (§3 lines 214-217)
			name:  "tier_precedence_instance_beats_fleet",
			graph: graph1(),
			policies: []ScopedPolicy{
				{Name: "fleet-off", Scope: ScopeFleet, Capabilities: []CapabilityToggle{{capA, false}}},
				{Name: "inst-on", Scope: ScopeInstance, Selector: map[string]string{"env": "prod"}, Capabilities: []CapabilityToggle{{capA, true}}},
			},
			wantRows: []DesiredRow{
				{ServerID: "s1", DatabaseName: "", Capability: capA, Enabled: true},
			},
		},
		{
			// Priority tie-break within a tier: two fleet policies, higher
			// priority wins; lower-priority loser dropped. (§3 line 218)
			name:  "priority_tiebreak_within_tier",
			graph: graph1(),
			policies: []ScopedPolicy{
				{Name: "lo", Scope: ScopeFleet, Priority: 1, Capabilities: []CapabilityToggle{{capA, false}}},
				{Name: "hi", Scope: ScopeFleet, Priority: 5, Capabilities: []CapabilityToggle{{capA, true}}},
			},
			wantRows: []DesiredRow{
				{ServerID: "s1", DatabaseName: "", Capability: capA, Enabled: true},
			},
		},
		{
			// Conflict refusal: equal tier + equal priority + conflicting enabled
			// for capA => Conflict for capA and NO row for capA; capB still
			// resolves. (§3 line 218 — refuse-and-surface, not hard-fail)
			name:  "conflict_refusal_isolates_capability",
			graph: graph1(),
			policies: []ScopedPolicy{
				{Name: "x", Scope: ScopeFleet, Priority: 2, Capabilities: []CapabilityToggle{{capA, true}, {capB, true}}},
				{Name: "y", Scope: ScopeFleet, Priority: 2, Capabilities: []CapabilityToggle{{capA, false}}},
			},
			wantRows: []DesiredRow{
				{ServerID: "s1", DatabaseName: "", Capability: capB, Enabled: true},
			},
			wantConf: []Conflict{
				{Capability: capA, ServerID: "s1", Reason: "conflict: equal tier fleet and equal priority 2 disagree on enabled"},
			},
		},
		{
			// Tier precedence over a lower-tier conflict: a fleet-tier
			// equal-priority disagreement (true/false pri0) is shadowed by an
			// instance-tier winner (true). Per "narrower wins", the instance tier
			// decides outright => one NULL-default row enabled=true and NO
			// conflict. The lower-tier disagreement must NOT surface as a Conflict
			// nor drop the winner. (§3 line 139 — reduce to winning level first)
			name:  "tier_winner_shadows_lower_tier_conflict",
			graph: graph1(),
			policies: []ScopedPolicy{
				{Name: "fleet-on", Scope: ScopeFleet, Priority: 0, Capabilities: []CapabilityToggle{{capA, true}}},
				{Name: "fleet-off", Scope: ScopeFleet, Priority: 0, Capabilities: []CapabilityToggle{{capA, false}}},
				{Name: "inst-on", Scope: ScopeInstance, Selector: map[string]string{"env": "prod"}, Capabilities: []CapabilityToggle{{capA, true}}},
			},
			wantRows: []DesiredRow{
				{ServerID: "s1", DatabaseName: "", Capability: capA, Enabled: true},
			},
		},
		{
			// Priority tie-break over a lower-priority conflict within a tier:
			// two fleet pri0 policies disagree (true/false) but a fleet pri5
			// policy (true) wins outright. The higher priority decides => one
			// NULL-default row enabled=true and NO conflict. The lower-priority
			// disagreement must NOT surface as a Conflict. (§3 line 139)
			name:  "priority_winner_shadows_lower_priority_conflict",
			graph: graph1(),
			policies: []ScopedPolicy{
				{Name: "lo-on", Scope: ScopeFleet, Priority: 0, Capabilities: []CapabilityToggle{{capA, true}}},
				{Name: "lo-off", Scope: ScopeFleet, Priority: 0, Capabilities: []CapabilityToggle{{capA, false}}},
				{Name: "hi-on", Scope: ScopeFleet, Priority: 5, Capabilities: []CapabilityToggle{{capA, true}}},
			},
			wantRows: []DesiredRow{
				{ServerID: "s1", DatabaseName: "", Capability: capA, Enabled: true},
			},
		},
		{
			// Absence => no row: capB has no policy at any tier, so it is absent
			// from rows and the gate fails open. (§3 line 219; gate.go:36-44)
			name:  "absence_yields_no_row",
			graph: graph1(),
			policies: []ScopedPolicy{
				{Name: "only-a", Scope: ScopeFleet, Capabilities: []CapabilityToggle{{capA, true}}},
			},
			wantRows: []DesiredRow{
				{ServerID: "s1", DatabaseName: "", Capability: capA, Enabled: true},
			},
		},
		{
			// Server-wide flatten target: a group policy matching two server_ids
			// => two NULL-default rows, one per server_id. (§3 lines 225, 233-236)
			name:  "serverwide_flatten_two_servers",
			graph: graph2(),
			policies: []ScopedPolicy{
				{Name: "grp", Scope: ScopeGroup, Selector: map[string]string{"env": "prod"}, Capabilities: []CapabilityToggle{{capA, false}}},
			},
			wantRows: []DesiredRow{
				{ServerID: "s1", DatabaseName: "", Capability: capA, Enabled: false},
				{ServerID: "s2", DatabaseName: "", Capability: capA, Enabled: false},
			},
		},
		{
			// Database-tier targeting: a database policy whose DatabaseName ==
			// s1's current_database => one override row (DatabaseName != "")
			// keyed to s1 only; s2 (current_database "billing") gets nothing.
			// (§2.3 lines 134-137, §3 lines 226, 236)
			name:  "database_tier_targets_matching_stream",
			graph: graph2(),
			policies: []ScopedPolicy{
				{Name: "db-app", Scope: ScopeDatabase, Selector: map[string]string{"env": "prod"}, DatabaseName: "app", Capabilities: []CapabilityToggle{{capA, true}}},
			},
			wantRows: []DesiredRow{
				{ServerID: "s1", DatabaseName: "app", Capability: capA, Enabled: true},
			},
		},
		{
			// Dead-row suppression (privacy-critical): a database policy naming a
			// database_name that is NO in-scope stream's current_database =>
			// NO override row + a Warning-class Conflict. No sibling-datname rows.
			// (§3 line 226)
			name:  "dead_row_suppressed_with_warning",
			graph: graph2(),
			policies: []ScopedPolicy{
				{Name: "db-ghost", Scope: ScopeDatabase, Selector: map[string]string{"env": "prod"}, DatabaseName: "nonexistent", Capabilities: []CapabilityToggle{{capA, true}}},
			},
			wantRows: nil,
			wantConf: []Conflict{
				{Capability: capA, ServerID: "", Reason: "dead-row: databaseSelector policy \"db-ghost\" targets database \"nonexistent\" which is no in-scope stream's current_database"},
			},
		},
		{
			// Override-beats-default fidelity: fleet default enabled=false AND a
			// database override enabled=true (matching current_database) => BOTH
			// a NULL-default row (enabled=false) AND an override row
			// (enabled=true). Flatten does NOT pre-collapse; the read path picks
			// the override. (capability_policy.go:169-193; policy_refresh.go:48-61)
			name:  "override_beats_default_emits_both_rows",
			graph: graph1(),
			policies: []ScopedPolicy{
				{Name: "fleet-default-off", Scope: ScopeFleet, Capabilities: []CapabilityToggle{{capA, false}}},
				{Name: "db-override-on", Scope: ScopeDatabase, Selector: map[string]string{"env": "prod"}, DatabaseName: "app", Capabilities: []CapabilityToggle{{capA, true}}},
			},
			wantRows: []DesiredRow{
				{ServerID: "s1", DatabaseName: "", Capability: capA, Enabled: false},
				{ServerID: "s1", DatabaseName: "app", Capability: capA, Enabled: true},
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rows, conf := Flatten(c.policies, c.graph)
			if !reflect.DeepEqual(rows, c.wantRows) {
				t.Errorf("rows mismatch:\n got %#v\nwant %#v", rows, c.wantRows)
			}
			if !reflect.DeepEqual(conf, c.wantConf) {
				t.Errorf("conflicts mismatch:\n got %#v\nwant %#v", conf, c.wantConf)
			}
		})
	}
}

// TestFlatten_deterministic proves Flatten is order-independent and stable:
// the same inputs, and inputs supplied in shuffled slice order, produce
// byte-identical sorted output, so the §4.3 line 275 diff step is stable.
func TestFlatten_deterministic(t *testing.T) {
	graph := graph2()
	base := []ScopedPolicy{
		{Name: "fleet-off", Scope: ScopeFleet, Capabilities: []CapabilityToggle{{capA, false}, {capB, true}}},
		{Name: "grp-on", Scope: ScopeGroup, Selector: map[string]string{"env": "prod"}, Capabilities: []CapabilityToggle{{capA, true}}},
		{Name: "db-app", Scope: ScopeDatabase, Selector: map[string]string{"env": "prod"}, DatabaseName: "app", Capabilities: []CapabilityToggle{{capB, false}}},
	}

	rows1, conf1 := Flatten(base, graph)

	// Run twice on identical inputs.
	rows2, conf2 := Flatten(base, graph)
	if !reflect.DeepEqual(rows1, rows2) || !reflect.DeepEqual(conf1, conf2) {
		t.Fatal("Flatten not stable across two runs on identical inputs")
	}

	// Run on a shuffled (reversed) policy slice — output must match.
	shuffled := make([]ScopedPolicy, len(base))
	for i, p := range base {
		shuffled[len(base)-1-i] = p
	}
	rows3, conf3 := Flatten(shuffled, graph)
	if !reflect.DeepEqual(rows1, rows3) {
		t.Fatalf("Flatten output depends on input order:\n in-order %#v\nshuffled %#v", rows1, rows3)
	}
	if !reflect.DeepEqual(conf1, conf3) {
		t.Fatalf("Flatten conflicts depend on input order:\n in-order %#v\nshuffled %#v", conf1, conf3)
	}

	// Sanity: rows must be sorted by (ServerID, DatabaseName, Capability).
	for i := 1; i < len(rows1); i++ {
		a, b := rows1[i-1], rows1[i]
		if a.ServerID > b.ServerID ||
			(a.ServerID == b.ServerID && a.DatabaseName > b.DatabaseName) ||
			(a.ServerID == b.ServerID && a.DatabaseName == b.DatabaseName && a.Capability > b.Capability) {
			t.Fatalf("rows not canonically sorted at %d: %#v then %#v", i, a, b)
		}
	}
}

// TestFlatten_deterministic_withConflict is the regression for the
// order-dependent resolvePrecedence bug: the SAME policy set must yield
// byte-identical rows AND conflicts regardless of input slice order, even when
// an equal-tier/equal-priority disagreement coexists with a higher-tier and a
// higher-priority winner. With the old forward-scan early-return, the fleet
// true/false pri0 pair surfaced a spurious Conflict (and dropped the winning
// row) only when it appeared before the deciding candidate.
func TestFlatten_deterministic_withConflict(t *testing.T) {
	graph := graph1()
	// capA: instance-tier winner (true) shadows a fleet pri0 true/false conflict.
	// capB: fleet pri5 winner (true) shadows a fleet pri0 true/false conflict.
	base := []ScopedPolicy{
		{Name: "fleet-a-on", Scope: ScopeFleet, Priority: 0, Capabilities: []CapabilityToggle{{capA, true}}},
		{Name: "fleet-a-off", Scope: ScopeFleet, Priority: 0, Capabilities: []CapabilityToggle{{capA, false}}},
		{Name: "inst-a-on", Scope: ScopeInstance, Selector: map[string]string{"env": "prod"}, Capabilities: []CapabilityToggle{{capA, true}}},
		{Name: "fleet-b-on", Scope: ScopeFleet, Priority: 0, Capabilities: []CapabilityToggle{{capB, true}}},
		{Name: "fleet-b-off", Scope: ScopeFleet, Priority: 0, Capabilities: []CapabilityToggle{{capB, false}}},
		{Name: "fleet-b-hi", Scope: ScopeFleet, Priority: 5, Capabilities: []CapabilityToggle{{capB, true}}},
	}
	// Rows sort canonically by Capability: capB ("auto_explain") < capA
	// ("pg_stat_statements").
	want := []DesiredRow{
		{ServerID: "s1", DatabaseName: "", Capability: capB, Enabled: true},
		{ServerID: "s1", DatabaseName: "", Capability: capA, Enabled: true},
	}

	rows1, conf1 := Flatten(base, graph)
	if !reflect.DeepEqual(rows1, want) {
		t.Fatalf("rows mismatch:\n got %#v\nwant %#v", rows1, want)
	}
	if len(conf1) != 0 {
		t.Fatalf("expected NO conflict (winning level decides), got %#v", conf1)
	}

	// Reversed order must produce byte-identical output.
	shuffled := make([]ScopedPolicy, len(base))
	for i, p := range base {
		shuffled[len(base)-1-i] = p
	}
	rows2, conf2 := Flatten(shuffled, graph)
	if !reflect.DeepEqual(rows1, rows2) {
		t.Fatalf("rows depend on input order:\n in-order %#v\nshuffled %#v", rows1, rows2)
	}
	if !reflect.DeepEqual(conf1, conf2) {
		t.Fatalf("conflicts depend on input order:\n in-order %#v\nshuffled %#v", conf1, conf2)
	}
}
