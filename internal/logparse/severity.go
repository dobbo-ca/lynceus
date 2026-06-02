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
