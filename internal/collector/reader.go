// Package collector reads telemetry from a monitored PostgreSQL
// instance, normalizes it locally, and ships only normalized (T1)
// data over a websocket to the ingestion server.
//
// Every query text returned by pg_stat_statements is re-run through
// the Lynceus normalizer before it leaves this package: we never
// trust the upstream source for the privacy guarantee. Queries that
// cannot be parsed are dropped entirely (TierBlocked), not shipped.
package collector

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dobbo-ca/lynceus/internal/caps"
	"github.com/dobbo-ca/lynceus/internal/normalize"
	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
)

// Reader queries pg_stat_statements on a monitored Postgres instance
// and returns T1 (normalized) query statistics. Read is gated: when the
// pg_stat_statements capability is disabled for the connection's
// database, Read issues no query and returns no rows.
type Reader struct {
	pool *pgxpool.Pool
	gate *caps.Gate
	db   string // current_database() of pool, used as the gate key
}

// NewReader returns a Reader bound to pool. gate is consulted before
// every Read; db is the connection's current_database() (the gate key).
func NewReader(pool *pgxpool.Pool, gate *caps.Gate, db string) *Reader {
	return &Reader{pool: pool, gate: gate, db: db}
}

// Read returns the current pg_stat_statements rows, normalized.
// Rows whose query text cannot be parsed (TierBlocked) are dropped,
// not returned. The returned QueryStat values are safe to transmit.
//
// When the query_text_t2 capability is strictly enabled for the
// connection's database (fail-closed AllowedStrict — see ly-cwr.5), Read
// emits the literal-bearing QueryStatRaw sibling instead of the T1
// QueryStat: raw query text alongside the pg_query fingerprint +
// normalized skeleton. Ingestion writes these to query_stats_t2 and a
// ClickHouse materialized view derives the literal-free T1 rows. When the
// capability is absent or off (the fail-closed default), Read emits T1
// QueryStat only and no raw ever leaves the edge.
func (r *Reader) Read(ctx context.Context) ([]*lynceusv1.QueryStat, []*lynceusv1.QueryStatRaw, error) {
	if !r.gate.Allowed(r.db, caps.PgStatStatements) {
		return nil, nil, nil // capability disabled: build & ship nothing
	}
	shipRaw := r.gate.AllowedStrict(r.db, caps.QueryTextT2) // fail-closed
	rows, err := r.pool.Query(ctx,
		`SELECT query, calls, total_exec_time, mean_exec_time,
		        rows, shared_blks_hit, shared_blks_read
		   FROM pg_stat_statements
		  WHERE query IS NOT NULL`,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("query pg_stat_statements: %w", err)
	}
	defer rows.Close()

	var out []*lynceusv1.QueryStat
	var raws []*lynceusv1.QueryStatRaw
	for rows.Next() {
		var (
			raw            string
			calls          int64
			totalTimeMs    float64
			meanTimeMs     float64
			rowsOut        int64
			sharedBlksHit  int64
			sharedBlksRead int64
		)
		if err := rows.Scan(&raw, &calls, &totalTimeMs, &meanTimeMs, &rowsOut, &sharedBlksHit, &sharedBlksRead); err != nil {
			return nil, nil, fmt.Errorf("scan: %w", err)
		}

		normText, tier := normalize.Normalize(raw)
		if tier != normalize.TierNormalized {
			// Parser rejected the query — drop it. We do not transmit
			// any part of an unparseable, potentially literal-bearing
			// string.
			continue
		}
		fp, err := normalize.Fingerprint(raw)
		if err != nil {
			continue
		}

		if shipRaw {
			raws = append(raws, &lynceusv1.QueryStatRaw{
				RawQuery:        raw,
				Fingerprint:     fp,
				NormalizedQuery: normText,
				Calls:           calls,
				TotalTimeMs:     totalTimeMs,
				MeanTimeMs:      meanTimeMs,
				Rows:            rowsOut,
				SharedBlksHit:   sharedBlksHit,
				SharedBlksRead:  sharedBlksRead,
			})
			continue
		}

		out = append(out, &lynceusv1.QueryStat{
			Fingerprint:     fp,
			NormalizedQuery: normText,
			Calls:           calls,
			TotalTimeMs:     totalTimeMs,
			MeanTimeMs:      meanTimeMs,
			Rows:            rowsOut,
			SharedBlksHit:   sharedBlksHit,
			SharedBlksRead:  sharedBlksRead,
		})
	}
	return out, raws, rows.Err()
}
