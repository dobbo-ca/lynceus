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
