// internal/collector/freeze_age_reader.go
package collector

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dobbo-ca/lynceus/internal/caps"
	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
)

// FreezeAgeReader reads per-database and per-table transaction-id / MultiXact
// freeze AGES from pg_database + pg_class on a monitored Postgres. Every
// returned field is a catalog identifier or a non-negative age COUNT — never a
// raw xid or column value, preserving the T1 privacy contract.
//
// age(relfrozenxid)/age(datfrozenxid) is the distance (in transactions) from
// the oldest unfrozen xid to the current one; mxid_age the MultiXact analog.
// The wraparound check (ly-u4t.26) compares these against the ~2.1B ceiling.
// Read-only, ACCESS-SHARE catalog locks only — RDS-safe, belongs on the slow
// (~10m) full cadence.
//
// filter is the SAME SchemaFilter instance shared with the other catalog
// readers: a schema excluded by ignore_schema_regexp produces zero table-scope
// rows, keeping the redaction boundary identical across readers.
type FreezeAgeReader struct {
	pool   *pgxpool.Pool
	filter *SchemaFilter
	gate   *caps.Gate
	db     string // current_database() of pool, the gate key
}

// NewFreezeAgeReader returns a reader bound to pool, gated by filter and the
// capability gate. db is the connection's current_database().
func NewFreezeAgeReader(pool *pgxpool.Pool, filter *SchemaFilter, gate *caps.Gate, db string) *FreezeAgeReader {
	return &FreezeAgeReader{pool: pool, filter: filter, gate: gate, db: db}
}

// freezeDBSQL returns the freeze ages for the connection's own database.
const freezeDBSQL = `
SELECT datname,
       age(datfrozenxid)::bigint    AS xid_age,
       mxid_age(datminmxid)::bigint AS mxid_age
  FROM pg_database WHERE datname = current_database()`

// freezeTableSQL returns the freeze ages for ordinary/matview/toast tables.
const freezeTableSQL = `
SELECT n.nspname, c.relname,
       age(c.relfrozenxid)::bigint    AS xid_age,
       mxid_age(c.relminmxid)::bigint AS mxid_age
  FROM pg_class c
  JOIN pg_namespace n ON n.oid = c.relnamespace
 WHERE c.relkind IN ('r','m','t')
   AND n.nspname NOT IN ('pg_catalog','information_schema','pg_toast')
 ORDER BY n.nspname, c.relname`

// freezeMaxAgeSQL reads the autovacuum_freeze_max_age setting (a count),
// attached to every returned row for headroom computation.
const freezeMaxAgeSQL = `SELECT current_setting('autovacuum_freeze_max_age')::bigint`

// Read returns one FreezeAge per database (the current one) plus one per
// allowed ordinary/matview/toast table. Rows whose schema is excluded by the
// SchemaFilter are skipped. autovacuum_freeze_max_age is attached to each row.
func (r *FreezeAgeReader) Read(ctx context.Context, serverID string) ([]*lynceusv1.FreezeAge, error) {
	_ = serverID // reserved for future per-server scoping; identifiers are server-agnostic here
	if !r.gate.Allowed(r.db, caps.FreezeAge) {
		return nil, nil // capability disabled: build & ship nothing
	}

	var maxAge int64
	if err := r.pool.QueryRow(ctx, freezeMaxAgeSQL).Scan(&maxAge); err != nil {
		return nil, fmt.Errorf("read autovacuum_freeze_max_age: %w", err)
	}

	var out []*lynceusv1.FreezeAge

	// Database scope: one row for the connection's own database.
	{
		dbRows, err := r.pool.Query(ctx, freezeDBSQL)
		if err != nil {
			return nil, fmt.Errorf("query database freeze ages: %w", err)
		}
		for dbRows.Next() {
			var (
				datname          string
				xidAge, mxidAge  int64
			)
			if err := dbRows.Scan(&datname, &xidAge, &mxidAge); err != nil {
				dbRows.Close()
				return nil, fmt.Errorf("scan database freeze age: %w", err)
			}
			out = append(out, &lynceusv1.FreezeAge{
				Scope:                  "database",
				Schema:                 "",
				Name:                   datname,
				Fqn:                    datname,
				XidAge:                 xidAge,
				MxidAge:                mxidAge,
				AutovacuumFreezeMaxAge: maxAge,
			})
		}
		dbRows.Close()
		if err := dbRows.Err(); err != nil {
			return nil, err
		}
	}

	// Table scope: one row per allowed table/matview/toast.
	tblRows, err := r.pool.Query(ctx, freezeTableSQL)
	if err != nil {
		return nil, fmt.Errorf("query table freeze ages: %w", err)
	}
	defer tblRows.Close()
	for tblRows.Next() {
		var (
			schema, name    string
			xidAge, mxidAge int64
		)
		if err := tblRows.Scan(&schema, &name, &xidAge, &mxidAge); err != nil {
			return nil, fmt.Errorf("scan table freeze age: %w", err)
		}
		if !r.filter.IsAllowed(schema) {
			continue
		}
		out = append(out, &lynceusv1.FreezeAge{
			Scope:                  "table",
			Schema:                 schema,
			Name:                   name,
			Fqn:                    schema + "." + name,
			XidAge:                 xidAge,
			MxidAge:                mxidAge,
			AutovacuumFreezeMaxAge: maxAge,
		})
	}
	return out, tblRows.Err()
}
