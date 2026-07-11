package console

import (
	"strconv"
	"strings"
)

// ConsoleCopyGuardBytes caps the clipboard payload embedded in the page;
// larger results must use CSV.
const ConsoleCopyGuardBytes = 500000

// CopyTSV renders the full result as tab-separated text for clipboard copy,
// or ("", true) when it would exceed the guard.
func CopyTSV(res Result) (string, bool) {
	var b strings.Builder
	b.WriteString(strings.Join(res.Columns, "\t"))
	b.WriteByte('\n')
	for i, row := range res.Rows {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(strings.Join(row, "\t"))
	}
	if b.Len() > ConsoleCopyGuardBytes {
		return "", true
	}
	return b.String(), false
}

// CSV renders the full result as RFC4180 CSV.
func CSV(res Result) string {
	var b strings.Builder
	writeRow := func(cells []string) {
		for i, c := range cells {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(csvField(c))
		}
		b.WriteByte('\n')
	}
	writeRow(res.Columns)
	for _, row := range res.Rows {
		writeRow(row)
	}
	return b.String()
}

func csvField(s string) string {
	if strings.ContainsAny(s, ",\"\n") {
		return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
	}
	return s
}

// SQLInserts renders the full result as one INSERT statement per row.
func SQLInserts(res Result, table string) string {
	cols := strings.Join(res.Columns, ", ")
	var b strings.Builder
	for _, row := range res.Rows {
		b.WriteString("INSERT INTO ")
		b.WriteString(table)
		b.WriteString(" (")
		b.WriteString(cols)
		b.WriteString(") VALUES (")
		for i, c := range row {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(sqlLiteral(c))
		}
		b.WriteString(");\n")
	}
	return b.String()
}

func sqlLiteral(s string) string {
	if s == "never" || s == "" {
		return "NULL"
	}
	if _, err := strconv.ParseInt(strings.ReplaceAll(s, ",", ""), 10, 64); err == nil {
		return strings.ReplaceAll(s, ",", "")
	}
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
