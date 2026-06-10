// internal/collector/connections_reader.go
package collector

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dobbo-ca/lynceus/internal/caps"
)

// ConnectionsReader samples per-backend durations and lock-wait relationships
// from pg_stat_activity / pg_blocking_pids() for the Connections checks
// (ly-u4t.22). Like ActivityReader, the column lists DELIBERATELY exclude
// `query`, `query_id`, and every other literal-bearing column: only the backend
// pid, a fixed state label, integer durations, and wait_event_type travel. Live
// query text is the separate T2 connection-traces feature (ly-xqf.4).
type ConnectionsReader struct {
	pool *pgxpool.Pool
	gate *caps.Gate
	db   string // current_database() of pool, the gate key
}

// ConnectionSample is one point-in-time backend observation (durations in
// whole seconds). It carries no literal.
type ConnectionSample struct {
	PID           int64
	State         string
	ActiveSeconds int64
	XactSeconds   int64
	StateSeconds  int64
	WaitEventType string
}

// BlockingPair is one A→B lock-wait relationship; pids only.
type BlockingPair struct {
	BlockedPID         int64
	BlockerPID         int64
	BlockedWaitSeconds int64
}

// NewConnectionsReader returns a reader bound to pool. gate is consulted before
// every Read; db is the connection's current_database().
func NewConnectionsReader(pool *pgxpool.Pool, gate *caps.Gate, db string) *ConnectionsReader {
	return &ConnectionsReader{pool: pool, gate: gate, db: db}
}

// connSamplesSQL selects "notable" client backends only: those whose current
// state has held for >1s. The 1s floor is a privacy-neutral performance bound
// (sheds the high-churn short-query tail) far below any check threshold.
const connSamplesSQL = `
SELECT pid,
       COALESCE(state, '')                                              AS state,
       COALESCE(EXTRACT(EPOCH FROM (now() - query_start))::bigint, 0)   AS active_seconds,
       COALESCE(EXTRACT(EPOCH FROM (now() - xact_start))::bigint, 0)    AS xact_seconds,
       COALESCE(EXTRACT(EPOCH FROM (now() - state_change))::bigint, 0)  AS state_seconds,
       COALESCE(wait_event_type, '')                                    AS wait_event_type
  FROM pg_stat_activity
 WHERE backend_type = 'client backend'
   AND state IN ('active', 'idle in transaction', 'idle in transaction (aborted)')
   AND state_change < now() - interval '1 second'
 ORDER BY state_change ASC
 LIMIT 500`

// connBlockingSQL derives A→B edges from pg_blocking_pids(). pids only.
const connBlockingSQL = `
SELECT blocked.pid                                                       AS blocked_pid,
       bp.pid                                                            AS blocker_pid,
       COALESCE(EXTRACT(EPOCH FROM (now() - blocked.state_change))::bigint, 0) AS blocked_wait_seconds
  FROM pg_stat_activity blocked
  CROSS JOIN LATERAL unnest(pg_blocking_pids(blocked.pid)) AS bp(pid)
 WHERE blocked.backend_type = 'client backend'
 LIMIT 500`

// Read returns notable backend samples and blocking pairs observed now. Returns
// (nil, nil, nil) when the pg_stat_activity capability is gated off — identical
// to ActivityReader.Read.
func (r *ConnectionsReader) Read(ctx context.Context) ([]ConnectionSample, []BlockingPair, error) {
	if !r.gate.Allowed(r.db, caps.PgStatActivityFullRead) {
		return nil, nil, nil
	}

	sRows, err := r.pool.Query(ctx, connSamplesSQL)
	if err != nil {
		return nil, nil, fmt.Errorf("query connection samples: %w", err)
	}
	var samples []ConnectionSample
	for sRows.Next() {
		var s ConnectionSample
		if err := sRows.Scan(&s.PID, &s.State, &s.ActiveSeconds, &s.XactSeconds, &s.StateSeconds, &s.WaitEventType); err != nil {
			sRows.Close()
			return nil, nil, fmt.Errorf("scan connection sample: %w", err)
		}
		samples = append(samples, s)
	}
	sRows.Close()
	if err := sRows.Err(); err != nil {
		return nil, nil, err
	}

	bRows, err := r.pool.Query(ctx, connBlockingSQL)
	if err != nil {
		return nil, nil, fmt.Errorf("query blocking edges: %w", err)
	}
	defer bRows.Close()
	var edges []BlockingPair
	for bRows.Next() {
		var e BlockingPair
		if err := bRows.Scan(&e.BlockedPID, &e.BlockerPID, &e.BlockedWaitSeconds); err != nil {
			return nil, nil, fmt.Errorf("scan blocking edge: %w", err)
		}
		edges = append(edges, e)
	}
	return samples, edges, bRows.Err()
}
