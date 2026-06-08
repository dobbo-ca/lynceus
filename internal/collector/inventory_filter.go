package collector

import (
	"fmt"
	"regexp"
)

// SchemaFilter is the collector-boundary control for which Postgres
// schemas may be inventoried. Schema NAMES can be sensitive (the
// inventory is otherwise structural T1), so this filter runs before
// any SchemaObject proto value is constructed — there is no path from
// catalog row to wire that bypasses it.
//
// The SAME instance is shared by the ly-xqf.6 TableStatsReader so both
// catalog readers enforce an identical boundary; keep the API stable.
//
// Semantics:
//   - include == nil → allow any schema (default).
//   - include != nil → schema must MATCH to be considered.
//   - ignore  != nil → schema is REJECTED if it matches, even if the
//     include regex matches. Ignore wins.
//   - pg_catalog, information_schema, pg_toast, and any schema whose
//     name starts with "pg_temp_" / "pg_toast_temp_" are always rejected.
//
// Both regexps are Go RE2 (`regexp` package), evaluated anchorless —
// callers wishing to anchor must use ^ and $ explicitly. This matches
// how operators expect to write `ignore_schema_regexp` rules.
type SchemaFilter struct {
	include *regexp.Regexp
	ignore  *regexp.Regexp
}

// NewSchemaFilter compiles the two optional regexps. An empty string
// for either disables that side of the filter.
func NewSchemaFilter(includeRE, ignoreRE string) (*SchemaFilter, error) {
	f := &SchemaFilter{}
	if includeRE != "" {
		re, err := regexp.Compile(includeRE)
		if err != nil {
			return nil, fmt.Errorf("compile include_schema_regexp: %w", err)
		}
		f.include = re
	}
	if ignoreRE != "" {
		re, err := regexp.Compile(ignoreRE)
		if err != nil {
			return nil, fmt.Errorf("compile ignore_schema_regexp: %w", err)
		}
		f.ignore = re
	}
	return f, nil
}

// IsAllowed reports whether schema may be inventoried.
func (f *SchemaFilter) IsAllowed(schema string) bool {
	if isSystemSchema(schema) {
		return false
	}
	if f.include != nil && !f.include.MatchString(schema) {
		return false
	}
	if f.ignore != nil && f.ignore.MatchString(schema) {
		return false
	}
	return true
}

func isSystemSchema(s string) bool {
	switch s {
	case "pg_catalog", "information_schema", "pg_toast":
		return true
	}
	// Per-session temp schemas: pg_temp_N, pg_toast_temp_N.
	if len(s) >= 8 && s[:8] == "pg_temp_" {
		return true
	}
	if len(s) >= 14 && s[:14] == "pg_toast_temp_" {
		return true
	}
	return false
}
