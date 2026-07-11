package web

import (
	"context"
	"strings"
	"testing"
)

func TestWaitsScreen_LegendMixAndCaption(t *testing.T) {
	vm := WaitsVM{
		ScopeLabel: "SRV-ORDERS-PRIMARY",
		Legend:     []WaitLegend{{Key: "IO / DataFileRead", Avg: "12", ColorVar: "var(--chart-io)"}},
		Servers: []WaitServerMix{{
			Name: "srv-orders-primary", TopClass: "IO / DataFileRead", TopColorVar: "var(--chart-io)",
			Mix: []WaitMixSeg{{Key: "IO / DataFileRead", WidthPct: 60, ColorVar: "var(--chart-io)"}},
		}},
	}
	var sb strings.Builder
	_ = WaitsScreen(vm).Render(context.Background(), &sb)
	html := sb.String()
	for _, want := range []string{
		"Wait Events", "badge--live", "SAMPLED FROM PG_STAT_ACTIVITY · ON-CPU PRESERVED",
		"IO / DataFileRead", "avg 12", "var(--chart-io)", "srv-orders-primary",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("WaitsScreen missing %q", want)
		}
	}
}

func TestWaitsScreen_HistogramSoonStateWhenNoBuckets(t *testing.T) {
	var sb strings.Builder
	_ = WaitsView(WaitsVM{ScopeLabel: "srv-1"}).Render(context.Background(), &sb)
	if !strings.Contains(sb.String(), "ly-u4t.22") {
		t.Error("empty histogram should cite the bucketed-data bead")
	}
}
