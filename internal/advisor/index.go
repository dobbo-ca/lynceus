package advisor

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
)

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

// TableInfo is the size + scan signal the advisor ranks candidates by, fed in
// from store.TableStatRow (the api handler maps it). Decoupled from store so
// the recommender stays pure + trivially testable.
type TableInfo struct {
	TotalBytes int64
	SeqScans   int64
}

// IndexRecommendation is one suggested index, T1-safe (identifiers + counts).
type IndexRecommendation struct {
	Relation     string
	Columns      []string
	QueryCount   int // distinct fingerprints whose Seq Scan filters this way
	TotalBytes   int64
	SeqScans     int64
	Fingerprints []string
	Rationale    string
}

// RecommendIndexes walks each plan for Seq Scan nodes with a normalized filter,
// turns each (relation, columns) into a candidate, aggregates across plans, and
// ranks by table size * seq-scan frequency (biggest, hottest first).
func RecommendIndexes(plans []*lynceusv1.QueryPlan, tables map[string]TableInfo) []IndexRecommendation {
	type agg struct {
		cols []string
		fps  map[string]bool
	}
	cand := map[string]*agg{} // key: relation + "\x00" + strings.Join(cols,",")
	for _, qp := range plans {
		walk(qp.GetRoot(), func(n *lynceusv1.PlanNode) {
			if n.GetNodeType() != "Seq Scan" || n.GetRelationName() == "" {
				return
			}
			cols := filterColumns(n.GetNormalizedCondition())
			if len(cols) == 0 {
				return
			}
			key := n.GetRelationName() + "\x00" + strings.Join(cols, ",")
			a := cand[key]
			if a == nil {
				a = &agg{cols: cols, fps: map[string]bool{}}
				cand[key] = a
			}
			if fp := qp.GetFingerprint(); fp != "" {
				a.fps[fp] = true
			}
		})
	}

	var out []IndexRecommendation
	for key, a := range cand {
		rel := key[:strings.IndexByte(key, 0)]
		ti := tables[rel]
		out = append(out, IndexRecommendation{
			Relation:     rel,
			Columns:      a.cols,
			QueryCount:   len(a.fps),
			TotalBytes:   ti.TotalBytes,
			SeqScans:     ti.SeqScans,
			Fingerprints: sortedKeys(a.fps),
			Rationale: fmt.Sprintf(
				"%d quer(ies) seq-scan %s filtering on (%s); no usable index exists for that predicate.",
				len(a.fps), rel, strings.Join(a.cols, ", "),
			),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		si := out[i].TotalBytes * (out[i].SeqScans + 1)
		sj := out[j].TotalBytes * (out[j].SeqScans + 1)
		if si != sj {
			return si > sj
		}
		return out[i].Relation < out[j].Relation // stable tiebreak
	})
	return out
}

func walk(n *lynceusv1.PlanNode, fn func(*lynceusv1.PlanNode)) {
	if n == nil {
		return
	}
	fn(n)
	for _, c := range n.GetPlans() {
		walk(c, fn)
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
