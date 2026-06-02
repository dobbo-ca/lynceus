// Package caps probes a monitored PostgreSQL instance to discover which
// Lynceus capabilities are available on it: which extensions are
// installed, what the role can read, where logs go, what server version
// is running. Results are metadata-only — every Reason string is bounded
// content written by this package, never a literal from the monitored
// database, preserving the Lynceus T1 privacy contract.
//
// Discover is intended to run at the collector on the full-snapshot
// cadence; wiring into cmd/collector and the wire-message form for
// shipping results to the api_server are handled by ly-xnk.2.
package caps

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
	}
}

// Status is one probe's verdict.
//
// Reason is a short, bounded, package-authored string — never a row,
// column value, or query from the monitored database. For Available
// probes it carries a useful detail (e.g. extension version, list of
// granted roles); for unavailable probes it explains why (e.g.
// "extension not installed", "probe error: ...").
type Status struct {
	Available bool
	Reason    string
}

// Set is the output of Discover. Every Capability returned by Declared()
// is guaranteed to be present as a key.
type Set map[Capability]Status
