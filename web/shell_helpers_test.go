package web

import (
	"testing"

	"github.com/dobbo-ca/lynceus/internal/scope"
)

func TestParseRange(t *testing.T) {
	cases := map[string]string{
		"15m": "15M", "1H": "1H", "24h": "24H", "7D": "7D", "30D": "30D",
		"": DefaultRange, "bogus": DefaultRange,
	}
	for in, want := range cases {
		if got := ParseRange(in); got != want {
			t.Errorf("ParseRange(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestScopeHref(t *testing.T) {
	if got := ScopeHref(scope.Scope{Kind: scope.Fleet}); string(got) != "/fleet" {
		t.Errorf("fleet href = %q, want /fleet", got)
	}
	got := string(ScopeHref(scope.Scope{Kind: scope.Cluster, ClusterID: "c-1"}))
	if got != "/fleet?scope=cluster%3Ac-1" {
		t.Errorf("cluster href = %q, want /fleet?scope=cluster%%3Ac-1", got)
	}
}

func TestRangeOptions_selectedAndScopePreserved(t *testing.T) {
	sc := scope.Scope{Kind: scope.Cluster, ClusterID: "c-1"}
	opts := RangeOptions("1h", sc)
	if len(opts) != len(ValidRanges) {
		t.Fatalf("got %d options, want %d", len(opts), len(ValidRanges))
	}
	var sel int
	for _, o := range opts {
		if o.Selected {
			sel++
			if o.Label != "1H" {
				t.Errorf("selected label = %q, want 1H", o.Label)
			}
		}
		if !containsSubstr(string(o.Href), "scope=cluster%3Ac-1") {
			t.Errorf("href %q dropped the active scope", o.Href)
		}
	}
	if sel != 1 {
		t.Errorf("selected count = %d, want 1", sel)
	}
}

func TestRangeOptions_fleetHasNoScopeParam(t *testing.T) {
	for _, o := range RangeOptions("24H", scope.Scope{Kind: scope.Fleet}) {
		if containsSubstr(string(o.Href), "scope=") {
			t.Errorf("fleet range href %q must not carry a scope param", o.Href)
		}
	}
}

func containsSubstr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
