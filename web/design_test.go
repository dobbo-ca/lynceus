package web

import "testing"

func TestSevClass(t *testing.T) {
	cases := map[string]string{
		"critical": "crit", "CRIT": "crit", "high": "crit", "error": "crit",
		"warning": "warn", "medium": "warn", "WARN": "warn",
		"info": "info", "low": "info", "notice": "info", "": "info", "weird": "info",
	}
	for in, want := range cases {
		if got := SevClass(in); got != want {
			t.Errorf("SevClass(%q)=%q want %q", in, got, want)
		}
	}
}

func TestSevLabel(t *testing.T) {
	if got := SevLabel("high"); got != "CRIT" {
		t.Errorf("SevLabel(high)=%q want CRIT", got)
	}
	if got := SevLabel("low"); got != "INFO" {
		t.Errorf("SevLabel(low)=%q want INFO", got)
	}
}

func TestMeanMs(t *testing.T) {
	if got := MeanMs(100, 0); got != 0 {
		t.Errorf("MeanMs div-by-zero guard: got %v want 0", got)
	}
	if got := MeanMs(100, 4); got != 25 {
		t.Errorf("MeanMs(100,4)=%v want 25", got)
	}
}

func TestRecommendationFor(t *testing.T) {
	if RecommendationFor("slow_scan") == "" {
		t.Error("slow_scan should have a recommendation")
	}
	if RecommendationFor("no_such_kind") != "" {
		t.Error("unknown kind should return empty string")
	}
}

func TestKindLabel(t *testing.T) {
	if got := KindLabel("slow_scan"); got != "SLOW SEQ SCAN" {
		t.Errorf("KindLabel(slow_scan)=%q want SLOW SEQ SCAN", got)
	}
	if got := KindLabel("mystery_kind"); got != "MYSTERY KIND" {
		t.Errorf("KindLabel fallback=%q want MYSTERY KIND", got)
	}
}
