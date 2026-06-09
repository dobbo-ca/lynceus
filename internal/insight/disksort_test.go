package insight_test

import (
	"strings"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/insight"
)

func TestDiskSort_external_flagged(t *testing.T) {
	got := insight.DetectAll(planFromFixture(t, "disksort_external.json"))
	var ds *insight.Insight
	for i := range got {
		if got[i].Kind == insight.KindDiskSort {
			ds = &got[i]
		}
	}
	if ds == nil {
		t.Fatalf("no disk_sort insight: %+v", got)
	}
	if ds.Severity != insight.SeverityLow { // 24000 kB < 32 MB
		t.Errorf("severity = %q, want low", ds.Severity)
	}
	if !strings.Contains(ds.Detail, "work_mem") {
		t.Errorf("detail missing work_mem hint: %q", ds.Detail)
	}
	for _, banned := range []string{"'", "=", "::"} {
		if strings.Contains(ds.Detail, banned) {
			t.Errorf("possible literal %q in detail: %q", banned, ds.Detail)
		}
	}
}

func TestDiskSort_inMemory_notFlagged(t *testing.T) {
	for _, in := range insight.DetectAll(planFromFixture(t, "sort_inmemory.json")) {
		if in.Kind == insight.KindDiskSort {
			t.Errorf("in-memory sort flagged: %+v", in)
		}
	}
}
