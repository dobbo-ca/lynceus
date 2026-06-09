package logparse

import (
	"strings"
	"testing"
	"time"
)

func TestClassify_recognisesCoreEvents(t *testing.T) {
	cases := []struct {
		name     string
		message  string
		severity Severity
		want     EventType
	}{
		{
			name:     "connection authorized",
			message:  "connection authorized: user=postgres database=postgres application_name=psql",
			severity: SeverityLog,
			want:     EventConnectionAuthorized,
		},
		{
			name:     "connection received",
			message:  "connection received: host=127.0.0.1 port=54321",
			severity: SeverityLog,
			want:     EventConnectionReceived,
		},
		{
			name:     "disconnection",
			message:  "disconnection: session time: 0:00:01.234 user=postgres database=postgres host=127.0.0.1 port=54321",
			severity: SeverityLog,
			want:     EventConnectionDisconnected,
		},
		{
			name:     "checkpoint starting",
			message:  "checkpoint starting: time",
			severity: SeverityLog,
			want:     EventCheckpointStarting,
		},
		{
			name:     "checkpoint complete",
			message:  "checkpoint complete: wrote 12 buffers (0.1%); 0 WAL file(s) added, 0 removed, 0 recycled; write=1.234 s, sync=0.005 s, total=1.245 s; ...",
			severity: SeverityLog,
			want:     EventCheckpointComplete,
		},
		{
			name:     "autovacuum",
			message:  "automatic vacuum of table \"app.public.users\": index scans: 0\npages: 0 removed, 100 remain ...",
			severity: SeverityLog,
			want:     EventAutovacuumCompleted,
		},
		{
			name:     "deadlock",
			message:  "deadlock detected",
			severity: SeverityError,
			want:     EventLockDeadlock,
		},
		{
			name:     "lock acquired after wait",
			message:  "process 12345 acquired ShareLock on transaction 56789 after 1234.567 ms",
			severity: SeverityLog,
			want:     EventLockAcquiredAfter,
		},
		{
			name:     "duration statement",
			message:  "duration: 12.345 ms  statement: SELECT 1",
			severity: SeverityLog,
			want:     EventQueryDuration,
		},
		{
			name:     "statement timeout",
			message:  "canceling statement due to statement timeout",
			severity: SeverityError,
			want:     EventQueryCanceledTimeout,
		},
		{
			name:     "constraint violation",
			message:  `duplicate key value violates unique constraint "users_pkey"`,
			severity: SeverityError,
			want:     EventErrorConstraintViolation,
		},
		{
			name:     "syntax error",
			message:  `syntax error at or near "FRMO"`,
			severity: SeverityError,
			want:     EventErrorSyntax,
		},
		{
			name:     "auto_explain plan",
			message:  "duration: 12.345 ms  plan:\nQuery Text: SELECT 1\nResult  (cost=0.00..0.01 rows=1 width=4)",
			severity: SeverityLog,
			want:     EventAutoExplainPlan,
		},
		{
			name:     "temp file",
			message:  "temporary file: path \"base/pgsql_tmp/pgsql_tmp123.0\", size 1048576",
			severity: SeverityLog,
			want:     EventTempFileCreated,
		},
		{
			name:     "unknown",
			message:  "something the rules have never seen",
			severity: SeverityLog,
			want:     EventUnclassified,
		},
	}

	c := NewClassifier(DefaultRules())
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := RawRecord{Message: tc.message, Severity: tc.severity}
			ev, _ := c.Classify(&rec)
			if ev.EventType != tc.want {
				t.Errorf("EventType = %q, want %q", ev.EventType, tc.want)
			}
		})
	}
}

func TestClassify_separatesEventFromPayload(t *testing.T) {
	rec := RawRecord{
		LoggedAt:      time.Date(2026, 5, 29, 12, 0, 1, 0, time.UTC),
		OccurredAt:    time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC),
		PID:           12345,
		Severity:      SeverityError,
		SQLState:      "23505",
		DatabaseName:  "app",
		UserName:      "postgres",
		AppName:       "psql",
		ClientAddr:    "10.0.0.5:54321",
		BackendType:   "client backend",
		SessionLine:   42,
		TxnID:         99,
		Message:       `duplicate key value violates unique constraint "users_pkey"`,
		Detail:        `Key (email)=(alice@example.com) already exists.`,
		StatementText: `INSERT INTO users(email) VALUES ('alice@example.com')`,
		Hint:          "",
	}

	ev, payload := NewClassifier(DefaultRules()).Classify(&rec)

	if ev.EventType != EventErrorConstraintViolation {
		t.Errorf("event_type = %q", ev.EventType)
	}
	if ev.Severity != SeverityError {
		t.Errorf("severity = %v", ev.Severity)
	}
	if ev.SQLState != "23505" {
		t.Errorf("sqlstate = %q", ev.SQLState)
	}
	if ev.DatabaseName != "app" {
		t.Errorf("database = %q", ev.DatabaseName)
	}
	if ev.ClientAddrHash == "" || ev.ClientAddrHash == "10.0.0.5:54321" {
		t.Errorf("client must be hashed; got %q", ev.ClientAddrHash)
	}

	for _, forbidden := range []string{
		"alice@example.com", "users_pkey", "INSERT INTO users",
	} {
		for _, s := range eventStringFields(&ev) {
			if strings.Contains(s, forbidden) {
				t.Fatalf("LITERAL LEAK: LogEvent field contains %q (event = %+v)", forbidden, ev)
			}
		}
	}

	if payload.Tier() != TierSensitive {
		t.Fatalf("payload tier = %v, want TierSensitive", payload.Tier())
	}
	if payload.StatementText != rec.StatementText {
		t.Errorf("payload.StatementText = %q", payload.StatementText)
	}
	if payload.Detail != rec.Detail {
		t.Errorf("payload.Detail = %q", payload.Detail)
	}
}

func TestClassify_emptyPayloadStaysEmpty(t *testing.T) {
	rec := RawRecord{
		Severity: SeverityLog,
		Message:  "connection authorized: user=postgres database=postgres",
	}
	ev, payload := NewClassifier(DefaultRules()).Classify(&rec)
	if ev.EventType != EventConnectionAuthorized {
		t.Errorf("event_type = %q", ev.EventType)
	}
	if payload.Message == "" {
		t.Error("classifier must always preserve Message in payload")
	}
}

func eventStringFields(ev *LogEvent) []string {
	return []string{
		string(ev.EventType),
		ev.Severity.String(),
		ev.BackendType,
		ev.DatabaseName,
		ev.UserName,
		ev.AppName,
		ev.ClientAddrHash,
		ev.SQLState,
	}
}
