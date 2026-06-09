package insight_test

import (
	"strings"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/insight"
)

func TestNestedLoop_flagged(t *testing.T) {
	got := insight.DetectAll(planFromFixture(t, "nestedloop.json"))
	var nl *insight.Insight
	for i := range got {
		if got[i].Kind == insight.KindNestedLoop {
			nl = &got[i]
		}
	}
	if nl == nil {
		t.Fatalf("no nested_loop insight: %+v", got)
	}
	if nl.Severity != insight.SeverityMedium { // 200000 inner loops: >=100000, <1000000
		t.Errorf("severity = %q, want medium", nl.Severity)
	}
	if !strings.Contains(nl.Detail, "Nested Loop") {
		t.Errorf("detail missing Nested Loop: %q", nl.Detail)
	}
	for _, banned := range []string{"'", "=", "::"} {
		if strings.Contains(nl.Detail, banned) {
			t.Errorf("possible literal %q in detail: %q", banned, nl.Detail)
		}
	}
}

func TestNestedLoop_notFlagged(t *testing.T) {
	for _, in := range insight.DetectAll(planFromFixture(t, "nestedloop_fewloops.json")) {
		if in.Kind == insight.KindNestedLoop {
			t.Errorf("few-loop Nested Loop flagged: %+v", in)
		}
	}
}
