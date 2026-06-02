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
