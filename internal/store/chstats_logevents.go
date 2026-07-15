package store

import "context"

// logEventCHColumns is the INSERT column order for the ClickHouse log_events
// table (migrations/clickhouse/0010_logevents.sql). It preserves the
// log-event column order carried over from the removed Postgres backend.
const logEventCHColumns = "server_id, event_type, severity, occurred_at, logged_at, pid, " +
	"backend_type, database_name, user_name, application_name, " +
	"client_addr_hash, sql_state, session_line_num, transaction_id, data_tier"

// WriteLogEvents batch-inserts classified log events into log_events. DataTier
// zero is normalized to 1 (T1) on write, matching the removed PG behaviour. Every
// column is classification metadata only — never statement text, bind params,
// error detail, or the raw message (see the LogEvent privacy contract test).
func (s *chStats) WriteLogEvents(ctx context.Context, rows []LogEventRow) error {
	if len(rows) == 0 {
		return nil
	}
	batch, err := s.conn.PrepareBatch(ctx, "INSERT INTO log_events ("+logEventCHColumns+")")
	if err != nil {
		return err
	}
	for i := range rows {
		r := rows[i]
		if r.DataTier == 0 {
			r.DataTier = 1
		}
		if err := batch.Append(
			r.ServerID, r.EventType, r.Severity, r.OccurredAt, r.LoggedAt, r.Pid,
			r.BackendType, r.DatabaseName, r.UserName, r.ApplicationName,
			r.ClientAddrHash, r.SqlState, r.SessionLineNum, r.TransactionID, r.DataTier,
		); err != nil {
			_ = batch.Abort()
			return err
		}
	}
	return batch.Send()
}
