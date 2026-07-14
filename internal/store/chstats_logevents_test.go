package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/dobbo-ca/lynceus/internal/testch"
)

func TestCH_logevents_WriteLogEvents_RoundTrip(t *testing.T) {
	ctx := context.Background()
	conn := testch.Start(t)
	if err := store.ApplyClickHouseMigrations(ctx, conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := store.NewCHStats(conn)

	// Two different months exercise the toYYYYMM partitioning.
	m1 := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	m2 := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	logged := time.Date(2026, 6, 3, 12, 0, 5, 0, time.UTC)
	rows := []store.LogEventRow{
		{
			ServerID: "srv-1", EventType: "checkpoint.completed", Severity: "LOG",
			OccurredAt: m1, LoggedAt: m1, Pid: 11, BackendType: "checkpointer",
		},
		{
			ServerID: "srv-1", EventType: "lock.deadlock_detected", Severity: "ERROR",
			OccurredAt: m2, LoggedAt: logged, Pid: 22, BackendType: "client backend",
			DatabaseName: "app", UserName: "alice", ApplicationName: "psql",
			ClientAddrHash: "abc123", SqlState: "40P01",
			SessionLineNum: 7, TransactionID: 99,
			// DataTier left 0 — must be stored as 1 (T1 default).
		},
		{
			ServerID: "srv-1", EventType: "vacuum.completed", Severity: "LOG",
			OccurredAt: m2, LoggedAt: m2, Pid: 33, BackendType: "autovacuum worker",
			DatabaseName: "app",
		},
	}
	if err := s.WriteLogEvents(ctx, rows); err != nil {
		t.Fatalf("write: %v", err)
	}

	// All three rows landed across the two monthly partitions.
	var total uint64
	if err := conn.QueryRow(ctx, `SELECT count() FROM log_events`).Scan(&total); err != nil {
		t.Fatalf("count: %v", err)
	}
	if total != 3 {
		t.Fatalf("rows = %d, want 3", total)
	}

	var parts uint64
	if err := conn.QueryRow(ctx,
		`SELECT uniqExact(toYYYYMM(occurred_at)) FROM log_events`,
	).Scan(&parts); err != nil {
		t.Fatalf("partition count: %v", err)
	}
	if parts != 2 {
		t.Fatalf("distinct monthly partitions = %d, want 2", parts)
	}

	// DataTier 0 defaulted to 1, and every classification field round-trips on
	// the deadlock row. Scalars are compared as a single struct so the field
	// check stays one branch (keeps cyclomatic complexity in check).
	type fields struct {
		sev, db, usr, app, hash, state, backend string
		tier                                    int16
		pid, line, txid                         int64
	}
	var got fields
	var occurred, loggedGot time.Time
	if err := conn.QueryRow(ctx,
		`SELECT severity, database_name, user_name, application_name,
		        client_addr_hash, sql_state, backend_type, data_tier, pid,
		        session_line_num, transaction_id, occurred_at, logged_at
		   FROM log_events WHERE event_type = ?`, "lock.deadlock_detected",
	).Scan(&got.sev, &got.db, &got.usr, &got.app, &got.hash, &got.state, &got.backend,
		&got.tier, &got.pid, &got.line, &got.txid, &occurred, &loggedGot); err != nil {
		t.Fatalf("select fields: %v", err)
	}
	want := fields{
		sev: "ERROR", db: "app", usr: "alice", app: "psql",
		hash: "abc123", state: "40P01", backend: "client backend",
		tier: 1, // DataTier left 0 on write must be stored as 1.
		pid: 22, line: 7, txid: 99,
	}
	if got != want {
		t.Errorf("classification fields = %+v, want %+v", got, want)
	}
	if !occurred.Equal(m2) {
		t.Errorf("occurred_at = %v, want %v", occurred, m2)
	}
	if !loggedGot.Equal(logged) {
		t.Errorf("logged_at = %v, want %v", loggedGot, logged)
	}

	// Empty batch is a no-op.
	if err := s.WriteLogEvents(ctx, nil); err != nil {
		t.Fatalf("empty write: %v", err)
	}
}
