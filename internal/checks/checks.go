// Package checks is the Lynceus Checks/Alerts engine. It runs pure check
// predicates over the latest per-server stats and emits severity-tagged
// Results. Like internal/insight, it is I/O-free and deterministic: the
// scheduler (scheduler.go) does the store reads and persistence. Every
// Result field is a count, a fixed enum, an identifier, or a bounded
// package-authored string — never a literal from the monitored database,
// preserving the T1 privacy contract.
package checks

import (
	"time"

	"github.com/dobbo-ca/lynceus/internal/advisor"
)

// Severity is the alert level of a check result.
type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

func (s Severity) rank() int {
	switch s {
	case SeverityCritical:
		return 3
	case SeverityWarning:
		return 2
	case SeverityInfo:
		return 1
	default:
		return 0
	}
}

// Status is whether a check is currently firing.
type Status string

const (
	StatusOK     Status = "ok"
	StatusFiring Status = "firing"
)

// Input is the per-server snapshot a check evaluates. The scheduler
// assembles it from store reads. Fields are added as bundles need them
// (Part B adds FreezeAges). Now is injected for deterministic tests.
type Input struct {
	ServerID   string
	Now        time.Time
	TableStats []TableInfo
	FreezeAges []FreezeInfo                  // populated in Part B (wraparound)
	IndexRecs  []advisor.IndexRecommendation // populated by the scheduler (ly-u4t.27)
}

// TableInfo is the check-local projection of store.TableStatRow.
type TableInfo struct {
	Relation         string
	LiveTuples       int64
	DeadTuples       int64
	NModSinceAnalyze int64
	SeqScan          int64
	IdxScan          int64
}

// FreezeInfo is the check-local projection of store.FreezeAgeRow (Part B).
type FreezeInfo struct {
	Scope                  string // "database" or "table"
	Relation               string // fqn for tables; database name for db scope
	XIDAge                 int64  // age(relfrozenxid) / age(datfrozenxid)
	MXIDAge                int64  // mxid_age(relminmxid) / mxid_age(datminmxid)
	AutovacuumFreezeMaxAge int64  // server setting (count)
}

// Result is one firing check observation. Object is an identifier label
// (relation / database / fingerprint), never a literal value.
type Result struct {
	ServerID string
	CheckID  string
	Category string
	Severity Severity
	Status   Status
	Object   string
	Detail   string
}

// Check is one pluggable predicate. Bundles implement it and Register.
type Check interface {
	ID() string
	Category() string
	Eval(in *Input) []Result
}

var registry []Check

// Register adds a check to the default set. Called from bundle init().
func Register(c Check) { registry = append(registry, c) }

// DefaultChecks returns the registered checks (copy).
func DefaultChecks() []Check {
	out := make([]Check, len(registry))
	copy(out, registry)
	return out
}

// Run evaluates every check against in and returns all firing results,
// each stamped with in.ServerID.
func Run(in *Input, checks []Check) []Result {
	var out []Result
	for _, c := range checks {
		for _, r := range c.Eval(in) {
			r.ServerID = in.ServerID
			out = append(out, r)
		}
	}
	return out
}
