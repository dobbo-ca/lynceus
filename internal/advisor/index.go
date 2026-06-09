package advisor

import "regexp"

// colCmp matches "<optional qualifier.>column <op>" in a normalized condition.
// The condition is already literal-free (only identifiers, ops, parens, $n).
// The \b only anchors the word-operator alternatives (between/in); symbolic
// operators are non-word characters and have no trailing word boundary.
var colCmp = regexp.MustCompile(`(?i)([a-z_][a-z0-9_]*)\s*(=|<>|!=|<=|>=|<|>|~~|between\b|in\b)`)

// filterColumns returns candidate index columns from a normalized predicate,
// equality/membership columns first (best btree leading columns) then range
// columns, de-duplicated, qualifiers stripped. The op-less left identifier of
// each comparison is the column (right side is always a $n placeholder).
func filterColumns(cond string) []string {
	var eq, rng []string
	seen := map[string]bool{}
	for _, m := range colCmp.FindAllStringSubmatch(cond, -1) {
		col, op := m[1], m[2]
		if seen[col] {
			continue
		}
		seen[col] = true
		switch op {
		case "<", ">", "<=", ">=", "between", "BETWEEN":
			rng = append(rng, col)
		default: // =, <>, !=, in, ~~ (LIKE) -> treat as equality-ish leading
			eq = append(eq, col)
		}
	}
	if len(eq)+len(rng) == 0 {
		return nil
	}
	return append(eq, rng...)
}
