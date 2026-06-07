package planextract

import (
	"strings"
	"testing"
)

// TestNormalizeCondition_noLiteralSurvives is the core safety property: no
// matter the input, the output must never contain a quoted string literal or
// a recognizable literal value from the source condition. The exact
// placeholder form is secondary — fail-closed (empty) is always acceptable.
func TestNormalizeCondition_noLiteralSurvives(t *testing.T) {
	cases := []struct {
		name string
		in   string
		// banned substrings that would indicate a literal leaked
		banned []string
	}{
		{"string literal", "(status = 'shipped'::text)", []string{"shipped", "'"}},
		{"int array", "(id = ANY ('{1,2,3}'::integer[]))", []string{"{1,2,3}", "'"}},
		{"numeric compare", "(orders.total > 1000.50)", []string{"1000.50"}},
		{"multi literal", "(name = 'alice' AND age > 30)", []string{"alice", "'", "30"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := NormalizeCondition(c.in)
			for _, b := range c.banned {
				if strings.Contains(got, b) {
					t.Fatalf("literal leaked: %q -> %q (contains %q)", c.in, got, b)
				}
			}
		})
	}
}

// TestNormalizeCondition_normalizesSimple confirms a clean condition does
// produce a usable normalized predicate (not just an empty fail-closed
// result), so the field carries signal when it safely can.
func TestNormalizeCondition_normalizesSimple(t *testing.T) {
	got := NormalizeCondition("(status = 'shipped'::text)")
	if got == "" {
		t.Fatal("expected a normalized predicate for a simple condition, got empty")
	}
	if !strings.Contains(got, "status") {
		t.Fatalf("normalized predicate dropped the column name: %q", got)
	}
	if !strings.Contains(got, "$1") {
		t.Fatalf("expected a positional placeholder in %q", got)
	}
}

// TestNormalizeCondition_failsClosed returns empty for input the parser
// cannot understand, rather than risk leaking an unparsed string.
func TestNormalizeCondition_failsClosed(t *testing.T) {
	for _, in := range []string{"", "   ", "this is not sql !@#$"} {
		if got := NormalizeCondition(in); got != "" {
			t.Fatalf("expected empty (fail closed) for %q, got %q", in, got)
		}
	}
}
