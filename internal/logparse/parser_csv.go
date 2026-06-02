package logparse

import (
	"encoding/csv"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// CSV column positions in postgres:16 csvlog (per src/backend/utils/error/elog.c
// write_csvlog). pg16 emits 26 columns; we index the ones we use by name to
// keep the parser readable.
const (
	csvIdxLogTime          = 0
	csvIdxUserName         = 1
	csvIdxDatabase         = 2
	csvIdxPID              = 3
	csvIdxConnFrom         = 4
	csvIdxSessionID        = 5
	csvIdxSessionLineNum   = 6
	csvIdxCommandTag       = 7
	csvIdxSessionStart     = 8
	csvIdxVirtualXID       = 9
	csvIdxTransactionID    = 10
	csvIdxSeverity         = 11
	csvIdxSQLState         = 12
	csvIdxMessage          = 13
	csvIdxDetail           = 14
	csvIdxHint             = 15
	csvIdxInternalQuery    = 16
	csvIdxInternalQueryPos = 17
	csvIdxContext          = 18
	csvIdxQuery            = 19
	csvIdxQueryPos         = 20
	csvIdxLocation         = 21
	csvIdxApplicationName  = 22
	csvIdxBackendType      = 23
	csvIdxLeaderPID        = 24
	csvIdxQueryID          = 25
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
