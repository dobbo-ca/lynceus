// Pure unit tests (no Postgres) for the literal-free ReasonCode privacy
// guardrail. These enforce, at build time, that the cross-boundary field
// of caps.Status is a closed code — never free text capable of echoing a
// monitored-DB literal. See docs/superpowers/plans/2026-06-23-ly-44f-caps-reason-code.md.
package caps_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/caps"
)

// TestStatusFieldAllowlist is the discovery-payload contract test: it pins
// the exact field set of caps.Status, the struct the future discovery
// write-back serializes. Every field must be either the boolean, the closed
// ReasonCode, or the collector-local Detail string. Adding a new free-text
// field that becomes the cross-boundary field, or reverting Reason to a
// plain string, fails this test before it can merge — the equivalent of the
// proto T1 reflection allowlist (contract_test.go assertOnlyAllowed) for the
// Go-struct (not-yet-proto) discovery boundary.
func TestStatusFieldAllowlist(t *testing.T) {
	type spec struct {
		kind     reflect.Kind
		typeName string // for the ReasonCode named type
	}
	allowed := map[string]spec{
		"Available": {kind: reflect.Bool},
		"Reason":    {kind: reflect.String, typeName: "ReasonCode"},
		"Detail":    {kind: reflect.String, typeName: "string"},
	}

	st := reflect.TypeOf(caps.Status{})
	for i := 0; i < st.NumField(); i++ {
		f := st.Field(i)
		want, ok := allowed[f.Name]
		if !ok {
			t.Fatalf("unexpected field %q on caps.Status — possible literal leak. "+
				"The cross-boundary discovery field must be a closed ReasonCode, "+
				"not free text. If you need human-readable diagnostics, put them in "+
				"the collector-local Detail field (never crosses the wire).", f.Name)
		}
		if f.Type.Kind() != want.kind {
			t.Errorf("Status.%s kind = %s, want %s", f.Name, f.Type.Kind(), want.kind)
		}
		if want.typeName != "" && f.Type.Name() != want.typeName {
			t.Errorf("Status.%s type = %s, want %s", f.Name, f.Type.Name(), want.typeName)
		}
	}
}

// TestErrToCode_stripsPoisonedLiteral is the privacy leak-detection test: it
// feeds a poisoned driver error — one carrying a sentinel that stands in for
// statement text / identifiers / constraint bodies a real pgx error can echo
// from the monitored database — through caps.ErrToCode, the single enforced
// error-to-code funnel every probe error site routes through, and proves the
// emitted code (and the entire closed vocabulary) carries NONE of the poisoned
// substring. This is the test that directly verifies raw pgx error text cannot
// reach the cross-boundary Status.Reason field. PURE: no Postgres — it exercises
// the string-mapping layer, where the leak fix lives, not a live probe.
func TestErrToCode_stripsPoisonedLiteral(t *testing.T) {
	const poison = "SECRET_LITERAL_email='alice@example.com'"
	poisoned := errors.New("pq: duplicate key value violates unique constraint, detail: " + poison)

	got := caps.ErrToCode(poisoned)

	if got != caps.ReasonProbeError {
		t.Errorf("ErrToCode(poisoned) = %q, want %q", got, caps.ReasonProbeError)
	}
	if strings.Contains(string(got), poison) {
		t.Errorf("emitted ReasonCode %q leaked the poisoned literal %q", got, poison)
	}
	// Belt and braces: no code in the closed vocabulary may ever carry the
	// poisoned substring, so even a future mis-mapping cannot smuggle it across.
	for _, c := range caps.AllReasonCodes() {
		if strings.Contains(string(c), poison) {
			t.Errorf("vocabulary code %q contains the poisoned literal %q", c, poison)
		}
	}
}

// TestAllReasonCodesIsClosedVocab asserts the vocabulary is enumerable,
// non-empty, and free of duplicates — the property the contract test relies
// on to iterate the closed set.
func TestAllReasonCodesIsClosedVocab(t *testing.T) {
	all := caps.AllReasonCodes()
	if len(all) == 0 {
		t.Fatal("AllReasonCodes() is empty; expected a closed vocabulary")
	}
	seen := map[caps.ReasonCode]bool{}
	for _, c := range all {
		if c == "" {
			t.Error("AllReasonCodes() contains the empty code")
		}
		if seen[c] {
			t.Errorf("AllReasonCodes() contains duplicate %q", c)
		}
		seen[c] = true
	}
}
