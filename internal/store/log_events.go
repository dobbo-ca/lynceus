package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// LogEventRow is one classified Postgres log event as stored in the stats DB.
// Every field is classification metadata only (event type, severity, catalog
// identifiers, hashed client IP, coarse counters) — never the statement text,
// bind params, error detail, or raw message. DataTier zero is treated as 1
// (T1) on insert. See the LogEvent privacy contract test.
type LogEventRow struct {
	ServerID        string
	EventType       string
	Severity        string
	OccurredAt      time.Time
	LoggedAt        time.Time
	Pid             int64
	BackendType     string
	DatabaseName    string
	UserName        string
	ApplicationName string
	ClientAddrHash  string
	SqlState        string
	SessionLineNum  int64
	TransactionID   int64
	DataTier        int16
}

// logEventColumns is the COPY column order for WriteLogEvents.
var logEventColumns = []string{
	"server_id", "event_type", "severity", "occurred_at", "logged_at", "pid",
	"backend_type", "database_name", "user_name", "application_name",
	"client_addr_hash", "sql_state", "session_line_num", "transaction_id", "data_tier",
}

// WriteLogEvents appends a batch of classified log events via the COPY
// protocol, creating any missing weekly partitions first. Mirrors
// WriteQueryPlans / WriteActivityBuckets: COPY routes each row to its weekly
// partition and is lighter on the storage DB than per-row INSERTs.
func (s *pgxStats) WriteLogEvents(ctx context.Context, rows []LogEventRow) error {
	if len(rows) == 0 {
		return nil
	}

	weeks := map[string]time.Time{}
	for i := range rows {
		r := &rows[i]
		weeks[logEventsPartitionName(r.OccurredAt)] = r.OccurredAt
	}
	for _, ts := range weeks {
		if err := s.EnsureLogEventsWeeklyPartition(ctx, ts); err != nil {
			return err
		}
	}

	src := pgx.CopyFromSlice(len(rows), func(i int) ([]any, error) {
		r := rows[i]
		if r.DataTier == 0 {
			r.DataTier = 1
		}
		return []any{
			r.ServerID, r.EventType, r.Severity, r.OccurredAt, r.LoggedAt, r.Pid,
			r.BackendType, r.DatabaseName, r.UserName, r.ApplicationName,
			r.ClientAddrHash, r.SqlState, r.SessionLineNum, r.TransactionID, r.DataTier,
		}, nil
	})
	_, err := s.pool.CopyFrom(ctx, pgx.Identifier{"log_events"}, logEventColumns, src)
	return err
}

// EnsureLogEventsWeeklyPartition creates the weekly partition for ts on
// log_events if it does not already exist. Idempotent.
func (s *pgxStats) EnsureLogEventsWeeklyPartition(ctx context.Context, ts time.Time) error {
	name := logEventsPartitionName(ts)
	from, to := isoWeekBounds(ts)
	_, err := s.pool.Exec(ctx, fmt.Sprintf(
		`CREATE TABLE IF NOT EXISTS %s PARTITION OF log_events
		 FOR VALUES FROM ('%s') TO ('%s')`,
		name,
		from.Format("2006-01-02"),
		to.Format("2006-01-02"),
	))
	return err
}

func logEventsPartitionName(ts time.Time) string {
	y, w := ts.UTC().ISOWeek()
	return fmt.Sprintf("log_events_%04d_%02d", y, w)
}
