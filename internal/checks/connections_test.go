package checks

import "testing"

func TestActiveLongRunningCheck_severityLadder(t *testing.T) {
	in := &Input{Connections: []ConnInfo{
		{PID: 1, State: "active", ActiveSeconds: 50},                // below warn
		{PID: 2, State: "active", ActiveSeconds: 400},               // warning
		{PID: 3, State: "active", ActiveSeconds: 1000},              // critical
		{PID: 4, State: "idle in transaction", ActiveSeconds: 9999}, // wrong state, ignored
	}}
	got := ActiveLongRunningCheck{}.Eval(in)
	if len(got) != 2 {
		t.Fatalf("want 2 firing results, got %d: %+v", len(got), got)
	}
	bySev := map[Severity]Result{}
	for _, r := range got {
		bySev[r.Severity] = r
		if r.CheckID != "connections.long_running_active" || r.Category != "connections" || r.Status != StatusFiring {
			t.Fatalf("bad result shape: %+v", r)
		}
	}
	if _, ok := bySev[SeverityWarning]; !ok {
		t.Fatalf("missing warning result: %+v", got)
	}
	if _, ok := bySev[SeverityCritical]; !ok {
		t.Fatalf("missing critical result: %+v", got)
	}
	if got := bySev[SeverityCritical].Object; got != "pid:3" {
		t.Fatalf("critical Object = %q, want pid:3", got)
	}
}

func TestIdleInTransactionCheck_severityLadder(t *testing.T) {
	in := &Input{Connections: []ConnInfo{
		{PID: 1, State: "idle in transaction", StateSeconds: 50},             // below warn
		{PID: 2, State: "idle in transaction", StateSeconds: 400},            // warning
		{PID: 3, State: "idle in transaction (aborted)", StateSeconds: 1000}, // critical
		{PID: 4, State: "active", StateSeconds: 9999},                        // wrong state, ignored
	}}
	got := IdleInTransactionCheck{}.Eval(in)
	if len(got) != 2 {
		t.Fatalf("want 2 firing results, got %d: %+v", len(got), got)
	}
	for _, r := range got {
		if r.CheckID != "connections.idle_in_transaction" || r.Category != "connections" || r.Status != StatusFiring {
			t.Fatalf("bad result shape: %+v", r)
		}
	}
}

func TestBlockingCheck_warnAndCritical(t *testing.T) {
	in := &Input{Blocking: []BlockEdge{
		{BlockedPID: 11, BlockerPID: 10, BlockedWaitSeconds: 5},  // warning
		{BlockedPID: 21, BlockerPID: 20, BlockedWaitSeconds: 90}, // critical (>= 60s)
	}}
	got := BlockingCheck{}.Eval(in)
	if len(got) != 2 {
		t.Fatalf("want 2 results, got %d: %+v", len(got), got)
	}
	bySev := map[Severity]Result{}
	for _, r := range got {
		bySev[r.Severity] = r
		if r.CheckID != "connections.blocking" || r.Category != "connections" || r.Status != StatusFiring {
			t.Fatalf("bad result shape: %+v", r)
		}
	}
	if bySev[SeverityCritical].Object != "pid:21" {
		t.Fatalf("critical Object = %q, want pid:21", bySev[SeverityCritical].Object)
	}
	if _, ok := bySev[SeverityWarning]; !ok {
		t.Fatalf("missing warning result: %+v", got)
	}
}

func TestConnectionsChecksRegistered(t *testing.T) {
	want := map[string]bool{
		"connections.long_running_active": false,
		"connections.idle_in_transaction": false,
		"connections.blocking":            false,
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
