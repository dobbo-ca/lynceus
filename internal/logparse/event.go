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
	EventType      EventType
	Severity       Severity
	OccurredAt     time.Time
	LoggedAt       time.Time
	PID            int64
	BackendType    string
	DatabaseName   string
	UserName       string
	AppName        string
	ClientAddrHash string
	SQLState       string
	SessionLineNum int64
	TransactionID  int64
}
