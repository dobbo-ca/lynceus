// Schema-object inventory reader.
//
// Walks the monitored Postgres system catalogs and emits one
// SchemaObject per allowed namespace/relation/function/sequence.
// The SchemaFilter runs at the boundary — schemas it rejects produce
// no objects of any kind. This is the privacy mechanism for schema
// NAMES (the inventory is otherwise structural T1).
//
// first_seen_at is NOT stamped here. The collector is outbound-only and
// never connects to the stats DB, so outgoing SchemaObjects carry
// first_seen_at_unix = 0. The ingestion server resolves and persists
// first-seen server-side: the schema_objects upsert preserves it via
// ON CONFLICT (see internal/store/schema_objects.go).
package collector

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
)

// Inventory reads the structural catalog of a monitored Postgres
// instance and returns it as a slice of T1 SchemaObject messages.
type Inventory struct {
	pool   *pgxpool.Pool
	filter *SchemaFilter
}

// NewInventory binds an Inventory reader. filter must be non-nil — a
// permissive filter is created via NewSchemaFilter("", "").
func NewInventory(pool *pgxpool.Pool, filter *SchemaFilter) *Inventory {
	return &Inventory{pool: pool, filter: filter}
}

// Read returns every allowed schema, table, index, view, function,
// and sequence on the monitored database. Rejected schemas produce
// zero objects of any kind. Errors from any single sub-query abort
// the whole read; partial results are not returned.
func (i *Inventory) Read(ctx context.Context) ([]*lynceusv1.SchemaObject, error) {
	var out []*lynceusv1.SchemaObject

	schemas, err := i.readSchemas(ctx)
	if err != nil {
		return nil, err
	}
	out = append(out, schemas...)

	tables, err := i.readTables(ctx)
	if err != nil {
		return nil, err
	}
	out = append(out, tables...)

	indexes, err := i.readIndexes(ctx)
	if err != nil {
		return nil, err
	}
	out = append(out, indexes...)

	views, err := i.readViews(ctx)
	if err != nil {
		return nil, err
	}
	out = append(out, views...)

	funcs, err := i.readFunctions(ctx)
	if err != nil {
		return nil, err
	}
	out = append(out, funcs...)

	seqs, err := i.readSequences(ctx)
	if err != nil {
		return nil, err
	}
	out = append(out, seqs...)

	return out, nil
}

// readSchemas queries pg_namespace and returns allowed schema objects.
func (i *Inventory) readSchemas(ctx context.Context) ([]*lynceusv1.SchemaObject, error) {
	rows, err := i.pool.Query(ctx,
		`SELECT nspname FROM pg_namespace ORDER BY nspname`,
	)
	if err != nil {
		return nil, fmt.Errorf("query pg_namespace: %w", err)
	}
	defer rows.Close()

	var out []*lynceusv1.SchemaObject
	for rows.Next() {
		var schema string
		if err := rows.Scan(&schema); err != nil {
			return nil, err
		}
		if !i.filter.IsAllowed(schema) {
			continue
		}
		out = append(out, i.build(
			lynceusv1.ObjectKind_OBJECT_KIND_SCHEMA, schema, "", 0, false, ""))
	}
	return out, rows.Err()
}

// readTables queries pg_class for tables (including partitioned tables,
// materialized views, and foreign tables) with sizes and partition info.
func (i *Inventory) readTables(ctx context.Context) ([]*lynceusv1.SchemaObject, error) {
	rows, err := i.pool.Query(ctx,
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
	defer rows.Close()

	var out []*lynceusv1.SchemaObject
	for rows.Next() {
		var (
			schema, name string
			sz           int64
			isPart       bool
			parentFQN    string
		)
		if err := rows.Scan(&schema, &name, &sz, &isPart, &parentFQN); err != nil {
			return nil, err
		}
		if !i.filter.IsAllowed(schema) {
			continue
		}
		// If the parent lives in a filtered schema, blank it out — we
		// must not leak a filtered name through the parent_fqn field.
		if parentFQN != "" {
			parentSchema, _, _ := strings.Cut(parentFQN, ".")
			if !i.filter.IsAllowed(parentSchema) {
				parentFQN = ""
			}
		}
		out = append(out, i.build(
			lynceusv1.ObjectKind_OBJECT_KIND_TABLE, schema, name, sz, isPart, parentFQN))
	}
	return out, rows.Err()
}

// readIndexes queries pg_class for indexes with sizes.
func (i *Inventory) readIndexes(ctx context.Context) ([]*lynceusv1.SchemaObject, error) {
	rows, err := i.pool.Query(ctx,
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
	defer rows.Close()

	var out []*lynceusv1.SchemaObject
	for rows.Next() {
		var (
			schema, name string
			sz           int64
		)
		if err := rows.Scan(&schema, &name, &sz); err != nil {
			return nil, err
		}
		if !i.filter.IsAllowed(schema) {
			continue
		}
		out = append(out, i.build(
			lynceusv1.ObjectKind_OBJECT_KIND_INDEX, schema, name, sz, false, ""))
	}
	return out, rows.Err()
}

// readViews queries pg_class for views.
func (i *Inventory) readViews(ctx context.Context) ([]*lynceusv1.SchemaObject, error) {
	rows, err := i.pool.Query(ctx,
		`SELECT n.nspname, c.relname
		   FROM pg_class c
		   JOIN pg_namespace n ON n.oid = c.relnamespace
		  WHERE c.relkind = 'v'
		  ORDER BY n.nspname, c.relname`,
	)
	if err != nil {
		return nil, fmt.Errorf("query pg_class views: %w", err)
	}
	defer rows.Close()

	var out []*lynceusv1.SchemaObject
	for rows.Next() {
		var schema, name string
		if err := rows.Scan(&schema, &name); err != nil {
			return nil, err
		}
		if !i.filter.IsAllowed(schema) {
			continue
		}
		out = append(out, i.build(
			lynceusv1.ObjectKind_OBJECT_KIND_VIEW, schema, name, 0, false, ""))
	}
	return out, rows.Err()
}

// readFunctions queries pg_proc for functions.
func (i *Inventory) readFunctions(ctx context.Context) ([]*lynceusv1.SchemaObject, error) {
	rows, err := i.pool.Query(ctx,
		`SELECT n.nspname, p.proname
		   FROM pg_proc p
		   JOIN pg_namespace n ON n.oid = p.pronamespace
		  ORDER BY n.nspname, p.proname`,
	)
	if err != nil {
		return nil, fmt.Errorf("query pg_proc: %w", err)
	}
	defer rows.Close()

	var out []*lynceusv1.SchemaObject
	for rows.Next() {
		var schema, name string
		if err := rows.Scan(&schema, &name); err != nil {
			return nil, err
		}
		if !i.filter.IsAllowed(schema) {
			continue
		}
		out = append(out, i.build(
			lynceusv1.ObjectKind_OBJECT_KIND_FUNCTION, schema, name, 0, false, ""))
	}
	return out, rows.Err()
}

// readSequences queries pg_class for sequences.
func (i *Inventory) readSequences(ctx context.Context) ([]*lynceusv1.SchemaObject, error) {
	rows, err := i.pool.Query(ctx,
		`SELECT n.nspname, c.relname
		   FROM pg_class c
		   JOIN pg_namespace n ON n.oid = c.relnamespace
		  WHERE c.relkind = 'S'
		  ORDER BY n.nspname, c.relname`,
	)
	if err != nil {
		return nil, fmt.Errorf("query pg_class sequences: %w", err)
	}
	defer rows.Close()

	var out []*lynceusv1.SchemaObject
	for rows.Next() {
		var schema, name string
		if err := rows.Scan(&schema, &name); err != nil {
			return nil, err
		}
		if !i.filter.IsAllowed(schema) {
			continue
		}
		out = append(out, i.build(
			lynceusv1.ObjectKind_OBJECT_KIND_SEQUENCE, schema, name, 0, false, ""))
	}
	return out, rows.Err()
}

// build constructs a SchemaObject. first_seen_at_unix is left 0: the
// collector never connects to the stats DB, so first-seen is resolved
// server-side by the ingestion upsert (ON CONFLICT preserves it).
func (i *Inventory) build(
	kind lynceusv1.ObjectKind,
	schema, name string,
	sizeBytes int64,
	isPartition bool,
	parentFQN string,
) *lynceusv1.SchemaObject {
	fqn := schema + "." + name // for kind=SCHEMA, name is "" → "schema."
	return &lynceusv1.SchemaObject{
		Kind:        kind,
		Schema:      schema,
		Name:        name,
		Fqn:         fqn,
		SizeBytes:   sizeBytes,
		IsPartition: isPartition,
		ParentFqn:   parentFQN,
	}
}
