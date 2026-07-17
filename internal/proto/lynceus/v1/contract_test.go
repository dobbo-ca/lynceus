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
		"bucket_start_unix": {},
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

// assertOnlyAllowed fails if any field in fields is not present in allowed.
// Used by the T1 privacy contract tests to enforce a field allowlist on a
// wire message: adding a field that could carry a literal becomes a build
// failure until the allowlist is deliberately updated.
func assertOnlyAllowed(t *testing.T, fields protoreflect.FieldDescriptors, allowed map[string]struct{}, msgName string) {
	t.Helper()
	for i := 0; i < fields.Len(); i++ {
		name := string(fields.Get(i).Name())
		if _, ok := allowed[name]; !ok {
			t.Fatalf(
				"unexpected field %q in T1 %s — possible literal leak. T1 messages "+
					"must contain only normalized/aggregate fields. If you need to carry "+
					"literal-bearing data, define a separate T2 message type and gate it "+
					"behind RBAC + audit (see docs/specs/2026-05-29-lynceus-design.md §2).",
				name, msgName,
			)
		}
	}
}

// TestQueryPlanHasNoLiteralFields enforces the T1 guarantee for extracted
// EXPLAIN plans. QueryPlan/PlanNode may carry only structural plan metadata
// (node type, relation/index name, cost/row/time estimates and actuals) and
// NORMALIZED condition strings — never a raw Filter/Output expression or any
// literal value from the monitored database. auto_explain plans are derived
// from real executions and the source plan body (in the collector-local T2
// LogPayload) is full of literals; this allowlist makes it impossible to
// silently ship one on the wire.
func TestQueryPlanHasNoLiteralFields(t *testing.T) {
	planAllowed := map[string]struct{}{
		"fingerprint": {}, "captured_at_unix": {}, "format_version": {},
		"total_cost": {}, "actual_total_time_ms": {}, "root": {},
	}
	assertOnlyAllowed(t, (&lynceusv1.QueryPlan{}).ProtoReflect().Descriptor().Fields(), planAllowed, "QueryPlan")

	nodeAllowed := map[string]struct{}{
		"node_type": {}, "relation_name": {}, "index_name": {}, "alias": {},
		"join_type": {}, "scan_direction": {},
		"startup_cost": {}, "total_cost": {}, "plan_rows": {}, "plan_width": {},
		"actual_startup_time_ms": {}, "actual_total_time_ms": {},
		"actual_rows": {}, "actual_loops": {},
		"rows_removed_by_filter": {},
		"sort_method":            {}, "sort_space_type": {}, "sort_space_used_kb": {},
		"hash_batches": {}, "original_hash_batches": {}, "peak_memory_usage_kb": {},
		"normalized_condition": {},
		"plans":                {}, // recursive children
	}
	assertOnlyAllowed(t, (&lynceusv1.PlanNode{}).ProtoReflect().Descriptor().Fields(), nodeAllowed, "PlanNode")
}

// TestPlanNodeConditionFieldIsScalarString sanity-checks that
// normalized_condition stayed a plain string. Guards against a refactor that
// swaps it for bytes or a nested message able to embed unstructured content.
func TestPlanNodeConditionFieldIsScalarString(t *testing.T) {
	fields := (&lynceusv1.PlanNode{}).ProtoReflect().Descriptor().Fields()
	f := fields.ByName("normalized_condition")
	if f == nil {
		t.Fatal("normalized_condition field missing from PlanNode")
	}
	if got := f.Kind().String(); got != "string" {
		t.Fatalf("normalized_condition must be string kind, got %s", got)
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

// TestTableStatHasOnlyAggregateFields enforces the T1 privacy guarantee for
// the per-table size/growth message. TableStat must carry only catalog
// identifiers (schema/name/fqn), size byte-counters, aggregate row/tuple
// counts, vacuum/analyze counters, and unix timestamps — never a column
// value, default expression, constraint body, comment, ACL, MCV value, or
// histogram bound. It is the same privacy class as ActivityBucket.
func TestTableStatHasOnlyAggregateFields(t *testing.T) {
	allowed := map[string]struct{}{
		"schema": {}, "name": {}, "fqn": {},
		"total_bytes": {}, "heap_bytes": {}, "toast_bytes": {}, "indexes_bytes": {},
		"row_estimate": {}, "live_tuples": {}, "dead_tuples": {}, "n_mod_since_analyze": {},
		"seq_scan": {}, "idx_scan": {},
		"n_tup_ins": {}, "n_tup_upd": {}, "n_tup_del": {}, "n_tup_hot_upd": {},
		"last_vacuum_unix": {}, "last_autovacuum_unix": {},
		"last_analyze_unix": {}, "last_autoanalyze_unix": {},
		"vacuum_count": {}, "autovacuum_count": {},
	}
	assertOnlyAllowed(t, (&lynceusv1.TableStat{}).ProtoReflect().Descriptor().Fields(), allowed, "TableStat")
}

// TestTableStatScalarFieldShapes guards against a refactor that swaps an
// identifier string for bytes or a nested message able to embed unstructured
// content. Only schema/name/fqn are strings; every other field is a numeric.
func TestTableStatScalarFieldShapes(t *testing.T) {
	fields := (&lynceusv1.TableStat{}).ProtoReflect().Descriptor().Fields()
	for _, fn := range []string{"schema", "name", "fqn"} {
		f := fields.ByName(protoreflect.Name(fn))
		if f == nil {
			t.Fatalf("field %q missing from TableStat", fn)
		}
		if got := f.Kind().String(); got != "string" {
			t.Fatalf("TableStat.%s must be string kind, got %s", fn, got)
		}
	}
}

// TestFreezeAgeHasOnlyAggregateFields enforces the T1 privacy guarantee for
// the freeze-age message (ly-u4t.26). FreezeAge must carry only catalog
// identifiers (scope/schema/name/fqn) and non-negative AGE counts — never a
// raw xid, column value, or any per-execution literal. Transaction-id /
// MultiXact ages are integer distances, not data; this allowlist makes it
// impossible to silently add a literal-bearing field on the wire.
func TestFreezeAgeHasOnlyAggregateFields(t *testing.T) {
	allowed := map[string]struct{}{
		"scope": {}, "schema": {}, "name": {}, "fqn": {},
		"xid_age": {}, "mxid_age": {}, "autovacuum_freeze_max_age": {},
	}
	assertOnlyAllowed(t, (&lynceusv1.FreezeAge{}).ProtoReflect().Descriptor().Fields(), allowed, "FreezeAge")
}

// TestXminHorizonHasOnlyAggregateFields enforces the T1 privacy guarantee for
// the xmin-horizon message. XminHorizon must carry only a non-negative age
// COUNT (age of the oldest backend_xmin / replication-slot xmin / prepared-xact
// xid) and a fixed-vocabulary holder_kind label — never a replication-slot
// name, prepared-xact gid, query text, or any per-execution literal from the
// monitored database. Same privacy class as FreezeAge (counts + a bounded
// label). holder_kind is checked to be a scalar string per the ActivityBucket /
// ConnectionSample convention.
func TestXminHorizonHasOnlyAggregateFields(t *testing.T) {
	allowed := map[string]struct{}{
		"oldest_xmin_age": {}, "holder_kind": {},
	}
	assertOnlyAllowed(t, (&lynceusv1.XminHorizon{}).ProtoReflect().Descriptor().Fields(), allowed, "XminHorizon")

	f := (&lynceusv1.XminHorizon{}).ProtoReflect().Descriptor().Fields().ByName("holder_kind")
	if f == nil {
		t.Fatal("holder_kind field missing from XminHorizon")
	}
	if got := f.Kind().String(); got != "string" {
		t.Fatalf("XminHorizon.holder_kind must be string kind, got %s", got)
	}
}

// TestConnectionSampleHasOnlyAggregateFields enforces the T1 privacy guarantee
// for per-backend connection observations. ConnectionSample must carry only the
// backend pid, a fixed state/wait label, and integer durations — never the
// pg_stat_activity `query` column or any literal value.
func TestConnectionSampleHasOnlyAggregateFields(t *testing.T) {
	allowed := map[string]struct{}{
		"server_id": {}, "observed_at_unix": {}, "pid": {}, "state": {},
		"active_seconds": {}, "xact_seconds": {}, "state_seconds": {},
		"wait_event_type": {},
	}
	assertOnlyAllowed(t, (&lynceusv1.ConnectionSample{}).ProtoReflect().Descriptor().Fields(), allowed, "ConnectionSample")

	for _, name := range []string{"state", "wait_event_type"} {
		f := (&lynceusv1.ConnectionSample{}).ProtoReflect().Descriptor().Fields().ByName(protoreflect.Name(name))
		if f == nil {
			t.Fatalf("field %q missing from ConnectionSample", name)
		}
		if got := f.Kind().String(); got != "string" {
			t.Fatalf("ConnectionSample.%s must be string kind, got %s", name, got)
		}
	}
}

// TestBlockingEdgeHasOnlyPidFields enforces the T1 privacy guarantee for the
// blocking relationship message: pids and a wait duration only.
func TestBlockingEdgeHasOnlyPidFields(t *testing.T) {
	allowed := map[string]struct{}{
		"server_id": {}, "observed_at_unix": {},
		"blocked_pid": {}, "blocker_pid": {}, "blocked_wait_seconds": {},
	}
	assertOnlyAllowed(t, (&lynceusv1.BlockingEdge{}).ProtoReflect().Descriptor().Fields(), allowed, "BlockingEdge")
}

// TestSnapshotCarriesLogEvents enforces that the Snapshot envelope grows only
// by adding allowlisted, literal-free repeated message fields. log_events (8)
// carries lynceus.v1.LogEvent elements — themselves contract-tested above. The
// allowlist makes it impossible to silently add a raw-text-bearing field
// (e.g. log_payloads) to the wire envelope.
func TestSnapshotCarriesLogEvents(t *testing.T) {
	allowed := map[string]struct{}{
		"server_id":          {},
		"collected_at_unix":  {},
		"query_stats":        {},
		"activity_buckets":   {},
		"query_plans":        {},
		"log_events":         {},
		"schema_objects":     {},
		"table_stats":        {},
		"freeze_ages":        {},
		"connection_samples": {},
		"blocking_edges":     {},
		"index_stats":        {},
		"xmin_horizons":      {},
		"settings":           {},
		"query_stat_raws":    {}, // ly-cwr.5: opt-in T2 raw payload (gated, literal-bearing) — deliberately allowed
	}
	assertOnlyAllowed(t, (&lynceusv1.Snapshot{}).ProtoReflect().Descriptor().Fields(), allowed, "Snapshot")

	f := (&lynceusv1.Snapshot{}).ProtoReflect().Descriptor().Fields().ByName("log_events")
	if f == nil {
		t.Fatal("log_events field missing from Snapshot")
	}
	if !f.IsList() {
		t.Fatal("log_events must be a repeated field")
	}
	if got := string(f.Message().Name()); got != "LogEvent" {
		t.Fatalf("log_events element must be LogEvent, got %s", got)
	}
}

// TestSnapshotCarriesTableStats verifies the table_stats field exists on the
// Snapshot wrapper and is a repeated TableStat — so ly-xqf.6 can ship rows.
func TestSnapshotCarriesTableStats(t *testing.T) {
	fields := (&lynceusv1.Snapshot{}).ProtoReflect().Descriptor().Fields()
	f := fields.ByName("table_stats")
	if f == nil {
		t.Fatal("table_stats field missing from Snapshot")
	}
	if f.Number() != 7 {
		t.Fatalf("table_stats field number = %d, want 7 (reserved)", f.Number())
	}
	if got := f.Message(); got == nil || got.Name() != "TableStat" {
		t.Fatalf("table_stats must be repeated TableStat, got %v", got)
	}
}

// TestIndexStatHasOnlyAggregateFields enforces the T1 privacy guarantee for
// the per-index message (ly-u4t.23). IndexStat must carry only catalog
// identifiers (schema/name/fqn/table_fqn), a scan COUNTER, a size byte-count,
// and structural catalog booleans — never the index expression
// (pg_get_indexdef) or a partial-index predicate (pg_index.indpred), both of
// which can embed literal values from the monitored database. Those belong in
// a separate T2 message gated behind RBAC + audit.
func TestIndexStatHasOnlyAggregateFields(t *testing.T) {
	allowed := map[string]struct{}{
		"schema": {}, "name": {}, "fqn": {}, "table_fqn": {},
		"idx_scan": {}, "size_bytes": {},
		"is_valid": {}, "is_ready": {}, "is_unique": {}, "is_primary": {},
	}
	assertOnlyAllowed(t, (&lynceusv1.IndexStat{}).ProtoReflect().Descriptor().Fields(), allowed, "IndexStat")
}

// TestIndexStatScalarFieldShapes guards against a refactor that swaps an
// identifier string for bytes or a nested message able to embed unstructured
// content. Only the four identifiers are strings.
func TestIndexStatScalarFieldShapes(t *testing.T) {
	fields := (&lynceusv1.IndexStat{}).ProtoReflect().Descriptor().Fields()
	for _, fn := range []string{"schema", "name", "fqn", "table_fqn"} {
		f := fields.ByName(protoreflect.Name(fn))
		if f == nil {
			t.Fatalf("field %q missing from IndexStat", fn)
		}
		if got := f.Kind().String(); got != "string" {
			t.Fatalf("IndexStat.%s must be string kind, got %s", fn, got)
		}
	}
}

// TestSnapshotCarriesIndexStats verifies the index_stats field exists on the
// Snapshot wrapper as a repeated IndexStat at field number 12.
func TestSnapshotCarriesIndexStats(t *testing.T) {
	fields := (&lynceusv1.Snapshot{}).ProtoReflect().Descriptor().Fields()
	f := fields.ByName("index_stats")
	if f == nil {
		t.Fatal("index_stats field missing from Snapshot")
	}
	if f.Number() != 12 {
		t.Fatalf("index_stats field number = %d, want 12", f.Number())
	}
	if got := f.Message(); got == nil || got.Name() != "IndexStat" {
		t.Fatalf("index_stats must be repeated IndexStat, got %v", got)
	}
}

// TestSettingHasOnlyConfigFields enforces the T1 privacy guarantee for the
// pg_settings tuning-config message (ly-u4t.24 / ly-u4t.18). Setting must
// carry only a GUC name, its value, unit, source, and pending_restart — the
// fixed shape the config checks and advisor need.
//
// UNLIKE the other T1 messages, the `value` field is deliberately a bounded
// string that is *literal-CAPABLE*: some GUCs (log_line_prefix, search_path,
// archive_command, *_file paths, primary_conninfo) carry free-form text that
// would leak infra detail / credentials. The real redaction boundary is NOT
// this proto — it is the collector SettingsReader's curated allowlist selected
// by name (`WHERE name = ANY(allowlist)`), which only ever ships GUCs whose
// value is a number, a bool, or a bounded planner/durability enum keyword.
// This allowlist pins the field SET so nobody can add a raw_config_line /
// short_desc field or otherwise widen the message beyond {name,value,unit,
// source,pending_restart}.
func TestSettingHasOnlyConfigFields(t *testing.T) {
	allowed := map[string]struct{}{
		"name": {}, "value": {}, "unit": {}, "source": {}, "pending_restart": {},
	}
	assertOnlyAllowed(t, (&lynceusv1.Setting{}).ProtoReflect().Descriptor().Fields(), allowed, "Setting")
}

// TestSettingScalarFieldShapes guards against a bytes/nested-message swap that
// could smuggle unstructured content: name/value/unit/source must stay plain
// strings.
func TestSettingScalarFieldShapes(t *testing.T) {
	fields := (&lynceusv1.Setting{}).ProtoReflect().Descriptor().Fields()
	for _, fn := range []string{"name", "value", "unit", "source"} {
		f := fields.ByName(protoreflect.Name(fn))
		if f == nil {
			t.Fatalf("field %q missing from Setting", fn)
		}
		if got := f.Kind().String(); got != "string" {
			t.Fatalf("Setting.%s must be string kind, got %s", fn, got)
		}
	}
}

// TestSnapshotCarriesSettings verifies the settings field exists on the
// Snapshot wrapper as a repeated Setting at field number 14 (renumbered from
// 13 to clear #35's xmin_horizons, which took 13 first).
func TestSnapshotCarriesSettings(t *testing.T) {
	fields := (&lynceusv1.Snapshot{}).ProtoReflect().Descriptor().Fields()
	f := fields.ByName("settings")
	if f == nil {
		t.Fatal("settings field missing from Snapshot")
	}
	if f.Number() != 14 {
		t.Fatalf("settings field number = %d, want 14", f.Number())
	}
	if !f.IsList() {
		t.Fatal("settings must be a repeated field")
	}
	if got := f.Message(); got == nil || got.Name() != "Setting" {
		t.Fatalf("settings must be repeated Setting, got %v", got)
	}
}

// TestSnapshotCarriesQueryStatRaws verifies the opt-in T2 raw payload field
// exists on the Snapshot envelope as repeated QueryStatRaw at field 15.
func TestSnapshotCarriesQueryStatRaws(t *testing.T) {
	fields := (&lynceusv1.Snapshot{}).ProtoReflect().Descriptor().Fields()
	f := fields.ByName("query_stat_raws")
	if f == nil {
		t.Fatal("query_stat_raws field missing from Snapshot")
	}
	if f.Number() != 15 {
		t.Fatalf("query_stat_raws field number = %d, want 15", f.Number())
	}
	if !f.IsList() {
		t.Fatal("query_stat_raws must be a repeated field")
	}
	if got := f.Message(); got == nil || got.Name() != "QueryStatRaw" {
		t.Fatalf("query_stat_raws must be repeated QueryStatRaw, got %v", got)
	}
}

// TestQueryStatRawCarriesRawQuery documents that QueryStatRaw is the ONE T2
// message permitted a literal-bearing raw_query field, alongside the pg_query
// fingerprint + normalized_query (literal-free). It is shipped only when the
// query_text_t2 gate is on (servers.t2_enabled ∧ policy).
func TestQueryStatRawCarriesRawQuery(t *testing.T) {
	fields := (&lynceusv1.QueryStatRaw{}).ProtoReflect().Descriptor().Fields()
	if f := fields.ByName("raw_query"); f == nil || f.Kind().String() != "string" {
		t.Fatal("QueryStatRaw.raw_query must exist and be string kind")
	}
	for _, n := range []string{"fingerprint", "normalized_query"} {
		if fields.ByName(protoreflect.Name(n)) == nil {
			t.Fatalf("QueryStatRaw.%s missing", n)
		}
	}
}
