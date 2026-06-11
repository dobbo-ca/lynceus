package checks

import "fmt"

func init() { Register(BlockingCheck{}) }

// BlockingCheck flags A→B lock-wait relationships (B waits on a lock held by A).
// Any active blocking edge is at least a warning; a long wait escalates to
// critical. Object is the blocked pid (the victim). pids only — T1.
type BlockingCheck struct{}

const blockingCriticalWaitSeconds = 60

func (BlockingCheck) ID() string       { return "connections.blocking" }
func (BlockingCheck) Category() string { return "connections" }

func (BlockingCheck) Eval(in *Input) []Result {
	out := make([]Result, 0, len(in.Blocking))
	for _, e := range in.Blocking {
		sev := SeverityWarning
		if e.BlockedWaitSeconds >= blockingCriticalWaitSeconds {
			sev = SeverityCritical
		}
		out = append(out, Result{
			CheckID:  "connections.blocking",
			Category: "connections",
			Severity: sev,
			Status:   StatusFiring,
			Object:   fmt.Sprintf("pid:%d", e.BlockedPID),
			Detail:   fmt.Sprintf("pid %d blocked by pid %d for %ds", e.BlockedPID, e.BlockerPID, e.BlockedWaitSeconds),
		})
	}
	return out
}
