package web

import (
	"context"
	"strings"
	"testing"
)

func TestChecksScreen_ColumnsSummaryStripeAndExpand(t *testing.T) {
	vm := ChecksVM{
		Summary: "2 FIRING · VACUUM / SETTINGS",
		Rows: []ChecksRow{
			{Severity: "high", Category: "vacuum", CheckID: "vacuum.wraparound", Object: "public.orders",
				Detail: "xid age high", ServerID: "srv-orders-primary", FirstSeen: "3h ago", Expanded: true,
				History: []HistCell{{ColorVar: "var(--crit)", Title: "-24h"}}},
			{Severity: "low", Category: "settings", CheckID: "settings.work_mem", Object: "cluster",
				Detail: "low work_mem", ServerID: "srv-orders-primary", FirstSeen: "1d ago", Muted: true},
		},
	}
	var sb strings.Builder
	_ = ChecksScreen(vm).Render(context.Background(), &sb)
	html := sb.String()
	for _, want := range []string{
		"Checks", "badge--live", "2 FIRING · VACUUM / SETTINGS",
		"SEVERITY", "CHECK", "OBJECT", "DETAIL", "SERVER", "FIRST SEEN",
		"vacuum.wraparound", "srv-orders-primary", "3h ago", "stripe-crit",
		"HISTORY · LAST 24H", "MUTE", "MUTED",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("ChecksScreen missing %q", want)
		}
	}
}
