package store_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/dobbo-ca/lynceus/internal/testch"
)

// TestMV_DerivesLiteralFreeT1FromRaw pins the ly-cwr.5 boundary: inserting a T2
// row (with a literal in raw_query and a pg_query $1 normalized_query) into
// query_stats_t2 auto-populates query_stats (T1) via the MV, literal-free, with
// the edge pg_query fingerprint + normalized_query preserved verbatim (parity),
// and raw_query excluded.
func TestMV_DerivesLiteralFreeT1FromRaw(t *testing.T) {
	ctx := context.Background()
	conn := testch.Start(t) // shared container
	if err := store.ApplyClickHouseMigrations(ctx, conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := store.NewCHStats(conn)

	when := time.Now().UTC().Truncate(time.Second)
	if err := s.WriteQueryStats(ctx, []store.QueryStat{{
		ServerID:        "s-mv",
		CollectedAt:     when,
		Fingerprint:     "fp-parity",
		NormalizedQuery: "SELECT * FROM users WHERE email=$1 AND age>$2",
		RawQuery:        "SELECT * FROM users WHERE email='SECRET-LITERAL@x.com' AND age>30",
		DataTier:        2,
		Calls:           5,
	}}); err != nil {
		t.Fatalf("write T2: %v", err)
	}

	// The MV must have projected a T1 row into query_stats.
	var (
		fp, norm string
		leak     uint64
	)
	if err := conn.QueryRow(ctx,
		`SELECT fingerprint, normalized_query FROM query_stats WHERE server_id='s-mv' LIMIT 1`).
		Scan(&fp, &norm); err != nil {
		t.Fatalf("read T1 from MV: %v", err)
	}
	if fp != "fp-parity" || norm != "SELECT * FROM users WHERE email=$1 AND age>$2" {
		t.Fatalf("parity lost: fp=%q norm=%q", fp, norm)
	}
	if strings.Contains(norm, "SECRET") {
		t.Fatalf("literal leaked into T1 normalized_query: %q", norm)
	}
	// Defense-in-depth: CH normalizeQuery leaves an already-$1 skeleton unchanged.
	if err := conn.QueryRow(ctx,
		`SELECT count() FROM query_stats WHERE server_id='s-mv'
		   AND normalizeQuery(normalized_query) != normalized_query`).Scan(&leak); err != nil {
		t.Fatalf("guardrail query: %v", err)
	}
	if leak != 0 {
		t.Fatalf("normalizeQuery guardrail: %d T1 rows carry a stray literal", leak)
	}
}

// TestMV_RLSInsertIdentity re-pins the spiked CH behaviour: a row policy on the
// MV source (query_stats_t2, ly-cwr.6) filters the MV transform by the INSERTING
// identity. An insert BY the runtime USER (matching the policy) populates the T1
// target via the MV. See spec §9.
func TestMV_RLSInsertIdentity(t *testing.T) {
	ctx := context.Background()
	// provision applies migrations (incl. the MV), creates the runtime USER, and
	// installs the ly-cwr.6 row policy on query_stats_t2.
	_, dsn, _ := provision(t, 7)

	userConn := testch.OpenAs(t, dsn, chUser, chPass)
	us := store.NewCHStats(userConn)
	if err := us.WriteQueryStats(ctx, []store.QueryStat{{
		ServerID: "s-rls", CollectedAt: time.Now().UTC(), Fingerprint: "fp",
		NormalizedQuery: "SELECT $1", RawQuery: "SELECT 'x'", DataTier: 2, Calls: 1,
	}}); err != nil {
		t.Fatalf("USER write T2: %v", err)
	}
	var n uint64
	if err := userConn.QueryRow(ctx,
		`SELECT count() FROM query_stats WHERE server_id='s-rls'`).Scan(&n); err != nil {
		t.Fatalf("read T1: %v", err)
	}
	if n == 0 {
		t.Fatal("MV did not populate T1 for a USER (policy-matching) insert")
	}
}
