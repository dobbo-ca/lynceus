package store

import (
	"time"

	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
)

// This file holds the backend-agnostic row/value types and the one shared
// column-order const consumed by the ClickHouse stats implementation
// (chstats_*.go) and the Stats interface. They were relocated here when the
// Postgres stats backend (pgxStats) was removed.

// Throughput is the aggregate query volume for a server set over a window.
type Throughput struct {
	Calls       int64
	TotalTimeMs float64
}

// QPSBucket is the summed calls for a server set in one hourly time bucket.
type QPSBucket struct {
	BucketStart time.Time
	Calls       int64
}

// ActivitySummary is the connection-state snapshot for a server set: peak active
// connections in the most recent bucket and the dominant wait event over the
// window.
type ActivitySummary struct {
	ActiveConns int64
	TopWait     string // "" if nothing was waiting
}

// QueryPlanRow is one extracted auto_explain plan as stored in the stats DB.
// Plan carries the normalized (literal-free) T1 tree; it is marshaled to the
// plan_tree JSONB column. DataTier zero is treated as 1 (T1) on insert.
type QueryPlanRow struct {
	ServerID   string
	CapturedAt time.Time
	Plan       *lynceusv1.QueryPlan
	DataTier   int16
}

// PlanKey is one (server_id, fingerprint) pair that has at least one stored
// plan in the queried window. Both fields are structural identifiers — no
// literal.
type PlanKey struct {
	ServerID    string
	Fingerprint string
}

// InsightRow is one detected anti-pattern as stored in the stats DB. Every field
// is a structural identifier or an aggregate count (T1, literal-free) — it maps
// 1:1 to insight.Insight. DataTier zero is treated as 1 (T1) on insert.
type InsightRow struct {
	ServerID     string
	CapturedAt   time.Time
	Kind         string
	Severity     string
	Fingerprint  string
	Relation     string
	NodePath     string
	RowsReturned int64
	RowsScanned  int64
	Selectivity  float64
	Detail       string
	DataTier     int16
}

// insightsColumns is the INSERT column order for insights, shared by the
// ClickHouse batch writer (chstats_insights.go).
var insightsColumns = []string{
	"server_id", "captured_at", "kind", "severity", "fingerprint",
	"relation", "node_path", "rows_returned", "rows_scanned",
	"selectivity", "detail", "data_tier",
}

// TableStatRow is one T1 row of per-table size/growth + TOAST/heap/index
// breakdown plus dead-tuple and vacuum/analyze metrics. Zero-valued
// Last* timestamps are written as SQL NULL. DataTier zero is treated as
// 1 (T1) on insert — see the Stats package comment.
type TableStatRow struct {
	ServerID    string
	CollectedAt time.Time
	SchemaName  string
	ObjectName  string
	FQN         string

	TotalBytes   int64
	HeapBytes    int64
	ToastBytes   int64
	IndexesBytes int64

	RowEstimate      int64
	LiveTuples       int64
	DeadTuples       int64
	NModSinceAnalyze int64

	SeqScan    int64
	IdxScan    int64
	NTupIns    int64
	NTupUpd    int64
	NTupDel    int64
	NTupHotUpd int64

	LastVacuum      time.Time // zero -> NULL
	LastAutovacuum  time.Time // zero -> NULL
	LastAnalyze     time.Time // zero -> NULL
	LastAutoanalyze time.Time // zero -> NULL
	VacuumCount     int64
	AutovacuumCount int64

	DataTier int16 // 0 -> coerced to 1
}

// IndexStatRow is one T1 row of per-index scan counter + size + structural
// validity/uniqueness flags. DataTier zero is coerced to 1 (T1) on insert.
// It carries NO index expression or predicate — those are literal-bearing
// and belong to T2.
type IndexStatRow struct {
	ServerID    string
	CollectedAt time.Time
	SchemaName  string
	ObjectName  string
	FQN         string
	TableFQN    string

	IdxScan   int64
	SizeBytes int64
	IsValid   bool
	IsReady   bool
	IsUnique  bool
	IsPrimary bool

	DataTier int16 // 0 -> coerced to 1
}

// ConnectionSampleRow is one T1 point-in-time pg_stat_activity backend
// observation: pid, fixed state label, integer durations — never query text.
// DataTier zero is coerced to 1 (T1) on insert.
type ConnectionSampleRow struct {
	ServerID      string
	ObservedAt    time.Time
	PID           int64
	State         string
	ActiveSeconds int64
	XactSeconds   int64
	StateSeconds  int64
	WaitEventType string
	DataTier      int16 // 0 -> coerced to 1
}

// BlockingEdgeRow is one T1 A→B lock-wait relationship from pg_blocking_pids().
type BlockingEdgeRow struct {
	ServerID           string
	ObservedAt         time.Time
	BlockedPID         int64
	BlockerPID         int64
	BlockedWaitSeconds int64
	DataTier           int16 // 0 -> coerced to 1
}

// FreezeAgeRow is one T1 row of per-database / per-table transaction-id /
// MultiXact freeze AGES (counts only — never raw xids). Scope is "database"
// or "table". DataTier zero is coerced to 1 (T1) on insert.
type FreezeAgeRow struct {
	ServerID    string
	CollectedAt time.Time
	Scope       string // "database" | "table"
	SchemaName  string // "" for database scope
	ObjectName  string // table name or database name
	FQN         string // schema.name for tables; datname for db

	XIDAge                 int64
	MXIDAge                int64
	AutovacuumFreezeMaxAge int64

	DataTier int16 // 0 -> coerced to 1
}

// XminHorizonRow is one T1 row of the cluster-global oldest-xmin observation
// (ly-32k): the AGE (in transactions) of the oldest xid still pinned by some
// backend / replication slot / prepared xact, plus a fixed HolderKind label.
// Counts + a bounded label only — never a slot name or gid. DataTier zero is
// coerced to 1 (T1) on insert.
type XminHorizonRow struct {
	ServerID      string
	CollectedAt   time.Time
	OldestXminAge int64
	HolderKind    string // "backend" | "replication_slot" | "prepared_xact"

	DataTier int16 // 0 -> coerced to 1
}

// SettingRow is one T1 row of a curated pg_settings tuning GUC. value is a
// bounded config string (number/bool/enum) — the collector allowlist, not this
// struct, is the redaction boundary. DataTier zero is coerced to 1 (T1) on
// insert. Mirrors FreezeAgeRow.
type SettingRow struct {
	ServerID       string
	CollectedAt    time.Time
	Name           string
	Value          string
	Unit           string
	Source         string
	PendingRestart bool

	DataTier int16 // 0 -> coerced to 1
}

// ChecksResultRow is one persisted Checks engine result. DataTier zero is
// coerced to 1 (T1) on insert.
type ChecksResultRow struct {
	ServerID    string
	EvaluatedAt time.Time
	CheckID     string
	Category    string
	Severity    string
	Status      string
	Object      string
	Detail      string
	Muted       bool
	DataTier    int16
}

// MuteRow is an operator suppression entry.
type MuteRow struct {
	ServerID   string
	CheckID    string
	Object     string
	MutedUntil time.Time
	Reason     string
}

// LogEventRow is one classified Postgres log event as stored in the stats DB.
// Every field is classification metadata only (event type, severity, catalog
// identifiers, hashed client IP, coarse counters) — never the statement text,
// bind params, error detail, or raw message. DataTier zero is treated as 1
// (T1) on insert. See the LogEvent privacy contract test.
type LogEventRow struct {
	ServerID        string
	EventType       string
	Severity        string
	OccurredAt      time.Time
	LoggedAt        time.Time
	Pid             int64
	BackendType     string
	DatabaseName    string
	UserName        string
	ApplicationName string
	ClientAddrHash  string
	SqlState        string
	SessionLineNum  int64
	TransactionID   int64
	DataTier        int16
}

// SchemaObjectRow is one row to upsert. Kind mirrors proto ObjectKind's
// numeric value. The caller is responsible for the collector-side
// schema-name filter — by the time a row reaches this struct, its
// schema is already approved for transmission.
type SchemaObjectRow struct {
	ServerID    string
	Kind        int16
	FQN         string
	SchemaName  string // stored in the "schema" column (matches proto field name)
	ObjectName  string // stored in the "name" column (matches proto field name)
	SizeBytes   int64
	IsPartition bool
	ParentFQN   string
}

// SchemaObjectRecord is one stored schema_objects row, including the stable
// first_seen_at and refreshed last_seen_at timestamps.
type SchemaObjectRecord struct {
	ServerID    string
	Kind        int16
	FQN         string
	SchemaName  string
	ObjectName  string
	SizeBytes   int64
	IsPartition bool
	ParentFQN   string
	FirstSeenAt time.Time
	LastSeenAt  time.Time
}
