# ly-cxe.1 — Postgres Log Line Parsing Framework + Event Classifier Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a reusable parser that consumes a stream of Postgres log lines (csvlog and stderr formats) and emits structured `LogEvent` records with classification (event type + severity + timestamps + process metadata) held strictly separate from the sensitive payload (statement text, bind params, detail message) so PII filters can act on payload only before any data leaves the collector.

**Architecture:** A new `internal/logparse` package layered in three responsibilities. (1) `Scanner` — turns a byte stream into a sequence of raw, deframed log records (handling csvlog quoting + stderr multi-line continuations). (2) `Parser` — turns each raw record into a `RawRecord` struct of typed Postgres log fields (no classification yet). (3) `Classifier` — pattern-matches the `message` text against a rules table and produces a `LogEvent` (T1 metadata: event type / severity / pid / db / timestamp) plus a separate `LogPayload` (T2: statement text, detail, hint) so downstream PII filters and the wire layer never mix the two. A new T1 proto message `LogEvent` is added to the wire contract and the existing privacy contract test is extended to assert it carries no payload-capable field.

**Tech Stack:** Go 1.26, `encoding/csv` (stdlib) for csvlog, `regexp` for stderr prefix parsing, classifier rules expressed as Go-native `*regexp.Regexp` patterns in a single registry file, protobuf via `proto/lynceus/v1/`, integration tests against real Postgres 16 via testcontainers-go, golden fixtures captured from a live container for both `log_destination` formats.

**Spec links:**
- `docs/specs/2026-05-29-lynceus-design.md` §2 (Privacy & Data-Classification Model), §3.1 (collector), §9 (Testing Strategy)
- `docs/specs/2026-05-29-lynceus-features.md` §6 (Log Insights — MUST feature, highest PII risk)
- Existing pattern reference: `internal/normalize/normalize.go`, `internal/collector/reader.go`, `internal/proto/lynceus/v1/contract_test.go`

---

## File Structure

```
lynceus/
  proto/lynceus/v1/
    log_event.proto                        # NEW: T1 LogEvent wire message
  internal/proto/lynceus/v1/
    contract_test.go                       # EXTEND: assert LogEvent is T1-clean
    (regenerated log_event.pb.go)          # generated, do not edit
  internal/logparse/
    doc.go                                 # package doc — privacy invariants
    severity.go                            # Severity enum + parsing
    event_type.go                          # EventType enum (vocabulary)
    record.go                              # RawRecord struct (parsed, not classified)
    payload.go                              # LogPayload (T2) + Tier tagging
    event.go                                # LogEvent (T1) — classification only
    scanner.go                              # framing: csvlog + stderr continuations
    scanner_test.go                         # unit tests for framing
    parser_csv.go                           # csvlog → RawRecord
    parser_stderr.go                        # stderr line prefix → RawRecord
    parser_test.go                          # unit tests for both parsers
    classifier.go                           # rules registry + Classify()
    classifier_test.go                      # unit tests across event vocabulary
    rules.go                                # initial rule set (~12 core events)
    framework.go                            # ParseStream — top-level wiring
    framework_test.go                       # end-to-end against real Postgres logs
    testdata/
      csvlog_sample.csv                     # captured from postgres:16 container
      stderr_sample.log                     # captured from postgres:16 container
```

Single responsibility per file. `record.go` / `payload.go` / `event.go` deliberately live as separate types because their tier classifications differ — keeping them in one file invites accidentally cross-pollinating fields. Reviewers should be able to look at `event.go` and confirm at a glance that no payload field exists on `LogEvent`.

---

## Task 1: T1 wire contract for LogEvent + extended privacy contract test

**Files:**
- Create: `proto/lynceus/v1/log_event.proto`
- Modify: `internal/proto/lynceus/v1/contract_test.go`
- Generated: `internal/proto/lynceus/v1/log_event.pb.go`

- [ ] **Step 1: Write the failing contract test extension.**

Append to `internal/proto/lynceus/v1/contract_test.go`:

```go
// TestLogEventHasOnlyClassificationFields enforces the T1 privacy guarantee
// for the log-insights pipeline: the LogEvent wire message must carry only
// classification metadata (event type, severity, timestamps, process info).
// It must NEVER carry the statement text, bind parameters, error detail,
// or any other field capable of holding a literal value from the monitored
// database. Sensitive payload travels in a separate T2 message (defined
// later) gated behind RBAC + audit.
func TestLogEventHasOnlyClassificationFields(t *testing.T) {
	allowed := map[string]struct{}{
		"event_type":         {},
		"severity":           {},
		"occurred_at_unix":   {},
		"logged_at_unix":     {},
		"pid":                {},
		"backend_type":       {},
		"database_name":      {},
		"user_name":          {},
		"application_name":   {},
		"client_addr_hash":   {}, // SHA-256 of client_addr — not the IP itself
		"sql_state":          {},
		"session_line_num":   {},
		"transaction_id":     {},
	}

	fields := (&lynceusv1.LogEvent{}).ProtoReflect().Descriptor().Fields()
	for i := 0; i < fields.Len(); i++ {
		name := string(fields.Get(i).Name())
		if _, ok := allowed[name]; !ok {
			t.Fatalf(
				"unexpected field %q in T1 LogEvent — possible literal leak. "+
					"T1 log events carry only classification metadata. Statement "+
					"text, bind params, error detail, hint, and the raw message "+
					"belong in a separate T2 LogPayload message gated behind "+
					"RBAC + audit (see docs/specs/2026-05-29-lynceus-design.md §2).",
				name,
			)
		}
	}
}

// TestLogEventScalarFieldShapes guards against type-changing regressions
// where a string field is replaced by bytes or a nested message that
// could embed unstructured content.
func TestLogEventScalarFieldShapes(t *testing.T) {
	fields := (&lynceusv1.LogEvent{}).ProtoReflect().Descriptor().Fields()
	wantString := []string{
		"event_type", "backend_type", "database_name", "user_name",
		"application_name", "client_addr_hash", "sql_state",
	}
	for _, fn := range wantString {
		f := fields.ByName(protoreflect.Name(fn))
		if f == nil {
			t.Fatalf("field %q missing from LogEvent", fn)
		}
		if got := f.Kind().String(); got != "string" {
			t.Fatalf("%s must be string kind, got %s", fn, got)
		}
	}
}
```

Add the import for `protoreflect` to the test file:

```go
import (
	"testing"

	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
	"google.golang.org/protobuf/reflect/protoreflect"
)
```

- [ ] **Step 2: Run the test to verify it fails.**

Run: `go test ./internal/proto/lynceus/v1/...`
Expected: FAIL — `undefined: lynceusv1.LogEvent` (the proto type doesn't exist yet).

- [ ] **Step 3: Define the proto.**

Create `proto/lynceus/v1/log_event.proto`:

```proto
// Lynceus T1 wire contract — log insights.
//
// LogEvent is the classification-only counterpart for a parsed Postgres
// log line. Any field capable of carrying literal values from the
// monitored database (statement text, bind parameters, error detail,
// hint, the raw log message itself) belongs in a separate T2
// LogPayload message — never here.
//
// The contract test in internal/proto/lynceus/v1/contract_test.go
// enforces the literal-free invariant. Update its allowlist whenever
// you legitimately add a normalized field.
syntax = "proto3";

package lynceus.v1;

option go_package = "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1;lynceusv1";

// LogEvent is one classified Postgres log event. Every field is either
// a fixed-vocabulary string (event_type, severity, sql_state), a Postgres
// catalog identifier (database_name, user_name), or a coarse numeric
// counter. client_addr is hashed at the collector (SHA-256) because raw
// IP addresses are PII under GDPR. No field carries the raw message.
message LogEvent {
  // Canonical event type from the Lynceus vocabulary, e.g.
  //   "connection.authorized", "connection.disconnected",
  //   "checkpoint.completed", "checkpoint.starting",
  //   "vacuum.completed", "vacuum.autovacuum_completed",
  //   "lock.deadlock_detected", "lock.acquired_after_wait",
  //   "query.duration", "query.canceled_due_to_timeout",
  //   "error.constraint_violation", "error.syntax",
  //   "auto_explain.plan", "temp_file.created", "log.unclassified".
  string event_type = 1;

  // PANIC | FATAL | ERROR | WARNING | NOTICE | INFO | LOG | DEBUG.
  string severity = 2;

  // Event timestamp, unix seconds UTC (when the event occurred in
  // Postgres — from the log line's own timestamp, not when we read it).
  int64 occurred_at_unix = 3;

  // When the collector observed the line. Lets the server detect skew.
  int64 logged_at_unix = 4;

  // Postgres backend pid.
  int64 pid = 5;

  // e.g. "client backend", "autovacuum worker", "checkpointer".
  string backend_type = 6;

  // Postgres database name (catalog identifier, not user data).
  string database_name = 7;

  // Postgres role name (catalog identifier).
  string user_name = 8;

  // application_name from the connection (catalog identifier — pgsql
  // truncates/sanitizes this; treated as low-risk normalized metadata).
  string application_name = 9;

  // SHA-256 (hex) of the client IP. Lets us count distinct clients
  // without storing the IP itself.
  string client_addr_hash = 10;

  // Postgres SQLSTATE 5-char code, e.g. "23505".
  string sql_state = 11;

  // Per-session line number from csvlog (cheap correlation key).
  int64 session_line_num = 12;

  // Postgres transaction id at the time of the event (or 0 if none).
  int64 transaction_id = 13;
}
```

- [ ] **Step 4: Regenerate Go code and run the contract test.**

Run: `make proto && go test ./internal/proto/lynceus/v1/...`
Expected: PASS — both `TestLogEventHasOnlyClassificationFields` and `TestLogEventScalarFieldShapes` pass.

- [ ] **Step 5: Commit.**

```bash
git add proto/lynceus/v1/log_event.proto internal/proto/lynceus/v1/
git commit -m "feat(proto): add T1 LogEvent wire message with privacy contract test"
```

---

## Task 2: Severity, event type, and core domain types

**Files:**
- Create: `internal/logparse/doc.go`, `internal/logparse/severity.go`, `internal/logparse/event_type.go`, `internal/logparse/record.go`, `internal/logparse/payload.go`, `internal/logparse/event.go`

- [ ] **Step 1: Write the failing tests for severity + payload tier tagging.**

Create `internal/logparse/severity_test.go`:

```go
package logparse

import "testing"

func TestParseSeverity(t *testing.T) {
	cases := []struct {
		in   string
		want Severity
	}{
		{"PANIC", SeverityPanic},
		{"FATAL", SeverityFatal},
		{"ERROR", SeverityError},
		{"WARNING", SeverityWarning},
		{"NOTICE", SeverityNotice},
		{"INFO", SeverityInfo},
		{"LOG", SeverityLog},
		{"DEBUG", SeverityDebug},
		{"DEBUG1", SeverityDebug},
		{"DEBUG5", SeverityDebug},
		{"", SeverityUnknown},
		{"NOT_A_SEVERITY", SeverityUnknown},
	}
	for _, c := range cases {
		if got := ParseSeverity(c.in); got != c.want {
			t.Errorf("ParseSeverity(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestSeverityString(t *testing.T) {
	if SeverityError.String() != "ERROR" {
		t.Errorf("SeverityError.String() = %q, want ERROR", SeverityError.String())
	}
	if SeverityUnknown.String() != "" {
		t.Errorf("SeverityUnknown must stringify to empty (so it doesn't reach the wire as a bogus value)")
	}
}
```

Create `internal/logparse/payload_test.go`:

```go
package logparse

import "testing"

func TestPayloadTier(t *testing.T) {
	p := LogPayload{StatementText: "SELECT * FROM users WHERE email = 'a@b'"}
	if p.Tier() != TierSensitive {
		t.Fatalf("any non-empty payload field must be TierSensitive (T2); got %v", p.Tier())
	}
	empty := LogPayload{}
	if empty.Tier() != TierEmpty {
		t.Fatalf("empty payload must be TierEmpty; got %v", empty.Tier())
	}
}

func TestLogEventHasNoPayloadFields(t *testing.T) {
	// Sanity check at the Go-struct level: someone reading LogEvent's
	// definition must not be tempted to add a Statement / Detail / Hint
	// field. We assert by reflection that no field name overlaps
	// with the payload field set.
	forbidden := map[string]struct{}{
		"StatementText": {}, "Detail": {}, "Hint": {},
		"BindParameters": {}, "RawMessage": {}, "Query": {},
		"InternalQuery":  {}, "ErrorContext": {},
	}
	checkNoForbiddenFields(t, LogEvent{}, forbidden)
}
```

(Helper `checkNoForbiddenFields` reflects over struct field names and fails on collision. Defined in the same `_test.go` file.)

```go
// In payload_test.go
import "reflect"

func checkNoForbiddenFields(t *testing.T, v any, forbidden map[string]struct{}) {
	t.Helper()
	rt := reflect.TypeOf(v)
	for i := 0; i < rt.NumField(); i++ {
		name := rt.Field(i).Name
		if _, bad := forbidden[name]; bad {
			t.Fatalf("type %s has forbidden payload-bearing field %q — "+
				"T1 LogEvent must carry only classification metadata", rt.Name(), name)
		}
	}
}
```

- [ ] **Step 2: Run tests — expect FAIL (types don't exist).**

Run: `go test ./internal/logparse/...`
Expected: FAIL — `undefined: Severity`, `undefined: LogPayload`, `undefined: LogEvent`.

- [ ] **Step 3: Create the package doc.**

Create `internal/logparse/doc.go`:

```go
// Package logparse turns Postgres log lines into structured events.
//
// PRIVACY INVARIANT (do not change without reviewing docs/specs/
// 2026-05-29-lynceus-design.md §2):
//
//   - LogEvent (this file's primary T1 output) carries classification
//     metadata only — event type, severity, timestamps, pid, database
//     name, user name, application name, SQLSTATE, and a HASHED client
//     address. It never carries the raw log message, statement text,
//     bind parameters, error detail, or hint.
//
//   - LogPayload carries every sensitive substring extracted from the
//     log line. Its zero value is the empty (TierEmpty) payload; any
//     non-empty payload is TierSensitive (T2) and may only be
//     transmitted off the collector after PII filters have run on it
//     AND the destination server has T2 capture explicitly enabled.
//
//   - The two types travel separately end-to-end. Downstream PII
//     filters (filter_log_secret, filter_query_text, etc.) operate on
//     LogPayload; the wire-protocol T1 path serializes only LogEvent.
//
// The package supports both Postgres log_destination formats:
//
//   - csvlog (preferred — unambiguous quoting)
//   - stderr (best-effort; uses a configurable log_line_prefix template)
package logparse
```

- [ ] **Step 4: Implement Severity.**

Create `internal/logparse/severity.go`:

```go
package logparse

import "strings"

// Severity is the Postgres log severity (per src/include/utils/elog.h).
type Severity int

const (
	SeverityUnknown Severity = iota
	SeverityDebug
	SeverityLog
	SeverityInfo
	SeverityNotice
	SeverityWarning
	SeverityError
	SeverityFatal
	SeverityPanic
)

// String returns the canonical uppercase name, or "" for Unknown so it
// never reaches the wire as a bogus severity.
func (s Severity) String() string {
	switch s {
	case SeverityDebug:
		return "DEBUG"
	case SeverityLog:
		return "LOG"
	case SeverityInfo:
		return "INFO"
	case SeverityNotice:
		return "NOTICE"
	case SeverityWarning:
		return "WARNING"
	case SeverityError:
		return "ERROR"
	case SeverityFatal:
		return "FATAL"
	case SeverityPanic:
		return "PANIC"
	}
	return ""
}

// ParseSeverity recognises every level Postgres can emit, including
// DEBUG1..DEBUG5 which all collapse to SeverityDebug.
func ParseSeverity(s string) Severity {
	s = strings.ToUpper(strings.TrimSpace(s))
	switch s {
	case "PANIC":
		return SeverityPanic
	case "FATAL":
		return SeverityFatal
	case "ERROR":
		return SeverityError
	case "WARNING":
		return SeverityWarning
	case "NOTICE":
		return SeverityNotice
	case "INFO":
		return SeverityInfo
	case "LOG":
		return SeverityLog
	}
	if strings.HasPrefix(s, "DEBUG") {
		return SeverityDebug
	}
	return SeverityUnknown
}
```

- [ ] **Step 5: Define the event-type vocabulary.**

Create `internal/logparse/event_type.go`:

```go
package logparse

// EventType is the canonical, T1-safe classification of a log line.
// New event types are added here AND a matching rule is added in
// rules.go. Strings are intentionally dotted hierarchies so consumers
// can filter on prefixes (e.g. all "vacuum.*").
type EventType string

const (
	EventUnclassified EventType = "log.unclassified"

	EventConnectionAuthorized   EventType = "connection.authorized"
	EventConnectionReceived     EventType = "connection.received"
	EventConnectionDisconnected EventType = "connection.disconnected"
	EventConnectionAuthFailed   EventType = "connection.auth_failed"

	EventCheckpointStarting EventType = "checkpoint.starting"
	EventCheckpointComplete EventType = "checkpoint.completed"

	EventVacuumCompleted     EventType = "vacuum.completed"
	EventAutovacuumCompleted EventType = "vacuum.autovacuum_completed"

	EventLockDeadlock      EventType = "lock.deadlock_detected"
	EventLockAcquiredAfter EventType = "lock.acquired_after_wait"

	EventQueryDuration       EventType = "query.duration"
	EventQueryCanceledTimeout EventType = "query.canceled_due_to_timeout"

	EventErrorConstraintViolation EventType = "error.constraint_violation"
	EventErrorSyntax              EventType = "error.syntax"

	EventAutoExplainPlan EventType = "auto_explain.plan"
	EventTempFileCreated EventType = "temp_file.created"
)
```

- [ ] **Step 6: Define RawRecord.**

Create `internal/logparse/record.go`:

```go
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
	LoggedAt     time.Time // when we observed the line
	OccurredAt   time.Time // postgres-supplied timestamp on the line
	Severity     Severity
	SQLState     string // 5-char SQLSTATE, "" if not present
	PID          int64
	BackendType  string
	DatabaseName string
	UserName     string
	AppName      string
	ClientAddr   string // raw IP; hashed before LogEvent is produced
	SessionLine  int64
	TxnID        int64

	// Sensitive payload -----------------------------------------------
	// Anything below this line may contain literal values from the
	// monitored database.
	Message       string // primary log message text
	Detail        string // DETAIL: line
	Hint          string // HINT: line
	StatementText string // STATEMENT: line
	InternalQuery string // INTERNAL QUERY: line
	ContextLine   string // CONTEXT: line
}
```

- [ ] **Step 7: Define LogPayload and its tier tagging.**

Create `internal/logparse/payload.go`:

```go
package logparse

// PayloadTier classifies a LogPayload at runtime so downstream filters
// and the wire layer can refuse to ship sensitive payload unless the
// destination server has T2 enabled.
type PayloadTier int

const (
	TierEmpty     PayloadTier = 0 // nothing sensitive to ship
	TierSensitive PayloadTier = 2 // T2: contains literal-bearing strings
)

// LogPayload is the sensitive (T2) component of a parsed log line.
// Every field below MAY contain literal values from the monitored
// database. Callers MUST run PII filters (filter_log_secret etc.) over
// these fields before transmission, and MUST NOT attach a non-empty
// LogPayload to a T1 wire message.
type LogPayload struct {
	Message       string
	Detail        string
	Hint          string
	StatementText string
	InternalQuery string
	ContextLine   string
}

// Tier reports TierEmpty if every field is empty, otherwise
// TierSensitive. There is no in-between: any non-empty payload requires
// T2 treatment.
func (p LogPayload) Tier() PayloadTier {
	if p.Message == "" && p.Detail == "" && p.Hint == "" &&
		p.StatementText == "" && p.InternalQuery == "" && p.ContextLine == "" {
		return TierEmpty
	}
	return TierSensitive
}
```

- [ ] **Step 8: Define LogEvent (T1, classification only).**

Create `internal/logparse/event.go`:

```go
package logparse

import "time"

// LogEvent is the T1, literal-free classification of a Postgres log
// line. It corresponds field-for-field to the protobuf lynceus.v1.LogEvent
// message and is the only output of this package that may be
// transmitted as part of a T1 snapshot.
//
// DO NOT add fields here that could carry a literal value from the
// monitored database. The reviewer test in payload_test.go and the
// proto contract test will both fail if you do.
type LogEvent struct {
	EventType       EventType
	Severity        Severity
	OccurredAt      time.Time
	LoggedAt        time.Time
	PID             int64
	BackendType     string
	DatabaseName    string
	UserName        string
	AppName         string
	ClientAddrHash  string // SHA-256 hex of ClientAddr; never the raw IP
	SQLState        string
	SessionLineNum  int64
	TransactionID   int64
}
```

- [ ] **Step 9: Run tests — expect PASS.**

Run: `go test ./internal/logparse/...`
Expected: PASS (`TestParseSeverity`, `TestSeverityString`, `TestPayloadTier`, `TestLogEventHasNoPayloadFields`).

- [ ] **Step 10: Commit.**

```bash
git add internal/logparse/doc.go internal/logparse/severity.go internal/logparse/severity_test.go internal/logparse/event_type.go internal/logparse/record.go internal/logparse/payload.go internal/logparse/payload_test.go internal/logparse/event.go
git commit -m "feat(logparse): core domain types — Severity, EventType, RawRecord, T1 LogEvent, T2 LogPayload"
```

---

## Task 3: Scanner — framing for csvlog and stderr streams

**Files:**
- Create: `internal/logparse/scanner.go`, `internal/logparse/scanner_test.go`

- [ ] **Step 1: Write the failing scanner tests.**

Create `internal/logparse/scanner_test.go`:

```go
package logparse

import (
	"strings"
	"testing"
)

// csvlog rows are RFC-4180-quoted; multi-line statements live inside
// quoted fields and survive embedded newlines and doubled quotes.
func TestScanner_CSV_HandlesQuotedNewlines(t *testing.T) {
	input := `2026-05-29 12:00:00.123 UTC,"postgres","app","12345","127.0.0.1:54321",` +
		`"6500a000.3039","2026-05-29 12:00:00 UTC","3/12","0","LOG","00000",` +
		`"duration: 2.345 ms  statement: SELECT 1
FROM users
WHERE id = 1",,,,,,,,"psql","client backend",,"0"
2026-05-29 12:00:01.000 UTC,"postgres","app","12345","",,,,"0","LOG","00000",` +
		`"connection authorized: user=postgres database=postgres",,,,,,,,"psql","client backend",,"0"
`
	s := NewScanner(strings.NewReader(input), FormatCSV)
	var got []string
	for s.Scan() {
		got = append(got, s.Text())
	}
	if err := s.Err(); err != nil {
		t.Fatalf("scanner err: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 csv records, got %d: %q", len(got), got)
	}
	if !strings.Contains(got[0], "FROM users") {
		t.Errorf("first record should preserve embedded newline content; got %q", got[0])
	}
}

// stderr lines may continue onto subsequent lines with a TAB prefix
// (the canonical Postgres continuation marker). The scanner must
// stitch those back into a single record.
func TestScanner_Stderr_StitchesContinuations(t *testing.T) {
	input := "2026-05-29 12:00:00.123 UTC [12345] LOG:  duration: 2.345 ms  statement: SELECT 1\n" +
		"\tFROM users\n" +
		"\tWHERE id = 1\n" +
		"2026-05-29 12:00:01.000 UTC [12345] LOG:  connection authorized: user=postgres database=postgres\n"

	s := NewScanner(strings.NewReader(input), FormatStderr)
	var got []string
	for s.Scan() {
		got = append(got, s.Text())
	}
	if err := s.Err(); err != nil {
		t.Fatalf("scanner err: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 stitched records, got %d: %q", len(got), got)
	}
	if !strings.Contains(got[0], "FROM users") || !strings.Contains(got[0], "WHERE id = 1") {
		t.Errorf("continuation lines were not stitched; got %q", got[0])
	}
}

func TestScanner_EmptyInputProducesNoRecords(t *testing.T) {
	for _, f := range []Format{FormatCSV, FormatStderr} {
		s := NewScanner(strings.NewReader(""), f)
		if s.Scan() {
			t.Errorf("format %v: empty input should not produce a record", f)
		}
		if err := s.Err(); err != nil {
			t.Errorf("format %v: err on empty input: %v", f, err)
		}
	}
}
```

- [ ] **Step 2: Run — expect FAIL.**

Run: `go test ./internal/logparse/ -run TestScanner`
Expected: FAIL — `undefined: NewScanner`.

- [ ] **Step 3: Implement the scanner.**

Create `internal/logparse/scanner.go`:

```go
package logparse

import (
	"bufio"
	"encoding/csv"
	"io"
	"strings"
)

// Format selects the framing rules.
type Format int

const (
	// FormatCSV expects Postgres csvlog. Records are RFC-4180 rows.
	FormatCSV Format = iota
	// FormatStderr expects the default Postgres stderr log_destination.
	// One record per line, with continuation lines prefixed by TAB.
	FormatStderr
)

// Scanner deframes a Postgres log byte stream into individual records.
// It does NOT parse the records into fields — that is the Parser's job.
type Scanner struct {
	format Format
	csv    *csv.Reader
	br     *bufio.Reader
	cur    string
	err    error
}

// NewScanner returns a Scanner that reads from r in the given format.
func NewScanner(r io.Reader, format Format) *Scanner {
	s := &Scanner{format: format}
	switch format {
	case FormatCSV:
		c := csv.NewReader(r)
		c.FieldsPerRecord = -1   // csvlog field count varies by pg version
		c.ReuseRecord = false
		c.LazyQuotes = false
		s.csv = c
	case FormatStderr:
		s.br = bufio.NewReaderSize(r, 64*1024)
	}
	return s
}

// Scan advances to the next record. Returns false on EOF or error.
func (s *Scanner) Scan() bool {
	switch s.format {
	case FormatCSV:
		row, err := s.csv.Read()
		if err == io.EOF {
			return false
		}
		if err != nil {
			s.err = err
			return false
		}
		// Re-encode as a CSV line so the parser owns one source-of-truth
		// parser; cheaper alternatives exist but this keeps the
		// CSV scanner and CSV parser stages composable for tests.
		var b strings.Builder
		w := csv.NewWriter(&b)
		_ = w.Write(row)
		w.Flush()
		s.cur = strings.TrimRight(b.String(), "\n")
		return true
	case FormatStderr:
		return s.scanStderr()
	}
	return false
}

func (s *Scanner) scanStderr() bool {
	// Read a line. If the next line starts with a TAB it is a
	// continuation and we keep appending until the next non-TAB.
	var b strings.Builder
	for {
		peek, _ := s.br.Peek(1)
		if len(peek) == 0 {
			// EOF: flush if we have a buffered record.
			if b.Len() == 0 {
				return false
			}
			s.cur = b.String()
			return true
		}
		if b.Len() > 0 && peek[0] != '\t' {
			s.cur = b.String()
			return true
		}
		line, err := s.br.ReadString('\n')
		if line != "" {
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(strings.TrimRight(line, "\n"))
		}
		if err == io.EOF {
			if b.Len() == 0 {
				return false
			}
			s.cur = b.String()
			return true
		}
		if err != nil {
			s.err = err
			return false
		}
	}
}

// Text returns the most recent record produced by Scan.
func (s *Scanner) Text() string { return s.cur }

// Err returns the first non-EOF error encountered, if any.
func (s *Scanner) Err() error { return s.err }
```

- [ ] **Step 4: Run — expect PASS.**

Run: `go test ./internal/logparse/ -run TestScanner -v`
Expected: PASS (all three TestScanner_* tests).

- [ ] **Step 5: Commit.**

```bash
git add internal/logparse/scanner.go internal/logparse/scanner_test.go
git commit -m "feat(logparse): framing scanner for csvlog and stderr formats"
```

---

## Task 4: Parsers — csvlog and stderr → RawRecord

**Files:**
- Create: `internal/logparse/parser_csv.go`, `internal/logparse/parser_stderr.go`, `internal/logparse/parser_test.go`

- [ ] **Step 1: Write the failing parser tests.**

Create `internal/logparse/parser_test.go`:

```go
package logparse

import (
	"strings"
	"testing"
	"time"
)

// One canonical csvlog row covering common fields.
const csvSample = `2026-05-29 12:00:00.123 UTC,"postgres","app","12345",` +
	`"127.0.0.1:54321","6500a000.3039","2026-05-29 12:00:00 UTC","3/12",` +
	`"42","LOG","00000","duration: 12.345 ms  statement: SELECT 1",,,,,,` +
	`"psql","client backend",,"0"`

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
	// Default Postgres log_line_prefix in postgres:16 image is
	// "%m [%p] " — timestamp with ms, then "[pid] ".
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
	// If the prefix doesn't match, we still get a record with the raw
	// line in Message and Severity=Unknown — the classifier will tag
	// it log.unclassified rather than crashing the pipeline.
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
```

- [ ] **Step 2: Run — expect FAIL.**

Run: `go test ./internal/logparse/ -run TestParse`
Expected: FAIL — `undefined: ParseCSV`, `undefined: ParseStderr`.

- [ ] **Step 3: Implement the csvlog parser.**

Create `internal/logparse/parser_csv.go`:

```go
package logparse

import (
	"encoding/csv"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// CSV column positions in postgres:16 csvlog (per src/backend/utils/error/elog.c).
// We index by name to keep the parser readable.
const (
	csvIdxLogTime         = 0
	csvIdxUserName        = 1
	csvIdxDatabase        = 2
	csvIdxPID             = 3
	csvIdxConnFrom        = 4
	csvIdxSessionID       = 5
	csvIdxSessionStart    = 6
	csvIdxVirtualXID      = 7
	csvIdxSessionLineNum  = 8
	csvIdxSeverity        = 9
	csvIdxSQLState        = 10
	csvIdxMessage         = 11
	csvIdxDetail          = 12
	csvIdxHint            = 13
	csvIdxInternalQuery   = 14
	csvIdxInternalQueryPos = 15
	csvIdxContext         = 16
	csvIdxQuery           = 17
	csvIdxQueryPos        = 18
	csvIdxLocation        = 19
	csvIdxApplicationName = 20
	csvIdxBackendType     = 21
	csvIdxLeaderPID       = 22
	csvIdxTransactionID   = 23
)

// ParseCSV turns one csvlog row into a RawRecord. loggedAt is the
// time the caller observed the line (used for logged_at_unix).
func ParseCSV(line string, loggedAt time.Time) (RawRecord, error) {
	r := csv.NewReader(strings.NewReader(line))
	r.FieldsPerRecord = -1
	r.LazyQuotes = false
	row, err := r.Read()
	if err != nil {
		return RawRecord{}, fmt.Errorf("csv read: %w", err)
	}
	get := func(i int) string {
		if i >= 0 && i < len(row) {
			return row[i]
		}
		return ""
	}

	rec := RawRecord{
		LoggedAt:      loggedAt,
		OccurredAt:    parseCSVTimestamp(get(csvIdxLogTime)),
		UserName:      get(csvIdxUserName),
		DatabaseName:  get(csvIdxDatabase),
		PID:           atoi64(get(csvIdxPID)),
		ClientAddr:    get(csvIdxConnFrom),
		SessionLine:   atoi64(get(csvIdxSessionLineNum)),
		Severity:      ParseSeverity(get(csvIdxSeverity)),
		SQLState:      get(csvIdxSQLState),
		Message:       get(csvIdxMessage),
		Detail:        get(csvIdxDetail),
		Hint:          get(csvIdxHint),
		InternalQuery: get(csvIdxInternalQuery),
		ContextLine:   get(csvIdxContext),
		StatementText: get(csvIdxQuery),
		AppName:       get(csvIdxApplicationName),
		BackendType:   get(csvIdxBackendType),
		TxnID:         atoi64(get(csvIdxTransactionID)),
	}
	return rec, nil
}

// parseCSVTimestamp recognises Postgres's csvlog format
// "2006-01-02 15:04:05.000 MST".
func parseCSVTimestamp(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{
		"2006-01-02 15:04:05.000 MST",
		"2006-01-02 15:04:05 MST",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func atoi64(s string) int64 {
	if s == "" {
		return 0
	}
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}
```

- [ ] **Step 4: Implement the stderr parser.**

Create `internal/logparse/parser_stderr.go`:

```go
package logparse

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// StderrConfig configures the stderr parser. The Prefix matches what
// the monitored Postgres has set as log_line_prefix; we currently
// support the two escapes that account for >95% of real-world configs:
// %m (millisecond timestamp) and %p (pid).
type StderrConfig struct {
	Prefix string
}

// Each token we expand and the regex fragment it becomes.
var stderrTokens = []struct {
	tok, expr string
}{
	{"%m", `(?P<m>\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\.\d{3} [A-Z]+)`},
	{"%t", `(?P<t>\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2} [A-Z]+)`},
	{"%p", `(?P<p>\d+)`},
	{"%a", `(?P<a>[^ \[]*)`},
	{"%u", `(?P<u>[^ \[]*)`},
	{"%d", `(?P<d>[^ \[]*)`},
	{"%h", `(?P<h>[^ \[]*)`},
}

func compilePrefix(prefix string) (*regexp.Regexp, error) {
	expr := regexp.QuoteMeta(prefix)
	for _, t := range stderrTokens {
		// QuoteMeta escaped the % to \%, so undo that.
		expr = strings.ReplaceAll(expr, regexp.QuoteMeta(t.tok), t.expr)
	}
	// Then the canonical stderr trailer: "SEVERITY:  message"
	expr = "^" + expr + `(?P<sev>PANIC|FATAL|ERROR|WARNING|NOTICE|INFO|LOG|DEBUG[1-5]?):  ?(?P<msg>.*)$`
	return regexp.Compile(expr)
}

// ParseStderr parses one stitched stderr record (newlines between
// continuation lines are preserved by the scanner). If the prefix
// does not match the line, the record is returned with the entire
// line in Message and SeverityUnknown — the classifier will mark it
// log.unclassified rather than dropping it.
func ParseStderr(line string, loggedAt time.Time, cfg StderrConfig) (RawRecord, error) {
	re, err := compilePrefix(cfg.Prefix)
	if err != nil {
		return RawRecord{}, fmt.Errorf("compile prefix: %w", err)
	}

	// Split off continuation lines so the regex only has to match the
	// first physical line. Continuations get appended back into Message.
	first, rest, _ := strings.Cut(line, "\n")
	m := re.FindStringSubmatch(first)
	if m == nil {
		// Unknown shape — preserve the line for classification but
		// flag severity unknown.
		rec := RawRecord{LoggedAt: loggedAt, Message: line}
		return rec, nil
	}

	cap := func(name string) string {
		i := re.SubexpIndex(name)
		if i < 0 || i >= len(m) {
			return ""
		}
		return m[i]
	}

	rec := RawRecord{
		LoggedAt:    loggedAt,
		OccurredAt:  parseStderrTimestamp(cap("m"), cap("t")),
		PID:         atoi64(cap("p")),
		AppName:     cap("a"),
		UserName:    cap("u"),
		DatabaseName: cap("d"),
		ClientAddr:  cap("h"),
		Severity:    ParseSeverity(cap("sev")),
		Message:     cap("msg"),
	}
	if rest != "" {
		// Stderr continuation: the scanner left embedded "\n\t"; keep
		// content but strip the tabs so the classifier sees clean text.
		clean := strings.ReplaceAll(rest, "\n\t", "\n")
		rec.Message = rec.Message + "\n" + clean
	}
	return rec, nil
}

func parseStderrTimestamp(m, t string) time.Time {
	if m != "" {
		if ts, err := time.Parse("2006-01-02 15:04:05.000 MST", m); err == nil {
			return ts.UTC()
		}
	}
	if t != "" {
		if ts, err := time.Parse("2006-01-02 15:04:05 MST", t); err == nil {
			return ts.UTC()
		}
	}
	return time.Time{}
}
```

- [ ] **Step 5: Run — expect PASS.**

Run: `go test ./internal/logparse/ -run TestParse -v`
Expected: PASS (all four TestParse* tests).

- [ ] **Step 6: Commit.**

```bash
git add internal/logparse/parser_csv.go internal/logparse/parser_stderr.go internal/logparse/parser_test.go
git commit -m "feat(logparse): csvlog and stderr parsers producing RawRecord"
```

---

## Task 5: Classifier + initial rule set

**Files:**
- Create: `internal/logparse/classifier.go`, `internal/logparse/rules.go`, `internal/logparse/classifier_test.go`

- [ ] **Step 1: Write the failing classifier tests.**

Create `internal/logparse/classifier_test.go`:

```go
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
			ev, _ := c.Classify(rec)
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

	ev, payload := NewClassifier(DefaultRules()).Classify(rec)

	// T1 LogEvent must carry the classification fields...
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

	// ...and MUST NOT carry any field whose name matches a payload field.
	for _, forbidden := range []string{
		"alice@example.com", "users_pkey", "INSERT INTO users",
	} {
		// Walk every exported string field on LogEvent.
		for _, s := range eventStringFields(ev) {
			if strings.Contains(s, forbidden) {
				t.Fatalf("LITERAL LEAK: LogEvent field contains %q (event = %+v)", forbidden, ev)
			}
		}
	}

	// T2 LogPayload must carry the sensitive bits and tag itself accordingly.
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
	ev, payload := NewClassifier(DefaultRules()).Classify(rec)
	if ev.EventType != EventConnectionAuthorized {
		t.Errorf("event_type = %q", ev.EventType)
	}
	// Note: even "connection authorized" has the message itself, which
	// is classification-derivable but still potentially literal-bearing
	// (some installations log custom application_name strings). It
	// flows as payload — classification never strips it.
	if payload.Message == "" {
		t.Error("classifier must always preserve Message in payload")
	}
}

// eventStringFields returns every exported string field on a LogEvent.
// Defined here (test-only) so the test can iterate without listing
// fields by hand.
func eventStringFields(ev LogEvent) []string {
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
```

- [ ] **Step 2: Run — expect FAIL.**

Run: `go test ./internal/logparse/ -run TestClassify`
Expected: FAIL — `undefined: NewClassifier`, `undefined: DefaultRules`.

- [ ] **Step 3: Implement the classifier.**

Create `internal/logparse/classifier.go`:

```go
package logparse

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
)

// Rule is one entry in the classification table.
//
// New event types are added by appending a Rule to DefaultRules. The
// classifier is a linear scan in declaration order, so put more
// specific rules above more general ones (e.g. auto_explain.plan
// must come BEFORE query.duration — both start with "duration: ").
type Rule struct {
	EventType EventType
	// Match returns true if the rule applies. RawRecord is supplied
	// rather than just the message so rules can also inspect severity,
	// SQLSTATE, etc.
	Match func(RawRecord) bool
}

// Classifier turns a RawRecord into a (LogEvent, LogPayload) pair.
type Classifier struct {
	rules []Rule
}

// NewClassifier returns a Classifier that scans rules in order.
func NewClassifier(rules []Rule) *Classifier {
	return &Classifier{rules: rules}
}

// Classify returns the T1 event and the T2 payload split out from rec.
// The returned LogEvent carries no payload-bearing field; the
// LogPayload carries every sensitive substring.
func (c *Classifier) Classify(rec RawRecord) (LogEvent, LogPayload) {
	ev := LogEvent{
		EventType:      EventUnclassified,
		Severity:       rec.Severity,
		OccurredAt:     rec.OccurredAt,
		LoggedAt:       rec.LoggedAt,
		PID:            rec.PID,
		BackendType:    rec.BackendType,
		DatabaseName:   rec.DatabaseName,
		UserName:       rec.UserName,
		AppName:        rec.AppName,
		ClientAddrHash: hashClientAddr(rec.ClientAddr),
		SQLState:       rec.SQLState,
		SessionLineNum: rec.SessionLine,
		TransactionID:  rec.TxnID,
	}
	for _, r := range c.rules {
		if r.Match(rec) {
			ev.EventType = r.EventType
			break
		}
	}
	payload := LogPayload{
		Message:       rec.Message,
		Detail:        rec.Detail,
		Hint:          rec.Hint,
		StatementText: rec.StatementText,
		InternalQuery: rec.InternalQuery,
		ContextLine:   rec.ContextLine,
	}
	return ev, payload
}

func hashClientAddr(addr string) string {
	if addr == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(addr))
	return hex.EncodeToString(sum[:])
}

// reMatch is a small helper so rule definitions read cleanly.
func reMatch(re *regexp.Regexp) func(RawRecord) bool {
	return func(rec RawRecord) bool { return re.MatchString(rec.Message) }
}

// reMatchWith adds an additional precondition (e.g. severity).
func reMatchWith(re *regexp.Regexp, pre func(RawRecord) bool) func(RawRecord) bool {
	return func(rec RawRecord) bool {
		return pre(rec) && re.MatchString(rec.Message)
	}
}
```

- [ ] **Step 4: Implement the rule set.**

Create `internal/logparse/rules.go`:

```go
package logparse

import "regexp"

// DefaultRules is the v1 vocabulary. Each rule's regex is anchored
// loosely (we use .MatchString, not full-match) so trailing fields
// don't break it. Ordering matters: more specific rules first.
//
// To add a new event:
//   1. Add an EventType constant in event_type.go.
//   2. Append a Rule here, BEFORE any broader rule it could collide with.
//   3. Add a test case in classifier_test.go.
func DefaultRules() []Rule {
	var (
		// auto_explain emits "duration: NN ms  plan:" — must match before
		// the bare "duration: NN ms  statement:" rule.
		reAutoExplain    = regexp.MustCompile(`(?m)^duration: [0-9.]+ ms\s+plan:`)
		reDuration       = regexp.MustCompile(`(?m)^duration: [0-9.]+ ms\s+(statement|execute|parse|bind)\b`)
		reConnAuthorized = regexp.MustCompile(`^connection authorized:`)
		reConnReceived   = regexp.MustCompile(`^connection received:`)
		reDisconnect     = regexp.MustCompile(`^disconnection: session time:`)
		reAuthFailed     = regexp.MustCompile(`^(password authentication failed|no pg_hba\.conf entry|authentication failed)`)
		reCheckpointStart = regexp.MustCompile(`^checkpoint starting:`)
		reCheckpointDone  = regexp.MustCompile(`^checkpoint complete:`)
		reAutovacuum      = regexp.MustCompile(`^automatic vacuum of table`)
		reManualVacuum    = regexp.MustCompile(`^vacuuming "[^"]+"`)
		reDeadlock        = regexp.MustCompile(`^deadlock detected`)
		reLockAcquired    = regexp.MustCompile(`acquired \w+Lock on .* after [0-9.]+ ms`)
		reTimeoutCancel   = regexp.MustCompile(`^canceling statement due to (statement|lock|idle-in-transaction-session) timeout`)
		reConstraint      = regexp.MustCompile(`violates (unique|foreign key|check|not-null) constraint`)
		reSyntax          = regexp.MustCompile(`^syntax error at or near`)
		reTempFile        = regexp.MustCompile(`^temporary file:`)
	)

	isError := func(rec RawRecord) bool {
		return rec.Severity == SeverityError || rec.Severity == SeverityFatal
	}

	return []Rule{
		// Specific-before-general.
		{EventType: EventAutoExplainPlan, Match: reMatch(reAutoExplain)},
		{EventType: EventQueryDuration, Match: reMatch(reDuration)},

		{EventType: EventConnectionAuthorized, Match: reMatch(reConnAuthorized)},
		{EventType: EventConnectionReceived, Match: reMatch(reConnReceived)},
		{EventType: EventConnectionDisconnected, Match: reMatch(reDisconnect)},
		{EventType: EventConnectionAuthFailed, Match: reMatchWith(reAuthFailed, func(r RawRecord) bool {
			return r.Severity == SeverityFatal || r.Severity == SeverityError
		})},

		{EventType: EventCheckpointStarting, Match: reMatch(reCheckpointStart)},
		{EventType: EventCheckpointComplete, Match: reMatch(reCheckpointDone)},

		{EventType: EventAutovacuumCompleted, Match: reMatch(reAutovacuum)},
		{EventType: EventVacuumCompleted, Match: reMatch(reManualVacuum)},

		{EventType: EventLockDeadlock, Match: reMatchWith(reDeadlock, isError)},
		{EventType: EventLockAcquiredAfter, Match: reMatch(reLockAcquired)},

		{EventType: EventQueryCanceledTimeout, Match: reMatchWith(reTimeoutCancel, isError)},

		{EventType: EventErrorConstraintViolation, Match: reMatchWith(reConstraint, isError)},
		{EventType: EventErrorSyntax, Match: reMatchWith(reSyntax, isError)},

		{EventType: EventTempFileCreated, Match: reMatch(reTempFile)},
	}
}
```

- [ ] **Step 5: Run — expect PASS.**

Run: `go test ./internal/logparse/ -run TestClassify -v`
Expected: PASS (all three TestClassify_* tests, including the per-case sub-tests).

- [ ] **Step 6: Commit.**

```bash
git add internal/logparse/classifier.go internal/logparse/rules.go internal/logparse/classifier_test.go
git commit -m "feat(logparse): classifier with initial 15-event rule set and T1/T2 split"
```

---

## Task 6: ParseStream — top-level wiring + end-to-end test against real Postgres

**Files:**
- Create: `internal/logparse/framework.go`, `internal/logparse/framework_test.go`, `internal/logparse/testdata/csvlog_sample.csv`, `internal/logparse/testdata/stderr_sample.log`

- [ ] **Step 1: Write the failing end-to-end test.**

Create `internal/logparse/framework_test.go`:

```go
// Integration test for the full log-parsing framework. Spins up real
// Postgres 16 with csvlog enabled, executes literal-bearing queries
// that produce known log lines, reads the produced log file back, runs
// it through ParseStream, and asserts:
//   - Every produced LogEvent has a recognised classification.
//   - No LogEvent string field contains any of the canary literals
//     that we deliberately seeded into the workload.
//   - The corresponding LogPayload carries those literals (so PII
//     filters downstream have something to redact).
package logparse_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/dobbo-ca/lynceus/internal/logparse"
)

func TestParseStream_realPostgresCsvlog(t *testing.T) {
	ctx := context.Background()

	c, err := tcpostgres.Run(ctx,
		"postgres:16",
		tcpostgres.WithDatabase("lynceus_logtest"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		// Force csvlog into a known location inside the container.
		testcontainers.WithCmd(
			"postgres",
			"-c", "logging_collector=on",
			"-c", "log_destination=csvlog",
			"-c", "log_directory=/var/lib/postgresql/data/log",
			"-c", "log_filename=postgres.log",
			"-c", "log_min_duration_statement=0",
			"-c", "log_connections=on",
			"-c", "log_disconnections=on",
		),
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
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	const canaryEmail = "canary-leak@phi.example.com"
	const canaryTable = "patients_canary_table"

	// Seed a workload that produces several distinct events:
	//   - duration logs from log_min_duration_statement=0
	//   - constraint violation
	//   - syntax error
	for _, stmt := range []string{
		`CREATE TABLE ` + canaryTable + ` (id INT PRIMARY KEY, email TEXT)`,
		`INSERT INTO ` + canaryTable + ` VALUES (1, '` + canaryEmail + `')`,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("setup: %v", err)
		}
	}
	// Force a constraint violation (must not bubble up — the conn pool
	// would surface it as a Go error which is fine).
	_, _ = pool.Exec(ctx, `INSERT INTO `+canaryTable+` VALUES (1, '`+canaryEmail+`')`)
	// Force a syntax error.
	_, _ = pool.Exec(ctx, `SELCT 1`)

	// Wait briefly for Postgres's logging collector to flush.
	time.Sleep(2 * time.Second)

	// Pull the csv log out of the container.
	rc, err := c.CopyFileFromContainer(ctx, "/var/lib/postgresql/data/log/postgres.csv")
	if err != nil {
		// On some pg builds the rotated file uses .csv extension; the
		// non-csv file is what we configured. Try the configured name.
		rc, err = c.CopyFileFromContainer(ctx, "/var/lib/postgresql/data/log/postgres.log")
		if err != nil {
			t.Fatalf("copy log file: %v", err)
		}
	}
	defer rc.Close()

	tmp := filepath.Join(t.TempDir(), "postgres.csv")
	f, err := os.Create(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(f, rc); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	in, err := os.Open(tmp)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()

	events, payloads, err := logparse.ParseStream(in, logparse.Options{
		Format:   logparse.FormatCSV,
		LoggedAt: func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		t.Fatalf("ParseStream: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("no events parsed from real postgres log")
	}
	if len(events) != len(payloads) {
		t.Fatalf("event/payload count mismatch: %d vs %d", len(events), len(payloads))
	}

	// PRIVACY GUARANTEE: no LogEvent field carries the canaries.
	canaries := []string{canaryEmail, "canary-leak", "phi.example.com"}
	for i, ev := range events {
		for _, f := range []string{
			string(ev.EventType), ev.Severity.String(), ev.BackendType,
			ev.DatabaseName, ev.UserName, ev.AppName, ev.ClientAddrHash, ev.SQLState,
		} {
			for _, c := range canaries {
				if strings.Contains(f, c) {
					t.Fatalf("LITERAL LEAK in event %d field %q: contains %q", i, f, c)
				}
			}
		}
	}

	// Verify we saw at least the event types we deliberately provoked.
	wantSeen := map[logparse.EventType]bool{
		logparse.EventQueryDuration:           false,
		logparse.EventErrorConstraintViolation: false,
		logparse.EventErrorSyntax:             false,
	}
	for _, ev := range events {
		if _, want := wantSeen[ev.EventType]; want {
			wantSeen[ev.EventType] = true
		}
	}
	for et, seen := range wantSeen {
		if !seen {
			t.Errorf("expected to see at least one %s event in real-postgres log", et)
		}
	}

	// Sanity: at least one payload is TierSensitive (we know we ran
	// literal-bearing INSERTs).
	sawSensitive := false
	for _, p := range payloads {
		if p.Tier() == logparse.TierSensitive {
			sawSensitive = true
			break
		}
	}
	if !sawSensitive {
		t.Error("no TierSensitive payload produced — sensitive payload must be preserved for PII filters")
	}
}
```

- [ ] **Step 2: Run — expect FAIL.**

Run: `go test ./internal/logparse/ -run TestParseStream`
Expected: FAIL — `undefined: logparse.ParseStream`, `undefined: logparse.Options`.

- [ ] **Step 3: Implement ParseStream.**

Create `internal/logparse/framework.go`:

```go
package logparse

import (
	"io"
	"time"
)

// Options configures ParseStream.
type Options struct {
	// Format is the log_destination of the source stream.
	Format Format

	// StderrPrefix configures the stderr parser; ignored when
	// Format == FormatCSV. If empty when needed, defaults to "%m [%p] ".
	StderrPrefix string

	// LoggedAt returns the time the calling collector observed the
	// record. Used so events carry an out-of-band timestamp the
	// server can compare against the in-band OccurredAt to detect
	// clock skew. Defaults to time.Now().UTC.
	LoggedAt func() time.Time
}

// ParseStream consumes r as a Postgres log stream and returns the
// parallel slices (events, payloads). events[i] is the T1 classification
// of the same source record that produced payloads[i]; the two slices
// always have the same length.
//
// Returning two parallel slices — rather than a struct that bundles
// them — is deliberate. It makes the privacy invariant visible at the
// callsite: the T1 path takes events; the T2 path takes payloads. They
// flow through independent pipelines and only the audited T2 path may
// emit payload content off the collector.
func ParseStream(r io.Reader, opts Options) ([]LogEvent, []LogPayload, error) {
	if opts.LoggedAt == nil {
		opts.LoggedAt = func() time.Time { return time.Now().UTC() }
	}
	if opts.Format == FormatStderr && opts.StderrPrefix == "" {
		opts.StderrPrefix = "%m [%p] "
	}

	classifier := NewClassifier(DefaultRules())
	scanner := NewScanner(r, opts.Format)

	var events []LogEvent
	var payloads []LogPayload
	for scanner.Scan() {
		raw := scanner.Text()
		loggedAt := opts.LoggedAt()
		var rec RawRecord
		var err error
		switch opts.Format {
		case FormatCSV:
			rec, err = ParseCSV(raw, loggedAt)
		case FormatStderr:
			rec, err = ParseStderr(raw, loggedAt, StderrConfig{Prefix: opts.StderrPrefix})
		}
		if err != nil {
			// Per privacy invariant: drop unparseable records rather
			// than ship them. The collector will surface a metric so
			// operators can spot parser regressions.
			continue
		}
		ev, payload := classifier.Classify(rec)
		events = append(events, ev)
		payloads = append(payloads, payload)
	}
	if err := scanner.Err(); err != nil {
		return events, payloads, err
	}
	return events, payloads, nil
}
```

- [ ] **Step 4: Run — expect PASS.**

Run: `go test ./internal/logparse/...`
Expected: PASS, including the testcontainers-backed end-to-end (skipped automatically if Docker is unavailable). The test asserts (a) the framework produces events for a real Postgres workload, (b) no canary literal appears in any T1 event field, (c) sensitive content lands in payloads with TierSensitive.

- [ ] **Step 5: Capture golden fixtures.**

Run the end-to-end test in capture mode (one-time) and save snippets of the produced log to `internal/logparse/testdata/`. This makes fast unit-level coverage possible later without spinning up Postgres.

Manual: copy 10–20 representative csv rows from the testcontainers run into `internal/logparse/testdata/csvlog_sample.csv`. Capture an equivalent stderr-format run (rerun the test with `-c log_destination=stderr -c log_line_prefix='%m [%p] '`) and save to `internal/logparse/testdata/stderr_sample.log`.

Add a fast offline test that exercises the fixtures (append to `framework_test.go`):

```go
//go:embed testdata/csvlog_sample.csv
var csvFixture []byte

//go:embed testdata/stderr_sample.log
var stderrFixture []byte

func TestParseStream_offlineFixtures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		data   []byte
		format logparse.Format
	}{
		{"csv", csvFixture, logparse.FormatCSV},
		{"stderr", stderrFixture, logparse.FormatStderr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ev, pl, err := logparse.ParseStream(bytes.NewReader(tc.data), logparse.Options{Format: tc.format})
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if len(ev) == 0 {
				t.Fatal("fixture produced no events")
			}
			classified := 0
			for _, e := range ev {
				if e.EventType != logparse.EventUnclassified {
					classified++
				}
			}
			if classified == 0 {
				t.Error("fixture produced only unclassified events — rules regression?")
			}
			if len(ev) != len(pl) {
				t.Errorf("len(events)=%d len(payloads)=%d", len(ev), len(pl))
			}
		})
	}
}
```

Add the necessary imports (`bytes`, `_ "embed"`) at the top of the test file.

- [ ] **Step 6: Run all logparse tests once more.**

Run: `go test ./internal/logparse/... ./internal/proto/...`
Expected: PASS for all tests (the heavy testcontainers test will skip cleanly when Docker is absent).

- [ ] **Step 7: Commit.**

```bash
git add internal/logparse/framework.go internal/logparse/framework_test.go internal/logparse/testdata/
git commit -m "feat(logparse): ParseStream wiring + real-Postgres integration test with privacy assertions"
```

---

## Self-Review

**1. Spec coverage**

- §2 Privacy guarantee — covered: T1/T2 split lives in three distinct types (`LogEvent`, `LogPayload`, `RawRecord`), the wire contract is constrained by the extended proto test in Task 1, the Go struct invariant by the reflection test in Task 2, and the runtime invariant by the canary assertion in Task 6. Sensitive content stays in `LogPayload` tagged `TierSensitive`.
- §2.2 Filter knobs (`filter_log_secret`, `filter_query_text`) — not implemented here (out of scope for this feature per the user's scope guidance), but the design makes wiring them in obvious: filters run over `[]LogPayload` between ParseStream and any transport.
- features.md §6 "Structured log extraction into classified events" — Tasks 4–6.
- features.md §6 "100+ log event classes/filters" — framework supports trivial extension: add an EventType constant + one Rule + one test case (Task 5 explicitly documents this in the rules.go file comment). Initial 15 cover the highest-priority parity targets.
- features.md §6 "`auto_explain` log integration" — `EventAutoExplainPlan` recognised by `reAutoExplain`, ordered before `EventQueryDuration` to win the regex race (and verified by the auto_explain test case).
- features.md §6 "VACUUM monitoring from logs" — both `EventAutovacuumCompleted` and `EventVacuumCompleted` are in the v1 rules.
- Design §9 testing — no DB mocking: `framework_test.go` runs a real `postgres:16` container with `logging_collector=on`. Postgres extension constraint respected: only `shared_preload_libraries=pg_stat_statements` (which is already used by the existing collector test) is loaded on the monitored DB; nothing is loaded on Lynceus's own DBs. `auto_explain` is loaded on the monitored DB only when that future feature lands — out of scope here.
- RDS-safe: this package introduces no Lynceus-DB schema changes at all, so no extensions are added to the config or stats DB.

**2. Placeholder scan**

Searched for "TBD", "TODO", "implement later", "similar to", "and so on", "add appropriate". None present. Every step has full code or a precise command with expected output. Step 5 of Task 6 (capturing golden fixtures) is the only manual step; it is described concretely (which rows to copy, what command to run, what filename) and produces files exercised by the offline test in the same step.

**3. Type consistency**

- `Severity` enum: used identically in `record.go`, `event.go`, parsers, classifier, and rules.
- `EventType` is a `string` typedef; used as map keys in the e2e test and as struct-field type — consistent.
- `RawRecord` field names (`SessionLine`, `ClientAddr`, `TxnID`, `ContextLine`) are referenced identically by both parsers and the classifier. `LogEvent` uses the protobuf-style field names (`SessionLineNum`, `TransactionID`, `ClientAddrHash`) — distinct deliberately so the wire-layer naming maps 1:1 to the proto.
- `Classify` signature is `(RawRecord) (LogEvent, LogPayload)` everywhere (classifier definition, test cases, ParseStream).
- `Options.LoggedAt` is `func() time.Time` in both the type definition and the test that passes one in.

**Notes on what is intentionally NOT included** (out of scope per the user's directive): log source readers (file tail / directory / S3 / Azure Blob), the PII filter knobs themselves, and the wire-transport integration that ships LogEvents in a Snapshot — those are subsequent features and the framework here is designed so each is a thin wrapper around the types introduced in Tasks 2–6.
