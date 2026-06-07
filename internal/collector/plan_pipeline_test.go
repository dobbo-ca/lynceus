package collector

import (
	"strings"
	"testing"
	"time"

	"github.com/dobbo-ca/lynceus/internal/logparse"
)

const autoExplainJSON = `[{"Query Text":"SELECT count(*) FROM orders WHERE total > 500.00",` +
	`"Plan":{"Node Type":"Seq Scan","Relation Name":"orders","Total Cost":96.5,` +
	`"Plan Rows":2532,"Filter":"(total > 500.00)"}}]`

func TestExtractPlans_extractsAutoExplainAndSkipsRest(t *testing.T) {
	at := time.Unix(1700000000, 0).UTC()
	events := []logparse.LogEvent{
		{EventType: logparse.EventAutoExplainPlan, OccurredAt: at},
		{EventType: logparse.EventUnclassified, OccurredAt: at}, // ignored
		{EventType: logparse.EventAutoExplainPlan, OccurredAt: at}, // text body -> dropped
	}
	payloads := []logparse.LogPayload{
		{Message: "duration: 5.123 ms  plan:\n" + autoExplainJSON},
		{Message: "some unrelated log line"},
		{Message: "duration: 9.9 ms plan:\nSeq Scan on orders  (cost=0.00..96.50 rows=2532)"},
	}

	plans := ExtractPlans(events, payloads)
	if len(plans) != 1 {
		t.Fatalf("got %d plans, want 1", len(plans))
	}
	qp := plans[0]
	if qp.GetRoot().GetNodeType() != "Seq Scan" {
		t.Errorf("root node type = %q, want Seq Scan", qp.GetRoot().GetNodeType())
	}
	if qp.GetCapturedAtUnix() != at.Unix() {
		t.Errorf("captured_at_unix = %d, want %d", qp.GetCapturedAtUnix(), at.Unix())
	}
	// Fingerprint derived collector-side from Query Text; only the hash leaves.
	if qp.GetFingerprint() == "" {
		t.Error("expected a non-empty fingerprint from the plan's Query Text")
	}
	// No literal survives into the normalized condition.
	cond := qp.GetRoot().GetNormalizedCondition()
	if cond == "" {
		t.Error("expected a normalized Filter on the Seq Scan")
	}
	if strings.Contains(cond, "500.00") || strings.Contains(cond, "'") {
		t.Errorf("literal leaked into normalized_condition: %q", cond)
	}
}

func TestExtractPlans_noAutoExplainReturnsNil(t *testing.T) {
	events := []logparse.LogEvent{{EventType: logparse.EventUnclassified}}
	payloads := []logparse.LogPayload{{Message: "nothing here"}}
	if got := ExtractPlans(events, payloads); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}
