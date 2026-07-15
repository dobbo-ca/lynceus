package store_test

import (
	"context"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/dobbo-ca/lynceus/internal/testch"
)

func TestCH_dlq_ParkDLQ_RoundTrip(t *testing.T) {
	ctx := context.Background()
	conn := testch.Start(t)
	if err := store.ApplyClickHouseMigrations(ctx, conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := store.NewCHStats(conn)

	raw := []byte{0x0a, 0x03, 'a', 'b', 'c'} // arbitrary bytes — String must be binary-safe
	if err := s.ParkDLQ(ctx, "srv-1", "rate_limited", raw); err != nil {
		t.Fatalf("park: %v", err)
	}
	// server_id='' path (unmarshal failure) must also land.
	if err := s.ParkDLQ(ctx, "", "unmarshal: boom", []byte("x")); err != nil {
		t.Fatalf("park empty server: %v", err)
	}

	var total uint64
	if err := conn.QueryRow(ctx, `SELECT count() FROM dlq`).Scan(&total); err != nil {
		t.Fatalf("count: %v", err)
	}
	if total != 2 {
		t.Fatalf("dlq rows = %d, want 2", total)
	}

	var reason string
	var gotRaw []byte
	if err := conn.QueryRow(ctx,
		`SELECT reason, raw FROM dlq WHERE server_id = ?`, "srv-1",
	).Scan(&reason, &gotRaw); err != nil {
		t.Fatalf("select: %v", err)
	}
	if reason != "rate_limited" || string(gotRaw) != string(raw) {
		t.Fatalf("row = (%q, %v), want (rate_limited, %v)", reason, gotRaw, raw)
	}
}
