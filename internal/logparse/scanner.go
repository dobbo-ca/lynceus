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
		c.FieldsPerRecord = -1
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
		// parser; keeps the CSV scanner and CSV parser stages composable
		// for tests.
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
	var b strings.Builder
	for {
		peek, _ := s.br.Peek(1)
		if len(peek) == 0 {
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
