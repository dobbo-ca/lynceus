package checks

import "fmt"

func init() { Register(InsufficientVacuumFrequencyCheck{}) }

// InsufficientVacuumFrequencyCheck flags tables whose dead-tuple count has
// grown past the point where autovacuum should have already run. It models
// Postgres's own autovacuum trigger — autovacuum_vacuum_threshold +
// autovacuum_vacuum_scale_factor * n_live_tup — using the built-in defaults,
// and fires when the observed dead tuples have overshot that trigger. A dead
// count far past the trigger means autovacuum is falling behind (bloat, IO, or
// a too-low cost limit). Counts only — T1.
type InsufficientVacuumFrequencyCheck struct{}

const (
	avVacThreshold      = 50.0 // autovacuum_vacuum_threshold default
	avVacScaleFactor    = 0.2  // autovacuum_vacuum_scale_factor default
	vacFreqMinDead      = 1000 // absolute dead-tuple floor: suppress tiny-table noise
	vacFreqCriticalMult = 2.0  // dead past trigger*this is critical
)

func (InsufficientVacuumFrequencyCheck) ID() string       { return "vacuum.insufficient_frequency" }
func (InsufficientVacuumFrequencyCheck) Category() string { return "vacuum" }

func (InsufficientVacuumFrequencyCheck) Eval(in *Input) []Result {
	out := make([]Result, 0, len(in.TableStats))
	for _, t := range in.TableStats {
		trigger := avVacThreshold + avVacScaleFactor*float64(t.LiveTuples)
		if t.DeadTuples < vacFreqMinDead || float64(t.DeadTuples) <= trigger {
			continue
		}
		sev := SeverityWarning
		if float64(t.DeadTuples) > trigger*vacFreqCriticalMult {
			sev = SeverityCritical
		}
		out = append(out, Result{
			CheckID:  "vacuum.insufficient_frequency",
			Category: "vacuum",
			Severity: sev,
			Status:   StatusFiring,
			Object:   t.Relation,
			Detail: fmt.Sprintf("%d dead tuples (%.0f%% of %d live); autovacuum trigger is %.0f — vacuum is not keeping up",
				t.DeadTuples, float64(t.DeadTuples)/float64(max64(t.LiveTuples, 1))*100, t.LiveTuples, trigger),
		})
	}
	return out
}

// max64 returns the larger of a, b. Guards the dead-ratio divisor against a
// zero live-tuple count.
func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
