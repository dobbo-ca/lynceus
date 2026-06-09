// internal/collector/activity_reader_test.go
//
// Integration test for the activity reader. Spins up a real Postgres,
// opens connections in distinct states (active, idle, idle-in-txn),
// then asserts the reader returns the corresponding ActivitySample rows
// and CONTAINS NO QUERY TEXT (the privacy guarantee enforced at the
// SQL layer — defense in depth alongside the proto contract test).
package collector_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/dobbo-ca/lynceus/internal/caps"
	"github.com/dobbo-ca/lynceus/internal/collector"
)

func TestActivityReader_seesDistinctConnectionStates(t *testing.T) {
	ctx := context.Background()

	c, err := tcpostgres.Run(ctx,
		"postgres:16",
		tcpostgres.WithDatabase("lynceus_target"),
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

	// The reader's own pool — kept small so it does not crowd the
	// states we want to observe.
	readerPool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(readerPool.Close)

	// Seed table for the "active" query to scan.
	if _, err := readerPool.Exec(ctx,
		`CREATE TABLE canary (id INT PRIMARY KEY, secret TEXT)`,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := readerPool.Exec(ctx,
		`INSERT INTO canary VALUES (1, 'leaky-canary@phi.example.com')`,
	); err != nil {
		t.Fatal(err)
	}

	// 1) An IDLE connection — open, do nothing.
	idleConn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = idleConn.Close(context.Background()) })

	// 2) An IDLE-IN-TRANSACTION connection — BEGIN, then sit.
	txnConn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = txnConn.Close(context.Background()) })
	if _, err := txnConn.Exec(ctx, "BEGIN"); err != nil {
		t.Fatal(err)
	}
	if _, err := txnConn.Exec(ctx, "SELECT 1"); err != nil {
		t.Fatal(err)
	}

	// 3) An ACTIVE connection — kick off a long-running statement in a
	//    goroutine and let it run while we sample.
	activeConn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	activeCtx, cancelActive := context.WithCancel(ctx)
	activeDone := make(chan struct{})
	go func() {
		defer close(activeDone)
		// pg_sleep keeps the backend in `active` state with the literal
		// secret embedded in the query text — perfect canary for the
		// "reader must not return query text" assertion.
		_, _ = activeConn.Exec(activeCtx,
			"SELECT pg_sleep(5), secret FROM canary WHERE secret = 'leaky-canary@phi.example.com'",
		)
	}()
	// *pgx.Conn is not safe for concurrent use: the cleanup must stop the
	// goroutine touching activeConn before closing it. Cancel the in-flight
	// query, wait for the goroutine to return (the channel receive is the
	// happens-before edge), then close. Otherwise Close races the still-running
	// Exec — caught by `go test -race` in CI.
	t.Cleanup(func() {
		cancelActive()
		<-activeDone
		_ = activeConn.Close(context.Background())
	})

	// Give Postgres a beat to publish the new backend states.
	time.Sleep(500 * time.Millisecond)

	r := collector.NewActivityReader(readerPool, caps.NewGate(), "lynceus_target")
	samples, err := r.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(samples) == 0 {
		t.Fatal("reader returned no samples")
	}

	// Privacy guarantee at the SQL layer: no ActivitySample field may
	// contain a substring of the literal we know is currently running.
	// (This is belt-and-braces — the proto contract test already
	// forbids carrying query text on the wire.)
	for _, s := range samples {
		joined := strings.Join([]string{s.Database, s.State, s.WaitEventType, s.WaitEvent}, "|")
		for _, forbidden := range []string{
			"leaky-canary", "phi.example.com", "pg_sleep", "canary",
		} {
			if strings.Contains(joined, forbidden) {
				t.Errorf("LITERAL LEAK from activity reader: sample %+v contains %q",
					s, forbidden)
			}
		}
	}

	// State coverage: at least one active and one idle-in-transaction
	// row should appear, in our target database.
	seen := map[string]bool{}
	for _, s := range samples {
		if s.Database == "lynceus_target" {
			seen[s.State] = true
		}
	}
	if !seen["active"] {
		t.Errorf("expected an active sample in lynceus_target, got states %v", seen)
	}
	if !seen["idle in transaction"] {
		t.Errorf("expected an idle-in-txn sample in lynceus_target, got states %v", seen)
	}
}
