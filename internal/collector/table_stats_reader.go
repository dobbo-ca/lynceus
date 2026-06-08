// internal/collector/table_stats_reader.go
package collector

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dobbo-ca/lynceus/internal/caps"
	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
)

// TableStatsReader reads per-table size/growth + TOAST/heap/index breakdown
// and vacuum/dead-tuple metrics from pg_class + pg_stat_user_tables on a
// monitored Postgres. Every returned field is a catalog identifier, a byte
// size, an aggregate count, or a unix timestamp — never a column value.
//
// The query DELIBERATELY selects only size functions, catalog identifiers,
// and pg_stat_user_tables counters. It takes ACCESS-SHARE catalog locks and
// stat()s relation files, so it belongs on the slow (~10m) full cadence,
// never the ~10s activity cadence. Read-only / RDS-safe.
//
// filter is the SAME SchemaFilter instance shared with the schema inventory
// reader (ly-xqf.5): a schema excluded by ignore_schema_regexp produces zero
// table-stat rows, keeping the redaction boundary identical across readers.
type TableStatsReader struct {
	pool   *pgxpool.Pool
	filter *SchemaFilter
	gate   *caps.Gate
	db     string // current_database() of pool, the gate key
}

// NewTableStatsReader returns a reader bound to pool, gated by filter and the
// capability gate. db is the connection's current_database().
func NewTableStatsReader(pool *pgxpool.Pool, filter *SchemaFilter, gate *caps.Gate, db string) *TableStatsReader {
	return &TableStatsReader{pool: pool, filter: filter, gate: gate, db: db}
}

// Read returns one TableStat per allowed ordinary/partitioned/matview table.
// Rows whose schema is excluded by the SchemaFilter are skipped entirely.
func (r *TableStatsReader) Read(ctx context.Context, serverID string) ([]*lynceusv1.TableStat, error) {
	_ = serverID // reserved for future per-server scoping; identifiers are server-agnostic here
	if !r.gate.Allowed(r.db, caps.TableSize) {
		return nil, nil // capability disabled: build & ship nothing
	}
	rows, err := r.pool.Query(ctx,
		`SELECT n.nspname, c.relname,
		        pg_total_relation_size(c.oid)::bigint                              AS total_bytes,
		        (pg_table_size(c.oid)
		          - COALESCE(pg_total_relation_size(c.reltoastrelid),0))::bigint   AS heap_bytes,
		        COALESCE(pg_total_relation_size(c.reltoastrelid),0)::bigint        AS toast_bytes,
		        pg_indexes_size(c.oid)::bigint                                     AS indexes_bytes,
		        GREATEST(c.reltuples,0)::bigint                                    AS row_estimate,
		        COALESCE(s.n_live_tup,0), COALESCE(s.n_dead_tup,0), COALESCE(s.n_mod_since_analyze,0),
		        COALESCE(s.seq_scan,0), COALESCE(s.idx_scan,0),
		        COALESCE(s.n_tup_ins,0), COALESCE(s.n_tup_upd,0), COALESCE(s.n_tup_del,0), COALESCE(s.n_tup_hot_upd,0),
		        s.last_vacuum, s.last_autovacuum, s.last_analyze, s.last_autoanalyze,
		        COALESCE(s.vacuum_count,0), COALESCE(s.autovacuum_count,0)
		   FROM pg_class c
		   JOIN pg_namespace n ON n.oid = c.relnamespace
		   LEFT JOIN pg_stat_user_tables s ON s.relid = c.oid
		  WHERE c.relkind IN ('r','p','m')
		  ORDER BY n.nspname, c.relname`,
	)
	if err != nil {
		return nil, fmt.Errorf("query table stats: %w", err)
	}
	defer rows.Close()

	var out []*lynceusv1.TableStat
	for rows.Next() {
		var (
			schema, name                               string
			total, heap, toast, indexes                int64
			rowEst, live, dead, nMod                   int64
			seqScan, idxScan                           int64
			nIns, nUpd, nDel, nHot                     int64
			lastVac, lastAutoVac, lastAna, lastAutoAna *time.Time
			vacCount, autovacCount                     int64
		)
		if err := rows.Scan(
			&schema, &name,
			&total, &heap, &toast, &indexes,
			&rowEst, &live, &dead, &nMod,
			&seqScan, &idxScan,
			&nIns, &nUpd, &nDel, &nHot,
			&lastVac, &lastAutoVac, &lastAna, &lastAutoAna,
			&vacCount, &autovacCount,
		); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}

		if !r.filter.IsAllowed(schema) {
			continue
		}

		out = append(out, &lynceusv1.TableStat{
			Schema: schema,
			Name:   name,
			Fqn:    schema + "." + name,

			TotalBytes:   total,
			HeapBytes:    heap,
			ToastBytes:   toast,
			IndexesBytes: indexes,

			RowEstimate:      rowEst,
			LiveTuples:       live,
			DeadTuples:       dead,
			NModSinceAnalyze: nMod,

			SeqScan:    seqScan,
			IdxScan:    idxScan,
			NTupIns:    nIns,
			NTupUpd:    nUpd,
			NTupDel:    nDel,
			NTupHotUpd: nHot,

			LastVacuumUnix:      unixOrZero(lastVac),
			LastAutovacuumUnix:  unixOrZero(lastAutoVac),
			LastAnalyzeUnix:     unixOrZero(lastAna),
			LastAutoanalyzeUnix: unixOrZero(lastAutoAna),
			VacuumCount:         vacCount,
			AutovacuumCount:     autovacCount,
		})
	}
	return out, rows.Err()
}

// unixOrZero returns t's unix seconds, or 0 when the timestamp is NULL
// (never vacuumed/analyzed).
func unixOrZero(t *time.Time) int64 {
	if t == nil {
		return 0
	}
	return t.Unix()
}
