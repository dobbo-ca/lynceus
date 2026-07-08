// Package caps probes a monitored PostgreSQL instance to discover which
// Lynceus capabilities are available on it: which extensions are
// installed, what the role can read, where logs go, what server version
// is running. Results are metadata-only — the cross-boundary Status.Reason
// is a closed, package-authored ReasonCode, never a literal from the
// monitored database, preserving the Lynceus T1 privacy contract. Any
// human-readable diagnostic lives in the collector-local Status.Detail
// field, which never crosses the wire.
//
// Discover is intended to run at the collector on the full-snapshot
// cadence; wiring into cmd/collector and the wire-message form for
// shipping results to the api_server are handled by ly-xnk.2.
package caps

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Capability is the stable identifier of one probed capability. The
// string form is the wire/storage representation that will be reused by
// downstream beads (ly-xnk.2 storage, ly-xnk.4 API).
type Capability string

// Declared capabilities. Edit Declared() when adding a constant.
const (
	PgStatStatements       Capability = "pg_stat_statements"
	AutoExplain            Capability = "auto_explain"
	PgBuffercache          Capability = "pg_buffercache"
	PgWaitSampling         Capability = "pg_wait_sampling"
	PgStatTuple            Capability = "pgstattuple"
	PgStatActivityFullRead Capability = "pg_stat_activity_full_read"
	LogDestination         Capability = "log_destination"
	ServerVersion          Capability = "server_version"
	RolePermissions        Capability = "role_permissions"
	// SchemaInventory gates the schema/object inventory reader (ly-xqf.5).
	// Catalog reads are always available, but the operator may disable
	// shipping inventory via capability policy.
	SchemaInventory Capability = "schema_inventory"
	// TableSize gates the per-table size/growth/TOAST reader (ly-xqf.6).
	TableSize Capability = "table_size"
	// FreezeAge gates the per-database/per-table transaction-id / MultiXact
	// freeze-age reader feeding the wraparound check (ly-u4t.26).
	FreezeAge Capability = "freeze_age"
	// IndexStats gates the per-index scan/validity reader feeding the Schema
	// checks (ly-u4t.23): invalid indexes + unused indexes.
	IndexStats Capability = "index_stats"
	// XminHorizon gates the cluster-global oldest-xmin reader feeding the
	// "blocked by xmin horizon" vacuum check (ly-32k).
	XminHorizon Capability = "xmin_horizon"
)

// Declared returns every capability the package knows how to probe.
// Discover guarantees one entry in the returned Set per declared
// capability — downstream code may rely on key presence.
func Declared() []Capability {
	return []Capability{
		PgStatStatements,
		AutoExplain,
		PgBuffercache,
		PgWaitSampling,
		PgStatTuple,
		PgStatActivityFullRead,
		LogDestination,
		ServerVersion,
		RolePermissions,
		SchemaInventory,
		TableSize,
		FreezeAge,
		IndexStats,
		XminHorizon,
	}
}

// ReasonCode is a closed, package-authored vocabulary explaining a probe's
// verdict. It is the CROSS-BOUNDARY field: the discovery write-back (the
// future first non-test caller of store.UpsertDiscoveredCapabilities)
// serializes Status.Reason and that value may travel from the collector to
// the api_server. It therefore must NEVER carry a literal from the monitored
// database — only one of AllReasonCodes(). See
// docs/superpowers/plans/crd-operator-control-plane.md §4.6.
type ReasonCode string

// The closed reason-code vocabulary. Every Status constructed in this
// package sets Reason to one of these. Edit AllReasonCodes() when adding a
// constant.
const (
	// ReasonProbeError is the verdict for any probe that hit a driver error.
	// Set via ErrToCode — see its doc for the privacy rationale.
	ReasonProbeError           ReasonCode = "PROBE_ERROR"
	ReasonNotInstalled         ReasonCode = "NOT_INSTALLED"
	ReasonInstalled            ReasonCode = "INSTALLED"
	ReasonParseError           ReasonCode = "PARSE_ERROR"
	ReasonVersionBelowBaseline ReasonCode = "VERSION_BELOW_BASELINE"
	ReasonVersionOK            ReasonCode = "VERSION_OK"
	ReasonNotPreloaded         ReasonCode = "NOT_PRELOADED"
	ReasonDisabled             ReasonCode = "DISABLED"
	ReasonNoRole               ReasonCode = "NO_ROLE"
	ReasonRoleOK               ReasonCode = "ROLE_OK"
	ReasonLogReachable         ReasonCode = "LOG_REACHABLE"
	ReasonLogUnreachable       ReasonCode = "LOG_UNREACHABLE"
	// ReasonInternal marks a Status that no probe recorded (a bug in this
	// package), surfaced by Discover's completeness loop.
	ReasonInternal ReasonCode = "INTERNAL"
)

// AllReasonCodes returns the closed reason-code vocabulary. The contract
// test iterates it to prove every code is bounded, package-authored content.
//
// ponytail: this slice is a second hand-maintained copy of the ReasonCode
// const block above (ceiling: two lists synced by hand). Kept deliberately —
// the contract test needs an enumerable set and Go has no built-in iterator
// over a const group.
func AllReasonCodes() []ReasonCode {
	return []ReasonCode{
		ReasonProbeError,
		ReasonNotInstalled,
		ReasonInstalled,
		ReasonParseError,
		ReasonVersionBelowBaseline,
		ReasonVersionOK,
		ReasonNotPreloaded,
		ReasonDisabled,
		ReasonNoRole,
		ReasonRoleOK,
		ReasonLogReachable,
		ReasonLogUnreachable,
		ReasonInternal,
	}
}

// ErrToCode is the single enforced funnel that maps any driver/probe error
// to a cross-boundary ReasonCode. It returns ReasonProbeError unconditionally
// and DISCARDS err entirely: a pgx error can echo statement text, identifiers,
// hints, or constraint bodies from the monitored database, so the error string
// must never reach Status.Reason. Every probe-error site routes through this
// helper instead of writing a Reason literal, which keeps the literal-stripping
// in one auditable, unit-testable place (see TestErrToCode_stripsPoisonedLiteral).
// Callers that want the message keep it in collector-local Status.Detail, never
// Reason.
//
// ponytail: the parameter is unused by design (ceiling: a code-only mapping) —
// it exists so call sites read as "this error becomes this code" and so the
// funnel is the obvious place every probe error must pass through.
func ErrToCode(error) ReasonCode {
	return ReasonProbeError
}

// pick returns yes when avail is true, else no. It collapses the
// "reason := no; if avail { reason = yes }" shape the boolean probes share.
func pick(avail bool, yes, no ReasonCode) ReasonCode {
	if avail {
		return yes
	}
	return no
}

// Status is one probe's verdict.
//
// Reason is the CROSS-BOUNDARY field — a closed ReasonCode, never a row,
// column value, error message, or query from the monitored database. It is
// the field the discovery write-back serializes and may travel over the
// wire, so it must be one of AllReasonCodes().
//
// Detail is collector-local human-readable diagnostics (e.g. extension
// version, list of granted roles, log destination). It MAY contain bounded
// monitored-DB-derived content and therefore MUST NEVER cross the wire: the
// discovery write-back serializes Reason, not Detail.
type Status struct {
	Available bool
	Reason    ReasonCode
	Detail    string
}

// Set is the output of Discover. Every Capability returned by Declared()
// is guaranteed to be present as a key.
type Set map[Capability]Status

// Discoverer probes a monitored Postgres instance for the capabilities
// declared in Declared(). It is safe to call Discover repeatedly; each
// call issues fresh probe queries.
type Discoverer struct {
	pool *pgxpool.Pool
}

// NewDiscoverer returns a Discoverer bound to pool.
func NewDiscoverer(pool *pgxpool.Pool) *Discoverer {
	return &Discoverer{pool: pool}
}

// Discover runs every probe and returns the resulting Set. The returned
// Set is guaranteed to contain exactly one entry per Declared()
// capability — probes that fail or report "not installed" still produce
// a key with Available=false and a descriptive Reason.
//
// Discover only returns a non-nil error for infrastructure failures
// (context cancellation, total pool acquisition failure). Individual
// probe SQL errors are surfaced as Status entries, not bubbled.
func (d *Discoverer) Discover(ctx context.Context) (Set, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	conn, err := d.pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	conn.Release()

	out := make(Set, len(Declared()))

	ProbeExtensions(ctx, d.pool, out)
	ProbeServerVersion(ctx, d.pool, out)
	ProbeRolePermissions(ctx, d.pool, out)
	ProbeStatActivityFullRead(ctx, d.pool, out)
	ProbeLogDestination(ctx, d.pool, out)
	ProbeAutoExplain(ctx, d.pool, out)

	for _, c := range Declared() {
		if _, ok := out[c]; !ok {
			out[c] = Status{
				Available: false,
				Reason:    ReasonInternal,
				Detail:    "probe did not record a result (bug)",
			}
		}
	}
	return out, nil
}
