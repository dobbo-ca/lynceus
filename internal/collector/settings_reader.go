// internal/collector/settings_reader.go
package collector

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dobbo-ca/lynceus/internal/caps"
	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
)

// SettingsReader reads a CURATED ALLOWLIST of tuning-relevant GUCs from
// pg_settings on a monitored Postgres. It feeds the Settings checks
// (ly-u4t.24) and the Config advisor (ly-u4t.18).
//
// PRIVACY — LOAD-BEARING: pg_settings is mostly safe operator config, but SOME
// GUC values are free-form text that would leak infra detail or credentials
// (log_line_prefix, search_path, archive_command, *_file paths,
// primary_conninfo). This reader NEVER does `SELECT * FROM pg_settings`; it
// selects ONLY the names in settingsAllowlist via `WHERE name = ANY($1)`. Every
// allowlisted GUC has a value that is a number, a boolean, or a bounded
// planner/durability enum keyword — none capable of carrying text/paths/
// hostnames/PII. The allowlist, not the proto, is the redaction boundary
// (the wire `value` field is a literal-CAPABLE string). See
// settings_allowlist_test.go for the allowlist∩denylist guard.
//
// Settings are not schema-scoped, so this reader carries no SchemaFilter.
// Read-only, ACCESS-SHARE only — RDS-safe, belongs on the slow (~10m) full
// cadence beside the freeze-age and index readers.
type SettingsReader struct {
	pool *pgxpool.Pool
	gate *caps.Gate
	db   string // current_database() of pool, the gate key
}

// NewSettingsReader returns a reader bound to pool, gated by the capability
// gate. db is the connection's current_database().
func NewSettingsReader(pool *pgxpool.Pool, gate *caps.Gate, db string) *SettingsReader {
	return &SettingsReader{pool: pool, gate: gate, db: db}
}

// settingsAllowlist is the curated set of numeric/bool/enum tuning GUCs whose
// pg_settings.setting value is safe to ship as T1. It DELIBERATELY excludes
// every free-form / path / conninfo / log-template GUC (see the denylist in
// settings_allowlist_test.go). Edits require human review of the new GUC's
// value class — a novel text setting would defeat the T1 guarantee.
var settingsAllowlist = []string{
	"shared_buffers",
	"work_mem",
	"maintenance_work_mem",
	"effective_cache_size",
	"effective_io_concurrency",
	"fsync",
	"full_page_writes",
	"synchronous_commit",
	"wal_compression",
	"autovacuum",
	"track_counts",
	"track_activities",
	"track_io_timing",
	"default_statistics_target",
	"autovacuum_max_workers",
	"autovacuum_naptime",
	"autovacuum_vacuum_scale_factor",
	"autovacuum_analyze_scale_factor",
	"autovacuum_vacuum_cost_limit",
	"autovacuum_vacuum_cost_delay",
	"autovacuum_freeze_max_age",
	"random_page_cost",
	"checkpoint_completion_target",
	"max_wal_size",
	"min_wal_size",
	"wal_buffers",
	"max_connections",
	"max_worker_processes",
	"max_parallel_workers",
	"max_parallel_workers_per_gather",
	"server_version_num",
}

// SettingsAllowlistForTest exposes the curated allowlist to the external
// collector_test package so the integration test can assert every shipped
// name is allowlisted. Test-only accessor; no production caller.
func SettingsAllowlistForTest() []string { return settingsAllowlist }

// settingsSQL selects ONLY the allowlisted names — never a wildcard. unit is
// COALESCE'd because many GUCs have a NULL unit.
const settingsSQL = `SELECT name, setting, COALESCE(unit,''), source, pending_restart
  FROM pg_settings
 WHERE name = ANY($1)
 ORDER BY name`

// Read returns one Setting per allowlisted GUC present on the server. When the
// Settings capability is disabled for this database the reader ships nothing
// and issues no query.
func (r *SettingsReader) Read(ctx context.Context, serverID string) ([]*lynceusv1.Setting, error) {
	_ = serverID // reserved for future per-server scoping; settings are server-agnostic here
	if !r.gate.Allowed(r.db, caps.Settings) {
		return nil, nil // capability disabled: build & ship nothing
	}

	rows, err := r.pool.Query(ctx, settingsSQL, settingsAllowlist)
	if err != nil {
		return nil, fmt.Errorf("query settings: %w", err)
	}
	defer rows.Close()

	var out []*lynceusv1.Setting
	for rows.Next() {
		var name, value, unit, source string
		var pendingRestart bool
		if err := rows.Scan(&name, &value, &unit, &source, &pendingRestart); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		out = append(out, &lynceusv1.Setting{
			Name:           name,
			Value:          value,
			Unit:           unit,
			Source:         source,
			PendingRestart: pendingRestart,
		})
	}
	return out, rows.Err()
}
