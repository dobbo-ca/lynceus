package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/dobbo-ca/lynceus/internal/testch"
)

const (
	chUser = "lynceus_user"
	chPass = "user-pw"
)

// provision migrates the isolated test DB and provisions T2 security, returning
// the admin conn, the isolated-DB dsn, and the db name.
func provision(t *testing.T, ttlDays int) (driver.Conn, string, string) {
	t.Helper()
	ctx := context.Background()
	admin, dsn := testch.StartDSN(t)
	if err := store.ApplyClickHouseMigrations(ctx, admin); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	db := dbFromDSN(t, dsn)
	if err := store.ProvisionCHSecurity(ctx, admin, store.ProvisionOpts{
		UserName: chUser, UserPassword: chPass, DB: db, T2TTLDays: ttlDays,
	}); err != nil {
		t.Fatalf("provision: %v", err)
	}
	return admin, dsn, db
}

func dbFromDSN(t *testing.T, dsn string) string {
	t.Helper()
	// testch DSN is clickhouse://user:pass@host:port/<db>
	for i := len(dsn) - 1; i >= 0; i-- {
		if dsn[i] == '/' {
			return dsn[i+1:]
		}
	}
	t.Fatalf("no db in dsn %q", dsn)
	return ""
}

// The RLS boundary: a non-Lynceus user with SELECT on the DB sees ZERO
// query_stats_t2 rows; the Lynceus USER sees them.
func TestCHSecurity_RowPolicy_DeniesNonUser(t *testing.T) {
	ctx := context.Background()
	admin, dsn, db := provision(t, 7)

	// Write a T2 row AS the Lynceus USER (the runtime identity + the only one
	// the row policy admits on read).
	userConn := testch.OpenAs(t, dsn, chUser, chPass)
	when := time.Now().UTC()
	if err := store.NewCHStats(userConn).WriteQueryStats(ctx, []store.QueryStat{{
		ServerID: "srv", CollectedAt: when, Fingerprint: "fp",
		NormalizedQuery: "SELECT * FROM t WHERE ssn='123-45-6789'", DataTier: 2, Calls: 1, TotalTimeMs: 1,
	}}); err != nil {
		t.Fatalf("user write t2: %v", err)
	}

	// A third-party CH user with SELECT on the DB.
	if err := admin.Exec(ctx, "CREATE USER third IDENTIFIED BY 'tp'"); err != nil {
		t.Fatalf("create third: %v", err)
	}
	if err := admin.Exec(ctx, "GRANT SELECT ON "+db+".* TO third"); err != nil {
		t.Fatalf("grant third: %v", err)
	}
	third := testch.OpenAs(t, dsn, "third", "tp")

	count := func(conn driver.Conn, tbl string) uint64 {
		var n uint64
		if err := conn.QueryRow(ctx, "SELECT count() FROM "+tbl).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", tbl, err)
		}
		return n
	}

	if got := count(userConn, "query_stats_t2"); got != 1 {
		t.Fatalf("lynceus USER query_stats_t2 count = %d, want 1", got)
	}
	if got := count(third, "query_stats_t2"); got != 0 {
		t.Fatalf("RLS breach: third sees %d query_stats_t2 rows, want 0", got)
	}
}

// T1 tables carry no policy → a non-Lynceus user reads them.
func TestCHSecurity_T1_ReadableByNonUser(t *testing.T) {
	ctx := context.Background()
	admin, dsn, db := provision(t, 7)

	userConn := testch.OpenAs(t, dsn, chUser, chPass)
	when := time.Now().UTC()
	if err := store.NewCHStats(userConn).WriteQueryStats(ctx, []store.QueryStat{{
		ServerID: "srv", CollectedAt: when, Fingerprint: "fp-t1",
		NormalizedQuery: "SELECT 1", DataTier: 1, Calls: 1, TotalTimeMs: 1,
	}}); err != nil {
		t.Fatalf("user write t1: %v", err)
	}
	if err := admin.Exec(ctx, "CREATE USER third2 IDENTIFIED BY 'tp'"); err != nil {
		t.Fatalf("create third2: %v", err)
	}
	if err := admin.Exec(ctx, "GRANT SELECT ON "+db+".* TO third2"); err != nil {
		t.Fatalf("grant third2: %v", err)
	}
	third := testch.OpenAs(t, dsn, "third2", "tp")

	var n uint64
	if err := third.QueryRow(ctx, "SELECT count() FROM query_stats").Scan(&n); err != nil {
		t.Fatalf("third read query_stats: %v", err)
	}
	if n != 1 {
		t.Fatalf("third query_stats count = %d, want 1 (T1 must be open)", n)
	}
}

// TTL is configurable: provisioning with T2TTLDays=3 sets the table TTL to 3 days.
func TestCHSecurity_TTLConfigurable(t *testing.T) {
	ctx := context.Background()
	admin, _, _ := provision(t, 3)

	var createSQL string
	if err := admin.QueryRow(ctx, "SHOW CREATE TABLE query_stats_t2").Scan(&createSQL); err != nil {
		t.Fatalf("show create: %v", err)
	}
	// CH 25.8 renders `INTERVAL 3 DAY` as `toIntervalDay(3)` in SHOW CREATE TABLE.
	if !containsAll(createSQL, "TTL", "toIntervalDay(3)") {
		t.Fatalf("query_stats_t2 TTL not 3 days:\n%s", createSQL)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		found := false
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
