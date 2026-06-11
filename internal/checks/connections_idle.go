package checks

import "fmt"

func init() { Register(IdleInTransactionCheck{}) }

// IdleInTransactionCheck flags backends sitting idle inside an open transaction
// past a threshold. These hold the xmin horizon (blocking VACUUM) and any locks
// the transaction already took, so a long idle-in-txn is a real availability
// hazard. Time-in-state only — T1.
type IdleInTransactionCheck struct{}

const (
	idleTxnWarnSeconds     = 300 // 5 min
	idleTxnCriticalSeconds = 900 // 15 min
)

func (IdleInTransactionCheck) ID() string       { return "connections.idle_in_transaction" }
func (IdleInTransactionCheck) Category() string { return "connections" }

func (IdleInTransactionCheck) Eval(in *Input) []Result {
	var out []Result
	for _, c := range in.Connections {
		if c.State != "idle in transaction" && c.State != "idle in transaction (aborted)" {
			continue
		}
		var sev Severity
		switch {
		case c.StateSeconds >= idleTxnCriticalSeconds:
			sev = SeverityCritical
		case c.StateSeconds >= idleTxnWarnSeconds:
			sev = SeverityWarning
		default:
			continue
		}
		out = append(out, Result{
			CheckID:  "connections.idle_in_transaction",
			Category: "connections",
			Severity: sev,
			Status:   StatusFiring,
			Object:   fmt.Sprintf("pid:%d", c.PID),
			Detail:   fmt.Sprintf("%s for %ds (xact age %ds)", c.State, c.StateSeconds, c.XactSeconds),
		})
	}
	return out
}
