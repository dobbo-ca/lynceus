package store_test

import (
	"context"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/dobbo-ca/lynceus/internal/testch"
)

func TestCH_schemaObjects_WriteAndFirstSeenStable(t *testing.T) {
	ctx := context.Background()
	conn := testch.Start(t)
	if err := store.ApplyClickHouseMigrations(ctx, conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := store.NewCHStats(conn)

	row := store.SchemaObjectRow{
		ServerID: "srv-inv", Kind: 1, FQN: "public.orders",
		SchemaName: "public", ObjectName: "orders", SizeBytes: 8192,
	}
	if err := s.WriteSchemaObjects(ctx, []store.SchemaObjectRow{row}); err != nil {
		t.Fatalf("write 1: %v", err)
	}

	// first_seen after the first observation.
	var fs1 string
	if err := conn.QueryRow(ctx,
		`SELECT toString(min(first_seen_at)) FROM schema_objects
		  WHERE server_id = ? AND kind = ? AND fqn = ?`,
		"srv-inv", int16(1), "public.orders",
	).Scan(&fs1); err != nil {
		t.Fatalf("read fs1: %v", err)
	}
	if fs1 == "" {
		t.Fatal("first_seen_at must be stamped server-side on write")
	}

	// Second observation, larger size — size updates, first_seen must not.
	row.SizeBytes = 16384
	if err := s.WriteSchemaObjects(ctx, []store.SchemaObjectRow{row}); err != nil {
		t.Fatalf("write 2: %v", err)
	}

	// Exactly one logical row per key (AggregatingMergeTree collapses via FINAL).
	var distinct uint64
	if err := conn.QueryRow(ctx,
		`SELECT count() FROM (SELECT 1 FROM schema_objects FINAL
		   WHERE server_id = ? GROUP BY server_id, kind, fqn)`, "srv-inv",
	).Scan(&distinct); err != nil {
		t.Fatalf("distinct: %v", err)
	}
	if distinct != 1 {
		t.Fatalf("distinct keys = %d, want 1", distinct)
	}

	var size int64
	var fs2 string
	if err := conn.QueryRow(ctx,
		`SELECT size_bytes, toString(first_seen_at) FROM schema_objects FINAL
		  WHERE server_id = ? AND kind = ? AND fqn = ?`,
		"srv-inv", int16(1), "public.orders",
	).Scan(&size, &fs2); err != nil {
		t.Fatalf("read merged: %v", err)
	}
	if size != 16384 {
		t.Errorf("size_bytes = %d, want 16384 (latest observation)", size)
	}
	if fs2 != fs1 {
		t.Errorf("first_seen_at = %q, want stable %q", fs2, fs1)
	}
}
