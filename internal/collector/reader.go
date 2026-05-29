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

	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
	"github.com/dobbo-ca/lynceus/internal/normalize"
)

// Reader queries pg_stat_statements on a monitored Postgres instance
// and returns T1 (normalized) query statistics.
type Reader struct {
	pool *pgxpool.Pool
}

// NewReader returns a Reader bound to pool.
func NewReader(pool *pgxpool.Pool) *Reader { return &Reader{pool: pool} }

// Read returns the current pg_stat_statements rows, normalized.
// Rows whose query text cannot be parsed (TierBlocked) are dropped,
// not returned. The returned QueryStat values are safe to transmit.
func (r *Reader) Read(ctx context.Context) ([]*lynceusv1.QueryStat, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT query, calls, total_exec_time, mean_exec_time,
		        rows, shared_blks_hit, shared_blks_read
		   FROM pg_stat_statements
		  WHERE query IS NOT NULL`,
	)
	if err != nil {
		return nil, fmt.Errorf("query pg_stat_statements: %w", err)
	}
	defer rows.Close()

	var out []*lynceusv1.QueryStat
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
			return nil, fmt.Errorf("scan: %w", err)
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
	return out, rows.Err()
}
