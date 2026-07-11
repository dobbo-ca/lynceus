package api

import (
	"testing"

	"github.com/dobbo-ca/lynceus/web"
)

func TestFilterInsights_BySeverityAndKind(t *testing.T) {
	s := &Server{} // filterInsights is pure — no stats/conf touched
	rows := []web.InsightRow{
		{Kind: "slow_scan", Severity: "high", Fingerprint: "a"},
		{Kind: "disk_sort", Severity: "low", Fingerprint: "b"},
		{Kind: "slow_scan", Severity: "low", Fingerprint: "c"},
	}
	// Severity filter uses the mapped class: "crit" keeps high.
	if got := s.filterInsights(rows, web.InsightFilter{Sev: "crit"}); len(got) != 1 || got[0].Fingerprint != "a" {
		t.Errorf("crit sev filter wrong: %d rows", len(got))
	}
	if got := s.filterInsights(rows, web.InsightFilter{Kind: "slow_scan"}); len(got) != 2 {
		t.Errorf("kind filter wrong: %d rows", len(got))
	}
}
