package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/dobbo-ca/lynceus/internal/testch"
)

func TestOpenStats_RequiresBackend(t *testing.T) {
	t.Setenv("LYNCEUS_STATS_BACKEND", "")
	if _, err := store.OpenStats(context.Background()); err == nil {
		t.Fatal("want error when LYNCEUS_STATS_BACKEND is unset")
	}
}

func TestOpenStats_UnknownBackend(t *testing.T) {
	t.Setenv("LYNCEUS_STATS_BACKEND", "mysql")
	if _, err := store.OpenStats(context.Background()); err == nil {
		t.Fatal("want error for unknown backend value")
	}
}

// Two-identity happy path: ADMIN bootstraps (migrate + provision), OpenStats
// returns a USER-backed Stats that can round-trip a T1 write/read.
func TestOpenStats_ClickHouse_TwoIdentity(t *testing.T) {
	ctx := context.Background()
	_, adminDSN := testch.StartDSN(t)
	userDSN := userDSNFor(t, adminDSN)

	t.Setenv("LYNCEUS_STATS_BACKEND", "clickhouse")
	t.Setenv("LYNCEUS_CLICKHOUSE_ADMIN_DSN", adminDSN)
	t.Setenv("LYNCEUS_CLICKHOUSE_USER_DSN", userDSN)
	t.Setenv("LYNCEUS_CLICKHOUSE_T2_TTL_DAYS", "7")

	stats, err := store.OpenStats(ctx)
	if err != nil {
		t.Fatalf("OpenStats: %v", err)
	}
	when := time.Now().UTC()
	if err := stats.WriteQueryStats(ctx, []store.QueryStat{{
		ServerID: "srv", CollectedAt: when, Fingerprint: "fp", NormalizedQuery: "SELECT 1",
		DataTier: 1, Calls: 1, TotalTimeMs: 1,
	}}); err != nil {
		t.Fatalf("write via USER-backed stats: %v", err)
	}
	got, err := stats.TopQueriesByTotalTime(ctx, when.Add(-time.Hour), when.Add(time.Hour), 10)
	if err != nil || len(got) != 1 {
		t.Fatalf("top = (%v, %v), want 1 row", got, err)
	}
}

// USER_DSN is required.
func TestOpenStats_ClickHouse_RequiresUserDSN(t *testing.T) {
	t.Setenv("LYNCEUS_STATS_BACKEND", "clickhouse")
	t.Setenv("LYNCEUS_CLICKHOUSE_ADMIN_DSN", "")
	t.Setenv("LYNCEUS_CLICKHOUSE_USER_DSN", "")
	if _, err := store.OpenStats(context.Background()); err == nil {
		t.Fatal("expected error when LYNCEUS_CLICKHOUSE_USER_DSN is unset")
	}
}

// userDSNFor derives a runtime-user DSN from the admin DSN by ensuring the user
// exists (via the admin conn) and swapping in its credentials. The user is
// created here so OpenStats' own provisioning is exercised as a no-op re-create.
func userDSNFor(t *testing.T, adminDSN string) string {
	t.Helper()
	ctx := context.Background()
	admin := testch.OpenAs(t, adminDSN, "test", "test") // testch bootstrap creds
	if err := admin.Exec(ctx, "CREATE USER IF NOT EXISTS lynceus_user IDENTIFIED BY 'user-pw'"); err != nil {
		t.Fatalf("create user: %v", err)
	}
	db := dbFromDSN(t, adminDSN)
	if err := admin.Exec(ctx, "GRANT SELECT, INSERT ON "+db+".* TO lynceus_user"); err != nil {
		t.Fatalf("grant: %v", err)
	}
	// Rebuild the DSN with the user's creds (same host/port/db).
	// adminDSN form: clickhouse://test:test@host:port/db
	return replaceUserInfo(adminDSN, "lynceus_user", "user-pw")
}

func replaceUserInfo(dsn, user, pass string) string {
	// dsn: scheme://user:pass@rest
	at := indexOf(dsn, '@')
	scheme := "clickhouse://"
	return scheme + user + ":" + pass + dsn[at:]
}

func indexOf(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}
