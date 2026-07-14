package store_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/dobbo-ca/lynceus/internal/testpg"
)

func TestPG_ParkDLQ_RoundTrip(t *testing.T) {
	ctx := context.Background()
	c, err := tcpostgres.Run(ctx, "postgres:16",
		tcpostgres.WithDatabase("lynceus_stats"),
		tcpostgres.WithUsername("test"), tcpostgres.WithPassword("test"),
		testpg.ReadyWait(),
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

	s := store.NewStats(pool)
	if err := s.ParkDLQ(ctx, "srv-9", "write: boom", []byte("payload")); err != nil {
		t.Fatalf("park: %v", err)
	}
	if err := s.ParkDLQ(ctx, "", "unmarshal: boom", []byte("y")); err != nil {
		t.Fatalf("park empty: %v", err)
	}

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM dlq`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Fatalf("dlq rows = %d, want 2", n)
	}
	// server_id='' stored as NULL (NULLIF).
	var nullServers int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM dlq WHERE server_id IS NULL`).Scan(&nullServers); err != nil {
		t.Fatalf("count null: %v", err)
	}
	if nullServers != 1 {
		t.Fatalf("null server_id rows = %d, want 1", nullServers)
	}
}
