// Schema-object inventory reader.
//
// Walks the monitored Postgres system catalogs and emits one
// SchemaObject per allowed namespace/relation/function/sequence.
// The SchemaFilter runs at the boundary — schemas it rejects produce
// no objects of any kind. This is the privacy mechanism for schema
// NAMES (the inventory is otherwise structural T1).
//
// First-seen timestamps are sourced from the stats DB via a small
// interface so the reader is unit-testable without that dependency.
package collector

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
)

// FirstSeenLookup returns the persisted first_seen_at for a given
// object, or the zero Time if it has never been recorded. The
// production implementation is store.SchemaObjects.FirstSeenAt — wired
// in cmd/collector/main.go with a small adapter that converts
// lynceusv1.ObjectKind ↔ int16.
type FirstSeenLookup interface {
	FirstSeenAt(ctx context.Context, serverID string, kind lynceusv1.ObjectKind, fqn string) (time.Time, error)
}

// Inventory reads the structural catalog of a monitored Postgres
// instance and returns it as a slice of T1 SchemaObject messages.
type Inventory struct {
	pool      *pgxpool.Pool
	filter    *SchemaFilter
	firstSeen FirstSeenLookup
}

// NewInventory binds an Inventory reader. filter must be non-nil — a
// permissive filter is created via NewSchemaFilter("", "").
func NewInventory(pool *pgxpool.Pool, filter *SchemaFilter, firstSeen FirstSeenLookup) *Inventory {
	return &Inventory{pool: pool, filter: filter, firstSeen: firstSeen}
}

// Read returns every allowed schema, table, index, view, function,
// and sequence on the monitored database. Rejected schemas produce
// zero objects of any kind. Errors from any single sub-query abort
// the whole read; partial results are not returned.
func (i *Inventory) Read(ctx context.Context, serverID string) ([]*lynceusv1.SchemaObject, error) {
	var out []*lynceusv1.SchemaObject

	// 1. Schemas.
	rows, err := i.pool.Query(ctx,
		`SELECT nspname FROM pg_namespace ORDER BY nspname`,
	)
	if err != nil {
		return nil, fmt.Errorf("query pg_namespace: %w", err)
	}
	for rows.Next() {
		var schema string
		if err := rows.Scan(&schema); err != nil {
			rows.Close()
			return nil, err
		}
		if !i.filter.IsAllowed(schema) {
			continue
		}
		out = append(out, i.build(ctx, serverID,
			lynceusv1.ObjectKind_OBJECT_KIND_SCHEMA, schema, "", 0, false, ""))
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	// 2. Tables (including partitioned tables and materialized views) +
	//    sizes via pg_total_relation_size. relkind: r=table, p=partitioned,
	//    m=materialized view, f=foreign. f is included as a "table-like"
	//    inventory entry but size will be 0.
	rows, err = i.pool.Query(ctx,
		`SELECT n.nspname, c.relname,
		        COALESCE(pg_total_relation_size(c.oid), 0)::bigint AS sz,
		        c.relispartition,
		        COALESCE(pn.nspname || '.' || pc.relname, '') AS parent_fqn
		   FROM pg_class c
		   JOIN pg_namespace n ON n.oid = c.relnamespace
		   LEFT JOIN pg_inherits i ON i.inhrelid = c.oid
		   LEFT JOIN pg_class    pc ON pc.oid = i.inhparent
		   LEFT JOIN pg_namespace pn ON pn.oid = pc.relnamespace
		  WHERE c.relkind IN ('r','p','m','f')
		  ORDER BY n.nspname, c.relname`,
	)
	if err != nil {
		return nil, fmt.Errorf("query pg_class tables: %w", err)
	}
	for rows.Next() {
		var (
			schema, name string
			sz           int64
			isPart       bool
			parentFQN    string
		)
		if err := rows.Scan(&schema, &name, &sz, &isPart, &parentFQN); err != nil {
			rows.Close()
			return nil, err
		}
		if !i.filter.IsAllowed(schema) {
			continue
		}
		// If the parent lives in a filtered schema, blank it out — we
		// must not leak a filtered name through the parent_fqn field.
		if parentFQN != "" {
			parentSchema := parentFQN
			for j := 0; j < len(parentFQN); j++ {
				if parentFQN[j] == '.' {
					parentSchema = parentFQN[:j]
					break
				}
			}
			if !i.filter.IsAllowed(parentSchema) {
				parentFQN = ""
			}
		}
		out = append(out, i.build(ctx, serverID,
			lynceusv1.ObjectKind_OBJECT_KIND_TABLE, schema, name, sz, isPart, parentFQN))
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	// 3. Indexes (relkind='i') with pg_relation_size.
	rows, err = i.pool.Query(ctx,
		`SELECT n.nspname, c.relname,
		        COALESCE(pg_relation_size(c.oid), 0)::bigint AS sz
		   FROM pg_class c
		   JOIN pg_namespace n ON n.oid = c.relnamespace
		  WHERE c.relkind = 'i'
		  ORDER BY n.nspname, c.relname`,
	)
	if err != nil {
		return nil, fmt.Errorf("query pg_class indexes: %w", err)
	}
	for rows.Next() {
		var (
			schema, name string
			sz           int64
		)
		if err := rows.Scan(&schema, &name, &sz); err != nil {
			rows.Close()
			return nil, err
		}
		if !i.filter.IsAllowed(schema) {
			continue
		}
		out = append(out, i.build(ctx, serverID,
			lynceusv1.ObjectKind_OBJECT_KIND_INDEX, schema, name, sz, false, ""))
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	// 4. Views (relkind='v'). Size meaningless → 0.
	rows, err = i.pool.Query(ctx,
		`SELECT n.nspname, c.relname
		   FROM pg_class c
		   JOIN pg_namespace n ON n.oid = c.relnamespace
		  WHERE c.relkind = 'v'
		  ORDER BY n.nspname, c.relname`,
	)
	if err != nil {
		return nil, fmt.Errorf("query pg_class views: %w", err)
	}
	for rows.Next() {
		var schema, name string
		if err := rows.Scan(&schema, &name); err != nil {
			rows.Close()
			return nil, err
		}
		if !i.filter.IsAllowed(schema) {
			continue
		}
		out = append(out, i.build(ctx, serverID,
			lynceusv1.ObjectKind_OBJECT_KIND_VIEW, schema, name, 0, false, ""))
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	// 5. Functions (pg_proc).
	rows, err = i.pool.Query(ctx,
		`SELECT n.nspname, p.proname
		   FROM pg_proc p
		   JOIN pg_namespace n ON n.oid = p.pronamespace
		  ORDER BY n.nspname, p.proname`,
	)
	if err != nil {
		return nil, fmt.Errorf("query pg_proc: %w", err)
	}
	for rows.Next() {
		var schema, name string
		if err := rows.Scan(&schema, &name); err != nil {
			rows.Close()
			return nil, err
		}
		if !i.filter.IsAllowed(schema) {
			continue
		}
		out = append(out, i.build(ctx, serverID,
			lynceusv1.ObjectKind_OBJECT_KIND_FUNCTION, schema, name, 0, false, ""))
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	// 6. Sequences (relkind='S').
	rows, err = i.pool.Query(ctx,
		`SELECT n.nspname, c.relname
		   FROM pg_class c
		   JOIN pg_namespace n ON n.oid = c.relnamespace
		  WHERE c.relkind = 'S'
		  ORDER BY n.nspname, c.relname`,
	)
	if err != nil {
		return nil, fmt.Errorf("query pg_class sequences: %w", err)
	}
	for rows.Next() {
		var schema, name string
		if err := rows.Scan(&schema, &name); err != nil {
			rows.Close()
			return nil, err
		}
		if !i.filter.IsAllowed(schema) {
			continue
		}
		out = append(out, i.build(ctx, serverID,
			lynceusv1.ObjectKind_OBJECT_KIND_SEQUENCE, schema, name, 0, false, ""))
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	return out, nil
}

// build constructs a SchemaObject, stamping first_seen_at from the
// FirstSeenLookup (falling back to now() when the lookup has no
// record — the upsert path will then persist that timestamp).
func (i *Inventory) build(
	ctx context.Context,
	serverID string,
	kind lynceusv1.ObjectKind,
	schema, name string,
	sizeBytes int64,
	isPartition bool,
	parentFQN string,
) *lynceusv1.SchemaObject {
	fqn := schema + "." + name // for kind=SCHEMA, name is "" → "schema."

	firstSeen := time.Time{}
	if i.firstSeen != nil {
		// Best-effort: a lookup error must not block the inventory.
		// The collector caller can re-stamp on the next snapshot.
		if t, err := i.firstSeen.FirstSeenAt(ctx, serverID, kind, fqn); err == nil {
			firstSeen = t
		}
	}
	if firstSeen.IsZero() {
		firstSeen = time.Now().UTC()
	}

	return &lynceusv1.SchemaObject{
		Kind:            kind,
		Schema:          schema,
		Name:            name,
		Fqn:             fqn,
		SizeBytes:       sizeBytes,
		IsPartition:     isPartition,
		ParentFqn:       parentFQN,
		FirstSeenAtUnix: firstSeen.Unix(),
	}
}
