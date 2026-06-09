package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/dobbo-ca/lynceus/internal/store"
)

func TestApplyStatsMigrations_createsPartitionedLogEvents(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyStatsMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var strategy string
	err := pool.QueryRow(ctx,
		`SELECT partstrat::text FROM pg_partitioned_table
		 WHERE partrelid = 'log_events'::regclass`,
	).Scan(&strategy)
	if err != nil {
		t.Fatalf("log_events not partitioned: %v", err)
	}
	if strategy != "r" {
		t.Fatalf("partition strategy = %q, want 'r' (range)", strategy)
	}

	// data_tier column exists (privacy invariant).
	var hasCol bool
	_ = pool.QueryRow(ctx,
		`SELECT EXISTS(
		   SELECT 1 FROM information_schema.columns
		   WHERE table_name = 'log_events' AND column_name = 'data_tier'
		 )`,
	).Scan(&hasCol)
	if !hasCol {
		t.Fatal("log_events.data_tier missing")
	}
}

func TestWriteLogEvents_createsPartitionAndRoundtrips(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyStatsMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := store.NewStats(pool)

	wk1 := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC) // ISO week A (Wednesday)
	wk2 := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)  // ISO week B (Wednesday)
	logged := time.Date(2026, 6, 3, 12, 0, 5, 0, time.UTC)
	rows := []store.LogEventRow{
		{
			ServerID: "srv-1", EventType: "checkpoint.completed", Severity: "LOG",
			OccurredAt: wk1, LoggedAt: wk1, Pid: 11, BackendType: "checkpointer",
		},
		{
			ServerID: "srv-1", EventType: "lock.deadlock_detected", Severity: "ERROR",
			OccurredAt: wk2, LoggedAt: logged, Pid: 22, BackendType: "client backend",
			DatabaseName: "app", UserName: "alice", ApplicationName: "psql",
			ClientAddrHash: "abc123", SqlState: "40P01",
			SessionLineNum: 7, TransactionID: 99,
			// DataTier left 0 — must be stored as 1 (T1 default).
		},
		{
			ServerID: "srv-1", EventType: "vacuum.completed", Severity: "LOG",
			OccurredAt: wk2, LoggedAt: wk2, Pid: 33, BackendType: "autovacuum worker",
			DatabaseName: "app",
		},
	}
	if err := s.WriteLogEvents(ctx, rows); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Both weekly partitions were created and rows routed across them.
	var partCount int
	_ = pool.QueryRow(ctx,
		`SELECT count(*) FROM pg_inherits WHERE inhparent = 'log_events'::regclass`,
	).Scan(&partCount)
	if partCount != 2 {
		t.Fatalf("partitions = %d, want 2", partCount)
	}

	var rowCount int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM log_events`).Scan(&rowCount)
	if rowCount != 3 {
		t.Fatalf("rows = %d, want 3", rowCount)
	}

	// DataTier 0 was defaulted to 1 on insert.
	var tier int16
	if err := pool.QueryRow(ctx,
		`SELECT data_tier FROM log_events WHERE event_type = $1`, "lock.deadlock_detected",
	).Scan(&tier); err != nil {
		t.Fatalf("select tier: %v", err)
	}
	if tier != 1 {
		t.Errorf("data_tier = %d, want 1 (defaulted)", tier)
	}

	// Classification fields round-trip on the deadlock row.
	var (
		sev, db, usr, app, hash, state string
		pid, line, txid                int64
	)
	if err := pool.QueryRow(ctx,
		`SELECT severity, database_name, user_name, application_name,
		        client_addr_hash, sql_state, pid, session_line_num, transaction_id
		   FROM log_events WHERE event_type = $1`, "lock.deadlock_detected",
	).Scan(&sev, &db, &usr, &app, &hash, &state, &pid, &line, &txid); err != nil {
		t.Fatalf("select fields: %v", err)
	}
	if sev != "ERROR" || db != "app" || usr != "alice" || app != "psql" ||
		hash != "abc123" || state != "40P01" || pid != 22 || line != 7 || txid != 99 {
		t.Errorf("classification fields not round-tripped: sev=%q db=%q usr=%q app=%q hash=%q state=%q pid=%d line=%d txid=%d",
			sev, db, usr, app, hash, state, pid, line, txid)
	}

	// EnsureLogEventsWeeklyPartition is idempotent.
	if err := s.EnsureLogEventsWeeklyPartition(ctx, wk1); err != nil {
		t.Fatalf("ensure idempotent: %v", err)
	}
}
