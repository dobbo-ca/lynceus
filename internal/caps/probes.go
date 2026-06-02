package caps

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// extensionsOfInterest maps the catalog name in pg_extension to the
// Capability we attribute installation to. Keep in sync with
// Declared().
var extensionsOfInterest = map[string]Capability{
	"pg_stat_statements": PgStatStatements,
	"pg_buffercache":     PgBuffercache,
	"pg_wait_sampling":   PgWaitSampling,
	"pgstattuple":        PgStatTuple,
}

// ProbeExtensions writes one Status entry into out for each extension
// declared in extensionsOfInterest. Available=true iff the extension
// has a row in pg_extension on the connected database; Reason carries
// the extversion when available, otherwise "not installed" / a probe
// error message.
//
// Writes occur unconditionally — every key from extensionsOfInterest is
// always set, so Discover's completeness invariant holds even when this
// probe encounters errors.
func ProbeExtensions(ctx context.Context, pool *pgxpool.Pool, out Set) {
	versions := map[string]string{}
	rows, err := pool.Query(ctx,
		`SELECT extname, extversion FROM pg_extension`,
	)
	if err != nil {
		for _, cap := range extensionsOfInterest {
			out[cap] = Status{
				Available: false,
				Reason:    fmt.Sprintf("probe error: %s", err.Error()),
			}
		}
		return
	}
	defer rows.Close()

	for rows.Next() {
		var name, ver string
		if err := rows.Scan(&name, &ver); err != nil {
			continue
		}
		versions[name] = ver
	}

	for extname, cap := range extensionsOfInterest {
		if ver, ok := versions[extname]; ok {
			out[cap] = Status{
				Available: true,
				Reason:    fmt.Sprintf("extversion=%s", ver),
			}
		} else {
			out[cap] = Status{
				Available: false,
				Reason:    "not installed",
			}
		}
	}
}

// ProbeServerVersion writes a ServerVersion entry into out. Available
// requires server_version_num >= 12_0000 (Lynceus's supported baseline).
func ProbeServerVersion(ctx context.Context, pool *pgxpool.Pool, out Set) {
	var raw string
	err := pool.QueryRow(ctx, `SHOW server_version_num`).Scan(&raw)
	if err != nil {
		out[ServerVersion] = Status{
			Available: false,
			Reason:    fmt.Sprintf("probe error: %s", err.Error()),
		}
		return
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		out[ServerVersion] = Status{
			Available: false,
			Reason:    fmt.Sprintf("parse error: %q", raw),
		}
		return
	}
	if n < 12_0000 {
		out[ServerVersion] = Status{
			Available: false,
			Reason:    fmt.Sprintf("server_version_num=%d below baseline 120000", n),
		}
		return
	}
	out[ServerVersion] = Status{
		Available: true,
		Reason:    fmt.Sprintf("server_version_num=%d", n),
	}
}

// ProbeRolePermissions writes a RolePermissions entry into out. Available
// requires at least pg_monitor membership (Lynceus's minimum collector
// role). Reason carries a comma-separated list of every membership we
// checked, true or false, so the operator can see exactly what the
// collector role can do.
func ProbeRolePermissions(ctx context.Context, pool *pgxpool.Pool, out Set) {
	type check struct {
		label string
		query string
	}
	checks := []check{
		{"pg_monitor", `SELECT pg_has_role(current_user, 'pg_monitor', 'MEMBER')`},
		{"pg_read_all_stats", `SELECT pg_has_role(current_user, 'pg_read_all_stats', 'MEMBER')`},
		{"pg_read_server_files", `SELECT pg_has_role(current_user, 'pg_read_server_files', 'MEMBER')`},
		{"rolsuper", `SELECT rolsuper FROM pg_roles WHERE rolname = current_user`},
	}
	parts := make([]string, 0, len(checks))
	var monitor, super bool
	for _, c := range checks {
		var got bool
		if err := pool.QueryRow(ctx, c.query).Scan(&got); err != nil {
			parts = append(parts, fmt.Sprintf("%s=err(%s)", c.label, err.Error()))
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%t", c.label, got))
		switch c.label {
		case "pg_monitor":
			monitor = got
		case "rolsuper":
			super = got
		}
	}
	out[RolePermissions] = Status{
		Available: monitor || super,
		Reason:    strings.Join(parts, ","),
	}
}

// ProbeStatActivityFullRead writes a PgStatActivityFullRead entry into
// out. Available iff the connected role can see queries from other
// backends — operationally, iff pg_has_role(current_user,
// 'pg_read_all_stats','MEMBER') OR rolsuper.
//
// This is the visibility property other readers care about (the wait
// events and connection-state readers degrade gracefully if the role
// can only see its own backend rows).
func ProbeStatActivityFullRead(ctx context.Context, pool *pgxpool.Pool, out Set) {
	var hasRead, isSuper bool
	if err := pool.QueryRow(ctx,
		`SELECT pg_has_role(current_user, 'pg_read_all_stats', 'MEMBER')`,
	).Scan(&hasRead); err != nil {
		out[PgStatActivityFullRead] = Status{
			Available: false,
			Reason:    fmt.Sprintf("probe error: %s", err.Error()),
		}
		return
	}
	_ = pool.QueryRow(ctx,
		`SELECT rolsuper FROM pg_roles WHERE rolname = current_user`,
	).Scan(&isSuper)
	out[PgStatActivityFullRead] = Status{
		Available: hasRead || isSuper,
		Reason:   fmt.Sprintf("pg_read_all_stats=%t,rolsuper=%t", hasRead, isSuper),
	}
}
