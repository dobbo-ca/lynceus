package checks

import "fmt"

func init() { Register(XminHorizonCheck{}) }

// XminHorizonCheck flags a stuck xmin horizon: an old transaction id still
// pinned by a long-running backend snapshot, an abandoned replication slot, or
// an orphaned prepared transaction. VACUUM cannot reclaim dead rows newer than
// this horizon, so a large age drives table/index bloat until the holder is
// released. The holder_kind (Object) tells the operator where to look. Counts
// only — T1.
type XminHorizonCheck struct{}

const (
	xminWarnAge     = 50_000_000  // ~a freeze cycle of held-back cleanup
	xminCriticalAge = 500_000_000 // horizon far behind — serious bloat risk
)

func (XminHorizonCheck) ID() string       { return "vacuum.xmin_horizon" }
func (XminHorizonCheck) Category() string { return "vacuum" }

func (XminHorizonCheck) Eval(in *Input) []Result {
	if in.XminHorizon == nil {
		return nil
	}
	age := in.XminHorizon.OldestXminAge
	var sev Severity
	switch {
	case age >= xminCriticalAge:
		sev = SeverityCritical
	case age >= xminWarnAge:
		sev = SeverityWarning
	default:
		return nil
	}
	return []Result{{
		CheckID:  "vacuum.xmin_horizon",
		Category: "vacuum",
		Severity: sev,
		Status:   StatusFiring,
		Object:   in.XminHorizon.HolderKind,
		Detail: fmt.Sprintf("oldest xmin held by %s, age %d transactions; VACUUM cannot reclaim dead rows past this horizon",
			in.XminHorizon.HolderKind, age),
	}}
}
