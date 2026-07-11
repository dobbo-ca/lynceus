package web

import "testing"

func TestHealthLine(t *testing.T) {
	cases := []struct {
		crit, warn, info    int
		wantText, wantClass string
	}{
		{1, 4, 0, "[DEGRADED] 1 CRIT · 4 WARN", "hl-crit"},
		{0, 2, 1, "[WARNING] 2 WARN", "hl-warn"},
		{0, 0, 3, "[HEALTHY] 3 INFO", "hl-info"},
		{0, 0, 0, "[HEALTHY] 0 OPEN", "hl-ok"},
	}
	for _, c := range cases {
		gotText, gotClass := HealthLine(c.crit, c.warn, c.info)
		if gotText != c.wantText || gotClass != c.wantClass {
			t.Errorf("HealthLine(%d,%d,%d) = %q/%q, want %q/%q",
				c.crit, c.warn, c.info, gotText, gotClass, c.wantText, c.wantClass)
		}
	}
}

func TestSevRankAndNextSort(t *testing.T) {
	if SevRank(1, 0, 0) != 2 || SevRank(0, 1, 9) != 1 || SevRank(0, 0, 5) != 0 {
		t.Fatal("SevRank ordering wrong")
	}
	if nextSort("health") != "name" || nextSort("name") != "health" || nextSort("") != "name" {
		t.Fatal("nextSort toggle wrong")
	}
}
