package collector

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/dobbo-ca/lynceus/internal/logparse"
)

// fakeSource yields chunks[0], then chunks[1], ... then empty readers forever.
type fakeSource struct {
	chunks []string
	i      int
	closed bool
}

func (f *fakeSource) Read() (io.Reader, error) {
	if f.i >= len(f.chunks) {
		return bytes.NewReader(nil), nil
	}
	r := strings.NewReader(f.chunks[f.i])
	f.i++
	return r, nil
}
func (f *fakeSource) Close() error { f.closed = true; return nil }

const pipelineCanary = "PHI-CANARY-LEAK-9c2e3a"

// One stderr-format record: an auto_explain.plan whose JSON body references the
// canary in a Filter. The raw line is the T2 payload; the classified T1 event
// must carry only event_type/severity/etc. ParseStream's stderr parser uses the
// default "%m [%p] " prefix.
var stderrAutoExplain = "2026-06-08 12:00:00.000 UTC [123] LOG:  duration: 5.123 ms  plan:\n" +
	"\t[{\"Query Text\":\"SELECT id FROM patients WHERE email = '" + pipelineCanary + "@example.com'\"," +
	"\"Plan\":{\"Node Type\":\"Seq Scan\",\"Relation Name\":\"patients\"," +
	"\"Total Cost\":96.5,\"Plan Rows\":2532,\"Filter\":\"(email = '" + pipelineCanary + "@example.com')\"}}]\n"

func newTestPipeline(src LogSource, detectLocally bool) *LogPipeline {
	return NewLogPipeline(src, "srv-test",
		logparse.Options{Format: logparse.FormatStderr}, detectLocally)
}

func TestDrain_mapsLogEventsAndExtractsPlans(t *testing.T) {
	src := &fakeSource{chunks: []string{stderrAutoExplain}}
	res, err := newTestPipeline(src, false).Drain()
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if len(res.LogEvents) != 1 {
		t.Fatalf("got %d log events, want 1", len(res.LogEvents))
	}
	ev := res.LogEvents[0]
	if ev.GetEventType() != string(logparse.EventAutoExplainPlan) {
		t.Errorf("event_type = %q, want %q", ev.GetEventType(), logparse.EventAutoExplainPlan)
	}
	if ev.GetSeverity() != "LOG" {
		t.Errorf("severity = %q, want LOG", ev.GetSeverity())
	}
	if ev.GetPid() != 123 {
		t.Errorf("pid = %d, want 123", ev.GetPid())
	}
	if len(res.QueryPlans) != 1 {
		t.Fatalf("got %d query plans, want 1", len(res.QueryPlans))
	}
	qp := res.QueryPlans[0]
	if qp.GetRoot().GetNodeType() != "Seq Scan" {
		t.Errorf("root node type = %q, want Seq Scan", qp.GetRoot().GetNodeType())
	}
	if qp.GetFingerprint() == "" {
		t.Error("expected a non-empty fingerprint from the plan's Query Text")
	}
	cond := qp.GetRoot().GetNormalizedCondition()
	if strings.Contains(cond, pipelineCanary) || strings.Contains(cond, "'") {
		t.Errorf("literal leaked into normalized_condition: %q", cond)
	}
}

func TestDrain_logEventBytesNeverCarryCanary(t *testing.T) {
	src := &fakeSource{chunks: []string{stderrAutoExplain}}
	res, err := newTestPipeline(src, false).Drain()
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	for _, ev := range res.LogEvents {
		b, err := proto.Marshal(ev)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if bytes.Contains(b, []byte(pipelineCanary)) {
			t.Fatal("LOG EVENT LEAKED CANARY: the raw payload reached a T1 wire field")
		}
	}
}

func TestDrain_emptyChunkReturnsNothing(t *testing.T) {
	src := &fakeSource{chunks: nil}
	res, err := newTestPipeline(src, false).Drain()
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if len(res.LogEvents) != 0 || len(res.QueryPlans) != 0 || len(res.Insights) != 0 {
		t.Fatalf("empty chunk should yield nothing, got %+v", res)
	}
}

func TestDrain_detectLocallyPopulatesInsights(t *testing.T) {
	// A Seq Scan with actuals that trip DefaultSlowScan (>=1000 scanned, <=10%
	// returned): plan_rows large, actual rows tiny, rows removed by filter huge.
	slowScan := "2026-06-08 12:00:00.000 UTC [123] LOG:  duration: 5.0 ms  plan:\n" +
		"\t[{\"Query Text\":\"SELECT id FROM orders WHERE total > 500\"," +
		"\"Plan\":{\"Node Type\":\"Seq Scan\",\"Relation Name\":\"orders\"," +
		"\"Actual Rows\":1,\"Actual Loops\":1,\"Rows Removed by Filter\":9999," +
		"\"Total Cost\":96.5,\"Plan Rows\":1,\"Filter\":\"(total > 500)\"}}]\n"
	src := &fakeSource{chunks: []string{slowScan}}
	res, err := newTestPipeline(src, true).Drain()
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if len(res.Insights) == 0 {
		t.Fatal("detectLocally=true should have produced a slow_scan insight")
	}
	for _, in := range res.Insights {
		if strings.Contains(in.Detail, "500") && strings.Contains(in.Detail, "WHERE") {
			t.Fatalf("insight Detail leaked a predicate literal: %q", in.Detail)
		}
	}
}
