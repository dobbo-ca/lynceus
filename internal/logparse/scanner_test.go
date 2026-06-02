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
