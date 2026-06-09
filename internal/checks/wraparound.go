package checks

import "fmt"

func init() { Register(WraparoundCheck{}) }

// WraparoundCheck flags databases/tables whose transaction-id or MultiXact
// freeze age approaches the ~2.1B wraparound ceiling. Critical-safety:
// hitting the ceiling forces Postgres into read-only "datfrozenxid" refusal.
// It evaluates both xid_age and mxid_age and reports the worse of the two.
// Counts only — T1.
type WraparoundCheck struct{}

const (
	wrapBudget   = 2_000_000_000.0 // wraparound budget (~2.1B hard ceiling)
	wrapCritical = 1_500_000_000   // ~75% of budget — emergency autovacuum territory
	wrapWarning  = 500_000_000     // lagging past a freeze cycle
)

func (WraparoundCheck) ID() string       { return "vacuum.wraparound" }
func (WraparoundCheck) Category() string { return "vacuum" }

func (WraparoundCheck) Eval(in *Input) []Result {
	out := make([]Result, 0, len(in.FreezeAges))
	for _, f := range in.FreezeAges {
		age := f.XIDAge
		kind := "transaction-id"
		if f.MXIDAge > age {
			age, kind = f.MXIDAge, "MultiXact"
		}
		var sev Severity
		switch {
		case age >= wrapCritical:
			sev = SeverityCritical
		case age >= wrapWarning:
			sev = SeverityWarning
		default:
			continue
		}
		out = append(out, Result{
			CheckID:  "vacuum.wraparound",
			Category: "vacuum",
			Severity: sev,
			Status:   StatusFiring,
			Object:   f.Relation,
			Detail: fmt.Sprintf("%s freeze age %d (%.0f%% of %.1fB wraparound budget); autovacuum_freeze_max_age=%d",
				kind, age, float64(age)/wrapBudget*100, wrapBudget/1e9, f.AutovacuumFreezeMaxAge),
		})
	}
	return out
}
