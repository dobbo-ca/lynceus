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
		expr = strings.ReplaceAll(expr, regexp.QuoteMeta(t.tok), t.expr)
	}
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

	first, rest, _ := strings.Cut(line, "\n")
	m := re.FindStringSubmatch(first)
	if m == nil {
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
		LoggedAt:     loggedAt,
		OccurredAt:   parseStderrTimestamp(cap("m"), cap("t")),
		PID:          atoi64(cap("p")),
		AppName:      cap("a"),
		UserName:     cap("u"),
		DatabaseName: cap("d"),
		ClientAddr:   cap("h"),
		Severity:     ParseSeverity(cap("sev")),
		Message:      cap("msg"),
	}
	if rest != "" {
		clean := strings.ReplaceAll(rest, "\n\t", "\n")
		clean = strings.TrimPrefix(clean, "\t")
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
