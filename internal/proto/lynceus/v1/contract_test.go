// Package lynceusv1_test enforces the Lynceus privacy contract at the
// schema level. T1 messages must contain only normalized, literal-free
// fields. If a future change adds a field that could carry a literal
// value (e.g. "raw_text", "query_sample", "parameter_value"), the test
// fails before the change can merge.
package lynceusv1_test

import (
	"testing"

	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
)

// TestQueryStatHasOnlyNormalizedFields enforces the T1 privacy guarantee:
// the QueryStat message schema is the source of truth for what may travel
// from collector to server, and every field must be a fingerprint, a
// normalized identifier, or an aggregate metric. No field name is
// permitted that could carry a per-execution literal value.
func TestQueryStatHasOnlyNormalizedFields(t *testing.T) {
	allowed := map[string]struct{}{
		"fingerprint":      {},
		"normalized_query": {},
		"calls":            {},
		"total_time_ms":    {},
		"mean_time_ms":     {},
		"rows":             {},
		"shared_blks_hit":  {},
		"shared_blks_read": {},
	}

	fields := (&lynceusv1.QueryStat{}).ProtoReflect().Descriptor().Fields()
	for i := 0; i < fields.Len(); i++ {
		name := string(fields.Get(i).Name())
		if _, ok := allowed[name]; !ok {
			t.Fatalf(
				"unexpected field %q in T1 QueryStat — possible literal leak. "+
					"T1 messages must contain only normalized fields. If you need to "+
					"carry literal-bearing data, define a separate T2 message type and "+
					"gate it behind RBAC + audit (see docs/specs/2026-05-29-lynceus-design.md §2).",
				name,
			)
		}
	}
}

// TestNormalizedQueryFieldShape sanity-checks that normalized_query is a
// scalar string, not bytes or a nested message. This guards against a
// subtle regression where someone changes its type to `bytes` or to a
// message that could embed arbitrary content.
func TestNormalizedQueryFieldShape(t *testing.T) {
	fields := (&lynceusv1.QueryStat{}).ProtoReflect().Descriptor().Fields()
	f := fields.ByName("normalized_query")
	if f == nil {
		t.Fatal("normalized_query field missing from T1 QueryStat")
	}
	if got := f.Kind().String(); got != "string" {
		t.Fatalf("normalized_query must be string kind, got %s", got)
	}
}
