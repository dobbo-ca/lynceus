// internal/collector/xmin_horizon_reader.go
package collector

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dobbo-ca/lynceus/internal/caps"
	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
)

// XminHorizonReader reads the cluster-global oldest-xmin horizon (ly-32k): the
// AGE (in transactions) of the oldest xid still pinned by a backend, a
// replication slot, or a prepared transaction. VACUUM cannot reclaim dead rows
// newer than this horizon, so a large age means a long-running snapshot,
// abandoned slot, or orphaned prepared xact is blocking cleanup.
//
// Every returned field is literal-free: oldest_xmin_age is a non-negative age
// COUNT and holder_kind is a package-authored label
// ("backend"|"replication_slot"|"prepared_xact") emitted as a SQL string
// literal in the UNION below — it NEVER carries a replication-slot name,
// prepared-xact gid, or query text. The reader is cluster-global (no
// SchemaFilter): no schema/table identifier leaves. Read-only, catalog/stats
// views only — RDS-safe, belongs on the slow (~10m) full cadence.
type XminHorizonReader struct {
	pool *pgxpool.Pool
	gate *caps.Gate
	db   string // current_database() of pool, the gate key
}

// NewXminHorizonReader returns a reader bound to pool, gated by the capability
// gate. db is the connection's current_database().
func NewXminHorizonReader(pool *pgxpool.Pool, gate *caps.Gate, db string) *XminHorizonReader {
	return &XminHorizonReader{pool: pool, gate: gate, db: db}
}

// xminHorizonSQL returns the single oldest xmin holder across all three
// horizon sources, tagged with a fixed holder_kind label. NULLS LAST keeps a
// non-holding source from winning the ORDER BY. Zero rows when nothing pins an
// xmin — the reader ships nothing.
const xminHorizonSQL = `
SELECT holder_kind, age
  FROM (
    SELECT 'backend'          AS holder_kind, age(backend_xmin)::bigint AS age
      FROM pg_stat_activity WHERE backend_xmin IS NOT NULL
    UNION ALL
    SELECT 'replication_slot', age(x)::bigint
      FROM pg_replication_slots, LATERAL (VALUES (xmin), (catalog_xmin)) v(x)
     WHERE x IS NOT NULL
    UNION ALL
    SELECT 'prepared_xact',    age(transaction)::bigint
      FROM pg_prepared_xacts
  ) s
 ORDER BY age DESC NULLS LAST
 LIMIT 1`

// Read returns a 0-or-1-element slice: the single oldest xmin holder observed
// now, or nothing when no backend/slot/prepared-xact pins an xmin. Returns
// (nil, nil) when the XminHorizon capability is gated off — no query is issued.
func (r *XminHorizonReader) Read(ctx context.Context) ([]*lynceusv1.XminHorizon, error) {
	if !r.gate.Allowed(r.db, caps.XminHorizon) {
		return nil, nil // capability disabled: build & ship nothing
	}

	var (
		holderKind string
		age        int64
	)
	err := r.pool.QueryRow(ctx, xminHorizonSQL).Scan(&holderKind, &age)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil // nothing pins an xmin
	}
	if err != nil {
		return nil, fmt.Errorf("query xmin horizon: %w", err)
	}
	return []*lynceusv1.XminHorizon{{
		OldestXminAge: age,
		HolderKind:    holderKind,
	}}, nil
}
