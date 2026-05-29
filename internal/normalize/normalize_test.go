// Tests for the query normalization + classification package.
//
// These are property-style tests: they assert the privacy guarantee
// (no literal value survives normalization) rather than exact strings,
// so they don't break if the upstream parser refines its whitespace.
package normalize

import (
	"strings"
	"testing"
)

func TestNormalizeStripsLiteralsAndClassifiesNormalized(t *testing.T) {
	cases := []struct {
		name         string
		query        string
		mustContain  []string // placeholders that must appear
		mustNotHave  []string // literal substrings that must NOT appear
		wantTier     Tier
	}{
		{
			name:        "string literal in WHERE",
			query:       "SELECT * FROM users WHERE email = 'alice@example.com'",
			mustContain: []string{"$1"},
			mustNotHave: []string{"alice", "example.com", "'alice"},
			wantTier:    TierNormalized,
		},
		{
			name:        "mixed numeric + string in INSERT",
			query:       "INSERT INTO t (a, b) VALUES (42, 'secret')",
			mustContain: []string{"$1", "$2"},
			mustNotHave: []string{"42", "secret", "'secret"},
			wantTier:    TierNormalized,
		},
		{
			name:        "IN list",
			query:       "SELECT 1 FROM accounts WHERE id IN (10, 20, 30)",
			mustContain: []string{"$1", "$2", "$3"},
			mustNotHave: []string{"10", "20", "30"},
			wantTier:    TierNormalized,
		},
		{
			name:        "LIKE with literal pattern",
			query:       "SELECT * FROM logs WHERE msg LIKE '%password%'",
			mustContain: []string{"$1"},
			mustNotHave: []string{"password", "%password%"},
			wantTier:    TierNormalized,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, tier := Normalize(tc.query)
			if tier != tc.wantTier {
				t.Fatalf("tier = %v, want %v (normalized = %q)", tier, tc.wantTier, got)
			}
			for _, want := range tc.mustContain {
				if !strings.Contains(got, want) {
					t.Errorf("normalized %q is missing placeholder %q", got, want)
				}
			}
			for _, forbidden := range tc.mustNotHave {
				if strings.Contains(got, forbidden) {
					t.Errorf("LITERAL LEAK: normalized %q contains forbidden substring %q", got, forbidden)
				}
			}
		})
	}
}

func TestUnparseableQueryIsBlocked(t *testing.T) {
	cases := []string{
		"this is not sql at all",
		"';DROP TABLE--",
		"",
	}
	for _, q := range cases {
		t.Run(q, func(t *testing.T) {
			got, tier := Normalize(q)
			if tier != TierBlocked {
				t.Fatalf("unparseable input %q must be TierBlocked, got tier=%v normalized=%q", q, tier, got)
			}
			if got != "" {
				t.Fatalf("TierBlocked must return empty normalized text; got %q", got)
			}
		})
	}
}

func TestFingerprintIsStableAndDistinguishes(t *testing.T) {
	a1, err := Fingerprint("SELECT * FROM users WHERE id = 1")
	if err != nil {
		t.Fatal(err)
	}
	a2, err := Fingerprint("SELECT * FROM users WHERE id = 99")
	if err != nil {
		t.Fatal(err)
	}
	if a1 != a2 {
		t.Errorf("queries differing only in literal values must have the same fingerprint: %q vs %q", a1, a2)
	}

	b, err := Fingerprint("SELECT * FROM accounts WHERE id = 1")
	if err != nil {
		t.Fatal(err)
	}
	if a1 == b {
		t.Errorf("queries against different tables must have different fingerprints; both are %q", a1)
	}

	if a1 == "" {
		t.Error("fingerprint must be non-empty for a valid query")
	}
}
