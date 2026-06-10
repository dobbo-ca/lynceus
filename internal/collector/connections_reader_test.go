// internal/collector/connections_reader_test.go
//
// Integration test for the connections reader. Spins up a real Postgres,
// creates a genuine lock-wait (session B blocked by an open transaction in
// session A), then asserts the reader returns the corresponding A→B blocking
// edge. The gated-off case asserts the capability gate short-circuits to a
// no-op exactly like ActivityReader.
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
	"github.com/dobbo-ca/lynceus/internal/testpg"
)

func TestConnectionsReader_seesBlockingEdge(t *testing.T) {
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

	if _, err := pool.Exec(ctx, `CREATE TABLE t (id int primary key); INSERT INTO t VALUES (1)`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Session A: hold a row lock inside an open transaction (idle-in-txn).
	connA, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire A: %v", err)
	}
	defer connA.Release()
	if _, err := connA.Exec(ctx, `BEGIN`); err != nil {
		t.Fatalf("A begin: %v", err)
	}
	if _, err := connA.Exec(ctx, `UPDATE t SET id = id WHERE id = 1`); err != nil {
		t.Fatalf("A update: %v", err)
	}

	// Session B: try to update the same row → blocks on A's lock.
	connB, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire B: %v", err)
	}
	defer connB.Release()
	go func() {
		_, _ = connB.Exec(ctx, `UPDATE t SET id = id WHERE id = 1`)
	}()

	r := collector.NewConnectionsReader(pool, caps.NewGate(), "app")
	deadline := time.Now().Add(15 * time.Second)
	var (
		samples []collector.ConnectionSample
		edges   []collector.BlockingPair
	)
	for time.Now().Before(deadline) {
		samples, edges, err = r.Read(ctx)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if len(edges) > 0 {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}

	if len(edges) == 0 {
		t.Fatalf("expected at least one blocking edge, got none (samples=%+v)", samples)
	}
	if edges[0].BlockerPID == edges[0].BlockedPID || edges[0].BlockerPID == 0 || edges[0].BlockedPID == 0 {
		t.Fatalf("bad blocking edge: %+v", edges[0])
	}
}

func TestConnectionsReader_gatedOff(t *testing.T) {
	ctx := context.Background()
	gate := caps.NewGate()
	gate.Replace(map[caps.GateKey]bool{
		{Db: "app", Cap: caps.PgStatActivityFullRead}: false,
	})
	r := collector.NewConnectionsReader(nil, gate, "app")
	samples, edges, err := r.Read(ctx)
	if err != nil || samples != nil || edges != nil {
		t.Fatalf("gated-off reader must no-op, got samples=%v edges=%v err=%v", samples, edges, err)
	}
}
