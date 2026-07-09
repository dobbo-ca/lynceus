// internal/collector/xmin_horizon_reader_test.go
//
// Integration test for the xmin-horizon reader. Spins up a real Postgres,
// holds an old snapshot in a REPEATABLE READ transaction so a backend
// advertises backend_xmin, then asserts the reader returns exactly one
// cluster-global horizon with a holder_kind in the closed set and a
// non-negative age. The gated-off case asserts the capability gate
// short-circuits to a no-op with a nil pool (no query).
package collector_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/dobbo-ca/lynceus/internal/caps"
	"github.com/dobbo-ca/lynceus/internal/collector"
	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
	"github.com/dobbo-ca/lynceus/internal/testpg"
)

func TestXminHorizonReader_readsOldestHolder(t *testing.T) {
	ctx := context.Background()

	c, err := tcpostgres.Run(ctx,
		"postgres:16",
		tcpostgres.WithDatabase("app"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
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
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()

	// Session A: hold a snapshot in a REPEATABLE READ transaction so this
	// backend advertises backend_xmin — a deterministic xmin holder.
	connA, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire A: %v", err)
	}
	defer connA.Release()
	if _, err := connA.Exec(ctx, `BEGIN ISOLATION LEVEL REPEATABLE READ`); err != nil {
		t.Fatalf("A begin: %v", err)
	}
	var one int
	if err := connA.QueryRow(ctx, `SELECT 1`).Scan(&one); err != nil {
		t.Fatalf("A snapshot: %v", err)
	}

	r := collector.NewXminHorizonReader(pool, caps.NewGate(), "app")
	deadline := time.Now().Add(10 * time.Second)
	var rows []*lynceusv1.XminHorizon
	for time.Now().Before(deadline) {
		rows, err = r.Read(ctx)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if len(rows) > 0 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if len(rows) != 1 {
		t.Fatalf("want exactly one xmin horizon, got %d: %+v", len(rows), rows)
	}
	h := rows[0]
	switch h.GetHolderKind() {
	case "backend", "replication_slot", "prepared_xact":
	default:
		t.Fatalf("holder_kind %q not in closed set", h.GetHolderKind())
	}
	if h.GetOldestXminAge() < 0 {
		t.Fatalf("negative xmin age: %+v", h)
	}
}

func TestXminHorizonReader_gatedOff(t *testing.T) {
	gate := caps.NewGate()
	gate.Replace(map[caps.GateKey]bool{
		{Db: "app", Cap: caps.XminHorizon}: false,
	})
	r := collector.NewXminHorizonReader(nil, gate, "app")
	rows, err := r.Read(context.Background())
	if err != nil || rows != nil {
		t.Fatalf("gated-off reader must no-op, got rows=%v err=%v", rows, err)
	}
}
