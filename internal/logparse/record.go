package logparse

import "time"

// RawRecord is a single Postgres log entry after framing + parsing,
// but BEFORE classification. It contains everything both formats can
// carry; fields the source format doesn't supply are zero.
//
// RawRecord is an internal staging type. It deliberately carries the
// raw message (and statement text, etc.) so the classifier can run
// against it. Once classification has happened, callers split it into
// (LogEvent, LogPayload) and discard the RawRecord — the raw fields
// MUST NOT be transmitted off the collector.
type RawRecord struct {
	// Classification inputs ------------------------------------------
	LoggedAt     time.Time
	OccurredAt   time.Time
	Severity     Severity
	SQLState     string
	PID          int64
	BackendType  string
	DatabaseName string
	UserName     string
	AppName      string
	ClientAddr   string
	SessionLine  int64
	TxnID        int64

	// Sensitive payload -----------------------------------------------
	// Anything below this line may contain literal values from the
	// monitored database.
	Message       string
	Detail        string
	Hint          string
	StatementText string
	InternalQuery string
	ContextLine   string
}
