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
			// Drop unparseable records rather than ship them. The
			// collector will surface a metric so operators can spot
			// parser regressions.
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
