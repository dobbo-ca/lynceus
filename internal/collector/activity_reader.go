// internal/collector/activity_reader.go
package collector

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dobbo-ca/lynceus/internal/caps"
)

// ActivityReader samples pg_stat_activity on a monitored Postgres and
// returns one ActivitySample per (database, state, wait_event_type,
// wait_event) tuple — just counts and labels.
//
// The query DELIBERATELY does not select `query`, `query_id`, or any
// other column that could carry literal-bearing content. Live query
// text is the separate T2 "connection traces" feature (ly-xqf.4) and is
// not part of this reader's contract. Keeping the column list narrow is
// the SQL-layer half of the defense-in-depth privacy guarantee; the
// proto-layer half lives in internal/proto/lynceus/v1/contract_test.go.
//
// Backends with state=NULL (e.g. autovacuum workers) are folded into
// the empty-string "" state so they remain visible as a count without
// becoming a separate sparse column.
type ActivityReader struct {
	pool *pgxpool.Pool
	gate *caps.Gate
	db   string // current_database() of pool, used as the gate key
}

// NewActivityReader returns a reader bound to pool. gate is consulted
// before every Read; db is the connection's current_database().
func NewActivityReader(pool *pgxpool.Pool, gate *caps.Gate, db string) *ActivityReader {
	return &ActivityReader{pool: pool, gate: gate, db: db}
}

// Read returns one ActivitySample per (database, state, wait_event_type,
// wait_event) tuple observed in pg_stat_activity at call time.
func (r *ActivityReader) Read(ctx context.Context) ([]ActivitySample, error) {
	// One connection cannot pre-filter other databases' pg_stat_activity
	// rows, so the activity capability is gated once with the connection's
	// own database — effectively server-scoped (spec §4.4.2 B4).
	if !r.gate.Allowed(r.db, caps.PgStatActivityFullRead) {
		return nil, nil // capability disabled: build & ship nothing
	}
	rows, err := r.pool.Query(ctx,
		`SELECT COALESCE(datname, '')        AS database_name,
		        COALESCE(state, '')          AS state,
		        COALESCE(wait_event_type,'') AS wait_event_type,
		        COALESCE(wait_event,'')      AS wait_event,
		        count(*)                     AS connections
		   FROM pg_stat_activity
		  WHERE backend_type = 'client backend'
		  GROUP BY 1, 2, 3, 4`,
	)
	if err != nil {
		return nil, fmt.Errorf("query pg_stat_activity: %w", err)
	}
	defer rows.Close()

	var out []ActivitySample
	for rows.Next() {
		var s ActivitySample
		if err := rows.Scan(
			&s.Database, &s.State, &s.WaitEventType, &s.WaitEvent, &s.Count,
		); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
