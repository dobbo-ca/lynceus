package insight_test

import (
	"strings"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/insight"
)

func TestDiskSpill_flagged(t *testing.T) {
	got := insight.DetectAll(planFromFixture(t, "diskspill.json"))
	var ds *insight.Insight
	n := 0
	for i := range got {
		if got[i].Kind == insight.KindDiskSpill {
			ds = &got[i]
			n++
		}
	}
	if ds == nil {
		t.Fatalf("no disk_spill insight: %+v", got)
	}
	if n != 1 {
		t.Errorf("disk_spill fired %d times, want exactly 1 (query-level)", n)
	}
	if ds.Severity != insight.SeverityMedium { // 60000 kB spilled: >=32768, <262144
		t.Errorf("severity = %q, want medium", ds.Severity)
	}
	if ds.NodePath != "plan" {
		t.Errorf("node_path = %q, want plan", ds.NodePath)
	}
	if !strings.Contains(ds.Detail, "work_mem") {
		t.Errorf("detail missing work_mem hint: %q", ds.Detail)
	}
	if !strings.Contains(ds.Detail, "64 MB") { // next pow2 MB > 40000 kB largest node
		t.Errorf("detail missing recommended 64 MB: %q", ds.Detail)
	}
	for _, banned := range []string{"'", "=", "::"} {
		if strings.Contains(ds.Detail, banned) {
			t.Errorf("possible literal %q in detail: %q", banned, ds.Detail)
		}
	}
}

func TestDiskSpill_inMemory_notFlagged(t *testing.T) {
	for _, in := range insight.DetectAll(planFromFixture(t, "diskspill_inmemory.json")) {
		if in.Kind == insight.KindDiskSpill {
			t.Errorf("in-memory plan flagged: %+v", in)
		}
	}
}
