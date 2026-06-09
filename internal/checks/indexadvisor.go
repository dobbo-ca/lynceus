package checks

import "fmt"

func init() { Register(IndexAdvisorCheck{}) }

// IndexAdvisorCheck turns Index Advisor missing-index recommendations into
// check results. Advisory (not safety): info by default, warning when the
// table is large and heavily seq-scanned. Counts/identifiers only — T1.
type IndexAdvisorCheck struct{}

const (
	idxWarnSeqScans = 100_000     // heavy sequential scanning
	idxWarnBytes    = 100_000_000 // ~100 MB+ table
)

func (IndexAdvisorCheck) ID() string       { return "queries.missing_index" }
func (IndexAdvisorCheck) Category() string { return "queries" }

func (IndexAdvisorCheck) Eval(in *Input) []Result {
	out := make([]Result, 0, len(in.IndexRecs))
	for _, rec := range in.IndexRecs {
		sev := SeverityInfo
		if rec.SeqScans >= idxWarnSeqScans && rec.TotalBytes >= idxWarnBytes {
			sev = SeverityWarning
		}
		cols := ""
		for i, c := range rec.Columns {
			if i > 0 {
				cols += ", "
			}
			cols += c
		}
		out = append(out, Result{
			CheckID:  "queries.missing_index",
			Category: "queries",
			Severity: sev,
			Status:   StatusFiring,
			Object:   rec.Relation,
			Detail: fmt.Sprintf("missing index on (%s): %d queries seq-scan this %d-byte table %d times — an index would avoid the scans.",
				cols, rec.QueryCount, rec.TotalBytes, rec.SeqScans),
		})
	}
	return out
}
