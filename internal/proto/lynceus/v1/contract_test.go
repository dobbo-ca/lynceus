// Package lynceusv1_test enforces the Lynceus privacy contract at the
// schema level. T1 messages must contain only normalized, literal-free
// fields. If a future change adds a field that could carry a literal
// value (e.g. "raw_text", "query_sample", "parameter_value"), the test
// fails before the change can merge.
package lynceusv1_test

import (
	"testing"

	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
	"google.golang.org/protobuf/reflect/protoreflect"
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

// TestLogEventHasOnlyClassificationFields enforces the T1 privacy guarantee
// for the log-insights pipeline: the LogEvent wire message must carry only
// classification metadata (event type, severity, timestamps, process info).
// It must NEVER carry the statement text, bind parameters, error detail,
// or any other field capable of holding a literal value from the monitored
// database. Sensitive payload travels in a separate T2 message (defined
// later) gated behind RBAC + audit.
func TestLogEventHasOnlyClassificationFields(t *testing.T) {
	allowed := map[string]struct{}{
		"event_type":       {},
		"severity":         {},
		"occurred_at_unix": {},
		"logged_at_unix":   {},
		"pid":              {},
		"backend_type":     {},
		"database_name":    {},
		"user_name":        {},
		"application_name": {},
		"client_addr_hash": {},
		"sql_state":        {},
		"session_line_num": {},
		"transaction_id":   {},
	}

	fields := (&lynceusv1.LogEvent{}).ProtoReflect().Descriptor().Fields()
	for i := 0; i < fields.Len(); i++ {
		name := string(fields.Get(i).Name())
		if _, ok := allowed[name]; !ok {
			t.Fatalf(
				"unexpected field %q in T1 LogEvent — possible literal leak. "+
					"T1 log events carry only classification metadata. Statement "+
					"text, bind params, error detail, hint, and the raw message "+
					"belong in a separate T2 LogPayload message gated behind "+
					"RBAC + audit (see docs/specs/2026-05-29-lynceus-design.md §2).",
				name,
			)
		}
	}
}

// TestActivityBucketHasOnlyAggregateFields enforces the T1 privacy guarantee for
// the connection-state histogram message. ActivityBucket must contain only
// labels (database name, state, wait_event_type, wait_event) and aggregate
// counts — never a query text, parameter value, or any per-execution literal.
// pg_stat_activity exposes a `query` column; the existence of this allowlist
// makes it impossible to silently add such a field on the wire.
func TestActivityBucketHasOnlyAggregateFields(t *testing.T) {
	allowed := map[string]struct{}{
		"server_id":         {},
		"database_name":     {},
		"state":             {},
		"wait_event_type":   {},
		"wait_event":        {},
		"bucket_start_unix":  {},
		"bucket_seconds":    {},
		"sample_count":      {},
		"count_sum":         {},
		"count_max":         {},
	}

	fields := (&lynceusv1.ActivityBucket{}).ProtoReflect().Descriptor().Fields()
	for i := 0; i < fields.Len(); i++ {
		name := string(fields.Get(i).Name())
		if _, ok := allowed[name]; !ok {
			t.Fatalf(
				"unexpected field %q in T1 ActivityBucket — possible literal leak. "+
					"pg_stat_activity exposes raw query text in its `query` column; "+
					"T1 must never carry it. If you need live query samples, define a "+
					"separate T2 message (see ly-xqf.4 connection traces) and gate it "+
					"behind RBAC + audit (docs/specs/2026-05-29-lynceus-design.md §2).",
				name,
			)
		}
	}
}

// TestActivityBucketStateIsScalarString sanity-checks that the state label
// stayed a plain string. Guards against a refactor that swaps it for `bytes`
// or a nested message that could embed arbitrary content.
func TestActivityBucketStateIsScalarString(t *testing.T) {
	fields := (&lynceusv1.ActivityBucket{}).ProtoReflect().Descriptor().Fields()
	for _, name := range []string{"state", "wait_event_type", "wait_event", "database_name"} {
		f := fields.ByName(protoreflect.Name(name))
		if f == nil {
			t.Fatalf("field %q missing from ActivityBucket", name)
		}
		if got := f.Kind().String(); got != "string" {
			t.Fatalf("ActivityBucket.%s must be string kind, got %s", name, got)
		}
	}
}

// TestLogEventScalarFieldShapes guards against type-changing regressions
// where a string field is replaced by bytes or a nested message that
// could embed unstructured content.
func TestLogEventScalarFieldShapes(t *testing.T) {
	fields := (&lynceusv1.LogEvent{}).ProtoReflect().Descriptor().Fields()
	wantString := []string{
		"event_type", "backend_type", "database_name", "user_name",
		"application_name", "client_addr_hash", "sql_state",
	}
	for _, fn := range wantString {
		f := fields.ByName(protoreflect.Name(fn))
		if f == nil {
			t.Fatalf("field %q missing from LogEvent", fn)
		}
		if got := f.Kind().String(); got != "string" {
			t.Fatalf("%s must be string kind, got %s", fn, got)
		}
	}
}
