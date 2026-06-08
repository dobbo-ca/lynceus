package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dobbo-ca/lynceus/internal/caps"
)

// DiscoveredCapability is one row of discovered_capability: whether a
// capability is available on a server (DatabaseName == "" for the
// server-level / connection-database probe) or for a specific database,
// plus the package-authored reason and when it was observed.
type DiscoveredCapability struct {
	ServerID     string
	DatabaseName string // "" means the server-level probe (stored as SQL NULL)
	Capability   string
	Available    bool
	Reason       string
	ObservedAt   time.Time
}

// DiscoveredCapabilities is typed access to the discovered_capability
// table. It uses the same primary/read-replica pool split as Config.
type DiscoveredCapabilities struct {
	pool *pgxpool.Pool
	ro   *pgxpool.Pool
}

// NewDiscoveredCapabilities returns a store bound to its primary pool;
// standalone reads fall back to the primary until a replica is attached
// via WithReadPool.
func NewDiscoveredCapabilities(pool *pgxpool.Pool) *DiscoveredCapabilities {
	return &DiscoveredCapabilities{pool: pool, ro: pool}
}

// WithReadPool attaches a read-replica pool used to serve
// ListDiscoveredCapabilities. A nil ro is ignored. Returns the receiver
// for chaining.
func (d *DiscoveredCapabilities) WithReadPool(ro *pgxpool.Pool) *DiscoveredCapabilities {
	if ro != nil {
		d.ro = ro
	}
	return d
}

// UpsertDiscoveredCapabilities persists one caps.Discover result. Each
// entry in set becomes (or refreshes) a discovered_capability row keyed
// by (server_id, database_name, capability); observed_at is refreshed to
// now() on every call. databaseName == "" is stored as SQL NULL.
func (d *DiscoveredCapabilities) UpsertDiscoveredCapabilities(ctx context.Context, serverID, databaseName string, set caps.Set) error {
	if serverID == "" {
		return fmt.Errorf("UpsertDiscoveredCapabilities: serverID required")
	}
	var dbArg any
	if databaseName != "" {
		dbArg = databaseName
	}

	tx, err := d.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for capability, status := range set {
		if _, err := tx.Exec(ctx,
			`INSERT INTO discovered_capability
			   (server_id, database_name, capability, available, reason, observed_at)
			 VALUES ($1, $2, $3, $4, $5, now())
			 ON CONFLICT (server_id, database_name, capability)
			 DO UPDATE SET
			   available   = EXCLUDED.available,
			   reason      = EXCLUDED.reason,
			   observed_at = EXCLUDED.observed_at`,
			serverID, dbArg, string(capability), status.Available, status.Reason,
		); err != nil {
			return fmt.Errorf("upsert %s: %w", capability, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// ListDiscoveredCapabilities returns every discovered_capability row for
// one server, ordered for stable display (by capability, then
// server-level rows before per-database rows, then database_name).
func (d *DiscoveredCapabilities) ListDiscoveredCapabilities(ctx context.Context, serverID string) ([]DiscoveredCapability, error) {
	rows, err := d.ro.Query(ctx,
		`SELECT server_id, database_name, capability, available, reason, observed_at
		   FROM discovered_capability
		  WHERE server_id = $1
		  ORDER BY capability, (database_name IS NOT NULL), database_name`,
		serverID)
	if err != nil {
		return nil, fmt.Errorf("list discovered capabilities: %w", err)
	}
	defer rows.Close()

	var out []DiscoveredCapability
	for rows.Next() {
		var (
			dc     DiscoveredCapability
			dbName *string
		)
		if err := rows.Scan(&dc.ServerID, &dbName, &dc.Capability,
			&dc.Available, &dc.Reason, &dc.ObservedAt); err != nil {
			return nil, fmt.Errorf("scan discovered capability: %w", err)
		}
		if dbName != nil {
			dc.DatabaseName = *dbName
		}
		out = append(out, dc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate discovered capabilities: %w", err)
	}
	return out, nil
}
