package checks

import "fmt"

func init() { Register(ActiveLongRunningCheck{}) }

// ActiveLongRunningCheck flags client backends whose CURRENT statement has been
// executing longer than a threshold (a runaway query, a missing index scan, or
// a stuck lock-waiter). Duration only — T1.
type ActiveLongRunningCheck struct{}

const (
	activeWarnSeconds     = 300 // 5 min
	activeCriticalSeconds = 900 // 15 min
)

func (ActiveLongRunningCheck) ID() string       { return "connections.long_running_active" }
func (ActiveLongRunningCheck) Category() string { return "connections" }

func (ActiveLongRunningCheck) Eval(in *Input) []Result {
	var out []Result
	for _, c := range in.Connections {
		if c.State != "active" {
			continue
		}
		var sev Severity
		switch {
		case c.ActiveSeconds >= activeCriticalSeconds:
			sev = SeverityCritical
		case c.ActiveSeconds >= activeWarnSeconds:
			sev = SeverityWarning
		default:
			continue
		}
		out = append(out, Result{
			CheckID:  "connections.long_running_active",
			Category: "connections",
			Severity: sev,
			Status:   StatusFiring,
			Object:   fmt.Sprintf("pid:%d", c.PID),
			Detail:   fmt.Sprintf("active query running %ds (wait_event_type=%q)", c.ActiveSeconds, c.WaitEventType),
		})
	}
	return out
}
