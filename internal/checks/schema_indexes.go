package checks

import "fmt"

func init() {
	Register(InvalidIndexCheck{})
	Register(UnusedIndexCheck{})
}

// InvalidIndexCheck flags indexes with pg_index.indisvalid = false. An invalid
// index is ignored by the planner yet still maintained on every write — the
// usual cause is a failed CREATE INDEX CONCURRENTLY. Always actionable
// (DROP + recreate), so warning severity. Identifiers only — T1.
type InvalidIndexCheck struct{}

func (InvalidIndexCheck) ID() string       { return "schema.invalid_index" }
func (InvalidIndexCheck) Category() string { return "schema" }

func (InvalidIndexCheck) Eval(in *Input) []Result {
	var out []Result
	for _, ix := range in.Indexes {
		if ix.IsValid {
			continue
		}
		out = append(out, Result{
			CheckID:  "schema.invalid_index",
			Category: "schema",
			Severity: SeverityWarning,
			Status:   StatusFiring,
			Object:   ix.FQN,
			Detail: fmt.Sprintf(
				"index on %s is INVALID (is_ready=%v) — the planner ignores it but writes still maintain it; usually a failed CREATE INDEX CONCURRENTLY. DROP and recreate.",
				ix.TableFQN, ix.IsReady),
		})
	}
	return out
}

// UnusedIndexCheck flags valid, non-constraint-backing indexes with a low
// cumulative scan count and a non-trivial size — dead weight that wastes
// storage and adds write amplification. Advisory (mirrors IndexAdvisorCheck):
// info by default, warning when the index is large.
//
// Suppressions avoid false positives:
//   - invalid indexes are owned by InvalidIndexCheck;
//   - PRIMARY KEY / UNIQUE indexes back constraints and cannot simply be
//     dropped, so they are never "unused" in the actionable sense;
//   - indexes below unusedMinBytes are too small to be worth flagging.
//
// LIMITATION: idx_scan is cumulative since the last stats reset
// (pg_stat_user_indexes has no per-index reset timestamp). Treat a firing
// result as "investigate over a full workload cycle", not "drop immediately".
// Counts/identifiers only — T1.
type UnusedIndexCheck struct{}

const (
	unusedScanThreshold int64 = 50          // idx_scan at or below this is "effectively unused"
	unusedMinBytes      int64 = 1 << 20     // 1 MiB — ignore trivially small indexes
	unusedWarnBytes     int64 = 100_000_000 // ~100 MB — large unused index escalates to warning
)

func (UnusedIndexCheck) ID() string       { return "schema.unused_index" }
func (UnusedIndexCheck) Category() string { return "schema" }

func (UnusedIndexCheck) Eval(in *Input) []Result {
	var out []Result
	for _, ix := range in.Indexes {
		if !ix.IsValid || ix.IsPrimary || ix.IsUnique {
			continue
		}
		if ix.IdxScan > unusedScanThreshold || ix.SizeBytes < unusedMinBytes {
			continue
		}
		sev := SeverityInfo
		if ix.SizeBytes >= unusedWarnBytes {
			sev = SeverityWarning
		}
		out = append(out, Result{
			CheckID:  "schema.unused_index",
			Category: "schema",
			Severity: sev,
			Status:   StatusFiring,
			Object:   ix.FQN,
			Detail: fmt.Sprintf(
				"index on %s scanned %d times (<= %d) using %d bytes — likely unused; confirm over a full workload cycle, then consider DROP.",
				ix.TableFQN, ix.IdxScan, unusedScanThreshold, ix.SizeBytes),
		})
	}
	return out
}
