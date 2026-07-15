package store

import (
	"context"
	"fmt"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// ProvisionOpts configures T2 security provisioning. UserName/UserPassword are
// the Lynceus runtime identity (parsed from LYNCEUS_CLICKHOUSE_USER_DSN); DB is
// the stats database; T2TTLDays is the query_stats_t2 retention window.
type ProvisionOpts struct {
	UserName     string
	UserPassword string
	DB           string
	T2TTLDays    int
}

// ProvisionCHSecurity applies, idempotently and as the ADMIN identity, the
// ClickHouse-layer T2 isolation for ly-cwr.6:
//
//  1. bounds query_stats_t2 retention to opts.T2TTLDays,
//  2. ensures the runtime USER exists (dev/test; a prod org may pre-create it),
//  3. grants the USER SELECT+INSERT on the stats DB, and
//  4. installs the row policy that makes the USER the ONLY identity that can
//     read query_stats_t2 rows (USING currentUser()='<user>' TO ALL — verified
//     against CH 25.8; a naive `TO <user>` does NOT deny other users).
//
// Statements run scrubbed (log_queries=0) so the CREATE USER password never
// reaches system.query_log. It is a hard error here; OpenStats decides whether
// to tolerate a failure (a locked-down prod ADMIN) by logging and continuing.
func ProvisionCHSecurity(ctx context.Context, admin driver.Conn, opts ProvisionOpts) error {
	if opts.UserName == "" || opts.DB == "" {
		return fmt.Errorf("provision ch security: UserName and DB are required")
	}
	ttl := opts.T2TTLDays
	if ttl <= 0 {
		ttl = 7
	}
	sctx := scrubbedCtx(ctx)
	stmts := []string{
		fmt.Sprintf(
			"ALTER TABLE query_stats_t2 MODIFY TTL toDateTime(collected_at) + INTERVAL %d DAY", ttl),
		fmt.Sprintf(
			"CREATE USER IF NOT EXISTS %s IDENTIFIED BY '%s'", opts.UserName, opts.UserPassword),
		fmt.Sprintf(
			"GRANT SELECT, INSERT ON %s.* TO %s", opts.DB, opts.UserName),
		fmt.Sprintf(
			"CREATE ROW POLICY OR REPLACE t2_lynceus_only ON query_stats_t2 "+
				"USING (currentUser() = '%s') TO ALL", opts.UserName),
	}
	for _, s := range stmts {
		if err := admin.Exec(sctx, s); err != nil {
			return fmt.Errorf("provision ch security: %w", err)
		}
	}
	return nil
}
