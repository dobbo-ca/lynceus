// internal/collector/index_stats_reader.go
package collector

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dobbo-ca/lynceus/internal/caps"
	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
)

// IndexStatsReader reads per-index scan counters, sizes, and structural
// validity/uniqueness flags from pg_index + pg_class + pg_stat_user_indexes on
// a monitored Postgres. Every returned field is a catalog identifier, a scan
// COUNT, a byte SIZE, or a catalog BOOLEAN — never an index expression
// (pg_get_indexdef) or partial-index predicate (pg_index.indpred), preserving
// the T1 privacy contract. Feeds the Schema checks (ly-u4t.23).
//
// The query DELIBERATELY selects only identifiers, the idx_scan counter,
// pg_relation_size, and four pg_index booleans. It takes ACCESS-SHARE catalog
// locks only, so it belongs on the slow (~10m) full cadence beside the table
// and freeze-age readers, never the ~10s activity cadence. Read-only / RDS-safe.
//
// In Postgres an index always shares its table's namespace, so filter is
// applied once on the index schema; table_fqn's schema is identical.
//
// filter is the SAME SchemaFilter instance shared with the other catalog
// readers: a schema excluded by ignore_schema_regexp produces zero index rows,
// keeping the redaction boundary identical across readers.
type IndexStatsReader struct {
	pool   *pgxpool.Pool
	filter *SchemaFilter
	gate   *caps.Gate
	db     string // current_database() of pool, the gate key
}

// NewIndexStatsReader returns a reader bound to pool, gated by filter and the
// capability gate. db is the connection's current_database().
func NewIndexStatsReader(pool *pgxpool.Pool, filter *SchemaFilter, gate *caps.Gate, db string) *IndexStatsReader {
	return &IndexStatsReader{pool: pool, filter: filter, gate: gate, db: db}
}

// indexStatsSQL joins the index relation (ic) to its table (tc) via pg_index,
// left-joining pg_stat_user_indexes for the scan counter (an index may have no
// stats row yet → COALESCE to 0).
const indexStatsSQL = `
SELECT n.nspname                                  AS schema,
       ic.relname                                 AS index_name,
       tn.nspname || '.' || tc.relname            AS table_fqn,
       COALESCE(psui.idx_scan, 0)::bigint         AS idx_scan,
       pg_relation_size(ic.oid)::bigint           AS size_bytes,
       i.indisvalid, i.indisready, i.indisunique, i.indisprimary
  FROM pg_index i
  JOIN pg_class      ic ON ic.oid = i.indexrelid
  JOIN pg_namespace  n  ON n.oid  = ic.relnamespace
  JOIN pg_class      tc ON tc.oid = i.indrelid
  JOIN pg_namespace  tn ON tn.oid = tc.relnamespace
  LEFT JOIN pg_stat_user_indexes psui ON psui.indexrelid = i.indexrelid
 WHERE ic.relkind = 'i'
   AND n.nspname NOT IN ('pg_catalog','information_schema','pg_toast')
 ORDER BY n.nspname, ic.relname`

// Read returns one IndexStat per allowed index. Rows whose schema is excluded
// by the SchemaFilter are skipped entirely.
func (r *IndexStatsReader) Read(ctx context.Context, serverID string) ([]*lynceusv1.IndexStat, error) {
	_ = serverID // reserved for future per-server scoping; identifiers are server-agnostic here
	if !r.gate.Allowed(r.db, caps.IndexStats) {
		return nil, nil // capability disabled: build & ship nothing
	}

	rows, err := r.pool.Query(ctx, indexStatsSQL)
	if err != nil {
		return nil, fmt.Errorf("query index stats: %w", err)
	}
	defer rows.Close()

	var out []*lynceusv1.IndexStat
	for rows.Next() {
		var (
			schema, name, tableFQN              string
			idxScan, sizeBytes                  int64
			isValid, isReady, isUniq, isPrimary bool
		)
		if err := rows.Scan(
			&schema, &name, &tableFQN,
			&idxScan, &sizeBytes,
			&isValid, &isReady, &isUniq, &isPrimary,
		); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		if !r.filter.IsAllowed(schema) {
			continue
		}
		out = append(out, &lynceusv1.IndexStat{
			Schema:    schema,
			Name:      name,
			Fqn:       schema + "." + name,
			TableFqn:  tableFQN,
			IdxScan:   idxScan,
			SizeBytes: sizeBytes,
			IsValid:   isValid,
			IsReady:   isReady,
			IsUnique:  isUniq,
			IsPrimary: isPrimary,
		})
	}
	return out, rows.Err()
}
