package logparse

import (
	"strings"
	"testing"
	"time"
)

// One canonical pg16 csvlog row covering common fields. pg16 emits 26
// columns in this order: log_time, user_name, database_name, pid,
// connection_from, session_id, session_line_num, command_tag,
// session_start_time, virtual_xid, transaction_id, severity, sqlstate,
// message, detail, hint, internal_query, internal_query_pos, context,
// query, query_pos, location, application_name, backend_type,
// leader_pid, query_id.
const csvSample = `2026-05-29 12:00:00.123 UTC,"postgres","app","12345",` +
	`"127.0.0.1:54321","6500a000.3039","42","SELECT",` +
	`"2026-05-29 12:00:00 UTC","3/12","0","LOG","00000",` +
	`"duration: 12.345 ms  statement: SELECT 1",,,,,,,,,` +
	`"psql","client backend",,`

func TestParseCSV_extractsExpectedFields(t *testing.T) {
	rec, err := ParseCSV(csvSample, time.Date(2026, 5, 29, 12, 0, 0, 500000000, time.UTC))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if rec.PID != 12345 {
		t.Errorf("pid = %d, want 12345", rec.PID)
	}
	if rec.UserName != "postgres" {
		t.Errorf("user = %q, want postgres", rec.UserName)
	}
	if rec.DatabaseName != "app" {
		t.Errorf("db = %q, want app", rec.DatabaseName)
	}
	if rec.ClientAddr != "127.0.0.1:54321" {
		t.Errorf("client = %q, want 127.0.0.1:54321", rec.ClientAddr)
	}
	if rec.Severity != SeverityLog {
		t.Errorf("severity = %v, want LOG", rec.Severity)
	}
	if rec.SQLState != "00000" {
		t.Errorf("sqlstate = %q, want 00000", rec.SQLState)
	}
	if rec.BackendType != "client backend" {
		t.Errorf("backend_type = %q, want client backend", rec.BackendType)
	}
	if rec.AppName != "psql" {
		t.Errorf("app = %q, want psql", rec.AppName)
	}
	if rec.SessionLine != 42 {
		t.Errorf("session_line = %d, want 42", rec.SessionLine)
	}
	if !rec.OccurredAt.Equal(time.Date(2026, 5, 29, 12, 0, 0, 123000000, time.UTC)) {
		t.Errorf("occurred_at = %v, want 2026-05-29 12:00:00.123 UTC", rec.OccurredAt)
	}
	if !strings.Contains(rec.Message, "duration: 12.345 ms") {
		t.Errorf("message lost: %q", rec.Message)
	}
}

const stderrSample = `2026-05-29 12:00:00.123 UTC [12345] LOG:  duration: 12.345 ms  statement: SELECT 1`

func TestParseStderr_extractsExpectedFields(t *testing.T) {
	cfg := StderrConfig{Prefix: "%m [%p] "}
	rec, err := ParseStderr(stderrSample, time.Date(2026, 5, 29, 12, 0, 0, 500000000, time.UTC), cfg)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if rec.PID != 12345 {
		t.Errorf("pid = %d, want 12345", rec.PID)
	}
	if rec.Severity != SeverityLog {
		t.Errorf("severity = %v, want LOG", rec.Severity)
	}
	if !strings.Contains(rec.Message, "duration: 12.345 ms") {
		t.Errorf("message lost: %q", rec.Message)
	}
	if !rec.OccurredAt.Equal(time.Date(2026, 5, 29, 12, 0, 0, 123000000, time.UTC)) {
		t.Errorf("occurred_at = %v, want 2026-05-29 12:00:00.123 UTC", rec.OccurredAt)
	}
}

func TestParseStderr_continuationLinesAttachToMessage(t *testing.T) {
	multi := "2026-05-29 12:00:00.123 UTC [12345] LOG:  duration: 1 ms  statement: SELECT 1\n\tFROM users"
	rec, err := ParseStderr(multi, time.Now(), StderrConfig{Prefix: "%m [%p] "})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !strings.Contains(rec.Message, "FROM users") {
		t.Errorf("continuation not attached: %q", rec.Message)
	}
}

func TestParseStderr_unknownPrefixIsRecoverable(t *testing.T) {
	rec, err := ParseStderr("this is not a postgres log line", time.Now(), StderrConfig{Prefix: "%m [%p] "})
	if err != nil {
		t.Fatalf("must not error on unrecognized line: %v", err)
	}
	if rec.Severity != SeverityUnknown {
		t.Errorf("expected SeverityUnknown for unrecognized line, got %v", rec.Severity)
	}
	if !strings.Contains(rec.Message, "this is not a postgres log line") {
		t.Errorf("raw line should land in Message: %q", rec.Message)
	}
}
