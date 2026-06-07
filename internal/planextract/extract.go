package planextract

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
)

// FormatVersion is the schema version of the normalized plan tree this
// extractor produces. Bump it when the PlanNode mapping changes in a way
// downstream insight passes must distinguish.
const FormatVersion = 1

// ErrUnsupportedPlanFormat is returned when the auto_explain body is not the
// expected JSON shape (e.g. text/xml/yaml format). The caller drops it rather
// than guess — see the JSON-only decision in the plan doc.
var ErrUnsupportedPlanFormat = errors.New("planextract: unsupported plan format (require auto_explain.log_format=json)")

// rawEnvelope mirrors one element of the JSON array auto_explain logs. Only
// the structural Plan tree is consumed; Query Text / Query Parameters and
// other literal-bearing siblings are intentionally never mapped.
type rawEnvelope struct {
	Plan *rawNode `json:"Plan"`
}

// rawNode mirrors the auto_explain/EXPLAIN JSON node shape. Literal-bearing
// fields (Output) are deliberately absent so they cannot be copied. Condition
// fields are kept only to feed NormalizeCondition (fail-closed).
type rawNode struct {
	NodeType      string `json:"Node Type"`
	RelationName  string `json:"Relation Name"`
	IndexName     string `json:"Index Name"`
	Alias         string `json:"Alias"`
	JoinType      string `json:"Join Type"`
	ScanDirection string `json:"Scan Direction"`

	StartupCost float64 `json:"Startup Cost"`
	TotalCost   float64 `json:"Total Cost"`
	PlanRows    int64   `json:"Plan Rows"`
	PlanWidth   int32   `json:"Plan Width"`

	ActualStartupTime float64 `json:"Actual Startup Time"`
	ActualTotalTime   float64 `json:"Actual Total Time"`
	ActualRows        int64   `json:"Actual Rows"`
	ActualLoops       int64   `json:"Actual Loops"`

	Filter      string `json:"Filter"`
	IndexCond   string `json:"Index Cond"`
	HashCond    string `json:"Hash Cond"`
	JoinFilter  string `json:"Join Filter"`
	RecheckCond string `json:"Recheck Cond"`

	Plans []rawNode `json:"Plans"`
}

// Extract parses a JSON auto_explain plan body into a normalized T1 QueryPlan.
// fingerprint identifies the statement the plan is for (computed by the caller
// from the statement text, which never leaves the collector). capturedAt is
// when the plan was logged. Returns ErrUnsupportedPlanFormat if the body is
// not JSON in the expected shape.
func Extract(planJSON []byte, fingerprint string, capturedAt time.Time) (*lynceusv1.QueryPlan, error) {
	if len(strings.TrimSpace(string(planJSON))) == 0 {
		return nil, ErrUnsupportedPlanFormat
	}

	var envs []rawEnvelope
	if err := json.Unmarshal(planJSON, &envs); err != nil {
		return nil, ErrUnsupportedPlanFormat
	}
	if len(envs) == 0 || envs[0].Plan == nil {
		return nil, ErrUnsupportedPlanFormat
	}

	root := convert(envs[0].Plan)
	return &lynceusv1.QueryPlan{
		Fingerprint:       fingerprint,
		CapturedAtUnix:    capturedAt.Unix(),
		FormatVersion:     FormatVersion,
		TotalCost:         root.GetTotalCost(),
		ActualTotalTimeMs: root.GetActualTotalTimeMs(),
		Root:              root,
	}, nil
}

// convert maps a raw node and its subtree into a normalized PlanNode.
func convert(n *rawNode) *lynceusv1.PlanNode {
	out := &lynceusv1.PlanNode{
		NodeType:            n.NodeType,
		RelationName:        n.RelationName,
		IndexName:           n.IndexName,
		Alias:               n.Alias,
		JoinType:            n.JoinType,
		ScanDirection:       n.ScanDirection,
		StartupCost:         n.StartupCost,
		TotalCost:           n.TotalCost,
		PlanRows:            n.PlanRows,
		PlanWidth:           n.PlanWidth,
		ActualStartupTimeMs: n.ActualStartupTime,
		ActualTotalTimeMs:   n.ActualTotalTime,
		ActualRows:          n.ActualRows,
		ActualLoops:         n.ActualLoops,
		NormalizedCondition: normalizeConds(n),
	}
	for i := range n.Plans {
		out.Plans = append(out.Plans, convert(&n.Plans[i]))
	}
	return out
}

// normalizeConds collapses the present condition fields into one normalized,
// literal-free predicate. Each source condition is run through the fail-closed
// NormalizeCondition; any that cannot be proven literal-free is dropped.
func normalizeConds(n *rawNode) string {
	var parts []string
	for _, raw := range []string{n.Filter, n.IndexCond, n.HashCond, n.JoinFilter, n.RecheckCond} {
		if norm := NormalizeCondition(raw); norm != "" {
			parts = append(parts, norm)
		}
	}
	return strings.Join(parts, " AND ")
}
