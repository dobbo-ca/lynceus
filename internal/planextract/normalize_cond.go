// Package planextract turns auto_explain JSON plan bodies (collected into the
// collector-local T2 LogPayload) into normalized, literal-free T1 QueryPlan
// records. No literal value from the monitored database may survive into the
// output — see proto/lynceus/v1/plan.proto and the contract test.
package planextract

import (
	"strings"

	"github.com/dobbo-ca/lynceus/internal/normalize"
)

// condWrapPrefix wraps a bare boolean condition into a syntactically valid,
// literal-free SQL statement so the libpg_query normalizer can parse it. The
// prefix contains no literal of its own, so normalization leaves it verbatim
// and we can strip it back off to recover just the predicate.
const condWrapPrefix = "SELECT * FROM __ly_cond WHERE "

// NormalizeCondition replaces every literal in an EXPLAIN condition string
// (Filter, Index Cond, Hash Cond, …) with a positional placeholder and
// returns the resulting predicate. It FAILS CLOSED: if the condition cannot
// be parsed and proven literal-free, it returns "" rather than risk shipping
// a raw predicate. The empty result means "no condition available", never
// "the original condition".
func NormalizeCondition(cond string) string {
	cond = strings.TrimSpace(cond)
	if cond == "" {
		return ""
	}

	out, tier := normalize.Normalize(condWrapPrefix + cond)
	if tier != normalize.TierNormalized {
		return ""
	}

	pred, ok := strings.CutPrefix(out, condWrapPrefix)
	if !ok {
		// Prefix changed during normalization — unexpected shape; fail closed.
		return ""
	}
	pred = strings.TrimSpace(pred)

	// Defense in depth: a surviving single quote means a string literal was
	// not stripped. Never return it.
	if strings.Contains(pred, "'") {
		return ""
	}
	return pred
}
