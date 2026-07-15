package store

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/ClickHouse/clickhouse-go/v2"
)

// OpenStats builds the stats backend selected by the REQUIRED
// LYNCEUS_STATS_BACKEND env var (there is no default — every deployment must
// choose). ClickHouse is the sole stats backend, reached with two identities:
//
//   - "clickhouse":
//   - LYNCEUS_CLICKHOUSE_USER_DSN (required) — the runtime identity for all
//     reads/writes; the only identity permitted to read query_stats_t2.
//   - LYNCEUS_CLICKHOUSE_ADMIN_DSN (optional) — DDL + one-time T2 security
//     provisioning; falls back to the USER DSN when unset.
//   - LYNCEUS_CLICKHOUSE_T2_TTL_DAYS (default 7) — query_stats_t2 retention.
//
// Migrations are applied and T2 security is provisioned (via ADMIN) before the
// handle is returned. The underlying connection is owned for the process
// lifetime (closed by the OS on exit), matching the config-pool convention in
// the service mains.
//
// TLS: the ClickHouse DSN carries its own TLS setting
// (clickhouse://…?secure=true); it is not run through secure.CheckDatabaseDSN,
// which is libpq/sslmode-specific.
func OpenStats(ctx context.Context) (Stats, error) {
	switch backend := os.Getenv("LYNCEUS_STATS_BACKEND"); backend {
	case "clickhouse":
		userDSN := os.Getenv("LYNCEUS_CLICKHOUSE_USER_DSN")
		if userDSN == "" {
			return nil, errors.New("LYNCEUS_CLICKHOUSE_USER_DSN required for clickhouse backend")
		}
		userOpts, err := clickhouse.ParseDSN(userDSN)
		if err != nil {
			return nil, fmt.Errorf("parse LYNCEUS_CLICKHOUSE_USER_DSN: %w", err)
		}

		// Bootstrap (DDL + provisioning) runs as ADMIN when provided; otherwise
		// fall back to USER for simple single-identity dev where USER has DDL rights.
		bootstrapDSN := os.Getenv("LYNCEUS_CLICKHOUSE_ADMIN_DSN")
		if bootstrapDSN == "" {
			bootstrapDSN = userDSN
		}
		bootOpts, err := clickhouse.ParseDSN(bootstrapDSN)
		if err != nil {
			return nil, fmt.Errorf("parse LYNCEUS_CLICKHOUSE_ADMIN_DSN: %w", err)
		}
		bootConn, err := clickhouse.Open(bootOpts)
		if err != nil {
			return nil, err
		}
		if err := ApplyClickHouseMigrations(ctx, bootConn); err != nil {
			_ = bootConn.Close()
			return nil, err
		}
		// T2 security is best-effort: a locked-down prod ADMIN may lack rights —
		// log and continue so reads still work, rather than failing startup.
		if err := ProvisionCHSecurity(ctx, bootConn, ProvisionOpts{
			UserName:     userOpts.Auth.Username,
			UserPassword: userOpts.Auth.Password,
			DB:           userOpts.Auth.Database,
			T2TTLDays:    t2TTLDaysFromEnv(),
		}); err != nil {
			log.Printf("WARNING: ClickHouse T2 security provisioning failed; T2 RLS may not be enforced: %v", err)
		}
		_ = bootConn.Close()

		userConn, err := clickhouse.Open(userOpts)
		if err != nil {
			return nil, err
		}
		return NewCHStats(userConn), nil
	case "":
		return nil, errors.New("LYNCEUS_STATS_BACKEND required (clickhouse)")
	default:
		return nil, fmt.Errorf("unknown LYNCEUS_STATS_BACKEND %q (want clickhouse)", backend)
	}
}

// t2TTLDaysFromEnv reads LYNCEUS_CLICKHOUSE_T2_TTL_DAYS (default 7).
func t2TTLDaysFromEnv() int {
	if v := os.Getenv("LYNCEUS_CLICKHOUSE_T2_TTL_DAYS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 7
}
