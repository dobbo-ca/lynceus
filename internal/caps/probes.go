package caps

import (
	"context"
	"fmt"

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
