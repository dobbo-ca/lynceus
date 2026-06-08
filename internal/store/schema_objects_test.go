// Integration test for the schema_objects upsert path. Spins up a real
// Postgres via testcontainers, applies the bundled stats migrations,
// and asserts:
//
//  1. UpsertSchemaObjects inserts new rows.
//  2. A second UpsertSchemaObjects for the same (server_id, kind, fqn)
//     PRESERVES first_seen_at and ADVANCES last_seen_at and size.
//  3. ListByServer returns the latest sizes + the original first_seen.
package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/dobbo-ca/lynceus/internal/store"
)

func TestSchemaObjects_FirstSeenIsStableAcrossUpserts(t *testing.T) {
	ctx := context.Background()

	c, err := tcpostgres.Run(ctx,
		"postgres:16",
		tcpostgres.WithDatabase("lynceus_stats"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Skipf("docker/testcontainers unavailable: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(c) })

	url, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	if err := store.ApplyStatsMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	so := store.NewSchemaObjects(pool)

	const srv = "srv-1"
	row := store.SchemaObjectRow{
		ServerID:    srv,
		Kind:        int16(2), // OBJECT_KIND_TABLE
		FQN:         "public.users",
		SchemaName:  "public",
		ObjectName:  "users",
		SizeBytes:   1024,
		IsPartition: false,
	}

	// First upsert.
	if err := so.UpsertSchemaObjects(ctx, []store.SchemaObjectRow{row}); err != nil {
		t.Fatalf("upsert 1: %v", err)
	}
	got1, err := so.ListByServer(ctx, srv)
	if err != nil {
		t.Fatalf("list 1: %v", err)
	}
	if len(got1) != 1 {
		t.Fatalf("expected 1 row, got %d", len(got1))
	}
	firstSeen := got1[0].FirstSeenAt
	if firstSeen.IsZero() {
		t.Fatal("first_seen_at is zero")
	}

	// Wait long enough that any UPDATE of first_seen_at would be detectable.
	time.Sleep(50 * time.Millisecond)

	// Re-upsert with a new size — first_seen must NOT change.
	row.SizeBytes = 4096
	if err := so.UpsertSchemaObjects(ctx, []store.SchemaObjectRow{row}); err != nil {
		t.Fatalf("upsert 2: %v", err)
	}
	got2, err := so.ListByServer(ctx, srv)
	if err != nil {
		t.Fatalf("list 2: %v", err)
	}
	if len(got2) != 1 {
		t.Fatalf("expected 1 row after re-upsert, got %d", len(got2))
	}
	if !got2[0].FirstSeenAt.Equal(firstSeen) {
		t.Errorf("first_seen_at changed across upserts: was %v, now %v",
			firstSeen, got2[0].FirstSeenAt)
	}
	if got2[0].SizeBytes != 4096 {
		t.Errorf("size_bytes_latest not updated: got %d, want 4096", got2[0].SizeBytes)
	}
	if !got2[0].LastSeenAt.After(firstSeen) {
		t.Errorf("last_seen_at did not advance: first=%v last=%v",
			firstSeen, got2[0].LastSeenAt)
	}
}
